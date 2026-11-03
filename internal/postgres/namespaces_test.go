package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dominikbb/looster/internal/app"
	"github.com/dominikbb/looster/internal/task"
	"github.com/jackc/pgx/v5"
)

func TestNamespacePoolConfig(t *testing.T) {
	if schema, err := namespaceSchema("default"); err != nil || schema != "public" {
		t.Fatalf("default compatibility: %q %v", schema, err)
	}
	if schema, err := namespaceSchema(strings.Repeat("a", 52)); err != nil || len(schema) != 63 {
		t.Fatalf("maximum schema identifier: %q %v", schema, err)
	}
	for _, tc := range []struct{ name, want string }{{"default", `"public",pg_temp`}, {"team-a", `"looster_ns_team-a",pg_temp`}} {
		cfg, err := namespaceConfig("host=localhost search_path=public", tc.name)
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.ConnConfig.RuntimeParams["search_path"]; got != tc.want {
			t.Errorf("%s: %q != %q", tc.name, got, tc.want)
		}
	}
	if _, err := namespaceConfig("", "bad;sql"); err == nil {
		t.Fatal("invalid namespace accepted")
	}
}

func TestNamespaceMigrationPaths(t *testing.T) {
	scoped, err := namespaceMigrations(mustSub(), "looster_ns_team-a")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := fs.ReadDir(scoped, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := fs.ReadFile(scoped, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "search_path=public,pg_temp") {
			t.Errorf("public path remains in %s", entry.Name())
		}
	}
	data, err := fs.ReadFile(scoped, "001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `search_path="looster_ns_team-a",pg_temp`) || !strings.Contains(string(data), "'looster_ns_team-a:looster:event-sequence'") {
		t.Fatal("migration was not schema scoped")
	}
}

