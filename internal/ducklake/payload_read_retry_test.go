package ducklake

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestCommittedPayloadReadRetry(t *testing.T) {
	t.Run("success discards partial", func(t *testing.T) {
		calls := 0
		rows, err := retryCommittedPayloadRead(context.Background(), time.Second, time.Millisecond, func(ctx context.Context) (map[string][]byte, error) {
			calls++
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("missing bounded deadline")
			}
			if calls < 3 {
				return map[string][]byte{"partial": []byte("bad")}, fmt.Errorf("safe: %w", ErrCatalogBusy)
			}
			return map[string][]byte{"key": []byte("value")}, nil
		})
		if err != nil || calls != 3 || string(rows["key"]) != "value" || len(rows) != 1 {
			t.Fatalf("calls=%d rows=%v err=%v", calls, rows, err)
		}
	})
	t.Run("permanent no retry", func(t *testing.T) {
		for _, cause := range []error{ErrPayloadConflict, ErrClosed, errors.New("connection_limit CANARY do not parse this")} {
			calls := 0
			_, err := retryCommittedPayloadRead(context.Background(), time.Second, time.Millisecond, func(context.Context) (map[string][]byte, error) { calls++; return nil, cause })
			if err != cause || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		}
	})
	t.Run("bounded exhaustion", func(t *testing.T) {
		calls := 0
		start := time.Now()
		_, err := retryCommittedPayloadRead(context.Background(), 25*time.Millisecond, time.Millisecond, func(context.Context) (map[string][]byte, error) { calls++; return nil, ErrCatalogBusy })
		if !errors.Is(err, ErrCatalogBusy) || calls < 2 || calls > 8 || time.Since(start) > time.Second {
			t.Fatalf("calls=%d elapsed=%v err=%v", calls, time.Since(start), err)
		}
	})
	t.Run("execution included in budget", func(t *testing.T) {
		calls := 0
		_, err := retryCommittedPayloadRead(context.Background(), 25*time.Millisecond, time.Millisecond, func(ctx context.Context) (map[string][]byte, error) {
			calls++
			if calls == 1 {
				return nil, ErrCatalogBusy
			}
			<-ctx.Done()
			return nil, ctx.Err()
		})
		if !errors.Is(err, ErrCatalogBusy) || calls != 2 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	})
	t.Run("caller cancellation interrupts wait", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		_, err := retryCommittedPayloadRead(ctx, time.Second, time.Second, func(context.Context) (map[string][]byte, error) { calls++; cancel(); return nil, ErrCatalogBusy })
		if err != context.Canceled || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	})
	t.Run("caller deadline interrupts wait", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		calls := 0
		_, err := retryCommittedPayloadRead(ctx, time.Second, time.Second, func(context.Context) (map[string][]byte, error) { calls++; return nil, ErrCatalogBusy })
		if err != context.DeadlineExceeded || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	})
	t.Run("already canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := retryCommittedPayloadRead(ctx, time.Second, time.Millisecond, func(context.Context) (map[string][]byte, error) { t.Fatal("read called"); return nil, nil })
		if err != context.Canceled {
			t.Fatal(err)
		}
	})
	t.Run("permanent after busy", func(t *testing.T) {
		calls := 0
		_, err := retryCommittedPayloadRead(context.Background(), time.Second, time.Millisecond, func(context.Context) (map[string][]byte, error) {
			calls++
			if calls == 1 {
				return nil, ErrCatalogBusy
			}
			return nil, ErrPayloadConflict
		})
		if err != ErrPayloadConflict || calls != 2 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	})
}

type busyReadSink struct {
	*memoryPayloadSink
	calls int
}

func (s *busyReadSink) getMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	s.calls++
	if s.calls == 1 {
		return map[string][]byte{"partial": []byte("bad")}, ErrCatalogBusy
	}
	result := map[string][]byte{}
	for _, k := range keys {
		v, err := s.memoryPayloadSink.get(ctx, k)
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
func TestGetManyCommittedRetriesBusyOnly(t *testing.T) {
	sink := &busyReadSink{memoryPayloadSink: newMemorySink()}
	sink.rows["remote"] = []byte("committed")
	s := testPayloadStore(t, sink, PayloadOptions{})
	s.mu.Lock()
	s.pending["remote"] = []byte("pending")
	s.mu.Unlock()
	rows, err := s.GetManyCommitted(context.Background(), []string{"remote", "missing"})
	if err != nil || sink.calls != 2 || len(rows) != 1 || string(rows["remote"]) != "committed" {
		t.Fatalf("calls=%d rows=%v err=%v", sink.calls, rows, err)
	}
	sink.calls = 0
	if _, err = s.GetMany(context.Background(), []string{"missing"}); !errors.Is(err, ErrCatalogBusy) || sink.calls != 1 {
		t.Fatalf("ordinary read retried: calls=%d err=%v", sink.calls, err)
	}
}
