package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dominikbb/looster/internal/agent"
	"github.com/dominikbb/looster/internal/app"
	"github.com/dominikbb/looster/internal/behaviour"
	"github.com/dominikbb/looster/internal/event"
	"github.com/dominikbb/looster/internal/memory"
	"github.com/dominikbb/looster/internal/tag"
	"github.com/dominikbb/looster/internal/task"
)

type testRepo struct {
	filter event.Filter
	fail   bool
	status task.Status
}

func (s *testRepo) Tasks(context.Context) ([]task.Task, error) {
	if s.fail {
		return nil, errors.New("private database info")
	}
	return []task.Task{{ID: 1, Name: "Example", Status: task.Todo, CreatedAt: time.Unix(100, 0), LastActivityAt: time.Unix(200, 0), StatusChanges: 3, CommentCount: 2}}, nil
}
func (*testRepo) Task(_ context.Context, id int64) (task.Task, error) {
	if id != 1 {
		return task.Task{}, app.ErrNotFound
	}
	return task.Task{ID: 1}, nil
}
func (*testRepo) CreateTask(_ context.Context, name, description string, status task.Status, tags []string) (task.Task, error) {
	return task.Task{ID: 2, Name: name, Description: description, Status: status, Tags: tags}, nil
}
func (s *testRepo) SetStatus(_ context.Context, id int64, status task.Status) error {
	if id != 1 {
		return app.ErrNotFound
	}
	s.status = status
	return nil
}
func (*testRepo) CreateTag(context.Context, string, string) error       { return nil }
func (*testRepo) Comment(context.Context, int64, string) (int64, error) { return 4, nil }
func (*testRepo) Agents(context.Context) ([]agent.Agent, error) {
	return []agent.Agent{{Name: "coder"}}, nil
}
func (*testRepo) Sessions(context.Context, string) ([]agent.Session, error) {
	return []agent.Session{{TaskID: 1, Agent: "coder"}, {TaskID: 2, Agent: "other"}}, nil
}
func (s *testRepo) Logs(_ context.Context, f event.Filter) ([]event.Event, error) {
	s.filter = f
	if !f.Desc {
		return []event.Event{{ID: 2}, {ID: 3}}, nil
	}
	return []event.Event{{ID: 3}, {ID: 2}}, nil
}

type allStatusRepo struct{ testRepo }

func (*allStatusRepo) Tasks(context.Context) ([]task.Task, error) {
	return []task.Task{{ID: 1, Status: task.Planning}, {ID: 2, Status: task.Todo}, {ID: 3, Status: task.Validated}, {ID: 4, Status: task.Closed}}, nil
}

func TestBoardIncludesCompletedTasks(t *testing.T) {
	response := httptest.NewRecorder()
	Handler(&allStatusRepo{}).ServeHTTP(response, httptest.NewRequest("GET", "/api/board", nil))
	if response.Code != 200 {
		t.Fatalf("board status: %d", response.Code)
	}
	var board struct {
		Tasks []task.Task `json:"tasks"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &board); err != nil {
		t.Fatal(err)
	}
	want := []task.Status{task.Planning, task.Todo, task.Validated, task.Closed}
	if len(board.Tasks) != len(want) {
		t.Fatalf("board dropped tasks: %+v", board.Tasks)
	}
	for i, status := range want {
		if board.Tasks[i].Status != status {
			t.Fatalf("task %d status = %s, want %s", i, board.Tasks[i].Status, status)
		}
	}
}

func TestDashboard(t *testing.T) {
	repo := &testRepo{}
	handler := Handler(repo)
	for _, path := range []string{"/", "/app.js", "/style.css", "/task-graph.js", "/task-graph.mjs", "/task-graph.css", "/api/board", "/api/status", "/api/system", "/api/library", "/api/tasks/TSK-1", "/api/tasks/TSK-1/events"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 200 {
			t.Fatalf("%s: %d", path, response.Code)
		}
	}
	board := httptest.NewRecorder()
	handler.ServeHTTP(board, httptest.NewRequest("GET", "/api/board", nil))
	var boardData struct {
		Tasks []task.Task `json:"tasks"`
	}
	if err := json.Unmarshal(board.Body.Bytes(), &boardData); err != nil {
		t.Fatal(err)
	}
	if len(boardData.Tasks) != 1 || boardData.Tasks[0].StatusChanges != 3 || boardData.Tasks[0].CommentCount != 2 {
		t.Fatalf("board task activity missing: %+v", boardData.Tasks)
	}
	if !boardData.Tasks[0].LastActivityAt.Equal(time.Unix(200, 0)) || !boardData.Tasks[0].LastActivityAt.After(boardData.Tasks[0].CreatedAt) {
		t.Fatalf("last activity missing from board API: %+v", boardData.Tasks[0])
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/tasks/TSK-1", nil))
	var detail struct {
		Sessions []agent.Session `json:"sessions"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Sessions) != 1 || detail.Sessions[0].TaskID != 1 {
		t.Fatal("session from another task leaked into detail")
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/tasks/TSK-1/events", nil))
	var events []event.Event
	_ = json.Unmarshal(response.Body.Bytes(), &events)
	if len(events) != 2 || events[0].ID != 2 || repo.filter.Limit != 200 || !repo.filter.Desc {
		t.Fatal("recent activity must be bounded and chronological")
	}
	for path, code := range map[string]int{"/api/tasks/bad": 400, "/api/tasks/TSK-2": 404, "/api/tasks/TSK-1/events?after=-1": 400, "/api/unknown": 404} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != code {
			t.Errorf("%s: got %d want %d", path, response.Code, code)
		}
	}
	repo.fail = true
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/board", nil))
	if response.Code != 500 || strings.Contains(response.Body.String(), "private database info") {
		t.Fatal("invalid repository error response")
	}
}

