package cli

import (
	"context"
	"fmt"
	"testing"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/postgres"
	"github.com/api-mittens/looster/internal/task"
)

// Use the configured disposable PG/S3 store, not a payload stub. Each lease
// follows the same capture, archive, and completion path as a serve agent.
func storageConcurrentAgentRuns(t *testing.T, ctx context.Context, s *postgres.Store) []int64 {
	t.Helper()
	const count = 8
	jobs := make([]int64, count)
	leases := make([]app.Lease, 0, count)
	for i := range jobs {
		created, err := s.CreateTask(ctx, fmt.Sprintf("Concurrent lake run %d", i), "", task.Todo, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = s.Pool.QueryRow(ctx, `WITH e AS (INSERT INTO events(kind,agent_name) VALUES('scheduled','storage_test') RETURNING id) INSERT INTO jobs(task_id,agent_name,event_id,state) SELECT $1,'storage_test',id,'pending' FROM e RETURNING id`, created.ID).Scan(&jobs[i])
		if err != nil {
			t.Fatal(err)
		}
		lease, err := s.Claim(ctx, []string{"storage_test"})
		if err != nil || lease == nil {
			t.Fatalf("claim concurrent run: %v", err)
		}
		leases = append(leases, lease)
	}
	defer func() {
		for _, lease := range leases {
			lease.Close()
		}
	}()
	results := make(chan error, count)
	for i, lease := range leases {
		go func(i int, lease app.Lease) {
			if err := lease.Started(ctx); err != nil {
				results <- err
				return
			}
			sid := fmt.Sprintf("concurrent-session-%d", i)
			for n := 0; n < 3; n++ {
				text := fmt.Sprintf(`{"type":"text","text":"Run %d output %d"}`, i, n)
				if err := lease.Capture(ctx, app.Output{SessionID: sid, Text: text}); err != nil {
					results <- err
					return
				}
			}
			summary := fmt.Sprintf(`{"type":"done","text":"Concurrent run %d completed."}`, i)
			if err := lease.Capture(ctx, app.Output{SessionID: sid, Text: summary}); err != nil {
				results <- err
				return
			}
			history := []byte(fmt.Sprintf(`{"session_id":%q,"summary":"Concurrent run %d completed."}`, sid, i))
			if err := lease.Archive(ctx, app.Result{SessionID: sid, History: history}); err != nil {
				results <- err
				return
			}
			results <- lease.Complete(ctx)
		}(i, lease)
	}
	for range count {
		if err := <-results; err != nil {
			t.Errorf("concurrent agent completion: %v", err)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	return jobs
}

func storageCheckConcurrentRuns(t *testing.T, ctx context.Context, s *postgres.Store, jobs []int64) {
	t.Helper()
	for i, job := range jobs {
		run, err := s.SessionRun(ctx, job)
		expected := fmt.Sprintf("Concurrent run %d completed.", i)
		if err != nil || run.Outcome != "done" || run.Summary != expected {
			t.Fatalf("concurrent run %d lost outcome or summary: %+v %v", job, run, err)
		}
		output, err := s.SessionRunOutput(ctx, job, 0, 100)
		if err != nil || len(output) != 4 {
			t.Fatalf("concurrent run %d lost output: count=%d err=%v", job, len(output), err)
		}
	}
}
