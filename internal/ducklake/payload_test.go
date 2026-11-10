package ducklake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

// Exercise the exact SQL adapter across real, separate DuckDB CLI processes.
// DUCKLAKE_PAYLOAD_TEST_PRELUDE may attach a disposable real DuckLake catalog as
// lake (with data inlining disabled) for the same end-to-end payload assertions.
func TestPayloadSQLCLI(t *testing.T) {
	binary := os.Getenv("DUCKLAKE_PAYLOAD_TEST_BINARY")
	if binary == "" {
		var e error
		binary, e = exec.LookPath("duckdb")
		if e != nil {
			t.Skip("duckdb CLI not on PATH")
		}
	}
	prelude := os.Getenv("DUCKLAKE_PAYLOAD_TEST_PRELUDE")
	if prelude == "" {
		prelude = "ATTACH " + quote(filepath.Join(t.TempDir(), "lake.db")) + " AS lake;"
	}
	var copies int32
	lake := &Lake{binary: binary, prelude: prelude, run: func(ctx context.Context, binary, sql string) error {
		if strings.Contains(sql, "COPY (") {
			atomic.AddInt32(&copies, 1)
		}
		return runCLI(ctx, binary, sql)
	}}
	ctx := context.Background()
	if e := lake.Exec(ctx, "CREATE TABLE lake.main.looster_payloads(payload_key VARCHAR,payload BLOB);"); e != nil {
		t.Fatal(e)
	}
	if os.Getenv("DUCKLAKE_PAYLOAD_TEST_PRELUDE") != "" {
		root := t.TempDir()
		store, err := NewPayloadStore(ctx, lake, PayloadOptions{SpoolDir: root, FlushInterval: time.Hour})
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if err = store.Put(ctx, "store-roundtrip", []byte("durable")); err != nil {
			t.Fatal(err)
		}
		other, err := NewPayloadStore(ctx, lake, PayloadOptions{SpoolDir: root, FlushInterval: time.Hour})
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
	before := atomic.LoadInt32(&copies)
	rows, err := sink.getMany(ctx, []string{key, "empty", "missing", key})
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&copies)-before != 1 || len(rows) != 2 || string(rows[key]) != string(value) {
		t.Fatalf("GetMany COPY calls=%d rows=%d", atomic.LoadInt32(&copies)-before, len(rows))
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
func TestPayloadSQLNoInlining(t *testing.T) {
	var sql string
	lake := &Lake{run: func(_ context.Context, _, q string) error { sql = q; return nil }}
	s, e := NewPayloadStore(context.Background(), lake, PayloadOptions{SpoolDir: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if !strings.Contains(sql, "CREATE TABLE IF NOT EXISTS lake.main.looster_payloads") || !strings.Contains(sql, "data_inlining_row_limit', 0") || !strings.Contains(sql, "SET PARTITIONED BY (bucket(256, payload_key))") {
		t.Fatal("missing real lake table or disabled inline storage")
	}
}
