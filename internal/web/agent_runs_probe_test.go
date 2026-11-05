package web

import "testing"

func TestAgentRunViews(t *testing.T) { runJSProbe(t, agentRunProbe) }

const agentRunProbe = `import {appendOutput} from './static/output.mjs';
import {runHistoryHTML,runDetailHTML} from './static/agent-runs.mjs';
import {sessionCardHTML,taskSessionCardHTML} from './static/board.mjs';
test('run history supports empty, taskless, linked and paginated runs safely',()=>{
 assert.match(runHistoryHTML({runs:[]}),/No runs recorded/);
 const html=runHistoryHTML({runs:[{job_id:7,outcome:'running',task_id:0},{job_id:8,outcome:'<script>',task_id:42}],has_more:true,next_before_job_id:7},true);
 assert.match(html,/data-run="7"/);assert.match(html,/No task/);assert.match(html,/TSK-42/);
 assert.match(html,/data-run-previous/);assert.match(html,/data-run-before="7"/);
 assert.doesNotMatch(html,/<script>/);assert.match(html,/&lt;script&gt;/);
});
test('run detail renders outcome summary task session and timestamps without HTML injection',()=>{
 const html=runDetailHTML({job_id:7,status:'done',task_id:42,session_id:'session-7',outcome:'success',summary:'<img src=x onerror=alert(1)>',created_at:'2026-11-04T10:00:00Z',started_at:'2026-11-04T10:01:00Z',finished_at:'2026-11-04T10:02:00Z'});
 for(const label of ['Outcome','success','Summary','Session','session-7','Started','Finished','data-run-task="42"'])assert.ok(html.includes(label));
 assert.doesNotMatch(html,/<img/);assert.match(html,/&lt;img/);
 const taskless=runDetailHTML({job_id:8,task_id:0});assert.match(taskless,/No task/);assert.doesNotMatch(taskless,/data-run-task/);assert.match(taskless,/Not assigned/);assert.match(taskless,/No summary recorded/);
});
test('taskless card has a native keyboard accessible inspector button and stable run metadata',()=>{
 const html=sessionCardHTML({agent:'solo',job_id:99,status:'running',task_id:0});
 assert.match(html,/<button /);assert.match(html,/type="button"/);assert.match(html,/data-session-agent="solo"/);assert.match(html,/data-session-job="99"/);assert.match(html,/aria-label="Inspect solo session"/);assert.doesNotMatch(html,/TSK-0/);
});
test('task session cards escape short summaries, preserve distinct jobs and show summary states',()=>{
 const first=taskSessionCardHTML({agent:'coder',job_id:11,short_summary:'<img onerror="bad">',summary_status:'available'});
 const second=taskSessionCardHTML({agent:'coder',job_id:12,summary_status:'pending'});
 assert.match(first,/data-refresh-key="job:11"/);assert.match(second,/data-refresh-key="job:12"/);
 assert.match(first,/data-task-session-job="11"/);assert.match(second,/Summary pending/);
 assert.match(first,/&lt;img/);assert.doesNotMatch(first,/<img/);
 for(const state of ['missing','error','failed','interrupted']) assert.match(taskSessionCardHTML({job_id:13,agent:'coder',summary_status:state}),new RegExp(state));
 assert.match(taskSessionCardHTML({agent:'legacy',job_id:0}),/ disabled/);
 assert.match(taskSessionCardHTML({agent:'legacy'}),/ disabled/);
});
function inspectorHarness() {
 const classes=new Map();
 const context=vm.createContext({HTMLElement:class {},customElements:{define:(name,type)=>classes.set(name,type)},runHistoryHTML,runDetailHTML,appendOutput});
 vm.runInContext(readFileSync(new URL('./static/app.js','file:./probe'),'utf8').replace(/^import .*;\n/gm,''),context);
 return new (classes.get('looster-board'))();
}
test('run selection ignores older responses and responses after inspector navigation',async()=>{
 const board=inspectorHarness(), target={isConnected:true,querySelector:()=>null}, pending=new Map();
 board.panel={querySelector:()=>target};board.api=path=>new Promise(resolve=>pending.set(path,resolve));
 const inspection=board.agentInspection={name:'solo'};
 const first=board.loadAgentRun(inspection,1),second=board.loadAgentRun(inspection,2);
 pending.get('/api/agents/solo/runs/2')({job_id:2,summary:'new selection'});await second;
 assert.match(target.innerHTML,/new selection/);
 pending.get('/api/agents/solo/runs/1')({job_id:1,summary:'stale selection'});await first;
 assert.doesNotMatch(target.innerHTML,/stale selection/);
 const third=board.loadAgentRun(inspection,3);board.agentInspection={name:'other'};
 pending.get('/api/agents/solo/runs/3')({job_id:3,summary:'wrong agent'});await third;
 assert.doesNotMatch(target.innerHTML,/wrong agent/);
});
test('separate task session inspector ignores responses after back or a different job selection',async()=>{
 const board=inspectorHarness(),target={isConnected:true,querySelector:()=>null},pending=new Map();
 board.api=path=>new Promise(resolve=>pending.set(path,resolve));
 const first=board.sessionInspection={name:'coder',target};
 const a=board.loadAgentRun(first,11);
 const second=board.sessionInspection={name:'coder',target};
 const b=board.loadAgentRun(second,12);
 pending.get('/api/agents/coder/runs/12')({job_id:12,summary:'second job'});await b;
 pending.get('/api/agents/coder/runs/11')({job_id:11,summary:'first job'});await a;
 assert.match(target.innerHTML,/second job/);assert.doesNotMatch(target.innerHTML,/first job/);
 const c=board.loadAgentRun(second,13);board.sessionInspection=null;
 pending.get('/api/agents/coder/runs/13')({job_id:13,summary:'closed dialog'});await c;
 assert.doesNotMatch(target.innerHTML,/closed dialog/);
});
test('lazy session output paginates readable deltas and ignores responses after close',async()=>{
 const board=inspectorHarness(),box={textContent:'',scrollHeight:0,scrollTop:0,clientHeight:0},note={},more={};
 board.sessionDialog={open:true,querySelector:s=>s==='#task-session-output'?box:s==='#session-output-status'?note:more};
 const inspection=board.sessionInspection={name:'coder',jobID:11,output:{text:''},after:0};const paths=[];
 board.api=async path=>{paths.push(path);return paths.length===1?{events:[{body:'{"type":"text_delta","text":"Hello "}'}],has_more:true,next_after:1}:{events:[{body:'{"type":"text_delta","text":"world"}'},{body:'{"type":"done","text":"Hello world"}'}],has_more:false,next_after:3};};
 await board.loadSessionOutput(inspection);assert.equal(box.textContent,'Hello world');assert.match(paths[1],/runs\/11\/output\?after=1/);assert.equal(inspection.after,3);
 let resolve;board.api=()=>new Promise(r=>resolve=r);const pending=board.loadSessionOutput(inspection);board.sessionInspection=null;
 resolve({events:[{body:'stale'}],next_after:4});await pending;assert.equal(box.textContent,'Hello world');
});
test('lazy output distinguishes pending missing and API errors',async()=>{
 const board=inspectorHarness(),box={scrollHeight:0,scrollTop:0,clientHeight:0},note={},more={};
 board.sessionDialog={open:true,querySelector:s=>s==='#task-session-output'?box:s==='#session-output-status'?note:more};
 const inspection=board.sessionInspection={name:'coder',jobID:12,output:{text:''},after:0,status:'running'};
 board.api=async()=>({events:[],has_more:false,next_after:0});await board.loadSessionOutput(inspection);assert.match(note.textContent,/pending/);
 inspection.status='done';await board.loadSessionOutput(inspection);assert.match(note.textContent,/No output recorded/);
 board.api=async()=>{throw Object.assign(new Error('missing'),{status:404});};await board.loadSessionOutput(inspection);assert.match(note.textContent,/Output missing/);
 board.api=async()=>{throw new Error('offline');};await board.loadSessionOutput(inspection);assert.match(note.textContent,/Could not load session output: offline/);assert.equal(more.hidden,false);
});
test('unfinished live run has pending summary metadata but missing archived run stays an error',async()=>{
 const board=inspectorHarness(), target={isConnected:true,innerHTML:'',querySelector:()=>null};
 board.panel={querySelector:()=>target};board.api=async()=>{throw Object.assign(new Error('not found'),{status:404});};
 board.data={sessions:[{agent:'solo',job_id:100,task_id:0,session_id:'live-session',status:'running'}]};
 const inspection=board.agentInspection={name:'solo'};
 await board.loadAgentRun(inspection,100);assert.match(target.innerHTML,/pending/);assert.match(target.innerHTML,/live-session/);
 await board.loadAgentRun(inspection,101);assert.equal(target.innerHTML,'');assert.match(target.textContent,/Run unavailable/);
});
`
