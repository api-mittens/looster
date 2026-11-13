package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/postgres"
)

// This opt-in check reads the selected namespace. It does not migrate or write data.
func TestLiveMetadataHTTP(t *testing.T) {
	if os.Getenv("LOOSTER_METADATA_HTTP_CHECK") != "1" {
		t.Skip("set LOOSTER_METADATA_HTTP_CHECK=1 for read-only HTTP checks")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := postgres.OpenNamespace(ctx, os.Getenv("LOOSTER_DATABASE_URL"), os.Getenv("LOOSTER_NAMESPACE"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for name, query := range map[string]string{
		"task":      "SELECT id,name,description,status FROM tasks WHERE id=$1",
		"task logs": "SELECT id,job_id,kind,created_at FROM session_logs WHERE task_id=$1 ORDER BY id DESC LIMIT 200",
		"task runs": "SELECT job_id,outcome,summary_status FROM agent_session_runs WHERE task_id=$1 OR context_task_id=$1 ORDER BY job_id DESC LIMIT 50",
	} {
		rows, err := store.Pool.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+query, int64(80))
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			t.Logf("%s plan: %s", name, line)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	repo, err := app.WithPermissions(store, app.Actor{Role: app.Human})
	if err != nil {
		t.Fatal(err)
	}
	handler := Handler(repo)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "read-only acceptance server", http.StatusMethodNotAllowed)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	t.Logf("metadata server: %s", server.URL)
	if os.Getenv("LOOSTER_METADATA_BROWSER_HOLD") == "1" {
		// Optional manual browser acceptance window. Never permits mutations.
		time.Sleep(5 * time.Minute)
		return
	}
	for _, path := range []string{"/api/board/list", "/api/board", "/api/tasks/TSK-80", "/api/tasks/TSK-80/events?kind=comment", "/api/status"} {
		for attempt := 0; attempt < 2; attempt++ {
			start := time.Now()
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(io.LimitReader(response.Body, (32<<20)+1))
			closeErr := response.Body.Close()
			if readErr != nil || closeErr != nil {
				t.Fatalf("%s read=%v close=%v", path, readErr, closeErr)
			}
			t.Logf("%s attempt=%d status=%d bytes=%d elapsed=%s", path, attempt, response.StatusCode, len(body), time.Since(start))
			if response.StatusCode != 200 || len(body) > 32<<20 || !json.Valid(body) {
				t.Errorf("%s: expected a complete bounded JSON success response", path)
			}
		}
	}
}
