package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/chromedp"
)

// Uses the embedded production page and modules with a stateful HTTP API fixture.
// No Node, package install, build step, or externally hosted asset is needed.
func TestMarkdownBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1 to run Chromium markdown acceptance")
	}
	source := "# Markdown heading\n\n**Strong** and *emphasis* [safe](https://example.com).\n\n- item one\n- item two\n\n```js\n" + strings.Repeat("unbroken_code", 100) + "\n```\n\n| Name | Value |\n| --- | --- |\n| " + strings.Repeat("wide", 100) + " | cell |\n\n<script>globalThis.markdownPwned=1</script>\n\n<img src=x onerror=\"globalThis.markdownPwned=2\">\n\n[unsafe](javascript:alert(1))"
	existing := "## Existing comment\n\n**Stored** and *safe*\n\n<script>globalThis.markdownPwned=3</script>"
	submitted := "## New comment\n\n**Posted** and [unsafe](javascript:alert(1))\n\n- fresh item\n\n```\n<not-html>\n```"
	var mu sync.Mutex
	comments := []map[string]any{{"id": 1, "kind": "comment", "body": existing, "actor_agent": "human", "created_at": "2026-11-03T18:00:00Z"}}
	var posted string
	handler := Handler(&testRepo{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			handler.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		taskData := func(id int) map[string]any {
			description := source
			if id == 2 {
				description = ""
			}
			return map[string]any{"id": id, "name": fmt.Sprintf("Markdown %d", id), "status": "todo", "description": description}
		}
		var data any = []any{}
		switch r.URL.Path {
		case "/api/namespaces":
			w.Header().Set("X-Default-Namespace", "default")
			data = []string{"default"}
		case "/api/board":
			data = map[string]any{"tasks": []any{taskData(1), taskData(2)}, "agents": []any{}, "sessions": []any{}}
		case "/api/status", "/api/system", "/api/library":
			data = map[string]any{}
		case "/api/tasks/TSK-1":
			data = map[string]any{"task": taskData(1), "sessions": []any{}}
		case "/api/tasks/TSK-2":
			data = map[string]any{"task": taskData(2), "sessions": []any{}}
		case "/api/tasks/TSK-1/comments":
			var body struct {
				Body string `json:"body"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			posted = body.Body
			comments = append(comments, map[string]any{"id": len(comments) + 1, "kind": "comment", "body": body.Body, "actor_agent": "human", "created_at": "2026-11-03T18:01:00Z"})
			w.WriteHeader(http.StatusCreated)
			data = map[string]any{"id": len(comments)}
		case "/api/tasks/TSK-1/events":
			if r.URL.Query().Get("kind") == "comment" {
				after, _ := strconv.Atoi(r.URL.Query().Get("after"))
				result := []any{}
				for _, c := range comments {
					if c["id"].(int) > after {
						result = append(result, c)
					}
				}
				data = result
			}
		}
		if err := json.NewEncoder(w).Encode(data); err != nil {
			t.Errorf("fixture response: %v", err)
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	evaluateTrue := func(t *testing.T, expression string) {
		t.Helper()
		var ok bool
		if err := chromedp.Run(tab, chromedp.Evaluate(expression, &ok)); err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("browser assertion failed: %s", expression)
		}
	}
	for _, mode := range []string{"dark", "light"} {
		t.Run(mode, func(t *testing.T) {
			if err := chromedp.Run(tab, emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: mode}}), emulation.SetDeviceMetricsOverride(375, 900, 1, false), chromedp.Navigate(server.URL+"/?theme="+mode+"#board"), chromedp.Click("#toggle-stages"), chromedp.WaitVisible("task-card"), chromedp.Click(`task-card[data-id="1"]`), chromedp.WaitVisible("#task-description h1"), chromedp.WaitVisible("#task-comments h2")); err != nil {
				t.Fatal(err)
			}
			evaluateTrue(t, `matchMedia('(prefers-color-scheme: dark)').matches===`+strconv.FormatBool(mode == "dark"))
			evaluateTrue(t, `(() => {const d=document.querySelector('#task-description');return d.querySelector('h1').textContent==='Markdown heading' && !!d.querySelector('strong') && !!d.querySelector('em') && d.querySelectorAll('li').length===2 && d.querySelector('a').href==='https://example.com/' && !!d.querySelector('pre code.language-js') && !!d.querySelector('table th') && !!d.querySelector('table td');})()`)
			evaluateTrue(t, `(() => {const d=document.querySelector('.detail');return !globalThis.markdownPwned && !d.querySelector('script,img,[onerror],a[href^="javascript:"]') && document.querySelector('#task-description').textContent.includes('<script>') && document.querySelector('#task-comments h2').textContent==='Existing comment';})()`)
			evaluateTrue(t, `(() => {const d=document.querySelector('.detail'), m=document.querySelector('#task-description'), p=m.querySelector('pre'), table=m.querySelector('table');return d.scrollWidth<=d.clientWidth+1 && m.getBoundingClientRect().right<=d.getBoundingClientRect().right+1 && ['auto','scroll'].includes(getComputedStyle(p).overflowX) && ['auto','scroll'].includes(getComputedStyle(table).overflowX) && getComputedStyle(m).color!==getComputedStyle(document.querySelector('dialog')).backgroundColor;})()`)
			evaluateTrue(t, `document.querySelector('looster-board').comments.get(1).body===`+strconv.Quote(existing))
			sourceJSON, _ := json.Marshal(source)
			evaluateTrue(t, `document.querySelector('looster-board').data.tasks.find(t=>t.id===1).description===`+string(sourceJSON))
		})
	}
	// Submit through the real form handler, then refresh the page and reopen details.
	bodyJSON, _ := json.Marshal(submitted)
	if err := chromedp.Run(tab, chromedp.Evaluate(`document.querySelector('#comment-form textarea').value=`+string(bodyJSON), nil), chromedp.Evaluate(`document.querySelector('#comment-form').requestSubmit()`, nil), chromedp.Poll(`document.querySelectorAll('#task-comments article').length===2`, nil)); err != nil {
		t.Fatal(err)
	}
	evaluateTrue(t, `document.querySelector('#task-comments article:last-child h2').textContent==='New comment' && document.querySelector('#task-comments article:last-child strong').textContent==='Posted' && document.querySelector('#comment-form textarea').value==='' && !document.querySelector('#task-comments a[href^="javascript:"]')`)
	evaluateTrue(t, `document.querySelector('looster-board').comments.get(2).body===`+string(bodyJSON))
	mu.Lock()
	gotPosted := posted
	mu.Unlock()
	if gotPosted != submitted {
		t.Fatalf("comment source changed: got %q want %q", gotPosted, submitted)
	}
	if err := chromedp.Run(tab, chromedp.Reload(), chromedp.Click("#toggle-stages"), chromedp.WaitVisible("task-card"), chromedp.Click(`task-card[data-id="1"]`), chromedp.WaitVisible("#task-comments article:nth-child(2) h2")); err != nil {
		t.Fatal(err)
	}
	evaluateTrue(t, `document.querySelectorAll('#task-comments article').length===2 && document.querySelector('#task-comments article:first-child h2').textContent==='Existing comment' && document.querySelector('#task-comments article:last-child code').textContent.includes('<not-html>')`)
	if err := chromedp.Run(tab, chromedp.Click("#close"), chromedp.Poll(`!document.querySelector('looster-board').dialog.open && document.querySelector('looster-board').detailID===null`, nil), chromedp.Click(`task-card[data-id="2"]`), chromedp.WaitVisible("#task-description"), chromedp.Poll(`document.querySelector('#task-description').textContent.trim()==='No description.' && document.querySelector('#task-comments').textContent.trim()==='No comments yet.'`, nil)); err != nil {
		t.Fatal(err)
	}
}
