//go:build uitest

package web

// Behavioral probes execute only through their Go test entrypoints.
const outputProbe = `import {appendOutput} from './static/output.mjs';
import {layout} from './static/workflow.mjs';
import {behavioursHTML,serversHTML,ruleLabel} from './static/views.mjs';
import {sortTasksByLatestActivity,taskCardHTML,activeTasklessSessions,boardAgentNames,sessionCardHTML} from './static/board.mjs';

test('taskless board sessions stay active only and mixed agents keep regular columns',()=>{
 const sessions=[{task_id:0,job_id:10,agent:'solo',status:'running'},{task_id:0,job_id:11,agent:'solo',status:'running'},{task_id:0,job_id:12,agent:'mixed',status:'running'},{task_id:0,job_id:13,agent:'ended',status:'idle'},{task_id:1,job_id:14,agent:'mixed',status:'idle'}];
 assert.deepEqual(activeTasklessSessions(sessions).map(s=>s.job_id),[10,11,12]);
 assert.deepEqual(boardAgentNames([{name:'solo'},{name:'mixed'},{name:'unused'}],sessions),['mixed','unused']);
 const html=sessionCardHTML({...sessions[0],agent:'<solo>'});
 assert.ok(html.includes('&lt;solo&gt;'));assert.ok(!html.includes('TSK-'));assert.ok(!html.includes('data-task'));
});

test('completed taskless jobs do not restore empty agent columns',()=>{
 const agents=[{name:'solo',taskless_only:true},{name:'mixed',taskless_only:false},{name:'unused'}];
 const running=[{task_id:0,job_id:1,agent:'solo',status:'running'}];
 assert.deepEqual(boardAgentNames(agents,running),['mixed','unused']);
 assert.deepEqual(boardAgentNames(agents,[]),['mixed','unused']);
 assert.deepEqual(boardAgentNames(agents,[{task_id:1,agent:'solo',status:'idle'}]),['mixed','solo','unused']);
 assert.equal(activeTasklessSessions([]).length,0);
});

test('board cards preserve activity counts and escaping',() => {
  const tasks=[{id:3,name:'Third',status:'todo',status_changes:2,comment_count:4},{id:2,name:'Second',status:'todo',status_changes:4,comment_count:1},{id:1,name:'<First>',status:'todo',status_changes:2,comment_count:0}];
  assert.deepEqual(tasks.map(t=>t.id),[3,2,1]);
  const html=taskCardHTML(tasks[2]);
  assert.ok(html.indexOf('Status 2') < html.indexOf('&lt;First&gt;'));
  assert.ok(html.includes('Comments 0'));
  assert.ok(html.includes('aria-label="2 status changes including initial status"'));
});

test('latest activity wins over status counts, creation dates and input order',() => {
  const tasks=Object.freeze([
    Object.freeze({id:1,status_changes:100,created_at:'2026-11-04T12:00:00Z',last_activity_at:'2026-11-01T12:00:00Z'}),
    Object.freeze({id:3,status_changes:0,last_activity_at:'2026-11-03T13:00:00+01:00'}),
    Object.freeze({id:2,status_changes:50,last_activity_at:'2026-11-03T12:00:00Z'}),
    Object.freeze({id:4,last_activity_at:'2026-11-03T12:00:00.001Z'})
  ]);
  assert.deepEqual(sortTasksByLatestActivity(tasks).map(t=>t.id),[4,2,3,1]);
  assert.deepEqual(tasks.map(t=>t.id),[1,3,2,4]);
  assert.deepEqual(sortTasksByLatestActivity([...tasks].reverse()).map(t=>t.id),[4,2,3,1]);
  assert.deepEqual(sortTasksByLatestActivity([]),[]);
});

test('missing or invalid activity uses creation, then undated tasks sort last by ID',() => {
  for(const missing of [undefined,null,'','  ','invalid',false,0,{},[]]) {
    const tasks=[
      {id:9,last_activity_at:missing,created_at:'2026-11-03T12:00:00Z'},
      {id:8,last_activity_at:'2026-11-02T12:00:00Z'},
      {id:7,last_activity_at:missing,created_at:missing,status_changes:999},
      {id:6,last_activity_at:missing,created_at:missing},
      {id:5,last_activity_at:'1970-01-01T00:00:00Z'},
      {id:4,last_activity_at:'1969-12-31T23:59:59Z'},
      {id:3,last_activity_at:'2026-11-03T12:00:00Z'}
    ];
    assert.deepEqual(sortTasksByLatestActivity(tasks).map(t=>t.id),[3,9,8,5,4,6,7]);
  }
});

test('text deltas preserve whitespace without repeating final answer',() => {
  const s = {text:''};
  for(const event of [{type:'start'},{type:'text_delta',text:'Hello '},{type:'delta',delta:{text:'world'}},{type:'done',text:'Hello world'}]) appendOutput(s,JSON.stringify(event));
  assert.equal(s.text,'Hello world');
  appendOutput(s,JSON.stringify({type:'start'}));
  appendOutput(s,JSON.stringify({type:'done',text:'Next run'}));
  assert.equal(s.text,'Hello world\n\nNext run');
});
test('tool output and errors are readable, metadata is omitted',() => {
  const s = {text:''};
  appendOutput(s,JSON.stringify({type:'usage',tokens:123}));
  appendOutput(s,JSON.stringify({type:'tool_result',name:'shell',output:'ok'}));
  appendOutput(s,JSON.stringify({type:'error',message:'Failed'}));
  assert.equal(s.text,'\n[tool result: shell]\nok\n\n[Error] Failed\n');
});
test('legacy plain output is retained',() => {const s={text:''}; appendOutput(s,'plain output'); assert.equal(s.text,'plain output');});
test('workflow ranks handoffs after triggers',() => {
  const graph=layout([{trigger:'todo',target:'coder'},{trigger:'completed',source:'coder',target:'reviewer'},{trigger:'prompt',target:'independent'}]);
  const find=id=>graph.nodes.find(n=>n.id===id);
  assert.ok(find('status:todo:coder').x < find('agent:coder').x);
  assert.ok(find('agent:coder').x < find('agent:reviewer').x);
  assert.equal(find('agent:independent'),undefined);
});
test('stop rules aggregate on their agent card',() => {
  const graph=layout([{trigger:'todo',target:'coder'},{trigger:'stop_validated',target:'coder'},{trigger:'stop_closed',target:'coder'},{trigger:'stop_validated',target:'coder'}]);
  assert.deepEqual(graph.nodes.find(n=>n.id==='agent:coder').stopStatuses,['validated','closed']);
  assert.equal(graph.nodes.filter(n=>n.kind==='agent').length,1);
  assert.equal(graph.edges.length,1);
  assert.ok(!graph.nodes.some(n=>n.kind==='stop'));
  assert.equal(ruleLabel({trigger:'stop_validated',target:'coder'}),'Stop pinging coder after validated');
  const html=behavioursHTML({behaviours:[{trigger:'stop_validated',target:'coder'}]}, {agents:[{name:'coder'}],tags:[]},()=> '');
  assert.ok(!html.includes('data-section="rules"'));
  assert.ok(html.includes('Behaviour manifest'));
  assert.ok(html.includes('workflow-graph'));
});
test('workflow graph renders one banner beneath the agent card',async() => {
  const previousElement=globalThis.HTMLElement, previousRegistry=globalThis.customElements;
  let Graph;
  globalThis.HTMLElement=class { querySelectorAll() { return []; } };
  globalThis.customElements={define(name,element) { if(name==='workflow-graph') Graph=element; }};
  try {
    require('./static/workflow.js');
    const graph=new Graph();
    graph.rules=[{trigger:'todo',target:'coder'},{trigger:'stop_validated',target:'coder'},{trigger:'stop_closed',target:'coder'}];
    assert.ok(graph.innerHTML.includes('Until task is validated or closed'));
    assert.equal((graph.innerHTML.match(/class="workflow-stop-banner"/g)||[]).length,1);
    assert.ok(!graph.innerHTML.includes('workflow-stop-edge'));
    graph.rules=[{trigger:'todo',target:'coder',tag_pattern:'design/*',tag_negated:true},{trigger:'stop_validated',target:'coder',tag_pattern:'design/*'}];
    assert.ok(graph.innerHTML.includes('class="graph-kind workflow-condition"'));
    assert.ok(graph.innerHTML.includes('WHEN NOT TAGGED design/*'));
    assert.ok(graph.innerHTML.includes('Until task is validated WHEN TAGGED design/*'));
  } finally {
    globalThis.HTMLElement=previousElement;
    globalThis.customElements=previousRegistry;
  }
});
test('independent workflows sharing a status occupy separate lanes',() => {
  const graph=layout([{trigger:'todo',target:'a'},{trigger:'todo',target:'b'},{trigger:'completed',source:'a',target:'review-a'},{trigger:'completed',source:'b',target:'review-b'}]);
  assert.equal(graph.lanes.length,2);
  assert.ok(graph.lanes[0].y+graph.lanes[0].height < graph.lanes[1].y);
  for(const edge of graph.edges) assert.equal(graph.nodes.find(n=>n.id===edge.source).lane,graph.nodes.find(n=>n.id===edge.target).lane);
});
test('long dependencies use a channel above nodes',() => {
  const graph=layout([{trigger:'todo',target:'a'},{trigger:'completed',source:'a',target:'b'},{trigger:'todo',target:'b'}]);
  const edge=graph.edges.find(e=>e.source==='status:todo:b');
  assert.ok(Number.isFinite(edge.channelY));
  assert.ok(graph.nodes.every(n=>n.y > edge.channelY));
});
test('workflow renders cycles and self loops finitely',() => {
  const graph=layout([{trigger:'completed',source:'a',target:'b'},{trigger:'completed',source:'b',target:'a'},{trigger:'completed',source:'c',target:'c'}]);
  assert.ok(graph.nodes.every(n=>n.cyclic && Number.isFinite(n.x)));
  assert.equal(graph.edges.length,3);
  assert.deepEqual(layout([]).nodes,[]);
});
test('merged behaviour view distinguishes instructions-only agents from workflow participants',() => {
 const html=behavioursHTML({behaviours:[{trigger:'prompt',target:'unused',prompt:'Instructions'},{trigger:'completed',source:'a',target:'b'}]}, {agents:[{name:'a'},{name:'b'},{name:'unused'}],tags:[{name:'design',description:'<script>unsafe</script>'}]},()=> 'memories');
 assert.ok(!html.includes('data-section="rules"'));
 assert.ok(html.includes('aria-label="Full behaviour manifest"'));
 assert.ok(html.includes('data-section="orphaned"'));
 assert.equal((html.match(/data-agent=/g)||[]).length,1);
 assert.ok(html.includes('data-agent="unused"'));
 assert.ok(html.includes('&lt;script&gt;unsafe&lt;/script&gt;'));
 assert.ok(!html.includes('Dashboard process configuration'));
});
test('server view nests coverage and displays only safe worker settings',() => {
 const html=serversHTML({workers:[{id:'server-a',agents:['a'],last_seen:'2026-11-02',settings:{concurrency:4,harness:'jcode',poll_interval:'1s',run_timeout:'1h',password:'do-not-render'}}],agents:[{name:'a'},{name:'b'}],sessions:[]});
 assert.ok(html.includes('server-agents'));
 assert.ok(html.includes('data-agent="a"'));
 assert.ok(html.includes('Orphaned agents'));
 assert.ok(html.includes('data-agent="b"'));
 assert.ok(!html.includes('do-not-render'));
 assert.ok(!html.includes('Agent coverage'));
});
`
