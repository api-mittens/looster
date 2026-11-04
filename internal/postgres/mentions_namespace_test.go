package postgres

import (
	"testing"

	"github.com/api-mittens/looster/internal/task"
	"github.com/jackc/pgx/v5"
)

func TestCommentMentionNamespaceAndRollback(t *testing.T) {
	ctx, s := manifestTestStore(t)
	_, other := manifestTestStore(t)
	if err := s.CreateAgent(ctx, "local", "Local"); err != nil {
		t.Fatal(err)
	}
	if err := other.CreateAgent(ctx, "foreign", "Other namespace"); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateTask(ctx, "isolated", "", task.Planning, nil)
	if err != nil {
		t.Fatal(err)
	}
	eid, err := s.Comment(ctx, created.ID, "@local @foreign")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	if err := s.Pool.QueryRow(ctx, `SELECT array_agg(agent_name ORDER BY agent_name) FROM jobs WHERE event_id=$1`, eid).Scan(&names); err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "local" {
		t.Fatalf("cross-namespace mention: %v", names)
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var schema string
	if err := tx.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	// Trigger lookups remain local even for callers using a different path.
	if _, err := tx.Exec(ctx, `SET LOCAL search_path=pg_catalog`); err != nil {
		t.Fatal(err)
	}
	query := `INSERT INTO ` + pgx.Identifier{schema, "events"}.Sanitize() + `(task_id,kind,body) VALUES($1,'comment','@local @foreign') RETURNING id`
	if err := tx.QueryRow(ctx, query, created.ID).Scan(&eid); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+pgx.Identifier{schema, "jobs"}.Sanitize()+` WHERE event_id=$1`, eid).Scan(&n); err != nil || n != 1 {
		t.Fatalf("pinned namespace jobs=%d err=%v", n, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE event_id=$1`, eid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rollback jobs=%d err=%v", n, err)
	}
}
