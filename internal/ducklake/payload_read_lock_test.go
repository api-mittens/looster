package ducklake

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type blockedPayloadReadSink struct {
	*memoryPayloadSink
	readStarted  chan struct{}
	readRelease  chan struct{}
	writeStarted chan struct{}
	writeRelease chan struct{}
}

func (s *blockedPayloadReadSink) get(ctx context.Context, key string) ([]byte, error) {
	close(s.readStarted)
	select {
	case <-s.readRelease:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.memoryPayloadSink.get(ctx, key)
}

func (s *blockedPayloadReadSink) putBatch(ctx context.Context, rows []payloadRecord) error {
	close(s.writeStarted)
	select {
	case <-s.writeRelease:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.memoryPayloadSink.putBatch(ctx, rows)
}

type blockedPayloadBatchReadSink struct{ *blockedPayloadReadSink }

func (s blockedPayloadBatchReadSink) getMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	rows := map[string][]byte{}
	for _, key := range keys {
		value, err := s.get(ctx, key)
		if errors.Is(err, ErrPayloadNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		rows[key] = value
	}
	return rows, nil
}

func waitPayloadSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not reach the test gate")
	}
}

func waitPayloadResult(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not complete")
	}
}

func TestPayloadBlockedReadAllowsCommitAndSafeClose(t *testing.T) {
	for _, batch := range []bool{false, true} {
		for _, committed := range []bool{false, true} {
			name := "single"
			if batch {
				name = "batch"
			}
			if committed {
				name += "/committed"
			} else {
				name += "/overlay"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				sink := &blockedPayloadReadSink{
					memoryPayloadSink: newMemorySink(),
					readStarted:       make(chan struct{}), readRelease: make(chan struct{}),
					writeStarted: make(chan struct{}), writeRelease: make(chan struct{}),
				}
				var readOnce, writeOnce sync.Once
				releaseRead := func() { readOnce.Do(func() { close(sink.readRelease) }) }
				releaseWrite := func() { writeOnce.Do(func() { close(sink.writeRelease) }) }
				var remote payloadSink = sink
				if batch {
					remote = blockedPayloadBatchReadSink{sink}
				}
				s := testPayloadStore(t, remote, PayloadOptions{})
				// Release both gates before store cleanup, also when a check fails.
				t.Cleanup(releaseRead)
				t.Cleanup(releaseWrite)
				lake, err := OpenEngine(ctx)
				if err != nil {
					t.Fatal(err)
				}
				s.ownedLake = lake
				readResult := make(chan error, 1)
				go func() {
					var err error
					if committed {
						_, err = s.GetManyCommitted(ctx, []string{"remote"})
					} else {
						_, err = s.GetMany(ctx, []string{"remote"})
					}
					readResult <- err
				}()
				waitPayloadSignal(t, sink.readStarted)
				putResult := make(chan error, 1)
				go func() { putResult <- s.Put(ctx, "new", []byte("value")) }()
				waitPayloadSignal(t, sink.writeStarted)
				select {
				case err := <-putResult:
					t.Fatalf("Put returned before remote commit: %v", err)
				default:
				}
				flushResult := make(chan error, 1)
				go func() { flushResult <- s.Flush(ctx) }()
				releaseWrite()
				waitPayloadResult(t, putResult)
				waitPayloadResult(t, flushResult)
				sink.mu.Lock()
				value := string(sink.rows["new"])
				sink.mu.Unlock()
				if value != "value" {
					t.Fatal("remote commit is absent")
				}
				closeResults := make(chan error, 2)
				go func() { closeResults <- s.Close() }()
				// A rejected read shows that Close has set closed and released mu.
				deadline := time.Now().Add(5 * time.Second)
				for {
					_, err := s.GetMany(ctx, nil)
					if errors.Is(err, ErrPayloadClosed) {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("Close did not reject new reads")
					}
					time.Sleep(time.Millisecond)
				}
				go func() { closeResults <- s.Close() }()
				if _, err := s.GetManyCommitted(ctx, nil); !errors.Is(err, ErrPayloadClosed) {
					t.Fatalf("closed read: %v", err)
				}
				select {
				case err := <-closeResults:
					t.Fatalf("Close returned during a remote read: %v", err)
				default:
				}
				if err := lake.Exec(ctx, "SELECT 1"); err != nil {
					t.Fatalf("engine closed during a remote read: %v", err)
				}
				releaseRead()
				waitPayloadResult(t, readResult)
				waitPayloadResult(t, closeResults)
				waitPayloadResult(t, closeResults)
				if err := lake.Exec(ctx, "SELECT 1"); !errors.Is(err, ErrClosed) {
					t.Fatalf("engine did not close: %v", err)
				}
			})
		}
	}
}
