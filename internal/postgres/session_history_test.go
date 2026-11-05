package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/task"
)

func TestCapturedSummary(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{"type":"done","text":" final result "}`, "final result"},
		{`{"type":"message","text":"not final"}`, ""},
		{`{"type":"done"}`, ""}, {"raw text", ""}, {`{"type":"done","text":42}`, ""},
	} {
		if got := capturedSummary(tc.in); got != tc.want {
			t.Errorf("%s: %q != %q", tc.in, got, tc.want)
		}
	}
}

func historyClaim(t *testing.T, ctx context.Context, s *Store) app.Lease {
	t.Helper()
	l, err := s.Claim(ctx, []string{"queue_test"})
	if err != nil || l == nil {
		t.Fatalf("claim: %v %v", l, err)
	}
	return l
}

func TestSessionHistoryCompletionAndArchiveResume(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	first := queuePerformanceJob(t, ctx, s, nil, nil, "queue_test", "pending", nil)
	l := historyClaim(t, ctx, s)
	if err := l.Started(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.Capture(ctx, app.Output{SessionID: "shared", Text: `{"type":"done","text":"first summary"}`}); err != nil {
		t.Fatal(err)
	}
	if err := l.Archive(ctx, app.Result{SessionID: "shared", History: []byte("archive")}); err != nil {
		t.Fatal(err)
	}
	if err := l.Fail(ctx, errors.New("object storage unavailable")); err != nil {
		t.Fatal(err)
	}
	l.Close()
	if _, err := s.SessionRun(ctx, first); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("retry finalized early: %v", err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET available_at=now() WHERE id=$1`, first); err != nil {
		t.Fatal(err)
	}
	l = historyClaim(t, ctx, s)
	if l.Job().State != "archiving" {
		t.Fatalf("state %s", l.Job().State)
	}
	if err := l.Complete(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.Complete(ctx); err != nil {
		t.Fatal(err)
	}
	l.Close()
	r, err := s.SessionRun(ctx, first)
	if err != nil || r.Summary != "first summary" || r.SummaryStatus != "available" || r.SessionID != "shared" || r.Outcome != "done" || r.StartedAt.IsZero() || r.FinishedAt.Before(r.StartedAt) {
		t.Fatalf("run=%+v err=%v", r, err)
	}
	second := queuePerformanceJob(t, ctx, s, nil, nil, "queue_test", "pending", nil)
	l = historyClaim(t, ctx, s)
	if err := l.Started(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.Archive(ctx, app.Result{SessionID: "shared"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Complete(ctx); err != nil {
		t.Fatal(err)
	}
	l.Close()
	runs, err := s.SessionHistory(ctx, "queue_test", 0, 0, 1)
	if err != nil || len(runs) != 1 || runs[0].JobID != second || runs[0].SummaryStatus != "missing" {
		t.Fatalf("page one=%+v %v", runs, err)
	}
	runs, err = s.SessionHistory(ctx, "queue_test", 0, second, 1)
	if err != nil || len(runs) != 1 || runs[0].JobID != first || runs[0].Summary != "first summary" {
		t.Fatalf("page two=%+v %v", runs, err)
	}
	for _, sql := range []string{`UPDATE agent_session_runs SET summary='changed' WHERE job_id=$1`, `DELETE FROM agent_session_runs WHERE job_id=$1`} {
		if _, err := s.Pool.Exec(ctx, sql, first); err == nil {
			t.Fatal("history mutation accepted")
		}
	}
	_, other := queuePerformanceStore(t)
	if _, err := other.SessionRun(ctx, first); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("cross namespace read: %v", err)
	}
}

func TestSessionHistoryRetainsDistinctSuccessiveTasklessSummaries(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	summaries := []string{"first scheduled result", "second scheduled result"}
	var ids []int64
	for _, summary := range summaries {
		id := queuePerformanceJob(t, ctx, s, nil, nil, "queue_test", "pending", nil)
		ids = append(ids, id)
		l := historyClaim(t, ctx, s)
		if l.Job().ID != id {
			t.Fatalf("claimed job %d, want %d", l.Job().ID, id)
		}
		if err := l.Started(ctx); err != nil {
			t.Fatal(err)
		}
		if err := l.Capture(ctx, app.Output{SessionID: "shared-scheduled-session", Text: `{"type":"done","text":"` + summary + `"}`}); err != nil {
			t.Fatal(err)
		}
		if err := l.Archive(ctx, app.Result{SessionID: "shared-scheduled-session"}); err != nil {
			t.Fatal(err)
		}
		if err := l.Complete(ctx); err != nil {
			t.Fatal(err)
		}
		l.Close()
	}
	runs, err := s.SessionHistory(ctx, "queue_test", 0, 0, 10)
	if err != nil || len(runs) != 2 {
		t.Fatalf("retained history=%+v err=%v", runs, err)
	}
	for i, id := range ids {
		listed := runs[len(runs)-1-i]
		detail, err := s.SessionRun(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, run := range []agent.SessionRun{listed, detail} {
			if run.JobID != id || run.Summary != summaries[i] || run.SummaryStatus != "available" || run.Outcome != "done" || run.SessionID != "shared-scheduled-session" || run.TaskID != 0 || run.ContextTaskID != 0 {
				t.Fatalf("job %d lost its distinct taskless summary: %+v", id, run)
			}
		}
	}
}

func TestSessionHistoryFailureInterruptionAndDeletion(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	for _, tc := range []struct {
		name  string
		cause error
		want  string
	}{
		{"failed", errors.New("harness failed"), "failed"},
		{"deadline exceeded", context.DeadlineExceeded, "failed"},
		{"closed", app.ErrTaskClosed, "interrupted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := queuePerformanceJob(t, ctx, s, nil, int64(123), "queue_test", "pending", nil)
			l := historyClaim(t, ctx, s)
			if err := l.Started(ctx); err != nil {
				t.Fatal(err)
			}
			if err := l.Capture(ctx, app.Output{SessionID: "failure-session", Text: `{"type":"start"}`}); err != nil {
				t.Fatal(err)
			}
			if err := l.Fail(ctx, tc.cause); err != nil {
				t.Fatal(err)
			}
			l.Close()
			r, err := s.SessionRun(ctx, id)
			if err != nil || r.Outcome != tc.want || r.SummaryStatus != tc.want || r.Error != tc.cause.Error() || r.SessionID != "failure-session" || r.ContextTaskID != 123 {
				t.Fatalf("run=%+v err=%v", r, err)
			}
			var state string
			if err := s.Pool.QueryRow(ctx, `SELECT state FROM jobs WHERE id=$1`, id).Scan(&state); err != nil || state != "failed" {
				t.Fatalf("terminal job state=%q err=%v", state, err)
			}
		})
	}
	id := queuePerformanceJob(t, ctx, s, nil, nil, "queue_test", "pending", nil)
	l := historyClaim(t, ctx, s)
	if err := l.Started(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.Fail(ctx, context.Canceled); err != nil {
		t.Fatal(err)
	}
	l.Close()
	if _, err := s.SessionRun(ctx, id); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("cancellation froze resumable job: %v", err)
	}
	// Final records are not tied to cascading task or agent foreign keys.
	created, err := s.CreateTask(ctx, "history task", "", task.Planning, nil)
	if err != nil {
		t.Fatal(err)
	}
	taskJob := queuePerformanceJob(t, ctx, s, created.ID, nil, "queue_other", "pending", nil)
	l, err = s.Claim(ctx, []string{"queue_other"})
	if err != nil || l == nil {
		t.Fatalf("claim task: %v", err)
	}
	if err := l.Started(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.Archive(ctx, app.Result{SessionID: "task-session"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Complete(ctx); err != nil {
		t.Fatal(err)
	}
	l.Close()
	if err := s.RemoveTask(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM agents WHERE name='queue_other'`); err != nil {
		t.Fatal(err)
	}
	r, err := s.SessionRun(ctx, taskJob)
	if err != nil || r.TaskID != created.ID {
		t.Fatalf("deleted history=%+v %v", r, err)
	}
	runs, err := s.SessionHistory(ctx, "queue_other", created.ID, 0, 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("task filter: %+v %v", runs, err)
	}
}
