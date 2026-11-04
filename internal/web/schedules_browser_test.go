package web

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

func TestManualScheduleBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("set LOOSTER_BROWSER_TEST=1 for Chromium acceptance")
	}
	server := httptest.NewServer(Handler(&testRepo{}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	var result string
	script := `(async()=>{
 const b=document.querySelector('looster-board');clearInterval(b.timer);
 while(b.loading) await new Promise(r=>setTimeout(r,20));
 const assert=(ok,message)=>{if(!ok)throw Error(message);};
 b.system={behaviours:[{id:42,trigger:'every',target:'coder',interval_seconds:60},{id:43,trigger:'every',target:'coder',interval_seconds:120}]};
 b.library={agents:[],tags:[]};b.manifests=[];b.page='system';b.renderPage();
 const button=id=>b.querySelector('[data-run-schedule="'+id+'"]');
 const status=id=>b.querySelector('[data-schedule-status="'+id+'"]');
 assert(button(42)&&button(43),'missing independent controls');
 let release,calls=0;
 b.post=(path,body)=>{calls++;assert(path==='/api/behaviours/42/run','wrong rule');assert(JSON.stringify(body)==='{}','wrong body');return new Promise(r=>release=r);};
 button(42).click();assert(button(42).disabled,'pending button enabled');assert(!button(43).disabled,'other rule disabled');
 button(42).click();assert(calls===1,'duplicate request');
 release({rule_id:42,status:'queued',agent_name:'coder'});await new Promise(r=>setTimeout(r,0));
 assert(status(42).textContent.includes('Queued for coder'),'queued status missing');
 b.post=async()=>({rule_id:43,status:'already_active',agent_name:'coder'});
 button(43).click();await new Promise(r=>setTimeout(r,0));
 assert(status(43).textContent.includes('Already active'),'already-active status missing');
 assert(status(42).textContent.includes('Queued for coder'),'other rule result overwritten');
 b.post=async()=>{throw Error(JSON.stringify({error:'Permission denied'}));};
 button(43).click();await new Promise(r=>setTimeout(r,0));
 assert(status(43).textContent==='Blocked: Permission denied','blocked status missing');
 assert(!button(43).disabled,'blocked button cannot retry');
 b.renderPage();assert(status(43).textContent==='Blocked: Permission denied','refresh lost result');
 window.scheduleResult='ok';
 })().catch(e=>window.scheduleResult=e.stack||String(e));`
	if err := chromedp.Run(tab, chromedp.Navigate(server.URL+"/#system"), chromedp.WaitVisible("#page"), chromedp.Evaluate(script, nil, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }), chromedp.Evaluate(`window.scheduleResult`, &result)); err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Fatal(result)
	}
}
