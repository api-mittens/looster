import {patchHTML} from './refresh.mjs';
import {layout} from './workflow.mjs';

const esc = v => String(v).replace(/[&<>"']/g,c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const stopLabel = statuses => `Until task is ${statuses.join(' or ')}`;

class WorkflowGraph extends HTMLElement {
  set rules(rules) {
    const graph = layout(rules), byID = new Map(graph.nodes.map(n => [n.id,n]));
    if(!graph.nodes.length) { patchHTML(this,'<p>No workflow rules configured.</p>'); return; }
    patchHTML(this,`<div class="workflow-canvas"><svg width="${graph.width}" height="${graph.height}" viewBox="0 0 ${graph.width} ${graph.height}" role="group" aria-labelledby="graph-title"><title id="graph-title">Agent workflows. Stop conditions appear beneath agent cards. Select an agent to read instructions and memories.</title><defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="#a1a1aa"/></marker></defs>${graph.edges.map(e => {
      const a = byID.get(e.source), b = byID.get(e.target);
      let path;
      if(e.channelY !== undefined) { const ax=a.x+220, bx=b.x, ay=a.y+38, by=b.y+38; path=`M ${ax} ${ay} H ${ax+16} V ${e.channelY} H ${bx-16} V ${by} H ${bx}`; }
      else if(a.x === b.x) { const x = a.x+245; path = `M ${a.x+220} ${a.y+25} C ${x+25} ${a.y-25}, ${x+25} ${b.y+105}, ${b.x+220} ${b.y+55}`; }
      else { const ax = a.x+220, ay = a.y+38, bx = b.x, by = b.y+38, mid = (ax+bx)/2; path = `M ${ax} ${ay} C ${mid} ${ay}, ${mid} ${by}, ${bx} ${by}`; }
      const labelX = a.x === b.x ? a.x+240 : (a.x+220+b.x)/2;
      const labelY = e.channelY !== undefined ? e.channelY-8 : (a.y+b.y)/2+25;
      return `<path d="${path}" fill="none" stroke="#a1a1aa" stroke-width="1.5" marker-end="url(#arrow)"><title>${esc(a.label)} → ${esc(b.label)} (${esc(e.label)})</title></path>${e.condition ? `<text class="graph-kind workflow-condition" x="${labelX}" y="${labelY}" text-anchor="middle"><title>${esc(e.condition)}</title>${esc(e.condition)}</text>` : ''}`;
    }).join('')}${graph.nodes.map(n => {
      const stops = n.stopStatuses?.length ? stopLabel(n.stopStatuses) : '';
      return `<g data-refresh-key="${esc(n.id)}" ${n.kind === 'agent' ? `data-agent="${esc(n.label)}" role="button" tabindex="0" aria-label="View ${esc(n.label)} instructions and memories${stops ? '. '+esc(stops) : ''}"` : ''} transform="translate(${n.x},${n.y})"><title>${esc(n.label)}${n.cyclic ? ' · cycle' : ''}${stops ? ' · '+esc(stops) : ''}</title><rect width="220" height="76" rx="8" fill="white" stroke="#d4d4d8"/><circle cx="18" cy="22" r="4" fill="${n.kind === 'agent' ? '#3b82f6' : '#a1a1aa'}"/><text x="30" y="26" class="graph-kind">${esc(n.kind)}${n.cyclic ? ' · cycle' : ''}</text><text x="16" y="53" class="graph-label">${esc(n.label.length > 24 ? n.label.slice(0,23)+'…' : n.label)}</text>${stops ? `<g class="workflow-stop-banner"><rect y="82" width="220" height="24" rx="5"/><title>${esc(stops)}</title><text x="10" y="98">${esc(stops.length > 28 ? stops.slice(0,27)+'…' : stops)}</text></g>` : ''}</g>`;
    }).join('')}</svg></div>`);
    const open = node => this.dispatchEvent(new CustomEvent('agent-open',{bubbles:true,detail:{name:node.dataset.agent}}));
    this.querySelectorAll('[data-agent]').forEach(node => {
      node.onclick = () => open(node);
      node.onkeydown = e => { if(e.key === 'Enter' || e.key === ' ') {e.preventDefault();open(node);} };
    });
  }
}
customElements.define('workflow-graph',WorkflowGraph);
