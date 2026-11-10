package ducklake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var ErrPayloadNotFound = errors.New("DuckLake payload not found")
var ErrPayloadConflict = errors.New("DuckLake payload key has different content")
var ErrPayloadClosed = errors.New("DuckLake payload store closed")

// PayloadOptions controls durable retry storage and bounded lake writes.
// Each client owns a private subdirectory of SpoolDir. BatchBytes bounds normal
// batches, but a larger individual archive is supported as a singleton batch.
type PayloadOptions struct {
	SpoolDir      string
	BatchSize     int
	BatchBytes    int
	FlushInterval time.Duration
}
type payloadRecord struct {
	Key   string
	Value []byte
}
type payloadSink interface {
	get(context.Context, string) ([]byte, error)
	putBatch(context.Context, []payloadRecord) error
}
type payloadBatchReader interface {
	getMany(context.Context, []string) (map[string][]byte, error)
}

// PayloadStore acknowledges Put only after a remote lake transaction commits.
// Local atomic, fsynced spool records retain failed attempts for background
// retry and restart recovery. Failed attempts remain readable in the overlay.
// Separate clients do not share a lifetime lock or reject one another.
type PayloadStore struct {
	mu        sync.Mutex
	flushMu   sync.Mutex
	waiters   map[string][]chan error
	wake      chan struct{}
	closeDone chan struct{}
	sink      payloadSink
	opts      PayloadOptions
	dir       string
	pending   map[string][]byte
	lock      *os.File
	stop      chan struct{}
	done      chan struct{}
	closed    bool
	closeErr  error
}

