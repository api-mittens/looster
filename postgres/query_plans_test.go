package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dominikbb/looster/event"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

var queryIndexDefinitions = []struct{ name, definition string }{
	{"task_tags_tag_idx", "task_tags(tag_id)"},
	{"memories_tag_id_idx", "memories(tag_id,id)"},
	{"memories_agent_id_idx", "memories(agent_name,id)"},
	{"sessions_agent_idx", "sessions(agent_name)"},
	{"attachments_task_id_idx", "attachments(task_id,id)"},
	{"attachments_event_idx", "attachments(event_id)"},
	{"jobs_active_session_idx", "jobs(agent_name,(coalesce(task_id,context_task_id)),id) WHERE state IN ('pending','running','archiving')"},
	{"jobs_task_idx", "jobs(task_id)"},
	{"jobs_agent_idx", "jobs(agent_name)"},
	{"jobs_schedule_rule_idx", "jobs(schedule_rule_id)"},
}

// These tests create a separate database, never reset the supplied database.
func queryPlanDatabase(t *testing.T) (context.Context, string, *pgx.Conn) {
	return queryPlanNamespaceDatabase(t, "default")
}

func queryPlanNamespaceDatabase(t *testing.T, name string) (context.Context, string, *pgx.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	url := legacyUpgradeDatabase(t)
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := namespaceSchema(name)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if name != "default" {
		queryPlanExec(t, ctx, admin, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	}
	admin.Close(ctx)
	cfg.RuntimeParams["search_path"] = pgx.Identifier{schema}.Sanitize() + ",pg_temp"
	source, err := namespaceMigrations(mustSub(), schema)
	if err != nil {
		t.Fatal(err)
	}
	d := stdlib.OpenDB(*cfg)
	defer d.Close()
	p, err := goose.NewProvider(goose.DialectPostgres, d, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.UpTo(ctx, 15); err != nil {
		t.Fatal(err)
	}
	d.Close()
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	return ctx, url, c
}

func queryPlanExec(t *testing.T, ctx context.Context, c *pgx.Conn, sql string) {
	t.Helper()
	if _, err := c.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
}

func seedQueryPlans(t *testing.T, ctx context.Context, c *pgx.Conn) {
	t.Helper()
	// Trigger disabling is local to the disposable database, avoids generating
	// unrelated dispatch/event history and makes exact row counts reproducible.
	queryPlanExec(t, ctx, c, `
 ALTER TABLE tasks DISABLE TRIGGER USER;
 ALTER TABLE events DISABLE TRIGGER USER;
 ALTER TABLE task_tags DISABLE TRIGGER USER;
 ALTER TABLE attachments DISABLE TRIGGER USER;
 INSERT INTO agents(name,description) SELECT 'agent'||g,'fixture' FROM generate_series(1,1000) g;
 INSERT INTO tags(name,description) SELECT 'tag/'||translate(lpad(g::text,4,'0'),'0123456789','abcdefghij'),'fixture' FROM generate_series(1,1000) g;
 INSERT INTO tasks(id,name,description) SELECT g,'task '||g,repeat('x',80) FROM generate_series(1,100000) g;
 INSERT INTO events(id,task_id,kind,body) SELECT g,1+(g-1)%100000,CASE WHEN g%10=0 THEN 'comment' ELSE 'output' END,repeat('x',80) FROM generate_series(1,200000) g;
 INSERT INTO task_tags SELECT g,1+(g-1)%1000 FROM generate_series(1,100000) g;
 INSERT INTO memories(tag_id,body) SELECT 1+(g-1)%1000,repeat('x',120) FROM generate_series(1,100000) g;
 INSERT INTO memories(agent_name,body) SELECT 'agent'||(1+(g-1)%1000),repeat('x',120) FROM generate_series(1,100000) g;
 INSERT INTO sessions(task_id,agent_name,session_id) SELECT g,'agent'||(1+(g-1)%1000),'session' FROM generate_series(1,100000) g;
 INSERT INTO attachments(task_id,event_id,name,object_key,size) SELECT 1+(g-1)%100000,g,'file','key'||g,128 FROM generate_series(1,200000) g;
 INSERT INTO rules(id,trigger,target) SELECT g,'todo','agent'||g FROM generate_series(1,1000) g;
 INSERT INTO jobs(task_id,context_task_id,agent_name,event_id,state,schedule_rule_id)
 SELECT CASE WHEN g%2=0 THEN 1+(g-1)%40000 END,
 CASE WHEN g%2=1 THEN 1+(g-1)%40000 END,'agent'||(1+(g-1)%1000),g,
 CASE WHEN g<=80000 THEN 'pending' ELSE 'done' END,CASE WHEN g>80000 THEN 1+(g-1)%1000 END FROM generate_series(1,200000) g;
 ALTER TABLE tasks ENABLE TRIGGER USER;
 ALTER TABLE events ENABLE TRIGGER USER;
 ALTER TABLE task_tags ENABLE TRIGGER USER;
 ALTER TABLE attachments ENABLE TRIGGER USER;
 ANALYZE;
 `)
}

var measuredQueries = []struct{ name, sql, index string }{
	{"tag reverse lookup", `SELECT task_id FROM task_tags WHERE tag_id=777`, "task_tags_tag_idx"},
	{"tag memories", `SELECT id,body,created_at FROM memories WHERE tag_id=(SELECT id FROM tags WHERE name='tag/ahhh') ORDER BY id`, "memories_tag_id_idx"},
	{"agent memories", `SELECT id,body,created_at FROM memories WHERE agent_name='agent777' ORDER BY id`, "memories_agent_id_idx"},
	{"agent sessions", `SELECT task_id,agent_name,session_id,status FROM sessions WHERE ( 'agent777'='' OR agent_name='agent777') ORDER BY task_id,agent_name`, "sessions_agent_idx"},
	{"task attachments", `SELECT id,task_id,event_id,name,object_key,size FROM attachments WHERE task_id=777 ORDER BY id`, "attachments_task_id_idx"},
	{"attachment event FK probe", `SELECT 1 FROM attachments WHERE event_id=777 FOR KEY SHARE`, "attachments_event_idx"},
	{"active predecessor", `SELECT 1 FROM jobs earlier WHERE coalesce(earlier.task_id,earlier.context_task_id)=777 AND earlier.agent_name='agent777' AND earlier.id<79000 AND earlier.state IN ('pending','running','archiving')`, "jobs_active_session_idx"},
	{"jobs task FK probe", `SELECT 1 FROM jobs WHERE task_id=778 FOR KEY SHARE`, "jobs_task_idx"},
	{"jobs agent FK probe", `SELECT 1 FROM jobs WHERE agent_name='agent777' FOR KEY SHARE`, "jobs_agent_idx"},
	{"jobs rule FK probe", `SELECT 1 FROM jobs WHERE schedule_rule_id=777 FOR KEY SHARE`, "jobs_schedule_rule_idx"},
	{"task activity count", `SELECT count(*) FROM events WHERE task_id=780 AND kind IN ('status_changed','comment')`, "events_task_activity_idx"},
	{"rules target FK probe", `SELECT 1 FROM rules WHERE target='agent777' FOR KEY SHARE`, ""},
	{"rules source FK probe", `SELECT 1 FROM rules WHERE source='agent777' FOR KEY SHARE`, ""},
	{"task latest activity", `SELECT created_at FROM events WHERE task_id=777 ORDER BY created_at DESC LIMIT 1`, "events_task_last_activity"},
	{"claim anti join", `SELECT j.id,coalesce(j.task_id,0),j.agent_name,j.state,j.session_id,j.prompt,coalesce(j.context_task_id,0),j.notification FROM jobs j WHERE j.agent_name=ANY(ARRAY['agent777']) AND j.available_at<=now() AND j.state IN ('pending','running','archiving') AND NOT EXISTS(SELECT FROM jobs earlier WHERE coalesce(earlier.task_id,earlier.context_task_id)=coalesce(j.task_id,j.context_task_id) AND earlier.agent_name=j.agent_name AND earlier.id<j.id AND earlier.state IN ('pending','running','archiving')) ORDER BY j.id LIMIT 100`, "jobs_active_session_idx"},
}

func explainQuery(t *testing.T, ctx context.Context, c *pgx.Conn, sql string, args ...any) string {
	t.Helper()
	rows, err := c.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) "+sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

// Opt-in because fixture loading is deliberately much larger than unit tests.
// Run with -v to preserve complete before/after ANALYZE BUFFERS evidence.
func TestQueryIndexPlans(t *testing.T) {
	if os.Getenv("LOOSTER_QUERY_PLANS") != "1" {
		t.Skip("set LOOSTER_QUERY_PLANS=1 for large disposable fixture evidence")
	}
	ctx, url, c := queryPlanDatabase(t)
	seedQueryPlans(t, ctx, c)
	var version string
	if err := c.QueryRow(ctx, "SELECT version()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Log(version)
	for _, q := range measuredQueries {
		_ = explainQuery(t, ctx, c, q.sql) // Warm both phases once, without forcing planner choices.
		t.Logf("BEFORE %s\n%s", q.name, explainQuery(t, ctx, c, q.sql))
	}
	if err := Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	assertQueryIndexes(t, ctx, c)
	for _, idx := range queryIndexDefinitions {
		var bytes int64
		if err := c.QueryRow(ctx, `SELECT pg_relation_size($1::regclass)`, idx.name).Scan(&bytes); err != nil {
			t.Fatal(err)
		}
		t.Logf("INDEX BYTES %s %d", idx.name, bytes)
	}
	for _, q := range measuredQueries {
		_ = explainQuery(t, ctx, c, q.sql)
		plan := explainQuery(t, ctx, c, q.sql)
		t.Logf("AFTER %s\n%s", q.name, plan)
		if q.index != "" && !strings.Contains(plan, q.index) {
			t.Errorf("%s did not use %s", q.name, q.index)
		}
	}
}

// Exercise the complete board query rather than treating an isolated activity
// count as evidence for all-task board latency. Read the authoritative sqlc SQL.
func TestQueryPlansTaskListAndGenericLogs(t *testing.T) {
	if os.Getenv("LOOSTER_QUERY_PLANS") != "1" {
		t.Skip("set LOOSTER_QUERY_PLANS=1 for large disposable fixture evidence")
	}
	ctx, url, c := queryPlanDatabase(t)
	seedQueryPlans(t, ctx, c)
	source, err := os.ReadFile("queries/tasks.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(source), "-- name: ListTasks :many\n", 2)
	if len(parts) != 2 {
		t.Fatal("ListTasks SQL not found")
	}
	sql := strings.TrimSpace(strings.SplitN(parts[1], "-- name:", 2)[0])
	for _, phase := range []string{"BEFORE", "AFTER"} {
		if phase == "AFTER" {
			if err = Migrate(ctx, url); err != nil {
				t.Fatal(err)
			}
		}
		_ = explainQuery(t, ctx, c, sql)
		plan := explainQuery(t, ctx, c, sql)
		t.Logf("%s full ListTasks (100000 tasks)\n%s", phase, plan)
		for _, index := range []string{"events_task_activity_idx", "events_task_last_activity"} {
			if !strings.Contains(plan, index) {
				t.Errorf("%s full ListTasks missing %s", phase, index)
			}
		}
	}
	queryPlanExec(t, ctx, c, `SET plan_cache_mode=force_generic_plan; SET client_encoding='UTF8'`)
	// Historical SQL kept here only as a measured regression baseline. The new
	// query is built by logQuery itself, so shape regressions affect this test.
	legacy := `SELECT id,task_id,kind,agent_name,body,created_at,actor_agent FROM events
 WHERE id>$1 AND ($2::bigint=0 OR id<=$2) AND ($3::bigint=0 OR task_id=$3)
 AND ($4::text='' OR agent_name=$4 OR actor_agent=$4)
 AND (cardinality($5::text[])=0 OR kind=ANY($5))
 AND ($6::timestamptz IS NULL OR created_at>=$6) ORDER BY id `
	for _, desc := range []bool{false, true} {
		direction := "ASC"
		if desc {
			direction = "DESC"
		}
		oldName := "legacy_log_" + strings.ToLower(direction)
		newName := "selected_log_" + strings.ToLower(direction)
		query, args := logQuery(event.Filter{TaskID: 777, Limit: 500, Desc: desc})
		queryPlanExec(t, ctx, c, "PREPARE "+oldName+"(bigint,bigint,bigint,text,text[],timestamptz,integer) AS "+legacy+direction+" LIMIT $7")
		queryPlanExec(t, ctx, c, "PREPARE "+newName+" AS "+query)
		oldSQL := "EXECUTE " + oldName + "(0,0,777,'','{}',NULL,500)"
		placeholders := make([]string, len(args))
		for i := range args {
			placeholders[i] = fmt.Sprintf("$%d", i+1)
		}
		newSQL := "EXECUTE " + newName + "(" + strings.Join(placeholders, ",") + ")"
		// EXECUTE utility statements cannot bind extended-query parameters. Simple
		// protocol safely interpolates the same values returned by logQuery.
		bind := append([]any{pgx.QueryExecModeSimpleProtocol}, args...)
		_ = explainQuery(t, ctx, c, oldSQL)
		before := explainQuery(t, ctx, c, oldSQL)
		_ = explainQuery(t, ctx, c, newSQL, bind...)
		after := explainQuery(t, ctx, c, newSQL, bind...)
		t.Logf("GENERIC LEGACY LOG %s\n%s", direction, before)
		t.Logf("GENERIC SELECTED LOG %s\n%s", direction, after)
		if !strings.Contains(after, "events_task_id_idx") {
			t.Errorf("generic selected log %s lost task index", direction)
		}
		oldRows := queryPlanRows(t, ctx, c, oldSQL)
		newRows := queryPlanRows(t, ctx, c, newSQL, bind...)
		if len(oldRows) != 2 || !reflect.DeepEqual(oldRows, newRows) {
			t.Errorf("log rows/order changed: old=%v new=%v", oldRows, newRows)
		}
	}
}

func queryPlanRows(t *testing.T, ctx context.Context, c *pgx.Conn, sql string, args ...any) [][]any {
	t.Helper()
	rows, err := c.Query(ctx, sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result [][]any
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, values)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertQueryIndexes(t *testing.T, ctx context.Context, c *pgx.Conn) {
	t.Helper()
	for _, idx := range queryIndexDefinitions {
		var valid, ready bool
		var definition string
		var predicate string
		err := c.QueryRow(ctx, `SELECT i.indisvalid,i.indisready,pg_get_indexdef(i.indexrelid),coalesce(pg_get_expr(i.indpred,i.indrelid),'') FROM pg_index i JOIN pg_class x ON x.oid=i.indexrelid JOIN pg_namespace n ON n.oid=x.relnamespace WHERE n.nspname=current_schema() AND x.relname=$1`, idx.name).Scan(&valid, &ready, &definition, &predicate)
		if err != nil || !valid || !ready {
			t.Fatalf("%s valid=%v ready=%v error=%v definition=%s", idx.name, valid, ready, err, definition)
		}
		var schema string
		if err := c.QueryRow(ctx, "SELECT current_schema()").Scan(&schema); err != nil {
			t.Fatal(err)
		}
		canonical := strings.SplitN(definition, " WHERE ", 2)[0]
		canonical = strings.TrimPrefix(canonical, "CREATE INDEX "+idx.name+" ON "+pgx.Identifier{schema}.Sanitize()+".")
		canonical = strings.TrimPrefix(canonical, "CREATE INDEX "+idx.name+" ON "+schema+".")
		canonical = strings.ReplaceAll(canonical, " USING btree ", "")
		canonical = strings.ReplaceAll(canonical, " ", "")
		// pg_get_indexdef omits the redundant expression parentheses.
		expected := strings.SplitN(idx.definition, " WHERE ", 2)[0]
		expected = strings.ReplaceAll(expected, "(coalesce(task_id,context_task_id))", "COALESCE(task_id,context_task_id)")
		if canonical != expected {
			t.Errorf("%s definition=%s expected=%s", idx.name, canonical, expected)
		}
		wantPredicate := ""
		if idx.name == "jobs_active_session_idx" {
			wantPredicate = "(state = ANY (ARRAY['pending'::text, 'running'::text, 'archiving'::text]))"
		}
		if predicate != wantPredicate {
			t.Errorf("%s predicate=%q want=%q", idx.name, predicate, wantPredicate)
		}
	}
}

func TestQueryIndexesFreshNamespace(t *testing.T) {
	ctx, s := manifestTestStore(t)
	c, err := s.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Release()
	assertQueryIndexes(t, ctx, c.Conn())
}

func TestQueryIndexesUpgradeAndRetry(t *testing.T) {
	for _, name := range []string{"default", "index_retry"} {
		t.Run(name, func(t *testing.T) {
			ctx, url, c := queryPlanNamespaceDatabase(t, name)
			if name != "default" {
				queryPlanExec(t, ctx, c, `CREATE TABLE public.index_sentinel(id int); CREATE INDEX sessions_agent_idx ON public.index_sentinel(id);`)
			}
			// A failed concurrent UNIQUE build leaves an invalid index. A retry must not
			// mistake this name for a usable index (CREATE IF NOT EXISTS would do so).
			queryPlanExec(t, ctx, c, `INSERT INTO agents(name,description) VALUES('fixture','fixture'); INSERT INTO tasks(name,description) VALUES('one',''),('two',''); INSERT INTO sessions(task_id,agent_name) SELECT id,'fixture' FROM tasks;`)
			_, err := c.Exec(ctx, `CREATE UNIQUE INDEX CONCURRENTLY sessions_agent_idx ON sessions(agent_name)`)
			if err == nil {
				t.Fatal("expected failed concurrent build")
			}
			var valid bool
			if err = c.QueryRow(ctx, `SELECT indisvalid FROM pg_index WHERE indexrelid='sessions_agent_idx'::regclass`).Scan(&valid); err != nil || valid {
				t.Fatalf("expected invalid index: %v %v", valid, err)
			}
			// A valid earlier index also models partial migration completion.
			queryPlanExec(t, ctx, c, "CREATE INDEX CONCURRENTLY task_tags_tag_idx ON task_tags(tag_id)")
			failQueryIndexMigration(t, ctx, c, name)
			if err = MigrateNamespace(ctx, url, name); err != nil {
				t.Fatal(err)
			}
			assertQueryIndexes(t, ctx, c)
			if err = MigrateNamespace(ctx, url, name); err != nil {
				t.Fatal(err)
			}
			assertQueryIndexes(t, ctx, c)
			if name != "default" {
				var table string
				if err = c.QueryRow(ctx, `SELECT indrelid::regclass::text FROM pg_index WHERE indexrelid='public.sessions_agent_idx'::regclass`).Scan(&table); err != nil || table != "public.index_sentinel" {
					t.Fatalf("migration touched public index: %s %v", table, err)
				}
			}
			var count int
			if err = c.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&count); err != nil || count != 2 {
				t.Fatalf("upgrade lost data: %d %v", count, err)
			}
		})
	}
}

func failQueryIndexMigration(t *testing.T, ctx context.Context, c *pgx.Conn, name string) {
	t.Helper()
	blocker, err := c.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(ctx, "LOCK TABLE sessions IN SHARE MODE"); err != nil {
		t.Fatal(err)
	}
	cfg := c.Config().Copy()
	cfg.RuntimeParams["lock_timeout"] = "50ms"
	d := stdlib.OpenDB(*cfg)
	defer d.Close()
	schema, _ := namespaceSchema(name)
	source, err := namespaceMigrations(mustSub(), schema)
	if err != nil {
		t.Fatal(err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, d, source)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Up(ctx)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("expected concurrent DDL lock timeout, got %v", err)
	}
	if err = blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// Earlier nontransactional statements must survive, but version 16 must not
	// be recorded. The retry will rebuild these valid indexes and the invalid one.
	var valid bool
	if err = c.QueryRow(ctx, `SELECT indisvalid FROM pg_index WHERE indexrelid='memories_agent_id_idx'::regclass`).Scan(&valid); err != nil || !valid {
		t.Fatalf("earlier DDL not persisted: valid=%v err=%v", valid, err)
	}
	var version int
	if err = c.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&version); err != nil || version != 15 {
		t.Fatalf("failed migration recorded: version=%d err=%v", version, err)
	}
}

func TestQueryIndexesRollbackAndReapply(t *testing.T) {
	for _, name := range []string{"default", "index_rollback"} {
		t.Run(name, func(t *testing.T) {
			ctx, url, c := queryPlanNamespaceDatabase(t, name)
			if err := MigrateNamespace(ctx, url, name); err != nil {
				t.Fatal(err)
			}
			assertQueryIndexes(t, ctx, c)
			schema, _ := namespaceSchema(name)
			source, err := namespaceMigrations(mustSub(), schema)
			if err != nil {
				t.Fatal(err)
			}
			d := stdlib.OpenDB(*c.Config())
			defer d.Close()
			p, err := goose.NewProvider(goose.DialectPostgres, d, source)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.DownTo(ctx, 15); err != nil {
				t.Fatal(err)
			}
			for _, idx := range queryIndexDefinitions {
				var absent bool
				if err = c.QueryRow(ctx, `SELECT to_regclass($1) IS NULL`, idx.name).Scan(&absent); err != nil || !absent {
					t.Errorf("rollback retained %s: %v", idx.name, err)
				}
			}
			if err = MigrateNamespace(ctx, url, name); err != nil {
				t.Fatal(err)
			}
			assertQueryIndexes(t, ctx, c)
		})
	}
}

func TestQueryIndexesFreshDefault(t *testing.T) {
	ctx := context.Background()
	url := legacyUpgradeDatabase(t)
	if err := Migrate(ctx, url); err != nil {
		t.Fatal(err)
	}
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	assertQueryIndexes(t, ctx, c)
}
