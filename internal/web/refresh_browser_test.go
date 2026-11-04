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

// Exercise the real dashboard DOM with deterministic polling payloads. Replacing
// api rather than refresh keeps the production rendering/lifecycle under test.
func TestRefreshPreservesUIBrowser(t *testing.T) {
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
	script := `(async () => {
 const b=document.querySelector('looster-board'); window.refreshTestStage='initial';
 clearInterval(b.timer);
 while(b.loading) await new Promise(r=>setTimeout(r,20));
 let revision=0, reverse=false;
 const task=id=>({id,name:'Task '+id+' revision '+revision,status:'todo',description:'Description '+revision,tags:['a','b']});
 b.api=async path=>{
  if(path==='/api/board') return {tasks:[task(1),task(2)],agents:[{name:'aaa-offline'},{name:'coder'}],sessions:[{task_id:1,agent:'aaa-offline',status:'running'},{task_id:1,agent:'coder',status:'running'}]};
  if(path==='/api/status') return {workers:[{id:'refresh-worker',agents:['coder']}]};
  if(path==='/api/system') return {behaviours:[]};
  if(path==='/api/library') return {agents:[{name:'coder',description:'Prompt '+revision}],tags:[{name:'a',description:'Tag '+revision,memories:[]}]};
  if(path==='/api/manifests') return [];
  if(/^\/api\/tasks\/TSK-\d+$/.test(path)) return {task:task(Number(path.match(/\d+$/)[0])),sessions:[],context:{tags:(reverse?['b','a']:['a','b']).map((name,i)=>({id:name==='a'?1:2,name,description:'Tag '+name+' revision '+revision,memories:[{id:i+1,body:'Memory '+revision}]}))}};
  return [];
 };
 const assert=(ok,message)=>{if(!ok) throw Error(message);};
 await b.refresh(); window.refreshTestStage='board';
 assert(b.stagesCollapsed && b.querySelector('#toggle-stages').getAttribute('aria-expanded')==='false','stages not collapsed by default');
 assert(!b.board.querySelector('[data-refresh-key="todo"]'),'collapsed stage rendered');
 const onlineColumn=b.board.querySelector('[data-refresh-key="agent:coder"]');
 const onlineCard=onlineColumn.querySelector('task-card'), onlineButton=onlineCard.querySelector('button');
 const offline=()=>b.board.querySelector('[data-refresh-key="agent:aaa-offline"]');
 const checkOnline=()=>{
  assert(b.board.querySelector('[data-refresh-key="agent:coder"]')===onlineColumn,'online column replaced');
  assert(onlineColumn.querySelector('task-card')===onlineCard && onlineCard.querySelector('button')===onlineButton,'online card/button replaced');
  assert(document.activeElement===onlineButton,'online focus lost');
 };
 onlineButton.focus();
 assert(!offline(),'offline agent visible by default');
 for(let i=0;i<2;i++){
  b.querySelector('#toggle-offline-agents').click();
  assert(!!offline(),'offline toggle did not show agent'); checkOnline();
  await b.refresh(); checkOnline();
  assert(!!offline(),'refresh hid expanded offline agent');
  b.querySelector('#toggle-offline-agents').click();
  assert(!offline(),'offline toggle did not hide agent'); checkOnline();
  await b.refresh(); checkOnline();
  assert(!offline(),'refresh restored filtered offline agent');
 }
 // Stages are now opt-in. Show them explicitly for the stage-card checks.
 b.querySelector('#toggle-stages').click();
 assert(!b.stagesCollapsed && !!b.board.querySelector('[data-refresh-key="todo"]'),'stages did not expand');
 checkOnline();
 const card=b.board.querySelector('task-card'), button=card.querySelector('button');
 button.focus();
 for(let i=0;i<3;i++) await b.refresh();
 assert(card===b.board.querySelector('task-card'),'unchanged board card replaced');
 assert(document.activeElement===button,'board focus lost');
 revision++; await b.refresh();
 assert(card===b.board.querySelector('task-card'),'changed board card replaced');
 assert(document.activeElement===button,'changed board focus lost');
 assert(card.textContent.includes('revision 1'),'board data stale');
 await b.open(1); window.refreshTestStage='detail';
 const tag=Array.from(b.panel.querySelectorAll('#tag-context details')).find(x=>x.textContent.includes('Tag: a'));
 tag.open=true;
 const draft=b.panel.querySelector('#comment-form textarea');
 draft.value='unsaved comment draft'; draft.focus(); draft.setSelectionRange(2,9);
 // Force genuine overflowing dialog content so scroll assertions cannot pass at zero.
 b.panel.style.height='240px'; b.panel.style.overflow='auto';
 b.panel.scrollTop=80;
 const scroll=b.panel.scrollTop;
 assert(scroll>0,'fixture did not scroll');
 const check=()=>{
  assert(tag.isConnected && tag.open,'tag expansion/identity lost');
  assert(document.activeElement===draft,'draft focus lost');
  assert(draft.value==='unsaved comment draft' && draft.selectionStart===2 && draft.selectionEnd===9,'draft or selection reset');
  assert(b.panel.scrollTop===scroll,'dialog scroll changed');
 };
 for(let i=0;i<3;i++){await b.refresh();check();}
 revision++; reverse=true; await b.refresh(); check();
 assert(tag.textContent.includes('revision 2'),'tag data stale');
 assert(b.panel.querySelector('#task-description').textContent.includes('Description 2'),'description stale');
 await b.open(2);
 assert(!b.panel.querySelector('#tag-context details').open,'expansion leaked to another task');
 assert(b.panel.querySelector('#comment-form textarea').value==='','draft leaked to another task');
 b.switchNamespace('beta'); window.refreshTestStage='namespace';
 while(b.loading) await new Promise(r=>setTimeout(r,20));
 await b.open(1); window.refreshTestStage='detail';
 assert(!b.panel.querySelector('#tag-context details').open,'expansion leaked to namespace');
 assert(b.panel.querySelector('#comment-form textarea').value==='','draft leaked to namespace');
 b.dialog.close(); await new Promise(r=>setTimeout(r,20));
 b.page='system'; await b.refresh();
 const orphan=b.querySelector('[data-section="orphaned"]'); orphan.open=true;
 const agent=orphan.querySelector('[data-agent]'); agent.focus();
 revision++; await b.refresh();
 assert(orphan.isConnected && orphan.open,'page accordion reset');
 assert(document.activeElement===agent,'page focus reset');
 assert(orphan.textContent.includes('Prompt '+revision),'page data stale');
 b.openPalette();
 b.paletteSelection=b.paletteItems.findIndex(item=>item.key==='task:2'); b.paletteQuery=null; b.renderPalette();
 const option=b.palette.querySelector('[aria-selected="true"]'); option.focus();
 revision++; await b.refresh();
 assert(b.paletteItems[b.paletteSelection].key==='task:2','palette selection reset');
 assert(option.isConnected && document.activeElement===option,'palette focus/identity reset');
 b.palette.close(); await new Promise(r=>setTimeout(r,20));
 b.page='graph'; await b.refresh();
 const graph=b.querySelector('task-agent-graph'), tagMenu=graph.querySelector('.tg-tag-filter');
 tagMenu.open=true;
 const filter=graph.querySelector('[data-tag-filter]'); filter.focus();
 revision++; await b.refresh();
 assert(tagMenu.isConnected && tagMenu.open,'graph tag menu reset');
 assert(filter.isConnected && document.activeElement===filter,'graph filter focus reset');
 window.refreshTestResult='ok';
})().catch(e=>window.refreshTestResult=e.stack || String(e));`
	if err := chromedp.Run(tab,
		chromedp.Navigate(server.URL+"/#board"),
		chromedp.WaitVisible("#toggle-stages"),
		chromedp.Evaluate(script, nil, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }),
		chromedp.Evaluate(`window.refreshTestResult`, &result),
	); err != nil {
		var stage string
		_ = chromedp.Run(tab, chromedp.Evaluate(`JSON.stringify({stage:window.refreshTestStage,result:window.refreshTestResult,loading:document.querySelector('looster-board').loading})`, &stage))
		t.Fatalf("%v: %s", err, stage)
	}
	if result != "ok" {
		t.Fatal(result)
	}
}
