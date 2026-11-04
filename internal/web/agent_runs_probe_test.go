package web

import "testing"

func TestAgentRunViews(t *testing.T) { runJSProbe(t, agentRunProbe) }

const agentRunProbe = `import {runHistoryHTML,runDetailHTML} from './static/agent-runs.mjs';
import {sessionCardHTML} from './static/board.mjs';
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
function inspectorHarness() {
 const classes=new Map();
 const context=vm.createContext({HTMLElement:class {},customElements:{define:(name,type)=>classes.set(name,type)},runHistoryHTML,runDetailHTML});
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
test('unfinished live run has pending summary metadata but missing archived run stays an error',async()=>{
 const board=inspectorHarness(), target={isConnected:true,innerHTML:'',querySelector:()=>null};
 board.panel={querySelector:()=>target};board.api=async()=>{throw Object.assign(new Error('not found'),{status:404});};
 board.data={sessions:[{agent:'solo',job_id:100,task_id:0,session_id:'live-session',status:'running'}]};
 const inspection=board.agentInspection={name:'solo'};
 await board.loadAgentRun(inspection,100);assert.match(target.innerHTML,/pending/);assert.match(target.innerHTML,/live-session/);
 await board.loadAgentRun(inspection,101);assert.equal(target.innerHTML,'');assert.match(target.textContent,/Run unavailable/);
});
`