func TestGraphAssets(t *testing.T) {
	handler := Handler(&testRepo{})
	for _, asset := range []struct{ path, content, media string }{
		{"/", "task-graph.css", "text/html"},
		{"/app.js", `href="#graph">Graph <kbd>gg</kbd></a>`, "javascript"},
		{"/task-graph.js", "task-agent-graph", "javascript"},
		{"/task-graph.mjs", "buildGraph", "javascript"},
		{"/d3-force-adapter.mjs", "forceSimulation", "javascript"},
		{"/vendor/d3.v7.9.0.min.js", "v7.9.0", "javascript"},
		{"/vendor/D3-LICENSE.txt", "Permission to use", "text/plain"},
		{"/task-graph.css", "prefers-reduced-motion", "text/css"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", asset.path, nil))
		if response.Code != 200 || !strings.Contains(response.Body.String(), asset.content) || !strings.Contains(response.Header().Get("Content-Type"), asset.media) {
			t.Errorf("graph asset %s: status=%d content-type=%s", asset.path, response.Code, response.Header().Get("Content-Type"))
		}
	}
}

func TestOutputCursor(t *testing.T) {
	repo := &testRepo{}
	handler := Handler(repo)
	for _, cursor := range []string{"0", "100"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/tasks/TSK-1/events?after="+cursor, nil))
		if response.Code != 200 || repo.filter.Desc {
			t.Fatal("explicit cursors must stream forwards, including after=0")
		}
	}
	comments := httptest.NewRecorder()
	handler.ServeHTTP(comments, httptest.NewRequest("GET", "/api/tasks/TSK-1/events?kind=comment&after=0", nil))
	if comments.Code != 200 || len(repo.filter.Kinds) != 1 || repo.filter.Kinds[0] != "comment" || repo.filter.Desc {
		t.Fatal("comments must have their own chronological cursor")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/system", nil))
	if strings.Contains(response.Body.String(), "config") {
		t.Fatal("web-process settings should not be exposed as worker settings")
	}
}

func TestLibraryContext(t *testing.T) {
	handler := Handler(&testRepo{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/library", nil))
	var result struct {
		Agents []app.AgentContext `json:"agents"`
		Tags   []app.TagContext   `json:"tags"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Agents) != 1 || result.Agents[0].Memories[0].Body != "agent memory for coder" {
		t.Fatal("agent memories missing")
	}
	if len(result.Tags) != 1 || result.Tags[0].Description != "Design instructions" || result.Tags[0].Memories[0].Body != "tag memory for design" {
		t.Fatal("tag description/memories missing")
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("POST", "/api/library", nil))
	if response.Code != 405 {
		t.Fatal("library must remain read-only")
	}
}

func TestMutations(t *testing.T) {
	repo := &testRepo{}
	handler := Handler(repo)
	for _, tc := range []struct {
		path, body string
		code       int
	}{
		{"/api/tasks", `{"name":"New task","description":"Do work","tags":["design"]}`, 201},
		{"/api/tasks", `{"name":"New task","tags":["undefined-tag"]}`, 400},
		{"/api/tasks", `{"name":"New task","status":"unvalidated"}`, 400},
		{"/api/tasks", `{"name":" ","status":"planning"}`, 400},
		{"/api/tasks", `{"name":"New task","status":"invalid"}`, 400},
		{"/api/tags", `{"name":"design/ui","description":"Interface"}`, 405},
		{"/api/tags", `{"name":"invalid tag"}`, 405},
		{"/api/tasks/TSK-1/comments", `{"body":"A note"}`, 201},
		{"/api/tasks/TSK-1/comments", `{"body":"  "}`, 400},
		{"/api/tasks/TSK-2/comments", `{"body":"A note"}`, 404},
		{"/api/tasks/TSK-1/status", `{"status":"closed"}`, 200},
		{"/api/tasks/TSK-1/status", `{"status":"invalid"}`, 400},
		{"/api/tasks/TSK-2/status", `{"status":"todo"}`, 404},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(response, request)
		if response.Code != tc.code {
			t.Errorf("%s %s: got %d, want %d", tc.path, tc.body, response.Code, tc.code)
		}
	}
	if repo.status != task.Closed {
		t.Fatalf("status not saved: %s", repo.status)
	}
}

func (*testRepo) Workers(context.Context) ([]app.Worker, error)       { return []app.Worker{}, nil }
func (*testRepo) Behaviour(context.Context) ([]behaviour.Rule, error) { return []behaviour.Rule{}, nil }
func (*testRepo) Tags(context.Context) ([]tag.Tag, error) {
	return []tag.Tag{{Name: "design", Description: "Design instructions"}}, nil
}
func (*testRepo) Tag(_ context.Context, name string) (tag.Tag, error) {
	return tag.Tag{Name: name, Description: "Design instructions"}, nil
}
func (*testRepo) Agent(_ context.Context, name string) (agent.Agent, error) {
	return agent.Agent{Name: name}, nil
}
func (*testRepo) Memories(_ context.Context, kind, name string) ([]memory.Memory, error) {
	return []memory.Memory{{ID: 1, Body: kind + " memory for " + name}}, nil
}

type manifestTestRepo struct {
	testRepo
	active      int64
	activations int
}

func (s *manifestTestRepo) ApplyManifest(context.Context, string) (behaviour.Manifest, error) {
	return behaviour.Manifest{}, nil
}
func (s *manifestTestRepo) Manifests(context.Context) ([]behaviour.Manifest, error) {
	return []behaviour.Manifest{{ID: 1, Source: "agents:\n  coder: {}\n", Active: s.active == 1}, {ID: 2, Source: "agents: {}\n", Active: s.active == 2}}, nil
}
func (s *manifestTestRepo) ActivateManifest(_ context.Context, id int64) error {
	s.activations++
	if id != 1 && id != 2 {
		return app.ErrNotFound
	}
	s.active = id
	return nil
}

type testNamespaces struct{ repos map[string]Repository }

func (s testNamespaces) Namespaces(context.Context) ([]string, error) {
	return []string{"alpha", "beta"}, nil
}
func (s testNamespaces) Repository(_ context.Context, name string) (Repository, error) {
	if repo, ok := s.repos[name]; ok {
		return repo, nil
	}
	return nil, app.ErrNotFound
}

func TestNamespaceManifestIsolation(t *testing.T) {
	base, alpha, beta := &manifestTestRepo{active: 2}, &manifestTestRepo{active: 2}, &manifestTestRepo{active: 2}
	handler := Handler(base, testNamespaces{map[string]Repository{"alpha": alpha, "beta": beta}})
	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{"GET", "/api/namespaces", 200},
		{"GET", "/api/manifests?namespace=alpha", 200},
		{"GET", "/api/manifests?namespace=missing", 404},
		{"POST", "/api/manifests/1/activate?namespace=alpha", 200},
		{"POST", "/api/manifests/99/activate?namespace=alpha", 404},
		{"POST", "/api/manifests/no/activate?namespace=alpha", 400},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
		if tc.method == "POST" {
			request.Header.Set("Content-Type", "application/json")
		}
		handler.ServeHTTP(response, request)
		if response.Code != tc.code {
			t.Errorf("%s: got %d want %d", tc.path, response.Code, tc.code)
		}
	}
	if alpha.active != 1 || beta.active != 2 || base.active != 2 {
		t.Fatal("manifest activation crossed namespaces")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/manifests?namespace=alpha", nil))
	var manifests []behaviour.Manifest
	if err := json.Unmarshal(response.Body.Bytes(), &manifests); err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 2 || !manifests[0].Active || manifests[0].Source != "agents:\n  coder: {}\n" {
		t.Fatalf("full immutable manifest missing: %+v", manifests)
	}
}

func TestManifestActivationRequiresJSON(t *testing.T) {
	for _, tc := range []struct {
		name, path, contentType, body string
		code                          int
	}{
		{"missing content type", "/api/manifests/1/activate", "", "{}", 415},
		{"form post", "/api/manifests/1/activate", "application/x-www-form-urlencoded", "id=1", 415},
		{"plain text", "/api/manifests/1/activate", "text/plain", "{}", 415},
		{"malformed JSON", "/api/manifests/1/activate", "application/json", "{", 400},
		{"empty JSON", "/api/manifests/1/activate", "application/json", "", 400},
		{"invalid ID first", "/api/manifests/no/activate", "", "", 400},
		{"valid JSON", "/api/manifests/1/activate", "application/json", "{}", 200},
		{"JSON charset", "/api/manifests/1/activate", "application/json; charset=utf-8", "{}", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &manifestTestRepo{active: 2}
			request := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			if tc.contentType != "" {
				request.Header.Set("Content-Type", tc.contentType)
			}
			response := httptest.NewRecorder()
			Handler(repo).ServeHTTP(response, request)
			if response.Code != tc.code {
				t.Fatalf("got %d want %d", response.Code, tc.code)
			}
			if tc.code != 200 && (repo.activations != 0 || repo.active != 2) {
				t.Fatal("invalid request activated manifest")
			}
			if tc.code == 200 && (repo.activations != 1 || repo.active != 1) {
				t.Fatal("valid request did not activate manifest")
			}
		})
	}
}
