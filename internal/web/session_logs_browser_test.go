package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestSessionLogsBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1")
	}
	task := map[string]any{"id": 1, "name": "Log timeline", "status": "todo"}
	log := map[string]any{"id": 1, "job_id": 9, "agent": "coder", "kind": "blocker", "body": "Historical <img src=x onerror=alert(1)>", "created_at": "2026-11-05T11:00:00Z"}
	task["session_logs"] = []any{log}
	handler := Handler(&testRepo{})
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
			data = map[string]any{"task": task, "sessions": []any{}}
		case "/api/tasks/TSK-1/events":
			if r.URL.Query().Get("after") == "0" {
				data = []any{map[string]any{"id": 1, "body": "Before log", "created_at": "2026-11-05T10:00:00Z"}, map[string]any{"id": 2, "body": "After log", "created_at": "2026-11-05T12:00:00Z"}}
			}
		}
		json.NewEncoder(w).Encode(data)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	var valid bool
	if err := chromedp.Run(tab, chromedp.Navigate(server.URL+"/#board"), chromedp.WaitVisible("#toggle-stages"), chromedp.Click("#toggle-stages"), chromedp.WaitVisible(`task-card[data-id="1"] button`), chromedp.Click(`task-card[data-id="1"] button`), chromedp.WaitVisible(`#task-comments .session-log`), chromedp.Evaluate(`(()=>{const box=document.querySelector('#task-comments');const children=[...box.children];return children.length===3&&children[0].textContent.includes('Before log')&&children[1].textContent.includes('Historical <img')&&children[1].textContent.includes('coder')&&children[2].textContent.includes('After log')&&!box.querySelector('img')&&!box.querySelector('[data-resolve-log]');})()`, &valid)); err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("logs must interleave chronologically, remain safe and read-only")
	}
	// Repeated polling must preserve unique log/comment identities and their ordering.
	if err := chromedp.Run(tab, chromedp.Evaluate(`document.querySelector('looster-board').loadDetail(1)`, nil), chromedp.Evaluate(`document.querySelectorAll('#task-comments .session-log').length===1&&document.querySelectorAll('#task-comments > *').length===3`, &valid)); err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("refresh duplicated timeline entries")
	}
	if err := chromedp.Run(tab, chromedp.Evaluate(`document.querySelector('looster-board').dialog.close();location.hash='#graph'`, nil), chromedp.WaitVisible(`.tg-task-signal`), chromedp.Evaluate(`(()=>{const el=document.querySelector('task-agent-graph');el.settle();const m=el.querySelector('[data-key="task:1"] .tg-task-signal'),x=+m.getAttribute('x'),y=+m.getAttribute('y');return el.graph.edges.length===0&&m.textContent.includes('⚠️')&&x>0&&y<0&&Math.abs(Math.hypot(x,y)-30)<.001;})()`, &valid)); err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("graph log marker must sit on task circle top-right edge even without sessions")
	}
}
