const esc = v => String(v ?? '').replace(/[&<>"']/g,c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));

export const isTasklessSession = s => s.task_id === 0;
export const activeTasklessSessions = sessions => sessions.filter(s => isTasklessSession(s) && s.status === 'running');
export function boardAgentNames(agents, sessions) {
  const standalone = new Set([...agents.filter(a => a.taskless_only).map(a => a.name), ...sessions.filter(isTasklessSession).map(s => s.agent)]);
  const taskAgents = new Set(sessions.filter(s => !isTasklessSession(s)).map(s => s.agent));
  return [...new Set([...agents.map(a => a.name), ...sessions.map(s => s.agent)])].filter(n => n && (!standalone.has(n) || taskAgents.has(n))).sort();
}
export function taskSessionCardHTML(s) {
  const state = s.summary_status || (s.status === 'running' ? 'pending' : 'missing');
  const summary = s.short_summary || (s.summary ? String(s.summary).slice(0,280) : '') || ({pending:'Summary pending.',missing:'No summary recorded.',error:'Summary unavailable due to an error.'}[state]) || 'No summary recorded.';
  return `<button type="button" class="session" data-refresh-key="job:${esc(s.job_id)}" data-task-session-job="${esc(s.job_id)}" data-session-agent="${esc(s.agent)}"${s.job_id == null ? ' disabled' : ''} aria-label="Inspect ${esc(s.agent)} run ${esc(s.job_id)}"><strong>${esc(s.agent)}</strong><span class="badge ${s.status === 'running' ? 'running' : ''}">${esc(s.status || s.outcome)}</span><code>Run ${esc(s.job_id)} · ${esc(s.session_id || 'Not started')}</code><span class="muted">${esc(state)}</span><span class="task-session-summary">${esc(summary)}</span></button>`;
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

export function taskCardHTML(t,s) {
  const statuses = t.status_changes ?? 0, comments = t.comment_count ?? 0;
  return `<button tabindex="-1"><div class="card-top"><span class="ref">TSK-${esc(t.id)}</span><div class="task-counters"><span class="activity-count" title="Status changes (including initial status)" aria-label="${esc(statuses)} status changes including initial status">Status ${esc(statuses)}</span><span class="activity-count" title="Comments" aria-label="${esc(comments)} comments">Comments ${esc(comments)}</span></div></div><h3>${esc(t.name)}</h3><div class="badges"><span class="badge ${s?.status === 'running' ? 'running' : ''}">${esc(s?.status || t.status)}</span>${(t.tags || []).map(tag => `<span class="badge">${esc(tag)}</span>`).join('')}</div></button>`;
}
