package ducklake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

func TestPayloadDelayedBatchesExceedSharedBudget(t *testing.T) {
	const budget = 500 * time.Millisecond
	const delay = 300 * time.Millisecond
	attempts := 0
	sink := payloadAttemptSink{newMemorySink(), func(ctx context.Context, rows []payloadRecord) error {
		attempts++
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			return ctx.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	s := payloadManualStore(t, sink)
	a := payloadQueueTestRecord(t, s, "a")
	b := payloadQueueTestRecord(t, s, "b")
	start := time.Now()
	if err := s.flushBatches(context.Background(), budget); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed <= budget {
		t.Fatalf("total time %v did not exceed shared budget %v", elapsed, budget)
	}
	waitPayloadResult(t, a)
	waitPayloadResult(t, b)
	if attempts != 2 {
		t.Fatalf("attempts: %d", attempts)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.rows) != 2 {
		t.Fatalf("committed records: %d", len(sink.rows))
	}
}

type payloadErrorReadKeysSink struct {
	payloadAttemptSink
	readKeys []string
}

func (s *payloadErrorReadKeysSink) getMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	s.readKeys = append(s.readKeys, keys...)
	return map[string][]byte{}, nil
}

func TestPayloadCloseFailureReturnsAllWaitersAndKeepsSpool(t *testing.T) {
	failure := errors.New("native close write failure")
	attempts := 0
	sink := &payloadErrorReadKeysSink{payloadAttemptSink: payloadAttemptSink{newMemorySink(), func(context.Context, []payloadRecord) error {
		attempts++
		return failure
	}}}
	s := payloadManualStore(t, sink)
	// Supply the close resources without a background loop. Only Close can flush.
	s.done = make(chan struct{})
	close(s.done)
	s.closeDone = make(chan struct{})
	lock, err := os.OpenFile(filepath.Join(s.dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	s.lock = lock
	t.Cleanup(func() { _ = lock.Close() })
	waiters := map[string]<-chan error{}
	for _, key := range []string{"a", "b", "c"} {
		waiters[key] = payloadQueueTestRecord(t, s, key)
	}
	if err := s.Close(); !errors.Is(err, failure) {
		t.Fatalf("Close error: %v", err)
	}
	for key, waiter := range waiters {
		expected := ErrPayloadClosed
		if key == "a" {
			expected = failure
		}
		select {
		case err := <-waiter:
			if !errors.Is(err, expected) {
				t.Fatalf("waiter %s: %v", key, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("waiter %s did not return", key)
		}
		data, err := os.ReadFile(filepath.Join(s.dir, payloadName(key)))
		if err != nil {
			t.Fatal(err)
		}
		var record payloadRecord
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		if record.Key != key || string(record.Value) != key {
			t.Fatalf("spool record changed: %s", key)
		}
	}
	if attempts != 1 {
		t.Fatalf("attempts: %d", attempts)
	}
	if len(sink.readKeys) != 1 || sink.readKeys[0] != "a" {
		t.Fatalf("error read keys: %v", sink.readKeys)
	}
	if len(s.waiters) != 0 {
		t.Fatalf("waiters remain: %d", len(s.waiters))
	}
	if err := s.Close(); !errors.Is(err, failure) {
		t.Fatalf("second Close error: %v", err)
	}
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
