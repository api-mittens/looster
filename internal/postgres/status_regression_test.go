package postgres

import (
	"context"
	"testing"

	"github.com/api-mittens/looster/internal/task"
)

func TestSetStatusStopsPendingJobsAfterPayloadCutover(t *testing.T) {
	cases := []struct {
		name   string
		status task.Status
		direct bool
	}{
		{name: "store_validated", status: task.Validated},
		{name: "store_closed", status: task.Closed},
		{name: "sql_validated", status: task.Validated, direct: true},
		{name: "sql_closed", status: task.Closed, direct: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, s := manifestTestStore(t)
			if _, err := s.ApplyManifest(ctx, `AGENT coder WITH "Do the work"; ON TODO PING coder; AFTER VALIDATED STOP PINGING coder; AFTER CLOSED STOP PINGING coder;`); err != nil {
				t.Fatal(err)
			}
			item, err := s.CreateTask(ctx, "Status test", "", task.Todo, nil)
			if err != nil {
				t.Fatal(err)
			}
			var jobID int64
			if err := s.Pool.QueryRow(ctx, `SELECT id FROM jobs WHERE task_id=$1 AND agent_name='coder' AND state='pending'`, item.ID).Scan(&jobID); err != nil {
				t.Fatal(err)
			}
			s.SetPayloadStore(&backfillTestPayloads{})
			if err := s.EnablePayloadCutover(ctx); err != nil {
				t.Fatal(err)
			}

			if tc.direct {
				_, err = s.Pool.Exec(ctx, `UPDATE tasks SET status=$2 WHERE id=$1`, item.ID, string(tc.status))
			} else {
				err = s.SetStatus(ctx, item.ID, tc.status)
			}
			if err != nil {
				t.Fatalf("cannot set status to %s: %v", tc.status, err)
			}
			var gotStatus task.Status
			if err := s.Pool.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1`, item.ID).Scan(&gotStatus); err != nil {
				t.Fatal(err)
			}
			if gotStatus != tc.status {
				t.Errorf("status = %s, want %s", gotStatus, tc.status)
			}
			assertStoppedJobAfterPayloadCutover(t, ctx, s, jobID)
			var count int
			if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE task_id=$1 AND kind='status_changed' AND body=$2`, item.ID, string(tc.status)).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Errorf("status event count = %d, want 1", count)
			}
			if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET error='Inline test error' WHERE id=$1`, jobID); err == nil {
				t.Error("payload fence accepted an inline job error")
			}
		})
	}
}

func TestTagChangesStopPendingJobsAfterPayloadCutover(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		tags     []string
		query    string
	}{
		{
			name:     "add_tag",
			manifest: `AGENT coder WITH "Do the work"; TAG stop WITH "Stop the work"; ON TODO PING coder; AFTER TODO STOP PINGING coder WHEN TAGGED stop;`,
			query:    `INSERT INTO task_tags(task_id,tag_id) SELECT $1,id FROM tags WHERE name='stop'`,
		},
		{
			name:     "remove_tag",
			manifest: `AGENT coder WITH "Do the work"; TAG work WITH "Do the work"; ON TODO PING coder; AFTER TODO STOP PINGING coder WHEN NOT TAGGED work;`,
			tags:     []string{"work"},
			query:    `DELETE FROM task_tags WHERE task_id=$1 AND tag_id=(SELECT id FROM tags WHERE name='work')`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, s := manifestTestStore(t)
			if _, err := s.ApplyManifest(ctx, tc.manifest); err != nil {
				t.Fatal(err)
			}
			item, err := s.CreateTask(ctx, "Tag test", "", task.Todo, tc.tags)
			if err != nil {
				t.Fatal(err)
			}
			var jobID, before int64
			if err := s.Pool.QueryRow(ctx, `SELECT id FROM jobs WHERE task_id=$1 AND agent_name='coder' AND state='pending'`, item.ID).Scan(&jobID); err != nil {
				t.Fatal(err)
			}
			if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE task_id=$1 AND kind='tags_changed'`, item.ID).Scan(&before); err != nil {
				t.Fatal(err)
			}
			s.SetPayloadStore(&backfillTestPayloads{})
			if err := s.EnablePayloadCutover(ctx); err != nil {
				t.Fatal(err)
			}
			result, err := s.Pool.Exec(ctx, tc.query, item.ID)
			if err != nil {
				t.Fatalf("cannot change task tags: %v", err)
			}
			if result.RowsAffected() != 1 {
				t.Fatalf("tag row count = %d, want 1", result.RowsAffected())
			}
			assertStoppedJobAfterPayloadCutover(t, ctx, s, jobID)
			var after int64
			var status task.Status
			if err := s.Pool.QueryRow(ctx, `SELECT status FROM tasks WHERE id=$1`, item.ID).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status != task.Todo {
				t.Errorf("status = %s, want todo", status)
			}
			if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE task_id=$1 AND kind='tags_changed'`, item.ID).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if after != before+1 {
				t.Errorf("tag event count = %d, want %d", after, before+1)
			}
		})
	}
}

func assertStoppedJobAfterPayloadCutover(t *testing.T, ctx context.Context, s *Store, jobID int64) {
	t.Helper()
	var state, cause, payloadError string
	if err := s.Pool.QueryRow(ctx, `SELECT state,stop_reason,error FROM jobs WHERE id=$1`, jobID).Scan(&state, &cause, &payloadError); err != nil {
		t.Fatal(err)
	}
	if state != "failed" {
		t.Errorf("job state = %s, want failed", state)
	}
	if cause != "ping stopped by behaviour" {
		t.Errorf("stop reason = %q, want %q", cause, "ping stopped by behaviour")
	}
	if payloadError != "" {
		t.Errorf("job error = %q, want an empty value", payloadError)
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO sessions(task_id,agent_name) SELECT task_id,agent_name FROM jobs WHERE id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	sessions, err := s.Sessions(ctx, "coder")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].JobID != jobID || sessions[0].Error != cause {
		t.Errorf("session stop cause = %+v, want job %d with cause %q", sessions, jobID, cause)
	}
	report, err := s.VerifyPayloads(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete || report.Legacy != 0 {
		t.Errorf("payload check = %+v, want a complete check with no legacy data", report)
	}
}