func NewPayloadStore(ctx context.Context, lake *Lake, opts PayloadOptions) (*PayloadStore, error) {
	if lake == nil {
		return nil, errors.New("DuckLake lake is required")
	}
	if err := lake.Exec(ctx, "CREATE TABLE IF NOT EXISTS lake.main.looster_payloads (payload_key VARCHAR, payload BLOB); CALL lake.set_option('data_inlining_row_limit', 0, table_name => 'looster_payloads'); ALTER TABLE lake.main.looster_payloads SET PARTITIONED BY (bucket(256, payload_key));"); err != nil {
		return nil, err
	}
	return newPayloadStoreContext(ctx, lakePayloadSink{lake}, opts)
}
func newPayloadStore(sink payloadSink, opts PayloadOptions) (*PayloadStore, error) {
	return newPayloadStoreContext(context.Background(), sink, opts)
}
func newPayloadStoreContext(ctx context.Context, sink payloadSink, opts PayloadOptions) (*PayloadStore, error) {
	if opts.SpoolDir == "" {
		return nil, errors.New("DuckLake durable spool directory is required")
	}
	if opts.BatchSize < 0 || opts.BatchBytes < 0 || opts.FlushInterval < 0 {
		return nil, errors.New("invalid DuckLake payload batch options")
	}
	if opts.BatchSize == 0 {
		opts.BatchSize = 128
	}
	if opts.BatchBytes == 0 {
		opts.BatchBytes = 4 << 20
	}
	if opts.FlushInterval == 0 {
		opts.FlushInterval = time.Second
	}
	root, err := filepath.Abs(opts.SpoolDir)
	if err != nil {
		return nil, err
	}
	opts.SpoolDir = root
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	for dir := root; ; dir = filepath.Dir(dir) {
		if err = syncPayloadDir(dir); err != nil {
			return nil, err
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	// A short startup-only lock protects directory creation and recovery. It is
	// released before returning and waits instead of rejecting another client.
	guard, err := os.OpenFile(filepath.Join(root, ".recovery.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer guard.Close()
	for {
		err = lockPayloadFile(guard)
		if err == nil {
			break
		}
		if !payloadLockBusy(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	dir, err := os.MkdirTemp(root, "client-")
	if err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockPayloadFile(lock); err != nil {
		lock.Close()
		return nil, err
	}
	s := &PayloadStore{sink: sink, opts: opts, dir: dir, pending: map[string][]byte{}, waiters: map[string][]chan error{}, wake: make(chan struct{}, 1), closeDone: make(chan struct{}), lock: lock, stop: make(chan struct{}), done: make(chan struct{})}
	if err = syncPayloadDir(root); err == nil {
		err = s.recoverSpools()
	}
	if err != nil {
		lock.Close()
		return nil, err
	}
	go s.loop()
	return s, nil
}

// recoverSpools claims only abandoned clients. Their locks are nonblocking,
// so active CLI/server writers continue independently. Source records are kept
// until the recovered copy and directory entry have both reached stable storage.
func (s *PayloadStore) recoverSpools() error {
	entries, err := os.ReadDir(s.opts.SpoolDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(s.opts.SpoolDir, e.Name())
		if path == s.dir {
			continue
		}
		// Support legacy spool records from the original single-client layout.
		if !e.IsDir() {
			if filepath.Ext(e.Name()) == ".json" {
				if err = s.recoverFile(path, e.Name()); err != nil {
					return err
				}
			}
			continue
		}
		if !strings.HasPrefix(e.Name(), "client-") {
			continue
		}
		lock, openErr := os.OpenFile(filepath.Join(path, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
		if os.IsNotExist(openErr) {
			continue
		}
		if openErr != nil {
			return openErr
		}
		if err = lockPayloadFile(lock); err != nil {
			lock.Close()
			continue
		}
		files, readErr := os.ReadDir(path)
		if readErr == nil {
			for _, f := range files {
				if filepath.Ext(f.Name()) == ".json" {
					if readErr = s.recoverFile(filepath.Join(path, f.Name()), f.Name()); readErr != nil {
						break
					}
				}
			}
		}
		lock.Close()
		if readErr != nil {
			return readErr
		}
		// Empty abandoned directories are harmless. Do not remove them here: a
		// simultaneous recovery may have the lock open on Unix or Windows.
	}
	return syncPayloadDir(s.opts.SpoolDir)
}
func (s *PayloadStore) recoverFile(path, name string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var r payloadRecord
	if err = json.Unmarshal(data, &r); err != nil {
		return err
	}
	if name != payloadName(r.Key) {
		return errors.New("invalid DuckLake spool record")
	}
	if old, ok := s.pending[r.Key]; ok && string(old) != string(r.Value) {
		return ErrPayloadConflict
	}
	if err = s.spool(r.Key, r.Value); err != nil {
		return err
	}
	if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncPayloadDir(filepath.Dir(path))
}
func payloadName(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:]) + ".json"
}
func (s *PayloadStore) syncDir() error { return syncPayloadDir(s.dir) }
func (s *PayloadStore) spool(key string, value []byte) error {
	data, err := json.Marshal(payloadRecord{key, value})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".pending-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, filepath.Join(s.dir, payloadName(key))); err != nil {
		return err
	}
	s.pending[key] = append([]byte{}, value...)
	return s.syncDir()
}
func (s *PayloadStore) Put(ctx context.Context, key string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !utf8.ValidString(key) || strings.IndexByte(key, 0) >= 0 {
		return errors.New("DuckLake payload key must be UTF-8 without NUL")
	}
	if len(key) == 64 {
		if decoded, e := hex.DecodeString(key); e == nil {
			h := sha256.Sum256(value)
			if string(decoded) != string(h[:]) {
				return ErrPayloadConflict
			}
		}
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrPayloadClosed
	}
	if old, ok := s.pending[key]; ok {
		if string(old) != string(value) {
			s.mu.Unlock()
			return ErrPayloadConflict
		}
		if err := s.syncDir(); err != nil {
			s.mu.Unlock()
			return err
		}
	} else if err := s.spool(key, value); err != nil {
		s.mu.Unlock()
		return err
	}
	waiter := make(chan error, 1)
	s.waiters[key] = append(s.waiters[key], waiter)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
	// Spooling is not acknowledgement. Every caller waits for its group commit
	// attempt, while the unlocked queue can collect other concurrent Put calls.
	select {
	case err := <-waiter:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *PayloadStore) Get(ctx context.Context, key string) ([]byte, error) {
	values, err := s.GetMany(ctx, []string{key})
	if err != nil {
		return nil, err
	}
	v, ok := values[key]
	if !ok {
		return nil, ErrPayloadNotFound
	}
	return v, nil
}

// GetMany reads all missing keys with one COPY command, merging pending values.
// Missing keys are omitted from the result. Duplicate requested keys are folded.
func (s *PayloadStore) GetMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrPayloadClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := map[string][]byte{}
	missing := []string{}
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		if v, ok := s.pending[k]; ok {
			result[k] = append([]byte{}, v...)
		} else {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return result, nil
	}
	if batch, ok := s.sink.(payloadBatchReader); ok {
		rows, err := batch.getMany(ctx, missing)
		if err != nil {
			return nil, err
		}
		for k, v := range rows {
			result[k] = v
		}
		return result, nil
	}
	// Only test/custom sinks lack batch support. The production SQL sink always
	// uses a single COPY, regardless of the number of requested references.
	for _, k := range missing {
		v, err := s.sink.get(ctx, k)
		if errors.Is(err, ErrPayloadNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result[k] = v
	}
	return result, nil
}

// GetCommitted bypasses local retry records for remote backfill verification.
func (s *PayloadStore) GetCommitted(ctx context.Context, key string) ([]byte, error) {
	rows, err := s.GetManyCommitted(ctx, []string{key})
	if err != nil {
		return nil, err
	}
	v, ok := rows[key]
	if !ok {
		return nil, ErrPayloadNotFound
	}
	return v, nil
}

// GetManyCommitted verifies remote state in one COPY and never consults pending.
func (s *PayloadStore) GetManyCommitted(ctx context.Context, keys []string) (map[string][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrPayloadClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if batch, ok := s.sink.(payloadBatchReader); ok {
		return batch.getMany(ctx, keys)
	}
	rows := map[string][]byte{}
	for _, k := range keys {
		v, err := s.sink.get(ctx, k)
		if errors.Is(err, ErrPayloadNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		rows[k] = v
	}
	return rows, nil
}
func (s *PayloadStore) Flush(ctx context.Context) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return ErrPayloadClosed
	}
	return s.flush(ctx)
}

// notify requires mu. Buffered waiters allow canceled callers to leave without
// blocking the writer, and every waiter is completed after one commit attempt.
func (s *PayloadStore) notify(key string, err error) {
	for _, ch := range s.waiters[key] {
		ch <- err
	}
	delete(s.waiters, key)
}
func (s *PayloadStore) flush(ctx context.Context) error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	for {
		s.mu.Lock()
		keys := make([]string, 0, len(s.pending))
		for k := range s.pending {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		batch := []payloadRecord{}
		size := 0
		for _, k := range keys {
			v := s.pending[k]
			if len(batch) >= s.opts.BatchSize || (len(batch) > 0 && size+len(k)+len(v) > s.opts.BatchBytes) {
				break
			}
			batch = append(batch, payloadRecord{k, v})
			size += len(k) + len(v)
		}
		s.mu.Unlock()
		if len(batch) == 0 {
			return nil
		}
		err := ctx.Err()
		if err == nil {
			err = s.sink.putBatch(ctx, batch)
		}
		if err != nil {
			// Only an error path reads remote state. Normal Put performs no per-record
			// preflight process. Proven immutable-key conflicts must not poison retries.
			conflicts := map[string]bool{}
			if reader, ok := s.sink.(payloadBatchReader); ok {
				if rows, e := reader.getMany(ctx, keys); e == nil {
					for _, r := range batch {
						if old, ok := rows[r.Key]; ok && string(old) != string(r.Value) {
							conflicts[r.Key] = true
						}
					}
				}
			} else {
				for _, r := range batch {
					if old, e := s.sink.get(ctx, r.Key); e == nil && string(old) != string(r.Value) {
						conflicts[r.Key] = true
					}
				}
			}
			s.mu.Lock()
			for k := range conflicts {
				if e := os.Remove(filepath.Join(s.dir, payloadName(k))); e == nil || os.IsNotExist(e) {
					delete(s.pending, k)
				}
				s.notify(k, ErrPayloadConflict)
			}
			_ = s.syncDir()
			for k := range s.waiters {
				s.notify(k, err)
			}
			s.mu.Unlock()
			return err
		}
		s.mu.Lock()
		// The remote commit is authoritative. Cleanup failures remain retryable and
		// must not acknowledge uncommitted entries outside this successful batch.
		for _, r := range batch {
			if e := os.Remove(filepath.Join(s.dir, payloadName(r.Key))); e != nil && !os.IsNotExist(e) {
				err = e
				break
			}
		}
		if err == nil {
			err = s.syncDir()
		}
		if err == nil {
			for _, r := range batch {
				delete(s.pending, r.Key)
				s.notify(r.Key, nil)
			}
		} else {
			for k := range s.waiters {
				s.notify(k, err)
			}
		}
		s.mu.Unlock()
		if err != nil {
			return err
		}
	}
}
func (s *PayloadStore) loop() {
	defer close(s.done)
	ticker := time.NewTicker(s.opts.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-s.wake:
			// A short group window allows concurrent callers to share one transaction
			// while each caller still waits for its own remote commit acknowledgement.
			timer := time.NewTimer(10 * time.Millisecond)
		collect:
			for {
				select {
				case <-s.stop:
					timer.Stop()
					return
				case <-s.wake:
				case <-timer.C:
					break collect
				}
			}
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = s.Flush(ctx)
		cancel()
	}
}
func (s *PayloadStore) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		<-s.closeDone
		s.mu.Lock()
		err := s.closeErr
		s.mu.Unlock()
		return err
	}
	s.closed = true
	close(s.stop)
	s.mu.Unlock()
	<-s.done
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err := s.flush(ctx)
	cancel()
	if e := s.lock.Close(); err == nil {
		err = e
	}
	s.mu.Lock()
	s.closeErr = err
	close(s.closeDone)
	s.mu.Unlock()
	return err
}
func (s *PayloadStore) String() string { return "ducklake.PayloadStore{values redacted}" }
