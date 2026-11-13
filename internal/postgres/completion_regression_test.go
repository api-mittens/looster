package postgres

import (
	"errors"
	"testing"

	"github.com/api-mittens/looster/internal/app"
)

func TestCompletionDispatchWork(t *testing.T) {
	cases := []struct {
		name                                               string
		normal                                             bool
		kind, actor, agent                                 string
		prior, boundary, closed, failed, deleted, taskless bool
		wantKind                                           string
		wantJobs                                           int
	}{
		{name: "normal", normal: true, wantKind: "agent_stopped", wantJobs: 2},
		{name: "empty", wantKind: "notification_handled"},
		{name: "comment", kind: "comment", actor: "coder", wantKind: "agent_stopped", wantJobs: 2},
		{name: "update", kind: "updated", actor: "coder", wantKind: "agent_stopped", wantJobs: 2},
		{name: "status", kind: "status_changed", actor: "coder", wantKind: "agent_stopped", wantJobs: 2},
		{name: "tags", kind: "tags_changed", actor: "coder", wantKind: "agent_stopped", wantJobs: 2},
		{name: "attachment_added", kind: "attachment_added", actor: "coder", wantKind: "agent_stopped", wantJobs: 2},
		{name: "attachment_updated", kind: "attachment_updated", actor: "coder", wantKind: "agent_stopped", wantJobs: 2},
		{name: "attachment_removed", kind: "attachment_removed", actor: "coder", wantKind: "agent_stopped", wantJobs: 2},
		{name: "peer", kind: "comment", actor: "reviewer", wantKind: "notification_handled"},
		{name: "human", kind: "comment", agent: "coder", wantKind: "notification_handled"},
		{name: "prior", kind: "comment", actor: "coder", prior: true, wantKind: "notification_handled"},
		{name: "boundary", kind: "comment", actor: "coder", boundary: true, wantKind: "notification_handled"},
		{name: "output", kind: "agent_output", actor: "coder", wantKind: "notification_handled"},
		{name: "closed", kind: "comment", actor: "coder", closed: true, wantKind: "notification_handled"},
		{name: "normal_closed", normal: true, closed: true, wantKind: "agent_archived"},
		{name: "failed", kind: "comment", actor: "coder", failed: true},
		{name: "normal_failed", normal: true, failed: true},
		{name: "deleted", deleted: true},
		{name: "taskless", taskless: true, wantKind: "notification_handled"},
		{name: "normal_taskless", taskless: true, normal: true, wantKind: "agent_stopped"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, s := manifestTestStore(t)
			if _, err := s.ApplyManifest(ctx, `AGENT coder WITH "Work"; AGENT reviewer WITH "Review"; AGENT analyst WITH "Analyze"; AFTER coder PING reviewer; AFTER coder PING analyst;`); err != nil {
				t.Fatal(err)
			}
			var taskID int64
			if err := s.Pool.QueryRow(ctx, `INSERT INTO tasks(name,description,status) VALUES('Completion test','','todo') RETURNING id`).Scan(&taskID); err != nil {
				t.Fatal(err)
			}
			var jobTask any = taskID
			if tc.taskless {
				jobTask = nil
			}
			jobID := queuePerformanceJob(t, ctx, s, jobTask, nil, "coder", "pending", nil)
			if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET notification=$2 WHERE id=$1`, jobID, !tc.normal); err != nil {
				t.Fatal(err)
			}
			l, err := s.Claim(ctx, []string{"coder"})
			if err != nil || l == nil {
				t.Fatalf("claim failed: %v", err)
			}
			defer l.Close()
			if err := l.Started(ctx); err != nil {
				t.Fatal(err)
			}
			if tc.kind != "" {
				offset := "1 second"
				if tc.prior {
					offset = "-1 second"
				}
				if tc.boundary {
					offset = "0 seconds"
				}
				if _, err := s.Pool.Exec(ctx, `INSERT INTO events(task_id,kind,actor_agent,agent_name,body,created_at)
 SELECT $2,$3,$4,$5,'Work',started_at+$6::interval FROM jobs WHERE id=$1`, jobID, taskID, tc.kind, tc.actor, tc.agent, offset); err != nil {
					t.Fatal(err)
				}
			}
			if tc.closed {
				if _, err := s.Pool.Exec(ctx, `UPDATE tasks SET status='closed' WHERE id=$1`, taskID); err != nil {
					t.Fatal(err)
				}
			}
			if tc.deleted {
				if _, err := s.Pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, taskID); err != nil {
					t.Fatal(err)
				}
			} else if tc.failed {
				if err := l.Fail(ctx, errors.New("Work failed")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := l.Archive(ctx, app.Result{SessionID: "completion-test"}); err != nil {
					t.Fatal(err)
				}
			}
			// Call Complete twice to test a retry on the same lease.
			for range 2 {
				if err := l.Complete(ctx); err != nil {
					t.Fatal(err)
				}
			}
			var jobs, stops, handled, archived, runs int
			if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE task_id=$1 AND agent_name IN ('reviewer','analyst')`, taskID).Scan(&jobs); err != nil {
				t.Fatal(err)
			}
			if jobs != tc.wantJobs {
				t.Fatalf("job count = %d, want %d", jobs, tc.wantJobs)
			}
			if err := s.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE kind='agent_stopped'),count(*) FILTER (WHERE kind='notification_handled'),count(*) FILTER (WHERE kind='agent_archived') FROM events WHERE agent_name='coder'`).Scan(&stops, &handled, &archived); err != nil {
				t.Fatal(err)
			}
			counts := map[string]int{"agent_stopped": stops, "notification_handled": handled, "agent_archived": archived}
			for kind, count := range counts {
				want := 0
				if kind == tc.wantKind {
					want = 1
				}
				if count != want {
					t.Errorf("%s count = %d, want %d", kind, count, want)
				}
			}
			if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM agent_session_runs WHERE job_id=$1`, jobID).Scan(&runs); err != nil {
				t.Fatal(err)
			}
			wantRuns := 1
			if tc.deleted {
				wantRuns = 0
			}
			if runs != wantRuns {
				t.Errorf("run count = %d, want %d", runs, wantRuns)
			}
			if tc.failed {
				var state, outcome string
				if err := s.Pool.QueryRow(ctx, `SELECT j.state,r.outcome FROM jobs j JOIN agent_session_runs r ON r.job_id=j.id WHERE j.id=$1`, jobID).Scan(&state, &outcome); err != nil {
					t.Fatal(err)
				}
				if state != "failed" || outcome != "failed" {
					t.Errorf("state = %s, outcome = %s, want failed", state, outcome)
				}
			}
		})
	}
}
