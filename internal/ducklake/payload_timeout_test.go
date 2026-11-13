package ducklake

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

type payloadAttemptSink struct {
	*memoryPayloadSink
	attempt func(context.Context, []payloadRecord) error
}

func (s payloadAttemptSink) putBatch(ctx context.Context, rows []payloadRecord) error {
	if err := s.attempt(ctx, rows); err != nil {
		return err
	}
	return s.memoryPayloadSink.putBatch(ctx, rows)
}

// This store has no loop. Each test starts the required flush attempts.
func payloadManualStore(t *testing.T, sink payloadSink) *PayloadStore {
	t.Helper()
	return &PayloadStore{
		sink: sink, opts: PayloadOptions{BatchSize: 1, BatchBytes: 1024},
		dir: t.TempDir(), pending: map[string][]byte{},
		waiters: map[string][]chan error{}, wake: make(chan struct{}, 1), stop: make(chan struct{}),
	}
}

func payloadQueueTestRecord(t *testing.T, s *PayloadStore, key string) <-chan error {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.spool(key, []byte(key)); err != nil {
		t.Fatal(err)
	}
	waiter := make(chan error, 1)
	s.waiters[key] = append(s.waiters[key], waiter)
	return waiter
}

func payloadAssertWaiting(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		t.Fatalf("caller returned before its commit attempt completed: %v", err)
	default:
	}
}

func TestPayloadBackgroundBatchHasNewDeadline(t *testing.T) {
	var previous context.Context
	attempts := 0
	sink := payloadAttemptSink{newMemorySink(), func(ctx context.Context, rows []payloadRecord) error {
		attempts++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("batch has no deadline")
		}
		if ctx.Err() != nil {
			t.Fatal("new batch context is not live")
		}
		if previous != nil {
			if previous == ctx {
				t.Fatal("batches share a context")
			}
			if !errors.Is(previous.Err(), context.Canceled) {
				t.Fatal("previous batch context was not released")
			}
		}
		previous = ctx
		return nil
	}}
	s := payloadManualStore(t, sink)
	a := payloadQueueTestRecord(t, s, "a")
	b := payloadQueueTestRecord(t, s, "b")
	if err := s.flushBatches(context.Background(), time.Minute); err != nil {
		t.Fatal(err)
	}
	waitPayloadResult(t, a)
	waitPayloadResult(t, b)
	if attempts != 2 {
		t.Fatalf("attempts: %d", attempts)
	}
}

func TestPayloadBackgroundDeadlineKeepsWaitersForRetry(t *testing.T) {
	var attempts atomic.Int32
	retryStarted := make(chan struct{})
	release := make(chan struct{})
	sink := payloadAttemptSink{newMemorySink(), func(ctx context.Context, rows []payloadRecord) error {
		if attempts.Add(1) == 1 {
			return fmt.Errorf("batch time limit: %w", context.DeadlineExceeded)
		}
		close(retryStarted)
		<-release
		return nil
	}}
	s := testPayloadStore(t, sink, PayloadOptions{FlushInterval: time.Millisecond})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	result := make(chan error, 1)
	go func() { result <- s.Put(context.Background(), "a", []byte("a")) }()
	waitPayloadSignal(t, retryStarted)
	payloadAssertWaiting(t, result)
	close(release)
	waitPayloadResult(t, result)
	if attempts.Load() != 2 {
		t.Fatalf("attempts: %d", attempts.Load())
	}
}

func TestPayloadBatchErrorDoesNotNotifyUnattempted(t *testing.T) {
	failure := errors.New("native write failure")
	sink := payloadAttemptSink{newMemorySink(), func(context.Context, []payloadRecord) error { return failure }}
	s := payloadManualStore(t, sink)
	a := payloadQueueTestRecord(t, s, "a")
	b := payloadQueueTestRecord(t, s, "b")
	if err := s.Flush(context.Background()); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := <-a; !errors.Is(err, failure) {
		t.Fatal(err)
	}
	payloadAssertWaiting(t, b)
	s.sink = newMemorySink()
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitPayloadResult(t, b)
}

func TestPayloadBackgroundNativeErrorAllowsLaterBatch(t *testing.T) {
	failure := errors.New("native write failure")
	attempts := 0
	sink := payloadAttemptSink{newMemorySink(), func(ctx context.Context, rows []payloadRecord) error {
		attempts++
		if rows[0].Key == "a" {
			return failure
		}
		return nil
	}}
	s := payloadManualStore(t, sink)
	a := payloadQueueTestRecord(t, s, "a")
	b := payloadQueueTestRecord(t, s, "b")
	if err := s.flushBatches(context.Background(), time.Minute); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := <-a; !errors.Is(err, failure) {
		t.Fatal(err)
	}
	waitPayloadResult(t, b)
	if attempts != 2 {
		t.Fatalf("attempts: %d", attempts)
	}
}

func TestPayloadBackgroundDeadlineAllowsLaterBatch(t *testing.T) {
	attempts := 0
	sink := payloadAttemptSink{newMemorySink(), func(ctx context.Context, rows []payloadRecord) error {
		attempts++
		if rows[0].Key == "a" {
			return context.DeadlineExceeded
		}
		return nil
	}}
	s := payloadManualStore(t, sink)
	a := payloadQueueTestRecord(t, s, "a")
	b := payloadQueueTestRecord(t, s, "b")
	if err := s.flushBatches(context.Background(), time.Minute); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	payloadAssertWaiting(t, a)
	waitPayloadResult(t, b)
	if attempts != 2 {
		t.Fatalf("attempts: %d", attempts)
	}
	s.sink = newMemorySink()
	if err := s.flushBatches(context.Background(), time.Minute); err != nil {
		t.Fatal(err)
	}
	waitPayloadResult(t, a)
}

func TestPayloadCanceledFlushDoesNotNotifyUnattempted(t *testing.T) {
	s := payloadManualStore(t, newMemorySink())
	a := payloadQueueTestRecord(t, s, "a")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Flush(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	payloadAssertWaiting(t, a)
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitPayloadResult(t, a)
}

func TestPayloadExplicitFlushReturnsDeadline(t *testing.T) {
	sink := payloadAttemptSink{newMemorySink(), func(context.Context, []payloadRecord) error { return context.DeadlineExceeded }}
	s := payloadManualStore(t, sink)
	a := payloadQueueTestRecord(t, s, "a")
	b := payloadQueueTestRecord(t, s, "b")
	if err := s.Flush(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if err := <-a; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	payloadAssertWaiting(t, b)
}

func TestPayloadCallerCancelDoesNotWaitForBatch(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	sink := payloadAttemptSink{newMemorySink(), func(context.Context, []payloadRecord) error {
		close(started)
		<-release
		return nil
	}}
	s := testPayloadStore(t, sink, PayloadOptions{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- s.Put(ctx, "a", []byte("a")) }()
	waitPayloadSignal(t, started)
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Put did not return after caller cancellation")
	}
	close(release)
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}