// This test creates two uniquely named schemas and drops only those schemas.
// It never migrates public or alters existing application rows.
func TestNamespaceIsolation(t *testing.T) {
	url := os.Getenv("LOOSTER_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set LOOSTER_TEST_DATABASE_URL to run real PostgreSQL namespace isolation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	names := []string{"test_a_" + suffix, "test-b-" + suffix}
	stores := make([]*Store, 0, 2)
	cfg, err := parsePoolConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.ConnectConfig(ctx, cfg.ConnConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	for _, name := range names {
		schema, _ := namespaceSchema(name)
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := admin.Exec(cleanup, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		ensured := make(chan error, 2)
		for range 2 {
			go func() { ensured <- EnsureNamespace(ctx, url, name) }()
		}
		first, second := <-ensured, <-ensured
		if first != nil || second != nil {
			t.Fatalf("concurrent namespace ensure: %v, %v", first, second)
		}
		store, err := OpenNamespace(ctx, url, name)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		stores = append(stores, store)
	}
	listed, err := ListNamespaces(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if !slices.Contains(listed, name) {
			t.Errorf("namespace %s missing from list", name)
		}
	}
	if store, err := OpenNamespace(ctx, url, "missing_"+suffix); err == nil {
		store.Close()
		t.Fatal("open silently created a namespace")
	}
	for i, s := range stores {
		if _, err := s.ApplyManifest(ctx, `AGENT shared WITH "Work"; TAG project WITH "Project"; ON TODO PING shared; EVERY 1 SECONDS PING shared WITH "Tick";`); err != nil {
			t.Fatal(err)
		}
		created, err := s.CreateTask(ctx, fmt.Sprintf("task-%d", i), "", task.Todo, []string{"project"})
		if err != nil {
			t.Fatal(err)
		}
		if created.ID != 1 {
			t.Fatalf("fresh namespaces must have independent task IDs, got %d", created.ID)
		}
		if err := s.Heartbeat(ctx, "worker", []string{"shared"}, app.WorkerSettings{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := stores[0].Comment(ctx, 1, "only-a"); err != nil {
		t.Fatal(err)
	}
	if err := stores[0].AddMemory(ctx, "agent", "shared", "private-a", false); err != nil {
		t.Fatal(err)
	}
	memories, err := stores[1].Memories(ctx, "agent", "shared")
	if err != nil || len(memories) != 0 {
		t.Fatalf("agent memories leaked: %+v %v", memories, err)
	}
	events, err := stores[1].Events(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Body == "only-a" {
			t.Fatal("events leaked between namespaces")
		}
	}
	// Identical job/task/agent identities must not contend on advisory leases.
	a, err := stores[0].Claim(ctx, []string{"shared"})
	if err != nil || a == nil {
		t.Fatalf("claim A: %v, %v", a, err)
	}
	defer a.Close()
	b, err := stores[1].Claim(ctx, []string{"shared"})
	if err != nil || b == nil {
		t.Fatalf("claim B blocked by A: %v, %v", b, err)
	}
	defer b.Close()
	if err := a.Started(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Started(ctx); err != nil {
		t.Fatal(err)
	}
	for _, s := range stores {
		sessions, err := s.Sessions(ctx, "shared")
		if err != nil {
			t.Fatal(err)
		}
		if len(sessions) != 1 || sessions[0].Status != "running" {
			t.Fatalf("namespace session liveness: %+v", sessions)
		}
		if err := s.Bind(ctx, 1, "shared", "overwrite"); err == nil {
			t.Fatal("active namespace session rebound")
		}
	}
	for _, s := range stores {
		if _, err := s.Pool.Exec(ctx, `UPDATE rules SET next_run_at=now()-interval '1 second' WHERE trigger='every'`); err != nil {
			t.Fatal(err)
		}
		n, err := s.Schedule(ctx, []string{"shared"})
		if err != nil {
			t.Fatal(err)
		}
		if n < 1 {
			t.Fatal("namespace-local recurring schedule did not enqueue")
		}
	}
	// Function execution is pinned even when a caller targets another schema explicitly.
	schemaA, _ := namespaceSchema(names[0])
	if _, err := stores[1].Pool.Exec(ctx, "INSERT INTO "+pgx.Identifier{schemaA, "tasks"}.Sanitize()+"(name,description,status) VALUES('qualified-a','','planning')"); err != nil {
		t.Fatal(err)
	}
	tasksA, err := stores[0].Tasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tasksB, err := stores[1].Tasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasksA) != 2 || len(tasksB) != 1 || tasksB[0].Name != "task-1" {
		t.Fatalf("cross-schema function routing: A=%+v B=%+v", tasksA, tasksB)
	}
	events, err = stores[0].Events(ctx, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("qualified insert trigger did not write to its own namespace")
	}
	var badFunctions int
	if err := stores[0].Pool.QueryRow(ctx, `SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=current_schema() AND left(p.proname,8)='looster_' AND NOT EXISTS(SELECT FROM unnest(coalesce(p.proconfig,ARRAY[]::text[])) c WHERE c LIKE 'search_path=%')`).Scan(&badFunctions); err != nil {
		t.Fatal(err)
	}
	if badFunctions != 0 {
		t.Fatalf("%d functions lack pinned search_path", badFunctions)
	}
	if err := stores[0].Leave(ctx, "worker"); err != nil {
		t.Fatal(err)
	}
	priorVersions, err := stores[1].Manifests(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stores[0].ApplyManifest(ctx, `AGENT exclusive WITH "Namespace A only"; TAG exclusive WITH "A only";`); err != nil {
		t.Fatal(err)
	}
	versions, err := stores[1].Manifests(ctx)
	if err != nil || len(versions) != len(priorVersions) {
		t.Fatalf("manifest versions leaked: %+v %v", versions, err)
	}
	if _, err := stores[1].Agent(ctx, "exclusive"); err == nil {
		t.Fatal("agents leaked across namespaces")
	}
	if _, err := stores[1].Tag(ctx, "exclusive"); err == nil {
		t.Fatal("tags leaked across namespaces")
	}
	workers, err := stores[1].Workers(ctx)
	if err != nil || len(workers) != 1 {
		t.Fatalf("worker rows leaked: %+v %v", workers, err)
	}
}
