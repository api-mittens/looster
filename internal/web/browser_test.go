package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

// Opt in with LOOSTER_BROWSER_TEST=1 go test ./internal/web -run TestGraphBrowser.
// Chromium is the only non-Go executable used by this fixture workflow.
func TestGraphBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1 to run Chromium acceptance")
	}
	chromiumPath(t)
	script := graphbrowserProbe
	tasks := json.RawMessage(`[{"id":1,"name":"Alpha task","status":"planning","comment_count":3,"status_changes":10},{"id":2,"name":"Implementation","status":"todo","comment_count":0,"status_changes":1},{"id":3,"name":"Review","status":"validated","comment_count":14,"status_changes":12},{"id":4,"name":"Done","status":"closed","comment_count":1,"status_changes":3},{"id":5,"name":"Follow-up","status":"todo","comment_count":1,"status_changes":2},{"id":6,"name":"Next item","status":"planning","comment_count":0,"status_changes":1}]`)
	agents := json.RawMessage(`[{"name":"coder","description":"Code"},{"name":"reviewer","description":"Review"}]`)
	sessions := json.RawMessage(`[{"task_id":1,"agent":"coder","status":"running"},{"task_id":2,"agent":"reviewer","status":"idle"},{"task_id":5,"agent":"coder","status":"idle"},{"task_id":6,"agent":"reviewer","status":"idle"},{"task_id":3,"agent":"coder","status":"idle"},{"task_id":4,"agent":"reviewer","status":"idle"}]`)
	var mu sync.Mutex
	var writes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mu.Lock()
			writes = append(writes, r.Method+" "+r.URL.Path)
			mu.Unlock()
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json")
			scoped := tasks
			related := sessions
			if r.URL.Query().Get("namespace") == "beta" {
				scoped = json.RawMessage(`[{"id":1,"name":"Beta only","status":"todo","comment_count":0,"status_changes":1}]`)
				related = json.RawMessage(`[]`)
			}
			var data interface{} = []interface{}{}
			switch r.URL.Path {
			case "/api/namespaces":
				w.Header().Set("X-Default-Namespace", "default")
				data = []string{"default", "beta"}
			case "/api/board":
				data = map[string]interface{}{"tasks": scoped, "agents": agents, "sessions": related}
			case "/api/status":
				data = map[string]interface{}{"workers": []interface{}{map[string]interface{}{"agents": []string{"coder"}}}, "agents": agents, "sessions": sessions}
			case "/api/library":
				data = map[string]interface{}{"agents": agents, "tags": []interface{}{}}
			case "/api/system":
				data = map[string]interface{}{"behaviours": []interface{}{}}
			default:
				if strings.HasPrefix(r.URL.Path, "/api/agents/") {
					if strings.HasSuffix(r.URL.Path, "/runs") {
						id := 101
						if r.URL.Query().Get("before_job_id") == "101" {
							id = 81
						}
						data = map[string]interface{}{"runs": []interface{}{map[string]interface{}{"job_id": id, "agent": "solo", "status": "done"}}, "has_more": id == 101, "next_before_job_id": 101}
					} else {
						data = map[string]interface{}{"job_id": 101, "agent": "solo", "status": "done", "summary": "Run summary <safe>", "outcome": "success", "session_id": "session-101", "started_at": "2026-11-04T10:00:00Z"}
					}
				}
				if regexp.MustCompile(`^/api/tasks/TSK-\d+$`).MatchString(r.URL.Path) {
					var ts []json.RawMessage
					json.Unmarshal(scoped, &ts)
					data = map[string]interface{}{"task": ts[0], "sessions": sessions, "context": map[string]interface{}{"tags": []interface{}{}}}
				}
			}
			json.NewEncoder(w).Encode(data)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if !regexp.MustCompile(`^(?:vendor/)?[\w.-]+$`).MatchString(name) {
			http.NotFound(w, r)
			return
		}
		text, err := os.ReadFile(filepath.Join("static", name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		switch filepath.Ext(name) {
		case ".html":
			w.Header().Set("Content-Type", "text/html")
			text = []byte(strings.Replace(string(text), "</body>", "<script type=\"module\">"+script+"</script></body>", 1))
		case ".css":
			w.Header().Set("Content-Type", "text/css")
		default:
			w.Header().Set("Content-Type", "text/javascript")
		}
		w.Write(text)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// Drive the fixture via CDP instead of dump-dom's virtual clock. Brave on
	// macOS can remain in dump-dom indefinitely despite its virtual time budget.
	opts := []chromedp.ExecAllocatorOption{chromedp.WindowSize(1440, 1100)}
	if os.Getenv("LOOSTER_BROWSER_DARK") == "1" {
		opts = append(opts, chromedp.Flag("force-dark-mode", true))
	}
	tab, stopBrowser := newTestBrowser(t, ctx, opts...)
	defer stopBrowser()
	var output string
	if err := chromedp.Run(tab,
		emulation.SetFocusEmulationEnabled(true),
		chromedp.Navigate(server.URL+"/#graph"),
		chromedp.WaitReady("#browser-result", chromedp.ByQuery),
		chromedp.OuterHTML("html", &output, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("Chromium fixture: %v", err)
	}
	if shot := os.Getenv("LOOSTER_GRAPH_SCREENSHOT"); shot != "" {
		var image []byte
		if err := chromedp.Run(tab, chromedp.CaptureScreenshot(&image)); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(shot, image, 0600); err != nil {
			t.Fatal(err)
		}
	}
	result := regexp.MustCompile(`<pre id="browser-result">([^<]*)</pre>`).FindStringSubmatch(output)
	if len(result) != 2 || !strings.HasPrefix(result[1], "PASS ") {
		t.Fatalf("browser acceptance failed: %s", output)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(writes) > 0 {
		t.Fatalf("graph controls issued writes: %v", writes)
	}
	t.Log(string(result[1]))
}
func chromiumPath(t *testing.T) string {
	t.Helper()
	if path := os.Getenv("CHROMIUM"); path != "" {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Fatal(fmt.Errorf("Chromium is required for opted-in browser tests, set CHROMIUM"))
	return ""
}
