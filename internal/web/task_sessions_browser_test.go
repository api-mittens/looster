package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

func TestTaskSessionLazyBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1")
	}
	var completed atomic.Bool
	var detailRequests, outputRequests, taskOutputRequests atomic.Int32
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
		case "/api/test/complete":
			completed.Store(true)
			data = map[string]any{}
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
				taskOutputRequests.Add(1)
			}
		case "/api/agents/coder/runs/11/output", "/api/agents/coder/runs/12/output":
			outputRequests.Add(1)
			if r.URL.Query().Get("after") == "3" || r.URL.Query().Get("after") == "9" {
				data = map[string]any{"events": []any{}, "has_more": false, "next_after": map[string]int{"3": 3, "9": 9}[r.URL.Query().Get("after")]}
			} else if strings.Contains(r.URL.Path, "/12/") {
				data = map[string]any{"events": []any{map[string]any{"id": 9, "body": `{"type":"text_delta","text":"Different job 12"}`}}, "has_more": false, "next_after": 9}
			} else if r.URL.Query().Get("after") == "0" {
				data = map[string]any{"events": []any{map[string]any{"id": 1, "body": `{"type":"text_delta","text":"Hello "}`}}, "has_more": true, "next_after": 1}
			} else {
				data = map[string]any{"events": []any{map[string]any{"id": 2, "body": `{"type":"text_delta","text":"<safe>"}`}, map[string]any{"id": 3, "body": `{"type":"done","text":"Hello <safe>"}`}}, "has_more": false, "next_after": 3}
			}
		case "/api/agents/coder/runs/11", "/api/agents/coder/runs/12":
			detailRequests.Add(1)
			id := 11
			if strings.HasSuffix(r.URL.Path, "/12") {
				id = 12
			}
			data = map[string]any{"job_id": id, "agent": "coder", "task_id": 1, "outcome": "done", "summary_status": "available", "summary": "Full summary for " + r.URL.Path}
			if id == 12 && !completed.Load() {
				data = map[string]any{"job_id": id, "agent": "coder", "task_id": 1, "outcome": "running", "summary_status": "pending", "summary": "Pending summary for " + r.URL.Path}
			}
		}
		json.NewEncoder(w).Encode(data)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	screenshot := func(name string) {
		t.Helper()
		dir := os.Getenv("LOOSTER_TEST_SCREENSHOT_DIR")
		if dir == "" {
			return
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		var image []byte
		if err := chromedp.Run(tab, chromedp.CaptureScreenshot(&image)); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name+".png")
		if err := os.WriteFile(path, image, 0644); err != nil {
			t.Fatal(err)
		}
		t.Logf("Screenshot: %s", path)
	}
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
	if err := chromedp.Run(tab, emulation.SetDeviceMetricsOverride(1280, 1600, 1, false), chromedp.Navigate(server.URL+"/#board"), chromedp.WaitVisible("looster-board"), chromedp.Evaluate(`document.querySelector('looster-board').open(1)`, nil), chromedp.WaitVisible(`[data-task-session-job="12"]`), chromedp.Evaluate(`document.querySelector('looster-board').loadDetail(1)`, nil)); err != nil {
		t.Fatal(err)
	}
	check(`document.querySelectorAll('#sessions button').length===2 && !document.querySelector('#sessions script') && document.querySelector('#sessions').textContent.includes('First <script>bad</script>') && !document.querySelector('#session-output')`)
	if detailRequests.Load() != 0 || outputRequests.Load() != 0 || taskOutputRequests.Load() != 0 {
		t.Fatal("task opening eagerly fetched session detail or output")
	}
	if err := chromedp.Run(tab, chromedp.Evaluate(`document.querySelector('#sessions').scrollIntoView({block:'end'})`, nil)); err != nil {
		t.Fatal(err)
	}
	screenshot("task-session-cards")
	if err := chromedp.Run(tab, chromedp.Evaluate(`document.querySelector('#comment-form textarea').value='Unsaved draft'`, nil), chromedp.Click(`[data-task-session-job="11"]`), chromedp.Poll(`document.querySelector('#task-session-output').textContent==='Hello <safe>' && document.querySelector('#task-session-run').textContent.includes('/11') && !document.querySelector('#task-session-output safe')`, nil)); err != nil {
		t.Fatal(err)
	}
	screenshot("task-session-detail")
	if err := chromedp.Run(tab, chromedp.Evaluate(`document.querySelector('#session-dialog .detail').scrollTop=0`, nil)); err != nil {
		t.Fatal(err)
	}
	screenshot("task-session-transcript")
	if err := chromedp.Run(tab, chromedp.KeyEvent(kb.Escape), chromedp.Poll(`!document.querySelector('#session-dialog').open`, nil)); err != nil {
		t.Fatal(err)
	}
	check(`document.querySelector('looster-board').detailID===1 && document.querySelector('looster-board').dialog.open && document.querySelector('#comment-form textarea').value==='Unsaved draft' && document.activeElement.dataset.taskSessionJob==='11'`)
	if err := chromedp.Run(tab, chromedp.Focus(`[data-task-session-job="12"]`), chromedp.KeyEvent(kb.Enter), chromedp.Poll(`document.querySelector('#session-dialog #task-session-run').textContent.includes('/12')`, nil), chromedp.Poll(`document.querySelector('#task-session-output').textContent==='Different job 12' && document.querySelector('#task-session-run').textContent.includes('pending')`, nil),
		chromedp.Evaluate(`window.sessionBeforeRefresh=document.querySelector('looster-board').sessionInspection;(async()=>{await fetch('/api/test/complete');await document.querySelector('looster-board').refresh();})()`, nil),
		chromedp.Poll(`document.querySelector('#task-session-run').textContent.includes('available') && document.querySelector('#task-session-run').textContent.includes('Full summary for /api/agents/coder/runs/12') && document.querySelector('#session-dialog').open && document.querySelector('looster-board').sessionInspection===window.sessionBeforeRefresh`, nil),
		chromedp.Click("#session-back"), chromedp.Poll(`!document.querySelector('#session-dialog').open`, nil)); err != nil {
		t.Fatal(err)
	}
	if detailRequests.Load() < 3 || outputRequests.Load() < 3 || taskOutputRequests.Load() != 0 {
		t.Fatalf("detail=%d output=%d taskOutput=%d", detailRequests.Load(), outputRequests.Load(), taskOutputRequests.Load())
	}
}
