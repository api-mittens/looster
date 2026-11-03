import {buildGraph,prepareGraph,renderGraph,createGraphSimulation,constrainGraph,topologySignature,escapeHTML,HEATMAP_LEGEND} from './task-graph.mjs';

const EVENTS=['click','keydown','pointerdown','pointermove','pointerup','pointercancel','lostpointercapture','wheel','pointerover','focusin','change'];
export class TaskAgentGraph extends HTMLElement {
  constructor() {
    super();
    this.positions=new Map(); this.pinned=new Map(); this.view={x:0,y:0,scale:1};
    this._data={}; this.heatmap=false; this.showTitles=false;
    this.handleEvent=this.handleEvent.bind(this);
    this.onMotionChange=()=>{if(this.motion?.matches) this.settle();};
  }
  set data(value) {
    this._data=value ?? {};
    if(Object.hasOwn(this._data,'heatmap')) this.heatmap=Boolean(this._data.heatmap);
    if(this.isConnected) this.render();
  }
  get data() {return this._data;}
  connectedCallback() {
    if(this.listening) return;
    this.listening=true;
    for(const type of EVENTS) this.addEventListener(type,this.handleEvent,{passive:false});
    this.motion=globalThis.matchMedia?.('(prefers-reduced-motion: reduce)');
    this.motion?.addEventListener?.('change',this.onMotionChange);
    this.render();
  }
  disconnectedCallback() {
    for(const type of EVENTS) this.removeEventListener(type,this.handleEvent);
    this.motion?.removeEventListener?.('change',this.onMotionChange);
    this.listening=false; this.stopSimulation(); this.releaseDrag();
  }
  stopSimulation() {
    this.simulation?.on('tick',null).on('end',null).stop();
    this.simulation=null;
  }
  render() {
    if(this.drag) {this.pendingRender=true;return;}
    const focused=this.querySelector('[data-key]:focus')?.dataset.key;
    const actionFocus=this.querySelector('[data-action]:focus')?.dataset.action;
    const tagMenuOpen=this.querySelector('.tg-tag-filter')?.open;
    const tagFocus=this.querySelector('[data-tag-filter]:focus')?.value;
    const tagSummaryFocus=Boolean(this.querySelector('[data-tag-summary]:focus'));
    const selectedTags=this._data.selectedTags || [];
    const tags=this._data.tagOptions || [];
    const tagMenu=`<details class="tg-tag-filter"${tagMenuOpen?' open':''}><summary data-tag-summary>Tags: ${selectedTags.length?`${selectedTags.length} selected`:'All'}</summary><div class="tg-tag-options" role="group" aria-label="Filter tasks by tags"><label><input type="checkbox" data-tag-all${selectedTags.length?'':' checked'}> All tags (no filter)</label>${tags.map(tag=>`<label><input type="checkbox" data-tag-filter value="${escapeHTML(tag)}"${selectedTags.includes(tag)?' checked':''}> ${escapeHTML(tag)}</label>`).join('')}${tags.length?'':'<span>No tags on unfinished tasks.</span>'}<small>Match any selected tag.</small></div></details>`;
    const model=buildGraph({...this._data,heatmap:this.heatmap});
    const signature=topologySignature(model,{showTitles:this.showTitles});
    const changed=!this.graph || signature!==this.graph.layoutSignature;
    if(changed) {
      this.stopSimulation();
      this.graph=prepareGraph(model,this.positions,{pinned:this.pinned,showTitles:this.showTitles});
    } else {
      const existing=new Map(this.graph.nodes.map(n=>[n.key,n]));
      this.graph={...this.graph,...model,nodes:model.nodes.map(n=>Object.assign(existing.get(n.key),n))};
    }
    // Never discard invisible positions or pins: search filtering is not deletion.
    for(const n of this.graph.nodes) this.positions.set(n.key,{x:n.x,y:n.y});
    this.innerHTML=`<div class="tg-controls" role="group" aria-label="Graph controls"><button type="button" data-action="in" aria-label="Zoom in">+</button><button type="button" data-action="out" aria-label="Zoom out">−</button><button type="button" data-action="heatmap" aria-pressed="${this.heatmap}">Heatmap</button>${tagMenu}<span>Circle counts: comments / status changes. Drag nodes or background. Scroll to zoom.</span></div>${this.graph.nodes.length?renderGraph(this.graph):'<p class="tg-empty">No tasks or agents yet.</p>'}<div class="tg-detail" data-graph-detail role="status">Hover or focus a node to read its full title. Select to open details.</div><p class="tg-legend">Only unfinished planning/todo tasks are shown. Progress ring: one segment = planning, two = todo. Smaller nodes and weaker links mean longer inactivity (daily decay based on last activity, created date fallback). Active work stays full size. Blue marks online agents and tasks being worked on. Solid animated links: running sessions. Faint links: historical idle sessions. Related nodes attract, with stronger running and repeated-session links. Dragged nodes stay anchored in this browser session.${this.heatmap?`<br><span class="tg-gradient" aria-hidden="true"></span>${HEATMAP_LEGEND}`:''}</p>`;
    this.nodeElements=new Map([...this.querySelectorAll('[data-key]')].map(n=>[n.dataset.key,n]));
    this.edgeElements=[...this.querySelectorAll('.tg-edge')];
    this.updateView(); this.updatePositions(); this.showDetail(this.selectedKey);
    if(tagFocus!==undefined) [...this.querySelectorAll('[data-tag-filter]')].find(n=>n.value===tagFocus)?.focus();
    else if(tagSummaryFocus) this.querySelector('[data-tag-summary]')?.focus();
    else if(focused) this.nodeElements.get(focused)?.focus();
    else if(actionFocus) [...this.querySelectorAll('[data-action]')].find(n=>n.dataset.action===actionFocus)?.focus();
    if(this.graph.nodes.length && (changed || !this.simulation)) {
      this.simulation=createGraphSimulation(this.graph).on('tick',()=>this.updatePositions());
      this.reheat(changed?1:.2);
    }
  }
  settle() {
    if(!this.simulation) return;
    this.simulation.stop().alphaTarget(0);
    for(let i=0;i<220;i++) {this.simulation.tick();constrainGraph(this.graph);}
    this.updatePositions();
  }
  reheat(alpha=.4) {
    if(!this.simulation || !this.isConnected) return;
    this.simulation.alpha(Math.max(alpha,this.simulation.alpha())).alphaTarget(0);
    if(this.motion?.matches) this.settle();
    else this.simulation.restart();
  }
  updatePositions() {
    if(!this.graph) return;
    constrainGraph(this.graph);
    const byKey=new Map(this.graph.nodes.map(n=>[n.key,n]));
    for(const n of this.graph.nodes) {
      this.positions.set(n.key,{x:n.x,y:n.y});
      this.nodeElements?.get(n.key)?.setAttribute('transform',`translate(${n.x},${n.y})`);
    }
    this.graph.edges.forEach((e,i)=>{
      const a=byKey.get(e.source),b=byKey.get(e.target),line=this.edgeElements?.[i];
      if(!a||!b||!line) return;
      line.setAttribute('x1',a.x);line.setAttribute('y1',a.y);line.setAttribute('x2',b.x);line.setAttribute('y2',b.y);
    });
  }
  showDetail(key) {
    const panel=this.querySelector('[data-graph-detail]'),n=this.graph?.nodes.find(n=>n.key===key);
    if(panel) panel.textContent=n?(n.kind==='task'?`TSK-${n.id}: ${n.label} · ${n.status} · ${n.comments} comments / ${n.changes} status changes · ${n.ageDays} days since last activity`:`${n.name} · ${n.online?'online':'offline'}`):'Hover or focus a node to read its full title. Select to open details.';
  }
  updateView() {this.querySelector('.tg-viewport')?.setAttribute('transform',`translate(${this.view.x},${this.view.y}) scale(${this.view.scale})`);}
  zoom(factor, point={x:(this.graph?.width??1200)/2,y:(this.graph?.height??1000)/2}) {
    const next=Math.min(4,Math.max(.25,this.view.scale*factor)),ratio=next/this.view.scale;
    this.view.x=point.x-(point.x-this.view.x)*ratio;this.view.y=point.y-(point.y-this.view.y)*ratio;this.view.scale=next;this.updateView();
  }
  point(event) {
    const rect=this.querySelector('svg').getBoundingClientRect(),{width,height}=this.graph;
    const scale=Math.min(rect.width/width,rect.height/height)||1;
    return {x:(event.clientX-rect.left-(rect.width-width*scale)/2)/scale,y:(event.clientY-rect.top-(rect.height-height*scale)/2)/scale};
  }
  open(key) {
    const node=this.graph.nodes.find(n=>n.key===key);if(!node)return;
    this.selectedKey=key;this.showDetail(key);
    this.dispatchEvent(new CustomEvent(node.kind==='task'?'task-open':'agent-open',{bubbles:true,detail:node.kind==='task'?{id:node.id}:{name:node.name}}));
  }
  releaseDrag() {
    const drag=this.drag;this.drag=null;
    if(drag&&this.hasPointerCapture?.(drag.id)) this.releasePointerCapture(drag.id);
    if(drag?.moved) this.reheat(.3);
    if(this.pendingRender&&this.isConnected) {this.pendingRender=false;this.render();}
  }
  handleEvent(event) {
    const node=event.target.closest?.('[data-key]'),action=event.target.closest?.('[data-action]')?.dataset.action;
    if(event.type==='change') {
      if(event.target.hasAttribute?.('data-tag-all')) this.dispatchEvent(new CustomEvent('tag-filter',{bubbles:true,detail:{tags:[]}}));
      else if(event.target.hasAttribute?.('data-tag-filter')) {
        const tags=new Set(this._data.selectedTags || []);
        if(event.target.checked) tags.add(event.target.value);else tags.delete(event.target.value);
        this.dispatchEvent(new CustomEvent('tag-filter',{bubbles:true,detail:{tags:[...tags]}}));
      }
      return;
    }
    if(event.type==='pointerover'||event.type==='focusin') {if(node)this.showDetail(node.dataset.key);return;}
    if(event.type==='click') {
      if(this.suppressClick&&!action) {this.suppressClick=false;return;}
      if(action==='heatmap') {
        this.heatmap=!this.heatmap;
        this.render();this.querySelector(`[data-action="${action}"]`)?.focus();
      } else if(action==='in'||action==='out') this.zoom(action==='in'?1.2:1/1.2);
      else if(node) this.open(node.dataset.key);
    } else if(event.type==='keydown'&&node&&(event.key==='Enter'||event.key===' ')) {event.preventDefault();this.open(node.dataset.key);}
    else if(event.type==='wheel'&&event.target.closest?.('svg')) {event.preventDefault();this.zoom(Math.exp(-Math.max(-100,Math.min(100,event.deltaY))*.002),this.point(event));}
    else if(event.type==='pointerdown'&&event.button===0&&event.target.closest?.('svg')) {
      if(this.drag)return;
      this.suppressClick=false;
      this.drag={id:event.pointerId,key:node?.dataset.key,start:this.point(event),view:{...this.view},position:node?{...this.positions.get(node.dataset.key)}:null,moved:false};
      this.setPointerCapture?.(event.pointerId);
    } else if(event.type==='pointermove'&&this.drag?.id===event.pointerId) {
      const d=this.drag,p=this.point(event),dx=p.x-d.start.x,dy=p.y-d.start.y;
      if(!d.moved&&Math.hypot(dx,dy)<4)return;
      d.moved=true;event.preventDefault();
      if(d.key) {
        const pos={x:d.position.x+dx/this.view.scale,y:d.position.y+dy/this.view.scale};this.positions.set(d.key,pos);this.pinned.set(d.key,pos);
        const n=this.graph.nodes.find(n=>n.key===d.key);Object.assign(n,pos,{fx:pos.x,fy:pos.y,vx:0,vy:0});
        this.updatePositions();this.reheat(.4);
      } else {this.view.x=d.view.x+dx;this.view.y=d.view.y+dy;this.updateView();}
    } else if(['pointerup','pointercancel','lostpointercapture'].includes(event.type)&&this.drag?.id===event.pointerId) {
      const drag=this.drag;this.suppressClick=drag.moved||Boolean(drag.key);
      if(event.type==='pointerup'&&!drag.moved&&drag.key)this.open(drag.key);
      this.releaseDrag();
    }
  }
}
if(!customElements.get('task-agent-graph'))customElements.define('task-agent-graph',TaskAgentGraph);
