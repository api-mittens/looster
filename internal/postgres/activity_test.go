package postgres

import (
	"testing"
	"time"
)

func TestTaskLastActivityUsesHistoryNotCreationOrEventID(t *testing.T) {
	ctx, s := manifestTestStore(t)
	created := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	// Only this disposable test schema is changed. Simulate imported legacy tasks
	// with no history before testing the normal append-only event path.
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `ALTER TABLE tasks DISABLE TRIGGER task_event`); err != nil {
		t.Fatal(err)
	}
	var activeID, idleID int64
	for i, id := range []*int64{&activeID, &idleID} {
		if err = tx.QueryRow(ctx, `INSERT INTO tasks(name,description,status,created_at) VALUES($1,'','planning',$2) RETURNING id`, []string{"Old but active", "Old and idle"}[i], created).Scan(id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `ALTER TABLE tasks ENABLE TRIGGER task_event`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	idle, err := s.Task(ctx, idleID)
	if err != nil || !idle.LastActivityAt.Equal(created) {
		t.Fatalf("no-history fallback: %+v, %v", idle, err)
	}
	for _, event := range []struct {
		kind string
		at   time.Time
	}{
		{"status_changed", created},
		{"comment", recent.Add(-time.Hour)},
		{"agent_output", recent},
		{"comment", created.Add(time.Hour)}, // Highest ID is deliberately NOT latest time.
	} {
		if _, err = s.Pool.Exec(ctx, `INSERT INTO events(task_id,kind,body,created_at) VALUES($1,$2,'',$3)`, activeID, event.kind, event.at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.Pool.Exec(ctx, `INSERT INTO events(kind,body,created_at) VALUES('scheduled','standalone',$1)`, recent.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Tasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("tasks: %+v", rows)
	}
	for _, row := range rows {
		want := created
		if row.ID == activeID {
			want = recent
		}
		if !row.LastActivityAt.Equal(want) {
			t.Errorf("task %d last activity %s, want %s", row.ID, row.LastActivityAt, want)
		}
		if !row.CreatedAt.Equal(created) {
			t.Errorf("creation timestamp changed: %+v", row)
		}
		if row.ID == activeID && (row.CommentCount != 2 || row.StatusChanges != 1) {
			t.Errorf("heatmap counts changed: %+v", row)
		}
		detail, err := s.Task(ctx, row.ID)
		if err != nil || !detail.LastActivityAt.Equal(row.LastActivityAt) {
			t.Errorf("list/detail mismatch: %+v, %v", detail, err)
		}
	}
	// A new comment revives a long-idle task without changing its creation age.
	revived := recent.Add(2 * time.Hour)
	if _, err = s.Pool.Exec(ctx, `INSERT INTO events(task_id,kind,body,created_at) VALUES($1,'comment','revived',$2)`, idleID, revived); err != nil {
		t.Fatal(err)
	}
	idle, err = s.Task(ctx, idleID)
	if err != nil || !idle.LastActivityAt.Equal(revived) || !idle.CreatedAt.Equal(created) {
		t.Fatalf("revived task: %+v, %v", idle, err)
	}
	var indexed bool
	if err = s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_indexes WHERE schemaname=current_schema() AND indexname='events_task_last_activity')`).Scan(&indexed); err != nil || !indexed {
		t.Fatalf("activity lookup index missing: %v", err)
	}
}
