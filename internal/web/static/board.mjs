import {renderMarkdown} from './markdown.mjs';

const esc = v => String(v ?? '').replace(/[&<>"']/g,c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));

export const isTasklessSession = s => s.task_id === 0;
export const activeTasklessSessions = sessions => sessions.filter(s => isTasklessSession(s) && s.status === 'running');
export function boardAgentNames(agents, sessions) {
  const standalone = new Set([...agents.filter(a => a.taskless_only).map(a => a.name), ...sessions.filter(isTasklessSession).map(s => s.agent)]);
  const taskAgents = new Set(sessions.filter(s => !isTasklessSession(s)).map(s => s.agent));
  return [...new Set([...agents.map(a => a.name), ...sessions.map(s => s.agent)])].filter(n => n && (!standalone.has(n) || taskAgents.has(n))).sort();
}
export function taskSessionCardHTML(s) {
  const outcome = s.status || s.outcome;
  const state = s.summary_status || (outcome === 'running' ? 'pending' : ['failed','interrupted'].includes(outcome) ? outcome : 'missing');
  const summary = String(s.short_summary || s.summary || '').slice(0,280) || ({pending:'Summary pending.',missing:'No summary recorded.',error:'Summary unavailable due to an error.',failed:'Run failed. No summary recorded.',interrupted:'Run interrupted. No summary recorded.'}[state]) || 'No summary recorded.';
  return `<article class="session" data-refresh-key="job:${esc(s.job_id)}"><strong>${esc(s.agent)}</strong><span class="badge ${s.status === 'running' ? 'running' : ''}">${esc(s.status || s.outcome)}</span><code>Run ${esc(s.job_id)} · ${esc(s.session_id || 'Not started')}</code><span class="muted">${esc(state)}</span><button type="button" data-task-session-job="${esc(s.job_id)}" data-session-agent="${esc(s.agent)}" data-session-status="${esc(s.status || s.outcome)}"${!Number.isSafeInteger(Number(s.job_id)) || Number(s.job_id) < 1 ? ' disabled' : ''} aria-label="Inspect ${esc(s.agent)} run ${esc(s.job_id)}">Inspect run</button><div class="task-session-summary markdown-body">${renderMarkdown(summary)}</div></article>`;
}

export function sessionCardHTML(s) {
  return `<button tabindex="-1" type="button" data-session-agent="${esc(s.agent)}" data-session-job="${esc(s.job_id || '')}" aria-label="Inspect ${esc(s.agent)} session"><div class="card-top"><span class="ref">Other session</span></div><h3>${esc(s.agent)}</h3><div class="badges"><span class="badge running">${esc(s.status)}</span></div><code>${esc(s.session_id || s.job_id)}</code></button>`;
}

// Fall back to creation when activity is unavailable, and put undated tasks last.
// Keep the existing ID order as a deterministic tie-breaker.
const timestamp = value => typeof value === 'string' && value.trim() ? Date.parse(value) : NaN;
const activityTime = task => {
  const activity = timestamp(task.last_activity_at), created = timestamp(task.created_at);
  return Number.isFinite(activity) ? activity : Number.isFinite(created) ? created : -Infinity;
};

export function sortTasksByLatestActivity(tasks) {
  return [...tasks].sort((a,b) => activityTime(b)-activityTime(a) || a.id-b.id);
}

export function taskSignals(task) {
  const logs = task.session_logs || [];
  return {blocker:logs.some(log => log.kind === 'blocker'),question:logs.some(log => log.kind === 'question')};
}
export function sortTasksForColumn(tasks, column) {
  const promote = column === 'agent' || column.startsWith('agent:') || ['planning','todo'].includes(column);
  const priority = task => { const flags = taskSignals(task); return flags.blocker ? 2 : flags.question ? 1 : 0; };
  return sortTasksByLatestActivity(tasks).sort((a,b) => promote ? priority(b)-priority(a) : 0);
}
export function taskCardAge(task, now = Date.now()) {
  const age = (now-activityTime(task))/86400000;
  const inactive = ['closed','validated'].includes(task.status) && Number.isFinite(age);
  return {faded:inactive && age > 1,compact:inactive && age > 3};
}
export function taskCardHTML(t,s,now = Date.now()) {
  const statuses = t.status_changes ?? 0, comments = t.comment_count ?? 0;
  const flags = taskSignals(t), age = taskCardAge(t,now);
  return `<button tabindex="-1" class="${flags.blocker ? 'task-blocked ' : ''}${age.faded ? 'task-faded ' : ''}${age.compact ? 'task-compact' : ''}"${age.compact ? ` aria-label="TSK-${esc(t.id)}: ${esc(t.name)}, ${esc(t.status)}${flags.blocker ? ', historical blocker log' : ''}${flags.question ? ', historical question log' : ''}" title="${esc(t.name)}"` : ''}><div class="card-top"><span class="ref">TSK-${esc(t.id)}</span><div class="task-counters"><span class="activity-count" title="Status changes (including initial status)" aria-label="${esc(statuses)} status changes including initial status">Status ${esc(statuses)}</span><span class="activity-count" title="Comments" aria-label="${esc(comments)} comments">Comments ${esc(comments)}</span></div></div><h3>${esc(t.name)}</h3><div class="badges">${flags.blocker ? '<span class="badge signal-badge blocker-badge" aria-label="Historical blocker log"><span aria-hidden="true">⚠️</span><span class="signal-label"> Blocker</span></span>' : ''}${flags.question ? '<span class="badge signal-badge question-badge" aria-label="Historical question log"><span aria-hidden="true">❓</span><span class="signal-label"> Question</span></span>' : ''}<span class="badge ${s?.status === 'running' ? 'running' : ''}">${esc(s?.status || t.status)}</span>${(t.tags || []).map(tag => `<span class="badge">${esc(tag)}</span>`).join('')}</div></button>`;
}
