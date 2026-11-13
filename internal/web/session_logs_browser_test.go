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

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

func TestGraphSignalBadgesBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1")
	}
	server := httptest.NewServer(Handler(&testRepo{}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	if err := chromedp.Run(tab, chromedp.Navigate(server.URL), chromedp.WaitReady("body")); err != nil {
		t.Fatal(err)
	}
	for _, theme := range []string{"light", "dark"} {
		for _, interaction := range []string{"rest", "hover", "focus"} {
			var result string
			x, y := float64(0), float64(0)
			if interaction == "hover" {
				x, y = 350, 350
			}
			err := chromedp.Run(tab,
				emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: theme}}),
				chromedp.Evaluate(`window.signalInteraction="`+interaction+`"`, nil),
				input.DispatchMouseEvent(input.MouseMoved, x, y),
				chromedp.Evaluate(`(async()=>{
 const {renderTaskSignals}=await import('/task-graph.mjs');
 const host=document.createElement('task-agent-graph');document.body.append(host);
 host.style.cssText='position:fixed;left:100px;top:100px;width:500px;z-index:9999';
 // Keep the production host CSS, replacing its empty graph after connection.
 host.innerHTML='<svg viewBox="0 0 500 500"></svg>';
 const svg=host.querySelector('svg');
 svg.style.height='500px';
 const svgScale=svg.getScreenCTM().a;
 const check=(ok,message)=>{if(!ok)throw Error(message);};
 try {
 for(const radius of [12,18,30,54]) for(const scale of [.4,1,1.8]) for(const blocker of [false,true]) for(const question of [false,true])
 for(const state of ['', 'tg-working', 'tg-heat-green', 'tg-heat-yellow', 'tg-heat-red', 'tg-working tg-heat-red']) {
  svg.innerHTML='<g class="tg-node tg-task '+state+'" tabindex="0" role="button" transform="translate(250,250)"><g class="tg-node-content" style="--tg-node-scale:'+scale+'"><circle class="tg-body" r="'+radius+'"/>'+renderTaskSignals({blocker,question},radius)+'</g></g>';
  const node=svg.firstElementChild,badges=[...node.querySelectorAll('.tg-task-signal')];
  check(badges.length===Number(blocker)+Number(question),'signal combination');
  if(window.signalInteraction==='focus') {node.focus();check(node.matches(':focus-visible'),'real focus-visible state');}
  if(window.signalInteraction==='hover') {await new Promise(requestAnimationFrame);check(node.matches(':hover'),'real hover state');}
  for(const badge of badges) {
   const circle=badge.querySelector('circle'),s=getComputedStyle(circle),text=badge.querySelector('text'),b=circle.getBoundingClientRect();
   const expected=matchMedia('(prefers-color-scheme: dark)').matches?['rgb(255, 255, 255)','rgb(228, 228, 231)']:['rgb(255, 255, 255)','rgb(24, 24, 27)'];
   check(s.fill===expected[0]&&s.stroke===expected[1]&&s.strokeWidth==='1px'&&s.fillOpacity==='1'&&s.opacity==='1','visible contrast circle '+state+' '+s.fill+' '+s.stroke);
   check(b.width>0&&Math.abs(b.width-b.height)<.01&&circle.r.baseVal.value===12&&Math.abs(b.width-24*scale*svgScale)<.01,'visible circular geometry');
   check(s.pointerEvents==='all'&&getComputedStyle(text).pointerEvents==='none'&&getComputedStyle(text).fontSize==='14px','circle delegates pointer input, text remains decorative');
   const hit=document.elementFromPoint(b.x+b.width/2+10*scale*svgScale/Math.sqrt(2),b.y+b.height/2-10*scale*svgScale/Math.sqrt(2));
   check(hit===circle,'padded outer area targets its own signal, not its neighbor or background');
   let delegated=false;
   node.addEventListener('click',()=>{delegated=true;},{once:true});
   hit.dispatchEvent(new MouseEvent('click',{bubbles:true}));
   check(delegated,'padded signal hit delegates to the task');
   check(badge.getAttribute('role')==='img'&&badge.getAttribute('aria-label')===badge.querySelector('title').textContent&&!badge.hasAttribute('tabindex'),'accessible signal');
  }
  if(badges.length===2) {
   const [a,b]=badges.map(el=>el.querySelector('circle').getBoundingClientRect());
   check(Math.hypot(a.x-b.x,a.y-b.y)>a.width+scale,'paired circles including strokes must not overlap');
  }
 }
 return 'ok';
 }catch(e){return e.message;}finally{host.remove();}
})()`, &result, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }))
			if err != nil {
				t.Fatal(err)
			}
			if result != "ok" {
				t.Fatalf("%s/%s: %s", theme, interaction, result)
			}
		}
	}
}

func TestSessionLogLinksBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1")
	}
	server := httptest.NewServer(Handler(&testRepo{}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	var valid bool
	if err := chromedp.Run(tab, chromedp.Navigate(server.URL), chromedp.WaitReady("body"), chromedp.Evaluate(`(async()=>{
 const {sessionLogHTML}=await import('/session-logs.mjs');
 const {runHistoryHTML,runDetailHTML}=await import('/agent-runs.mjs');
 const body='https://example.com/path?q="<img>"&other=1';
 const log={id:7,job_id:9,agent:'coder',kind:'link',body,created_at:'2026-11-05T11:00:00Z'};
 const renderers=[l=>sessionLogHTML(l),l=>runHistoryHTML({runs:[{job_id:9,logs:[l]}]}),l=>runDetailHTML({job_id:9,logs:[l]})];
 for (const render of renderers) {
  const root=document.createElement('section');document.body.append(root);root.innerHTML=render(log);
  const a=root.querySelector('.log-body a');
  if(!a||a.getAttribute('href')!==body||a.textContent!==body||a.target!=='_blank'||!a.relList.contains('noopener')||!a.relList.contains('noreferrer')||root.querySelector('img')||!root.textContent.includes('coder · link · Run 9'))return false;
  // Dispatch a cancelled click so the test verifies an actionable anchor
  // without opening any external site or popup.
  let clicked=false;a.addEventListener('click',e=>{clicked=true;e.preventDefault();});a.click();if(!clicked)return false;
  for(const body of ['javascript:alert(1)','data:text/html,<img>','ftp://example.com','//example.com','https:///example.com','https://one.test https://two.test','https://example.com\n','https://example.com/\u00a0','https://example.com\\evil','https://:80/']) {
   root.innerHTML=render({...log,body});if(root.querySelector('.log-body a,img,script')||root.querySelector('.log-body').textContent!==body)return false;
  }
  root.innerHTML=render({...log,kind:'future-kind'});if(root.querySelector('.log-body a')||!root.textContent.includes('future-kind'))return false;
  root.remove();
 }
 return true;
})()`, &valid, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("timeline and run history links must be safe, clickable, attributed, and limited to valid HTTP(S) link-kind bodies")
	}
}

func TestSessionLogArrivalBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1")
	}
	var arrived atomic.Bool
	var payloadRequests atomic.Int32
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
		case "/api/board", "/api/board/list":
			data = map[string]any{"tasks": []any{taskSnapshot()}, "agents": []any{}, "sessions": []any{}}
		case "/api/status", "/api/system", "/api/library":
			data = map[string]any{}
		case "/api/tasks/TSK-1":
			data = map[string]any{"task": map[string]any{"id": 1, "name": "Late log arrival", "status": "todo"}, "sessions": []any{}}
		case "/api/tasks/TSK-1/payloads":
			payloadRequests.Add(1)
			data = map[string]any{"task": taskSnapshot(), "sessions": []any{}, "runs": []any{}}
		case "/api/tasks/TSK-1/events":
			if r.URL.Query().Get("after") == "0" {
				data = []any{map[string]any{"id": 1, "body": "Before log", "created_at": "2026-11-05T10:00:00Z"}, map[string]any{"id": 2, "body": "After log", "created_at": "2026-11-05T12:00:00Z"}}
			}
		}
		json.NewEncoder(w).Encode(paginatedBoardFixture(t, r, data))
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
	if err := chromedp.Run(tab, chromedp.Navigate(server.URL+"/#board"), chromedp.WaitVisible("#toggle-stages"), chromedp.Click("#toggle-stages"), chromedp.Click(`task-card[data-id="1"] button`), chromedp.WaitVisible(`#task-comments .session-log`), chromedp.Poll(`document.querySelectorAll('#task-comments > *').length===3`, nil), chromedp.Evaluate(`(()=>{const draft=document.querySelector('#comment-form textarea');draft.value='Unsaved draft remains';draft.focus();draft.setSelectionRange(3,8);window.savedDraft=draft;window.savedComment=document.querySelector('[data-comment-id="1"]');})()`, nil)); err != nil {
		t.Fatal(err)
	}
	check(`document.querySelectorAll('#task-comments > *').length===3&&!document.querySelector('#task-comments').textContent.includes('Arrived')&&document.activeElement===window.savedDraft`, "fixture must open before the new log exists and focus the unsaved draft")
	arrived.Store(true)
	// Wait for the refresh and its separate detail requests.
	if err := chromedp.Run(tab, chromedp.Evaluate(`document.querySelector('looster-board').dataset.arrivalRefreshDone='false';document.querySelector('looster-board').refresh().then(async()=>{const board=document.querySelector('looster-board');while(board.detailBusy||board.payloadBusy||board.commentsBusy)await new Promise(resolve=>setTimeout(resolve,20));board.dataset.arrivalRefreshDone='true';})`, nil), chromedp.Poll(`document.querySelector('looster-board').dataset.arrivalRefreshDone==='true'&&document.querySelectorAll('#task-comments .session-log').length===2`, nil, chromedp.WithPollingInterval(20*time.Millisecond))); err != nil {
		var state string
		_ = chromedp.Run(tab, chromedp.Evaluate(`JSON.stringify({done:document.querySelector('looster-board').dataset.arrivalRefreshDone==='true',notice:document.querySelector('#notice').textContent,detailID:document.querySelector('looster-board').detailID,dialogOpen:document.querySelector('looster-board').dialog.open,timeline:document.querySelector('#task-comments')?.innerHTML})`, &state))
		t.Fatalf("late arrival refresh: %v, payload requests=%d, browser=%s", err, payloadRequests.Load(), state)
	}
	const arrivalState = `(()=>{const box=document.querySelector('#task-comments'),children=[...box.children],draft=document.querySelector('#comment-form textarea');return children.length===4&&children[0].textContent.includes('Before log')&&children[1].textContent.includes('Arrived <img')&&children[1].textContent.includes('future<kind>')&&children[1].textContent.includes('future-agent')&&children[2].textContent.includes('Existing log')&&children[3].textContent.includes('After log')&&box.querySelectorAll('[data-log-id="2"]').length===1&&!box.querySelector('img,future,script,[data-resolve-log]')&&document.querySelector('[data-comment-id="1"]')===window.savedComment&&draft===window.savedDraft&&draft.value==='Unsaved draft remains'&&document.activeElement===draft&&draft.selectionStart===3&&draft.selectionEnd===8&&!document.querySelector('task-card .signal-badge,task-card .task-blocked');})()`
	check(arrivalState, "late unknown-kind log must insert chronologically and safely without signals, duplicates, lost draft, or changed focus/selection")
	before := payloadRequests.Load()
	if err := chromedp.Run(tab, chromedp.Evaluate(`document.querySelector('looster-board').dataset.arrivalRefreshDone='false';document.querySelector('looster-board').refresh().then(async()=>{const board=document.querySelector('looster-board');while(board.detailBusy||board.payloadBusy||board.commentsBusy)await new Promise(resolve=>setTimeout(resolve,20));board.dataset.arrivalRefreshDone='true';})`, nil), chromedp.Poll(`document.querySelector('looster-board').dataset.arrivalRefreshDone==='true'`, nil, chromedp.WithPollingInterval(20*time.Millisecond))); err != nil {
		t.Fatal(err)
	}
	if payloadRequests.Load() <= before {
		t.Fatal("duplicate check must send another task payload request")
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
		case "/api/board", "/api/board/list":
			data = map[string]any{"tasks": []any{task}, "agents": []any{}, "sessions": []any{}}
		case "/api/status", "/api/system", "/api/library":
			data = map[string]any{}
		case "/api/tasks/TSK-1":
			data = map[string]any{"task": map[string]any{"id": 1, "name": "Log timeline", "status": "todo"}, "sessions": []any{}}
		case "/api/tasks/TSK-1/payloads":
			data = map[string]any{"task": task, "sessions": []any{}, "runs": []any{}}
		case "/api/tasks/TSK-1/events":
			if r.URL.Query().Get("after") == "0" {
				data = []any{map[string]any{"id": 1, "body": "Before log", "created_at": "2026-11-05T10:00:00Z"}, map[string]any{"id": 2, "body": "After log", "created_at": "2026-11-05T12:00:00Z"}}
			}
		}
		json.NewEncoder(w).Encode(paginatedBoardFixture(t, r, data))
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	var valid bool
	if err := chromedp.Run(tab, chromedp.Navigate(server.URL+"/#board"), chromedp.WaitVisible("#toggle-stages"), chromedp.Click("#toggle-stages"), chromedp.WaitVisible(`task-card[data-id="1"] button`), chromedp.Click(`task-card[data-id="1"] button`), chromedp.WaitVisible(`#task-comments .session-log`), chromedp.Poll(`document.querySelectorAll('#task-comments > *').length===3`, nil), chromedp.Evaluate(`(()=>{const box=document.querySelector('#task-comments');const children=[...box.children];return children.length===3&&children[0].textContent.includes('Before log')&&children[1].textContent.includes('Historical <img')&&children[1].textContent.includes('coder')&&children[2].textContent.includes('After log')&&!box.querySelector('img')&&!box.querySelector('[data-resolve-log]');})()`, &valid)); err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("logs must interleave chronologically, remain safe and read-only")
	}
	// Repeated polling must preserve unique log/comment identities and their ordering.
	if err := chromedp.Run(tab, chromedp.Evaluate(`(async()=>{const board=document.querySelector('looster-board');await board.loadDetail(1);while(board.detailBusy||board.payloadBusy||board.commentsBusy)await new Promise(resolve=>setTimeout(resolve,20));})()`, nil, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }), chromedp.Evaluate(`document.querySelectorAll('#task-comments .session-log').length===1&&document.querySelectorAll('#task-comments > *').length===3`, &valid)); err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("refresh duplicated timeline entries")
	}
	if err := chromedp.Run(tab, chromedp.Evaluate(`document.querySelector('looster-board').dialog.close();location.hash='#graph'`, nil), chromedp.WaitVisible(`.tg-task-signal`), chromedp.Evaluate(`(()=>{const el=document.querySelector('task-agent-graph');el.settle();const m=el.querySelector('[data-key="task:1"] .tg-task-signal'),x=m.transform.baseVal.getItem(0).matrix.e,y=m.transform.baseVal.getItem(0).matrix.f;return el.graph.edges.length===0&&m.textContent.includes('⚠️')&&x>0&&y<0&&Math.abs(Math.hypot(x,y)-30)<.001;})()`, &valid)); err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("graph log marker must sit on task circle top-right edge even without sessions")
	}
	var point []float64
	if err := chromedp.Run(tab, chromedp.ScrollIntoView(`.tg-task-signal-bg`)); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(tab, chromedp.Evaluate(`(()=>{const b=document.querySelector('.tg-task-signal-bg').getBoundingClientRect();return [b.x+b.width/2,b.y+b.height/2];})()`, &point)); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(tab, chromedp.MouseClickXY(point[0], point[1]), chromedp.WaitVisible(`#task-comments .session-log`)); err != nil {
		t.Fatalf("clicking signal circle must open its task: %v", err)
	}
	if err := chromedp.Run(tab, chromedp.Evaluate(`document.querySelector('looster-board').dialog.close()`, nil), chromedp.Focus(`task-agent-graph [data-key="task:1"]`), chromedp.Evaluate(`document.querySelector('task-agent-graph [data-key="task:1"]').dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}))`, nil), chromedp.WaitVisible(`#task-comments .session-log`)); err != nil {
		t.Fatalf("task must remain keyboard actionable with signals: %v", err)
	}
}
