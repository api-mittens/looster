package postgres

import (
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/postgres/db"
	"github.com/api-mittens/looster/internal/task"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSessionLogsAdvanceTaskActivityWithoutNotifications(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	created, err := s.CreateTask(ctx, "log activity", "", task.Planning, nil)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := s.Tasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var eventsBefore, jobsBefore int
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM events),(SELECT count(*) FROM jobs)`).Scan(&eventsBefore, &jobsBefore); err != nil {
		t.Fatal(err)
	}
	job := queuePerformanceJob(t, ctx, s, created.ID, nil, "queue_test", "pending", nil)
	// The scheduled fixture event has no task identity. Appending a log must not
	// emit a comment or enqueue additional jobs through comment notifications.
	log, err := s.AppendSessionLog(ctx, agent.SessionLogInput{JobID: job, Agent: "queue_test", Kind: "blocker", Body: "Need credentials"})
	if err != nil {
		t.Fatal(err)
	}
	if !log.CreatedAt.After(created.LastActivityAt) {
		t.Fatalf("log did not advance time: %+v %+v", created, log)
	}
	detail, err := s.Task(ctx, created.ID)
	if err != nil || !detail.LastActivityAt.Equal(log.CreatedAt) {
		t.Fatalf("detail activity %+v %v", detail, err)
	}
	list, err := s.Tasks(ctx)
	if err != nil || len(list) != 1 || !list[0].LastActivityAt.Equal(log.CreatedAt) {
		t.Fatalf("list activity %+v %v", list, err)
	}
	if list[0].CommentCount != baseline[0].CommentCount || list[0].StatusChanges != baseline[0].StatusChanges {
		t.Fatalf("log changed comment/status counts: %+v", list[0])
	}
	var eventsAfter, jobsAfter int
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM events),(SELECT count(*) FROM jobs)`).Scan(&eventsAfter, &jobsAfter); err != nil {
		t.Fatal(err)
	}
	if eventsAfter != eventsBefore+1 || jobsAfter != jobsBefore+1 {
		t.Fatalf("log emitted notification events/jobs: before %d/%d after %d/%d", eventsBefore, jobsBefore, eventsAfter, jobsAfter)
	}
	// A later log ID with an older timestamp must not regress activity, and
	// taskless logs must never affect arbitrary tasks.
	for _, id := range []int64{created.ID, 0} {
		at := log.CreatedAt.Add(-time.Hour)
		if id == 0 {
			at = log.CreatedAt.Add(time.Hour)
		}
		if _, err := s.Pool.Exec(ctx, `INSERT INTO session_logs(job_id,session_id,agent_name,task_id,kind,body,created_at) VALUES($1,'queued-session','queue_test',$2,'question','historical',$3)`, job, id, at); err != nil {
			t.Fatal(err)
		}
	}
	detail, err = s.Task(ctx, created.ID)
	if err != nil || !detail.LastActivityAt.Equal(log.CreatedAt) {
		t.Fatalf("activity regressed or taskless leaked: %+v %v", detail, err)
	}
	future := log.CreatedAt.Add(2 * time.Hour)
	if _, err := s.Pool.Exec(ctx, `INSERT INTO events(task_id,kind,body,created_at) VALUES($1,'comment','newer event',$2)`, created.ID, future); err != nil {
		t.Fatal(err)
	}
	detail, err = s.Task(ctx, created.ID)
	if err != nil || !detail.LastActivityAt.Equal(future) {
		t.Fatalf("later event ignored: %+v %v", detail, err)
	}
	var indexed bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT FROM pg_indexes WHERE schemaname=current_schema() AND indexname='session_logs_task_last_activity')`).Scan(&indexed); err != nil || !indexed {
		t.Fatalf("missing log activity index: %v", err)
	}
}

func TestTaskSessionLogsUseConstantQueries(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	trace := &queueQueryTrace{}
	cfg := s.Pool.Config()
	cfg.ConnConfig.Tracer = trace
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	reader := &Store{Pool: pool, q: db.New(pool)}
	for _, size := range []int{1, 12} {
		for i := 0; i < size; i++ {
			if _, err := s.CreateTask(ctx, "board activity", "", task.Planning, nil); err != nil {
				t.Fatal(err)
			}
		}
		trace.mu.Lock()
		trace.queries = nil
		trace.mu.Unlock()
		rows, err := reader.Tasks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		trace.mu.Lock()
		count := len(trace.queries)
		trace.mu.Unlock()
		if count != 2 {
			t.Fatalf("%d tasks used %d queries, want task list plus batch logs only", len(rows), count)
		}
		for _, row := range rows {
			if row.SessionLogs == nil {
				t.Fatal("null log array")
			}
		}
	}
}
