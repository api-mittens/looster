package cli

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/app"
)

type lazyTestPayloads struct {
	put    func(context.Context) error
	closed atomic.Bool
}

func (s *lazyTestPayloads) Put(ctx context.Context, _ string, _ []byte) error {
	if s.put != nil {
		return s.put(ctx)
	}
	return nil
}
func (s *lazyTestPayloads) Get(context.Context, string) ([]byte, error) { return []byte("read"), nil }
func (s *lazyTestPayloads) GetCommitted(context.Context, string) ([]byte, error) {
	return []byte("committed"), nil
}
func (s *lazyTestPayloads) GetMany(context.Context, []string) (map[string][]byte, error) {
	return map[string][]byte{"key": []byte("read")}, nil
}
func (s *lazyTestPayloads) Close() error { s.closed.Store(true); return nil }

func waitLazy(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("operation did not stop")
		return nil
	}
}
func TestLazyPayloadRetryAndReadMethods(t *testing.T) {
	calls := 0
	fail := errors.New("open failed")
	backend := &lazyTestPayloads{}
	p := newLazyPayloadStore(func(context.Context) (webPayloadStore, error) {
		calls++
		if calls == 1 {
			return nil, fail
		}
		return backend, nil
	})
	defer p.Close()
	if calls != 0 {
		t.Fatal("startup opened the lake")
	}
	if _, err := p.Get(context.Background(), "key"); !errors.Is(err, fail) {
		t.Fatal(err)
	}
	if got, err := p.GetCommitted(context.Background(), "key"); err != nil || string(got) != "committed" {
		t.Fatalf("committed read: %q, %v", got, err)
	}
	if got, err := p.GetMany(context.Background(), []string{"key"}); err != nil || string(got["key"]) != "read" {
		t.Fatalf("batch read: %v, %v", got, err)
	}
	if _, err := p.Get(context.Background(), "key"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("open count: %d", calls)
	}
}
func TestLazyBlockedOpenDoesNotBlockNamespaceMetadata(t *testing.T) {
	started := make(chan struct{})
	p := newLazyPayloadStore(func(ctx context.Context) (webPayloadStore, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	done := make(chan error, 1)
	go func() { _, err := p.Get(context.Background(), "key"); done <- err }()
	<-started
	provider := &webNamespaces{repos: map[string]app.Repository{"test": nil}}
	if _, err := provider.Repository(context.Background(), "test"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Get(ctx, "key"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := waitLazy(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := p.Get(context.Background(), "key"); err == nil {
		t.Fatal("read after close succeeded")
	}
}
func TestLazyPutWaitsForCommitAndCloseWaitsForPut(t *testing.T) {
	started, commit := make(chan struct{}), make(chan struct{})
	backend := &lazyTestPayloads{put: func(context.Context) error { close(started); <-commit; return nil }}
	p := newLazyPayloadStore(func(context.Context) (webPayloadStore, error) { return backend, nil })
	written, closed := make(chan error, 1), make(chan error, 1)
	go func() { written <- p.Put(context.Background(), "key", []byte("value")) }()
	<-started
	go func() { closed <- p.Close() }()
	select {
	case <-written:
		t.Fatal("write returned before commit")
	default:
	}
	if backend.closed.Load() {
		t.Fatal("store closed during write")
	}
	close(commit)
	if err := waitLazy(t, written); err != nil {
		t.Fatal(err)
	}
	if err := waitLazy(t, closed); err != nil {
		t.Fatal(err)
	}
	if !backend.closed.Load() {
		t.Fatal("store is not closed")
	}
}
func TestLazyOpenCancellationCanRetry(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	p := newLazyPayloadStore(func(ctx context.Context) (webPayloadStore, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return &lazyTestPayloads{}, nil
	})
	defer p.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := p.Get(ctx, "key"); done <- err }()
	<-started
	cancel()
	if err := waitLazy(t, done); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := p.Put(context.Background(), "key", nil); err != nil {
		t.Fatal(err)
	}
}
