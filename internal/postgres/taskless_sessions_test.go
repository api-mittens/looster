package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// All data is connection-local. A separate connection represents concurrent
// harness owners, holding the same advisory leases used by Claim.
func TestTasklessSessions(t *testing.T) {
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
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TEMP TABLE workers(id text,agents text[],last_seen timestamptz);
 CREATE TEMP TABLE sessions(task_id bigint,agent_name text,session_id text,status text);
 CREATE TEMP TABLE jobs(id bigint,task_id bigint,context_task_id bigint,agent_name text,session_id text,state text);
 INSERT INTO sessions VALUES(42,'coder','task-session','idle');
 INSERT INTO workers VALUES('worker',ARRAY['coder'],now());
 INSERT INTO jobs VALUES
 (100,NULL,NULL,'coder','','running'),
 (101,NULL,NULL,'coder','','running'),
 (102,NULL,42,'coder','notification-session','archiving'),
 (103,NULL,NULL,'coder','pending','pending'),
 (104,NULL,NULL,'coder','done','done'),
 (105,NULL,NULL,'coder','failed','failed'),
 (106,42,NULL,'coder','task-session','running');`)
	owner, err := pgx.ConnectConfig(ctx, cfg.ConnConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(ctx)
	lock := func(key string) {
		t.Helper()
		if _, err := owner.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, key); err != nil {
			t.Fatal(err)
		}
	}
	lock("looster:job:100")
	lock("looster:job:101")
	// A context job deliberately does not use looster:job:102.
	lock("looster:session:42:coder")
	store := &Store{Pool: pool}
	read := func() []agent.Session {
		t.Helper()
		got, err := store.Sessions(ctx, "coder")
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	got := read()
	if len(got) != 4 {
		t.Fatalf("sessions: %+v", got)
	}
	for i, id := range []int64{100, 101, 102} {
		if got[i].JobID != id || got[i].TaskID != 0 || got[i].Status != "running" {
			t.Fatalf("concurrent session %d: %+v", id, got[i])
		}
	}
	if got[0].SessionID != "" || got[1].SessionID != "" {
		t.Fatal("test requires concurrent runs before harness identity exists")
	}
	if got[3].TaskID != 42 || got[3].JobID != 0 || got[3].Status != "idle" {
		t.Fatalf("task session changed: %+v", got[3])
	}
	if filtered, err := store.Sessions(ctx, "other"); err != nil || len(filtered) != 0 {
		t.Fatalf("agent filter: %+v %v", filtered, err)
	}
	if all, err := store.Sessions(ctx, ""); err != nil || len(all) != 4 {
		t.Fatalf("all sessions: %+v %v", all, err)
	}
	exec(`UPDATE jobs SET session_id='captured' WHERE id=100`)
	if got := read(); got[0].JobID != 100 || got[0].SessionID != "captured" {
		t.Fatalf("unstable identity: %+v", got)
	}
	exec(`UPDATE workers SET last_seen=now()-interval '31 seconds'`)
	for _, s := range read()[:3] {
		if s.Status != "orphaned" {
			t.Fatalf("stale worker: %+v", s)
		}
	}
	// A live worker for another agent cannot make these jobs appear live.
	exec(`INSERT INTO workers VALUES('unrelated',ARRAY['other'],now())`)
	if got := read(); got[0].Status != "orphaned" {
		t.Fatalf("unrelated coverage: %+v", got)
	}
	exec(`UPDATE workers SET last_seen=now() WHERE id='worker'`)
	if _, err := owner.Exec(ctx, `SELECT pg_advisory_unlock(hashtextextended('looster:job:101',0))`); err != nil {
		t.Fatal(err)
	}
	got = read()
	if got[0].Status != "running" || got[1].Status != "interrupted" || got[2].Status != "running" {
		t.Fatalf("per-run leases: %+v", got)
	}
	// Completion must hide the job even while its owner still holds the lease.
	exec(`UPDATE jobs SET state='done' WHERE id=100; UPDATE jobs SET state='failed' WHERE id=101; UPDATE jobs SET state='pending' WHERE id=102`)
	got = read()
	if len(got) != 1 || got[0].TaskID != 42 {
		t.Fatalf("finished runs retained: %+v", got)
	}
}
