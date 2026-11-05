package postgres

import (
	"fmt"
	"testing"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/task"
)

func TestSessionRunOutputExactIdentity(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	created, err := s.CreateTask(ctx, "output runs", "", task.Planning, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Legacy output must never be attributed to any run, even on the same task/agent.
	if _, err := s.Pool.Exec(ctx, `INSERT INTO events(task_id,kind,agent_name,body) VALUES($1,'agent_output','queue_test','legacy unowned output')`, created.ID); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i, taskID := range []any{created.ID, created.ID, nil, nil} {
		id := queuePerformanceJob(t, ctx, s, taskID, nil, "queue_test", "pending", nil)
		ids = append(ids, id)
		l := historyClaim(t, ctx, s)
		if err := l.Started(ctx); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 3; j++ {
			body := fmt.Sprintf("run %d output %d", i, j)
			if err := l.Capture(ctx, app.Output{SessionID: "shared-session", Text: body}); err != nil {
				t.Fatal(err)
			}
		}
		// Pending/running output is inspectable before archive/completion.
		page, err := s.SessionRunOutput(ctx, id, 0, 2)
		if err != nil || len(page) != 2 || page[0].Body != fmt.Sprintf("run %d output 0", i) || page[1].ID <= page[0].ID {
			t.Fatalf("live page=%+v %v", page, err)
		}
		tail, err := s.SessionRunOutput(ctx, id, page[1].ID, 2)
		if err != nil || len(tail) != 1 || tail[0].Body != fmt.Sprintf("run %d output 2", i) {
			t.Fatalf("tail=%+v %v", tail, err)
		}
		if taskID == nil && tail[0].TaskID != nil {
			t.Fatalf("taskless event has task: %+v", tail[0])
		}
		if err := l.Archive(ctx, app.Result{SessionID: "shared-session"}); err != nil {
			t.Fatal(err)
		}
		if err := l.Complete(ctx); err != nil {
			t.Fatal(err)
		}
		l.Close()
	}
	// Interleaved non-output events cannot enter the transcript, even with job identity.
	if _, err := s.Pool.Exec(ctx, `INSERT INTO events(kind,agent_name,body,job_id) VALUES('system','queue_test','not output',$1)`, ids[0]); err != nil {
		t.Fatal(err)
	}
	// No FK to jobs: completed transcript remains available after mutable job deletion.
	if _, err := s.Pool.Exec(ctx, `DELETE FROM jobs WHERE id=ANY($1)`, ids); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		rows, err := s.SessionRunOutput(ctx, id, 0, 0)
		if err != nil || len(rows) != 3 {
			t.Fatalf("job %d output=%+v %v", id, rows, err)
		}
		for j, e := range rows {
			if e.Body != fmt.Sprintf("run %d output %d", i, j) || e.Kind != "agent_output" {
				t.Fatalf("run mixing: %+v", e)
			}
		}
	}
	// A job that existed before capture attribution has explicitly empty output.
	legacy := queuePerformanceJob(t, ctx, s, created.ID, nil, "queue_test", "pending", nil)
	rows, err := s.SessionRunOutput(ctx, legacy, 0, 100)
	if err != nil || len(rows) != 0 {
		t.Fatalf("legacy attribution: %+v %v", rows, err)
	}
	// The store bounds oversized requests to the API's 200 rows plus lookahead.
	if _, err := s.Pool.Exec(ctx, `INSERT INTO events(kind,agent_name,body,job_id) SELECT 'agent_output','queue_test','bounded output',$1 FROM generate_series(1,202)`, legacy); err != nil {
		t.Fatal(err)
	}
	rows, err = s.SessionRunOutput(ctx, legacy, 0, 10000)
	if err != nil || len(rows) != 201 {
		t.Fatalf("output cap: count=%d err=%v", len(rows), err)
	}
	_, other := queuePerformanceStore(t)
	otherID := queuePerformanceJob(t, ctx, other, nil, nil, "queue_test", "pending", nil)
	if otherID != ids[0] {
		t.Fatalf("test needs overlapping namespace job IDs: %d != %d", otherID, ids[0])
	}
	rows, err = other.SessionRunOutput(ctx, otherID, 0, 100)
	if err != nil || len(rows) != 0 {
		t.Fatalf("namespace leak: %+v %v", rows, err)
	}
	for _, bounds := range [][2]int64{{0, 0}, {ids[0], -1}} {
		if _, err := s.SessionRunOutput(ctx, bounds[0], bounds[1], 100); err == nil {
			t.Fatal("invalid bounds accepted")
		}
	}
}
