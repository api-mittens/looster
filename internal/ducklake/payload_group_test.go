package ducklake

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestPayloadConcurrentPutsGroupCommit(t *testing.T) {
	sink := newMemorySink()
	s := testPayloadStore(t, sink, PayloadOptions{BatchSize: 4, BatchBytes: 1024})
	// Hold the commit lane, not the queue mutex: every caller must be able to
	// fsync and enqueue while another commit is waiting/in flight.
	s.flushMu.Lock()
	const count = 8
	results := make(chan error, count)
	for i := 0; i < count; i++ {
		go func(i int) { results <- s.Put(context.Background(), fmt.Sprintf("group-%d", i), []byte("value")) }(i)
	}
	deadline := time.Now().Add(2 * time.Second)
	queued := 0
	for time.Now().Before(deadline) {
		s.mu.Lock()
		queued = len(s.pending)
		s.mu.Unlock()
		if queued == count {
			break
		}
		time.Sleep(time.Millisecond)
	}
	s.flushMu.Unlock()
	if queued != count {
		t.Fatalf("Put held queue mutex across remote commit, queued=%d", queued)
	}
	for i := 0; i < count; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.batches) != 2 {
		t.Fatalf("wanted two bounded groups, got %d", len(sink.batches))
	}
	for _, batch := range sink.batches {
		if len(batch) != 4 {
			t.Fatalf("group size %d", len(batch))
		}
	}
}
