package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/agent"
	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/postgres"
	"github.com/api-mittens/looster/internal/web"
	"github.com/jackc/pgx/v5"
)

// Exercise the production CLI wiring: migrated stores, real scheduled jobs,
// captured done events, authorization wrapper and namespace provider. Earlier
// handler-only fakes bypassed the wrapper and missed unavailable run history.
func TestDashboardScheduledSessionHistoryPostgres(t *testing.T) {
	url := os.Getenv("LOOSTER_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set LOOSTER_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	names := []string{fmt.Sprintf("history_web_%d", time.Now().UnixNano())}
	names = append(names, names[0]+"_other")
	stores := make([]*postgres.Store, 0, 2)
	for i, name := range names {
		if err := postgres.EnsureNamespace(ctx, url, name); err != nil {
			t.Fatal(err)
		}
		store, err := postgres.OpenNamespace(ctx, url, name)
		if err != nil {
			t.Fatal(err)
		}
		stores = append(stores, store)
		t.Cleanup(func() {
			defer store.Close()
			if _, err := store.Pool.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{"looster_ns_" + name}.Sanitize()+" CASCADE"); err != nil {
				t.Error(err)
			}
		})
		if _, err := store.ApplyManifest(ctx, `AGENT solo WITH "Inspect schedules"; EVERY 1 HOURS PING solo WITH "Review recent activity";`); err != nil {
			t.Fatal(err)
		}
		var ruleID int64
		if err := store.Pool.QueryRow(ctx, "SELECT id FROM rules WHERE trigger='every'").Scan(&ruleID); err != nil {
			t.Fatal(err)
		}
		for j := 1; j <= 2; j++ {
			queued, err := store.RunSchedule(ctx, ruleID)
			if err != nil || queued.Status != "queued" {
				t.Fatalf("queue=%+v %v", queued, err)
			}
			lease, err := store.Claim(ctx, []string{"solo"})
			if err != nil || lease == nil {
				t.Fatalf("claim=%v %v", lease, err)
			}
			func() {
				defer lease.Close()
				if err := lease.Started(ctx); err != nil {
					t.Fatal(err)
				}
				text, _ := json.Marshal(map[string]string{"type": "done", "text": fmt.Sprintf("namespace %d scheduled run %d", i, j)})
				if err := lease.Capture(ctx, app.Output{SessionID: "shared-session", Text: string(text)}); err != nil {
					t.Fatal(err)
				}
				if err := lease.Archive(ctx, app.Result{SessionID: "shared-session"}); err != nil {
					t.Fatal(err)
				}
				if err := lease.Complete(ctx); err != nil {
					t.Fatal(err)
				}
			}()
		}
	}
	repo, err := app.WithPermissions(stores[0], app.Actor{Role: app.Human})
	if err != nil {
		t.Fatal(err)
	}
	provider := &webNamespaces{url: url, selected: names[0], repos: map[string]app.Repository{names[0]: repo}, stores: map[string]*postgres.Store{}}
	defer provider.Close()
	handler := web.Handler(repo, web.WithNamespaces(provider))
	for i, suffix := range []string{"", "?namespace=" + names[1]} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/agents/solo/runs"+suffix, nil))
		var page struct {
			Runs []agent.SessionRun `json:"runs"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Runs) != 2 {
			t.Fatalf("namespace %d: %d %s", i, response.Code, response.Body.String())
		}
		for j, run := range page.Runs {
			want := fmt.Sprintf("namespace %d scheduled run %d", i, 2-j)
			if run.TaskID != 0 || run.SessionID != "shared-session" || run.Summary != want || run.SummaryStatus != "available" {
				t.Fatalf("run=%+v want=%s", run, want)
			}
			detail := httptest.NewRecorder()
			handler.ServeHTTP(detail, httptest.NewRequest("GET", fmt.Sprintf("/api/agents/solo/runs/%d%s", run.JobID, suffix), nil))
			if detail.Code != 200 || !strings.Contains(detail.Body.String(), want) || strings.Contains(detail.Body.String(), fmt.Sprintf("namespace %d scheduled", 1-i)) {
				t.Fatalf("detail namespace %d: %d %s", i, detail.Code, detail.Body.String())
			}
		}
	}
}
