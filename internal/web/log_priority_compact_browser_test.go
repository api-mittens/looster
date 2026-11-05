package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

func TestLogPriorityCompactBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1")
	}
	var tasks, sessions []any
	details := map[string]any{}
	now := time.Now().UTC()
	for index, status := range []string{"planning", "todo", "closed", "validated"} {
		for offset, age := range []int{2, 4, 3, 0} {
			id := index*10 + offset + 1
			logs := []any{}
			if offset == 0 || offset == 2 {
				logs = append(logs, map[string]any{"kind": "question", "agent": "coder"})
			}
			if offset == 1 || offset == 2 {
				logs = append(logs, map[string]any{"kind": "blocker", "agent": "coder"})
			}
			task := map[string]any{"id": id, "name": fmt.Sprintf("Full accessible task title %d", id), "status": status, "last_activity_at": now.Add(-time.Duration(age) * 24 * time.Hour).Format(time.RFC3339), "session_logs": logs}
			tasks = append(tasks, task)
			sessions = append(sessions, map[string]any{"agent": "coder", "task_id": id, "job_id": id, "status": "running"})
			details[fmt.Sprintf("/api/tasks/TSK-%d", id)] = map[string]any{"task": task, "sessions": []any{}}
		}
	}
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
			data = map[string]any{"tasks": tasks, "sessions": sessions, "agents": []any{map[string]any{"name": "coder"}}}
		case "/api/status":
			data = map[string]any{"workers": []any{map[string]any{"agents": []string{"coder"}}}}
		case "/api/system", "/api/library":
			data = map[string]any{}
		default:
			if detail, ok := details[r.URL.Path]; ok {
				data = detail
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
	if err := chromedp.Run(tab, chromedp.Navigate(server.URL+"/#board"), chromedp.WaitVisible("#toggle-stages"), chromedp.Click("#toggle-stages"), chromedp.WaitVisible(`.column[data-refresh-key="closed"] task-card`), chromedp.Evaluate(`(()=>{const ids=key=>[...document.querySelectorAll('.column[data-refresh-key="'+key+'"] task-card')].map(c=>+c.dataset.id).join(',');return ids('planning')==='3,2,1,4'&&ids('todo')==='13,12,11,14'&&ids('closed')==='24,21,23,22'&&ids('validated')==='34,31,33,32'&&ids('agent:coder')==='3,13,23,33,2,12,22,32,1,11,21,31,4,14,24,34';})()`, &valid)); err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("scoped blocker/question promotion or recency ordering is incorrect")
	}
	if err := chromedp.Run(tab, chromedp.Evaluate(`(()=>{const b=document.querySelector('.column[data-refresh-key="closed"] [data-id="22"] button'),v=document.querySelector('.column[data-refresh-key="validated"] [data-id="32"] button'),badge=b.querySelector('.signal-badge');return b.classList.contains('task-compact')&&v.classList.contains('task-compact')&&b.getAttribute('aria-label').includes('Full accessible task title 22')&&badge.getClientRects().length>0&&badge.textContent.includes('⚠️')&&getComputedStyle(b.querySelector('.signal-label')).display==='none'&&b.getBoundingClientRect().height<60&&+getComputedStyle(b).opacity<1;})()`, &valid)); err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("compact cards must retain readable inline signals and accessible identity")
	}
	if err := chromedp.Run(tab, chromedp.Focus(`.column[data-refresh-key="closed"] [data-id="22"] button`), chromedp.KeyEvent(kb.Enter), chromedp.WaitVisible(`#task-comments`), chromedp.Evaluate(`document.querySelector('looster-board').detailID===22&&document.querySelector('#dialog-title').textContent.includes('Full accessible task title 22')`, &valid)); err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("compact task card did not open via keyboard")
	}
}
