package postgres

import (
	"errors"
	"testing"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/task"
)

func TestSessionLogsDurableAttribution(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	created, err := s.CreateTask(ctx, "log task", "", task.Planning, nil)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, taskID := range []any{created.ID, nil, nil} {
		id := queuePerformanceJob(t, ctx, s, taskID, nil, "queue_test", "pending", nil)
		ids = append(ids, id)
		l := historyClaim(t, ctx, s)
		if err := l.Started(ctx); err != nil {
			t.Fatal(err)
		}
		if err := l.Capture(ctx, app.Output{SessionID: "shared", Text: "output"}); err != nil {
			t.Fatal(err)
		}
		for _, kind := range []string{"question", "blocker", "custom-note"} {
			got, err := s.AppendSessionLog(ctx, agent.SessionLogInput{JobID: id, Agent: "queue_test", SessionID: "shared", Kind: kind, Body: " <script>historical</script> "})
			if err != nil || got.JobID != id || got.SessionID != "shared" || got.Kind != kind || got.CreatedAt.IsZero() {
				t.Fatalf("append: %+v %v", got, err)
			}
			if taskID == nil && got.TaskID != 0 {
				t.Fatalf("taskless log has task: %+v", got)
			}
			if taskID != nil && got.TaskID != created.ID {
				t.Fatalf("wrong task: %+v", got)
			}
		}
		live, err := s.SessionRun(ctx, id)
		if err != nil || len(live.Logs) != 3 {
			t.Fatalf("live: %+v %v", live, err)
		}
		if err := l.Archive(ctx, app.Result{SessionID: "shared"}); err != nil {
			t.Fatal(err)
		}
		if err := l.Complete(ctx); err != nil {
			t.Fatal(err)
		}
		l.Close()
	}
	rows, err := s.TaskSessionLogs(ctx, created.ID)
	if err != nil || len(rows) != 3 {
		t.Fatalf("task logs: %+v %v", rows, err)
	}
	got, err := s.Task(ctx, created.ID)
	if err != nil || len(got.SessionLogs) != 3 {
		t.Fatalf("task detail: %+v %v", got, err)
	}
	runs, err := s.SessionHistory(ctx, "queue_test", 0, 0, 10)
	if err != nil || len(runs) != 3 {
		t.Fatalf("history: %+v %v", runs, err)
	}
	for _, r := range runs {
		if len(r.Logs) != 3 {
			t.Fatalf("history logs: %+v", r)
		}
	}
	for _, in := range []agent.SessionLogInput{
		{JobID: ids[0], Agent: "queue_other", Kind: "question", Body: "wrong agent"},
		{JobID: ids[0], Agent: "queue_test", SessionID: "foreign", Kind: "question", Body: "wrong session"},
		{JobID: ids[1], Agent: "queue_test", TaskID: created.ID, Kind: "question", Body: "wrong task"},
		{JobID: 99999999, Agent: "queue_test", Kind: "question", Body: "unknown job"},
	} {
		if _, err := s.AppendSessionLog(ctx, in); !errors.Is(err, app.ErrInvalidSessionLog) {
			t.Fatalf("accepted forged identity %+v: %v", in, err)
		}
	}
	for _, sql := range []string{`UPDATE session_logs SET body='changed' WHERE job_id=$1`, `DELETE FROM session_logs WHERE job_id=$1`} {
		if _, err := s.Pool.Exec(ctx, sql, ids[0]); err == nil {
			t.Fatal("history mutation accepted")
		}
	}
	if err := s.Bind(ctx, created.ID, "queue_test", "new-session"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveTask(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM jobs WHERE id=ANY($1)`, ids); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		run, err := s.SessionRun(ctx, id)
		if err != nil || len(run.Logs) != 3 || run.Logs[0].SessionID != "shared" {
			t.Fatalf("durability: %+v %v", run, err)
		}
	}
	_, other := queuePerformanceStore(t)
	if rows, err := other.RunSessionLogs(ctx, ids[0]); err != nil || len(rows) != 0 {
		t.Fatalf("cross namespace read %+v %v", rows, err)
	}
	if _, err := other.AppendSessionLog(ctx, agent.SessionLogInput{JobID: ids[0], Agent: "queue_test", Kind: "blocker", Body: "secret"}); !errors.Is(err, app.ErrInvalidSessionLog) {
		t.Fatalf("cross namespace append: %v", err)
	}
}

func TestSessionLogsRefuseMissingSession(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	id := queuePerformanceJob(t, ctx, s, nil, nil, "queue_test", "pending", nil)
	if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET session_id='' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendSessionLog(ctx, agent.SessionLogInput{JobID: id, Agent: "queue_test", SessionID: "invented", Kind: "question", Body: "body"}); !errors.Is(err, app.ErrInvalidSessionLog) {
		t.Fatalf("invented session accepted: %v", err)
	}
}

func TestSessionLogsContextTaskAttribution(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	created, err := s.CreateTask(ctx, "context", "", task.Planning, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := queuePerformanceJob(t, ctx, s, nil, created.ID, "queue_test", "pending", nil)
	log, err := s.AppendSessionLog(ctx, agent.SessionLogInput{JobID: id, Agent: "queue_test", TaskID: created.ID, Kind: "question", Body: "Context question"})
	if err != nil || log.TaskID != created.ID {
		t.Fatalf("context attribution: %+v %v", log, err)
	}
	rows, err := s.TaskSessionLogs(ctx, created.ID)
	if err != nil || len(rows) != 1 || rows[0].JobID != id {
		t.Fatalf("context task logs: %+v %v", rows, err)
	}
}
