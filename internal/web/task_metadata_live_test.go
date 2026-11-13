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
	if os.Getenv("LOOSTER_METADATA_BROWSER_CHECK") == "1" {
		time.Sleep(180 * time.Second)
	}
}
