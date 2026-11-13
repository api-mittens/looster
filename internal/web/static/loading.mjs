const esc = value => String(value).replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));

export function loadingHTML(label = 'Load data.') {
  return `<div class="loading-placeholder" role="status" aria-busy="true"><span>${esc(label)}</span><span class="loading-line" aria-hidden="true"></span><span class="loading-line" aria-hidden="true"></span></div>`;
}
