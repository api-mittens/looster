package cli

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestLazyConcurrentReadsOpenOnce(t *testing.T) {
	var opens atomic.Int32
	backend := &lazyTestPayloads{}
	p := newLazyPayloadStore(func(context.Context) (webPayloadStore, error) {
		opens.Add(1)
		return backend, nil
	})
	var readers sync.WaitGroup
	for i := 0; i < 32; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			if _, err := p.Get(context.Background(), "key"); err != nil {
				t.Error(err)
			}
		}()
	}
	readers.Wait()
	if opens.Load() != 1 {
		t.Fatalf("open count: %d", opens.Load())
	}
	var closers sync.WaitGroup
	for i := 0; i < 8; i++ {
		closers.Add(1)
		go func() {
			defer closers.Done()
			if err := p.Close(); err != nil {
				t.Error(err)
			}
		}()
	}
	closers.Wait()
	if !backend.closed.Load() {
		t.Fatal("store is not closed")
	}
}

func TestLazyCloseBeforeUseDoesNotOpen(t *testing.T) {
	p := newLazyPayloadStore(func(context.Context) (webPayloadStore, error) {
		t.Fatal("close opened the lake")
		return nil, nil
	})
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Put(context.Background(), "key", nil); err == nil {
		t.Fatal("write after close succeeded")
	}
}

func TestLazyCloseCancelsBlockedOperation(t *testing.T) {
	for _, method := range []string{"Get", "Put", "GetMany", "GetCommitted", "GetManyBounded"} {
		t.Run(method, func(t *testing.T) {
			started := make(chan struct{})
			block := func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() }
			backend := &lazyTestPayloads{read: block, put: block, bounded: func(ctx context.Context, _ []string, _ int64) (map[string][]byte, error) { return nil, block(ctx) }}
			p := newLazyPayloadStore(func(context.Context) (webPayloadStore, error) { return backend, nil })
			done, closed := make(chan error, 1), make(chan error, 1)
			go func() {
				var err error
				switch method {
				case "Get":
					_, err = p.Get(context.Background(), "key")
				case "Put":
					err = p.Put(context.Background(), "key", nil)
				case "GetMany":
					_, err = p.GetMany(context.Background(), []string{"key"})
				case "GetCommitted":
					_, err = p.GetCommitted(context.Background(), "key")
				case "GetManyBounded":
					_, err = p.GetManyBounded(context.Background(), []string{"key"}, 8<<20)
				}
				done <- err
			}()
			<-started
			go func() { closed <- p.Close() }()
			if err := waitLazy(t, done); err != context.Canceled {
				t.Fatalf("operation error: %v", err)
			}
			if err := waitLazy(t, closed); err != nil {
				t.Fatal(err)
			}
			if !backend.closed.Load() {
				t.Fatal("store is not closed")
			}
		})
	}
}

func TestLazyBoundedReadForwardsLimit(t *testing.T) {
	const limit int64 = 8 << 20
	rejected := errors.New("read limit exceeded")
	backend := &lazyTestPayloads{bounded: func(_ context.Context, keys []string, maxBytes int64) (map[string][]byte, error) {
		if maxBytes != limit || len(keys) != 1 || keys[0] != "key" {
			t.Fatalf("bounded arguments: %v, %d", keys, maxBytes)
		}
		return nil, rejected
	}}
	p := newLazyPayloadStore(func(context.Context) (webPayloadStore, error) { return backend, nil })
	defer p.Close()
	if rows, err := p.GetManyBounded(context.Background(), []string{"key"}, limit); rows != nil || !errors.Is(err, rejected) {
		t.Fatalf("bounded result: %v, %v", rows, err)
	}
}
