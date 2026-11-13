//go:build uitest

package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDashboard(t *testing.T) {
	handler := Handler(&testRepo{})
	for _, path := range []string{"/", "/app.js", "/style.css", "/task-graph.js", "/task-graph.mjs", "/task-graph.css"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 200 {
			t.Fatalf("%s: %d", path, response.Code)
		}
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
