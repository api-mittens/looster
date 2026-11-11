package postgres

import (
	"reflect"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/task"
)

// Historical fixtures are inserted with their final timestamps. In particular,
// no append-only event or session log is rewritten to manufacture recency.
func TestPerformanceLinkPreservesActivity(t *testing.T) {
	for _, status := range []task.Status{task.Validated, task.Closed} {
		t.Run(string(status), func(t *testing.T) {
			ctx, s := queuePerformanceStore(t)
			base := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
			end := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
			insertTask := func(name string, at time.Time) int64 {
				t.Helper()
				var id int64
				if err := s.Pool.QueryRow(ctx, `INSERT INTO tasks(name,description,status,created_at) VALUES($1,'',$2,$3) RETURNING id`, name, status, at).Scan(&id); err != nil {
					t.Fatal(err)
				}
				return id
			}
			// Suppress wall-clock creation events only while inserting historical
			// tasks in this disposable namespace. Restore before exercising APIs.
			if _, err := s.Pool.Exec(ctx, `ALTER TABLE tasks DISABLE TRIGGER task_event`); err != nil {
				t.Fatal(err)
			}
			older := insertTask("older", base)
			newer := insertTask("newer", base.Add(2*time.Hour))
			outside := insertTask("outside", base.Add(-2*time.Hour))
			if _, err := s.Pool.Exec(ctx, `ALTER TABLE tasks ENABLE TRIGGER task_event`); err != nil {
				t.Fatal(err)
			}
			job := queuePerformanceJob(t, ctx, s, older, nil, "queue_test", "pending", nil)
			outsideJob := queuePerformanceJob(t, ctx, s, outside, nil, "queue_test", "pending", nil)
			for i, kind := range []string{"question", "blocker"} {
				if _, err := s.Pool.Exec(ctx, `INSERT INTO session_logs(job_id,session_id,agent_name,task_id,kind,body,created_at) VALUES($1,'queued-session','queue_test',$2,$3,'historical attention',$4)`, job, older, kind, base.Add(time.Duration(i+1)*time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			// A task event, rather than a log, supplies this task's baseline maximum.
			if _, err := s.Pool.Exec(ctx, `INSERT INTO events(task_id,kind,body,created_at) VALUES($1,'comment','historical activity',$2)`, older, base.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			type snapshot struct {
				tasks []task.Task
				logs  []agent.SessionLog
			}
			read := func(start, end time.Time) snapshot {
				t.Helper()
				tasks, logs, err := s.PerformanceData(ctx, start, end)
				if err != nil {
					t.Fatal(err)
				}
				return snapshot{tasks, logs}
			}
			windows := [][2]time.Time{
				{base.Add(-24 * time.Hour), end}, // complete recency ordering
				{base, base.Add(3 * time.Hour)},  // link must not remove an included task
				{base.Add(3 * time.Hour), end},   // link must not pull an old task into a report
			}
			before := make([]snapshot, len(windows))
			for i, w := range windows {
				before[i] = read(w[0], w[1])
			}
			if len(before[0].tasks) != 3 || before[0].tasks[0].ID != newer || before[0].tasks[1].ID != older || before[0].tasks[2].ID != outside || len(before[0].logs) != 2 || len(before[1].tasks) != 2 || len(before[2].tasks) != 0 {
				t.Fatalf("invalid fixture: %+v", before)
			}
			for _, jobID := range []int64{job, outsideJob} {
				link, err := s.AppendSessionLog(ctx, agent.SessionLogInput{JobID: jobID, Agent: "queue_test", Kind: "link", Body: "https://github.com/api-mittens/looster/pull/75"})
				if err != nil {
					t.Fatal(err)
				}
				if !link.CreatedAt.After(base.Add(3 * time.Hour)) {
					t.Fatalf("link must postdate fixture: %+v", link)
				}
			}
			for i, w := range windows {
				after := read(w[0], w[1])
				// Compare all returned attention observations as well as task timestamps,
				// statuses, ordering and membership, not merely aggregate counts.
				if !reflect.DeepEqual(before[i], after) {
					t.Errorf("window %d changed after link: before=%+v after=%+v", i, before[i], after)
				}
			}
			for _, kind := range []string{"question", "blocker"} {
				previous := read(base.Add(-24*time.Hour), end)
				log, err := s.AppendSessionLog(ctx, agent.SessionLogInput{JobID: job, Agent: "queue_test", Kind: kind, Body: "ordinary attention"})
				if err != nil {
					t.Fatal(err)
				}
				after := read(base.Add(-24*time.Hour), end)
				var prior time.Time
				for _, row := range previous.tasks {
					if row.ID == older {
						prior = row.LastActivityAt
					}
				}
				if len(after.tasks) != 3 || after.tasks[0].ID != older || !after.tasks[0].LastActivityAt.Equal(log.CreatedAt) || !log.CreatedAt.After(prior) || len(after.logs) != len(previous.logs)+1 || !reflect.DeepEqual(after.logs[len(after.logs)-1], log) {
					t.Fatalf("%s did not advance activity/attention: log=%+v after=%+v", kind, log, after)
				}
				recent := read(base.Add(3*time.Hour), end)
				if len(recent.tasks) != 1 || recent.tasks[0].ID != older {
					t.Fatalf("%s did not enter recent reporting window: %+v", kind, recent)
				}
			}
		})
	}
}
