package cli

import (
	"context"
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
