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

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// TestMobileTouchBrowser uses native CDP touch input, not element.click(), so
// hit testing, sticky chrome, scrolling and pointer capture remain under test.
func TestMobileTouchBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1 for Chromium acceptance")
	}
	tasks := make([]map[string]any, 12)
	for i := range tasks {
		tasks[i] = map[string]any{"id": i + 1, "name": fmt.Sprintf("Mobile task %d %s", i+1, strings.Repeat("unbrokenlongtitle", 12)), "status": "todo", "description": strings.Repeat("Long description ", 80), "tags": []string{strings.Repeat("longtag", 20), "mobile"}}
	}
	sessions := []any{map[string]any{"task_id": 1, "agent": "coder", "job_id": 11, "status": "running", "summary_status": "available"}}
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
		case "/api/board":
			data = map[string]any{"tasks": tasks, "agents": []any{map[string]any{"name": "coder"}}, "sessions": sessions}
		case "/api/status":
			data = map[string]any{"workers": []any{map[string]any{"agents": []string{"coder"}}}}
		case "/api/system", "/api/library":
			data = map[string]any{}
		case "/api/agents/coder/runs/11":
			data = map[string]any{"job_id": 11, "agent": "coder", "task_id": 1, "outcome": "running", "summary_status": "available", "summary": "Mobile session summary"}
		case "/api/agents/coder/runs/11/output":
			data = map[string]any{"events": []any{}, "has_more": false, "next_after": 0}
		default:
			if strings.HasPrefix(r.URL.Path, "/api/tasks/TSK-") && !strings.Contains(strings.TrimPrefix(r.URL.Path, "/api/tasks/TSK-"), "/") {
				data = map[string]any{"task": tasks[0], "sessions": sessions}
			}
		}
		_ = json.NewEncoder(w).Encode(data)
	}))
	defer server.Close()
	for _, size := range []struct {
		name   string
		w, h   int64
		mobile bool
	}{{"portrait320", 320, 740, true}, {"portrait375", 375, 812, true}, {"portrait390", 390, 844, true}, {"landscape", 844, 390, true}, {"desktop", 1440, 1000, false}} {
		t.Run(size.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			// Native CDP touch is bounded by the host window as well as the
			// emulated viewport. Leave room for browser chrome on macOS.
			tab, shutdown := newTestBrowser(t, ctx, chromedp.WindowSize(int(size.w), int(size.h)+160))
			defer shutdown()
			run := func(a ...chromedp.Action) {
				t.Helper()
				if err := chromedp.Run(tab, a...); err != nil {
					t.Fatal(err)
				}
			}
			check := func(js string) {
				t.Helper()
				deadline := time.Now().Add(8 * time.Second)
				for {
					var ok bool
					run(chromedp.Evaluate(js, &ok))
					if ok {
						return
					}
					if time.Now().After(deadline) {
						var state string
						run(chromedp.Evaluate(`JSON.stringify({rails:[...document.querySelectorAll('.column .cards')].map(e=>({key:e.parentElement.dataset.refreshKey,left:e.scrollLeft,width:e.clientWidth,content:e.scrollWidth})),dialogs:[...document.querySelectorAll('dialog')].map(d=>({id:d.id,open:d.open,text:d.textContent.slice(-1800)})),url:location.href})`, &state))
						t.Fatalf("acceptance condition: %s\n%s", js, state)
					}
					time.Sleep(25 * time.Millisecond)
				}
			}
			eval := func(js string, out any) { t.Helper(); run(chromedp.Evaluate(js, out)) }
			tap := func(selector string) { t.Helper(); mobileTouchTarget(t, tab, selector, false) }
			overflow := func() { t.Helper(); check(`document.documentElement.scrollWidth<=innerWidth+1`) }
			run(emulation.SetDeviceMetricsOverride(size.w, size.h, 1, size.mobile), emulation.SetTouchEmulationEnabled(true), chromedp.Navigate(server.URL+"/#board"), chromedp.WaitVisible("#toggle-stages"))
			check(`document.querySelectorAll('task-card').length>0`)
			eval(`clearInterval(document.querySelector('looster-board').timer)`, nil)
			overflow()
			tap("#toggle-stages")
			check(`document.querySelector('[data-refresh-key="todo"] task-card')!==null`)
			check(`JSON.stringify([...document.querySelectorAll('.board>.column')].map(c=>c.dataset.refreshKey))===JSON.stringify(['planning','todo','validated','closed','agent:coder','other-sessions'])`)
			overflow()
			if size.w <= 650 {
				check(`(()=>{const c=[...document.querySelectorAll('.board>.column')].map(e=>e.getBoundingClientRect());return c.length>=5&&c.every((r,i)=>!i||(r.top>=c[i-1].bottom-1&&Math.abs(r.left-c[0].left)<2))})()`)
				rail := `document.querySelector('[data-refresh-key="todo"] .cards')`
				check(rail + `.scrollWidth>` + rail + `.clientWidth+100`)
				mobileTouchTarget(t, tab, `[data-refresh-key="todo"] .cards`, true)
				check(rail + `.scrollLeft>20`)
				var before float64
				// Momentum can continue after touchend. Establish a stable,
				// nonzero native position before testing refresh preservation.
				stable := 0
				for i := 0; i < 40 && stable < 4; i++ {
					var next float64
					eval(rail+`.scrollLeft`, &next)
					if next == before {
						stable++
					} else {
						stable = 0
					}
					before = next
					time.Sleep(50 * time.Millisecond)
				}
				if stable < 4 {
					t.Fatal("native rail momentum did not settle")
				}
				tap("#refresh")
				check(`!document.querySelector('looster-board').loading`)
				check(fmt.Sprintf(`Math.abs(%s.scrollLeft-%f)<2`, rail, before))
			} else if !size.mobile {
				check(`(()=>{const c=[...document.querySelectorAll('.board>.column')].map(e=>e.getBoundingClientRect());return c.length>=5&&c[1].left>c[0].left&&Math.abs(c[1].top-c[0].top)<2})()`)
			}
			// Use the online agent's first card after testing the stage rail offset.
			tap(`[data-refresh-key="agent:coder"] task-card button`)
			check(`document.querySelector('looster-board').dialog.open && !!document.querySelector('[data-task-session-job="11"]')`)
			overflow()
			tap(`[data-task-session-job="11"]`)
			check(`document.querySelector('#session-dialog').open && document.querySelector('#task-session-run').textContent.includes('Mobile session summary')`)
			overflow()
			tap("#session-back")
			check(`!document.querySelector('#session-dialog').open`)
			tap("#close")
			check(`!document.querySelector('looster-board').dialog.open`)
			tap("#new-task")
			check(`document.querySelector('#create-dialog').open`)
			tap(`#create-form input[name="name"]`)
			check(`document.activeElement===document.querySelector('#create-form input[name="name"]')`)
			overflow()
			tap("#create-close")
			check(`!document.querySelector('#create-dialog').open`)
			tap(`input[aria-label="Search tasks"]`)
			run(chromedp.Evaluate(`const b=document.querySelector('looster-board');b.search.value='no matching mobile task';b.search.dispatchEvent(new Event('input',{bubbles:true}))`, nil))
			check(`document.querySelectorAll('task-card').length===0 && !!document.querySelector('.empty')`)
			overflow()
			run(chromedp.Evaluate(`document.querySelector('looster-board').search.value='';document.querySelector('looster-board').search.dispatchEvent(new Event('input',{bubbles:true}))`, nil))
			check(`document.querySelectorAll('task-card').length>0`)
			tap(`nav a[href="#graph"]`)
			check(`!!document.querySelector('task-agent-graph .tg-task')`)
			eval(`document.querySelector('task-agent-graph').settle()`, nil)
			overflow()
			var scale float64
			eval(`document.querySelector('task-agent-graph').view.scale`, &scale)
			tap(`task-agent-graph [data-action="in"]`)
			check(fmt.Sprintf(`document.querySelector('task-agent-graph').view.scale>%f`, scale))
			// Reset only the camera and settle the real simulation, matching
			// existing graph probes without replacing native pointer capture.
			eval(`(()=>{const g=document.querySelector('task-agent-graph');g.view={x:0,y:0,scale:1};g.updateView();g.settle()})()`, nil)
			tap(`task-agent-graph [data-key="task:1"]`)
			check(`document.querySelector('looster-board').dialog.open`)
			tap("#close")
			// Background drag uses native touch pointer capture, just like a phone.
			var camera float64
			eval(`document.querySelector('task-agent-graph').view.x`, &camera)
			mobileTouchTarget(t, tab, `task-agent-graph svg`, true)
			check(fmt.Sprintf(`document.querySelector('task-agent-graph').view.x!==%f`, camera))
			overflow()
		})
	}
}

