package ducklake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type gatedPayloadSink struct {
	*memoryPayloadSink
	entered chan struct{}
	release chan struct{}
}

func (g *gatedPayloadSink) putBatch(ctx context.Context, records []payloadRecord) error {
	close(g.entered)
	select {
	case <-g.release:
		return g.memoryPayloadSink.putBatch(ctx, records)
	case <-ctx.Done():
		return ctx.Err()
	}
}
func TestPayloadPutWaitsForRemoteCommit(t *testing.T) {
	sink := &gatedPayloadSink{newMemorySink(), make(chan struct{}), make(chan struct{})}
	s := testPayloadStore(t, sink, PayloadOptions{})
	result := make(chan error, 1)
	go func() { result <- s.Put(context.Background(), "key", []byte("remote")) }()
	<-sink.entered
	select {
	case err := <-result:
		t.Fatalf("Put returned before commit: %v", err)
	default:
	}
	if pending, err := s.Get(context.Background(), "key"); err != nil || string(pending) != "remote" {
		t.Fatalf("in-flight overlay read blocked or lost: %q %v", pending, err)
	}
	if _, err := sink.get(context.Background(), "key"); err == nil {
		t.Fatal("remote row exists before commit")
	}
	close(sink.release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if v, err := sink.get(context.Background(), "key"); err != nil || string(v) != "remote" {
		t.Fatalf("Put ACK without remote state: %q %v", v, err)
	}
}
func TestPayloadConcurrentClientStartup(t *testing.T) {
	root := t.TempDir()
	sink := newMemorySink()
	const clients = 8
	results := make(chan *PayloadStore, clients)
	errs := make(chan error, clients)
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := newPayloadStore(sink, PayloadOptions{SpoolDir: root, FlushInterval: time.Hour})
			if err == nil {
				err = s.Put(context.Background(), fmt.Sprintf("client-%d", i), []byte("committed"))
				results <- s
			}
			errs <- err
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	dirs := map[string]bool{}
	for s := range results {
		if dirs[s.dir] {
			t.Error("shared client spool")
		}
		dirs[s.dir] = true
		t.Cleanup(func() { _ = s.Close() })
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(dirs) != clients {
		t.Fatalf("started %d clients", len(dirs))
	}
}
func TestPayloadLargeRecordAndHashValidation(t *testing.T) {
	sink := newMemorySink()
	s := testPayloadStore(t, sink, PayloadOptions{BatchBytes: 1024})
	value := []byte(strings.Repeat("archive", (5<<20)/7+1))
	hash := sha256.Sum256(value)
	key := hex.EncodeToString(hash[:])
	if err := s.Put(context.Background(), key, value); err != nil {
		t.Fatal(err)
	}
	if len(sink.batches) != 1 || len(sink.batches[0]) != 1 {
		t.Fatal("large record was not isolated")
	}
	if got, err := s.GetCommitted(context.Background(), key); err != nil || string(got) != string(value) {
		t.Fatal("large record missing", err)
	}
	if err := s.Put(context.Background(), key, []byte("different")); err != ErrPayloadConflict {
		t.Fatalf("hash content mismatch: %v", err)
	}
}

type countingBatchSink struct {
	*memoryPayloadSink
	batchesRead int
	singleReads int
}

func (s *countingBatchSink) get(ctx context.Context, key string) ([]byte, error) {
	s.singleReads++
	return s.memoryPayloadSink.get(ctx, key)
}
func (s *countingBatchSink) getMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	s.batchesRead++
	rows := map[string][]byte{}
	for _, k := range keys {
		if v, err := s.memoryPayloadSink.get(ctx, k); err == nil {
			rows[k] = v
		}
	}
	return rows, nil
}
func TestPayloadGetManyUsesBatchAndMergesOverlay(t *testing.T) {
	sink := &countingBatchSink{memoryPayloadSink: newMemorySink()}
	s := testPayloadStore(t, sink, PayloadOptions{})
	ctx := context.Background()
	for _, k := range []string{"remote-a", "remote-b"} {
		if err := s.Put(ctx, k, []byte(k)); err != nil {
			t.Fatal(err)
		}
	}
	if sink.singleReads != 0 {
		t.Fatal("Put performed per-record preflight read")
	}
	sink.mu.Lock()
	sink.fail = true
	sink.mu.Unlock()
	if err := s.Put(ctx, "pending", []byte("overlay")); err == nil {
		t.Fatal("uncommitted Put acknowledged")
	}
	sink.singleReads = 0
	sink.batchesRead = 0
	rows, err := s.GetMany(ctx, []string{"remote-a", "remote-b", "remote-a", "pending", "absent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || string(rows["pending"]) != "overlay" {
		t.Fatalf("batch rows %v", rows)
	}
	if sink.batchesRead != 1 || sink.singleReads != 0 {
		t.Fatalf("batch=%d single=%d", sink.batchesRead, sink.singleReads)
	}
	committed, err := s.GetManyCommitted(ctx, []string{"remote-a", "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := committed["pending"]; ok {
		t.Fatal("pending record verified as committed")
	}
}
