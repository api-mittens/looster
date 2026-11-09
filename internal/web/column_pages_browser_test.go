package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/api-mittens/looster/internal/app"
	"github.com/api-mittens/looster/internal/postgres"
	"github.com/api-mittens/looster/internal/task"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"github.com/jackc/pgx/v5"
)

type columnTestNamespaces struct {
	names  []string
	stores map[string]*postgres.Store
}

func (p columnTestNamespaces) DefaultNamespace() string                     { return p.names[0] }
func (p columnTestNamespaces) Namespaces(context.Context) ([]string, error) { return p.names, nil }
func (p columnTestNamespaces) Repository(_ context.Context, name string) (app.Repository, error) {
	s, ok := p.stores[name]
	if !ok {
		return nil, fmt.Errorf("unknown namespace")
	}
	return app.WithPermissions(s, app.Actor{Role: app.Human})
}

func permissionColumnStore(t *testing.T, s *postgres.Store) app.Repository {
	t.Helper()
	repo, err := app.WithPermissions(s, app.Actor{Role: app.Human})
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func columnIntegrationStore(t *testing.T, ctx context.Context, suffix string) (string, *postgres.Store) {
	t.Helper()
	url := os.Getenv("LOOSTER_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set LOOSTER_TEST_DATABASE_URL to an isolated PostgreSQL database")
	}
	name := fmt.Sprintf("column-pages-%d-%s", time.Now().UnixNano(), suffix)
	if err := postgres.EnsureNamespace(ctx, url, name); err != nil {
		t.Fatal(err)
	}
	store, err := postgres.OpenNamespace(ctx, url, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := store.Pool.Exec(cleanup, "DROP SCHEMA "+pgx.Identifier{"looster_ns_" + name}.Sanitize()+" CASCADE"); err != nil {
			t.Error(err)
		}
		store.Close()
	})
	return name, store
}

