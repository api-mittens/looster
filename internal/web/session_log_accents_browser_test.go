package web

import (
	"context"
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

func TestSessionLogAccentsBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1")
	}
	handler := Handler(&testRepo{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/log-accent-probe" {
			handler.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><link rel="stylesheet" href="/style.css"><link rel="stylesheet" href="/log-events.css"></head><body><article id="neutral" class="comment"><div class="muted">Comment metadata</div><pre class="log-body">Comment body</pre></article><span class="badge blocker-badge">Blocker</span><span class="badge question-badge">Question</span><main></main></body></html>`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	for _, theme := range []string{"light", "dark"} {
		t.Run(theme, func(t *testing.T) {
			var failures []string
			if err := chromedp.Run(tab,
				emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: theme}}),
				chromedp.Navigate(server.URL+"/log-accent-probe"),
				chromedp.Evaluate(`(async()=>{
 const {sessionLogsHTML}=await import('/session-logs.mjs');
 const {runHistoryHTML,runDetailHTML}=await import('/agent-runs.mjs');
 const kinds=['blocker','question','note','link','custom-kind','Blocker','question ','__proto__','blocker" onclick="alert(1)','<img src=x onerror=alert(1)>'];
 const body='Plain <img src=x onerror=alert(1)> body';
 const logs=kinds.map((kind,i)=>({id:i+1,job_id:9,agent:'<agent>',kind,body,created_at:'2026-11-05T11:00:00Z'}));
 document.querySelector('main').innerHTML=[sessionLogsHTML(logs),runHistoryHTML({runs:[{job_id:9,logs}]}),runDetailHTML({job_id:9,logs})].map(html=>'<section>'+html+'</section>').join('');
 const failures=[],check=(ok,label)=>{if(!ok)failures.push(label);};
 const neutral=document.querySelector('#neutral'),base=getComputedStyle(neutral),meta=getComputedStyle(neutral.querySelector('.muted'));
 check(matchMedia('(prefers-color-scheme:dark)').matches===`+fmt.Sprintf("%t", theme == "dark")+`, 'theme emulation');
 for(const [view,section] of [...document.querySelectorAll('main>section')].entries()) {
  const entries=[...section.querySelectorAll('.session-log')];
  check(entries.length===logs.length,'entry count '+view);
  entries.forEach((entry,i)=>{
   const kind=kinds[i],style=getComputedStyle(entry),metadata=getComputedStyle(entry.querySelector('.muted')),text=entry.querySelector('.log-body');
   const label=view+':'+kind;
   check(entry.dataset.refreshKey==='log:'+(i+1),'chronology '+label);
   check(text.textContent===body&&!entry.querySelector('img,script'),'escaped body '+label);
   check(entry.querySelector('.muted').textContent.includes('<agent> · '+kind+' · Run 9'),'attribution '+label);
   check(getComputedStyle(text).color===base.color&&style.color===base.color,'neutral body '+label);
   check(style.backgroundColor===base.backgroundColor,'neutral background '+label);
   check(style.borderLeftWidth===base.borderLeftWidth&&style.borderLeftStyle===base.borderLeftStyle,'border geometry '+label);
   if(kind==='blocker'||kind==='question') {
    const badge=getComputedStyle(document.querySelector('.'+kind+'-badge'));
    check(style.borderLeftColor===badge.borderLeftColor&&style.borderLeftColor!==base.borderLeftColor,'accent border '+label);
    check(metadata.color===badge.color&&metadata.color!==meta.color,'accent metadata '+label);
   } else {
    check(entry.className==='comment session-log','neutral classes '+label);
    check(style.borderLeftColor===base.borderLeftColor&&metadata.color===meta.color,'neutral fallback '+label);
   }
  });
 }
 return failures;
})()`, &failures, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }),
			); err != nil {
				t.Fatal(err)
			}
			for _, failure := range failures {
				t.Error(failure)
			}
		})
	}
}
