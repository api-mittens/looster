package postgres

import (
	"context"
	"github.com/api-mittens/looster/internal/app"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Opt-in PostgreSQL test. All test tables are connection-local temporary tables;
// no application rows or schemas are changed.
func TestSessionLiveness(t *testing.T) {
	url := os.Getenv("LOOSTER_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set LOOSTER_TEST_DATABASE_URL to exercise PostgreSQL leases")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	_, err = pool.Exec(ctx, `CREATE TEMP TABLE workers(id text,agents text[],last_seen timestamptz);
 CREATE TEMP TABLE jobs(id bigint,task_id bigint,context_task_id bigint,agent_name text,session_id text,state text);
 CREATE TEMP TABLE sessions(task_id bigint,agent_name text,session_id text,status text,PRIMARY KEY(task_id,agent_name));
 INSERT INTO sessions VALUES(987654321,'looster_liveness_test','','running');`)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := pgx.ConnectConfig(ctx, cfg.ConnConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(ctx)
	store := &Store{Pool: pool}
	assertStatus := func(want string) {
		t.Helper()
		sessions, err := store.Sessions(ctx, "looster_liveness_test")
		if err != nil {
			t.Fatal(err)
		}
		if len(sessions) != 1 || sessions[0].Status != want {
			t.Fatalf("got %+v, want %s", sessions, want)
		}
	}
	exec := func(sql string) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	assertStatus("orphaned")
	exec(`INSERT INTO workers VALUES('test',ARRAY['looster_liveness_test'],now())`)
	assertStatus("interrupted")
	if _, err := owner.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended('looster:session:987654321:looster_liveness_test',0))`); err != nil {
		t.Fatal(err)
	}
	assertStatus("running")
	if err := store.Bind(ctx, 987654321, "looster_liveness_test", "new"); err == nil {
		t.Fatal("rebound an active lease")
	}
	exec(`UPDATE workers SET last_seen=now()-interval '31 seconds'`)
	assertStatus("orphaned")
	if _, err := owner.Exec(ctx, `SELECT pg_advisory_unlock_all()`); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE workers SET last_seen=now()`)
	assertStatus("interrupted")
	if err := store.Bind(ctx, 987654321, "looster_liveness_test", "recovered"); err != nil {
		t.Fatal(err)
	}
	assertStatus("idle")
	exec(`UPDATE sessions SET status='failed'`)
	assertStatus("failed")
}

func TestWorkerHeartbeatBeforeAndAfterSettingsMigration(t *testing.T) {
	url := os.Getenv("LOOSTER_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set LOOSTER_TEST_DATABASE_URL to exercise PostgreSQL heartbeat compatibility")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(ctx, `CREATE TEMP TABLE workers(id text PRIMARY KEY,agents text[],last_seen timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	store := &Store{Pool: pool}
	settings := app.WorkerSettings{Harness: "jcode", Concurrency: 4, PollInterval: "1s", RunTimeout: "1h"}
	if err = store.Heartbeat(ctx, "test", []string{"coder"}, settings); err != nil {
		t.Fatal("heartbeat before migration:", err)
	}
	workers, err := store.Workers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != 1 || workers[0].Settings != nil {
		t.Fatalf("pre-migration worker view: %+v", workers)
	}
	if _, err = pool.Exec(ctx, `ALTER TABLE workers ADD COLUMN settings jsonb`); err != nil {
		t.Fatal(err)
	}
	if err = store.Heartbeat(ctx, "test", []string{"coder"}, settings); err != nil {
		t.Fatal("heartbeat after migration:", err)
	}
	workers, err = store.Workers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(workers) != 1 || workers[0].Settings == nil || workers[0].Settings.Concurrency != 4 {
		t.Fatalf("post-migration worker settings: %+v", workers)
	}
}
