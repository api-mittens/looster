import {forceSimulation,forceLink,forceManyBody,forceCollide,forceX,forceY} from './d3-force-adapter.mjs';
// Model/render helpers and deterministic D3 layout. No DOM dependencies.
export const escapeHTML = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
export const count = value => Number.isFinite(Number(value)) ? Math.max(0, Math.floor(Number(value))) : 0;
export const HEATMAP_LEGEND = 'Activity = comments + status changes (including initial status). Continuous fixed scale: green at 0, yellow at 10, red at 25+; colors interpolate between anchors, independent of other tasks.';
export function activityColor(total) {
  const value = Math.min(25, count(total));
  const start = value <= 10 ? [34,197,94] : [234,179,8];
  const end = value <= 10 ? [234,179,8] : [239,68,68];
  const ratio = value <= 10 ? value / 10 : (value - 10) / 15;
  return `rgb(${start.map((v,i) => Math.round(v + (end[i]-v)*ratio)).join(',')})`;
}
export function activity(task) {
  const comments = count(task.comment_count), changes = count(task.status_changes), total = comments + changes;
  return {comments, changes, total, level: total >= 25 ? 'red' : total >= 10 ? 'yellow' : 'green'};
}
const timestamp = value => value == null || value === '' ? NaN : typeof value === 'number' ? value : Date.parse(value);
export function taskRecency(task, {now=Date.now(),active=false} = {}) {
  const last=timestamp(task.last_activity_at),created=timestamp(task.created_at);
  const at=Number.isFinite(last)?last:Number.isFinite(created)?created:NaN;
  const ageDays=Number.isFinite(at)?Math.max(0,Math.floor((now-at)/86400000)):0;
  // Whole-day exponential decay is visible after days, not only after months.
  // Keep old counters readable, and never shrink a task currently being worked.
  const recencyScale=active?1:.25+.75*Math.pow(2,-ageDays/14);
  const radius=active?30:12+18*Math.pow(2,-ageDays/10);
  return {ageDays,recencyScale,radius,lastActivityAt:Number.isFinite(at)?at:null};
}
export const progressSegments = status => ({planning:1,todo:2,validated:3,closed:0})[status] ?? 0;
export function compactCount(value) {
  const n=count(value);
  for(const [divisor,suffix] of [[1e9,'B'],[1e6,'M'],[1e3,'k']]) if(n>=divisor) return n/divisor>=1000?'999B+':`${Math.floor(n/divisor*10)/10}${suffix}`;
  return String(n);
}
export function buildGraph({tasks = [], agents = [], sessions = [], workers = [], heatmap = false, now=Date.now()} = {}) {
  const nodes = [], taskKeys = new Set(), agentNames = new Set();
  const running=new Set(sessions.filter(s=>s.status==='running').map(s=>String(s.task_id)));
  for (const task of tasks) {
    if (!['planning','todo'].includes(task.status) || task.id == null || taskKeys.has(String(task.id))) continue;
    taskKeys.add(String(task.id));
    nodes.push({key:`task:${task.id}`, kind:'task', id:task.id, label:String(task.name ?? `TSK-${task.id}`), status:task.status, working:running.has(String(task.id)), ...activity(task),...taskRecency(task,{now,active:running.has(String(task.id))})});
  }
  for (const session of sessions) if (taskKeys.has(String(session.task_id)) && session.agent) agentNames.add(String(session.agent));
  const online = new Set(workers.flatMap(w => Array.isArray(w.agents) ? w.agents : []));
  for (const name of [...agentNames].sort()) nodes.push({key:`agent:${name}`, kind:'agent', name, label:name, online:online.has(name)});
  const grouped = new Map();
  const recency=new Map(nodes.filter(n=>n.kind==='task').map(n=>[n.key,n.recencyScale]));
  for (const session of sessions) {
    if (!taskKeys.has(String(session.task_id)) || !session.agent) continue;
    const source = `task:${session.task_id}`, target = `agent:${session.agent}`, key = JSON.stringify([source,target]);
    const edge = grouped.get(key) ?? {key, source, target, active:false, count:0,recencyScale:recency.get(source)};
    edge.active ||= session.status === 'running'; edge.count++; grouped.set(key,edge);
  }
  return {nodes, edges:[...grouped.values()], heatmap:Boolean(heatmap)};
}
// Bounded multiplicity prevents a prolific session history dominating the graph.
export const edgeWeight = edge => (edge.active ? 2.5 : 1) * (1 + .3 * Math.log2(Math.min(8, Math.max(1, count(edge.count))))) * (edge.recencyScale ?? 1);
export function topologySignature(graph, {showTitles=false} = {}) {
  return JSON.stringify([showTitles,graph.nodes.map(n=>[n.key,n.radius ?? 22,n.recencyScale ?? 1,collisionRadius(n,showTitles)]).sort((a,b)=>a[0].localeCompare(b[0])),graph.edges.map(e=>[e.source,e.target,edgeWeight(e)]).sort((a,b)=>JSON.stringify(a).localeCompare(JSON.stringify(b)))]);
}
function hash(text) { let h = 2166136261; for (const c of text) h = Math.imul(h ^ c.charCodeAt(0),16777619); return h >>> 0; }
const valid = p => p && Number.isFinite(p.x) && Number.isFinite(p.y);
export function collisionRadius(node, showTitles=false) {
  const text=node.kind==='agent'?node.label:showTitles?node.label:`TSK-${node.id}`;
  const width=Math.min(250,Math.max(64,String(text).length*8));
  // Circle plus label/state envelope, not merely the visible circle radius.
  return (Math.hypot(width/2,showTitles&&node.kind==='task'?95:65)+10)*(node.kind==='task'?(node.radius??30)/30:1);
}
export function prepareGraph(graph, previous = new Map(), {pinned=new Map(),showTitles=false} = {}) {
  const width=Math.max(1200,Math.ceil(Math.sqrt(graph.nodes.length)*175));
  const height=Math.max(1000,Math.ceil(Math.sqrt(graph.nodes.length)*145));
  const sorted=[...graph.nodes].sort((a,b)=>a.key.localeCompare(b.key));
  const adjacency=new Map(sorted.map(n=>[n.key,[]]));
  const edges=graph.edges.filter(e=>adjacency.has(e.source)&&adjacency.has(e.target)).sort((a,b)=>a.source.localeCompare(b.source)||a.target.localeCompare(b.target));
  for(const e of edges) {adjacency.get(e.source).push(e.target);adjacency.get(e.target).push(e.source);}
  const components=[],seen=new Set();
  for(const n of sorted) if(!seen.has(n.key)) {
    const keys=[n.key];seen.add(n.key);
    for(let i=0;i<keys.length;i++) for(const key of adjacency.get(keys[i])) if(!seen.has(key)) {seen.add(key);keys.push(key);}
    components.push(keys.sort());
  }
  const centers=new Map(),cols=Math.ceil(Math.sqrt(components.length || 1)),rows=Math.ceil(components.length/cols);
  components.forEach((keys,i)=>{
    const center={x:100+(i%cols+.5)*(width-200)/cols,y:100+(Math.floor(i/cols)+.5)*(height-200)/rows};
    for(const key of keys) centers.set(key,center);
  });
  const nodes=sorted.map(node=>{
    const center=centers.get(node.key),h=hash(node.key),angle=(h%3600)*Math.PI/1800;
    const initial=pinned.get(node.key) ?? previous.get(node.key);
    const pin=pinned.get(node.key);
    return {...node,x:valid(initial)?initial.x:center.x+Math.cos(angle)*100,y:valid(initial)?initial.y:center.y+Math.sin(angle)*100,vx:0,vy:0,fx:valid(pin)?pin.x:null,fy:valid(pin)?pin.y:null,cx:center.x,cy:center.y};
  });
  const byKey=new Map(nodes.map(n=>[n.key,n]));
  return {...graph,nodes:graph.nodes.map(n=>byKey.get(n.key)),edges,width,height,showTitles,layoutSignature:topologySignature(graph,{showTitles})};
}
export function constrainGraph(graph) {
  for(const n of graph.nodes) {
    if(n.fx!=null) {n.x=n.fx;n.y=n.fy;continue;}
    n.x=Math.max(80,Math.min(graph.width-80,Number.isFinite(n.x)?n.x:n.cx));
    n.y=Math.max(80,Math.min(graph.height-100,Number.isFinite(n.y)?n.y:n.cy));
  }
}
// Caller owns the timer: returned simulations start STOPPED, and may be manually
// ticked (pure layout/reduced motion) or restarted by the connected element.
export function createGraphSimulation(graph) {
  const links=graph.edges.map(e=>({...e})); // D3 mutates link endpoints.
  return forceSimulation([...graph.nodes].sort((a,b)=>a.key.localeCompare(b.key))).stop().alphaDecay(.035).velocityDecay(.38)
    .force('link',forceLink(links).id(n=>n.key).distance(e=>220/Math.sqrt(edgeWeight(e))).strength(e=>Math.min(.65,.16*edgeWeight(e))))
    .force('charge',forceManyBody().strength(-230).distanceMax(900))
    .force('collision',forceCollide(n=>collisionRadius(n,graph.showTitles)).strength(.95).iterations(3))
    .force('x',forceX(n=>n.cx).strength(.025))
    .force('y',forceY(n=>n.cy).strength(.025));
}
// Synchronous deterministic helper uses exactly the same D3 forces as live mode.
export function layoutGraph(graph, previous = new Map(), options = {}) {
  const prepared=prepareGraph(graph,previous,options);
  if((options.previousSignature===undefined || options.previousSignature===prepared.layoutSignature) && graph.nodes.every(n=>valid(previous.get(n.key)))) return prepared;
  const simulation=createGraphSimulation(prepared);
  for(let i=0;i<220;i++) {simulation.tick();constrainGraph(prepared);}
  simulation.stop();
  return prepared;
}
export function renderProgressRing(status, radius=30) {
  const filled=progressSegments(status),r=radius+6;
  return `<g class="tg-progress" data-progress="${filled}" aria-label="${escapeHTML(status)}, progress ${filled} of 3"><title>${escapeHTML(status)}: progress ${filled} of 3</title>${[0,1,2].map(i=>{
    const a=(-85+i*120)*Math.PI/180,b=(25+i*120)*Math.PI/180;
    const point=angle=>`${(r*Math.cos(angle)).toFixed(3)} ${(r*Math.sin(angle)).toFixed(3)}`;
    return `<path class="tg-progress-segment" data-segment="${i+1}" data-filled="${i<filled}" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" d="M ${point(a)} A ${r} ${r} 0 0 1 ${point(b)}"/>`;
  }).join('')}</g>`;
}
export function renderGraph(graph) {
  const esc=escapeHTML, byKey=new Map(graph.nodes.map(n=>[n.key,n]));
  return `<svg viewBox="0 0 ${graph.width} ${graph.height}" role="group" aria-label="Task and agent graph. Select nodes to open details. Drag background to pan, scroll to zoom."><title>Task and agent graph</title><g class="tg-viewport">${graph.edges.map(e=>{
    const a=byKey.get(e.source),b=byKey.get(e.target); if(!a || !b) return '';
    return `<line class="tg-edge${e.active?' tg-active':''}" x1="${a.x}" y1="${a.y}" x2="${b.x}" y2="${b.y}"><title>${esc(a.label)} to ${esc(b.label)}: ${e.active?'running':'historical idle'}, ${e.count} sessions</title></line>`;
  }).join('')}${graph.nodes.map(n=>{
    const info=n.kind==='task'?`TSK-${n.id}: ${n.label}, ${n.status}, progress ${progressSegments(n.status)} of 3, ${n.comments} comments, ${n.changes} status changes, ${n.total} activity, ${n.ageDays} days since last activity`:`${n.name}, ${n.online?'online':'offline'}`;
    const label=n.kind==='task'?`TSK-${n.id}`:n.label;
    const radius=n.kind==='task'?30:22,scale=n.kind==='task'?(n.radius??30)/30:1,counters=n.kind==='task'?`${compactCount(n.comments)}/${compactCount(n.changes)}`:'@';
    const countSize=Math.min(15,(radius*2-6)/Math.max(1,counters.length*.62));
    return `<g class="tg-node tg-${n.kind}${graph.heatmap&&n.kind==='task'?` tg-heat-${n.level}`:''}${n.online?' tg-online':''}${n.working?' tg-working':''}" data-key="${esc(n.key)}" role="button" tabindex="0" aria-label="${esc(info)}" transform="translate(${n.x},${n.y})"><title>${esc(info)}</title><g class="tg-node-content" style="--tg-node-scale:${scale}"><circle class="tg-body" r="${radius}"${graph.heatmap&&n.kind==='task'?` style="stroke:${activityColor(n.total)};fill:${activityColor(n.total)};fill-opacity:.25"`:''}/>${n.kind==='task'?renderProgressRing(n.status,radius):''}<text class="tg-count" text-anchor="middle" y="${countSize*.34}" style="font-size:${countSize.toFixed(2)}px">${counters}</text><text class="${n.kind==='task'?'tg-ref':'tg-label'}" text-anchor="middle" y="${n.kind==='task'?radius+23:49}">${esc(label.length>30?label.slice(0,29)+'…':label)}</text>${n.kind==='agent'?`<text class="tg-state" text-anchor="middle" y="66">${n.online?'online':'offline'}</text>`:''}${graph.showTitles&&n.kind==='task'?`<text class="tg-task-title" text-anchor="middle" y="${radius+43}">${esc(n.label.length>30?n.label.slice(0,29)+'…':n.label)}</text>`:''}</g></g>`;
  }).join('')}</g></svg>`;
}
