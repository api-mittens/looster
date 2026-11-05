package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/behaviour"
)

type scheduleTestRepo struct {
	testRepo
	calls  []int64
	status string
	err    error
}

func (s *scheduleTestRepo) RunSchedule(_ context.Context, id int64) (app.ScheduleRunResult, error) {
	s.calls = append(s.calls, id)
	return app.ScheduleRunResult{RuleID: id, Status: s.status, AgentName: "coder"}, s.err
}

func (s *scheduleTestRepo) Behaviour(context.Context) ([]behaviour.Rule, error) {
	return []behaviour.Rule{{ID: 42, Trigger: "every", Target: "coder", IntervalSeconds: 60}, {ID: 43, Trigger: "every", Target: "coder", IntervalSeconds: 120}}, nil
}

func TestManualScheduleSystemIDs(t *testing.T) {
	response := httptest.NewRecorder()
	Handler(&scheduleTestRepo{}).ServeHTTP(response, httptest.NewRequest("GET", "/api/system", nil))
	var result struct {
		Rules []behaviour.Rule `json:"behaviours"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || len(result.Rules) != 2 || result.Rules[0].ID != 42 || result.Rules[1].ID != 43 {
		t.Fatalf("system rules: %d %s", response.Code, response.Body.String())
	}
}

func TestManualScheduleAPI(t *testing.T) {
	for _, tc := range []struct {
		name, path, body, contentType, status string
		err                                   error
		code, calls                           int
	}{
		{"queued", "42", "{}", "application/json", "queued", nil, 200, 1},
		{"already active", "42", "{}", "application/json", "already_active", nil, 200, 1},
		{"missing", "42", "{}", "application/json", "", app.ErrNotFound, 404, 1},
		{"forbidden", "42", "{}", "application/json", "", app.ErrForbidden, 403, 1},
		{"internal", "42", "{}", "application/json", "", errors.New("private storage details"), 500, 1},
		{"invalid id", "oops", "{}", "application/json", "", nil, 400, 0},
		{"negative id", "-1", "{}", "application/json", "", nil, 400, 0},
		{"zero id", "0", "{}", "application/json", "", nil, 400, 0},
		{"invalid json", "42", "{", "application/json", "", nil, 400, 0},
		{"json required", "42", "{}", "text/plain", "", nil, 415, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &scheduleTestRepo{status: tc.status, err: tc.err}
			response := httptest.NewRecorder()
			request := httptest.NewRequest("POST", "/api/behaviours/"+tc.path+"/run", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", tc.contentType)
			Handler(repo).ServeHTTP(response, request)
			if response.Code != tc.code || len(repo.calls) != tc.calls {
				t.Fatalf("status %d body %s calls %v", response.Code, response.Body.String(), repo.calls)
			}
			if response.Code == 200 {
				var result app.ScheduleRunResult
				if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result.RuleID != 42 || result.Status != tc.status || result.AgentName != "coder" {
					t.Fatalf("result %+v", result)
				}
				if response.Header().Get("Content-Type") != "application/json" {
					t.Fatal("response is not JSON")
				}
			}
			if strings.Contains(response.Body.String(), "private storage") {
				t.Fatal("private error leaked")
			}
		})
	}
}

func TestManualScheduleNamespaceIsolation(t *testing.T) {
	base, alpha, beta := &scheduleTestRepo{status: "queued"}, &scheduleTestRepo{status: "queued"}, &scheduleTestRepo{status: "queued"}
	handler := Handler(base, testNamespaces{map[string]Repository{"alpha": alpha, "beta": beta}})
	for _, ns := range []string{"alpha", "beta", "missing"} {
		request := httptest.NewRequest("POST", "/api/behaviours/42/run?namespace="+ns, strings.NewReader("{}"))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		want := 200
		if ns == "missing" {
			want = 404
		}
		if response.Code != want {
			t.Fatalf("%s: %d %s", ns, response.Code, response.Body.String())
		}
	}
	if len(base.calls) != 0 || len(alpha.calls) != 1 || len(beta.calls) != 1 {
		t.Fatalf("namespace leak: base %v alpha %v beta %v", base.calls, alpha.calls, beta.calls)
	}
}

type permissionScheduleRepo struct {
	app.Repository
	calls int
}

func (s *permissionScheduleRepo) RunSchedule(_ context.Context, id int64) (app.ScheduleRunResult, error) {
	s.calls++
	return app.ScheduleRunResult{RuleID: id, Status: "queued", AgentName: "coder"}, nil
}

func TestManualScheduleHumanOnly(t *testing.T) {
	for _, role := range []app.Role{app.Human, app.Agent} {
		t.Run(string(role), func(t *testing.T) {
			underlying := &permissionScheduleRepo{}
			secured, err := app.WithPermissions(underlying, app.Actor{Role: role, Name: "coder"})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			request := httptest.NewRequest("POST", "/api/behaviours/42/run", strings.NewReader("{}"))
			request.Header.Set("Content-Type", "application/json")
			Handler(secured).ServeHTTP(response, request)
			want, calls := 200, 1
			if role == app.Agent {
				want, calls = 403, 0
			}
			if response.Code != want || underlying.calls != calls {
				t.Fatalf("status %d calls %d body %s", response.Code, underlying.calls, response.Body.String())
			}
		})
	}
}

func TestManualScheduleUnavailableAndMethod(t *testing.T) {
	handler := Handler(&testRepo{})
	for _, method := range []string{"POST", "GET"} {
		request := httptest.NewRequest(method, "/api/behaviours/42/run", strings.NewReader("{}"))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		want := 501
		if method == "GET" {
			want = 404
		}
		if response.Code != want {
			t.Fatalf("%s status %d", method, response.Code)
		}
	}
}
