package postgres

import (
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/task"
)

func TestPerformanceDataCurrentActivityAndDirectLogs(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	created, err := s.CreateTask(ctx, "performance", "", task.Validated, nil)
	if err != nil {
		t.Fatal(err)
	}
	job := queuePerformanceJob(t, ctx, s, created.ID, nil, "queue_test", "pending", nil)
	now := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	start := now.Add(-30 * 24 * time.Hour)
	// Immutable observations need not have an archived run. Taskless observations
	// remain in the result, and neither shared job nor session duplicates them.
	for _, v := range []struct {
		taskID int64
		kind   string
		at     time.Time
	}{
		{created.ID, "blocker", now.Add(-time.Minute)},
		{0, "question", start},
		{0, "blocker", now.Add(time.Microsecond)},
		{0, "question", start.Add(-time.Microsecond)},
		{0, "note", now},
	} {
		if _, err := s.Pool.Exec(ctx, `INSERT INTO session_logs(job_id,session_id,agent_name,task_id,kind,body,created_at) VALUES($1,'shared','queue_test',$2,$3,'observation',$4)`, job, v.taskID, v.kind, v.at); err != nil {
			t.Fatal(err)
		}
	}
	tasks, logs, err := s.PerformanceData(ctx, start, now)
	if err != nil || len(tasks) != 1 || len(logs) != 2 {
		t.Fatalf("data: %+v %+v %v", tasks, logs, err)
	}
	if !tasks[0].LastActivityAt.Equal(now.Add(-time.Minute)) || tasks[0].Status != task.Validated || logs[0].TaskID != 0 || logs[1].TaskID != created.ID {
		t.Fatalf("activity or attribution: %+v %+v", tasks, logs)
	}
	// A new comment moves the current snapshot without adding a task or log.
	if _, err := s.Pool.Exec(ctx, `INSERT INTO events(task_id,kind,body,created_at) VALUES($1,'comment','later',$2)`, created.ID, now); err != nil {
		t.Fatal(err)
	}
	tasks, logs, err = s.PerformanceData(ctx, start, now)
	if err != nil || len(tasks) != 1 || len(logs) != 2 || !tasks[0].LastActivityAt.Equal(now) {
		t.Fatalf("moved: %+v %+v %v", tasks, logs, err)
	}
}
