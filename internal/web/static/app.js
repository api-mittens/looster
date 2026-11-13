import {chronologicalEntries,sessionLogHTML} from './session-logs.mjs';
import {appendOutput} from './output.mjs';
import {performanceHTML} from './performance.mjs';
import {loadingHTML} from './loading.mjs';
import {patchHTML} from './refresh.mjs';
import {renderMarkdown} from './markdown.mjs';
import {serversHTML,behavioursHTML,ruleLabel} from './views.mjs';
import {columnPaginationHTML} from './pagination.mjs';
import {searchItems} from './search.mjs';
import {taskCardHTML,sessionCardHTML,boardAgentNames,activeTasklessSessions,isTasklessSession,taskSessionCardHTML} from './board.mjs';
import {attachmentsHTML,attachmentUploadHTML} from './attachments.mjs';
import {runHistoryHTML,runDetailHTML} from './agent-runs.mjs';
import './workflow.js';
import './task-graph.js';
const esc = v => String(v ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const stages = ['planning','todo','validated','closed'];
class TaskCard extends HTMLElement {
  set data({task:t,session:s}) {
    if (t) this.dataset.id = t.id;
    else delete this.dataset.id;
    if (s?.job_id != null) this.dataset.jobId = s.job_id;
    else delete this.dataset.jobId;
    patchHTML(this,t ? taskCardHTML(t,s) : sessionCardHTML(s));
  }
}
customElements.define('task-card',TaskCard);
class LoosterBoard extends HTMLElement {
  stagesCollapsed = true;
  offlineAgentsCollapsed = true;
  connectedCallback() {
    this.innerHTML = `<header><img class="logo" src="/logo.svg" alt="Looster" width="30" height="30"><strong>looster</strong><span>/ Workspace</span><div class="spacer"></div><button id="help">Shortcuts <kbd>?</kbd></button></header><main><h1>Task list</h1><p>Follow tasks from planning through closure.</p><div class="toolbar"><input type="search" aria-label="Search tasks" placeholder="Search tasks, tags, agents… /"><span id="summary" class="muted"></span><div class="spacer"></div><span id="sync" class="muted"></span><button id="refresh" title="Refresh (r)">Refresh <kbd>r</kbd></button></div><div id="notice" class="notice" role="status"></div><div class="board" aria-label="Task list"></div><div class="footer"><span><kbd>h</kbd> <kbd>l</kbd> columns</span><span><kbd>j</kbd> <kbd>k</kbd> tasks</span><span><kbd>Enter</kbd> details</span><span><kbd>/</kbd> search</span><span><kbd>n</kbd> new task</span><span>Hover or focus to trace a task across columns</span></div></main><dialog aria-labelledby="dialog-title"><div class="dialog-head"><h2 id="dialog-title"></h2><div class="spacer"></div><button id="close">Close <kbd>Esc</kbd></button></div><div class="detail" tabindex="0"></div></dialog>`;
    this.board = this.querySelector('.board'); this.search = this.querySelector('input'); this.dialog = this.querySelector('dialog'); this.panel = this.querySelector('.detail');
    this.insertAdjacentHTML('beforeend',`<dialog id="palette" class="palette" aria-label="Search everything"><div class="palette-search"><input type="search" aria-label="Search everything" placeholder="Search tasks, agents, tags, workflows…" autocomplete="off"><kbd>Esc</kbd></div><div class="palette-results" role="listbox"></div><div class="palette-foot">↑ ↓ select · Enter open</div></dialog><dialog id="create-dialog" aria-labelledby="create-title"><form id="create-form"><div class="dialog-head"><h2 id="create-title"></h2><div class="spacer"></div><button type="button" id="create-close">Close</button></div><div class="form-body"><label>Name <input name="name" required></label><label>Description <textarea name="description" rows="5"></textarea></label>${attachmentUploadHTML()}<div id="task-fields"><label>Status <select name="status"><option value="planning">Planning</option><option value="todo">Todo</option><option value="validated">Validated</option><option value="closed">Closed</option></select></label><fieldset><legend>Tags</legend><div id="task-tags"></div></fieldset></div><p class="form-error" role="alert"></p><button type="submit">Create</button></div></form></dialog>`);
    this.palette = this.querySelector('#palette'); this.paletteInput = this.palette.querySelector('input'); this.createDialog = this.querySelector('#create-dialog');
    this.querySelector('.toolbar').insertAdjacentHTML('beforeend','<button id="new-task" title="New task (n)">New task <kbd>n</kbd></button>');
    this.querySelector('#new-task').onclick = () => this.openCreate('task');
    this.querySelector('header .spacer').insertAdjacentHTML('afterend','<button id="global-search" title="Search everything (:)">Search <kbd>:</kbd></button>');
    this.querySelector('#global-search').onclick = () => this.openPalette();
    this.querySelector('#create-close').onclick = () => this.createDialog.close();
    this.querySelector('#create-form').onsubmit = e => this.submitCreate(e);
    this.paletteInput.oninput = () => { this.renderPalette(); this.loadPaletteTasks(); };
    this.paletteInput.onkeydown = e => this.paletteKey(e);
    this.palette.querySelector('.palette-results').onclick = e => { const row = e.target.closest('[data-index]'); if(row) this.selectPalette(Number(row.dataset.index)); };
    this.querySelector('header > span').outerHTML = '<nav aria-label="Main navigation"><a href="#graph">Graph <kbd>gg</kbd></a><a href="#board">List <kbd>gl</kbd></a><a href="#servers">Servers <kbd>gs</kbd></a><a href="#performance">Performance <kbd>gp</kbd></a><a href="#system">Behaviours <kbd>gb</kbd></a></nav>';
    this.board.insertAdjacentHTML('afterend','<section id="page" hidden></section>');
    const hints = this.querySelector('.footer');
    this.board.before(hints); this.board.id = 'board-columns';
    hints.querySelectorAll('span').forEach(span => span.classList.add('board-hint'));
    hints.insertAdjacentHTML('beforeend','<span class="page-hint" hidden><kbd>j</kbd> <kbd>k</kbd> scroll</span><span class="page-hint" hidden><kbd>G</kbd> bottom</span><div class="spacer"></div><button id="toggle-stages" aria-controls="board-columns" aria-expanded="false">Show task stages <kbd>s</kbd></button>');
    this.querySelector('#toggle-stages').onclick = () => this.toggleStages();
    hints.insertAdjacentHTML('beforeend','<button id="toggle-offline-agents" aria-controls="board-columns" aria-expanded="false">Show offline agents</button>');
    this.querySelector('#toggle-offline-agents').onclick = () => this.toggleOfflineAgents();
    const offlineButton = this.querySelector('#toggle-offline-agents');
    offlineButton.setAttribute('aria-expanded',String(!this.offlineAgentsCollapsed));
    offlineButton.textContent = (this.offlineAgentsCollapsed ? 'Show' : 'Hide')+' offline agents';
    const stagesButton = this.querySelector('#toggle-stages');
    stagesButton.setAttribute('aria-expanded',String(!this.stagesCollapsed));
    stagesButton.innerHTML = (this.stagesCollapsed ? 'Show' : 'Hide')+' task stages <kbd>s</kbd>';
    this.page = location.hash === '#library' ? 'system' : location.hash.slice(1) || 'graph';
    this.route = () => { this.page = location.hash === '#library' ? 'system' : location.hash.slice(1) || 'graph'; this.goPrefix = 0; this.lastG = 0; this.render(); this.refresh(); };
    window.addEventListener('hashchange',this.route);
    this.querySelector('#page').addEventListener('agent-open',e => this.openAgent(e.detail.name));
    this.querySelector('#page').addEventListener('status-filter',e => { this.graphStatuses = e.detail.statuses; this.renderPage(); });
    this.querySelector('#page').addEventListener('tag-filter',e => { this.graphTags = e.detail.tags; this.renderPage(); });
    this.querySelector('#page').addEventListener('task-open',e => this.open(e.detail.id));
    this.querySelector('#page').addEventListener('change',e => { if(e.target.id === 'manifest-version') { this.manifestID = Number(e.target.value); this.renderPage(); } });
    this.querySelector('#page').addEventListener('click',e => { if(e.target.closest('#performance-refresh')) this.refresh(); if(e.target.closest('#activate-manifest')) this.activateManifest(); const run=e.target.closest('[data-run-schedule]'); if(run) this.runSchedule(Number(run.dataset.runSchedule)); });
    this.insertAdjacentHTML('beforeend','<dialog id="session-dialog" aria-labelledby="session-title"><div class="dialog-head"><h2 id="session-title"></h2><div class="spacer"></div><button type="button" id="session-back">Back <kbd>Esc</kbd></button></div><div class="detail" tabindex="0"><section id="task-session-run" aria-live="polite"></section><section aria-labelledby="session-output-title"><h3 id="session-output-title">Session output</h3><pre id="task-session-output" class="session-output" tabindex="0" aria-label="Session output"></pre><p id="session-output-status" class="muted" role="status"></p><button type="button" id="session-output-more" hidden>Load more output</button></section></div></dialog>');
    this.sessionDialog = this.querySelector('#session-dialog');
    this.querySelector('#session-output-more').onclick = () => { if(this.sessionInspection) this.loadSessionOutput(this.sessionInspection); };
    this.querySelector('#session-back').onclick = () => this.sessionDialog.close();
    this.sessionDialog.onclose = () => {
      if(this.sessionDialog.open) return;
      this.sessionInspection = null; this.lastG = 0;
      // Native dialog closing restores focus. A delayed close event must not
      // override a newer user selection outside this inspector.
      const active = document.activeElement;
      if(this.sessionReturnFocus?.isConnected && (!active || active === document.body || this.sessionDialog.contains(active))) this.sessionReturnFocus.focus({preventScroll:true});
    };
    this.panel.onclick = e => { if(e.target.closest('[data-comments-retry]')) { this.loadComments(this.detailID,this.outputs); return; } if(e.target.closest('[data-detail-retry]')) { this.loadDetail(this.detailID); return; } if(e.target.closest('[data-payload-retry]')) { this.loadPayloads(this.detailID,this.outputs); return; } const card = e.target.closest('[data-task-session-job]'); if(card) this.openTaskSession(card.dataset.sessionAgent,card.dataset.taskSessionJob,card); };
    this.panel.onchange = e => { if(e.target.id === 'task-status') this.changeStatus(e.target); };
    this.panel.onsubmit = async e => {
      if(e.target.id !== 'comment-form') return;
      e.preventDefault(); const form=e.target, id=this.detailID;
      if(await this.saveCreation(form,async () => ({taskID:id,eventID:(await this.post(`/api/tasks/TSK-${id}/comments`,{body:form.elements.body.value})).event_id}))) await this.loadDetail(id);
    };
    this.search.oninput = () => this.filterTasks();
    this.querySelector('#notice').onclick = e => { if(e.target.closest('#metadata-retry')) this.refresh(); };
    this.querySelector('#refresh').onclick = () => this.refresh();
    this.querySelector('#close').onclick = () => this.dialog.close();
    this.querySelector('#help').onclick = () => this.showHelp();
    this.dialog.onclose = () => { if(this.dialog.open) return; this.detailController?.abort(); this.agentInspection = null; this.detailID = null; if(this.page === 'board') this.focus(); else if(this.returnFocus?.isConnected) this.returnFocus.focus(); };
    this.board.onclick = e => { const page = e.target.closest('[data-column-page]'); if(page) { this.changeColumnPage(page.dataset.column,Number(page.dataset.columnPage)); return; } const retry = e.target.closest('[data-column-retry]'); if(retry) { this.refresh(); return; } const card = e.target.closest('task-card'); if(card && !card.dataset.id) { const button = card.querySelector('[data-session-agent]'); if(button) this.openAgent(button.dataset.sessionAgent,button.dataset.sessionJob); } else if (card?.dataset.id) this.open(Number(card.dataset.id),card.dataset.key.startsWith('agent:') ? card.dataset.key.slice(6).split('/')[0] : ''); };
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
  disconnectedCallback() { this.detailController?.abort(); this.controller?.abort(); window.removeEventListener('hashchange',this.route); clearInterval(this.timer); document.removeEventListener('keydown',this.keyboard); }
  async loadNamespaces() {
    try {
      const response = await fetch('/api/namespaces',{signal:this.controller.signal});
      if(!response.ok) throw Error('Namespace list unavailable');
      const names = await response.json(), selected = response.headers.get('X-Default-Namespace');
      this.querySelector('#namespace').innerHTML = `<option value="">${esc(selected || 'Default')} namespace</option>`+names.filter(n => n !== selected).map(n => `<option value="${esc(n)}">${esc(n)} namespace</option>`).join('');
    } catch(e) { this.querySelector('#notice').textContent = 'Namespace list unavailable'; }
  }
  async api(path, data, explicitErrors = false, signal = this.controller.signal) {
    const generation = this.generation;
    const url = new URL(path, location.origin);
    if(this.namespace) url.searchParams.set('namespace',this.namespace);
    const options = {signal};
    if(data !== undefined) Object.assign(options,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(data)});
    const response = await fetch(url,options);
    if(!response.ok) { const error = Error(data === undefined && !explicitErrors ? 'Could not load data. Retrying automatically.' : (await response.text()).trim() || 'Could not save'); error.status = response.status; throw error; }
    const value = await response.json();
    if(generation !== this.generation) throw new DOMException('Namespace changed','AbortError');
    return value;
  }
  post(path, data) { return this.api(path,data); }
  switchNamespace(namespace) {
    this.agentInspection = null; this.sessionInspection = null;
    this.detailController?.abort();
    this.controller.abort(); this.controller = new AbortController(); this.generation++;
    this.namespace = namespace; this.columnPages = {}; this.listRevision = (this.listRevision || 0)+1; this.paletteTasks = []; this.listError = ''; this.goPrefix = 0; this.scheduleRuns = {};
    for(const dialog of this.querySelectorAll('dialog')) if(dialog.open) dialog.close();
    this.detailID = null; this.outputs = new Map(); this.comments = new Map(); this.detailQueued = false;
    this.cursor = 0; this.commentCursor = 0; this.logAgent = ''; this.selected = null;
    this.data = null; this.performance = null; this.status = null; this.system = null; this.library = null; this.manifests = null; this.manifestID = null;
    this.graphTags = [];
    this.graphStatuses = undefined;
    this.pageHTML = null; this.paletteItems = []; this.panel.replaceChildren(); this.board.replaceChildren();
    this.querySelector('#page').replaceChildren(); this.search.value = ''; this.paletteInput.value = '';
    const createForm = this.querySelector('#create-form');
    createForm.reset(); delete createForm.creation; delete createForm.saving;
    for(const field of createForm.elements) field.disabled = false;
    this.querySelector('#task-tags').replaceChildren();
    this.querySelector('#summary').textContent = ''; this.querySelector('#sync').textContent = '';
    this.palette.querySelector('.palette-results').replaceChildren(); this.returnFocus = null; this.paletteReturnFocus = null; this.createReturnFocus = null;
    this.querySelector('#notice').textContent = 'Loading namespace…'; this.refresh();
  }
  async runSchedule(id) {
    this.scheduleRuns ||= {};
    if(this.scheduleRuns[id]?.pending) return;
    const generation = this.generation;
    this.scheduleRuns[id] = {pending:true,message:'Queuing run…'};
    this.renderPage();
    try {
      const result = await this.post(`/api/behaviours/${id}/run`,{});
      if(generation !== this.generation) return;
      if(!['queued','already_active'].includes(result.status)) throw Error('Unexpected run result');
      this.scheduleRuns[id] = {message:result.status === 'queued' ? `Queued for ${result.agent_name}.` : `Already active for ${result.agent_name}. No duplicate run queued.`};
    } catch(e) {
      if(generation !== this.generation || e.name === 'AbortError') return;
      let message=e.message;
      try { message=JSON.parse(message).error || message; } catch {}
      this.scheduleRuns[id] = {blocked:true,message:`Blocked: ${message}`};
    }
    if(generation === this.generation) this.renderPage();
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
    const generation = this.generation, route = this.page, revision = this.listRevision || 0;
    const list = route !== 'graph';
    this.listPending = list; this.listError = ''; if(list) this.render();
    try {
      const params = `search=${encodeURIComponent(this.search?.value.trim() || '')}&pages=${encodeURIComponent(JSON.stringify(this.columnPages || {}))}`;
      const requests = [['board',list ? '/api/board/list?'+params : '/api/board']];
      const pending = requests.map(([,path]) => this.api(path));
      this.refreshAuxiliary(generation);
      const results = await Promise.allSettled(pending);
      if(generation !== this.generation) return;
      const failures=[];
      for(let i=0;i<requests.length;i++) {
        const [name] = requests[i], result=results[i];
        if(name === 'board' && (route !== this.page || revision !== (this.listRevision || 0))) continue;
        if(result.status==='fulfilled') {
          this[name === 'board' ? 'data' : name]=result.value;
          if(name === 'board' && list) {
            this.columnPages ||= {};
            for(const column of result.value.columns || []) this.columnPages[column.key] = column.page;
          }
        } else { failures.push(name+' data unavailable'); if(name === 'board') this.listError = 'Cannot load tasks. Select Retry to try again.'; }
      }
      this.listPending = false;
      if(!this.data && this.listError) patchHTML(this.board,'<p role="alert">Cannot load tasks. <button type="button" data-column-retry>Retry</button></p>');
      if(this.data || this.page === 'performance') {if(this.data) for(const k of ['tasks','agents','sessions']) this.data[k] ||= [];this.render();}
      if(this.palette.open) { if(list) void this.loadPaletteTasks(); else this.renderPalette(); }
      this.querySelector('#notice').textContent = failures.join(' · ');
      if(this.listError) patchHTML(this.querySelector('#notice'),'Cannot load tasks. <button type="button" id="metadata-retry">Retry</button>');
      this.querySelector('#sync').textContent = 'Updated ' + new Date().toLocaleTimeString();
      if(this.sessionDialog?.open && this.sessionInspection) {
        const inspection = this.sessionInspection;
        void Promise.all([this.loadAgentRun(inspection,inspection.jobID),this.loadSessionOutput(inspection)]);
      }
      if (this.detailID) void this.loadDetail(this.detailID);
    }
    catch (e) { if(generation === this.generation) this.querySelector('#notice').textContent = e.message; }
    finally { this.loading = false; this.listPending = false; if(this.refreshQueued) { this.refreshQueued=false; queueMicrotask(() => this.refresh()); } }
  }
  refreshAuxiliary(generation) {
    this.auxiliary ||= new Map();
    for(const name of ['status','system','library','manifests',...(this.page === 'performance' ? ['performance'] : [])]) {
      if(this.auxiliary.get(name) === generation) continue;
      this.auxiliary.set(name,generation);
      void this.api(`/api/${name}`).then(value => {
        if(generation !== this.generation) return;
        this[name] = value;
        this.render();
        if(this.palette.open) this.renderPalette();
      }).catch(error => {
        if(generation === this.generation && error.name !== 'AbortError') this.querySelector('#notice').textContent = 'Cannot load all data. Select Refresh to try again.';
      }).finally(() => { if(this.auxiliary.get(name) === generation) this.auxiliary.delete(name); });
    }
  }
  filterTasks() {
    if(this.page === 'graph') { this.render(); return; }
    this.columnPages = {}; this.listRevision = (this.listRevision || 0)+1; this.refresh();
  }
  changeColumnPage(key,page) {
    if(this.listPending || !Number.isInteger(page) || page < 1) return;
    this.columnPages = {...this.columnPages,[key]:page}; this.listRevision = (this.listRevision || 0)+1;
    this.refresh();
  }
  render() {
    if (!this.data && this.page !== 'performance') {
      this.querySelector('#notice').textContent = this.listError || 'Load task data.';
      if(!this.listError) patchHTML(this.board,loadingHTML('Load task data.'));
      return;
    }
    this.querySelectorAll('nav a').forEach(a => { if(a.hash === '#'+this.page) a.setAttribute('aria-current','page'); else a.removeAttribute('aria-current'); });
    const other = ['graph','servers','system','performance'].includes(this.page);
    this.board.hidden = other; this.querySelector('#page').hidden = !other;
    this.querySelector('.toolbar').hidden = other && this.page !== 'graph';
    this.querySelectorAll('.board-hint').forEach(hint => hint.hidden = other);
    this.querySelectorAll('.page-hint').forEach(hint => hint.hidden = !other || this.page === 'graph');
    this.querySelector('.footer').hidden = this.page === 'graph';
    this.querySelector('#toggle-stages').hidden = other;
    this.querySelector('#toggle-offline-agents').hidden = other;
    this.querySelector('h1').textContent = this.page === 'performance' ? 'Performance' : this.page === 'graph' ? 'Task graph' : this.page === 'servers' ? 'Servers & agents' : this.page === 'system' ? 'Behaviours' : 'Task list';
    this.querySelector('main > p').textContent = this.page === 'performance' ? 'Task outcomes and reports in UTC.' : this.page === 'graph' ? 'Explore tasks by status, tags and search. Planning and todo are shown by default. Animated links show active work, quiet links show past sessions.' : this.page === 'servers' ? 'Live workers, agent coverage, and interrupted sessions.' : this.page === 'system' ? 'How work moves through the factory.' : 'Follow tasks from planning through closure.';
    if(other) { this.renderPage(); return; }
    const focused = this.board.contains(document.activeElement);
    const pages = new Map((this.data.columns || []).map(column => [column.key,column]));
    const column = key => pages.get(key) || {key,tasks:[],total:0,page:1,page_size:15};
    const names = boardAgentNames(this.data.agents,this.data.sessions);
    const cols = [...(this.stagesCollapsed ? [] : stages.map(s => ({...column(s),label:s[0].toUpperCase()+s.slice(1),rows:column(s).tasks.map(task => ({task}))}))),...names.map(n => ({...column('agent:'+n),label:'@ '+n,agent:true,online:(this.status?.workers || []).some(w => w.agents.includes(n)),rows:column('agent:'+n).tasks.map(task => ({task,session:this.data.sessions.find(s => s.task_id === task.id && s.agent === n && s.status === 'running')}))}))];
    cols.push({key:'other-sessions',label:'Other sessions',rows:activeTasklessSessions(this.data.sessions).map(session => ({session}))});
    const total = stages.reduce((sum,key) => sum+column(key).total,0);
    this.querySelector('#summary').textContent = `${total} tasks · ${this.data.sessions.filter(s => s.status === 'running').length} running`;
    patchHTML(this.board,cols.filter(col => !col.agent || col.online || !this.offlineAgentsCollapsed).map(col => `<section data-refresh-key="${esc(col.key)}" class="column${col.agent ? ' agent' : ''}"><h2><span class="dot ${col.agent && !col.online ? 'offline' : ''}"></span>${esc(col.label)}${col.agent ? `<span class="muted">${col.online ? 'online' : 'offline'}</span>` : ''}<span class="count">${col.total ?? col.rows.length}</span></h2><div class="cards" aria-busy="${!!this.listPending}">${col.rows.map(row => `<task-card ${row.task ? `data-id="${esc(row.task.id)}"` : ''}${row.session?.job_id != null ? ` data-job-id="${esc(row.session.job_id)}"` : ''} data-key="${esc(col.key+'/'+(row.session?.job_id != null ? 'job:'+row.session.job_id : row.task.id))}">${row.task ? taskCardHTML(row.task,row.session) : sessionCardHTML(row.session)}</task-card>`).join('') || '<div class="empty">No tasks here</div>'}</div>${col.key === 'other-sessions' ? '' : columnPaginationHTML(col,{pending:!!this.listPending,error:this.listError})}</section>`).join(''));
    const cards = [...this.board.querySelectorAll('task-card')], current = cards.find(c => c.dataset.key === this.selected) || cards[0];
    this.selected = current?.dataset.key; this.roving(current); if (focused && !this.board.contains(document.activeElement)) current?.querySelector('button').focus({preventScroll:true}); this.highlight(this.board.querySelector('task-card:hover')?.dataset.id || (focused ? current?.dataset.id : null));
  }
  roving(current) { this.board.querySelectorAll('task-card').forEach(c => c.querySelector('button').tabIndex = c === current ? 0 : -1); }
  toggleStages() {
    this.stagesCollapsed = !this.stagesCollapsed;
    const button = this.querySelector('#toggle-stages'); button.setAttribute('aria-expanded',String(!this.stagesCollapsed));
    button.innerHTML = (this.stagesCollapsed ? 'Show' : 'Hide')+' task stages <kbd>s</kbd>'; this.render();
  }
  toggleOfflineAgents() {
    this.offlineAgentsCollapsed = !this.offlineAgentsCollapsed;
    const button = this.querySelector('#toggle-offline-agents'); button.setAttribute('aria-expanded',String(!this.offlineAgentsCollapsed));
    button.textContent = (this.offlineAgentsCollapsed ? 'Show' : 'Hide')+' offline agents'; this.render();
  }
  focus() { const c = [...this.board.querySelectorAll('task-card')].find(c => c.dataset.key === this.selected); c?.querySelector('button').focus({preventScroll:true}); c?.scrollIntoView({block:'nearest',inline:'nearest'}); }
  highlight(id) { this.board.querySelectorAll('task-card').forEach(c => c.classList.toggle('linked',!!id && c.dataset.id === id)); }
  showHelp() { this.detailController?.abort(); this.agentInspection = null; this.detailID = null; this.lastG = 0; this.querySelector('#dialog-title').textContent = 'Keyboard shortcuts'; this.panel.innerHTML = '<p><kbd>h</kbd> / <kbd>l</kbd> or left / right: list columns</p><p><kbd>j</kbd> / <kbd>k</kbd> or down / up: tasks; scroll pages and dialogs</p><p><kbd>gg</kbd>: Graph · <kbd>gl</kbd>: List · <kbd>gs</kbd>: Servers · <kbd>gb</kbd>: Behaviours · <kbd>gp</kbd>: Performance</p><p><kbd>G</kbd>: last task / bottom of page; <kbd>gg</kbd> / <kbd>G</kbd> inside dialogs: top / bottom</p><p><kbd>Enter</kbd>: open task</p><p><kbd>n</kbd>: new task on List or Graph</p><p><kbd>:</kbd>: search everything · <kbd>/</kbd>: filter list or graph · <kbd>r</kbd>: refresh · <kbd>?</kbd>: help</p><p><kbd>Esc</kbd>: close details or clear search</p><p><kbd>Tab</kbd>: move through controls</p>'; this.dialog.showModal(); }
  async open(id,agent = '') { this.detailController?.abort(); this.detailController = new AbortController(); this.detailQueued = false; this.commentsLoaded = false; this.commentsBusy = null; this.payloadBusy = null; this.payloadData = null; this.agentInspection = null; if(!this.dialog.open) this.returnFocus = document.activeElement; this.detailID = id; this.attachments = []; this.lastG = 0; this.cursor = 0; this.commentCursor = 0; this.comments = new Map(); this.outputs = new Map(); this.logAgent = agent; this.querySelector('#dialog-title').textContent = 'Loading…'; this.panel.innerHTML = '<div id="detail-notice" role="status">'+loadingHTML('Load task data.')+'</div>'; if(!this.dialog.open) this.dialog.showModal(); await this.loadDetail(id); }
  async loadDetail(id) {
    const generation = this.outputs;
    if (this.detailBusy === generation) { this.detailQueued = true; return; }
    this.detailBusy = generation;
    try {
      const d = await this.api(`/api/tasks/TSK-${id}`,undefined,false,this.detailController?.signal);
      if (this.outputs !== generation || this.detailID !== id || !this.dialog.open) return;
      const title = `TSK-${id} · ${d.task.name}`;
      patchHTML(this.querySelector('#dialog-title'),esc(title));
      if (!this.panel.querySelector('#task-status')) this.panel.innerHTML = '<div id="detail-notice" role="status"></div><div id="payload-notice" role="status"></div><div class="task-status-control"><label for="task-status">Status</label><select id="task-status" aria-label="Task status"><option value="planning">Planning</option><option value="todo">Todo</option><option value="validated">Validated</option><option value="closed">Closed</option></select><span id="status-error" class="form-error" role="alert"></span></div><div class="description markdown-body" id="task-description"></div><div id="description-attachments"></div><p id="attachment-notice" class="form-error" role="status"></p><div id="tag-context"></div><h3>Comments</h3><div id="comments-notice" role="status"></div><div id="task-comments"></div><form id="comment-form"><label>Add comment <textarea name="body" rows="3" required aria-describedby="comment-mention-hint"></textarea></label><p id="comment-mention-hint" class="muted">Mention @registered-agent to request a run on this task. Unknown agent names are ignored.</p>'+attachmentUploadHTML()+'<p class="form-error" role="alert"></p><button type="submit">Post comment</button></form><h3>Sessions</h3><div id="sessions"></div>';
      const statusSelect = this.panel.querySelector('#task-status');
      if(!statusSelect.disabled) { statusSelect.value = d.task.status; statusSelect.dataset.previous = d.task.status; }
      this.panel.querySelector('#detail-notice').textContent = '';
      this.detailMetadata = d;
      patchHTML(this.panel.querySelector('#task-description'),renderMarkdown(d.task.description || 'No description.'));
      const contextHTML = (d.context?.tags || []).map(t => `<details class="context-card" data-refresh-key="${esc(t.id ?? t.name)}"><summary>Tag: ${esc(t.name)} · ${(t.memories || []).length} memories</summary><pre>${esc(t.description)}</pre>${this.memoryHTML(t.memories)}</details>`).join('');
      patchHTML(this.panel.querySelector('#tag-context'),contextHTML);
      if(!this.commentsLoaded) patchHTML(this.panel.querySelector('#task-comments'),loadingHTML('Load comments.'));
      patchHTML(this.panel.querySelector('#sessions'),(d.sessions || []).map(session => {
        const saved = this.payloadData?.sessions?.find(item => item.job_id === session.job_id);
        return taskSessionCardHTML(saved ? {...session,summary:saved.summary,short_summary:saved.short_summary,error:saved.error} : session);
      }).join('') || '<p>No sessions yet.</p>');
      if(!this.payloadData) patchHTML(this.panel.querySelector('#payload-notice'),loadingHTML('Load task content.'));
      void this.loadComments(id,generation);
      void this.loadPayloads(id,generation);
      void this.loadAttachments(id,generation);
    } catch(e) {
      if(e.name !== 'AbortError' && this.outputs === generation && this.detailID === id) {
        const note = this.panel.querySelector('#detail-notice');
        if(note) patchHTML(note,'Cannot load task data. <button type="button" data-detail-retry>Retry</button>');
      }
    } finally { if(this.detailBusy === generation) { this.detailBusy = null; if(this.detailQueued) { this.detailQueued=false; queueMicrotask(() => this.loadDetail(id)); } } }
  }
  async loadPayloads(id,generation) {
    if(this.payloadBusy === generation) return;
    this.payloadBusy = generation;
    const current = () => this.outputs === generation && this.detailID === id && this.dialog.open;
    try {
      const payload = await this.api(`/api/tasks/TSK-${id}/payloads?limit=50`,undefined,false,this.detailController?.signal);
      if(!current()) return;
      const d = payload;
      this.payloadData = payload;
      this.panel.querySelector('#payload-notice').textContent = '';
      if(d.task?.description !== undefined) patchHTML(this.panel.querySelector('#task-description'),renderMarkdown(d.task.description || 'No description.'));
      if(d.context) {
        const contextHTML = (d.context.tags || []).map(t => `<details class="context-card" data-refresh-key="${esc(t.id ?? t.name)}"><summary>Tag: ${esc(t.name)} · ${(t.memories || []).length} memories</summary><pre>${esc(t.description)}</pre>${this.memoryHTML(t.memories)}</details>`).join('');
        patchHTML(this.panel.querySelector('#tag-context'),contextHTML);
      }
      if(d.sessions) patchHTML(this.panel.querySelector('#sessions'),d.sessions.map(taskSessionCardHTML).join('') || '<p>No sessions yet.</p>');
      for(const comment of d.comments || []) this.comments.set(comment.id,comment);
      this.renderTaskComments(id);
    } catch(error) {
      if(current() && error.name !== 'AbortError') patchHTML(this.panel.querySelector('#payload-notice'),'Cannot load task content. <button type="button" data-payload-retry>Retry</button>');
    } finally { if(this.payloadBusy === generation) this.payloadBusy = null; }
  }
  async loadComments(id,generation) {
    if(this.commentsBusy === generation) return;
    this.commentsBusy = generation;
    const current = () => this.outputs === generation && this.detailID === id && this.dialog.open;
    try {
      for(let page=0;page<10;page++) {
        const comments = await this.api(`/api/tasks/TSK-${id}/events?kind=comment&after=${this.commentCursor}`,undefined,false,this.detailController?.signal) || [];
        if(!current()) return;
        for(const comment of comments) { this.comments.set(comment.id,comment); this.commentCursor=Math.max(this.commentCursor,comment.id); }
        this.commentsLoaded = true;
        this.renderTaskComments(id);
        if(comments.length<200) break;
      }
      this.panel.querySelector('#comments-notice').textContent = '';
    } catch(error) {
      if(current() && error.name !== 'AbortError') {
        if(!this.commentsLoaded) this.panel.querySelector('#task-comments').replaceChildren();
        patchHTML(this.panel.querySelector('#comments-notice'),'Cannot load comments. <button type="button" data-comments-retry>Retry</button>');
      }
    } finally { if(this.commentsBusy === generation) this.commentsBusy = null; }
  }
  renderTaskComments(id) {
      if(!this.commentsLoaded && !this.payloadData) return;
      const d = this.payloadData || {};
      const commentBox = this.panel.querySelector('#task-comments');
      commentBox.querySelector('.loading-placeholder')?.remove();
      const entries = chronologicalEntries([...this.comments.values()],d.session_logs || d.task?.session_logs || []);
      if(!entries.length && !this.commentsLoaded) { patchHTML(commentBox,loadingHTML('Load comments.')); return; }
      if(entries.length) commentBox.querySelector('.no-comments')?.remove();
      else if(!commentBox.firstChild) commentBox.innerHTML = '<p class="muted no-comments">No comments yet.</p>';
      let entryIndex = 0;
      const placeEntry = article => { const current = commentBox.children[entryIndex++]; if(current !== article) commentBox.insertBefore(article,current || null); };
      for(const c of entries) {
        if(c.entryType === 'log') {
          let logArticle = commentBox.querySelector(`[data-log-id="${Number(c.id)}"]`);
          if(!logArticle) { logArticle = document.createElement('div'); logArticle.dataset.logId = c.id; }
          patchHTML(logArticle,sessionLogHTML(c)); placeEntry(logArticle); continue;
        }
        let article = commentBox.querySelector(`[data-comment-id="${c.id}"]`);
        if(!article) {
          article = document.createElement('article'); article.className = 'comment'; article.dataset.commentId = c.id;
          article.innerHTML = '<div class="comment-content"></div><div class="comment-attachments"></div>';
          commentBox.append(article);
        }
        const content = `<span class="muted">${esc(c.actor_agent || c.agent || 'human')} · ${esc(new Date(c.created_at).toLocaleString())}</span><div class="description markdown-body">${renderMarkdown(c.body)}</div>`;
        patchHTML(article.querySelector('.comment-content'),content);
        const files = attachmentsHTML(this.attachments,id,c.id,this.namespace);
        patchHTML(article.querySelector('.comment-attachments'),files);
        placeEntry(article);
      }
  }
  async loadAttachments(id,generation) {
    const current = () => this.outputs === generation && this.detailID === id && this.dialog.open;
    try {
      const result = await this.api(`/api/tasks/TSK-${id}/attachments`,undefined,true,this.detailController?.signal);
      if(!current()) return;
      this.attachments = result || [];
      this.panel.querySelector('#attachment-notice').textContent = '';
      const html = attachmentsHTML(this.attachments,id,null,this.namespace), box = this.panel.querySelector('#description-attachments');
      patchHTML(box,html);
      // Attachment refreshes also run independently of comment pagination. Reconcile
      // every mounted owner, including empty lists after an attachment is removed.
      for(const article of this.panel.querySelectorAll('[data-comment-id]')) {
        patchHTML(article.querySelector('.comment-attachments'),attachmentsHTML(this.attachments,id,article.dataset.commentId,this.namespace));
      }
    } catch(error) {
      if(current() && error.name !== 'AbortError') this.panel.querySelector('#attachment-notice').textContent = `Could not load attachments: ${error.message}`;
    }
  }
  async saveCreation(form, create) {
    const generation = this.generation, namespace = this.namespace, detailID = this.detailID;
    const button = form.querySelector('[type="submit"]'), result = form.querySelector('.form-error');
    if(button.disabled) return false;
    const current = () => generation === this.generation && (form.id !== 'comment-form' || this.detailID === detailID) && form.isConnected;
    const label = button.textContent;
    button.disabled = true;
    form.saving = true;
    result.textContent = '';
    try {
      if(form.creation && form.creation.generation !== generation) delete form.creation;
      if(!form.creation) {
        const files = [...form.elements.files.files];
        const target = await create();
        if(!current()) return false;
        form.creation = {...target,files,uploaded:0,generation};
      }
      const state = form.creation;
      // Once saved, editing fields would misleadingly imply another creation.
      for(const field of form.elements) if(field !== button && field.type !== 'button') field.disabled = true;
      while(state.uploaded < state.files.length) {
        if(!current()) return false;
        const file = state.files[state.uploaded];
        const body = new FormData(); body.append('file',file);
        if(state.eventID != null) body.append('event_id',state.eventID);
        const url = new URL(`/api/tasks/TSK-${state.taskID}/attachments`,location.origin);
        if(namespace) url.searchParams.set('namespace',namespace);
        const response = await fetch(url,{method:'POST',body,signal:this.controller.signal});
        if(!response.ok) throw Error((await response.text()).trim() || 'Could not upload file');
        state.uploaded++;
        if(!current()) return false;
      }
      form.reset(); delete form.creation;
      for(const field of form.elements) field.disabled = false;
      button.textContent = form.id === 'comment-form' ? 'Post comment' : 'Create';
      return state;
    } catch(error) {
      if(current() && error.name !== 'AbortError') {
        const state = form.creation;
        result.textContent = state ? `Saved. ${state.uploaded} of ${state.files.length} files uploaded. ${error.message} Click Retry remaining files to resume without creating another task or comment.` : error.message;
        button.textContent = state ? 'Retry remaining files' : label;
        if(state?.eventID != null) await this.loadDetail(state.taskID);
      }
      return false;
    } finally {
      if(current()) { button.disabled = false; delete form.saving; }
    }
  }
  async changeStatus(select) {
    const id = this.detailID, previous = select.dataset.previous || this.data?.tasks?.find(t => t.id === id)?.status || 'planning';
    select.disabled = true; this.panel.querySelector('#status-error').textContent = '';
    try { await this.post(`/api/tasks/TSK-${id}/status`,{status:select.value}); select.dataset.previous = select.value; await this.refresh(); }
    catch(err) { if(err.name !== 'AbortError' && select.isConnected) { select.value = previous; this.panel.querySelector('#status-error').textContent = err.message; } }
    finally { select.disabled = false; }
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
      const sessions = (this.data?.sessions || []).filter(s => taskIDs.has(s.task_id) || (isTasklessSession(s) && s.status === 'running'));
      graph.data = {tasks,tagOptions,selectedTags,selectedStatuses,agents:this.data?.agents || [],sessions,workers:this.status?.workers || []};
      this.querySelector('#summary').textContent = `${tasks.length} tasks · ${sessions.filter(s => s.status === 'running').length} running`;
      return;
    }
    this.manifestID = (this.manifests || []).some(m => m.id === this.manifestID) ? this.manifestID : (this.manifests || []).find(m => m.active)?.id || this.manifests?.[0]?.id;
    const html = this.page === 'performance' ? performanceHTML(this.performance) : this.page === 'servers' ? serversHTML(this.status) : behavioursHTML(this.system,this.library,m => this.memoryHTML(m),this.manifests,this.manifestID,this.scheduleRuns);
    if(this.pageHTML !== html) {
      if(panel.dataset.refreshPage !== this.page) { panel.replaceChildren(); panel.dataset.refreshPage = this.page; }
      patchHTML(panel,html); this.pageHTML = html;
      panel.querySelectorAll('[data-task]').forEach(a => a.onclick = () => this.open(Number(a.dataset.task)));
      panel.querySelectorAll('[data-agent]').forEach(a => a.onclick = () => this.openAgent(a.dataset.agent));
    }
    if(this.page === 'system') panel.querySelector('workflow-graph').rules = this.system?.behaviours || [];
  }
  openTaskSession(name,id,card) {
    if(!Number.isSafeInteger(Number(id)) || Number(id) < 1) return;
    this.sessionReturnFocus = card || document.activeElement;
    const inspection = this.sessionInspection = {name,jobID:id,target:this.sessionDialog.querySelector('#task-session-run'),output:{text:''},after:0,status:card?.dataset.sessionStatus};
    this.sessionDialog.querySelector('#task-session-output').textContent = '';
    this.sessionDialog.querySelector('#session-output-status').textContent = 'Loading session output…';
    this.sessionDialog.querySelector('#session-output-more').textContent = 'Load more output';
    this.sessionDialog.querySelector('#session-output-more').hidden = true;
    this.sessionDialog.querySelector('.detail').scrollTop = 0;
    this.querySelector('#session-title').textContent = `@ ${name} · Run ${id}`;
    this.lastG = 0;
    if(!this.sessionDialog.open) this.sessionDialog.showModal();
    this.loadAgentRun(inspection,id);
    this.loadSessionOutput(inspection);
  }
  async loadSessionOutput(inspection) {
    if(inspection.outputBusy || this.sessionInspection !== inspection) return;
    inspection.outputBusy = true;
    const current = () => this.sessionInspection === inspection && this.sessionDialog.open;
    const box = this.sessionDialog.querySelector('#task-session-output'), note = this.sessionDialog.querySelector('#session-output-status'), more = this.sessionDialog.querySelector('#session-output-more');
    try {
      for(let page=0;page<10;page++) {
        const result = await this.api(`/api/agents/${encodeURIComponent(inspection.name)}/runs/${encodeURIComponent(inspection.jobID)}/output?after=${inspection.after}`,undefined,true);
        if(!current()) return;
        const follow = box.scrollHeight-box.scrollTop-box.clientHeight < 40;
        const after = Number(result.next_after);
        if((result.events?.length || result.has_more) && (!Number.isSafeInteger(after) || after <= inspection.after)) throw Error('Invalid output cursor');
        for(const event of result.events || []) appendOutput(inspection.output,event.body || '');
        if(Number.isSafeInteger(after) && after >= inspection.after) inspection.after = after;
        box.textContent = inspection.output.text;
        if(follow) box.scrollTop = box.scrollHeight;
        more.textContent = 'Load more output';
        more.hidden = !result.has_more;
        const status = this.data?.sessions?.find(s => s.agent === inspection.name && String(s.job_id) === String(inspection.jobID))?.status || inspection.status;
        note.textContent = result.has_more ? 'Loading output in batches…' : inspection.output.text ? 'Updates every 5 seconds while this session is open.' : status === 'running' || result.status === 'pending' ? 'Output pending. This run has not produced output yet.' : 'No output recorded for this run. Historical runs from before output attribution may have no correlated output.';
        if(!result.has_more) return;
      }
      note.textContent = 'More output is available. Select Load more output to continue.';
    } catch(error) {
      if(current() && error.name !== 'AbortError') { note.textContent = error.status === 404 ? 'Output missing for this run.' : `Could not load session output: ${error.message}`; more.hidden = false; more.textContent = 'Retry output'; }
    } finally { inspection.outputBusy = false; }
  }
  openAgent(name,jobID) {
    this.detailController?.abort();
    if(this.sessionDialog?.open) this.sessionDialog.close();
    const agent = this.library?.agents?.find(a => a.name === name);
    const inspection = this.agentInspection = {name,jobID,cursors:[0]};
    if(!this.dialog.open) this.returnFocus = document.activeElement; this.detailID = null; this.lastG = 0;
    this.querySelector('#dialog-title').textContent = '@ '+name;
    this.panel.innerHTML = '<h3>Prompt</h3><div class="description"></div><h3>Memories</h3>'+this.memoryHTML(agent?.memories)+'<section id="agent-history" aria-live="polite">Loading run history…</section>';
    this.panel.querySelector('.description').textContent = agent ? agent.description || 'No prompt.' : 'Agent context is unavailable. Refresh and try again.';
    if(!this.dialog.open) this.dialog.showModal();
    this.loadAgentRuns(inspection);
  }
  async loadAgentRuns(inspection,before = 0) {
    const target = this.panel.querySelector('#agent-history');
    const request = inspection.historyRequest = {};
    const restoreFocus = target.contains(document.activeElement);
    target.textContent = 'Loading run history…';
    try {
      const page = await this.api(`/api/agents/${encodeURIComponent(inspection.name)}/runs?limit=20${before ? '&before_job_id='+before : ''}`);
      if(this.agentInspection !== inspection || inspection.historyRequest !== request) return;
      target.innerHTML = runHistoryHTML(page,inspection.cursors.length > 1);
      target.querySelectorAll('[data-run]').forEach(button => button.onclick = () => this.loadAgentRun(inspection,button.dataset.run));
      target.querySelector('[data-run-before]')?.addEventListener('click',() => { inspection.jobID = null; inspection.cursors.push(page.next_before_job_id); this.loadAgentRuns(inspection,page.next_before_job_id); });
      target.querySelector('[data-run-previous]')?.addEventListener('click',() => { inspection.jobID = null; inspection.cursors.pop(); this.loadAgentRuns(inspection,inspection.cursors.at(-1)); });
      if(restoreFocus) target.querySelector('[data-run]')?.focus({preventScroll:true});
      const selected = inspection.jobID || page.runs?.[0]?.job_id;
      if(selected) await this.loadAgentRun(inspection,selected);
    } catch(err) { if(this.agentInspection === inspection && inspection.historyRequest === request) target.textContent = 'Run history unavailable. '+err.message; }
  }
  async loadAgentRun(inspection,id) {
    const target = inspection.target || this.panel.querySelector('#selected-run');
    if(!target || inspection.runPending?.id === String(id) || (inspection.target && this.sessionInspection !== inspection)) return;
    const request = inspection.runRequest = {};
    inspection.runPending = {id:String(id),request};
    inspection.jobID = id;
    if(inspection.loadedJobID !== String(id)) target.textContent = 'Loading run…';
    try {
      const run = await this.api(`/api/agents/${encodeURIComponent(inspection.name)}/runs/${encodeURIComponent(id)}`);
      if((inspection.target ? this.sessionInspection !== inspection : this.agentInspection !== inspection) || inspection.runRequest !== request || !target.isConnected) return;
      if(inspection.target) inspection.status = run.outcome || run.status || inspection.status;
      const detail = inspection.target && this.sessionDialog?.querySelector('.detail'), atTop = detail?.scrollTop === 0;
      const html = runDetailHTML(run,!inspection.target);
      inspection.loadedJobID = String(id);
      if(target.innerHTML === html) return;
      target.innerHTML = html;
      if(atTop) detail.scrollTop = 0;
      target.querySelector('[data-run-task]')?.addEventListener('click',() => { if(inspection.target) { this.sessionDialog.close(); if(this.detailID === run.task_id) return; } this.open(run.task_id); });
    } catch(err) {
      if((inspection.target ? this.sessionInspection !== inspection : this.agentInspection !== inspection) || inspection.runRequest !== request || !target.isConnected) return;
      const live = this.data?.sessions?.find(s => s.agent === inspection.name && String(s.job_id) === String(id) && s.status === 'running');
      target.innerHTML = err.status === 404 && live ? runDetailHTML({...live,outcome:'running',summary_status:'pending',summary:'This run is still running. Its final summary will be available after completion.'},!inspection.target) : '';
      if(!target.innerHTML) target.textContent = 'Run unavailable. '+err.message;
      else target.querySelector('[data-run-task]')?.addEventListener('click',() => { if(inspection.target) { this.sessionDialog.close(); if(this.detailID === live.task_id) return; } this.open(live.task_id); });
    } finally {
      if(inspection.runPending?.request === request) inspection.runPending = null;
    }
    target.querySelector('[data-open-session-output]')?.addEventListener('click',e => this.openTaskSession(inspection.name,id,e.currentTarget));
  }
  memoryHTML(memories) {
    return (memories || []).map(m => `<div class="memory" data-refresh-key="memory:${esc(m.id)}"><span class="muted">MEM-${m.id}${m.created_at ? ' · '+esc(new Date(m.created_at).toLocaleString()) : ''}</span><pre>${esc(m.body)}</pre></div>`).join('') || '<p class="muted">No memories yet.</p>';
  }
  searchIndex() {
    const items = [];
    for(const t of (this.page === 'graph' ? this.data?.tasks : this.paletteTasks) || []) items.push({key:`task:${t.id}`,kind:'Task',label:`TSK-${t.id} · ${t.name}`,detail:t.description || t.status,search:`TSK-${t.id} ${t.name} ${t.description} ${t.status} ${(t.tags || []).join(' ')}`,open:() => this.open(t.id)});
    for(const a of this.library?.agents || this.data?.agents || []) items.push({key:`agent:${a.name}`,kind:'Agent',label:`@ ${a.name}`,detail:a.description || '',search:`agent ${a.name} ${a.description || ''} ${(a.memories || []).map(m => m.body).join(' ')}`,open:() => this.openAgent(a.name)});
    for(const t of this.library?.tags || []) items.push({key:`tag:${t.name}`,kind:'Tag',label:`# ${t.name}`,detail:t.description || '',search:`tag ${t.name} ${t.description || ''} ${(t.memories || []).map(m => m.body).join(' ')}`,open:() => { location.hash = 'system'; setTimeout(() => this.querySelector(`[data-tag="${CSS.escape(t.name)}"]`)?.scrollIntoView({block:'center'}),0); }});
    for(const r of (this.system?.behaviours || []).filter(r => !['prompt','agent','tag'].includes(r.trigger))) items.push({key:`workflow:${JSON.stringify([r.trigger,r.source || '',r.target,r.interval_seconds || 0,r.tag_pattern || '',Boolean(r.tag_negated)])}`,kind:'Workflow',label:ruleLabel(r),detail:r.prompt || '',search:`workflow ${ruleLabel(r)} ${r.trigger.startsWith('stop_') ? 'verified stop pinging' : ''} ${r.source || ''} ${r.prompt || ''}`,open:() => { location.hash = 'system'; setTimeout(() => this.querySelector('workflow-graph')?.scrollIntoView({block:'center'}),0); }});
    for(const w of this.status?.workers || []) items.push({key:`server:${w.id}`,kind:'Server',label:w.id,detail:(w.agents || []).join(', '),search:`server ${w.id} ${(w.agents || []).join(' ')}`,open:() => { location.hash = 'servers'; }});
    for(const s of (this.data?.sessions || []).filter(s => !isTasklessSession(s))) items.push({key:`session:${s.task_id}:${s.agent}`,kind:'Session',label:`TSK-${s.task_id} · ${s.agent}`,detail:s.status,search:`session ${s.task_id} ${s.agent} ${s.status} ${s.session_id || ''}`,open:() => this.open(s.task_id,s.agent)});
    items.unshift({key:'action:create',kind:'Action',label:'Create task',detail:'Add a new task',search:'new create add task',open:() => this.openCreate('task')});
    return items;
  }
  async loadPaletteTasks() {
    if(this.page === 'graph') return;
    const query = this.paletteInput.value.trim(), generation = this.generation;
    const request = this.paletteRequest = (this.paletteRequest || 0)+1;
    try {
      const data = await this.api('/api/palette/tasks?search='+encodeURIComponent(query));
      if(generation !== this.generation || request !== this.paletteRequest || !this.palette.open) return;
      this.paletteTasks = data.tasks || [];
      this.renderPalette();
    } catch(e) { if(e.name !== 'AbortError' && generation === this.generation && request === this.paletteRequest) this.palette.querySelector('.palette-results').insertAdjacentHTML('beforeend','<p class="muted" role="status">Task search unavailable. Try again.</p>'); }
  }
  openPalette() { this.paletteReturnFocus = document.activeElement; this.paletteInput.value=''; this.paletteTasks=[]; this.paletteItems = []; this.paletteQuery = null; this.paletteSelection=0; this.palette.showModal(); this.renderPalette(); this.loadPaletteTasks(); this.paletteInput.focus(); }
  renderPalette() {
    const selected = this.paletteItems?.[this.paletteSelection]?.key;
    const sameQuery = this.paletteQuery === this.paletteInput.value;
    this.paletteQuery = this.paletteInput.value;
    this.paletteItems = searchItems(this.searchIndex(),this.paletteInput.value).slice(0,50);
    const index = sameQuery ? this.paletteItems.findIndex(item => item.key === selected) : -1;
    this.paletteSelection = index >= 0 ? index : Math.min(this.paletteSelection || 0,Math.max(0,this.paletteItems.length-1));
    patchHTML(this.palette.querySelector('.palette-results'),this.paletteItems.map((item,i) => `<button data-refresh-key="${esc(item.key)}" type="button" role="option" aria-selected="${i === this.paletteSelection}" data-index="${i}"><span class="badge">${esc(item.kind)}</span><span><strong>${esc(item.label)}</strong><small>${esc(item.detail)}</small></span></button>`).join('') || '<p class="muted">No matches</p>');
  }
  paletteKey(e) {
    if(e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); this.paletteSelection = Math.max(0,Math.min(this.paletteItems.length-1,this.paletteSelection+(e.key === 'ArrowDown' ? 1 : -1))); this.paletteQuery = null; this.renderPalette(); this.palette.querySelector('[aria-selected="true"]')?.scrollIntoView({block:'nearest'}); }
    if(e.key === 'Enter') { e.preventDefault(); this.selectPalette(this.paletteSelection); }
  }
  selectPalette(index) { const item=this.paletteItems[index]; if(!item) return; this.palette.close(); if(this.dialog.open) this.dialog.close(); item.open(); }
  openCreate(kind) {
    if(kind !== 'task') return;
    if(this.dialog.open) this.dialog.close();
    this.createKind=kind; this.createReturnFocus=document.activeElement;
    const form=this.querySelector('#create-form');
    if(form.saving || form.creation?.generation === this.generation) { this.createDialog.showModal(); return; }
    form.reset(); delete form.creation; for(const field of form.elements) field.disabled=false; form.querySelector('[type="submit"]').textContent='Create'; form.querySelector('.form-error').textContent='';
    this.querySelector('#create-title').textContent=kind === 'task' ? 'New task' : 'New tag';
    this.querySelector('#task-fields').hidden=kind !== 'task';
    this.querySelector('#task-tags').innerHTML=(this.library?.tags || []).map(t => `<label class="tag-choice"><input type="checkbox" name="tag" value="${esc(t.name)}"> ${esc(t.name)}</label>`).join('') || '<span class="muted">No tags yet.</span>';
    this.createDialog.showModal(); form.elements.name.focus();
  }
  async submitCreate(e) {
    e.preventDefault(); const generation=this.generation, form=e.currentTarget;
    const saved = await this.saveCreation(form,async () => {
      const name=form.elements.name.value.trim(), description=form.elements.description.value;
      if(this.createKind === 'task') {
        const tags=[...form.querySelectorAll('[name="tag"]:checked')].map(c => c.value);
        const created=await this.post('/api/tasks',{name,description,status:form.elements.status.value,tags});
        return {taskID:created.id};
      }
    });
    if(saved && generation === this.generation) { this.createDialog.close(); location.hash='board'; await this.refresh(); if(generation === this.generation) this.open(saved.taskID); }
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
    if(this.sessionDialog?.open) { if(e.target.matches('select,input,textarea,[contenteditable]')) return; this.scrollMotion(e,e.target.closest('#task-session-output') || this.sessionDialog.querySelector('.detail')); return; }
    if (this.dialog.open) { if(e.target.matches('select,input,textarea,[contenteditable]')) return; this.scrollMotion(e,this.panel); return; }
    if (e.target.matches('select')) return;
    if (e.target.matches('input,textarea,[contenteditable]')) { if (e.key === 'Escape') { e.preventDefault(); this.search.value = ''; this.filterTasks(); this.focus(); } if (e.key === 'Enter') { e.preventDefault(); this.focus(); } return; }
    if(e.repeat && (e.key === 'g' || this.goPrefix)) { e.preventDefault(); return; }
    const goPage = {g:'graph',l:'board',s:'servers',b:'system',p:'performance'};
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
    if (e.key === 'Escape') { this.search.value = ''; this.filterTasks(); this.focus(); return; }
    if (['graph','servers','system','performance'].includes(this.page)) {
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
