const esc = v => String(v ?? '').replace(/[&<>"']/g,c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
export function chronologicalEntries(comments = [], logs = []) {
  return [...comments.map(entry => ({...entry,entryType:'comment'})), ...logs.map(entry => ({...entry,entryType:'log'}))].sort((a,b) => (Date.parse(a.created_at)||0)-(Date.parse(b.created_at)||0) || String(a.entryType).localeCompare(b.entryType) || Number(a.id)-Number(b.id));
}
export function sessionLogHTML(log) {
  const accent = log.kind === 'blocker' ? ' session-log-blocker' : log.kind === 'question' ? ' session-log-question' : '';
  let body = esc(log.body);
  if (log.kind === 'link' && typeof log.body === 'string' && /^https?:\/\/[^/?#]+(?:[/?#]|$)/i.test(log.body) && !/[\s\u0000-\u001f\u007f-\u009f\\]/u.test(log.body)) {
    try {
      const url = new URL(log.body);
      if (url.hostname && ['http:', 'https:'].includes(url.protocol)) body = `<a href="${esc(log.body)}" target="_blank" rel="noopener noreferrer">${esc(log.body)}</a>`;
    } catch { /* Invalid historical links remain escaped plain text. */ }
  }
  return `<article class="comment session-log${accent}" data-refresh-key="log:${esc(log.id)}"><div class="muted">${esc(log.agent || 'agent')} · ${esc(log.kind)} · Run ${esc(log.job_id)} · ${esc(new Date(log.created_at).toLocaleString())}</div><pre class="log-body">${body}</pre></article>`;
}
export function sessionLogsHTML(logs = []) {
  return chronologicalEntries([],logs).map(sessionLogHTML).join('');
}
