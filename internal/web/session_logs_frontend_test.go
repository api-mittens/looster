package web

import "testing"

func TestSessionLogFrontend(t *testing.T) { runJSProbe(t, sessionLogFrontendProbe) }

const sessionLogFrontendProbe = `import {chronologicalEntries,sessionLogHTML} from './static/session-logs.mjs';
import {sortTasksForColumn,taskCardAge,taskCardHTML,taskSignals} from './static/board.mjs';
import {buildGraph,renderGraph,renderTaskSignals} from './static/task-graph.mjs';
import {runHistoryHTML,runDetailHTML} from './static/agent-runs.mjs';
// The shared Goja host exposes href/searchParams only. Browser tests below use
// the native WHATWG URL implementation for the full DOM acceptance check.
Object.defineProperties(URL.prototype, {
 protocol:{get(){return this.href.match(/^[^:]+:/)?.[0] || '';}},
 hostname:{get(){return this.href.match(/^https?:\/\/([^/:?#]+)/i)?.[1] || '';}}
});
test('link logs are clickable and escaped in timelines and both run views',()=>{
 const body='https://example.com/path?q="<tag>"&other=1';
 const log={id:5,job_id:9,agent:'coder',kind:'link',body,created_at:'2026-11-05T11:00:00Z'};
 const renderers=[log=>sessionLogHTML(log),log=>runHistoryHTML({runs:[{job_id:9,logs:[log]}]}),log=>runDetailHTML({job_id:9,logs:[log]})];
 for(const render of renderers) {
  const html=render(log);
  assert.match(html,/<a href="https:\/\/example.com\/path\?q=&quot;&lt;tag&gt;&quot;&amp;other=1" target="_blank" rel="noopener noreferrer">/);
  assert.match(html,/coder · link · Run 9/);
  assert.doesNotMatch(html,/<tag>|data-log-action/);
  for(const body of ['javascript:alert(1)','data:text/html,<img>','ftp://example.com','//example.com','https:///example.com','https://one.test https://two.test','https://example.com\n','https://example.com/\u00a0','https://example.com\\evil']) {
   const invalid=render({...log,body});assert.doesNotMatch(invalid,/<a href=/);assert.doesNotMatch(invalid,/<img>/);
  }
  assert.doesNotMatch(render({...log,kind:'custom-kind'}),/<a href=/);
 }
});
test('historical logs interleave with comments using independent identities and stable chronological order',()=>{
 const comments=[{id:1,created_at:'2026-11-05T12:00:00Z'},{id:2,created_at:'2026-11-05T10:00:00Z'}];
 const logs=[{id:1,created_at:'2026-11-05T11:00:00Z',kind:'blocker'}];
 assert.deepEqual(chronologicalEntries(comments,logs).map(x=>x.entryType+':'+x.id),['comment:2','log:1','comment:1']);
 assert.equal(comments[0].id,1);
 assert.deepEqual(taskSignals({session_logs:logs}),{blocker:true,question:false});
});
test('logs are attributed escaped read-only observations in taskless run history and details',()=>{
 const log={id:8,job_id:9,agent:'<agent>',kind:'<question>',body:'<img src=x onerror=alert(1)>',created_at:'2026-11-05T11:00:00Z'};
 for(const html of [sessionLogHTML(log),runHistoryHTML({runs:[{job_id:9,task_id:0,logs:[log]}]}),runDetailHTML({job_id:9,task_id:0,logs:[log]})]) {
  assert.match(html,/&lt;agent&gt;/);assert.match(html,/&lt;question&gt;/);assert.match(html,/&lt;img/);assert.match(html,/Run 9/);
  assert.doesNotMatch(html,/<img|<agent>|resolve|data-log-action/);
 }
});
test('priority groups are scoped to agent planning todo columns with recency retained inside groups',()=>{
 const tasks=[{id:1,last_activity_at:'2026-11-05',session_logs:[{kind:'question'}]},{id:2,last_activity_at:'2026-11-03',session_logs:[{kind:'blocker'}]},{id:3,last_activity_at:'2026-11-04',session_logs:[{kind:'blocker'},{kind:'question'}]},{id:4,last_activity_at:'2026-11-06'}];
 for(const col of ['planning','todo','agent','agent:coder']) assert.deepEqual(sortTasksForColumn(tasks,col).map(t=>t.id),[3,2,1,4]);
 for(const col of ['closed','validated','other-sessions']) assert.deepEqual(sortTasksForColumn(tasks,col).map(t=>t.id),[4,1,3,2]);
 assert.deepEqual(tasks.map(t=>t.id),[1,2,3,4]);
});
test('graph signals have separate accessible circular backgrounds at different radii',()=>{
 for(const radius of [18,30,54]) for(const blocker of [false,true]) for(const question of [false,true]) {
  const html=renderTaskSignals({blocker,question},radius),count=Number(blocker)+Number(question);
  assert.equal((html.match(/class="tg-task-signal-bg" r="10"/g)||[]).length,count);
  assert.equal((html.match(/role="img"/g)||[]).length,count);
  for(const [kind,present,emoji] of [['blocker',blocker,'⚠️'],['question',question,'❓']]) {
   assert.equal(html.includes('aria-label="Historical '+kind+' log"'),present);
   assert.equal(html.includes(emoji),present);
  }
  const centers=[...html.matchAll(/translate\(([^,]+),([^\)]+)\)/g)].map(m=>[Number(m[1]),Number(m[2])]);
  if(count) {
   const x=centers.reduce((v,p)=>v+p[0],0)/count,y=centers.reduce((v,p)=>v+p[1],0)/count;
   assert.ok(Math.abs(Math.hypot(x,y)-radius)<.001);assert.ok(x>0&&y<0);
  } else assert.equal(html,'');
  if(count===2) assert.ok(Math.hypot(centers[0][0]-centers[1][0],centers[0][1]-centers[1][1])>21);
 }
 const model=buildGraph({tasks:[{id:1,name:'Task',status:'todo',session_logs:[{kind:'question'},{kind:'blocker'}]}]});
 assert.equal(model.nodes[0].question,true);assert.equal(model.edges.length,0);
 assert.match(renderGraph({...model,width:500,height:500}),/tg-task-signal/);
});
test('aged closed validated cards fade after one day compact after three and retain accessible task identity',()=>{
 const now=Date.parse('2026-11-05T12:00:00Z');
 for(const status of ['closed','validated']) {
  const t={id:4,name:'<Full title>',status,last_activity_at:'2026-11-02T11:59:59Z'};
  assert.deepEqual(taskCardAge(t,now),{faded:true,compact:true});
  const html=taskCardHTML(t,null,now);assert.match(html,/task-faded task-compact/);assert.match(html,/aria-label="TSK-4: &lt;Full title&gt;,/);assert.match(html,/<button/);
  assert.deepEqual(taskCardAge({...t,last_activity_at:'2026-11-04T12:00:00Z'},now),{faded:false,compact:false});
  assert.deepEqual(taskCardAge({...t,last_activity_at:'2026-11-02T12:00:00Z'},now),{faded:true,compact:false});
 }
 assert.deepEqual(taskCardAge({status:'todo',created_at:'2020-01-01'},now),{faded:false,compact:false});
 assert.deepEqual(taskCardAge({status:'closed'},now),{faded:false,compact:false});
 const html=taskCardHTML({id:1,name:'Test',session_logs:[{kind:'blocker'},{kind:'question'}]});assert.match(html,/task-blocked/);assert.match(html,/blocker-badge/);assert.match(html,/question-badge/);
});
`
