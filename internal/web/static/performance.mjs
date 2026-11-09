const esc = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const count = value => Number.isFinite(value) && value >= 0 ? value : 0;
export const performanceValue = (value, ratio = false) => ratio ? (Number.isFinite(value) && value >= 0 ? `${(value * 100).toFixed(1)}%` : 'N/A') : String(count(value));
const metrics = [['ratio','Validated / closed ratio'],['validated','Validated count'],['closed','Closed count'],['task_blockers','Task blocker reports'],['task_questions','Task question reports']];
const autonomous = [['autonomous_blockers','Taskless blocker reports'],['autonomous_questions','Taskless question reports']];

export function performanceChart(days, key, label) {
  const ratio = key === 'ratio';
  const values = days.map(day => ratio ? (day.closed > 0 && Number.isFinite(day[key]) && day[key] >= 0 ? day[key] : null) : count(day[key]));
  const max = Math.max(1,...values.filter(v => v !== null));
  const x = i => 48 + i * 568 / 29, y = v => 164 - v / max * 136;
  let drawing = '', previous = false;
  values.forEach((value,i) => {
    if(value === null) { previous = false; return; }
    drawing += `${previous ? 'L' : 'M'}${x(i).toFixed(2)},${y(value).toFixed(2)} `;
    previous = true;
  });
  const id = `performance-${key}`;
  return `<article class="info-card performance-chart" data-refresh-key="${id}"><h3 id="${id}-heading">${esc(label)}</h3><svg viewBox="0 0 640 210" role="img" aria-labelledby="${id}-heading ${id}-desc"><desc id="${id}-desc">30 daily UTC buckets. ${ratio ? 'Gaps mean N/A because no tasks are closed.' : 'Missing counts default to zero.'} Exact values are in the daily data table below.</desc><path class="performance-axis" d="M48 28V164H616"/><text x="42" y="32" text-anchor="end">${esc(performanceValue(max,ratio))}</text><text x="42" y="168" text-anchor="end">0</text><path class="performance-line" d="${drawing.trim()}"/>${values.map((v,i) => v === null ? '' : `<circle cx="${x(i).toFixed(2)}" cy="${y(v).toFixed(2)}" r="3"><title>${esc(days[i].date)}: ${performanceValue(v,ratio)}</title></circle>`).join('')}<text x="48" y="192">${esc(days[0]?.date)}</text><text x="616" y="192" text-anchor="end">${esc(days.at(-1)?.date)}</text></svg></article>`;
}

export function performanceHTML(data) {
  const refresh = '<button id="performance-refresh" type="button">Refresh <kbd>r</kbd></button>';
  if(!data) return `${refresh}<p role="status">Performance data unavailable. Refresh retries automatically.</p>`;
  const days = (Array.isArray(data.days) ? data.days : []).slice(-30), week = data.week || {};
  const section = (items,title) => `<section aria-label="${title}"><h2>${title}</h2><div class="performance-counters">${items.map(([key,label]) => `<article class="info-card"><h3>${label}</h3><strong>${performanceValue(key === 'ratio' && !week.closed ? null : week[key],key === 'ratio')}</strong><p class="muted">Trailing 7 × 24 hours</p></article>`).join('')}</div><div class="performance-charts">${items.map(([key,label]) => performanceChart(days,key,label)).join('')}</div></section>`;
  return `<div class="performance">${refresh}<p>Daily charts show 30 UTC calendar-day buckets at a fixed daily resolution, including the current partial day. Counters cover the trailing 7 × 24 hours, not seven calendar buckets.</p><p>Tasks are counted once by their <strong>current status</strong>, bucketed by <code>last_activity_at</code>, not by status-transition history. Validated / closed is validated count divided by closed count, shown as a percentage, and is N/A when closed is zero. Missing counts default to 0. Task blocker/question reports are separate from taskless Autonomous agents reports.</p>${section(metrics,'Tasks')}${section(autonomous,'Autonomous agents')}<p class="muted">Autonomous agents: blocker and question reports from taskless runs only. These are not included in task report counts.</p><details class="performance-data"><summary>Daily data (UTC)</summary><div class="performance-table" tabindex="0" role="region" aria-label="Daily performance data"><table><caption>30 daily UTC buckets. Ratio is N/A when closed is zero.</caption><thead><tr><th scope="col">Date (UTC)</th>${[...metrics,...autonomous].map(([,label]) => `<th scope="col">${label}</th>`).join('')}</tr></thead><tbody>${days.map(day => `<tr><th scope="row">${esc(day.date)}</th>${[...metrics,...autonomous].map(([key]) => `<td>${performanceValue(key === 'ratio' && !day.closed ? null : day[key],key === 'ratio')}</td>`).join('')}</tr>`).join('')}</tbody></table></div></details></div>`;
}
