package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/task"
)

func TestPerformanceBuckets(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 30, 0, 0, time.UTC)
	start, week := performanceStart(now), now.Add(-168*time.Hour)
	tasks := []task.Task{
		{ID: 1, Status: task.Validated, LastActivityAt: week},
		{ID: 2, Status: task.Closed, LastActivityAt: now},
		{ID: 3, Status: task.Closed, LastActivityAt: week.Add(-time.Nanosecond)},
		{ID: 4, Status: task.Validated, LastActivityAt: start},
		{ID: 5, Status: task.Closed, LastActivityAt: start.Add(-time.Nanosecond)},
		{ID: 6, Status: task.Closed, LastActivityAt: now.Add(time.Nanosecond)},
		{ID: 7, Status: task.Todo, LastActivityAt: now},
	}
	tasks = append(tasks, tasks[0])
	logs := []agent.SessionLog{
		{ID: 1, TaskID: 1, Kind: "blocker", CreatedAt: week},
		{ID: 2, TaskID: 1, Kind: "question", CreatedAt: now},
		{ID: 3, Kind: "blocker", CreatedAt: now},
		{ID: 4, Kind: "question", CreatedAt: now},
		{ID: 5, Kind: "question", CreatedAt: now.Add(time.Nanosecond)},
		{ID: 6, Kind: "question", CreatedAt: start.Add(-time.Nanosecond)},
		{ID: 7, Kind: "blocker", CreatedAt: week.Add(-time.Nanosecond)},
		{ID: 8, Kind: "custom-note", CreatedAt: now},
	}
	logs = append(logs, logs[0])
	got := buildPerformance(now.In(time.FixedZone("offset", 3600)), tasks, logs)
	if len(got.Days) != 30 || got.Days[0].Date != "2026-10-11" || got.Days[29].Date != "2026-11-09" || got.Timezone != "UTC" || got.WindowDays != 30 {
		t.Fatalf("window: %+v", got)
	}
	w := got.Week
	if w.Validated != 1 || w.Closed != 1 || w.TaskBlockers != 1 || w.TaskQuestions != 1 || w.AutonomousBlockers != 1 || w.AutonomousQuestions != 1 || w.Ratio == nil || *w.Ratio != 1 {
		t.Fatalf("week: %+v", w)
	}
	if got.Days[0].Validated != 1 || got.Days[0].Ratio != nil || got.Days[22].Closed != 1 || got.Days[22].AutonomousBlockers != 1 {
		t.Fatalf("daily boundaries: %+v", got.Days)
	}
	// Moving current activity moves the task, never adds another completion.
	tasks[0].LastActivityAt = now
	tasks = tasks[:7]
	moved := buildPerformance(now, tasks, nil)
	if moved.Days[22].Validated != 0 || moved.Days[29].Validated != 1 {
		t.Fatalf("activity not moved: %+v", moved.Days)
	}
	empty := buildPerformance(now, nil, nil)
	if empty.Week.Ratio != nil || empty.Week.Closed != 0 || len(empty.Days) != 30 {
		t.Fatalf("empty: %+v", empty)
	}
	zero := buildPerformance(now, []task.Task{{ID: 1, Status: task.Closed, LastActivityAt: now}}, nil)
	if zero.Week.Ratio == nil || *zero.Week.Ratio != 0 {
		t.Fatal("zero numerator should be numeric zero")
	}
}

type performanceTestRepo struct {
	testRepo
	tasks      []task.Task
	calls      int
	start, end time.Time
	err        error
}

func (p *performanceTestRepo) PerformanceData(_ context.Context, start, end time.Time) ([]task.Task, []agent.SessionLog, error) {
	p.calls++
	p.start = start
	p.end = end
	return p.tasks, nil, p.err
}
func TestPerformanceAPIIsolationAndErrors(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	base := &performanceTestRepo{}
	alpha := &performanceTestRepo{tasks: []task.Task{{ID: 1, Status: task.Validated, LastActivityAt: now}}}
	beta := &performanceTestRepo{tasks: []task.Task{{ID: 1, Status: task.Closed, LastActivityAt: now}}}
	handler := Handler(base, testNamespaces{repos: map[string]Repository{"alpha": alpha, "beta": beta}})
	for _, name := range []string{"alpha", "beta"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/performance?namespace="+name, nil))
		var got performanceResponse
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
			t.Fatalf("response: %d %s", rec.Code, rec.Body.String())
		}
		if (name == "alpha" && got.Week.Validated != 1) || (name == "beta" && got.Week.Closed != 1) {
			t.Fatalf("namespace %s: %+v", name, got)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cacheable response")
		}
	}
	if base.calls != 0 || alpha.calls != 1 || beta.calls != 1 || !alpha.start.Equal(performanceStart(alpha.end)) {
		t.Fatal("wrong scope or retrieval window")
	}
	alpha.err = errors.New("private database credentials")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/performance?namespace=alpha", nil))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "private") {
		t.Fatalf("unsafe error: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	Handler(&testRepo{}).ServeHTTP(rec, httptest.NewRequest("GET", "/api/performance", nil))
	if rec.Code != 501 {
		t.Fatalf("unsupported: %d", rec.Code)
	}
}
