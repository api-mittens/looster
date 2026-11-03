import {renderMarkdown} from './markdown.mjs';
import {serversHTML,behavioursHTML,ruleLabel} from './views.mjs';
import {appendOutput} from './output.mjs';
import {searchItems} from './search.mjs';
import {sortTasksByStatusChanges,taskCardHTML} from './board.mjs';
import './workflow.js';
import './task-graph.js';
const esc = v => String(v ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const stages = ['planning','todo','validated','closed'];
class TaskCard extends HTMLElement {
  set data({task:t,session:s}) {
    this.dataset.id = t.id;
    this.innerHTML = taskCardHTML(t,s);
  }
}
customElements.define('task-card',TaskCard);
class LoosterBoard extends HTMLElement {
  connectedCallback() {
    this.innerHTML = `<header><img class="logo" src="/logo.svg" alt="Looster" width="30" height="30"><strong>looster</strong><span>/ Workspace</span><div class="spacer"></div><button id="help">Shortcuts <kbd>?</kbd></button></header><main><h1>Task list</h1><p>Follow tasks from planning through closure.</p><div class="toolbar"><input type="search" aria-label="Search tasks" placeholder="Search tasks, tags, agents… /"><span id="summary" class="muted"></span><div class="spacer"></div><span id="sync" class="muted"></span><button id="refresh" title="Refresh (r)">Refresh <kbd>r</kbd></button></div><div id="notice" class="notice" role="status"></div><div class="board" aria-label="Task list"></div><div class="footer"><span><kbd>h</kbd> <kbd>l</kbd> columns</span><span><kbd>j</kbd> <kbd>k</kbd> tasks</span><span><kbd>Enter</kbd> details</span><span><kbd>/</kbd> search</span><span><kbd>n</kbd> new task</span><span>Hover or focus to trace a task across columns</span></div></main><dialog aria-labelledby="dialog-title"><div class="dialog-head"><h2 id="dialog-title"></h2><div class="spacer"></div><button id="close">Close <kbd>Esc</kbd></button></div><div class="detail" tabindex="0"></div></dialog>`;
    this.board = this.querySelector('.board'); this.search = this.querySelector('input'); this.dialog = this.querySelector('dialog'); this.panel = this.querySelector('.detail');
    this.insertAdjacentHTML('beforeend',`<dialog id="palette" class="palette" aria-label="Search everything"><div class="palette-search"><input type="search" aria-label="Search everything" placeholder="Search tasks, agents, tags, workflows…" autocomplete="off"><kbd>Esc</kbd></div><div class="palette-results" role="listbox"></div><div class="palette-foot">↑ ↓ select · Enter open</div></dialog><dialog id="create-dialog" aria-labelledby="create-title"><form id="create-form"><div class="dialog-head"><h2 id="create-title"></h2><div class="spacer"></div><button type="button" id="create-close">Close</button></div><div class="form-body"><label>Name <input name="name" required></label><label>Description <textarea name="description" rows="5"></textarea></label><div id="task-fields"><label>Status <select name="status"><option value="planning">Planning</option><option value="todo">Todo</option><option value="validated">Validated</option><option value="closed">Closed</option></select></label><fieldset><legend>Tags</legend><div id="task-tags"></div></fieldset></div><p class="form-error" role="alert"></p><button type="submit">Create</button></div></form></dialog>`);
    this.palette = this.querySelector('#palette'); this.paletteInput = this.palette.querySelector('input'); this.createDialog = this.querySelector('#create-dialog');
    this.querySelector('.toolbar').insertAdjacentHTML('beforeend','<button id="new-task" title="New task (n)">New task <kbd>n</kbd></button>');
    this.querySelector('#new-task').onclick = () => this.openCreate('task');
    this.querySelector('header .spacer').insertAdjacentHTML('afterend','<button id="global-search" title="Search everything (:)">Search <kbd>:</kbd></button>');
    this.querySelector('#global-search').onclick = () => this.openPalette();
    this.querySelector('#create-close').onclick = () => this.createDialog.close();
    this.querySelector('#create-form').onsubmit = e => this.submitCreate(e);
    this.paletteInput.oninput = () => this.renderPalette();
    this.paletteInput.onkeydown = e => this.paletteKey(e);
    this.palette.querySelector('.palette-results').onclick = e => { const row = e.target.closest('[data-index]'); if(row) this.selectPalette(Number(row.dataset.index)); };
    this.querySelector('header > span').outerHTML = '<nav aria-label="Main navigation"><a href="#graph">Graph <kbd>gg</kbd></a><a href="#board">List <kbd>gl</kbd></a><a href="#servers">Servers <kbd>gs</kbd></a><a href="#system">Behaviours <kbd>gb</kbd></a></nav>';
    this.board.insertAdjacentHTML('afterend','<section id="page" hidden></section>');
    const hints = this.querySelector('.footer');
    this.board.before(hints); this.board.id = 'board-columns';
    hints.querySelectorAll('span').forEach(span => span.classList.add('board-hint'));
    hints.insertAdjacentHTML('beforeend','<span class="page-hint" hidden><kbd>j</kbd> <kbd>k</kbd> scroll</span><span class="page-hint" hidden><kbd>G</kbd> bottom</span><div class="spacer"></div><button id="toggle-stages" aria-controls="board-columns" aria-expanded="true">Hide task stages <kbd>s</kbd></button>');
    this.querySelector('#toggle-stages').onclick = () => this.toggleStages();
    this.page = location.hash === '#library' ? 'system' : location.hash.slice(1) || 'graph';
    this.route = () => { this.page = location.hash === '#library' ? 'system' : location.hash.slice(1) || 'graph'; this.goPrefix = 0; this.lastG = 0; this.render(); this.refresh(); };
    window.addEventListener('hashchange',this.route);
    this.querySelector('#page').addEventListener('agent-open',e => this.openAgent(e.detail.name));
    this.querySelector('#page').addEventListener('status-filter',e => { this.graphStatuses = e.detail.statuses; this.renderPage(); });
    this.querySelector('#page').addEventListener('tag-filter',e => { this.graphTags = e.detail.tags; this.renderPage(); });
    this.querySelector('#page').addEventListener('task-open',e => this.open(e.detail.id));
    this.querySelector('#page').addEventListener('change',e => { if(e.target.id === 'manifest-version') { this.manifestID = Number(e.target.value); this.renderPage(); } });
    this.querySelector('#page').addEventListener('click',e => { if(e.target.closest('#activate-manifest')) this.activateManifest(); });
    this.panel.onchange = e => { if(e.target.id === 'log-agent') { this.logAgent = e.target.value; this.paintOutput(); } else if(e.target.id === 'task-status') this.changeStatus(e.target); };
    this.panel.onsubmit = async e => {
      if(e.target.id !== 'comment-form') return;
      e.preventDefault(); const generation=this.generation, form=e.target, button=form.querySelector('button'); button.disabled=true;
      try { await this.post(`/api/tasks/TSK-${this.detailID}/comments`,{body:form.elements.body.value}); form.reset(); form.querySelector('.form-error').textContent=''; await this.loadDetail(this.detailID); }
      catch(err) { if(generation === this.generation && err.name !== 'AbortError') form.querySelector('.form-error').textContent=err.message; }
      finally { button.disabled=false; }
    };
    this.search.oninput = () => this.render();
    this.querySelector('#refresh').onclick = () => this.refresh();
    this.querySelector('#close').onclick = () => this.dialog.close();
    this.querySelector('#help').onclick = () => this.showHelp();
    this.dialog.onclose = () => { this.detailID = null; if(this.page === 'board') this.focus(); else if(this.returnFocus?.isConnected) this.returnFocus.focus(); };
    this.board.onclick = e => { const card = e.target.closest('task-card'); if (card) this.open(Number(card.dataset.id),card.dataset.key.startsWith('agent:') ? card.dataset.key.slice(6).split('/')[0] : ''); };
    this.board.onpointerover = e => this.highlight(e.target.closest('task-card')?.dataset.id);
    this.board.onpointerleave = () => this.highlight(document.activeElement.closest('task-card')?.dataset.id);
    this.board.addEventListener('focusin',e => { const c = e.target.closest('task-card'); if (c) { this.selected = c.dataset.key; this.highlight(c.dataset.id); this.roving(c); } });
    this.keyboard = e => this.key(e); document.addEventListener('keydown',this.keyboard);
    this.namespace = ''; this.generation = 0; this.controller = new AbortController();
    this.querySelector('header strong').insertAdjacentHTML('afterend','<select id="namespace" aria-label="Namespace"><option value="">Default namespace</option></select>');
    this.querySelector('#namespace').onchange = e => this.switchNamespace(e.target.value);
    const header=this.querySelector('header'), brand=document.createElement('div'), actions=document.createElement('div');
    brand.className='header-brand'; actions.className='header-actions';
    brand.append(header.querySelector('.logo'),header.querySelector('strong'),this.querySelector('#namespace'));
    actions.append(this.querySelector('#global-search'),this.querySelector('#help'));
    header.replaceChildren(brand,header.querySelector('nav'),actions);
    this.loadNamespaces();
    this.refresh(); this.timer = setInterval(() => this.refresh(),5000);
  }
  disconnectedCallback() { this.controller?.abort(); window.removeEventListener('hashchange',this.route); clearInterval(this.timer); document.removeEventListener('keydown',this.keyboard); }
  async loadNamespaces() {
    try {
      const response = await fetch('/api/namespaces',{signal:this.controller.signal});
      if(!response.ok) throw Error('Namespace list unavailable');
      const names = await response.json(), selected = response.headers.get('X-Default-Namespace');
      this.querySelector('#namespace').innerHTML = `<option value="">${esc(selected || 'Default')} namespace</option>`+names.filter(n => n !== selected).map(n => `<option value="${esc(n)}">${esc(n)} namespace</option>`).join('');
    } catch(e) { this.querySelector('#notice').textContent = 'Namespace list unavailable'; }
  }
  async api(path, data) {
    const generation = this.generation;
    const url = new URL(path, location.origin);
    if(this.namespace) url.searchParams.set('namespace',this.namespace);
    const options = {signal:this.controller.signal};
    if(data !== undefined) Object.assign(options,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(data)});
    const response = await fetch(url,options);
    if(!response.ok) throw Error(data === undefined ? 'Could not load data. Retrying automatically.' : (await response.text()).trim() || 'Could not save');
    const value = await response.json();
    if(generation !== this.generation) throw new DOMException('Namespace changed','AbortError');
    return value;
  }
  post(path, data) { return this.api(path,data); }
  switchNamespace(namespace) {
    this.controller.abort(); this.controller = new AbortController(); this.generation++;
    this.namespace = namespace; this.goPrefix = 0;
    for(const dialog of this.querySelectorAll('dialog')) if(dialog.open) dialog.close();
    this.detailID = null; this.outputs = new Map(); this.comments = new Map(); this.detailQueued = false;
    this.cursor = 0; this.commentCursor = 0; this.logAgent = ''; this.selected = null;
    this.data = null; this.status = null; this.system = null; this.library = null; this.manifests = null; this.manifestID = null;
    this.graphTags = [];
    this.graphStatuses = undefined;
    this.pageHTML = null; this.paletteItems = []; this.panel.replaceChildren(); this.board.replaceChildren();
    this.querySelector('#page').replaceChildren(); this.search.value = ''; this.paletteInput.value = '';
    this.querySelector('#create-form').reset(); this.querySelector('#task-tags').replaceChildren();
    this.querySelector('#summary').textContent = ''; this.querySelector('#sync').textContent = '';
    this.palette.querySelector('.palette-results').replaceChildren(); this.returnFocus = null; this.paletteReturnFocus = null; this.createReturnFocus = null;
    this.querySelector('#notice').textContent = 'Loading namespace…'; this.refresh();
  }
  async activateManifest() {
    const button = this.querySelector('#activate-manifest'); if(!button) return;
    button.disabled = true;
    try { await this.post(`/api/manifests/${this.manifestID}/activate`,{}); await this.refresh(); }
    catch(e) { if(e.name !== 'AbortError') this.querySelector('#notice').textContent = e.message; }
    finally { if(button.isConnected) button.disabled = false; }
  }
  async refresh() {
    if (this.loading) { this.refreshQueued = true; return; } this.loading = true;
    const generation = this.generation;
    try {
      const requests = [['board','/api/board'],['status','/api/status'],['system','/api/system']];
      requests.push(['library','/api/library'],['manifests','/api/manifests']);
      const results = await Promise.allSettled(requests.map(([,path]) => this.api(path)));
      if(generation !== this.generation) return;
      const failures=[];
      for(let i=0;i<requests.length;i++) {
        const [name] = requests[i], result=results[i];
        if(result.status==='fulfilled') this[name === 'board' ? 'data' : name]=result.value;
        else failures.push(name+' data unavailable');
      }
      if(this.data) {for(const k of ['tasks','agents','sessions']) this.data[k] ||= [];this.render();}
      if(this.palette.open) this.renderPalette();
      this.querySelector('#notice').textContent = failures.join(' · ');
      this.querySelector('#sync').textContent = 'Updated ' + new Date().toLocaleTimeString();
      if (this.detailID) await this.loadDetail(this.detailID);
    }
    catch (e) { if(generation === this.generation) this.querySelector('#notice').textContent = e.message; }
    finally { this.loading = false; if(this.refreshQueued) { this.refreshQueued=false; queueMicrotask(() => this.refresh()); } }
  }
  render() {
    if (!this.data) return;
    this.querySelectorAll('nav a').forEach(a => { if(a.hash === '#'+this.page) a.setAttribute('aria-current','page'); else a.removeAttribute('aria-current'); });
    const other = ['graph','servers','system'].includes(this.page);
    this.board.hidden = other; this.querySelector('#page').hidden = !other;
    this.querySelector('.toolbar').hidden = other && this.page !== 'graph';
    this.querySelectorAll('.board-hint').forEach(hint => hint.hidden = other);
    this.querySelectorAll('.page-hint').forEach(hint => hint.hidden = !other || this.page === 'graph');
    this.querySelector('.footer').hidden = this.page === 'graph';
    this.querySelector('#toggle-stages').hidden = other;
    this.querySelector('h1').textContent = this.page === 'graph' ? 'Task graph' : this.page === 'servers' ? 'Servers & agents' : this.page === 'system' ? 'Behaviours' : 'Task list';
    this.querySelector('main > p').textContent = this.page === 'graph' ? 'Explore tasks by status, tags and search. Planning and todo are shown by default. Animated links show active work, quiet links show past sessions.' : this.page === 'servers' ? 'Live workers, agent coverage, and interrupted sessions.' : this.page === 'system' ? 'How work moves through the factory.' : 'Follow tasks from planning through closure.';
    if(other) { this.renderPage(); return; }
    const focused = this.board.contains(document.activeElement), q = this.search.value.toLowerCase().trim();
    const tasks = sortTasksByStatusChanges(this.data.tasks.filter(t => [t.name,t.description,t.status,`TSK-${t.id}`,...(t.tags || []),...this.data.sessions.filter(s => s.task_id === t.id).map(s => s.agent)].join(' ').toLowerCase().includes(q)));
    const names = [...new Set([...this.data.agents.map(a => a.name),...this.data.sessions.map(s => s.agent)])].sort();
    const cols = [...(this.stagesCollapsed ? [] : stages.map(s => ({key:s,label:s[0].toUpperCase()+s.slice(1),rows:tasks.filter(t => t.status === s).map(task => ({task}))}))),...names.map(n => ({key:'agent:'+n,label:'@ '+n,agent:true,online:(this.status?.workers || []).some(w => w.agents.includes(n)),rows:tasks.flatMap(task => this.data.sessions.filter(s => s.task_id === task.id && s.agent === n && s.status === 'running').map(session => ({task,session})))}))];
    this.querySelector('#summary').textContent = `${tasks.length} tasks · ${this.data.sessions.filter(s => s.status === 'running').length} running`;
    this.board.replaceChildren();
    for (const col of cols) {
      const section = document.createElement('section'); section.className = 'column'+(col.agent ? ' agent' : ''); section.innerHTML = `<h2><span class="dot ${col.agent && !col.online ? 'offline' : ''}"></span>${esc(col.label)}${col.agent ? `<span class="muted">${col.online ? 'online' : 'offline'}</span>` : ''}<span class="count">${col.rows.length}</span></h2><div class="cards"></div>`;
      for (const row of col.rows) { const card = document.createElement('task-card'); card.data = row; card.dataset.key = col.key+'/'+row.task.id; section.querySelector('.cards').append(card); }
      if (!col.rows.length) section.querySelector('.cards').innerHTML = '<div class="empty">No tasks here</div>';
      this.board.append(section);
    }
    const cards = [...this.board.querySelectorAll('task-card')], current = cards.find(c => c.dataset.key === this.selected) || cards[0];
    this.selected = current?.dataset.key; this.roving(current); if (focused) this.focus(); else this.highlight(this.board.querySelector('task-card:hover')?.dataset.id);
  }
  roving(current) { this.board.querySelectorAll('task-card').forEach(c => c.querySelector('button').tabIndex = c === current ? 0 : -1); }
  toggleStages() {
    this.stagesCollapsed = !this.stagesCollapsed;
    const button = this.querySelector('#toggle-stages'); button.setAttribute('aria-expanded',String(!this.stagesCollapsed));
    button.innerHTML = (this.stagesCollapsed ? 'Show' : 'Hide')+' task stages <kbd>s</kbd>'; this.render();
  }
  focus() { const c = [...this.board.querySelectorAll('task-card')].find(c => c.dataset.key === this.selected); c?.querySelector('button').focus({preventScroll:true}); c?.scrollIntoView({block:'nearest',inline:'nearest'}); }
  highlight(id) { this.board.querySelectorAll('task-card').forEach(c => c.classList.toggle('linked',!!id && c.dataset.id === id)); }
  showHelp() { this.detailID = null; this.lastG = 0; this.querySelector('#dialog-title').textContent = 'Keyboard shortcuts'; this.panel.innerHTML = '<p><kbd>h</kbd> / <kbd>l</kbd> or left / right: list columns</p><p><kbd>j</kbd> / <kbd>k</kbd> or down / up: tasks; scroll pages and dialogs</p><p><kbd>gg</kbd>: Graph · <kbd>gl</kbd>: List · <kbd>gs</kbd>: Servers · <kbd>gb</kbd>: Behaviours</p><p><kbd>G</kbd>: last task / bottom of page; <kbd>gg</kbd> / <kbd>G</kbd> inside dialogs: top / bottom</p><p><kbd>Enter</kbd>: open task</p><p><kbd>n</kbd>: new task on List or Graph</p><p><kbd>:</kbd>: search everything · <kbd>/</kbd>: filter list or graph · <kbd>r</kbd>: refresh · <kbd>?</kbd>: help</p><p><kbd>Esc</kbd>: close details or clear search</p><p><kbd>Tab</kbd>: move through controls</p>'; this.dialog.showModal(); }
  async open(id,agent = '') { this.returnFocus = document.activeElement; this.detailID = id; this.lastG = 0; this.cursor = 0; this.commentCursor = 0; this.comments = new Map(); this.outputs = new Map(); this.logAgent = agent; this.querySelector('#dialog-title').textContent = 'Loading…'; this.panel.textContent = ''; this.dialog.showModal(); await this.loadDetail(id); }
  async loadDetail(id) {
    const generation = this.outputs;
    if (this.detailBusy === generation) { this.detailQueued = true; return; }
    this.detailBusy = generation;
    try {
      const d = await this.api(`/api/tasks/TSK-${id}`);
      if (this.outputs !== generation || this.detailID !== id || !this.dialog.open) return;
      this.querySelector('#dialog-title').textContent = `TSK-${id} · ${d.task.name}`;
      if (!this.panel.querySelector('#session-output')) this.panel.innerHTML = '<div class="task-status-control"><label for="task-status">Status</label><select id="task-status" aria-label="Task status"><option value="planning">Planning</option><option value="todo">Todo</option><option value="validated">Validated</option><option value="closed">Closed</option></select><span id="status-error" class="form-error" role="alert"></span></div><div class="description markdown-body" id="task-description"></div><div id="tag-context"></div><h3>Comments</h3><div id="task-comments"></div><form id="comment-form"><label>Add comment <textarea name="body" rows="3" required></textarea></label><p class="form-error" role="alert"></p><button type="submit">Post comment</button></form><h3>Sessions</h3><div id="sessions"></div><div class="output-heading"><h3>Session output</h3><label>Agent <select id="log-agent" aria-label="Session output agent"></select></label></div><pre id="session-output" class="session-output" tabindex="0" aria-label="Session output"></pre><p id="output-note" class="muted" role="status"></p>';
      this.panel.querySelector('#task-status').value = d.task.status;
      this.panel.querySelector('#task-description').innerHTML = renderMarkdown(d.task.description || 'No description.');
      const contextHTML = (d.context?.tags || []).map(t => `<details class="context-card"><summary>Tag: ${esc(t.name)} · ${(t.memories || []).length} memories</summary><pre>${esc(t.description)}</pre>${this.memoryHTML(t.memories)}</details>`).join('');
      if(this.panel.querySelector('#tag-context').innerHTML !== contextHTML) this.panel.querySelector('#tag-context').innerHTML = contextHTML;
      this.panel.querySelector('#sessions').innerHTML = (d.sessions || []).map(s => `<div class="session"><strong>${esc(s.agent)}</strong><span class="badge ${s.status === 'running' ? 'running' : ''}">${esc(s.status)}</span><code>${esc(s.session_id || 'Not started')}</code></div>`).join('') || '<p>No sessions yet.</p>';
      for (const s of d.sessions || []) if (!this.outputs.has(s.agent)) this.outputs.set(s.agent,{text:''});
      for(let page=0;page<10;page++) {
        const comments = await this.api(`/api/tasks/TSK-${id}/events?kind=comment&after=${this.commentCursor}`) || [];
        if(this.outputs !== generation || this.detailID !== id || !this.dialog.open) return;
        for(const comment of comments) {this.comments.set(comment.id,comment);this.commentCursor=Math.max(this.commentCursor,comment.id);}
        if(comments.length<200) break;
      }
      const commentHTML = [...this.comments.values()].map(c => `<article class="comment"><span class="muted">${esc(c.actor_agent || c.agent || 'human')} · ${esc(new Date(c.created_at).toLocaleString())}</span><div class="description markdown-body">${renderMarkdown(c.body)}</div></article>`).join('') || '<p class="muted">No comments yet.</p>';
      if(this.panel.querySelector('#task-comments').innerHTML !== commentHTML) this.panel.querySelector('#task-comments').innerHTML = commentHTML;
      let pages = 0, more = true;
      while (more && pages++ < 10) {
        const events = await this.api(`/api/tasks/TSK-${id}/events?after=${this.cursor}`) || [];
        if (this.outputs !== generation || this.detailID !== id || !this.dialog.open) return;
        for (const event of events) {
          if (event.agent && event.kind === 'agent_output') { const state = this.outputs.get(event.agent) || {text:''}; appendOutput(state,event.body); this.outputs.set(event.agent,state); }
          this.cursor = Math.max(this.cursor,event.id);
        }
        more = events.length === 200;
      }
      const select = this.panel.querySelector('#log-agent'), agents = [...this.outputs.keys()].sort();
      const options = agents.map(a => `<option value="${esc(a)}">${esc(a)}</option>`).join('');
      if (select.innerHTML !== options) select.innerHTML = options;
      if (!agents.includes(this.logAgent)) this.logAgent = agents[0] || '';
      select.value = this.logAgent; select.disabled = !agents.length;
      this.paintOutput(); this.panel.querySelector('#output-note').textContent = more ? 'Loading output in batches…' : 'Updates every 5 seconds. Scroll up to pause following output.';
    } catch(e) { if (this.outputs === generation && this.detailID === id) { const note = this.panel.querySelector('#output-note'); if(note) note.textContent = e.message; else this.panel.textContent = e.message; } }
    finally { if(this.detailBusy === generation) { this.detailBusy = null; if(this.detailQueued) { this.detailQueued=false; queueMicrotask(() => this.loadDetail(id)); } } }
  }
  async changeStatus(select) {
    const id = this.detailID, previous = this.data?.tasks?.find(t => t.id === id)?.status || select.dataset.previous || 'planning';
    select.disabled = true; this.panel.querySelector('#status-error').textContent = '';
    try { await this.post(`/api/tasks/TSK-${id}/status`,{status:select.value}); select.dataset.previous = select.value; await this.refresh(); }
    catch(err) { if(err.name !== 'AbortError' && select.isConnected) { select.value = previous; this.panel.querySelector('#status-error').textContent = err.message; } }
    finally { select.disabled = false; }
  }
  paintOutput() {
    const box = this.panel.querySelector('#session-output'); if (!box) return;
    const follow = box.dataset.agent !== this.logAgent || box.scrollHeight-box.scrollTop-box.clientHeight < 40;
    const text = this.outputs.get(this.logAgent)?.text || 'No output yet.';
    if (box.textContent !== text) box.textContent = text;
    box.dataset.agent = this.logAgent; if(follow) box.scrollTop = box.scrollHeight;
  }
  renderPage() {
    const panel = this.querySelector('#page');
    if(this.page === 'graph') {
      let graph = panel.querySelector('task-agent-graph');
      if(!graph) { panel.innerHTML = '<task-agent-graph></task-agent-graph>'; graph = panel.querySelector('task-agent-graph'); }
      this.pageHTML = null;
      const q = this.search.value.toLowerCase().trim();
      const selectedStatuses = this.graphStatuses ?? ['planning','todo'];
      const selectedTags = this.graphTags || [];
      const tagOptions = [...new Set([...(this.data?.tasks || []).filter(t => selectedStatuses.includes(t.status)).flatMap(t => t.tags || []),...selectedTags])].sort();
      const tasks = (this.data?.tasks || []).filter(t => selectedStatuses.includes(t.status)).filter(t => !selectedTags.length || selectedTags.some(tag => (t.tags || []).includes(tag))).filter(t => [t.name,t.description,t.status,`TSK-${t.id}`,...(t.tags || []),...(this.data?.sessions || []).filter(s => s.task_id === t.id).map(s => s.agent)].join(' ').toLowerCase().includes(q));
      const taskIDs = new Set(tasks.map(t => t.id));
      const sessions = (this.data?.sessions || []).filter(s => taskIDs.has(s.task_id));
      graph.data = {tasks,tagOptions,selectedTags,selectedStatuses,agents:this.data?.agents || [],sessions,workers:this.status?.workers || []};
      this.querySelector('#summary').textContent = `${tasks.length} tasks · ${sessions.filter(s => s.status === 'running').length} running`;
      return;
    }
    this.manifestID = (this.manifests || []).some(m => m.id === this.manifestID) ? this.manifestID : (this.manifests || []).find(m => m.active)?.id || this.manifests?.[0]?.id;
    const html = this.page === 'servers' ? serversHTML(this.status) : behavioursHTML(this.system,this.library,m => this.memoryHTML(m),this.manifests,this.manifestID);
    if(this.pageHTML !== html) {
      const expanded = new Set([...panel.querySelectorAll('details[open][data-section]')].map(d => d.dataset.section));
      const focused = document.activeElement;
      const agent = focused?.dataset.agent, task = focused?.dataset.task;
      panel.innerHTML = html; this.pageHTML = html;
      panel.querySelectorAll('[data-section]').forEach(d => d.open = expanded.has(d.dataset.section));
      panel.querySelectorAll('[data-task]').forEach(a => a.onclick = () => this.open(Number(a.dataset.task)));
      panel.querySelectorAll('[data-agent]').forEach(a => a.onclick = () => this.openAgent(a.dataset.agent));
      if(this.page === 'system') panel.querySelector('workflow-graph').rules = this.system?.behaviours || [];
      if(agent) panel.querySelector('[data-agent="'+CSS.escape(agent)+'"]')?.focus();
      else if(task) panel.querySelector('[data-task="'+CSS.escape(task)+'"]')?.focus();
    }
  }
  openAgent(name) {
    const agent = this.library?.agents?.find(a => a.name === name);
    this.returnFocus = document.activeElement; this.detailID = null; this.lastG = 0;
    this.querySelector('#dialog-title').textContent = '@ '+name;
    this.panel.innerHTML = '<h3>Prompt</h3><div class="description"></div><h3>Memories</h3>'+this.memoryHTML(agent?.memories);
    this.panel.querySelector('.description').textContent = agent ? agent.description || 'No prompt.' : 'Agent context is unavailable. Refresh and try again.';
    this.dialog.showModal();
  }
  memoryHTML(memories) {
    return (memories || []).map(m => `<div class="memory"><span class="muted">MEM-${m.id}${m.created_at ? ' · '+esc(new Date(m.created_at).toLocaleString()) : ''}</span><pre>${esc(m.body)}</pre></div>`).join('') || '<p class="muted">No memories yet.</p>';
  }
  searchIndex() {
    const items = [];
    for(const t of this.data?.tasks || []) items.push({kind:'Task',label:`TSK-${t.id} · ${t.name}`,detail:t.description || t.status,search:`TSK-${t.id} ${t.name} ${t.description} ${t.status} ${(t.tags || []).join(' ')}`,open:() => this.open(t.id)});
    for(const a of this.library?.agents || this.data?.agents || []) items.push({kind:'Agent',label:`@ ${a.name}`,detail:a.description || '',search:`agent ${a.name} ${a.description || ''} ${(a.memories || []).map(m => m.body).join(' ')}`,open:() => this.openAgent(a.name)});
    for(const t of this.library?.tags || []) items.push({kind:'Tag',label:`# ${t.name}`,detail:t.description || '',search:`tag ${t.name} ${t.description || ''} ${(t.memories || []).map(m => m.body).join(' ')}`,open:() => { location.hash = 'system'; setTimeout(() => this.querySelector(`[data-tag="${CSS.escape(t.name)}"]`)?.scrollIntoView({block:'center'}),0); }});
    for(const r of (this.system?.behaviours || []).filter(r => !['prompt','agent','tag'].includes(r.trigger))) items.push({kind:'Workflow',label:ruleLabel(r),detail:r.prompt || '',search:`workflow ${ruleLabel(r)} ${r.trigger.startsWith('stop_') ? 'verified stop pinging' : ''} ${r.source || ''} ${r.prompt || ''}`,open:() => { location.hash = 'system'; setTimeout(() => this.querySelector('workflow-graph')?.scrollIntoView({block:'center'}),0); }});
    for(const w of this.status?.workers || []) items.push({kind:'Server',label:w.id,detail:(w.agents || []).join(', '),search:`server ${w.id} ${(w.agents || []).join(' ')}`,open:() => { location.hash = 'servers'; }});
    for(const s of this.data?.sessions || []) items.push({kind:'Session',label:`TSK-${s.task_id} · ${s.agent}`,detail:s.status,search:`session ${s.task_id} ${s.agent} ${s.status} ${s.session_id || ''}`,open:() => this.open(s.task_id,s.agent)});
    items.unshift({kind:'Action',label:'Create task',detail:'Add a new task',search:'new create add task',open:() => this.openCreate('task')});
    return items;
  }
  openPalette() { this.paletteReturnFocus = document.activeElement; this.paletteInput.value=''; this.paletteSelection=0; this.palette.showModal(); this.renderPalette(); this.paletteInput.focus(); }
  renderPalette() {
    this.paletteItems = searchItems(this.searchIndex(),this.paletteInput.value).slice(0,50);
    this.paletteSelection = Math.min(this.paletteSelection || 0,Math.max(0,this.paletteItems.length-1));
    this.palette.querySelector('.palette-results').innerHTML = this.paletteItems.map((item,i) => `<button type="button" role="option" aria-selected="${i === this.paletteSelection}" data-index="${i}"><span class="badge">${esc(item.kind)}</span><span><strong>${esc(item.label)}</strong><small>${esc(item.detail)}</small></span></button>`).join('') || '<p class="muted">No matches</p>';
  }
  paletteKey(e) {
    if(e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); this.paletteSelection = Math.max(0,Math.min(this.paletteItems.length-1,this.paletteSelection+(e.key === 'ArrowDown' ? 1 : -1))); this.renderPalette(); this.palette.querySelector('[aria-selected="true"]')?.scrollIntoView({block:'nearest'}); }
    if(e.key === 'Enter') { e.preventDefault(); this.selectPalette(this.paletteSelection); }
  }
  selectPalette(index) { const item=this.paletteItems[index]; if(!item) return; this.palette.close(); if(this.dialog.open) this.dialog.close(); item.open(); }
  openCreate(kind) {
    if(kind !== 'task') return;
    if(this.dialog.open) this.dialog.close();
    this.createKind=kind; this.createReturnFocus=document.activeElement;
    const form=this.querySelector('#create-form'); form.reset(); form.querySelector('.form-error').textContent='';
    this.querySelector('#create-title').textContent=kind === 'task' ? 'New task' : 'New tag';
    this.querySelector('#task-fields').hidden=kind !== 'task';
    this.querySelector('#task-tags').innerHTML=(this.library?.tags || []).map(t => `<label class="tag-choice"><input type="checkbox" name="tag" value="${esc(t.name)}"> ${esc(t.name)}</label>`).join('') || '<span class="muted">No tags yet.</span>';
    this.createDialog.showModal(); form.elements.name.focus();
  }
  async submitCreate(e) {
    e.preventDefault(); const generation=this.generation, form=e.currentTarget, button=form.querySelector('[type="submit"]'); button.disabled=true;
    try {
      const name=form.elements.name.value.trim(), description=form.elements.description.value;
      if(this.createKind === 'task') {
        const tags=[...form.querySelectorAll('[name="tag"]:checked')].map(c => c.value);
        const created=await this.post('/api/tasks',{name,description,status:form.elements.status.value,tags});
        this.createDialog.close(); location.hash='board'; await this.refresh(); if(generation === this.generation) this.open(created.id);
      }
    } catch(err) { if(generation === this.generation && err.name !== 'AbortError') form.querySelector('.form-error').textContent=err.message; }
    finally { button.disabled=false; }
  }
  scrollMotion(e,box) {
    if (!['j','k','g','G'].includes(e.key)) return false;
    e.preventDefault();
    if (e.key === 'g') {
      if (this.lastG && Date.now()-this.lastG < 700) { box.scrollTo({top:0}); this.lastG = 0; }
      else this.lastG = Date.now();
    } else {
      this.lastG = 0;
      if (e.key === 'G') box.scrollTo({top:box.scrollHeight});
      else box.scrollBy({top:e.key === 'j' ? 90 : -90});
    }
    return true;
  }
  key(e) {
    if (e.ctrlKey || e.metaKey || e.altKey || e.isComposing) return;
    if(this.palette.open || this.createDialog.open) { this.goPrefix = 0; return; }
    if(this.dialog.open || e.target.matches('input,textarea,select,[contenteditable]')) this.goPrefix = 0;
    if(e.key === ':' && !e.target.matches('input,textarea,select,[contenteditable]')) { e.preventDefault(); this.openPalette(); return; }
    if (this.dialog.open) { if(e.target.matches('select,input,textarea,[contenteditable]')) return; const box = e.target.closest('#session-output') || this.panel; this.scrollMotion(e,box); return; }
    if (e.target.matches('select')) return;
    if (e.target.matches('input,textarea,[contenteditable]')) { if (e.key === 'Escape') { e.preventDefault(); this.search.value = ''; this.render(); this.focus(); } if (e.key === 'Enter') { e.preventDefault(); this.focus(); } return; }
    if(e.repeat && (e.key === 'g' || this.goPrefix)) { e.preventDefault(); return; }
    const goPage = {g:'graph',l:'board',s:'servers',b:'system'};
    if(this.goPrefix && Date.now()-this.goPrefix < 700) {
      this.goPrefix = 0;
      if(goPage[e.key]) { e.preventDefault(); this.lastG = 0; location.hash = goPage[e.key]; window.scrollTo({top:0}); this.querySelector(`nav a[href="#${goPage[e.key]}"]`)?.focus({preventScroll:true}); return; }
    } else this.goPrefix = 0;
    if(e.key === 'g') { e.preventDefault(); this.goPrefix = Date.now(); return; }
    if (e.key === '/' && ['board','graph'].includes(this.page)) { e.preventDefault(); this.search.focus(); return; }
    if (e.key === '?') { e.preventDefault(); this.showHelp(); return; }
    if (e.key === 'r') { e.preventDefault(); this.refresh(); return; }
    if(e.key === 'n' && ['board','graph'].includes(this.page)) { e.preventDefault(); this.openCreate('task'); return; }
    if (e.key === 's' && this.page === 'board') { e.preventDefault(); this.toggleStages(); return; }
    if (e.key === 'Escape') { this.search.value = ''; this.render(); this.focus(); return; }
    if (['graph','servers','system'].includes(this.page)) {
      this.scrollMotion(e,document.scrollingElement || document.documentElement); return;
    }
    const cols = [...this.board.querySelectorAll('.column')].map(c => [...c.querySelectorAll('task-card')]);
    let x = cols.findIndex(c => c.some(t => t.dataset.key === this.selected)); if (x < 0) return;
    let y = cols[x].findIndex(t => t.dataset.key === this.selected);
    const key = ({ArrowLeft:'h',ArrowRight:'l',ArrowDown:'j',ArrowUp:'k'})[e.key] || e.key;
    if (!['h','j','k','l','g','G'].includes(key)) return; e.preventDefault();
    if (key === 'h' || key === 'l') { const step = key === 'h' ? -1 : 1; let n = x+step; while (n >= 0 && n < cols.length && !cols[n].length) n += step; if (n >= 0 && n < cols.length) x = n; }
    if (key === 'j') y++; if (key === 'k') y--; if (key === 'G') y = cols[x].length-1;
    this.lastG = 0;
    y = Math.max(0,Math.min(y,cols[x].length-1)); this.selected = cols[x][y].dataset.key; this.focus();
  }
}
customElements.define('looster-board',LoosterBoard);
