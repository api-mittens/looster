package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

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
	server := httptest.NewServer(Handler(store))
	defer server.Close()
	t.Logf("metadata server: %s", server.URL)
	for _, path := range []string{"/api/board/list", "/api/board", "/api/tasks/TSK-80", "/api/status"} {
		for attempt := 0; attempt < 2; attempt++ {
			start := time.Now()
			response, err := http.Get(server.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			t.Logf("%s attempt=%d status=%d elapsed=%s", path, attempt, response.StatusCode, time.Since(start))
			if response.StatusCode != 200 {
				t.Fail()
			}
		}
	}
}
