package postgres

import (
	"errors"
	"testing"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/task"
)

func TestSessionsExactRunSummaryAndLiveDetail(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	created, err := s.CreateTask(ctx, "session summaries", "", task.Planning, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Bind(ctx, created.ID, "queue_test", "shared"); err != nil {
		t.Fatal(err)
	}
	check := func(id int64, summary, status, cause string) {
		t.Helper()
		rows, err := s.Sessions(ctx, "queue_test")
		if err != nil || len(rows) != 1 {
			t.Fatalf("sessions=%+v err=%v", rows, err)
		}
		r := rows[0]
		if r.JobID != id || r.Summary != summary || r.SummaryStatus != status || r.Error != cause {
			t.Fatalf("session=%+v want job=%d summary=%q status=%q error=%q", r, id, summary, status, cause)
		}
	}
	check(0, "", "missing", "")
	var ids []int64
	for i, summary := range []string{"first result", "", ""} {
		id := queuePerformanceJob(t, ctx, s, created.ID, nil, "queue_test", "pending", nil)
		ids = append(ids, id)
		check(id, "", "pending", "")
		r, err := s.SessionRun(ctx, id)
		if err != nil || r.Outcome != "pending" || !r.StartedAt.IsZero() || !r.FinishedAt.IsZero() {
			t.Fatalf("pending=%+v %v", r, err)
		}
		l := historyClaim(t, ctx, s)
		if err := l.Started(ctx); err != nil {
			t.Fatal(err)
		}
		if err := l.Capture(ctx, app.Output{SessionID: "shared", Text: `{"type":"done","text":"` + summary + `"}`}); err != nil {
			t.Fatal(err)
		}
		r, err = s.SessionRun(ctx, id)
		if err != nil || r.Outcome != "running" || r.StartedAt.IsZero() || !r.FinishedAt.IsZero() || r.Summary != summary {
			t.Fatalf("running=%+v %v", r, err)
		}
		if i == 0 {
			// A later queued job must not replace the run currently holding the session.
			queued := queuePerformanceJob(t, ctx, s, created.ID, nil, "queue_test", "pending", nil)
			check(id, summary, "available", "")
			if _, err := s.Pool.Exec(ctx, `DELETE FROM jobs WHERE id=$1`, queued); err != nil {
				t.Fatal(err)
			}
		}
		status := "missing"
		cause := ""
		if i == 2 {
			cause = "harness exploded"
			status = "failed"
			if err := l.Fail(ctx, errors.New(cause)); err != nil {
				t.Fatal(err)
			}
		} else {
			if summary != "" {
				status = "available"
			}
			if err := l.Archive(ctx, app.Result{SessionID: "shared"}); err != nil {
				t.Fatal(err)
			}
			if err := l.Complete(ctx); err != nil {
				t.Fatal(err)
			}
		}
		l.Close()
		check(id, summary, status, cause)
	}
	runs, err := s.SessionHistory(ctx, "queue_test", created.ID, 0, 10)
	if err != nil || len(runs) != 3 || runs[2].JobID != ids[0] || runs[2].Summary != "first result" || runs[1].SummaryStatus != "missing" || runs[0].SummaryStatus != "failed" {
		t.Fatalf("durable runs=%+v %v", runs, err)
	}
	// Removing mutable jobs cannot erase the latest run or substitute an older summary.
	if _, err := s.Pool.Exec(ctx, `DELETE FROM jobs WHERE task_id=$1`, created.ID); err != nil {
		t.Fatal(err)
	}
	check(ids[2], "", "failed", "harness exploded")
	if err := s.Bind(ctx, created.ID, "queue_test", "unrelated"); err != nil {
		t.Fatal(err)
	}
	check(0, "", "missing", "")
	// Namespace-local IDs may overlap, but neither live nor durable metadata may leak.
	_, other := queuePerformanceStore(t)
	if _, err := other.SessionRun(ctx, ids[2]); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("namespace leak: %v", err)
	}
}
