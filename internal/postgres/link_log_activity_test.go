package postgres

import (
	"testing"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/task"
)

func TestLinkLogsPreserveActivityAndColumnOrdering(t *testing.T) {
	ctx, s := queuePerformanceStore(t)
	older, err := s.CreateTask(ctx, "older", "", task.Todo, nil)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := s.CreateTask(ctx, "newer", "", task.Todo, nil)
	if err != nil {
		t.Fatal(err)
	}
	job := queuePerformanceJob(t, ctx, s, older.ID, nil, "queue_test", "pending", nil)
	baselineRows, err := s.Tasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	baseline := baselineRows[0]
	link, err := s.AppendSessionLog(ctx, agent.SessionLogInput{JobID: job, Agent: "queue_test", Kind: "link", Body: "https://github.com/api-mittens/looster/pull/1"})
	if err != nil {
		t.Fatal(err)
	}
	if !link.CreatedAt.After(newer.LastActivityAt) {
		t.Fatalf("expected link newer than both tasks: %+v", link)
	}
	detail, err := s.Task(ctx, older.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !detail.LastActivityAt.Equal(baseline.LastActivityAt) || len(detail.SessionLogs) != 1 || detail.SessionLogs[0].Body != link.Body {
		t.Fatalf("link changed activity or was lost: %+v", detail)
	}
	rows, err := s.Tasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ID == older.ID && (!row.LastActivityAt.Equal(baseline.LastActivityAt) || row.CommentCount != baseline.CommentCount || row.StatusChanges != baseline.StatusChanges) {
			t.Fatalf("link changed task metrics: %+v", row)
		}
	}
	for _, query := range []task.ColumnQuery{{Key: "todo"}, {Key: "agent:queue_test", ActiveTaskIDs: []int64{older.ID, newer.ID}}} {
		page, err := s.ColumnTasks(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Tasks) != 2 || page.Tasks[0].ID != newer.ID || page.Tasks[1].ID != older.ID || !page.Tasks[1].LastActivityAt.Equal(baseline.LastActivityAt) || len(page.Tasks[1].SessionLogs) != 0 {
			t.Fatalf("link changed %s metadata or order: %+v", query.Key, page)
		}
	}
}
