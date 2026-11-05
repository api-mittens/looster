const esc = v => String(v ?? '').replace(/[&<>"']/g,c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
export function chronologicalEntries(comments = [], logs = []) {
  return [...comments.map(entry => ({...entry,entryType:'comment'})), ...logs.map(entry => ({...entry,entryType:'log'}))].sort((a,b) => (Date.parse(a.created_at)||0)-(Date.parse(b.created_at)||0) || String(a.entryType).localeCompare(b.entryType) || Number(a.id)-Number(b.id));
}
export function sessionLogHTML(log) {
  return `<article class="comment session-log" data-refresh-key="log:${esc(log.id)}"><div class="muted">${esc(log.agent || 'agent')} · ${esc(log.kind)} · Run ${esc(log.job_id)} · ${esc(new Date(log.created_at).toLocaleString())}</div><pre class="log-body">${esc(log.body)}</pre></article>`;
}
export function sessionLogsHTML(logs = []) {
  return chronologicalEntries([],logs).map(sessionLogHTML).join('');
}
