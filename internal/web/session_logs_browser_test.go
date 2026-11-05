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
)

func TestSessionLogArrivalBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1")
	}
	var arrived atomic.Bool
	var detailRequests atomic.Int32
	// Each request owns its snapshot. Concurrent polling never races a shared map.
	taskSnapshot := func() map[string]any {
		logs := []any{map[string]any{"id": 1, "job_id": 9, "agent": "coder", "kind": "info", "body": "Existing log", "created_at": "2026-11-05T11:00:00Z"}}
		if arrived.Load() {
			logs = append(logs, map[string]any{"id": 2, "job_id": 9, "agent": "future-agent", "kind": "future<kind>", "body": "Arrived <img src=x onerror=alert(1)>", "created_at": "2026-11-05T10:30:00Z"})
		}
		return map[string]any{"id": 1, "name": "Late log arrival", "status": "todo", "session_logs": logs}
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
			data = map[string]any{"tasks": []any{taskSnapshot()}, "agents": []any{}, "sessions": []any{}}
		case "/api/status", "/api/system", "/api/library":
			data = map[string]any{}
		case "/api/tasks/TSK-1":
			detailRequests.Add(1)
			data = map[string]any{"task": taskSnapshot(), "sessions": []any{}}
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
	check := func(expression, message string) {
		t.Helper()
		var valid bool
		if err := chromedp.Run(tab, chromedp.Evaluate(expression, &valid)); err != nil {
			t.Fatal(err)
		}
		if !valid {
			t.Fatal(message)
		}
	}
	if err := chromedp.Run(tab, chromedp.Navigate(server.URL+"/#board"), chromedp.WaitVisible("#toggle-stages"), chromedp.Click("#toggle-stages"), chromedp.Click(`task-card[data-id="1"] button`), chromedp.WaitVisible(`#task-comments .session-log`), chromedp.Evaluate(`(()=>{const draft=document.querySelector('#comment-form textarea');draft.value='Unsaved draft remains';draft.focus();draft.setSelectionRange(3,8);window.savedDraft=draft;window.savedComment=document.querySelector('[data-comment-id="1"]');})()`, nil)); err != nil {
		t.Fatal(err)
	}
	check(`document.querySelectorAll('#task-comments > *').length===3&&!document.querySelector('#task-comments').textContent.includes('Arrived')&&document.activeElement===window.savedDraft`, "fixture must open before the new log exists and focus the unsaved draft")
	arrived.Store(true)
	// Wait for the actual refresh promise before inspecting the preserved dialog.
	if err := chromedp.Run(tab, chromedp.Evaluate(`document.querySelector('looster-board').dataset.arrivalRefreshDone='false';document.querySelector('looster-board').refresh().then(()=>document.querySelector('looster-board').dataset.arrivalRefreshDone='true')`, nil), chromedp.Poll(`document.querySelector('looster-board').dataset.arrivalRefreshDone==='true'&&document.querySelectorAll('#task-comments .session-log').length===2`, nil, chromedp.WithPollingInterval(20*time.Millisecond))); err != nil {
		var state string
		_ = chromedp.Run(tab, chromedp.Evaluate(`JSON.stringify({done:document.querySelector('looster-board').dataset.arrivalRefreshDone==='true',notice:document.querySelector('#notice').textContent,detailID:document.querySelector('looster-board').detailID,dialogOpen:document.querySelector('looster-board').dialog.open,timeline:document.querySelector('#task-comments')?.innerHTML})`, &state))
		t.Fatalf("late arrival refresh: %v, detail requests=%d, browser=%s", err, detailRequests.Load(), state)
	}
	const arrivalState = `(()=>{const box=document.querySelector('#task-comments'),children=[...box.children],draft=document.querySelector('#comment-form textarea');return children.length===4&&children[0].textContent.includes('Before log')&&children[1].textContent.includes('Arrived <img')&&children[1].textContent.includes('future<kind>')&&children[1].textContent.includes('future-agent')&&children[2].textContent.includes('Existing log')&&children[3].textContent.includes('After log')&&box.querySelectorAll('[data-log-id="2"]').length===1&&!box.querySelector('img,future,script,[data-resolve-log]')&&document.querySelector('[data-comment-id="1"]')===window.savedComment&&draft===window.savedDraft&&draft.value==='Unsaved draft remains'&&document.activeElement===draft&&draft.selectionStart===3&&draft.selectionEnd===8&&!document.querySelector('task-card .signal-badge,task-card .task-blocked');})()`
	check(arrivalState, "late unknown-kind log must insert chronologically and safely without signals, duplicates, lost draft, or changed focus/selection")
	before := detailRequests.Load()
	if err := chromedp.Run(tab, chromedp.Evaluate(`document.querySelector('looster-board').dataset.arrivalRefreshDone='false';document.querySelector('looster-board').refresh().then(()=>document.querySelector('looster-board').dataset.arrivalRefreshDone='true')`, nil), chromedp.Poll(`document.querySelector('looster-board').dataset.arrivalRefreshDone==='true'`, nil, chromedp.WithPollingInterval(20*time.Millisecond))); err != nil {
		t.Fatal(err)
	}
	if detailRequests.Load() <= before {
		t.Fatal("duplicate check must exercise another actual task detail request")
	}
	check(arrivalState, "repeated refresh must retain one copy of the late log and preserve draft focus")
	if err := chromedp.Run(tab, chromedp.Evaluate(`document.querySelector('looster-board').dialog.close();location.hash='#graph'`, nil), chromedp.WaitVisible(`task-agent-graph [data-key="task:1"]`)); err != nil {
		t.Fatal(err)
	}
	check(`(()=>{const graph=document.querySelector('task-agent-graph');return !graph.querySelector('.tg-task-signal')&&!graph.graph.nodes.find(n=>n.key==='task:1').blocker&&!graph.graph.nodes.find(n=>n.key==='task:1').question;})()`, "future unknown log kind must not trigger graph question or blocker signals")
}

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
