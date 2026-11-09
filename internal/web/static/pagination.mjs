const esc = v => String(v ?? '').replace(/[&<>"']/g,c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));

export function columnPaginationHTML(column,{pending=false,error=''}={}) {
  const page = column.page || 1, total = column.total || 0, size = column.page_size || 15;
  const pages = Math.max(1,Math.ceil(total/size));
  const label = column.key.startsWith('agent:') ? '@ '+column.key.slice(6) : column.key;
  const first = total ? (page-1)*size+1 : 0, last = Math.min(page*size,total);
  const button = (text,target,disabled) => `<button type="button" data-column="${esc(column.key)}" data-column-page="${target}" aria-label="${text} page for ${esc(label)}"${disabled || pending ? ' disabled' : ''}>${text === 'Previous' ? '‹' : '›'} <span>${text}</span></button>`;
  return `<nav class="column-pagination" aria-label="${esc(label)} pagination" aria-busy="${pending}"><div class="column-page-summary" role="status" aria-live="polite">${pending ? 'Loading… · ' : ''}${first}–${last} of ${total} tasks</div><div class="column-page-buttons">${button('Previous',page-1,page<=1)}<span class="column-page-number">${page} / ${pages}</span>${button('Next',page+1,page>=pages)}</div>${error ? `<div class="column-page-error" role="alert">${esc(error)} <button type="button" data-column-retry>Retry</button></div>` : ''}</nav>`;
}
