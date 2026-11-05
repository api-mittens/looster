package web

import "testing"

func TestSessionLogFrontend(t *testing.T) { runJSProbe(t, sessionLogFrontendProbe) }

const sessionLogFrontendProbe = `import {chronologicalEntries,sessionLogHTML} from './static/session-logs.mjs';
import {sortTasksForColumn,taskCardAge,taskCardHTML,taskSignals} from './static/board.mjs';
import {buildGraph,renderGraph,renderTaskSignals} from './static/task-graph.mjs';
import {runHistoryHTML,runDetailHTML} from './static/agent-runs.mjs';
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
test('graph question and warning emojis sit on the task circle top-right edge without sessions',()=>{
 const html=renderTaskSignals({blocker:true,question:true});assert.match(html,/⚠️❓/);assert.match(html,/aria-label="Historical blocker log and Historical question log"/);
 assert.match(html,/x="21\.213/);assert.match(html,/y="-21\.213/);
 assert.equal(renderTaskSignals({blocker:false,question:false}),'');
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
