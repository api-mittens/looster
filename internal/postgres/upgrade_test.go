package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/task"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Each upgrade test creates its own disposable database. Never apply an old
// migration or a fixture to the database supplied by the developer.
func legacyUpgradeDatabase(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("LOOSTER_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("set LOOSTER_TEST_DATABASE_URL to run database upgrade tests (requires CREATEDB)")
	}
	ctx := context.Background()
	cfg, err := pgx.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("looster_upgrade_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	// Palette scoring uses Unicode semantics, so do not inherit a SQL_ASCII
	// template from developer test clusters.
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+quoted+" TEMPLATE template0 ENCODING 'UTF8' LC_COLLATE 'C' LC_CTYPE 'C'"); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Errorf("clean up test database: %v", err)
		}
		admin.Close(cleanup)
	})
	if strings.HasPrefix(raw, "postgres://") || strings.HasPrefix(raw, "postgresql://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		parsed.Path = "/" + name
		query := parsed.Query()
		query.Del("dbname")
		query.Del("database")
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	return raw + " dbname='" + name + "'"
}

func TestUpgradeExistingDatabasePreservesFactoryData(t *testing.T) {
	ctx := context.Background()
	url := legacyUpgradeDatabase(t)
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB := stdlib.OpenDB(*cfg)
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, mustSub())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 11); err != nil {
		t.Fatal(err)
	}
	sqlDB.Close()
	old, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close(ctx)
	_, err = old.Exec(ctx, `
 INSERT INTO agents(name,description) VALUES('coder','Implement carefully'),('reviewer','Review evidence');
 INSERT INTO tags(name,description) VALUES('legacy/source','Legacy tag context');
 INSERT INTO rules(trigger,target) VALUES('unvalidated','reviewer'),('stop_unvalidated','coder');
 INSERT INTO tasks(name,description,status) VALUES('Existing task','Keep this work','unvalidated');
 INSERT INTO task_tags SELECT tasks.id,tags.id FROM tasks,tags;
 INSERT INTO memories(tag_id,body) SELECT id,'Keep shared memory' FROM tags;
 INSERT INTO sessions(task_id,agent_name,session_id,status) SELECT id,'coder','legacy-session','idle' FROM tasks;
 INSERT INTO events(task_id,kind,body) SELECT id,'comment','Evidence stays append-only' FROM tasks;
 INSERT INTO attachments(task_id,name,object_key,size) SELECT id,'report.txt','legacy/report',12 FROM tasks;
 `)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := func() map[string]string {
		t.Helper()
		out := map[string]string{}
		for _, table := range []string{"events", "jobs", "sessions", "memories", "attachments", "task_tags"} {
			var body string
			projection := "to_jsonb(x)"
			if table == "jobs" {
				// Migration 018 adds nullable/default-only staging metadata.
				projection = "(to_jsonb(x)-'started_at'-'summary')"
			}
			if table == "events" {
				// Migration 019 adds nullable run attribution, without changing old rows.
				projection = "(to_jsonb(x)-'job_id')"
			}
			query := "SELECT coalesce(jsonb_agg(" + projection + " ORDER BY " + projection + "::text),'[]'::jsonb)::text FROM " + table + " x"
			if err := old.QueryRow(ctx, query).Scan(&body); err != nil {
				t.Fatal(err)
			}
			out[table] = body
		}
		return out
	}
	before := snapshot()
	if err := Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, url); err != nil {
		t.Fatalf("idempotent migrate: %v", err)
	}
	after := snapshot()
	var attributed int
	if err := old.QueryRow(ctx, `SELECT count(*) FROM events WHERE job_id IS NOT NULL`).Scan(&attributed); err != nil || attributed != 0 {
		t.Fatalf("upgrade attributed legacy events: count=%d err=%v", attributed, err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("upgrade changed historical or active factory data\nbefore=%v\nafter=%v", before, after)
	}
	store, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	tasks, err := store.Tasks(ctx)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	current := tasks[0]
	if current.Status != task.Todo || len(current.Tags) != 1 || current.Tags[0] != "legacy/source" {
		t.Fatalf("legacy task lost state: %+v", current)
	}
	if err := store.SetStatus(ctx, current.ID, task.Status("unvalidated")); err == nil {
		t.Fatal("accepted removed status")
	}
	var count int
	if err := old.QueryRow(ctx, `SELECT count(*) FROM rules WHERE trigger='todo' AND target='reviewer'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("legacy rule not migrated: count=%d err=%v", count, err)
	}
	var stopped bool
	if err := old.QueryRow(ctx, `SELECT looster_ping_stopped($1,'coder')`, current.ID).Scan(&stopped); err != nil || !stopped {
		t.Fatalf("historical stop rule lost across removed status: stopped=%v err=%v", stopped, err)
	}
	manifests, err := store.Manifests(ctx)
	if err != nil || len(manifests) != 1 || !manifests[0].Active {
		t.Fatalf("legacy baseline: %+v %v", manifests, err)
	}
	if !strings.Contains(manifests[0].Source, "AGENT coder") || !strings.Contains(manifests[0].Source, "TAG legacy/source") {
		t.Fatalf("baseline omitted legacy definitions: %s", manifests[0].Source)
	}
	// Full replacement may remove configuration, but never destroys task tags,
	// sessions, memory, attachments, or audit history associated with old work.
	source := "-- exact full-file manifest, including comments\nAGENT coder WITH \"New instructions\";\n"
	applied, err := store.ApplyManifest(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Source != source {
		t.Fatal("manifest source was normalized or truncated")
	}
	contextValue, err := app.ExpandTaskContext(ctx, store, current, "coder")
	if err != nil {
		t.Fatalf("legacy tags blocked task context: %v", err)
	}
	encoded, _ := json.Marshal(contextValue)
	if strings.Contains(string(encoded), "Legacy tag context") {
		t.Fatalf("removed tag instructions remain active: %s", encoded)
	}
	if !reflect.DeepEqual(after, snapshot()) {
		t.Fatal("manifest replacement destroyed task-related data")
	}
	if err := store.ActivateManifest(ctx, manifests[0].ID); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Tag(ctx, "legacy/source")
	if err != nil || restored.Description != "Legacy tag context" {
		t.Fatalf("restore legacy manifest: %+v %v", restored, err)
	}
}
