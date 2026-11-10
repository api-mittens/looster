package ducklake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryPayloadSink struct {
	mu      sync.Mutex
	rows    map[string][]byte
	fail    bool
	batches [][]payloadRecord
}

func (m *memoryPayloadSink) get(ctx context.Context, k string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.rows[k]
	if !ok {
		return nil, ErrPayloadNotFound
	}
	return append([]byte{}, v...), nil
}
func (m *memoryPayloadSink) putBatch(ctx context.Context, b []payloadRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("injected failure")
	}
	for _, r := range b {
		if v, ok := m.rows[r.Key]; ok && string(v) != string(r.Value) {
			return ErrPayloadConflict
		}
	}
	for _, r := range b {
		m.rows[r.Key] = append([]byte{}, r.Value...)
	}
	m.batches = append(m.batches, b)
	return nil
}
func newMemorySink() *memoryPayloadSink { return &memoryPayloadSink{rows: map[string][]byte{}} }
func testPayloadStore(t *testing.T, m payloadSink, o PayloadOptions) *PayloadStore {
	t.Helper()
	if o.SpoolDir == "" {
		o.SpoolDir = t.TempDir()
	}
	if o.FlushInterval == 0 {
		o.FlushInterval = time.Hour
	}
	s, e := newPayloadStore(m, o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func TestPayloadDurableRestartRetry(t *testing.T) {
	ctx := context.Background()
	m := newMemorySink()
	m.fail = true
	dir := t.TempDir()
	s := testPayloadStore(t, m, PayloadOptions{SpoolDir: dir})
	value := []byte{0, 1, 255}
	if e := s.Put(ctx, "a/'key", value); e == nil {
		t.Fatal("Put acknowledged uncommitted spool")
	}
	value[0] = 99
	got, e := s.Get(ctx, "a/'key")
	if e != nil || got[0] != 0 {
		t.Fatalf("pending read %v %v", got, e)
	}
	if _, e := s.GetCommitted(ctx, "a/'key"); !errors.Is(e, ErrPayloadNotFound) {
		t.Fatalf("pending record treated as remotely committed: %v", e)
	}
	got[0] = 55
	if e = s.Flush(ctx); e == nil {
		t.Fatal("expected injected failure")
	}
	if e = s.Close(); e == nil {
		t.Fatal("close must report failed drain")
	}
	s = testPayloadStore(t, m, PayloadOptions{SpoolDir: dir})
	got, e = s.Get(ctx, "a/'key")
	if e != nil || got[0] != 0 {
		t.Fatalf("restart read %v %v", got, e)
	}
	m.mu.Lock()
	m.fail = false
	m.mu.Unlock()
	if e = s.Flush(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(s.dir, payloadName("a/'key"))); !os.IsNotExist(e) {
		t.Fatalf("spool was not acknowledged: %v", e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	s = testPayloadStore(t, m, PayloadOptions{SpoolDir: dir})
	got, e = s.Get(ctx, "a/'key")
	if e != nil || got[0] != 0 {
		t.Fatalf("durable read %v %v", got, e)
	}
}
func TestPayloadIdempotenceConflictAndBounds(t *testing.T) {
	ctx := context.Background()
	m := newMemorySink()
	m.fail = true
	s := testPayloadStore(t, m, PayloadOptions{BatchSize: 2, BatchBytes: 8})
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		if e := s.Put(ctx, k, []byte("123")); e == nil {
			t.Fatal("Put acknowledged failed remote write")
		}
	}
	if e := s.Put(ctx, "a", []byte("123")); e == nil {
		t.Fatal("pending retry acknowledged without remote commit")
	}
	if e := s.Put(ctx, "a", []byte("456")); !errors.Is(e, ErrPayloadConflict) {
		t.Fatalf("pending conflict: %v", e)
	}
	m.mu.Lock()
	m.fail = false
	m.mu.Unlock()
	if e := s.Flush(ctx); e != nil {
		t.Fatal(e)
	}
	if len(m.batches) != 3 {
		t.Fatalf("batches: %d", len(m.batches))
	}
	for _, batch := range m.batches {
		n := 0
		for _, r := range batch {
			n += len(r.Key) + len(r.Value)
		}
		if n > 8 || len(batch) > 2 {
			t.Fatal("unbounded batch")
		}
	}
	if e := s.Put(ctx, "a", []byte("456")); !errors.Is(e, ErrPayloadConflict) {
		t.Fatalf("persisted conflict: %v", e)
	}
	if _, e := s.Get(ctx, "missing"); !errors.Is(e, ErrPayloadNotFound) {
		t.Fatalf("missing: %v", e)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if e := s.Put(canceled, "z", nil); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	if e := s.Put(ctx, "z", nil); !errors.Is(e, ErrPayloadClosed) {
		t.Fatal(e)
	}
}
func TestPayloadSpoolOwnershipAndCorruption(t *testing.T) {
	m := newMemorySink()
	dir := t.TempDir()
	s := testPayloadStore(t, m, PayloadOptions{SpoolDir: dir})
	other := testPayloadStore(t, m, PayloadOptions{SpoolDir: dir})
	if other.dir == s.dir {
		t.Fatal("clients share mutable spool directory")
	}
	if e := other.Put(context.Background(), "other", []byte("value")); e != nil {
		t.Fatal(e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "broken.json"), []byte("not json"), 0600); e != nil {
		t.Fatal(e)
	}
	if other, e := newPayloadStore(m, PayloadOptions{SpoolDir: dir}); e == nil {
		other.Close()
		t.Fatal("corruption ignored")
	}
}
func TestPayloadBackgroundRetry(t *testing.T) {
	m := newMemorySink()
	m.fail = true
	s := testPayloadStore(t, m, PayloadOptions{FlushInterval: time.Millisecond * 5})
	ctx := context.Background()
	if e := s.Put(ctx, "retry", []byte("ok")); e == nil {
		t.Fatal("Put acknowledged failed remote write")
	}
	time.Sleep(20 * time.Millisecond)
	m.mu.Lock()
	m.fail = false
	m.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		_, ok := m.rows["retry"]
		m.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond * 5)
	}
	t.Fatal("background retry did not persist")
}

// Exercise the production SQL adapter on the embedded engine without extensions,
// subprocesses, PATH lookup, or external services.
func TestPayloadSQLEmbedded(t *testing.T) {
	lake, err := OpenEngine(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lake.Close()
	ctx := context.Background()
	if err := lake.Exec(ctx, "ATTACH ':memory:' AS lake"); err != nil {
		t.Fatal(err)
	}
	if e := lake.Exec(ctx, "CREATE TABLE lake.main.looster_payloads(payload_key VARCHAR,payload BLOB);"); e != nil {
		t.Fatal(e)
	}
	{
		root := t.TempDir()
		store, err := newPayloadStore(lakePayloadSink{lake}, PayloadOptions{SpoolDir: root, FlushInterval: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if err = store.Put(ctx, "store-roundtrip", []byte("durable")); err != nil {
			t.Fatal(err)
		}
		other, err := newPayloadStore(lakePayloadSink{lake}, PayloadOptions{SpoolDir: root, FlushInterval: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		sharedValue := []byte("same cross-client immutable payload")
		digest := sha256.Sum256(sharedValue)
		sharedKey := hex.EncodeToString(digest[:])
		errs := make(chan error, 2)
		for _, client := range []*PayloadStore{store, other} {
			go func(client *PayloadStore) {
				err := client.Put(ctx, sharedKey, sharedValue)
				if err != nil {
					err = client.Put(ctx, sharedKey, sharedValue)
				}
				errs <- err
			}(client)
		}
		for i := 0; i < 2; i++ {
			if err := <-errs; err != nil {
				t.Fatal("concurrent independent client write:", err)
			}
		}
		if rows, err := other.GetManyCommitted(ctx, []string{sharedKey}); err != nil || len(rows) != 1 || string(rows[sharedKey]) != string(sharedValue) {
			t.Fatalf("logical concurrent idempotence: rows=%d err=%v", len(rows), err)
		}
		if v, err := other.GetCommitted(ctx, "store-roundtrip"); err != nil || string(v) != "durable" {
			t.Fatalf("remote ACK not visible to independent client: %q %v", v, err)
		}
		large := []byte(strings.Repeat("L", 5<<20))
		if err = store.Put(ctx, "large-archive", large); err != nil {
			t.Fatal(err)
		}
		if v, err := other.GetCommitted(ctx, "large-archive"); err != nil || string(v) != string(large) {
			t.Fatalf("large SQL archive: len=%d err=%v", len(v), err)
		}
		if err = store.Flush(ctx); err != nil {
			t.Fatal(err)
		}
		if v, err := store.Get(ctx, "store-roundtrip"); err != nil || string(v) != "durable" {
			t.Fatalf("store roundtrip %q %v", v, err)
		}
	}
	sink := lakePayloadSink{lake}
	key := "quoted'key\nwith newline"
	value := []byte{0, 255, 10, 13, 39}
	records := []payloadRecord{{key, value}, {"empty", []byte{}}}
	if e := sink.putBatch(ctx, records); e != nil {
		t.Fatal(e)
	}
	if e := sink.putBatch(ctx, records); e != nil {
		t.Fatal("idempotent retry:", e)
	}
	rows, err := sink.getMany(ctx, []string{key, "empty", "missing", key})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || string(rows[key]) != string(value) {
		t.Fatalf("GetMany rows=%d", len(rows))
	}
	for _, r := range records {
		v, e := sink.get(ctx, r.Key)
		if e != nil || string(v) != string(r.Value) {
			t.Fatalf("round trip %q: %x %v", r.Key, v, e)
		}
	}
	if _, e := sink.get(ctx, "missing"); !errors.Is(e, ErrPayloadNotFound) {
		t.Fatal(e)
	}
	if e := sink.putBatch(ctx, []payloadRecord{{key, []byte("conflict")}, {"rollback", []byte("absent")}}); e == nil {
		t.Fatal("conflicting content accepted")
	}
	if _, e := sink.get(ctx, "rollback"); !errors.Is(e, ErrPayloadNotFound) {
		t.Fatalf("transaction did not roll back: %v", e)
	}
	v, e := sink.get(ctx, key)
	if e != nil || string(v) != string(value) {
		t.Fatal("conflict replaced content", e)
	}
}
func TestPayloadStoreClosesOwnedEngine(t *testing.T) {
	ctx := context.Background()
	lake, err := OpenEngine(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lake.Close()
	if err = lake.Exec(ctx, "ATTACH ':memory:' AS lake; CREATE TABLE lake.main.looster_payloads(payload_key VARCHAR,payload BLOB)"); err != nil {
		t.Fatal(err)
	}
	s, err := newPayloadStore(lakePayloadSink{lake}, PayloadOptions{SpoolDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	s.ownedLake = lake
	if err = s.Put(ctx, "owned", []byte("committed")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := lake.Exec(ctx, "SELECT 1"); !errors.Is(err, ErrClosed) {
		t.Fatalf("engine remains open: %v", err)
	}
}
