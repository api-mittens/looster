package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
)

type historyTestRepo struct {
	testRepo
	runs           []agent.SessionRun
	name           string
	before, taskID int64
	limit, calls   int
	historyErr     error
}

func (s *historyTestRepo) SessionHistory(_ context.Context, name string, taskID, before int64, limit int) ([]agent.SessionRun, error) {
	s.calls++
	s.name = name
	s.taskID = taskID
	s.before = before
	s.limit = limit
	return s.runs, s.historyErr
}
func (s *historyTestRepo) SessionRun(_ context.Context, id int64) (agent.SessionRun, error) {
	s.calls++
	if s.historyErr != nil {
		return agent.SessionRun{}, s.historyErr
	}
	for _, run := range s.runs {
		if run.JobID == id {
			return run, nil
		}
	}
	return agent.SessionRun{}, app.ErrNotFound
}

func TestAgentRunHistoryAPI(t *testing.T) {
	repo := &historyTestRepo{runs: []agent.SessionRun{{JobID: 9, Agent: "solo", Summary: "Newest", Outcome: "done"}, {JobID: 8, Agent: "solo", TaskID: 42}, {JobID: 7, Agent: "solo"}}}
	handler := Handler(repo)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/agents/solo/runs?limit=2&before_job_id=10", nil))
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var page struct {
		Runs    []agent.SessionRun `json:"runs"`
		HasMore bool               `json:"has_more"`
		Next    int64              `json:"next_before_job_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Runs) != 2 || !page.HasMore || page.Next != 8 || repo.name != "solo" || repo.before != 10 || repo.limit != 3 || repo.taskID != 0 {
		t.Fatalf("page=%+v repo=%+v", page, repo)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("history must not be cached")
	}
	repo.runs = nil
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/agents/removed-agent/runs", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"runs":[]`) || repo.limit != 21 {
		t.Fatal(response.Code, response.Body.String())
	}
}

func TestAgentRunDetailAPI(t *testing.T) {
	repo := &historyTestRepo{runs: []agent.SessionRun{{JobID: 9, Agent: "solo", Summary: "Final result", Outcome: "failed", SessionID: "session-9"}}}
	handler := Handler(repo)
	for _, tc := range []struct {
		path string
		code int
	}{
		{"/api/agents/solo/runs/9", 200}, {"/api/agents/other/runs/9", 404}, {"/api/agents/solo/runs/10", 404},
		{"/api/agents/solo/runs/0", 400}, {"/api/agents/solo/runs/-1", 400}, {"/api/agents/solo/runs/nope", 400}, {"/api/agents/solo/runs/9223372036854775808", 400},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", tc.path, nil))
		if response.Code != tc.code {
			t.Errorf("%s: %d %s", tc.path, response.Code, response.Body.String())
		}
		if tc.code == 200 && !strings.Contains(response.Body.String(), "Final result") {
			t.Error("missing summary")
		}
		if tc.code == 404 && strings.Contains(response.Body.String(), "Final result") {
			t.Error("cross-agent detail leak")
		}
	}
}

func TestAgentRunValidationAndOptionalRepository(t *testing.T) {
	repo := &historyTestRepo{}
	for _, suffix := range []string{"?limit=0", "?limit=-1", "?limit=101", "?limit=nope", "?limit=", "?before_job_id=0", "?before_job_id=-1", "?before_job_id=nope", "?before_job_id=9223372036854775808"} {
		response := httptest.NewRecorder()
		Handler(repo).ServeHTTP(response, httptest.NewRequest("GET", "/api/agents/solo/runs"+suffix, nil))
		if response.Code != 400 {
			t.Errorf("%s: %d", suffix, response.Code)
		}
	}
	if repo.calls != 0 {
		t.Fatal("invalid queries reached repository")
	}
	for _, path := range []string{"/api/agents/solo/runs", "/api/agents/solo/runs/9"} {
		response := httptest.NewRecorder()
		Handler(&testRepo{}).ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != http.StatusNotImplemented {
			t.Fatal(response.Code, response.Body.String())
		}
		repo.historyErr = errors.New("secret database failure")
		response = httptest.NewRecorder()
		Handler(repo).ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 500 || strings.Contains(response.Body.String(), "secret") {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	Handler(repo).ServeHTTP(response, httptest.NewRequest("POST", "/api/agents/solo/runs", nil))
	if response.Code != 405 {
		t.Fatal(response.Code)
	}
}

func TestAgentRunNamespaceIsolation(t *testing.T) {
	base := &historyTestRepo{runs: []agent.SessionRun{{JobID: 1, Agent: "solo", Summary: "base secret"}}}
	beta := &historyTestRepo{runs: []agent.SessionRun{{JobID: 1, Agent: "solo", Summary: "beta result"}}}
	handler := Handler(base, testNamespaces{map[string]Repository{"beta": beta}})
	for _, path := range []string{"/api/agents/solo/runs?namespace=beta", "/api/agents/solo/runs/1?namespace=beta"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 200 || !strings.Contains(response.Body.String(), "beta result") || strings.Contains(response.Body.String(), "base secret") {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	if base.calls != 0 {
		t.Fatal("namespace selection reached base repository")
	}
}
