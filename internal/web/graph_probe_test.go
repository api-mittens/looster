package web

// Behavioral probes execute only through their Go test entrypoints.
const graphProbe = `import {activity,activityColor,buildGraph,layoutGraph,renderGraph,HEATMAP_LEGEND,edgeWeight,prepareGraph,createGraphSimulation,collisionRadius,taskRecency,progressSegments,renderProgressRing,compactCount} from './static/task-graph.mjs';

const fixture={tasks:[{id:1,name:'Fix <script>',status:'planning',comment_count:3,status_changes:10},{id:2,name:'Other',status:'todo'}],agents:[{name:'dev'},'idle'],workers:[{agents:['dev']}],sessions:[{task_id:1,agent:'dev',status:'done'},{task_id:1,agent:'dev',status:'running'},{task_id:2,agent:'dev',status:'failed'},{task_id:999,agent:'orphan',status:'running'}],heatmap:true};
test('model aggregates sessions without losing active state and uses worker online status',()=>{
 const graph=buildGraph(fixture); assert.equal(graph.edges.length,2);assert.equal(graph.edges[0].active,true);assert.equal(graph.edges[0].count,2);assert.equal(graph.edges[1].active,false);
 assert.equal(graph.nodes.find(n=>n.key==='agent:dev').online,true);assert.equal(graph.nodes.find(n=>n.key==='agent:idle'),undefined);assert.equal(graph.nodes[0].total,13);
 assert.equal(buildGraph({tasks:[{id:1,status:'todo'},{id:'1',status:'todo'}]}).nodes.length,1);
});
test('heatmap is absolute, sanitized, and thresholds are explained',()=>{
 for(const [total,level] of [[0,'green'],[9,'green'],[10,'yellow'],[24,'yellow'],[25,'red'],[1000,'red']]) assert.equal(activity({comment_count:total}).level,level);
 assert.deepEqual(activity({comment_count:-2,status_changes:Infinity}),{comments:0,changes:0,total:0,level:'green'});assert.match(HEATMAP_LEGEND,/25\+/);
});
test('only agents related to visible tasks remain, regardless of online state',()=>{
 const data={tasks:[{id:1,status:'todo'},{id:2,status:'closed'}],agents:['live','offline','unrelated','completed'],workers:[{agents:['live','unrelated','completed']}],sessions:[{task_id:1,agent:'live',status:'running'},{task_id:1,agent:'offline',status:'idle'},{task_id:2,agent:'completed',status:'running'}]};
 const graph=buildGraph(data);
 assert.deepEqual(graph.nodes.filter(n=>n.kind==='agent').map(n=>n.name),['live','offline']);
 assert.equal(graph.nodes.find(n=>n.kind==='task').working,true);
 assert.match(renderGraph(layoutGraph(graph)),/tg-working/);
 const idle=buildGraph({...data,sessions:data.sessions.map(s=>({...s,status:'idle'}))});
 assert.equal(idle.nodes.find(n=>n.kind==='task').working,false);
 assert.doesNotMatch(renderGraph(layoutGraph(idle)),/tg-working/);
 assert.deepEqual(buildGraph({...data,tasks:[]}).nodes,[]);
});
test('layout is deterministic and preserves cached positions through polling',()=>{
 const graph=buildGraph(fixture), first=layoutGraph(graph);assert.deepEqual(first,layoutGraph(graph));
 const cached=new Map(first.nodes.map(n=>[n.key,{x:n.x,y:n.y}]));cached.set('task:1',{x:17,y:29});
 const next=layoutGraph(buildGraph({...fixture,tasks:[...fixture.tasks].reverse()}),cached);assert.equal(next.nodes.find(n=>n.key==='task:1').x,17);assert.equal(next.nodes.find(n=>n.key==='task:1').y,29);assert.equal(first.nodes[0].x,layoutGraph(graph).nodes[0].x);
});
test('SVG escapes user content, labels counts, state, and activity',()=>{
 const svg=renderGraph(layoutGraph(buildGraph(fixture)));assert.match(svg,/Fix &lt;script&gt;/);assert.doesNotMatch(svg,/<script>/);assert.match(svg,/3\/10/);assert.match(svg,/3 comments, 10 status changes/);assert.match(svg,/historical idle/);assert.match(svg,/tg-active/);assert.match(svg,/role="button" tabindex="0"/);assert.match(svg,/dev, online/);
});

// Small DOM surface, not a replacement implementation: the real custom element
// source is loaded by the Go-owned embedded runtime's local module loader.
class Node extends EventTarget {
 constructor(key){super();this.dataset=key?{key}:{};this.attrs={};}
 setAttribute(k,v){this.attrs[k]=String(v);}
 closest(selector){if(selector==='[data-key]'&&this.dataset.key)return this;if(selector==='svg')return this.svg??null;return null;}
 focus(){this.owner.focused=this;}
}
class Host extends EventTarget {
 constructor(){super();this.isConnected=false;this.listeners=new Set();}
 addEventListener(type,fn,opts){super.addEventListener(type,fn,opts);this.listeners.add(type);}
 removeEventListener(type,fn){super.removeEventListener(type,fn);this.listeners.delete(type);}
 set innerHTML(html){this.html=html;this.nodes=[...html.matchAll(/data-key="([^"]+)"/g)].map(m=>{const n=new Node(m[1]);n.owner=this;return n;});this.lines=[...html.matchAll(/<line /g)].map(()=>new Node());this.viewport=new Node();this.panel=new Node();this.svg=new Node();this.svg.getBoundingClientRect=()=>({left:0,top:0,width:1200,height:1000});for(const n of this.nodes)n.svg=this.svg;}
 get innerHTML(){return this.html;}
 querySelector(s){if(s==='svg')return this.svg;if(s==='[data-graph-detail]')return this.panel;if(s==='.tg-viewport')return this.viewport;if(s==='[data-key]:focus')return this.focused;return null;}
 querySelectorAll(s){return s==='[data-key]'?this.nodes:s==='.tg-edge'?this.lines:[];}
 setPointerCapture(id){this.capture=id;}hasPointerCapture(id){return this.capture===id;}releasePointerCapture(){this.capture=null;}
}
globalThis.HTMLElement=Host;
globalThis.matchMedia=()=>({matches:true,addEventListener(){},removeEventListener(){}});
globalThis.CustomEvent=class extends Event {constructor(type,opts){super(type,opts);this.detail=opts.detail;}};
const registry=new Map();globalThis.customElements={get:k=>registry.get(k),define:(k,v)=>registry.set(k,v)};
const {TaskAgentGraph}=require('./static/task-graph.js');
function connected(){const el=new TaskAgentGraph();el.data=fixture;el.isConnected=true;el.connectedCallback();return el;}
function event(el,type,target,extra={}){const e=new Event(type,{cancelable:true});Object.assign(e,extra);Object.defineProperty(e,'target',{value:target});el.handleEvent(e);return e;}
test('actual custom element registers, renders data, emits click and keyboard events',()=>{
 assert.equal(registry.get('task-agent-graph'),TaskAgentGraph);const el=connected();assert.match(el.innerHTML,/Circle counts/);assert.match(el.innerHTML,/green at 0/);
 const opened=[];el.addEventListener('task-open',e=>opened.push([e.detail,e.bubbles]));el.addEventListener('agent-open',e=>opened.push([e.detail,e.bubbles]));
 event(el,'click',el.nodes[0]);const key=event(el,'keydown',el.nodes.find(n=>n.dataset.key==='agent:dev'),{key:' '});assert.equal(key.defaultPrevented,true);assert.deepEqual(opened,[[{id:1},true],[{name:'dev'},true]]);
 const before={...el.positions.get('task:1')};el.data={...fixture,heatmap:false};assert.deepEqual(el.positions.get('task:1'),before);assert.doesNotMatch(el.innerHTML,/tg-heat-yellow/);
 el.isConnected=false;el.disconnectedCallback();assert.deepEqual([...el.listeners].sort(),['agent-open','task-open']);el.isConnected=true;el.connectedCallback();assert.equal(el.listeners.size,13);
});
test('actual element zoom clamps, drag persists through deferred polling, pans and cleans capture',()=>{
 const el=connected();el.zoom(100);assert.equal(el.view.scale,4);el.zoom(.0001);assert.equal(el.view.scale,.25);el.view={x:0,y:0,scale:1};
 const n=el.nodes[0],old={...el.positions.get(n.dataset.key)};
 event(el,'pointerdown',n,{button:0,pointerId:4,clientX:100,clientY:100});event(el,'pointermove',n,{pointerId:4,clientX:150,clientY:125});
 assert.deepEqual(el.positions.get(n.dataset.key),{x:old.x+50,y:old.y+25});el.data={...fixture,heatmap:false};assert.equal(el.pendingRender,true);
 event(el,'pointerup',n,{pointerId:4});assert.equal(el.capture,null);assert.equal(el.pendingRender,false);assert.equal(el.positions.get(n.dataset.key).x,old.x+50);
 const background={closest:s=>s==='svg'?el.svg:null};event(el,'pointerdown',background,{button:0,pointerId:9,clientX:0,clientY:0});event(el,'pointermove',background,{pointerId:9,clientX:20,clientY:30});assert.equal(el.view.x,20);assert.equal(el.view.y,30);
 el.isConnected=false;el.disconnectedCallback();assert.equal(el.drag,null);assert.equal(el.capture,null);assert.equal(el.listeners.size,0);
});
test('captured pointer click opens once and wheel zoom prevents page scroll',()=>{
 const el=connected(), n=el.nodes[0], opened=[];el.addEventListener('task-open',e=>opened.push(e.detail));
 event(el,'pointerdown',n,{button:0,pointerId:3,clientX:100,clientY:100});event(el,'pointerup',el,{pointerId:3});event(el,'click',el);
 assert.deepEqual(opened,[{id:1}]);
 const wheel=event(el,'wheel',n,{deltaY:-100,clientX:200,clientY:200});assert.equal(wheel.defaultPrevented,true);assert.ok(el.view.scale>1);
 const enter=event(el,'keydown',n,{key:'Enter'});assert.equal(enter.defaultPrevented,true);assert.equal(opened.length,2);
});
test('empty data and reduced motion have explicit support',async()=>{
 const el=connected();el.data={};assert.match(el.innerHTML,/No tasks match the current filters/);const css=await readFile(new URL('./static/task-graph.css','file:./probe'),'utf8');assert.match(css,/prefers-reduced-motion:reduce/);assert.match(css,/animation:none/);assert.match(css,/prefers-color-scheme:dark/);
});
test('each status and every combination filters tasks and their relations',()=>{
 const statuses=['planning','todo','validated','closed'];
 const tasks=[...statuses,'unknown'].map((status,i)=>({id:i+1,status}));
 const sessions=tasks.map(t=>({task_id:t.id,agent:'agent-'+t.id,status:'idle'}));
 for(let mask=0;mask<16;mask++) {
  const selectedStatuses=statuses.filter((_,i)=>mask&(1<<i));
  const graph=buildGraph({tasks,sessions,selectedStatuses});
  const ids=tasks.filter(t=>selectedStatuses.includes(t.status)).map(t=>t.id);
  assert.deepEqual(graph.nodes.filter(n=>n.kind==='task').map(n=>n.id),ids);
  assert.equal(graph.edges.length,ids.length);assert.equal(graph.nodes.filter(n=>n.kind==='agent').length,ids.length);
 }
 assert.equal(buildGraph({tasks,sessions,selectedStatuses:['unknown']}).nodes.length,0);
});
test('status checkboxes default to planning/todo and emit explicit empty selection',()=>{
 const el=connected();el.data={tasks:['planning','todo','validated','closed'].map((status,i)=>({id:i+1,status}))};
 for(const status of ['planning','todo']) assert.ok(el.innerHTML.includes('data-status-filter value="'+status+'" checked'));
 for(const status of ['validated','closed']) assert.ok(el.innerHTML.includes('data-status-filter value="'+status+'">'));
 const changes=[];el.addEventListener('status-filter',e=>changes.push({statuses:e.detail.statuses,bubbles:e.bubbles}));
 const toggle=(value,checked)=>event(el,'change',{value,checked,hasAttribute:k=>k==='data-status-filter'});
 toggle('validated',true);toggle('closed',true);assert.equal(el.graph.nodes.length,4);
 for(const status of ['planning','todo','validated','closed']) toggle(status,false);
 assert.equal(el.graph.nodes.length,0);assert.deepEqual(changes.at(-1),{statuses:[],bubbles:true});
 assert.match(el.innerHTML,/No tasks match the current filters/);
 toggle('closed',true);assert.deepEqual(el.graph.nodes.map(n=>n.id),[4]);
 el.data={...el.data,tasks:el.data.tasks.map(t=>({...t,comment_count:2}))};
 assert.deepEqual(el.graph.nodes.map(n=>n.id),[4]);
 el.disconnectedCallback();
});
test('continuous heatmap colors have stable anchors and intermediate colors',()=>{
 assert.equal(activityColor(0),'rgb(34,197,94)');assert.equal(activityColor(10),'rgb(234,179,8)');assert.equal(activityColor(25),'rgb(239,68,68)');assert.equal(activityColor(10000),activityColor(25));
 assert.equal(activityColor(5),'rgb(134,188,51)');assert.notEqual(activityColor(11),activityColor(24));
 const svg=renderGraph(layoutGraph(buildGraph(fixture)));assert.match(svg,/class="tg-ref"[^>]*>TSK-1<\/text>/);assert.match(svg,new RegExp(` + "`" + `stroke:${activityColor(13).replace(/[()]/g,'\\$&')}` + "`" + `));
});
test('local heatmap toggle persists across polling without heatmap and retains edge legend',()=>{
 const el=connected(),{heatmap,...poll}=fixture;el.data={...poll,heatmap:false};
 const toggle={closest:s=>s==='[data-action]'?{dataset:{action:'heatmap'}}:null};
 assert.match(el.innerHTML,/data-action="heatmap" aria-pressed="false"/);
 event(el,'click',toggle);assert.equal(el.heatmap,true);assert.match(el.innerHTML,/data-action="heatmap" aria-pressed="true"/);assert.match(el.innerHTML,/tg-gradient/);assert.match(el.innerHTML,/Faint links: historical idle sessions/);
 const positions=[...el.positions];el.data=poll;assert.equal(el.heatmap,true);assert.equal(el.graph.heatmap,true);assert.deepEqual([...el.positions],positions);
 el.isConnected=false;el.disconnectedCallback();el.isConnected=true;el.connectedCallback();assert.equal(el.heatmap,true);
 event(el,'click',toggle);el.data=poll;assert.equal(el.heatmap,false);assert.doesNotMatch(el.innerHTML,/tg-gradient/);assert.match(el.innerHTML,/Faint links: historical idle sessions/);
});
const distance=(a,b)=>Math.hypot(a.x-b.x,a.y-b.y);
const coordinates=graph=>new Map(graph.nodes.map(n=>[n.key,{x:n.x,y:n.y}]));
const pairGraph=active=>buildGraph({tasks:[{id:1,status:'todo'}],agents:['dev'],sessions:[{task_id:1,agent:'dev',status:active?'running':'done'}]});
test('weighted forces make active and repeated relationships quantitatively shorter',()=>{
 const idle=layoutGraph(pairGraph(false)),active=layoutGraph(pairGraph(true));
 const idleLength=distance(...idle.nodes),activeLength=distance(...active.nodes);
 assert.ok(activeLength<idleLength*.85,` + "`" + `active ${activeLength} vs idle ${idleLength}` + "`" + `);
 const repeated=pairGraph(false);repeated.edges[0].count=8;const repeatedLength=distance(...layoutGraph(repeated).nodes);
 assert.ok(repeatedLength<idleLength*.9,` + "`" + `repeated ${repeatedLength} vs idle ${idleLength}` + "`" + `);
 assert.equal(edgeWeight({active:true,count:8}),edgeWeight({active:true,count:100000}));
});
test('connected components cluster closer internally than across unrelated groups',()=>{
 const graph=layoutGraph(buildGraph({tasks:[{id:1,status:'planning'},{id:2,status:'todo'},{id:3,status:'planning'},{id:4,status:'todo'}],agents:['a','b'],sessions:[1,2,3,4].map(id=>({task_id:id,agent:id<3?'a':'b',status:'done'}))}));
 const byKey=new Map(graph.nodes.map(n=>[n.key,n]));let same=0,cross=0,ns=0,nc=0;
 for(const a of graph.nodes) for(const b of graph.nodes) if(a.key<b.key){const group=n=>['task:1','task:2','agent:a'].includes(n.key);if(group(a)===group(b)){same+=distance(a,b);ns++;}else{cross+=distance(a,b);nc++;}}
 assert.ok(same/ns<cross/nc*.6,` + "`" + `within ${same/ns}, across ${cross/nc}` + "`" + `);
 assert.ok(distance(byKey.get('task:1'),byKey.get('agent:a'))>85,'collision spacing');
});
test('force simulation is input-order deterministic, finite and canvas bounded',()=>{
 const data={tasks:Array.from({length:36},(_,i)=>({id:i,status:'todo'})),agents:['a','b','c'],sessions:Array.from({length:36},(_,i)=>({task_id:i,agent:['a','b','c'][i%3],status:i%2?'running':'done'}))};
 const first=layoutGraph(buildGraph(data)),second=layoutGraph(buildGraph({...data,tasks:[...data.tasks].reverse(),agents:[...data.agents].reverse(),sessions:[...data.sessions].reverse()}));
 assert.deepEqual(coordinates(first),coordinates(second));
 for(const n of first.nodes){assert.ok(Number.isFinite(n.x)&&Number.isFinite(n.y));assert.ok(n.x>=65&&n.x<=1135);assert.ok(n.y>=65&&n.y<=900);}
 for(let i=0;i<first.nodes.length;i++) for(let j=0;j<i;j++) assert.ok(distance(first.nodes[i],first.nodes[j])>65,'circle collision separation');
});
test('weight changes reflow unpinned caches, unchanged polling never drifts and manual anchors survive',()=>{
 const graph=pairGraph(false),first=layoutGraph(graph),cache=coordinates(first);
 const unchanged=layoutGraph(graph,cache,{previousSignature:first.layoutSignature});assert.deepEqual(coordinates(unchanged),cache);
 const changed=pairGraph(true),anchor={x:300,y:400},pinned=new Map([['task:1',anchor]]);cache.set('task:1',anchor);
 const next=layoutGraph(changed,cache,{pinned,previousSignature:first.layoutSignature});assert.deepEqual(coordinates(next).get('task:1'),anchor);assert.notDeepEqual(coordinates(next).get('agent:dev'),cache.get('agent:dev'));
 const stable=layoutGraph(changed,coordinates(next),{pinned,previousSignature:next.layoutSignature});assert.deepEqual(coordinates(stable),coordinates(next));
 const extended=buildGraph({tasks:[{id:1,status:'todo'},{id:2,status:'planning'}],agents:['dev'],sessions:[{task_id:1,agent:'dev',status:'running'},{task_id:2,agent:'dev',status:'running'}]});
 const topologyChange=layoutGraph(extended,coordinates(stable),{pinned,previousSignature:stable.layoutSignature});
 assert.deepEqual(coordinates(topologyChange).get('task:1'),anchor);assert.notDeepEqual(coordinates(topologyChange).get('agent:dev'),coordinates(stable).get('agent:dev'));assert.ok(Number.isFinite(coordinates(topologyChange).get('task:2').x));
});
test('element tracks only dragged anchors and reflows changed relations',()=>{
 const el=connected(),node=el.nodes[0];event(el,'pointerdown',node,{button:0,pointerId:8,clientX:0,clientY:0});event(el,'pointermove',node,{pointerId:8,clientX:30,clientY:10});event(el,'pointerup',node,{pointerId:8});
 assert.equal(el.pinned.size,1);const anchor={...el.pinned.get(node.dataset.key)},old=el.positions.get('agent:dev');
 el.data={...fixture,sessions:fixture.sessions.map(s=>({...s,status:'done'}))};assert.deepEqual(el.positions.get(node.dataset.key),anchor);assert.notDeepEqual(el.positions.get('agent:dev'),old);
 const stable=[...el.positions];el.data={...el.data};assert.deepEqual([...el.positions],stable);
 assert.doesNotMatch(el.innerHTML,/data-action="(?:reset|layout|titles)"/);
});
test('compact labels omit long titles by default but aria, native title and hover/focus panel retain them',()=>{
 const el=connected(),svg=renderGraph(el.graph),visible=[...svg.matchAll(/<text[^>]*>(.*?)<\/text>/g)].map(m=>m[1]).join(' ');
 assert.match(visible,/TSK-1/);assert.doesNotMatch(visible,/planning|todo/);assert.match(svg,/progress 1 of 3/);assert.match(visible,/dev/);assert.doesNotMatch(visible,/Fix/);assert.match(svg,/aria-label="TSK-1: Fix &lt;script&gt;/);assert.match(svg,/<title>TSK-1: Fix &lt;script&gt;/);
 event(el,'pointerover',el.nodes[0]);assert.match(el.panel.textContent,/TSK-1: Fix <script>/);event(el,'focusin',el.nodes.find(n=>n.dataset.key==='agent:dev'));assert.match(el.panel.textContent,/dev · online/);
 assert.equal(el.showTitles,false);assert.doesNotMatch(el.innerHTML,/data-action="titles"/);
});
test('D3 links mutate their own copies, label envelopes collide, and static layouts leave no timer',()=>{
 const graph=prepareGraph(buildGraph(fixture)),simulation=createGraphSimulation(graph);
 try {
  assert.equal(typeof graph.edges[0].source,'string');assert.equal(typeof simulation.force('link').links()[0].source,'object');
  assert.ok(simulation.force('collision').radius()(graph.nodes[0])>70);
  simulation.tick(220);assert.ok(simulation.alpha()<simulation.alphaMin());
 } finally {simulation.stop();}
});
test('client-only drag pins and cached positions survive filtering and restoring nodes',()=>{
 const el=connected(),node=el.nodes[0];event(el,'pointerdown',node,{button:0,pointerId:12,clientX:0,clientY:0});event(el,'pointermove',node,{pointerId:12,clientX:70,clientY:40});event(el,'pointerup',node,{pointerId:12});
 const anchor={...el.pinned.get('task:1')};el.view={x:17,y:19,scale:1.4};el.data={...fixture,tasks:[fixture.tasks[1]]};assert.deepEqual(el.pinned.get('task:1'),anchor);assert.deepEqual(el.positions.get('task:1'),anchor);
 el.data=fixture;assert.deepEqual(el.positions.get('task:1'),anchor);assert.deepEqual(el.view,{x:17,y:19,scale:1.4});assert.equal(el.graph.nodes[0].fx,anchor.x);
});
test('live D3 ticks existing SVG nodes, unchanged polling does not restart, disconnect stops timers',async t=>{
 const media=globalThis.matchMedia;globalThis.matchMedia=()=>({matches:false,addEventListener(){},removeEventListener(){}});
 const el=connected();const simulation=el.simulation;t.after(()=>{el.isConnected=false;el.disconnectedCallback();globalThis.matchMedia=media;});
 const group=el.nodes[0],line=el.lines[0],initial=group.attrs.transform;
 await new Promise(resolve=>setTimeout(resolve,110));assert.equal(el.nodes[0],group);assert.notEqual(group.attrs.transform,initial);assert.ok(Number.isFinite(Number(line.attrs.x1)));
 let restarts=0;const restart=simulation.restart.bind(simulation);simulation.restart=()=>{restarts++;return restart();};const camera={...el.view};el.data={...fixture,tasks:fixture.tasks.map(n=>({...n,comment_count:99}))};assert.equal(el.simulation,simulation);assert.equal(restarts,0);assert.deepEqual(el.view,camera);
 const n=el.nodes[0];event(el,'pointerdown',n,{button:0,pointerId:42,clientX:0,clientY:0});event(el,'pointermove',n,{pointerId:42,clientX:40,clientY:20});event(el,'pointerup',n,{pointerId:42});assert.ok(restarts>0);assert.ok(el.pinned.has('task:1'));
 el.isConnected=false;el.disconnectedCallback();assert.equal(el.simulation,null);assert.equal(simulation.on('tick'),undefined);const alpha=simulation.alpha();const stopped=[...el.positions];await new Promise(resolve=>setTimeout(resolve,60));assert.equal(simulation.alpha(),alpha);assert.deepEqual([...el.positions],stopped);
});
test('reduced motion synchronously settles and media-query listener is cleaned on disconnect',()=>{
 const previous=globalThis.matchMedia;let added=0,removed=0;globalThis.matchMedia=()=>({matches:true,addEventListener(){added++;},removeEventListener(){removed++;}});
 const el=connected();try {assert.ok(el.simulation.alpha()<el.simulation.alphaMin());assert.equal(added,1);assert.ok(el.nodes[0].attrs.transform);} finally {el.isConnected=false;el.disconnectedCallback();globalThis.matchMedia=previous;}assert.equal(removed,1);
});
test('graph strictly includes planning/todo tasks only, and excludes their completed or unknown relations',()=>{
 const tasks=['planning','todo','validated','closed','unknown',undefined].map((status,i)=>({id:i+1,status}));
 const graph=buildGraph({tasks,sessions:tasks.map(t=>({task_id:t.id,agent:` + "`" + `agent-${t.id}` + "`" + `,status:'running'}))});
 assert.deepEqual(graph.nodes.filter(n=>n.kind==='task').map(n=>n.id),[1,2]);assert.equal(graph.edges.length,2);assert.deepEqual(graph.edges.map(e=>e.source),['task:1','task:2']);assert.equal(graph.nodes.filter(n=>n.kind==='agent').length,2);
 const svg=renderGraph(layoutGraph(graph));assert.doesNotMatch(svg,/data-key="task:[3-6]"/);assert.doesNotMatch(svg,/agent-[3-6]/);
});
test('age is last activity, never creation when valid, with stable bounded inactivity buckets',()=>{
 const now=Date.parse('2026-11-03T12:00:00Z'),ago=days=>new Date(now-days*86400000).toISOString();
 const recent=taskRecency({last_activity_at:ago(1),created_at:ago(365)},{now});assert.equal(recent.ageDays,1);assert.ok(recent.radius>28&&recent.radius<30);
 const old=taskRecency({last_activity_at:ago(100),created_at:ago(1)},{now});assert.equal(old.ageDays,100);assert.ok(old.radius>=12&&old.radius<12.1);assert.ok(old.recencyScale>.25&&old.recencyScale<.26);
 for(const last_activity_at of [undefined,null,'','invalid']) {const fallback=taskRecency({last_activity_at,created_at:ago(31)},{now});assert.equal(fallback.ageDays,31);assert.ok(fallback.radius>14&&fallback.radius<15);}
 assert.equal(taskRecency({last_activity_at:'invalid',created_at:'invalid'},{now}).radius,30);assert.equal(taskRecency({last_activity_at:ago(-1)},{now}).ageDays,0);
 assert.equal(taskRecency({last_activity_at:ago(100)},{now,active:true}).radius,30);
 assert.ok(taskRecency({last_activity_at:ago(8)},{now}).recencyScale<.8);
 const data={now,tasks:[{id:1,status:'todo',last_activity_at:ago(1)},{id:6,status:'planning',last_activity_at:ago(100),comment_count:12345,status_changes:67890}],sessions:[{task_id:1,agent:'a',status:'done'},{task_id:6,agent:'a',status:'done'}]};
 const graph=buildGraph(data);assert.ok(graph.nodes[1].radius<graph.nodes[0].radius);assert.ok(edgeWeight(graph.edges[1])<edgeWeight(graph.edges[0]));
 const sim=createGraphSimulation(prepareGraph(graph));try{const links=sim.force('link').links();assert.ok(sim.force('link').strength()(links[1])<sim.force('link').strength()(links[0]));}finally{sim.stop();}
 const svg=renderGraph(layoutGraph(graph));assert.match(svg,/class="tg-node-content" style="--tg-node-scale:0\.400/);assert.match(svg,/class="tg-body" r="30"/);assert.match(svg,/12\.3k\/67\.8k/);assert.equal(compactCount(999),'999');
});
test('node geometry changes restart physics but ordinary metadata polling stays stable',()=>{
 const el=connected(),now=Date.parse('2026-11-03T12:00:00Z');
 const tasks=[{id:1,name:'Short',status:'todo',last_activity_at:'2026-07-01T00:00:00Z'}];
 el.data={tasks,now};const aged=el.simulation;
 el.data={tasks:tasks.map(t=>({...t,comment_count:2})),now};assert.equal(el.simulation,aged);
 tasks[0].last_activity_at='2026-11-03T11:00:00Z';el.data={tasks,now};assert.notEqual(el.simulation,aged);assert.equal(el.graph.nodes[0].radius,30);
 el.showTitles=true;el.render();const compact=el.simulation;
 tasks[0].name='A much longer task title changes the collision envelope';el.data={tasks,now};assert.notEqual(el.simulation,compact);
 el.disconnectedCallback();
});
test('progress ring always has three unfilled-geometry arcs, counts filled by status, no task status text',()=>{
 for(const [status,segments] of [['planning',1],['todo',2],['validated',3],['closed',0]]) {
  assert.equal(progressSegments(status),segments);const ring=renderProgressRing(status,18);
  assert.equal([...ring.matchAll(/class="tg-progress-segment"/g)].length,3);assert.equal([...ring.matchAll(/data-filled="true"/g)].length,segments);assert.equal([...ring.matchAll(/fill="none"/g)].length,3);assert.equal([...ring.matchAll(/stroke-width="3"/g)].length,3);assert.doesNotMatch(ring,/NaN|Infinity/);
 }
 const graph=layoutGraph(buildGraph(fixture)),svg=renderGraph(graph);assert.equal([...svg.matchAll(/class="tg-progress"/g)].length,2);assert.equal([...svg.matchAll(/class="tg-state"/g)].length,1);assert.match(svg,/class="tg-state"[^>]*>online/);
 const el=connected();el.data={tasks:[...fixture.tasks,{id:3,status:'closed'},{id:4,status:'validated'}]};assert.equal(el.graph.nodes.filter(n=>n.kind==='task').length,2);assert.doesNotMatch(el.innerHTML,/data-key="task:[34]"/);
});
`
