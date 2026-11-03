package postgres

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dominikbb/looster/internal/app"
	"github.com/dominikbb/looster/internal/behaviour"
	"github.com/dominikbb/looster/internal/task"
	"github.com/jackc/pgx/v5"
)

func manifestTestStore(t *testing.T) (context.Context, *Store) {
	t.Helper()
	url := os.Getenv("LOOSTER_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set LOOSTER_TEST_DATABASE_URL to test real PostgreSQL manifests")
	}
	ctx := context.Background()
	name := fmt.Sprintf("test_manifest_%d", time.Now().UnixNano())
	if err := EnsureNamespace(ctx, url, name); err != nil {
		t.Fatal(err)
	}
	s, err := OpenNamespace(ctx, url, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		schema, _ := namespaceSchema(name)
		_, err := s.Pool.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		if err != nil {
			t.Error(err)
		}
		s.Close()
	})
	return ctx, s
}

func TestManifestVersionsAtomicReplacementAndConditions(t *testing.T) {
	ctx, s := manifestTestStore(t)
	initial, err := s.Manifests(ctx)
	if err != nil || len(initial) != 1 || !initial[0].Active {
		t.Fatalf("initial=%+v error=%v", initial, err)
	}
	source := "-- original bytes preserved\nAGENT coder WITH \"Implement\"; AGENT reviewer WITH \"Review\"; TAG team/backend WITH \"Backend\";\nON TODO PING coder WHEN TAGGED team/*; AFTER coder PING reviewer WHEN NOT TAGGED team/*; AFTER CLOSED STOP PINGING coder WHEN TAGGED team/*;\n"
	first, err := s.ApplyManifest(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateTask(ctx, "condition test", "", task.Planning, []string{"team/backend"})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := s.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	scalar := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := s.Pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	exec(`UPDATE tasks SET status='todo' WHERE id=$1`, created.ID)
	if n := scalar(`SELECT count(*) FROM jobs WHERE task_id=$1 AND agent_name='coder'`, created.ID); n != 1 {
		t.Fatalf("conditional ON dispatched %d", n)
	}
	exec(`INSERT INTO events(task_id,kind,agent_name) VALUES($1,'agent_stopped','coder')`, created.ID)
	if n := scalar(`SELECT count(*) FROM jobs WHERE task_id=$1 AND agent_name='reviewer'`, created.ID); n != 0 {
		t.Fatal("NOT TAGGED condition dispatched matching task")
	}
	exec(`UPDATE tasks SET status='closed' WHERE id=$1`, created.ID)
	if n := scalar(`SELECT count(*) FROM jobs WHERE task_id=$1 AND state='pending'`, created.ID); n != 0 {
		t.Fatal("conditional stop did not cancel pending job")
	}
	second, err := s.ApplyManifest(ctx, `AGENT coder WITH "Changed"; ON TODO PING coder WHEN TAGGED team/*; AFTER coder PING reviewer WHEN NOT TAGGED team/*;`)
	if err != nil {
		t.Fatal(err)
	}
	if n := scalar(`SELECT count(*) FROM tags WHERE name='team/backend' AND NOT manifest_active`); n != 1 {
		t.Fatal("old tag was lost or remains active")
	}
	if n := scalar(`SELECT count(*) FROM task_tags WHERE task_id=$1`, created.ID); n != 1 {
		t.Fatal("old assignment lost")
	}
	exec(`UPDATE tasks SET status='todo' WHERE id=$1`, created.ID)
	if n := scalar(`SELECT count(*) FROM jobs WHERE task_id=$1 AND agent_name='coder'`, created.ID); n != 1 {
		t.Fatal("inactive tag matched conditional ON")
	}
	exec(`INSERT INTO events(task_id,kind,agent_name) VALUES($1,'agent_stopped','coder')`, created.ID)
	if n := scalar(`SELECT count(*) FROM jobs WHERE task_id=$1 AND agent_name='reviewer'`, created.ID); n != 1 {
		t.Fatal("inactive tag blocked NOT TAGGED")
	}
	// This fails after agent/tag mutation has begun, proving transaction rollback.
	if _, err := s.ApplyManifest(ctx, `AGENT coder WITH "Must rollback"; TAG transient WITH "Rollback"; ON TODO PING missing_agent;`); err == nil {
		t.Fatal("unknown target accepted")
	}
	versions, err := s.Manifests(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 3 || versions[0].ID != second.ID || !versions[0].Active || versions[1].Source != source {
		t.Fatalf("failed apply changed history: %+v", versions)
	}
	if n := scalar(`SELECT count(*) FROM tags WHERE name='transient'`); n != 0 {
		t.Fatal("failed apply persisted a tag")
	}
	if n := scalar(`SELECT count(*) FROM agents WHERE name='coder' AND description='Changed'`); n != 1 {
		t.Fatal("failed apply changed instructions")
	}
	if err := s.ActivateManifest(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	versions, err = s.Manifests(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 3 || !versions[1].Active || versions[1].Source != source {
		t.Fatal("activation mutated versions")
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE behaviour_manifests SET source='changed' WHERE id=$1`, first.ID); err == nil {
		t.Fatal("manifest source mutable")
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM behaviour_manifests WHERE id=$1`, second.ID); err == nil {
		t.Fatal("manifest deletable")
	}
	if err := s.ActivateManifest(ctx, 99999999); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("missing manifest did not report domain not found: %v", err)
	}
	if _, err := s.ApplyManifest(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if n := scalar(`SELECT count(*) FROM rules`); n != 0 {
		t.Fatal("empty apply did not clear complete rules")
	}
	if n := scalar(`SELECT count(*) FROM tags WHERE manifest_active`); n != 0 {
		t.Fatal("empty apply retained active tags")
	}
}

func TestManifestPreservesScheduleCursorAndDurableContext(t *testing.T) {
	ctx, s := manifestTestStore(t)
	source := `AGENT coder WITH "Current"; AGENT omitted WITH "Old instructions"; TAG project WITH "Old tag"; EVERY 1 HOURS PING coder WITH "Tick";`
	if _, err := s.ApplyManifest(ctx, source); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, "durable context", "", task.Planning, []string{"project"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Bind(ctx, task.ID, "omitted", "preserved-session"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMemory(ctx, "agent", "omitted", "Preserved memory", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE rules SET next_run_at=now()+interval '2 hours' WHERE trigger='every'`); err != nil {
		t.Fatal(err)
	}
	var id int64
	var next time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT id,next_run_at FROM rules WHERE trigger='every'`).Scan(&id, &next); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyManifest(ctx, "-- New original file, same schedule\nAGENT coder WITH \"Current\"; EVERY 1 HOURS PING coder WITH \"Tick\";"); err != nil {
		t.Fatal(err)
	}
	var gotID int64
	var gotNext time.Time
	if err := s.Pool.QueryRow(ctx, `SELECT id,next_run_at FROM rules WHERE trigger='every'`).Scan(&gotID, &gotNext); err != nil {
		t.Fatal(err)
	}
	if gotID != id || !gotNext.Equal(next) {
		t.Fatalf("unchanged schedule reset: %d/%v != %d/%v", gotID, gotNext, id, next)
	}
	agent, err := s.Agent(ctx, "omitted")
	if err != nil || agent.Description != "" {
		t.Fatalf("omitted instructions remain: %+v %v", agent, err)
	}
	memories, err := s.Memories(ctx, "agent", "omitted")
	if err != nil || len(memories) != 1 {
		t.Fatalf("durable memory lost: %+v %v", memories, err)
	}
	tag, err := s.Tag(ctx, "project")
	if err != nil || !tag.Inactive {
		t.Fatalf("tag not preserved inactive: %+v %v", tag, err)
	}
	var sid string
	if err := s.Pool.QueryRow(ctx, `SELECT session_id FROM sessions WHERE task_id=$1 AND agent_name='omitted'`, task.ID).Scan(&sid); err != nil || sid != "preserved-session" {
		t.Fatalf("durable session lost: %s %v", sid, err)
	}
	expanded, err := app.ExpandTaskContext(ctx, s, task, "")
	if err != nil || len(expanded.Tags) != 0 {
		t.Fatalf("old tag instructions leak: %+v %v", expanded, err)
	}
}

func TestGeneratedLegacyManifest(t *testing.T) {
	ctx, s := manifestTestStore(t)
	schema := fmt.Sprintf("test_legacy_%d", time.Now().UnixNano())
	if _, err := s.Pool.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer s.Pool.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SET search_path="+pgx.Identifier{schema}.Sanitize()+",pg_temp"); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(ctx, "RESET search_path")
	scoped, err := namespaceMigrations(mustSub(), schema)
	if err != nil {
		t.Fatal(err)
	}
	files, err := fs.ReadDir(scoped, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.Name() >= "013_" {
			continue
		}
		data, err := fs.ReadFile(scoped, file.Name())
		if err != nil {
			t.Fatal(err)
		}
		up := strings.Split(string(data), "-- +goose Down")[0]
		if _, err := conn.Exec(ctx, up); err != nil {
			t.Fatalf("%s: %v", file.Name(), err)
		}
	}
	if _, err := conn.Exec(ctx, `INSERT INTO agents(name,description) VALUES('legacy','Original instructions'),('blank',''); INSERT INTO tags(name,description) VALUES('old/tag',''); INSERT INTO rules(trigger,target) VALUES('todo','legacy'),('todo','blank')`); err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(scoped, "013_manifests.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, strings.Split(string(data), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	var source string
	if err := conn.QueryRow(ctx, `SELECT source FROM behaviour_manifests`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	rules, err := behaviour.Parse(source)
	if err != nil {
		t.Fatalf("generated source invalid: %s: %v", source, err)
	}
	if len(rules) != 5 || !strings.Contains(source, `AGENT legacy WITH "Original instructions"`) || !strings.Contains(source, `AGENT blank WITH ""`) {
		t.Fatalf("legacy configuration missing: %s", source)
	}
	// Exported legacy configurations must be self-contained, including agents
	// registered without instructions but still referenced by dispatch rules.
	freshCtx, fresh := manifestTestStore(t)
	if _, err := fresh.ApplyManifest(freshCtx, source); err != nil {
		t.Fatalf("legacy source not portable to a fresh namespace: %v", err)
	}
	blank, err := fresh.Agent(freshCtx, "blank")
	if err != nil || blank.Description != "" {
		t.Fatalf("blank agent changed: %+v %v", blank, err)
	}
	var count int
	if err := fresh.Pool.QueryRow(freshCtx, `SELECT count(*) FROM rules WHERE trigger='todo' AND target='blank'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("blank-agent dispatch rule lost: %d %v", count, err)
	}
}
