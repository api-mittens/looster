package web

// Behavioral probes execute only through their Go test entrypoints.
const namespaceProbe = `import {behavioursHTML,ruleLabel} from './static/views.mjs';
import {layout} from './static/workflow.mjs';

function boardHarness(fetch) {
  const classes = new Map();
  const context = vm.createContext({HTMLElement:class {},customElements:{define:(name,type) => classes.set(name,type)},URL,AbortController,DOMException,location:{origin:'http://localhost'},fetch,queueMicrotask});
  vm.runInContext(readFileSync(new URL('./static/app.js','file:./probe'),'utf8').replace(/^import .*;\n/gm,''),context);
  const board = new (classes.get('looster-board'))();
  Object.assign(board,{namespace:'alpha',generation:0,controller:new AbortController()});
  return board;
}

test('manifest view shows full source, prior immutable versions, activation and escaping',() => {
  const source='agents:\n  coder:\n    prompt: |\n      <script>alert(1)</script>\ntags:\n  design: Full tag prompt\n';
  const versions=[{id:2,source:'agents: {}',active:true},{id:1,source,active:false}];
  const html=behavioursHTML({}, {},()=>'',versions,1);
  assert.ok(html.includes('Version 2 · active'));
  assert.ok(html.includes('value="1" selected'));
  assert.ok(html.includes('Activate this version'));
  assert.ok(html.includes('tags:\n  design: Full tag prompt\n'));
  assert.ok(html.includes('&lt;script&gt;alert(1)&lt;/script&gt;'));
  assert.ok(!html.includes('new-tag'));
  assert.ok(!behavioursHTML({}, {},()=>'',versions,2).includes('id="activate-manifest"'));
});

test('manifest declarations do not become workflow nodes or rules',() => {
  const rules=[{trigger:'agent',target:'coder',prompt:'Instructions'},{trigger:'tag',target:'design',prompt:'Tag instructions'},{trigger:'todo',target:'coder'}];
  const graph=layout(rules);
  assert.ok(!graph.nodes.some(n => n.id === 'agent:design'));
  const html=behavioursHTML({behaviours:rules},{},()=> '');
  assert.ok(!html.includes('On agent'));
  assert.ok(!html.includes('On tag'));
  assert.ok(!html.includes('data-section="rules"'));
});

test('workflow search labels retain positive and negative tag conditions',() => {
  assert.equal(ruleLabel({trigger:'todo',target:'coder',tag_pattern:'design/*'}),'On todo → coder WHEN TAGGED design/*');
  assert.equal(ruleLabel({trigger:'completed',source:'coder',target:'reviewer',tag_pattern:'private/*',tag_negated:true}),'After coder completes → reviewer WHEN NOT TAGGED private/*');
  const edges=layout([{trigger:'todo',target:'coder',tag_pattern:'design/*',tag_negated:true}]).edges;
  assert.equal(edges[0].condition,'WHEN NOT TAGGED design/*');
  assert.ok(edges[0].label.includes('WHEN NOT TAGGED design/*'));
});

test('dashboard has no removed status or tag creation action',() => {
  const source=readFileSync(new URL('./static/app.js','file:./probe'),'utf8');
  assert.ok(!source.includes('unvalidated'));
  assert.ok(!source.includes("'/api/tags'"));
  assert.ok(!source.includes("label:'Create tag'"));
});

test('dashboard requests scope reads and writes including event cursors',async () => {
  const calls=[];
  const board=boardHarness(async (url,options) => {calls.push({url,options});return {ok:true,json:async()=>({ok:true})};});
  await board.api('/api/tasks/TSK-1/events?after=42&kind=comment');
  await board.post('/api/manifests/1/activate',{});
  assert.equal(calls[0].url.searchParams.get('namespace'),'alpha');
  assert.equal(calls[0].url.searchParams.get('after'),'42');
  assert.equal(calls[0].url.searchParams.get('kind'),'comment');
  assert.equal(calls[1].options.method,'POST');
  assert.equal(calls[1].url.searchParams.get('namespace'),'alpha');
});

test('late namespace response is discarded even when fetch ignores abort',async () => {
  let resolve;
  const board=boardHarness(() => new Promise(r => {resolve=r;}));
  const pending=board.api('/api/board');
  board.generation++; board.namespace='beta';
  resolve({ok:true,json:async()=>({tasks:[{name:'alpha task'}]})});
  await assert.rejects(pending,{name:'AbortError'});
});

test('namespace switch clears all cached data, dialogs, cursors, search and forms',() => {
  const board=boardHarness(async()=>{});
  const controls=new Map();
  const control=selector => {if(!controls.has(selector)) controls.set(selector,{value:'old',textContent:'old',replaceChildren(){this.cleared=true;},reset(){this.resetCalled=true;}});return controls.get(selector);};
  board.querySelector=control; board.palette={querySelector:control};
  let closed=false;
  board.querySelectorAll=()=>[{open:true,close(){closed=true;}}];
  Object.assign(board,{graphStatuses:['closed'],data:{},status:{},system:{},library:{},manifests:[{}],manifestID:1,detailID:1,cursor:10,commentCursor:20,selected:'todo/1',pageHTML:'old',paletteItems:[{}],panel:control('panel'),board:control('board'),search:control('search'),paletteInput:control('paletteInput')});
  let refreshed=false;board.refresh=()=>{refreshed=true;};
  const signal=board.controller.signal;
  board.switchNamespace('beta');
  assert.ok(signal.aborted); assert.ok(closed); assert.ok(refreshed);
  for(const key of ['data','status','system','library','manifests','manifestID','detailID','selected','pageHTML']) assert.equal(board[key],null,key);
  assert.equal(board.cursor,0); assert.equal(board.commentCursor,0);
  assert.equal(board.outputs.size,0); assert.equal(board.comments.size,0);
  assert.equal(board.search.value,''); assert.equal(board.paletteInput.value,'');
  assert.ok(control('#page').cleared); assert.ok(control('#create-form').resetCalled);
  assert.equal(board.graphStatuses,undefined);
});

test('graph page reuses its element across refreshes and filters task relations',() => {
  const board=boardHarness(async()=>{}), graph={}, summary={};
  let mounts=0, mounted=false;
  const panel={querySelector:()=>mounted ? graph : null,set innerHTML(value){assert.equal(value,'<task-agent-graph></task-agent-graph>');mounts++;mounted=true;}};
  board.querySelector=selector=>selector==='#page' ? panel : summary;
  Object.assign(board,{page:'graph',search:{value:'src/linear'},data:{tasks:[{id:1,name:'First',status:'todo',tags:['src/linear'],comment_count:3,status_changes:10},{id:2,name:'Second',status:'closed'}],agents:[{name:'coder'}],sessions:[{task_id:1,agent:'coder',status:'running'},{task_id:2,agent:'coder',status:'idle'}]},status:{workers:[{agents:['coder']}]}});
  board.renderPage();
  assert.equal(mounts,1);
  assert.deepEqual(Array.from(graph.data.tasks,t=>t.id),[1]);
  assert.deepEqual(Array.from(graph.data.sessions,s=>s.task_id),[1]);
  assert.equal(graph.data.tasks[0].comment_count,3);
  assert.equal(graph.data.tasks[0].status_changes,10);
  assert.equal(graph.data.workers[0].agents[0],'coder');
  assert.equal(summary.textContent,'1 tasks · 1 running');
  board.data.sessions[0].status='idle';board.renderPage();
  assert.equal(mounts,1,'polling must not discard graph camera or heatmap state');
  assert.equal(summary.textContent,'1 tasks · 0 running');
  board.search.value='coder';board.renderPage();
  assert.equal(graph.data.tasks.length,1,'agent search finds unfinished related tasks only');
  board.data.tasks[0].status='validated';board.renderPage();
  assert.equal(graph.data.tasks.length,0,'validation removes task on the next refresh');
  assert.equal(graph.data.sessions.length,0,'validation also removes task links');
  assert.equal(mounts,1,'status transition retains the graph client');
  board.data.tasks[0].status='planning';board.renderPage();
  assert.equal(graph.data.tasks.length,1,'reopened unfinished work returns');
  board.search.value='missing';board.renderPage();
  assert.equal(graph.data.tasks.length,0);assert.equal(graph.data.sessions.length,0);
});

test('graph page receives no previous namespace data after reset',() => {
  const board=boardHarness(async()=>{}), graph={};
  board.querySelector=selector=>selector==='#page' ? {querySelector:()=>graph} : {};
  Object.assign(board,{page:'graph',search:{value:''},data:null,status:null});
  board.renderPage();
  assert.equal(graph.data.tasks.length,0);assert.equal(graph.data.sessions.length,0);assert.equal(graph.data.workers.length,0);
});
test('graph tag selection supports any-match, none, search and unavailable tags without losing options',()=>{
  const board=boardHarness(async()=>{}),graph={},summary={};
  board.querySelector=s=>s==='#page'?{querySelector:()=>graph}:summary;
  Object.assign(board,{page:'graph',search:{value:''},data:{tasks:[{id:1,name:'Alpha',status:'todo',tags:['a']},{id:2,name:'Beta',status:'planning',tags:['b']},{id:3,name:'Both',status:'todo',tags:['a','b']},{id:4,name:'Untagged',status:'todo',tags:[]}],agents:[],sessions:[1,2,3,4].map(task_id=>({task_id,agent:'coder'}))}});
  board.graphTags=['a'];board.renderPage();assert.deepEqual(Array.from(graph.data.tasks,t=>t.id),[1,3]);assert.deepEqual(Array.from(graph.data.tagOptions),['a','b']);
  board.graphTags=['a','b'];board.renderPage();assert.equal(graph.data.tasks.length,3);assert.equal(graph.data.sessions.length,3);
  board.search.value='Alpha';board.renderPage();assert.equal(graph.data.tasks.length,1);assert.equal(graph.data.tagOptions.length,2);
  board.search.value='';board.graphTags=[];board.renderPage();assert.equal(graph.data.tasks.length,4);
  board.graphTags=['removed'];board.renderPage();assert.equal(graph.data.tasks.length,0);assert.ok(graph.data.tagOptions.includes('removed'));
});

test('graph statuses combine with tags/search and persist on refresh including none selected',()=>{
  const board=boardHarness(async()=>{}),graph={},summary={};
  board.querySelector=s=>s==='#page'?{querySelector:()=>graph}:summary;
  const statuses=['planning','todo','validated','closed'];
  Object.assign(board,{page:'graph',search:{value:''},data:{tasks:statuses.map((status,i)=>({id:i+1,name:'Task '+status,status,tags:[i<2?'open':'done']})),sessions:statuses.map((_,i)=>({task_id:i+1,agent:'agent-'+(i+1),status:'running'}))}});
  board.renderPage();assert.deepEqual(Array.from(graph.data.tasks,t=>t.id),[1,2]);
  for(let mask=0;mask<16;mask++) {
    board.graphStatuses=statuses.filter((_,i)=>mask&(1<<i));board.renderPage();
    const ids=board.data.tasks.filter(t=>board.graphStatuses.includes(t.status)).map(t=>t.id);
    assert.deepEqual(Array.from(graph.data.tasks,t=>t.id),ids);assert.equal(graph.data.sessions.length,ids.length);
    assert.deepEqual(Array.from(graph.data.selectedStatuses),board.graphStatuses);
    board.renderPage();assert.equal(graph.data.tasks.length,ids.length);
  }
  board.graphStatuses=['validated','closed'];board.graphTags=['done'];board.search.value='agent-4';board.renderPage();
  assert.deepEqual(Array.from(graph.data.tasks,t=>t.id),[4]);assert.deepEqual(Array.from(graph.data.sessions,s=>s.task_id),[4]);
  assert.ok(graph.data.tagOptions.includes('done'));assert.equal(summary.textContent,'1 tasks · 1 running');
  board.data.tasks[3].status='todo';board.renderPage();assert.equal(graph.data.tasks.length,0);
  board.graphStatuses=[];board.renderPage();assert.equal(graph.data.tasks.length,0);assert.equal(graph.data.sessions.length,0);
});
test('graph route is a separate navigation tab and wires node detail events',() => {
  const source=readFileSync(new URL('./static/app.js','file:./probe'),'utf8');
  assert.ok(source.includes('href="#graph">Graph <kbd>gg</kbd></a>'));
  assert.ok(source.includes("const goPage = {g:'graph',l:'board',s:'servers',b:'system'}"));
  assert.ok(source.includes('href="#board">List <kbd>gl</kbd></a>'));
  assert.ok(!source.includes('switchTab('));
  assert.ok(source.includes("addEventListener('task-open',e => this.open(e.detail.id))"));
  assert.ok(source.includes("addEventListener('agent-open',e => this.openAgent(e.detail.name))"));
});
`
