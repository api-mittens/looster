package cli

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

type serverDatabase struct {
	closes int
	err    error
}

func (d *serverDatabase) Close() error { d.closes++; return d.err }

func TestServerDatabaseLifetime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	database := &serverDatabase{}
	opens := 0
	var workers atomic.Int32
	err := serveWithDatabase(ctx, func(received context.Context) (io.Closer, error) {
		if received != ctx {
			t.Fatal("open received a different context")
		}
		opens++
		return database, nil
	}, func(received context.Context) error {
		if received != ctx || opens != 1 || database.closes != 0 {
			t.Fatal("database not retained before serving")
		}
		var group sync.WaitGroup
		for i := 0; i < 8; i++ {
			group.Add(1)
			go func() { defer group.Done(); <-received.Done(); workers.Add(1) }()
		}
		cancel()
		group.Wait()
		if database.closes != 0 {
			t.Fatal("database closed before workers stopped")
		}
		return nil
	})
	if err != nil || opens != 1 || database.closes != 1 || workers.Load() != 8 {
		t.Fatalf("lifetime: err=%v opens=%d closes=%d workers=%d", err, opens, database.closes, workers.Load())
	}
}

func TestServerDatabaseFailurePaths(t *testing.T) {
	openFailure := errors.New("open failed")
	called := false
	if err := serveWithDatabase(context.Background(), func(context.Context) (io.Closer, error) { return nil, openFailure }, func(context.Context) error { called = true; return nil }); !errors.Is(err, openFailure) || called {
		t.Fatalf("open failure: err=%v serve=%v", err, called)
	}
	serveFailure, closeFailure := errors.New("serve failed"), errors.New("close failed")
	database := &serverDatabase{err: closeFailure}
	err := serveWithDatabase(context.Background(), func(context.Context) (io.Closer, error) { return database, nil }, func(context.Context) error { return serveFailure })
	if !errors.Is(err, serveFailure) || !errors.Is(err, closeFailure) || database.closes != 1 {
		t.Fatalf("failures not retained: %v closes=%d", err, database.closes)
	}
}

func TestServerBareEngineHasNoExternalPrerequisites(t *testing.T) {
	// Even poisoned catalog/S3 environment and an unusable extension cache must
	// not make the core server lifetime depend on DuckLake initialization.
	t.Setenv("PGSERVICE", "must-not-be-used")
	t.Setenv("LOOSTER_S3_ENDPOINT", "invalid")
	t.Setenv("XDG_CACHE_HOME", "relative-invalid-cache")
	t.Setenv("HOME", "")
	t.Setenv("LocalAppData", "")
	called := false
	if err := serveWithDuckDB(context.Background(), func(context.Context) error { called = true; return nil }); err != nil || !called {
		t.Fatalf("bare server engine: err=%v served=%v", err, called)
	}
}
