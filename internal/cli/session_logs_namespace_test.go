package cli

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/postgres"
	"github.com/api-mittens/looster/internal/task"
	"github.com/api-mittens/looster/internal/web"
	"github.com/jackc/pgx/v5"
)

func TestSessionLogHTTPNamespaceRoutesPostgres(t *testing.T) {
	url := os.Getenv("LOOSTER_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set LOOSTER_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx := context.Background()
	prefix := fmt.Sprintf("log_route_%d", time.Now().UnixNano())
	names := []string{prefix, prefix + "_other"}
	stores := make([]*postgres.Store, 2)
	repos := make([]app.Repository, 2)
	var taskID, jobID int64
	for i, name := range names {
		if err := postgres.EnsureNamespace(ctx, url, name); err != nil {
			t.Fatal(err)
		}
		store, err := postgres.OpenNamespace(ctx, url, name)
		if err != nil {
			t.Fatal(err)
		}
		stores[i] = store
		t.Cleanup(func() {
			defer store.Close()
			if _, err := store.Pool.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{"looster_ns_" + name}.Sanitize()+" CASCADE"); err != nil {
				t.Error(err)
			}
		})
		if _, err := store.ApplyManifest(ctx, `AGENT coder WITH "Work";`); err != nil {
			t.Fatal(err)
		}
		created, err := store.CreateTask(ctx, "namespaced task", "", task.Planning, nil)
		if err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := store.Pool.QueryRow(ctx, `WITH e AS (INSERT INTO events(kind,agent_name) VALUES('scheduled','coder') RETURNING id) INSERT INTO jobs(task_id,agent_name,event_id,session_id) SELECT $1,'coder',id,'shared-session' FROM e RETURNING id`, created.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			taskID = created.ID
			jobID = id
		} else if created.ID != taskID || id != jobID {
			t.Fatal("test requires colliding task/run IDs across isolated schemas")
		}
		if _, err := store.AppendSessionLog(ctx, agent.SessionLogInput{JobID: id, Agent: "coder", Kind: "question", Body: "secret " + name}); err != nil {
			t.Fatal(err)
		}
		repos[i], err = app.WithPermissions(store, app.Actor{Role: app.Human})
		if err != nil {
			t.Fatal(err)
		}
	}
	provider := &webNamespaces{url: url, selected: names[0], stores: map[string]*postgres.Store{}, repos: map[string]app.Repository{names[0]: repos[0]}}
	defer provider.Close()
	h := web.Handler(repos[0], web.WithNamespaces(provider))
	for i, name := range names {
		for _, path := range []string{fmt.Sprintf("/api/tasks/%s/session-logs", task.ID(taskID)), fmt.Sprintf("/api/agents/coder/runs/%d/logs", jobID), fmt.Sprintf("/api/agents/coder/runs/%d", jobID)} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", path+"?namespace="+name, nil))
			if w.Code != 200 || !strings.Contains(w.Body.String(), "secret "+name) || strings.Contains(w.Body.String(), "secret "+names[1-i]+`"`) {
				t.Fatalf("namespace %s %s: %d %s", name, path, w.Code, w.Body.String())
			}
		}
	}
	path := fmt.Sprintf("/api/agents/coder/runs/%d/logs?namespace=%s", jobID, names[1])
	req := httptest.NewRequest("POST", path, strings.NewReader(`{"kind":"blocker","body":"selected namespace only"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("POST: %d %s", w.Code, w.Body.String())
	}
	for i, store := range stores {
		logs, err := store.RunSessionLogs(ctx, jobID)
		want := 1 + i
		if err != nil || len(logs) != want {
			t.Fatalf("namespace %s logs %+v %v", names[i], logs, err)
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/api/agents/coder/runs/%d/logs?namespace=missing_namespace", jobID), nil))
	if w.Code != 404 {
		t.Fatalf("unknown namespace: %d", w.Code)
	}
}
