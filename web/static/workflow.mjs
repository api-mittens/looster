import {conditionLabel} from './views.mjs';

export function layout(rules) {
  const nodes = new Map(), edges = [];
  const add = (id,label,kind) => { if(!nodes.has(id)) nodes.set(id,{id,label,kind}); return id; };
  for(const [i,r] of rules.entries()) {
    if(['prompt','agent','tag'].includes(r.trigger)) continue;
    const target = add('agent:'+r.target,r.target,'agent');
    if(r.trigger.startsWith('stop_')) {
      const agent = nodes.get(target);
      const status = r.trigger.slice(5)+(conditionLabel(r) ? ' '+conditionLabel(r) : '');
      agent.stopStatuses ||= [];
      if(!agent.stopStatuses.includes(status)) agent.stopStatuses.push(status);
      continue;
    }
    // A shared status is a trigger, not a dependency between otherwise separate
    // agent workflows. Repeat its node for each target instead of joining DAGs.
    const source = r.trigger === 'completed' ? add('agent:'+r.source,r.source,'agent') : r.trigger === 'every' ? add('timer:'+i,`Every ${r.interval_seconds}s`,'timer') : add('status:'+r.trigger+':'+r.target,r.trigger,'status');
    const condition = conditionLabel(r);
    edges.push({source,target,label:(r.trigger === 'completed' ? 'completed' : 'dispatch')+(condition ? ' '+condition : ''),condition});
  }
  // Collapse strongly connected components so cyclic factories stay finite.
  let index = 0;
  const stack = [], active = new Set(), indices = new Map(), low = new Map(), components = [], component = new Map();
  const visit = id => {
    indices.set(id,index); low.set(id,index++); stack.push(id); active.add(id);
    for(const e of edges.filter(e => e.source === id)) {
      if(!indices.has(e.target)) { visit(e.target); low.set(id,Math.min(low.get(id),low.get(e.target))); }
      else if(active.has(e.target)) low.set(id,Math.min(low.get(id),indices.get(e.target)));
    }
    if(low.get(id) === indices.get(id)) {
      const group = []; let member;
      do { member = stack.pop(); active.delete(member); component.set(member,components.length); group.push(member); } while(member !== id);
      components.push(group);
    }
  };
  for(const id of nodes.keys()) if(!indices.has(id)) visit(id);
  const ranks = new Map();
  const rank = c => { if(ranks.has(c)) return ranks.get(c); const parents = edges.filter(e => component.get(e.target) === c && component.get(e.source) !== c).map(e => component.get(e.source)); const value = parents.length ? Math.max(...parents.map(p => rank(p)+1)) : 0; ranks.set(c,value); return value; };
  // Give each weakly connected workflow a separate horizontal lane.
  const neighbours = new Map([...nodes.keys()].map(id => [id,[]]));
  for(const e of edges) { neighbours.get(e.source).push(e.target); neighbours.get(e.target).push(e.source); }
  const visited = new Set(), workflows = [];
  for(const id of nodes.keys()) {
    if(visited.has(id)) continue;
    const group = [], pending = [id]; visited.add(id);
    while(pending.length) { const current = pending.pop(); group.push(nodes.get(current)); for(const next of neighbours.get(current)) if(!visited.has(next)) {visited.add(next);pending.push(next);} }
    workflows.push(group);
  }
  const columnWidth = Math.max(300,...edges.map(e => e.condition.length*7+260));
  const lanes = []; let offset = 16;
  for(const [laneIndex,group] of workflows.entries()) {
    const ids = new Set(group.map(n => n.id)), laneEdges = edges.filter(e => ids.has(e.source));
    const columns = new Map();
    for(const n of group) {
      const c = component.get(n.id); n.column = rank(c);
      n.cyclic = components[c].length > 1 || edges.some(e => e.source === n.id && e.target === n.id);
      if(!columns.has(n.column)) columns.set(n.column,[]);
      columns.get(n.column).push(n);
    }
    const ordered = [...columns.keys()].sort((a,b) => a-b);
    // Put siblings near their neighbours rather than relying on rule order.
    for(let sweep=0;sweep<4;sweep++) {
      const positions = new Map(); for(const column of columns.values()) column.forEach((n,i) => positions.set(n.id,i));
      for(const key of sweep%2 ? [...ordered].reverse() : ordered) {
        const score = n => {
          const adjacent = neighbours.get(n.id).filter(id => sweep%2 ? nodes.get(id).column > key : nodes.get(id).column < key);
          return adjacent.length ? adjacent.reduce((sum,id) => sum+positions.get(id),0)/adjacent.length : positions.get(n.id);
        };
        columns.get(key).sort((a,b) => score(a)-score(b));
        columns.get(key).forEach((n,i) => positions.set(n.id,i));
      }
    }
    const longEdges = laneEdges.filter(e => Math.abs(nodes.get(e.source).column-nodes.get(e.target).column)>1);
    const top = offset+52+longEdges.length*14;
    for(const [column,list] of columns) list.forEach((n,row) => {n.x=40+column*columnWidth;n.y=top+row*130;n.lane=laneIndex;});
    longEdges.forEach((e,i) => {e.channelY=offset+42+i*14;});
    const height = top-offset+Math.max(...[...columns.values()].map(c => c.length))*130-24;
    lanes.push({x:16,y:offset,width:Math.max(568,...group.map(n => n.x+280))-16,height,label:'Workflow '+(laneIndex+1)});
    offset += height+24;
  }
  return {nodes:[...nodes.values()],edges,lanes,width:Math.max(600,...lanes.map(l => l.x+l.width+16)),height:Math.max(220,offset)};
}
