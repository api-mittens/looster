//go:build uitest

package web

import "testing"

func TestLinkLogsStayOutOfTaskSignals(t *testing.T) {
	runJSProbe(t, `import {taskSignals,taskCardHTML,sortTasksForColumn,taskCardAge} from './static/board.mjs';
import {buildGraph,renderTaskSignals} from './static/task-graph.mjs';
import {chronologicalEntries,sessionLogHTML} from './static/session-logs.mjs';
test('link logs remain visible only in the timeline, not task signals or sorting',()=>{
 const log={id:7,kind:'link',body:'https://github.com/api-mittens/looster/pull/1',created_at:'2026-11-11T12:00:00Z'};
 const original={id:1,name:'Task',status:'todo',last_activity_at:'2026-11-01T12:00:00Z'};
 const linked={...original,session_logs:[log]};
 assert.deepEqual(taskSignals(linked),{blocker:false,question:false});
 assert.equal(taskCardHTML(linked),taskCardHTML(original));
 assert.equal(renderTaskSignals(taskSignals(linked),30),'');
 assert.deepEqual(buildGraph({tasks:[linked]}),buildGraph({tasks:[original]}));
 for(const col of ['todo','planning','agent:coder','closed','validated']) {
  const other={id:2,last_activity_at:'2026-11-02T12:00:00Z'};
  assert.deepEqual(sortTasksForColumn([linked,other],col).map(t=>t.id),[2,1]);
 }
 const now=Date.parse('2026-11-11T12:00:00Z');
 assert.deepEqual(taskCardAge({...linked,status:'closed'},now),taskCardAge({...original,status:'closed'},now));
 const entries=chronologicalEntries([{id:1,created_at:'2026-11-11T11:00:00Z'},{id:2,created_at:'2026-11-11T13:00:00Z'}],[log]);
 assert.deepEqual(entries.map(e=>e.entryType),['comment','log','comment']);
 assert.match(sessionLogHTML(entries[1]),/https:\/\/github.com\/api-mittens\/looster\/pull\/1/);
});`)
}
