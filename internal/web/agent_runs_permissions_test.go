package web

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
)

// Embed the application port, exactly as the CLI does before constructing the
// dashboard. Direct Store-like fakes alone cannot catch capability loss here.
type wrappedHistoryRepo struct {
	app.Repository
	history *historyTestRepo
}

func (r wrappedHistoryRepo) SessionHistory(ctx context.Context, name string, taskID, before int64, limit int) ([]agent.SessionRun, error) {
	return r.history.SessionHistory(ctx, name, taskID, before, limit)
}
func (r wrappedHistoryRepo) SessionRun(ctx context.Context, id int64) (agent.SessionRun, error) {
	return r.history.SessionRun(ctx, id)
}

func TestPermissionWrappedRunHistoryNamespaceRouting(t *testing.T) {
	wrap := func(summary string) Repository {
		t.Helper()
		repo, err := app.WithPermissions(wrappedHistoryRepo{history: &historyTestRepo{runs: []agent.SessionRun{{JobID: 9, Agent: "solo", Summary: summary}}}}, app.Actor{Role: app.Human})
		if err != nil {
			t.Fatal(err)
		}
		return repo
	}
	base, beta := wrap("base secret"), wrap("beta result")
	handler := Handler(base, testNamespaces{map[string]Repository{"beta": beta}})
	for _, path := range []string{"/api/agents/solo/runs", "/api/agents/solo/runs/9"} {
		for _, namespace := range []string{"", "?namespace=beta"} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", path+namespace, nil))
			want, absent := "base secret", "beta result"
			if namespace != "" {
				want, absent = absent, want
			}
			if response.Code != 200 || !strings.Contains(response.Body.String(), want) || strings.Contains(response.Body.String(), absent) {
				t.Fatalf("%s%s: %d %s", path, namespace, response.Code, response.Body.String())
			}
		}
	}
}

func TestPermissionWrappedHistoryUnsupported(t *testing.T) {
	repo, err := app.WithPermissions(struct{ app.Repository }{}, app.Actor{Role: app.Human})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/agents/solo/runs", "/api/agents/solo/runs/9"} {
		response := httptest.NewRecorder()
		Handler(repo).ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 501 {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
	}
}
