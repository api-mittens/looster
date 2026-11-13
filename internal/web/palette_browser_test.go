//go:build uitest

package web

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/postgres"
	"github.com/api-mittens/looster/internal/task"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

func TestPaletteRelevancePostgresBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1 for Chromium acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	first, s := columnIntegrationStore(t, ctx, "palette")
	second, other := columnIntegrationStore(t, ctx, "palette-other")
	exact, err := s.CreateTask(ctx, "quartzneedle", "", task.Todo, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Pool.Exec(ctx, `INSERT INTO tasks(name,description,status,created_at) SELECT (CASE WHEN i%2=0 THEN 'quartzneedle unrelated verbose task ' ELSE 'qxuxaxrxtxzxnxexexdxlxe ' END)||i,'newer fuzzy','todo',now()+interval '1 day' FROM generate_series(1,90) i`)
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := other.CreateTask(ctx, "quartzneedle other namespace", "", task.Todo, nil)
	if err != nil {
		t.Fatal(err)
	}
	provider := columnTestNamespaces{names: []string{first, second}, stores: map[string]*postgres.Store{first: s, second: other}}
	handler := Handler(permissionColumnStore(t, s), WithNamespaces(provider))
	// Real HTTP assertions cover namespace and permission forwarding, and request
	// validation without ever falling back to the unbounded board read.
	for _, tc := range []struct {
		query    string
		status   int
		contains string
	}{
		{"?namespace=" + first + "&search=quartzneedle", 200, "quartzneedle"},
		{"?namespace=" + second + "&search=quartzneedle", 200, hidden.Name},
		{"?namespace=" + first + "&search=" + strings.Repeat("x", 4097), 400, "Search too long"},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/palette/tasks"+tc.query, nil))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("HTTP %d %s", w.Code, w.Body.String())
		}
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	var result string
	script := fmt.Sprintf(`(async()=>{
 const b=document.querySelector('looster-board');
 b.openPalette(); b.paletteInput.value='quartzneedle'; await b.loadPaletteTasks();
 if(b.paletteTasks.length!==50) throw new Error('not global top50: '+b.paletteTasks.length);
 const first=b.palette.querySelector('[role=option] strong');
 if(!first || first.textContent!==%q) throw new Error('exact task not first: '+first?.textContent);
 if(b.paletteItems[0].key!==%q) throw new Error('wrong exact match');
 window.paletteResult='ok';
 })().catch(e=>window.paletteResult=e.stack||String(e));`, fmt.Sprintf("TSK-%d · %s", exact.ID, exact.Name), fmt.Sprintf("task:%d", exact.ID))
	if err = chromedp.Run(tab, chromedp.Navigate(server.URL+"/#board"), chromedp.WaitVisible("#toggle-stages"),
		// The controls exist before the initial refresh finishes. That refresh can
		// reload an open palette and supersede the request this probe awaits.
		chromedp.Poll(`!document.querySelector('looster-board').loading && !!document.querySelector('looster-board').data`, nil),
		chromedp.Evaluate(script, nil, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }),
		chromedp.Evaluate(`window.paletteResult`, &result)); err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Fatal(result)
	}
}
