package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/task"
)

type noColumnRepo struct{ Repository }

type pagedRepo struct {
	testRepo
	queries []task.ColumnQuery
	err     error
}

func (*pagedRepo) Tasks(context.Context) ([]task.Task, error) {
	panic("List must not hydrate unbounded Tasks")
}
func (s *pagedRepo) BoardSessions(context.Context) ([]agent.Session, error) {
	return []agent.Session{{TaskID: 1, Agent: "coder", Status: "running"}, {TaskID: 2, Agent: "coder", Status: "done"}, {TaskID: 3, Agent: "reviewer", Status: "interrupted"}, {TaskID: 0, Agent: "standalone", Status: "running"}}, nil
}
func (s *pagedRepo) Agents(context.Context) ([]agent.Agent, error) {
	return []agent.Agent{{Name: "coder"}, {Name: "reviewer"}, {Name: "standalone", TasklessOnly: true}}, nil
}
func (s *pagedRepo) ColumnTasks(_ context.Context, q task.ColumnQuery) (task.ColumnPage, error) {
	s.queries = append(s.queries, q)
	return task.ColumnPage{Key: q.Key, Page: q.Page, PageSize: 15, Total: 31, Tasks: []task.Task{{ID: 1, Name: "Bounded"}}}, s.err
}
func TestListBoardUsesOnlyBoundedQueries(t *testing.T) {
	repo := &pagedRepo{}
	response := httptest.NewRecorder()
	path := "/api/board/list?" + url.Values{"search": {"  Beyond page one  "}, "pages": {`{"todo":3,"agent:coder":2}`}}.Encode()
	Handler(repo).ServeHTTP(response, httptest.NewRequest("GET", path, nil))
	if response.Code != 200 {
		t.Fatalf("%d: %s", response.Code, response.Body)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
	var result struct {
		Columns []task.ColumnPage `json:"columns"`
		Tasks   []task.Task       `json:"tasks"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Tasks) != 0 || len(result.Columns) != 6 || len(repo.queries) != 6 {
		t.Fatalf("unexpected bounded result: %+v", result)
	}
	for _, q := range repo.queries {
		if q.Search != "Beyond page one" {
			t.Fatalf("search=%q", q.Search)
		}
		want := 1
		if q.Key == "todo" {
			want = 3
		}
		if q.Key == "agent:coder" {
			want = 2
			if len(q.ActiveTaskIDs) != 1 || q.ActiveTaskIDs[0] != 1 {
				t.Fatalf("live membership=%v", q.ActiveTaskIDs)
			}
		}
		if q.Page != want {
			t.Fatalf("independent page %s: %d", q.Key, q.Page)
		}
		if q.Key == "agent:reviewer" && len(q.ActiveTaskIDs) != 0 {
			t.Fatalf("interrupted tasks included: %+v", q)
		}
	}
}
func TestListBoardValidationAndErrors(t *testing.T) {
	for _, pages := range []string{`[]`, `{"todo":0}`, `{"todo":-1}`, `{"todo":1.5}`, `{"todo":9999999999999999999999999}`, `bad`} {
		repo := &pagedRepo{}
		response := httptest.NewRecorder()
		Handler(repo).ServeHTTP(response, httptest.NewRequest("GET", "/api/board/list?"+url.Values{"pages": {pages}}.Encode(), nil))
		if response.Code != 400 || len(repo.queries) != 0 {
			t.Fatalf("invalid %q: %d %s", pages, response.Code, response.Body)
		}
	}
	repo := &pagedRepo{err: errors.New("private database data")}
	response := httptest.NewRecorder()
	Handler(repo).ServeHTTP(response, httptest.NewRequest("GET", "/api/board/list", nil))
	if response.Code != 500 || strings.Contains(response.Body.String(), "private") {
		t.Fatalf("error disclosure: %d %s", response.Code, response.Body)
	}
	response = httptest.NewRecorder()
	Handler(&noColumnRepo{Repository: &testRepo{}}).ServeHTTP(response, httptest.NewRequest("GET", "/api/board/list", nil))
	if response.Code != 501 {
		t.Fatalf("unbounded fallback: %d", response.Code)
	}
}

func TestListBoardFuzzyIsExplicitOptIn(t *testing.T) {
	for _, flag := range []string{"", "false", "true"} {
		repo := &pagedRepo{}
		response := httptest.NewRecorder()
		Handler(repo).ServeHTTP(response, httptest.NewRequest("GET", "/api/board/list?search=ndlbynd&fuzzy="+flag, nil))
		if response.Code != 200 || len(repo.queries) == 0 {
			t.Fatalf("fuzzy=%q: %d %s", flag, response.Code, response.Body)
		}
		for _, q := range repo.queries {
			if q.Fuzzy != (flag == "true") {
				t.Fatalf("fuzzy flag not forwarded: %+v", q)
			}
		}
	}
}
