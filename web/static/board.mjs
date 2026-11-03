const esc = v => String(v ?? '').replace(/[&<>"']/g,c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));

export function sortTasksByStatusChanges(tasks) {
  return [...tasks].sort((a,b) => (b.status_changes ?? 0)-(a.status_changes ?? 0) || a.id-b.id);
}

export function taskCardHTML(t,s) {
  const statuses = t.status_changes ?? 0, comments = t.comment_count ?? 0;
  return `<button tabindex="-1"><div class="card-top"><span class="ref">TSK-${esc(t.id)}</span><div class="task-counters"><span class="activity-count" title="Status changes (including initial status)" aria-label="${esc(statuses)} status changes including initial status">Status ${esc(statuses)}</span><span class="activity-count" title="Comments" aria-label="${esc(comments)} comments">Comments ${esc(comments)}</span></div></div><h3>${esc(t.name)}</h3><div class="badges"><span class="badge ${s?.status === 'running' ? 'running' : ''}">${esc(s?.status || t.status)}</span>${(t.tags || []).map(tag => `<span class="badge">${esc(tag)}</span>`).join('')}</div></button>`;
}