// Real PostgreSQL, real repository, real HTTP routes and real browser. All writes
// are confined to uniquely named disposable test schemas in the explicit test DB.
func TestColumnPaginationPostgresBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1 for Chromium acceptance")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	first, s := columnIntegrationStore(t, ctx, "first")
	second, other := columnIntegrationStore(t, ctx, "second")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO agents(name,description) VALUES('coder','Implementation'); INSERT INTO workers(id,agents,last_seen) VALUES('test',ARRAY['coder'],now()+interval '10 minutes')`)
	owner, err := s.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Release()
	defer owner.Exec(context.Background(), `SELECT pg_advisory_unlock_all()`)
	var removedID int64
	counts := map[task.Status]int{task.Todo: 31, task.Planning: 16, task.Validated: 15, task.Closed: 0}
	statuses := []task.Status{task.Todo, task.Planning, task.Validated, task.Closed}
	for _, status := range statuses {
		for i := 0; i < counts[status]; i++ {
			name := fmt.Sprintf("%s task %02d", status, i)
			if status == task.Todo && i == 0 {
				name = "Needle beyond first page"
			}
			row, err := s.CreateTask(ctx, name, "Pagination acceptance", status, nil)
			if err != nil {
				t.Fatal(err)
			}
			if status == task.Todo {
				if i == 0 {
					removedID = row.ID
				}
				exec(`INSERT INTO sessions(task_id,agent_name,session_id,status) VALUES($1,'coder','run','running')`, row.ID)
				if _, err := owner.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, "looster_ns_"+first+":looster:session:"+fmt.Sprint(row.ID)+":coder"); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if _, err := other.CreateTask(ctx, "Other namespace only", "", task.Todo, nil); err != nil {
		t.Fatal(err)
	}
	metadata, err := s.BoardSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range metadata {
		if session.Status != "running" {
			t.Fatalf("fixture liveness: %+v", session)
		}
	}
	provider := columnTestNamespaces{names: []string{first, second}, stores: map[string]*postgres.Store{first: s, second: other}}
	var unbounded, requests atomic.Int32
	var fail, delay atomic.Bool
	release := make(chan struct{}, 1)
	handler := Handler(permissionColumnStore(t, s), WithNamespaces(provider))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/board" {
			unbounded.Add(1)
		}
		if r.URL.Path == "/api/board/list" {
			requests.Add(1)
			if delay.Load() {
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			if fail.Load() {
				http.Error(w, "Controlled test failure", 500)
				return
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
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
		// Poll in the page world. CDP's isolated-world Poll cannot observe custom
		// element instance fields, even though its DOM wrapper is the same node.
		var ready bool
		script := `(async()=>{const deadline=Date.now()+15000;while(Date.now()<deadline){try {if (` + expression + `) return true;} catch {} await new Promise(r=>setTimeout(r,20));}return false;})()`
		err := chromedp.Run(tab, chromedp.Evaluate(script, &ready, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }))
		if err != nil || !ready {
			var state string
			_ = chromedp.Run(tab, chromedp.Evaluate(`JSON.stringify({loading:document.querySelector('looster-board')?.loading,notice:document.querySelector('#notice')?.textContent,columns:document.querySelector('looster-board')?.data?.columns?.map(c=>({key:c.key,total:c.total,page:c.page})),sessions:document.querySelector('looster-board')?.data?.sessions})`, &state))
			t.Fatalf("%v waiting for %s: %s", err, expression, state)
		}
	}
	evaluate := func(script string) { t.Helper(); run(chromedp.Evaluate(script, nil)) }
	run(chromedp.Navigate(server.URL + "/#board"))
	check(`document.querySelector('looster-board').data?.columns?.some(c=>c.key==='agent:coder'&&c.total===31)`)
	evaluate(`clearInterval(document.querySelector('looster-board').timer)`)
	run(chromedp.Click("#toggle-stages"))
	check(`document.querySelector('[data-refresh-key="todo"] .cards').children.length===15 && document.querySelector('[data-refresh-key="planning"] .cards').children.length===15 && document.querySelector('[data-refresh-key="validated"] .cards').children.length===15`)
	check(`document.querySelector('[data-refresh-key="closed"] .column-page-summary').textContent==='0–0 of 0 tasks' && [...document.querySelectorAll('[data-refresh-key="closed"] .column-pagination button')].every(b=>b.disabled)`)
	check(`document.querySelector('[data-refresh-key="validated"] [aria-label="Next page for validated"]').disabled && !document.querySelector('[data-refresh-key="todo"] [aria-label="Next page for todo"]').disabled`)
	// Controls sit after cards and are ordinary keyboard-operable buttons.
	check(`document.querySelector('[data-refresh-key="todo"] .cards').nextElementSibling.classList.contains('column-pagination')`)
	evaluate(`{const b=document.querySelector('looster-board');b.openPalette();b.paletteInput.value='Needle beyond';b.paletteInput.dispatchEvent(new Event('input'));}`)
	check(`document.querySelector('#palette').open && document.querySelector('#palette .palette-results').textContent.includes('Needle beyond first page')`)
	evaluate(`document.querySelector('#palette').close()`)
	delay.Store(true)
	run(chromedp.Click(`[aria-label="Next page for todo"]`))
	check(`document.querySelector('[data-refresh-key="todo"] .column-pagination').getAttribute('aria-busy')==='true' && document.querySelector('[data-refresh-key="todo"] .column-page-summary').textContent.includes('Loading')`)
	delay.Store(false)
	release <- struct{}{}
	check(`document.querySelector('looster-board').columnPages.todo===2 && !document.querySelector('looster-board').loading`)
	check(`document.querySelector('looster-board').columnPages['agent:coder']===1 && document.querySelector('looster-board').columnPages.planning===1`)
	run(chromedp.Click(`[aria-label="Next page for @ coder"]`))
	check(`document.querySelector('looster-board').columnPages['agent:coder']===2 && !document.querySelector('looster-board').loading`)
	run(chromedp.Focus(`[aria-label="Next page for planning"]`), chromedp.KeyEvent(kb.Enter))
	check(`document.querySelector('looster-board').columnPages.planning===2 && document.querySelector('[data-refresh-key="planning"] .cards').children.length===1 && !document.querySelector('looster-board').loading`)
	run(chromedp.Click("#refresh"))
	check(`!document.querySelector('looster-board').loading && document.querySelector('looster-board').columnPages.todo===2 && document.querySelector('looster-board').columnPages['agent:coder']===2 && document.querySelector('looster-board').columnPages.planning===2`)
	evaluate(`{const b=document.querySelector('looster-board');b.search.value='Needle beyond';b.search.dispatchEvent(new Event('input'));}`)
	check(`!document.querySelector('looster-board').loading && document.querySelector('[data-refresh-key="todo"] .cards').children.length===1 && document.querySelector('[data-refresh-key="todo"]').textContent.includes('Needle beyond first page') && Object.values(document.querySelector('looster-board').columnPages).every(p=>p===1)`)
	evaluate(`{const b=document.querySelector('looster-board');b.search.value='';b.search.dispatchEvent(new Event('input'));}`)
	check(`!document.querySelector('looster-board').loading && document.querySelector('[data-refresh-key="todo"] .count').textContent==='31'`)
	for _, key := range []string{"todo", "agent:coder"} {
		evaluate(fmt.Sprintf(`document.querySelector('looster-board').changeColumnPage(%q,3)`, key))
		check(fmt.Sprintf(`!document.querySelector('looster-board').loading && document.querySelector('looster-board').columnPages[%q]===3`, key))
	}
	if err := s.RemoveTask(ctx, removedID); err != nil {
		t.Fatal(err)
	}
	run(chromedp.Click("#refresh"))
	check(`!document.querySelector('looster-board').loading && document.querySelector('looster-board').columnPages.todo===2 && document.querySelector('looster-board').columnPages['agent:coder']===2 && document.querySelector('[data-refresh-key="todo"] .column-page-summary').textContent==='16–30 of 30 tasks'`)
	fail.Store(true)
	run(chromedp.Click("#refresh"))
	check(`!document.querySelector('looster-board').loading && document.querySelector('[data-refresh-key="todo"] [role="alert"]')?.textContent.includes('Could not load columns')`)
	fail.Store(false)
	run(chromedp.Click(`[data-refresh-key="todo"] [data-column-retry]`))
	check(`!document.querySelector('looster-board').loading && !document.querySelector('[data-refresh-key="todo"] [role="alert"]')`)
	evaluate(fmt.Sprintf(`{const select=document.querySelector('#namespace');select.value=%q;select.dispatchEvent(new Event('change'));}`, second))
	check(`!document.querySelector('looster-board').loading && document.querySelector('[data-refresh-key="todo"]').textContent.includes('Other namespace only') && !document.querySelector('.board').textContent.includes('Pagination acceptance') && Object.values(document.querySelector('looster-board').columnPages).every(p=>p===1)`)
	if unbounded.Load() != 0 {
		t.Fatalf("List made %d unbounded board requests", unbounded.Load())
	}
	if requests.Load() < 8 {
		t.Fatalf("expected server-backed page requests, got %d", requests.Load())
	}
	// Graph deliberately keeps its independent whole-dataset endpoint.
	run(chromedp.Click(`header nav a[href="#graph"]`))
	check(`document.querySelector('looster-board').page==='graph' && document.querySelector('looster-board').data?.tasks?.some(t=>t.name==='Other namespace only')`)
	if unbounded.Load() == 0 {
		t.Fatal("graph did not use the retained graph endpoint")
	}
}

func TestColumnPaginationNamespaceIsolationPostgres(t *testing.T) {
	ctx := context.Background()
	first, s := columnIntegrationStore(t, ctx, "api-a")
	second, other := columnIntegrationStore(t, ctx, "api-b")
	for i := 0; i < 31; i++ {
		if _, err := s.CreateTask(ctx, fmt.Sprintf("First namespace %d", i), "", task.Todo, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := other.CreateTask(ctx, "Second namespace needle", "", task.Todo, nil); err != nil {
		t.Fatal(err)
	}
	provider := columnTestNamespaces{names: []string{first, second}, stores: map[string]*postgres.Store{first: s, second: other}}
	handler := Handler(permissionColumnStore(t, s), WithNamespaces(provider))
	for _, name := range []string{first, second} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/board/list?namespace="+name+"&search=needle", nil))
		if response.Code != 200 {
			t.Fatalf("%d: %s", response.Code, response.Body)
		}
		// Both repositories can have TSK-1. Namespace selection must happen before
		// count, search, page selection and log hydration, not after global loading.
		want := 0
		if name == second {
			want = 1
		}
		var payload struct {
			Columns []task.ColumnPage `json:"columns"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		for _, column := range payload.Columns {
			if column.Key == "todo" && (column.Total != int64(want) || len(column.Tasks) != want) {
				t.Fatalf("HTTP namespace isolation: %+v", column)
			}
		}
		page, err := provider.stores[name].ColumnTasks(ctx, task.ColumnQuery{Key: "todo", Search: "needle", Page: 99})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != int64(want) || page.Page != 1 || len(page.Tasks) != want {
			t.Fatalf("namespace %s: %+v", name, page)
		}
	}
}
