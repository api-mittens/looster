package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/event"
)

type runOutputRepo struct {
	historyTestRepo
	events             []event.Event
	job, after         int64
	limit, outputCalls int
	outputErr          error
}

func (r *runOutputRepo) SessionRunOutput(_ context.Context, job, after int64, limit int) ([]event.Event, error) {
	r.job, r.after, r.limit = job, after, limit
	r.outputCalls++
	return r.events, r.outputErr
}
func newRunOutputRepo(text string) *runOutputRepo {
	return &runOutputRepo{historyTestRepo: historyTestRepo{runs: []agent.SessionRun{{JobID: 9, Agent: "solo"}}}, events: []event.Event{{ID: 11, Body: text}, {ID: 12, Body: "next"}, {ID: 13, Body: "later"}}}
}

func TestAgentRunOutputPaginationAndIsolation(t *testing.T) {
	base, beta := newRunOutputRepo("base secret"), newRunOutputRepo("beta output")
	handler := Handler(base, testNamespaces{map[string]Repository{"beta": beta}})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/agents/solo/runs/9/output?namespace=beta&after=10&limit=2", nil))
	var page struct {
		Events []event.Event `json:"events"`
		More   bool          `json:"has_more"`
		Next   int64         `json:"next_after"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Events) != 2 || !page.More || page.Next != 12 || beta.job != 9 || beta.after != 10 || beta.limit != 3 || base.outputCalls != 0 || strings.Contains(response.Body.String(), "base secret") {
		t.Fatalf("page=%+v repo=%+v response=%d %s", page, beta, response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("output must not be cached")
	}
	beta.events = nil
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/agents/solo/runs/9/output?namespace=beta", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"events":[]`) {
		t.Fatal(response.Code, response.Body.String())
	}
}

func TestAgentRunOutputValidationAndFailures(t *testing.T) {
	repo := newRunOutputRepo("private output")
	for _, suffix := range []string{"?limit=0", "?limit=201", "?limit=nope", "?limit=", "?after=-1", "?after=nope", "?after=", "?after=9223372036854775808"} {
		response := httptest.NewRecorder()
		Handler(repo).ServeHTTP(response, httptest.NewRequest("GET", "/api/agents/solo/runs/9/output"+suffix, nil))
		if response.Code != 400 {
			t.Fatal(suffix, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{"/api/agents/other/runs/9/output", "/api/agents/solo/runs/8/output"} {
		response := httptest.NewRecorder()
		Handler(repo).ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 404 || strings.Contains(response.Body.String(), "private") {
			t.Fatal(path, response.Code, response.Body.String())
		}
	}
	if repo.outputCalls != 0 {
		t.Fatal("invalid or foreign identity reached output")
	}
	for _, path := range []string{"/api/agents/solo/runs/0/output", "/api/agents/solo/runs/nope/output"} {
		response := httptest.NewRecorder()
		Handler(repo).ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 400 {
			t.Fatal(path, response.Code)
		}
	}
	repo.outputErr = errors.New("secret SQL error")
	response := httptest.NewRecorder()
	Handler(repo).ServeHTTP(response, httptest.NewRequest("GET", "/api/agents/solo/runs/9/output", nil))
	if response.Code != 500 || strings.Contains(response.Body.String(), "secret SQL") {
		t.Fatal(response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	Handler(&historyTestRepo{}).ServeHTTP(response, httptest.NewRequest("GET", "/api/agents/solo/runs/9/output", nil))
	if response.Code != 501 {
		t.Fatal(response.Code, response.Body.String())
	}
}

// The output port must also survive the same production permission wrapper.
type wrappedOutputRepo struct {
	app.Repository
	source *runOutputRepo
}

func (r wrappedOutputRepo) SessionHistory(ctx context.Context, n string, task, before int64, limit int) ([]agent.SessionRun, error) {
	return r.source.SessionHistory(ctx, n, task, before, limit)
}
func (r wrappedOutputRepo) SessionRun(ctx context.Context, id int64) (agent.SessionRun, error) {
	return r.source.SessionRun(ctx, id)
}
func (r wrappedOutputRepo) SessionRunOutput(ctx context.Context, id, after int64, limit int) ([]event.Event, error) {
	return r.source.SessionRunOutput(ctx, id, after, limit)
}
func TestPermissionWrappedAgentRunOutput(t *testing.T) {
	repo, err := app.WithPermissions(wrappedOutputRepo{source: newRunOutputRepo("wrapped output")}, app.Actor{Role: app.Human})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	Handler(repo).ServeHTTP(response, httptest.NewRequest("GET", "/api/agents/solo/runs/9/output", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "wrapped output") {
		t.Fatal(response.Code, response.Body.String())
	}
}
