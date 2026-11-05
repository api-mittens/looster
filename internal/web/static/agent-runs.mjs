const esc = v => String(v ?? '').replace(/[&<>"']/g,c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const time = value => value && !String(value).startsWith('0001-') ? esc(new Date(value).toLocaleString()) : 'Not recorded';

export function runHistoryHTML(page, hasPrevious = false) {
  const runs = page.runs || [];
  return `<h3>Run history</h3><div class="run-list">${runs.map(run => `<button type="button" data-run="${esc(run.job_id)}">Run ${esc(run.job_id)} · ${esc(run.outcome)} · ${run.task_id ? 'TSK-'+esc(run.task_id) : 'No task'}<br><span class="muted">${time(run.started_at)}</span></button>`).join('') || '<p class="muted">No runs recorded.</p>'}</div><div class="run-pagination">${hasPrevious ? '<button type="button" data-run-previous>Previous runs</button>' : ''}${page.has_more ? `<button type="button" data-run-before="${esc(page.next_before_job_id)}">Next runs</button>` : ''}</div><section id="selected-run" aria-live="polite"></section>`;
}

export function runDetailHTML(run) {
  return `<h3>Run ${esc(run.job_id)}</h3><dl><dt>Outcome</dt><dd>${esc(run.outcome || 'Not recorded')}</dd><dt>Task</dt><dd>${run.task_id ? `<button type="button" data-run-task="${esc(run.task_id)}">TSK-${esc(run.task_id)}</button>` : 'No task'}</dd>${run.context_task_id ? `<dt>Context task</dt><dd>TSK-${esc(run.context_task_id)}</dd>` : ''}<dt>Session</dt><dd>${esc(run.session_id || 'Not assigned')}</dd><dt>Started</dt><dd>${time(run.started_at)}</dd><dt>Finished</dt><dd>${time(run.finished_at)}</dd><dt>Summary status</dt><dd>${esc(run.summary_status || 'Not recorded')}</dd></dl><h4>Summary</h4><pre class="run-summary">${esc(run.summary || 'No summary recorded.')}</pre>${run.error ? `<h4>Error</h4><pre class="run-summary">${esc(run.error)}</pre>` : ''}`;
}
