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

func TestProgressiveMetadataBrowser(t *testing.T) {
	if os.Getenv("LOOSTER_BROWSER_TEST") != "1" {
		t.Skip("Set LOOSTER_BROWSER_TEST=1 to run this test.")
	}
	server := httptest.NewServer(Handler(&testRepo{}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	tab, shutdown := newTestBrowser(t, ctx)
	defer shutdown()
	var result string
	script := `(async()=>{
 const b=document.querySelector('looster-board'); clearInterval(b.timer);
 while(b.loading) await new Promise(resolve=>setTimeout(resolve,20));
 const assert=(value,message)=>{if(!value) throw Error(message);};
 const task={id:80,name:'Metadata first',status:'todo',description:'Saved content'};
 let finishPayload,failPayload=false,description='Saved content';
 b.api=path=>{
  if(path==='/api/board') return Promise.resolve({tasks:[task],agents:[],sessions:[]});
  if(path==='/api/tasks/TSK-80') return Promise.resolve({task,sessions:[]});
  if(path.includes('/payloads?')) return failPayload?Promise.reject(Error('offline')):new Promise(resolve=>{finishPayload=()=>resolve({task:{...task,description},comments:[],sessions:[]});});
  if(path.includes('/events?')) return Promise.resolve([]);
  if(path.endsWith('/attachments')) return Promise.resolve([]);
  return new Promise(()=>{});
 };
 b.page='graph';
 await b.refresh();
 assert(b.data.tasks[0].name==='Metadata first','Metadata did not render.');
 assert(!b.loading,'Other endpoints blocked refresh.');
 await b.open(80);
 assert(b.querySelector('#dialog-title').textContent.includes('Metadata first'),'Task name did not render.');
 assert(b.panel.querySelector('.loading-placeholder') || b.panel.querySelector('#task-comments .no-comments'),'No comment state.');
 finishPayload(); await new Promise(resolve=>setTimeout(resolve,0));
 const content=b.panel.querySelector('#task-description');
 assert(content.textContent.includes('Saved content'),'No task content.');
 const draft=b.panel.querySelector('textarea'); draft.value='Keep this draft'; draft.focus();
 await b.loadDetail(80);
 assert(content.textContent.includes('Saved content'),'Refresh removed content.');
 assert(draft.value==='Keep this draft' && document.activeElement===draft,'Refresh removed the draft or focus.');
 finishPayload(); await new Promise(resolve=>setTimeout(resolve,0));
 failPayload=true; await b.loadPayloads(80,b.outputs);
 assert(content.textContent.includes('Saved content'),'An error removed content.');
 assert(b.panel.querySelector('[data-payload-retry]'),'No retry control.');
 failPayload=false; description='New content';
 b.panel.querySelector('[data-payload-retry]').click(); finishPayload(); await new Promise(resolve=>setTimeout(resolve,0));
 assert(content.textContent.includes('New content'),'Retry did not load content.');
 b.dialog.close();
 window.progressiveResult='ok';
 })().catch(error=>window.progressiveResult=error.stack || String(error));`
	if err := chromedp.Run(tab, chromedp.Navigate(server.URL), chromedp.WaitVisible("#refresh"), chromedp.Evaluate(script, nil, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }), chromedp.Evaluate(`window.progressiveResult`, &result)); err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Fatal(result)
	}
}
