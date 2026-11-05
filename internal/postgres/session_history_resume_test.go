package postgres

import (
	"context"
	"testing"

	"github.com/api-mittens/looster/internal/app"
)

func TestSessionHistoryInterruptedExecutionResumesWithoutStaleSummary(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	id := queuePerformanceJob(t, ctx, s, nil, nil, "queue_test", "pending", nil)
	l := historyClaim(t, ctx, s)
	if err := l.Started(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.Capture(ctx, app.Output{SessionID: "old", Text: `{"type":"done","text":"abandoned summary"}`}); err != nil {
		t.Fatal(err)
	}
	if err := l.Fail(ctx, context.Canceled); err != nil {
		t.Fatal(err)
	}
	l.Close()
	if r, err := s.SessionRun(ctx, id); err != nil || r.Outcome != "pending" || !r.FinishedAt.IsZero() {
		t.Fatalf("interrupted live detail: %+v %v", r, err)
	}
	if runs, err := s.SessionHistory(ctx, "queue_test", 0, 0, 10); err != nil || len(runs) != 0 {
		t.Fatalf("interruption finalized job: %+v %v", runs, err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET available_at=now() WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	l = historyClaim(t, ctx, s)
	if l.Job().SessionID != "old" {
		t.Fatalf("session not resumable: %+v", l.Job())
	}
	if err := l.Started(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.Archive(ctx, app.Result{SessionID: "resumed"}); err != nil {
		t.Fatal(err)
	}
	if err := l.Complete(ctx); err != nil {
		t.Fatal(err)
	}
	l.Close()
	r, err := s.SessionRun(ctx, id)
	if err != nil || r.Summary != "" || r.SummaryStatus != "missing" || r.SessionID != "resumed" {
		t.Fatalf("stale summary: %+v %v", r, err)
	}
}
