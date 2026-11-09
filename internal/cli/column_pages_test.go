package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/postgres"
	"github.com/api-mittens/looster/internal/task"
	"github.com/api-mittens/looster/internal/web"
	"github.com/jackc/pgx/v5"
)

// Match webCommand's production wiring, including accessRepository on the base
// repo and webNamespaces opening a fresh permission-wrapped selected namespace.
func TestDashboardColumnPaginationPermissionsPostgres(t *testing.T) {
	database := os.Getenv("LOOSTER_TEST_DATABASE_URL")
	if database == "" {
		t.Skip("set LOOSTER_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	names := []string{fmt.Sprintf("columns_web_%d", time.Now().UnixNano())}
	names = append(names, names[0]+"_other")
	stores := []*postgres.Store{}
	for i, name := range names {
		if err := postgres.EnsureNamespace(ctx, database, name); err != nil {
			t.Fatal(err)
		}
		store, err := postgres.OpenNamespace(ctx, database, name)
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
		total := 31
		if i == 1 {
			total = 16
		}
		for j := 0; j < total; j++ {
			title := fmt.Sprintf("namespace %d task %d", i, j)
			if j == 0 {
				title += " needle"
			}
			if _, err := store.CreateTask(ctx, title, "", task.Todo, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	repo, err := app.WithPermissions(stores[0], app.Actor{Role: app.Human})
	if err != nil {
		t.Fatal(err)
	}
	provider := &webNamespaces{url: database, selected: names[0], repos: map[string]app.Repository{names[0]: repo}, stores: map[string]*postgres.Store{}}
	defer provider.Close()
	handler := web.Handler(repo, web.WithNamespaces(provider))
	for i, name := range names {
		for _, search := range []string{"", "needle"} {
			query := url.Values{"pages": {`{"todo":3}`}, "search": {search}}
			// Empty selection exercises the production base repo; the second request
			// opens the namespace through the actual CLI provider, not a store stub.
			if i > 0 {
				query.Set("namespace", name)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/board/list?"+query.Encode(), nil))
			if response.Code != 200 {
				t.Fatalf("production wrapper namespace %d: %d %s", i, response.Code, response.Body)
			}
			var result struct {
				Columns []task.ColumnPage `json:"columns"`
				Tasks   []task.Task       `json:"tasks"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Tasks) != 0 {
				t.Fatal("List returned unbounded task payload")
			}
			for _, column := range result.Columns {
				if column.Key == "todo" {
					total, page := int64(31), 3
					if i == 1 {
						total, page = 16, 2
					}
					if search != "" {
						total, page = 1, 1
					}
					if column.Total != total || column.Page != page || column.PageSize != 15 || len(column.Tasks) != 1 || !strings.Contains(column.Tasks[0].Name, fmt.Sprintf("namespace %d ", i)) {
						t.Fatalf("namespace %d search %q: %+v", i, search, column)
					}
				}
			}
		}
	}
}
