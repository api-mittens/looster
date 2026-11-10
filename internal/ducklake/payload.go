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
	"syscall"
	"time"
	"unicode/utf8"
)

var ErrPayloadNotFound = errors.New("DuckLake payload not found")
var ErrPayloadConflict = errors.New("DuckLake payload key has different content")
var ErrPayloadClosed = errors.New("DuckLake payload store closed")

// PayloadOptions controls durable acknowledgement and bounded lake writes.
// SpoolDir must be on a durable local filesystem. All writers to the same
// catalog payload table must share this directory on one host. The file lock
// prevents competing local owners, not independent writers on other hosts.
// BatchBytes includes keys and values and is also the maximum record size.
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

// PayloadStore acknowledges Put only after fsync of both the spool file and its
// directory. Failed lake writes remain readable and are retried on each tick.
// Close attempts a final flush and returns any error, preserving the spool.
type PayloadStore struct {
	mu       sync.Mutex
	sink     payloadSink
	opts     PayloadOptions
	pending  map[string][]byte
	lock     *os.File
	stop     chan struct{}
	done     chan struct{}
	closed   bool
	closeErr error
}

func NewPayloadStore(ctx context.Context, lake *Lake, opts PayloadOptions) (*PayloadStore, error) {
	if lake == nil {
		return nil, errors.New("DuckLake lake is required")
	}
	if err := lake.Exec(ctx, "CREATE TABLE IF NOT EXISTS lake.main.looster_payloads (payload_key VARCHAR, payload BLOB); CALL lake.set_option('data_inlining_row_limit', 0, table_name => 'looster_payloads');"); err != nil {
		return nil, err
	}
	return newPayloadStore(lakePayloadSink{lake}, opts)
}
func newPayloadStore(sink payloadSink, opts PayloadOptions) (*PayloadStore, error) {
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
	if err := os.MkdirAll(opts.SpoolDir, 0700); err != nil {
		return nil, err
	}
	// Persist newly created directory links, including any MkdirAll ancestors,
	// before acknowledging a spool record inside them.
	absolute, err := filepath.Abs(opts.SpoolDir)
	if err != nil {
		return nil, err
	}
	for dir := absolute; ; dir = filepath.Dir(dir) {
		f, e := os.Open(dir)
		if e != nil {
			return nil, e
		}
		e = f.Sync()
		closeErr := f.Close()
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	lock, err := os.OpenFile(filepath.Join(opts.SpoolDir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("DuckLake spool already in use")
	}
	s := &PayloadStore{sink: sink, opts: opts, pending: map[string][]byte{}, lock: lock, stop: make(chan struct{}), done: make(chan struct{})}
	entries, err := os.ReadDir(opts.SpoolDir)
	if err == nil {
		for _, e := range entries {
			if filepath.Ext(e.Name()) != ".json" {
				continue
			}
			var r payloadRecord
			var b []byte
			b, err = os.ReadFile(filepath.Join(opts.SpoolDir, e.Name()))
			if err != nil {
				break
			}
			if err = json.Unmarshal(b, &r); err != nil {
				break
			}
			if e.Name() != payloadName(r.Key) {
				err = errors.New("invalid DuckLake spool record")
				break
			}
			if len(r.Key)+len(r.Value) > opts.BatchBytes {
				err = errors.New("DuckLake spool record exceeds configured batch limit")
				break
			}
			s.pending[r.Key] = r.Value
		}
	}
	if err != nil {
		lock.Close()
		return nil, err
	}
	go s.loop()
	return s, nil
}
func payloadName(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:]) + ".json"
}
func (s *PayloadStore) syncDir() error {
	f, e := os.Open(s.opts.SpoolDir)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func (s *PayloadStore) Put(ctx context.Context, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrPayloadClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !utf8.ValidString(key) || strings.IndexByte(key, 0) >= 0 {
		return errors.New("DuckLake payload key must be UTF-8 without NUL")
	}
	if len(value)+len(key) > s.opts.BatchBytes {
		return errors.New("DuckLake payload exceeds batch byte limit")
	}
	old, ok := s.pending[key]
	if !ok {
		var err error
		old, err = s.sink.get(ctx, key)
		if err == nil {
			ok = true
		} else if !errors.Is(err, ErrPayloadNotFound) {
			return err
		}
	}
	if ok {
		if string(old) != string(value) {
			return ErrPayloadConflict
		}
		return s.syncDir()
	}
	data, err := json.Marshal(payloadRecord{key, value})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.opts.SpoolDir, ".pending-")
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
	if err = os.Rename(name, filepath.Join(s.opts.SpoolDir, payloadName(key))); err != nil {
		return err
	}
	// Keep the overlay even if directory sync fails: retrying Put must not lose it.
	s.pending[key] = append([]byte{}, value...)
	return s.syncDir()
}
func (s *PayloadStore) Get(ctx context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrPayloadClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if v, ok := s.pending[key]; ok {
		return append([]byte{}, v...), nil
	}
	return s.sink.get(ctx, key)
}

// Flush drains the pending spool in bounded transactions. On any error all
// unacknowledged records remain available for retry, including after restart.
func (s *PayloadStore) Flush(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrPayloadClosed
	}
	return s.flush(ctx)
}
func (s *PayloadStore) flush(ctx context.Context) error {
	keys := make([]string, 0, len(s.pending))
	for k := range s.pending {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for len(keys) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch := []payloadRecord{}
		size := 0
		n := 0
		for n < len(keys) && n < s.opts.BatchSize {
			k := keys[n]
			v := s.pending[k]
			if n > 0 && size+len(k)+len(v) > s.opts.BatchBytes {
				break
			}
			batch = append(batch, payloadRecord{k, v})
			size += len(k) + len(v)
			n++
		}
		if err := s.sink.putBatch(ctx, batch); err != nil {
			return err
		}
		for _, r := range batch {
			if err := os.Remove(filepath.Join(s.opts.SpoolDir, payloadName(r.Key))); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err := s.syncDir(); err != nil {
			return err
		}
		for _, r := range batch {
			delete(s.pending, r.Key)
		}
		keys = keys[n:]
	}
	return nil
}
func (s *PayloadStore) loop() {
	defer close(s.done)
	t := time.NewTicker(s.opts.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = s.Flush(ctx)
			cancel()
		}
	}
}
func (s *PayloadStore) Close() error {
	s.mu.Lock()
	if s.closed {
		err := s.closeErr
		s.mu.Unlock()
		return err
	}
	s.closed = true
	close(s.stop)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	s.closeErr = s.flush(ctx)
	cancel()
	if err := s.lock.Close(); s.closeErr == nil {
		s.closeErr = err
	}
	err := s.closeErr
	s.mu.Unlock()
	<-s.done
	return err
}
func (s *PayloadStore) String() string { return "ducklake.PayloadStore{values redacted}" }
