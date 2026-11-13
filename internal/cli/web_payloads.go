package cli

import (
	"context"
	"errors"
	"sync"

	"github.com/api-mittens/looster/internal/postgres"
)

type webPayloadStore interface {
	postgres.PayloadStore
	GetMany(context.Context, []string) (map[string][]byte, error)
	GetManyBounded(context.Context, []string, int64) (map[string][]byte, error)
	GetCommitted(context.Context, string) ([]byte, error)
	Close() error
}

// lazyPayloadStore keeps one adapter in the PostgreSQL store for its full life.
// The gate protects startup, payload operations, and Close. Metadata does not use it.
type lazyPayloadStore struct {
	gate     chan struct{}
	life     context.Context
	cancel   context.CancelFunc
	open     func(context.Context) (webPayloadStore, error)
	store    webPayloadStore
	once     sync.Once
	closeErr error
}

func newLazyPayloadStore(open func(context.Context) (webPayloadStore, error)) *lazyPayloadStore {
	ctx, cancel := context.WithCancel(context.Background())
	return &lazyPayloadStore{gate: make(chan struct{}, 1), life: ctx, cancel: cancel, open: open}
}

func (p *lazyPayloadStore) acquire(ctx context.Context) (webPayloadStore, func(), error) {
	select {
	case p.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-p.life.Done():
		return nil, nil, errors.New("payload store is closed")
	}
	release := func() { <-p.gate }
	if err := ctx.Err(); err != nil {
		release()
		return nil, nil, err
	}
	if p.life.Err() != nil {
		release()
		return nil, nil, errors.New("payload store is closed")
	}
	if p.store == nil {
		store, err := p.open(ctx)
		if err != nil {
			release()
			return nil, nil, err
		}
		p.store = store
		if p.life.Err() != nil {
			release()
			return nil, nil, errors.New("payload store is closed")
		}
	}
	return p.store, release, nil
}
func (p *lazyPayloadStore) Put(ctx context.Context, key string, value []byte) error {
	ctx, cancel := p.operationContext(ctx)
	defer cancel()
	s, release, err := p.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	return s.Put(ctx, key, value)
}
func (p *lazyPayloadStore) Get(ctx context.Context, key string) ([]byte, error) {
	ctx, cancel := p.operationContext(ctx)
	defer cancel()
	s, release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.Get(ctx, key)
}
func (p *lazyPayloadStore) GetMany(ctx context.Context, keys []string) (map[string][]byte, error) {
	ctx, cancel := p.operationContext(ctx)
	defer cancel()
	s, release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.GetMany(ctx, keys)
}
func (p *lazyPayloadStore) GetCommitted(ctx context.Context, key string) ([]byte, error) {
	ctx, cancel := p.operationContext(ctx)
	defer cancel()
	s, release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.GetCommitted(ctx, key)
}
func (p *lazyPayloadStore) operationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(p.life, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func (p *lazyPayloadStore) GetManyBounded(ctx context.Context, keys []string, maxBytes int64) (map[string][]byte, error) {
	ctx, cancel := p.operationContext(ctx)
	defer cancel()
	s, release, err := p.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.GetManyBounded(ctx, keys, maxBytes)
}

func (p *lazyPayloadStore) Close() error {
	p.cancel()
	p.once.Do(func() {
		p.gate <- struct{}{}
		defer func() { <-p.gate }()
		if p.store != nil {
			p.closeErr = p.store.Close()
		}
	})
	return p.closeErr
}

func (a *application) lazyWebPayloadStore(ns string) *lazyPayloadStore {
	return newLazyPayloadStore(func(ctx context.Context) (webPayloadStore, error) {
		return a.openPayloadStore(ctx, ns)
	})
}
