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

func TestListVisibilityBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1 to run Chromium acceptance")
	}
	var reviewerOnline atomic.Bool
	static := http.FileServer(http.Dir("static"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			static.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var data any = []any{}
		switch r.URL.Path {
		case "/api/namespaces":
			data = []string{"default"}
		case "/api/board", "/api/board/list":
			data = map[string]any{"tasks": []any{map[string]any{"id": 1, "name": "Visibility fixture", "status": "todo"}}, "agents": []any{map[string]any{"name": "coder"}, map[string]any{"name": "reviewer"}}, "sessions": []any{map[string]any{"task_id": 1, "agent": "coder", "status": "running"}, map[string]any{"task_id": 1, "agent": "reviewer", "status": "running"}}}
		case "/api/status":
			names := []string{"coder"}
			if reviewerOnline.Load() {
				names = append(names, "reviewer")
			}
			data = map[string]any{"workers": []any{map[string]any{"agents": names}}}
		case "/api/system":
			data = map[string]any{"behaviours": []any{}}
		case "/api/library":
			data = map[string]any{"agents": []any{}, "tags": []any{}}
		}
		json.NewEncoder(w).Encode(paginatedBoardFixture(t, r, data))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	run := func(actions ...chromedp.Action) {
		t.Helper()
		if err := chromedp.Run(tab, actions...); err != nil {
			t.Fatal(err)
		}
	}
	check := func(expression string) {
		t.Helper()
		run(chromedp.Poll(expression, nil, chromedp.WithPollingTimeout(10*time.Second)))
	}
	run(chromedp.Navigate(server.URL + "/#board"))
	check(`document.querySelectorAll('.column:not([data-refresh-key="other-sessions"])').length===1 && document.querySelector('.column:not([data-refresh-key="other-sessions"]) h2').textContent.includes('@ coder')`)
	check(`document.querySelector('#toggle-stages').textContent.includes('Show task stages') && document.querySelector('#toggle-stages').getAttribute('aria-expanded')==='false' && document.querySelector('#toggle-offline-agents').textContent==='Show offline agents' && document.querySelector('#toggle-offline-agents').getAttribute('aria-expanded')==='false'`)
	run(chromedp.Click("#toggle-offline-agents"))
	check(`document.querySelectorAll('.column:not([data-refresh-key="other-sessions"])').length===2 && document.querySelector('#toggle-offline-agents').textContent==='Hide offline agents' && document.querySelector('#toggle-offline-agents').getAttribute('aria-expanded')==='true' && document.querySelector('#toggle-stages').getAttribute('aria-expanded')==='false'`)
	run(chromedp.Click("#toggle-stages"))
	check(`document.querySelectorAll('.column:not([data-refresh-key="other-sessions"])').length===6 && document.querySelector('#toggle-stages').textContent.includes('Hide task stages') && document.querySelector('#toggle-stages').getAttribute('aria-expanded')==='true'`)
	run(chromedp.Click("#toggle-offline-agents"))
	check(`document.querySelectorAll('.column:not([data-refresh-key="other-sessions"])').length===5 && document.querySelector('#toggle-stages').getAttribute('aria-expanded')==='true'`)
	run(chromedp.Click("#toggle-stages"), chromedp.Click("#refresh"))
	check(`document.querySelectorAll('.column:not([data-refresh-key="other-sessions"])').length===1`)
	reviewerOnline.Store(true)
	run(chromedp.Click("#refresh"))
	check(`document.querySelectorAll('.column:not([data-refresh-key="other-sessions"])').length===2 && [...document.querySelectorAll('.column:not([data-refresh-key="other-sessions"]) h2')].every(h=>h.textContent.includes('online')&&!h.textContent.includes('offline'))`)
	reviewerOnline.Store(false)
	run(chromedp.Click("#refresh"))
	check(`document.querySelectorAll('.column:not([data-refresh-key="other-sessions"])').length===1`)
	run(chromedp.Click("#toggle-offline-agents"), chromedp.Click(`nav a[href="#servers"]`))
	check(`document.querySelector('#toggle-stages').hidden && document.querySelector('#toggle-offline-agents').hidden`)
	run(chromedp.Click(`nav a[href="#board"]`))
	check(`document.querySelectorAll('.column:not([data-refresh-key="other-sessions"])').length===2 && !document.querySelector('#toggle-offline-agents').hidden && document.querySelector('#toggle-offline-agents').getAttribute('aria-expanded')==='true' && document.querySelector('#toggle-stages').getAttribute('aria-expanded')==='false'`)
}