// Scroll the target into view, then verify the actual hit-tested point before
// dispatching native touch. A swipe starts near the target's right edge.
func mobileTouchTarget(t *testing.T, ctx context.Context, selector string, swipe bool) {
	t.Helper()
	var point struct {
		X, Y float64
		Hit  bool
	}
	fraction := .5
	if swipe {
		fraction = .85
		if strings.HasSuffix(selector, ".cards") {
			fraction = .65
		}
	}
	expression := fmt.Sprintf(`(()=>{const e=document.querySelector(%q);if(!e)throw Error('Missing touch target');e.scrollIntoView({block:'center',inline:'nearest'});const r=e.getBoundingClientRect();const x=Math.min(innerWidth-12,Math.max(12,r.left+r.width*%f)),y=Math.min(innerHeight-12,Math.max(12,(Math.max(r.top,0)+Math.min(r.bottom,innerHeight))/2));const hit=document.elementFromPoint(x,y);return {x,y,hit:!!hit&&(e===hit||e.contains(hit))}})()`, selector, fraction)
	if err := chromedp.Run(ctx,
		chromedp.Sleep(100*time.Millisecond),
		chromedp.Evaluate(fmt.Sprintf(`document.querySelector(%q).scrollIntoView({block:'center',inline:'nearest'})`, selector), nil),
		chromedp.Sleep(100*time.Millisecond),
		chromedp.Evaluate(expression, &point)); err != nil {
		t.Fatal(err)
	}
	if !point.Hit {
		t.Fatalf("touch target %s occluded at (%g,%g)", selector, point.X, point.Y)
	}
	actions := []chromedp.Action{input.DispatchTouchEvent(input.TouchStart, []*input.TouchPoint{{X: point.X, Y: point.Y}})}
	if swipe {
		for i := 1; i <= 8; i++ {
			actions = append(actions, chromedp.Sleep(20*time.Millisecond), input.DispatchTouchEvent(input.TouchMove, []*input.TouchPoint{{X: point.X - float64(i)*18, Y: point.Y}}))
		}
	}
	actions = append(actions, chromedp.Sleep(50*time.Millisecond), input.DispatchTouchEvent(input.TouchEnd, []*input.TouchPoint{}), chromedp.Sleep(100*time.Millisecond))
	if err := chromedp.Run(ctx, actions...); err != nil {
		t.Fatal(err)
	}
}
