package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/dominikbb/looster/postgres/db"
	"github.com/dominikbb/looster/task"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// Use the production config hook on a fresh connection, not SET on one pooled
// session. This also checks that replacement connections inherit the bound.
func shortLockStore(t *testing.T, s *Store) *Store {
	t.Helper()
	cfg := s.Pool.Config()
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "100ms"
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &Store{Pool: pool, q: db.New(pool)}
}

func requireLockTimeout(t *testing.T, start time.Time, err error) {
	t.Helper()
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "55P03" {
		t.Fatalf("want PostgreSQL lock timeout (55P03), got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("lock timeout took %s", elapsed)
	}
}

func TestConnectionLockTimeout(t *testing.T) {
	ctx, s := manifestTestStore(t)
	// Both native migration connections and Goose's database/sql connections
	// must run the same post-startup hook as the application pool.
	for _, tc := range []struct{ name, setting, options, want string }{
		{"default", "", "", "5s"},
		{"zero", "0", "", "5s"},
		{"short explicit", "75ms", "", "75ms"},
		{"long explicit", "9s", "", "9s"},
		{"startup options", "", "-c lock_timeout=60ms", "60ms"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := s.Pool.Config().ConnConfig.Copy()
			delete(cfg.RuntimeParams, "lock_timeout")
			delete(cfg.RuntimeParams, "options")
			if tc.setting != "" {
				cfg.RuntimeParams["lock_timeout"] = tc.setting
			}
			if tc.options != "" {
				cfg.RuntimeParams["options"] = tc.options
			}
			want := tc.want
			conn, err := pgx.ConnectConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close(ctx)
			var got string
			if err := conn.QueryRow(ctx, "SHOW lock_timeout").Scan(&got); err != nil || got != want {
				t.Fatalf("native timeout=%q want=%q error=%v", got, want, err)
			}
			sqlDB := stdlib.OpenDB(*cfg)
			defer sqlDB.Close()
			if err := sqlDB.QueryRowContext(ctx, "SHOW lock_timeout").Scan(&got); err != nil || got != want {
				t.Fatalf("stdlib timeout=%q want=%q error=%v", got, want, err)
			}
		})
	}
}

func TestMutationLockTimeouts(t *testing.T) {
	ctx, s := manifestTestStore(t)
	bounded := shortLockStore(t, s)
	if _, err := s.ApplyManifest(ctx, `AGENT coder WITH "Code";`); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateTask(ctx, "locked", "", task.Planning, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMemory(ctx, "agent", "coder", "original", false); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, lock string
		args       []any
		mutate     func() error
	}{
		{"manifest advisory", `SELECT pg_advisory_xact_lock(hashtextextended(current_schema()||':looster:manifests',0))`, nil,
			func() error { _, err := bounded.ApplyManifest(ctx, `AGENT coder WITH "Changed";`); return err }},
		{"memory advisory", `SELECT pg_advisory_xact_lock(hashtextextended(current_schema()||':looster:memory:agent:coder',0))`, nil,
			func() error { return bounded.AddMemory(ctx, "agent", "coder", "replacement", true) }},
		{"task row", `SELECT id FROM tasks WHERE id=$1 FOR UPDATE`, []any{created.ID},
			func() error { return bounded.SetStatus(ctx, created.ID, task.Todo) }},
		{"memory row", `SELECT id FROM memories WHERE agent_name='coder' FOR UPDATE`, nil,
			func() error { return bounded.AddMemory(ctx, "agent", "coder", "replacement", true) }},
		{"manifest row", `SELECT singleton FROM behaviour_manifest_state FOR UPDATE`, nil,
			func() error { _, err := bounded.ApplyManifest(ctx, `AGENT coder WITH "Changed";`); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := s.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, tc.lock, tc.args...); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			requireLockTimeout(t, start, tc.mutate())
		})
	}
	memories, err := s.Memories(ctx, "agent", "coder")
	if err != nil || len(memories) != 1 || memories[0].Body != "original" {
		t.Fatalf("failed wipe was not atomic: %+v %v", memories, err)
	}
	current, err := s.Task(ctx, created.ID)
	if err != nil || current.Status != task.Planning {
		t.Fatalf("failed task mutation leaked: %+v %v", current, err)
	}
}

func TestEventCursorCommitOrderingWithLockTimeout(t *testing.T) {
	ctx, s := manifestTestStore(t)
	bounded := shortLockStore(t, s)
	created, err := s.CreateTask(ctx, "cursor", "", task.Planning, nil)
	if err != nil {
		t.Fatal(err)
	}
	initial, err := s.Events(ctx, created.ID, 0)
	if err != nil || len(initial) == 0 {
		t.Fatalf("initial events: %+v %v", initial, err)
	}
	cursor := initial[len(initial)-1].ID
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var first int64
	if err := tx.QueryRow(ctx, `INSERT INTO events(task_id,kind,body) VALUES($1,'comment','uncommitted') RETURNING id`, created.ID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	// The later writer must fail, not allocate and commit an ID beyond the
	// invisible first writer. Replacing serialization with try-lock-and-proceed
	// would let a consumer advance its cursor past that first event forever.
	start := time.Now()
	_, err = bounded.Comment(ctx, created.ID, "must not commit")
	requireLockTimeout(t, start, err)
	visible, err := s.Events(ctx, created.ID, cursor)
	if err != nil || len(visible) != 0 {
		t.Fatalf("cursor advanced past uncommitted event: %+v %v", visible, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := bounded.Comment(ctx, created.ID, "retry")
	if err != nil {
		t.Fatal(err)
	}
	visible, err = s.Events(ctx, created.ID, cursor)
	if err != nil || len(visible) != 2 || visible[0].ID != first || visible[1].ID != second || second != first+1 {
		t.Fatalf("commit ordered cursor: %+v first=%d second=%d error=%v", visible, first, second, err)
	}
	tail, err := s.Events(ctx, created.ID, first)
	if err != nil || len(tail) != 1 || tail[0].ID != second {
		t.Fatalf("resume cursor: %+v %v", tail, err)
	}
}

// Holding the event serialization lock must not block another namespace.
func TestEventLockNamespaceIsolation(t *testing.T) {
	ctx, a := manifestTestStore(t)
	_, b := manifestTestStore(t)
	bounded := shortLockStore(t, b)
	first, err := a.CreateTask(ctx, "a", "", task.Planning, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := b.CreateTask(ctx, "b", "", task.Planning, nil)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO events(task_id,kind,body) VALUES($1,'comment','held')`, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := bounded.Comment(ctx, second.ID, "independent"); err != nil {
		t.Fatalf("namespace-local event serialization leaked across schemas: %v", err)
	}
}

func TestMigrationLockTimeout(t *testing.T) {
	ctx, s := manifestTestStore(t)
	bounded := shortLockStore(t, s)
	conn, err := bounded.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	var schema string
	if err := conn.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, "looster:migrations:"+schema); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, "looster:migrations:"+schema)
	// Supply startup options without rewriting URLs or exposing credentials.
	t.Setenv("PGOPTIONS", "-c lock_timeout=100ms")
	start := time.Now()
	err = MigrateNamespace(ctx, os.Getenv("LOOSTER_TEST_DATABASE_URL"), schema[len(namespaceSchemaPrefix):])
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("migration lock wait was not bounded: elapsed=%s error=%v", time.Since(start), err)
	}
}
