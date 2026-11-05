package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

func TestTaskSessionLazyBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1")
	}
	var detailRequests, outputRequests atomic.Int32
	handler := Handler(&testRepo{})
	task := map[string]any{"id": 1, "name": "Session review", "status": "todo"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			handler.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var data any = []any{}
		switch r.URL.Path {
		case "/api/board":
			data = map[string]any{"tasks": []any{task}, "agents": []any{}, "sessions": []any{}}
		case "/api/status", "/api/system", "/api/library":
			data = map[string]any{}
		case "/api/tasks/TSK-1":
			data = map[string]any{"task": task, "sessions": []any{
				map[string]any{"agent": "coder", "job_id": 11, "status": "done", "short_summary": "First <script>bad</script>", "summary_status": "available"},
				map[string]any{"agent": "coder", "job_id": 12, "status": "running", "summary_status": "pending"},
			}}
		case "/api/tasks/TSK-1/events":
			if r.URL.Query().Get("kind") != "comment" {
				outputRequests.Add(1)
			}
		case "/api/agents/coder/runs/11", "/api/agents/coder/runs/12":
			detailRequests.Add(1)
			id := 11
			if strings.HasSuffix(r.URL.Path, "/12") {
				id = 12
			}
			data = map[string]any{"job_id": id, "agent": "coder", "task_id": 1, "outcome": "done", "summary": "Full summary for " + r.URL.Path}
		}
		json.NewEncoder(w).Encode(data)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	check := func(expression string) {
		t.Helper()
		var ok bool
		if err := chromedp.Run(tab, chromedp.Evaluate(expression, &ok)); err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatal(expression)
		}
	}
	if err := chromedp.Run(tab, chromedp.Navigate(server.URL+"/#board"), chromedp.WaitVisible("looster-board"), chromedp.Evaluate(`document.querySelector('looster-board').open(1)`, nil), chromedp.WaitVisible(`[data-task-session-job="12"]`), chromedp.Evaluate(`document.querySelector('looster-board').loadDetail(1)`, nil)); err != nil {
		t.Fatal(err)
	}
	check(`document.querySelectorAll('#sessions button').length===2 && !document.querySelector('#sessions script') && document.querySelector('#sessions').textContent.includes('First <script>bad</script>') && !document.querySelector('#session-output')`)
	if detailRequests.Load() != 0 || outputRequests.Load() != 0 {
		t.Fatal("task opening eagerly fetched session detail or output")
	}
	if err := chromedp.Run(tab, chromedp.Evaluate(`document.querySelector('#comment-form textarea').value='Unsaved draft'`, nil), chromedp.Click(`[data-task-session-job="11"]`), chromedp.Poll(`document.querySelector('#session-dialog #selected-run').textContent.includes('/11')`, nil), chromedp.KeyEvent(kb.Escape), chromedp.Poll(`!document.querySelector('#session-dialog').open`, nil)); err != nil {
		t.Fatal(err)
	}
	check(`document.querySelector('looster-board').detailID===1 && document.querySelector('looster-board').dialog.open && document.querySelector('#comment-form textarea').value==='Unsaved draft' && document.activeElement.dataset.taskSessionJob==='11'`)
	if err := chromedp.Run(tab, chromedp.Focus(`[data-task-session-job="12"]`), chromedp.KeyEvent(kb.Enter), chromedp.Poll(`document.querySelector('#session-dialog #selected-run').textContent.includes('/12')`, nil), chromedp.Click("#session-back"), chromedp.Poll(`!document.querySelector('#session-dialog').open`, nil)); err != nil {
		t.Fatal(err)
	}
	if detailRequests.Load() != 2 || outputRequests.Load() != 0 {
		t.Fatalf("detail=%d output=%d", detailRequests.Load(), outputRequests.Load())
	}
}
