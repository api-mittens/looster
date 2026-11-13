//go:build uitest

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

func TestSessionLogResponsiveLayoutBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1")
	}
	handler := Handler(&testRepo{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/log-layout-probe" {
			handler.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Production renderers and styles below are the same ones used by the task
		// timeline and run inspector. Constrain their containers like the real dialog.
		fmt.Fprint(w, `<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/style.css"><link rel="stylesheet" href="/log-events.css"><script src="/vendor/markdown-it.v15.0.3.min.js"></script></head><body><main style="max-width:850px;margin:auto"><div id="compact-fixture"></div><section class="detail"><h3>Task comments</h3><div id="task-comments"></div></section><section class="detail" id="history-fixture"></section></main></body></html>`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	for _, width := range []int64{360, 768, 1280} {
		for _, theme := range []string{"light", "dark"} {
			t.Run(fmt.Sprintf("%d/%s", width, theme), func(t *testing.T) {
				var result struct {
					Width        int             `json:"width"`
					Theme        string          `json:"theme"`
					Checks       map[string]bool `json:"checks"`
					Measurements map[string]any  `json:"measurements"`
				}
				var raw string
				if err := chromedp.Run(tab,
					emulation.SetDeviceMetricsOverride(width, 900, 1, false),
					emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: theme}, {Name: "prefers-reduced-motion", Value: "reduce"}}),
					chromedp.Navigate(server.URL+"/log-layout-probe"),
					chromedp.Evaluate(`(async()=>{
      const {taskCardHTML}=await import('/board.mjs');
      const {sessionLogsHTML}=await import('/session-logs.mjs');
      const {runHistoryHTML}=await import('/agent-runs.mjs');
      const agent='a'.repeat(64),kind='x'.repeat(100),body='<img src=x onerror=alert(1)> '+ 'unbroken'.repeat(150);
      const logs=[{id:1,job_id:51,agent,kind,body,created_at:'2026-11-05T12:00:00Z'}];
      const title='Very long compact task title '.repeat(10);
      const card={id:51,name:title,status:'closed',last_activity_at:'2026-10-01T12:00:00Z',session_logs:[{kind:'question'},{kind:'blocker'}]};
      document.querySelector('#compact-fixture').innerHTML='<task-card>'+taskCardHTML(card,null,Date.parse('2026-11-05T12:00:00Z'))+'</task-card>';
      document.querySelector('#task-comments').innerHTML=sessionLogsHTML(logs);
      document.querySelector('#history-fixture').innerHTML=runHistoryHTML({runs:[{job_id:51,outcome:'done',task_id:0,logs}]});
      const checks={noPageOverflow:document.documentElement.scrollWidth<=innerWidth+1};
      const measurements={pageWidth:document.documentElement.scrollWidth};
      for(const [name,selector] of [['timeline','#task-comments'],['history','#history-fixture']]) {
       const root=document.querySelector(selector),meta=root.querySelector('.session-log .muted'),text=root.querySelector('.log-body');
       checks[name+'MetadataWraps']=meta.clientWidth>0&&meta.scrollWidth<=meta.clientWidth+1;
       checks[name+'BodyWraps']=text.clientWidth>0&&text.scrollWidth<=text.clientWidth+1;
       checks[name+'SafeText']=meta.textContent.includes(agent)&&meta.textContent.includes(kind)&&text.textContent===body&&!root.querySelector('img,script');
       measurements[name]={metadataWidth:meta.clientWidth,metadataScrollWidth:meta.scrollWidth,bodyWidth:text.clientWidth,bodyScrollWidth:text.scrollWidth};
      }
      const button=document.querySelector('task-card button'),heading=button.querySelector('h3'),badges=[...button.querySelectorAll('.signal-badge')];
      button.focus();
      checks.compactInline=button.classList.contains('task-compact')&&button.getBoundingClientRect().height<60;
      checks.compactSignalsVisible=badges.length===2&&badges.every(b=>b.getBoundingClientRect().width>0&&getComputedStyle(b).display!=='none');
      checks.compactTitleAccessible=document.activeElement===button&&heading.textContent===title&&button.getAttribute('title')===title&&button.getAttribute('aria-label').includes(title)&&getComputedStyle(heading).textOverflow==='ellipsis';
      checks.tasklessHistory=document.querySelector('#history-fixture').textContent.includes('No task');
      return JSON.stringify({width:innerWidth,theme:matchMedia('(prefers-color-scheme: dark)').matches?'dark':'light',checks,measurements});
     })()`, &raw, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) })); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(raw), &result); err != nil {
					t.Fatal(err)
				}
				if result.Width != int(width) || result.Theme != theme {
					t.Fatalf("emulation mismatch: %s", raw)
				}
				for name, passed := range result.Checks {
					if !passed {
						t.Errorf("%s failed: %s", name, raw)
					}
				}
			})
		}
	}
}
