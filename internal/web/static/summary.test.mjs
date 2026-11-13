import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
const require = createRequire(import.meta.url);
globalThis.markdownit = require('./vendor/markdown-it.v15.0.3.min.js');
const {runDetailHTML} = await import('./agent-runs.mjs');
const {taskSessionCardHTML} = await import('./board.mjs');
const {renderMarkdown} = await import('./markdown.mjs');
const card = source => taskSessionCardHTML({job_id:7,agent:'coder',summary:source});

test('unloaded summaries show a loading state, not a missing summary',()=>{
  const session={job_id:7,agent:'coder',status:'done',summary_status:'available'};
  const pending=taskSessionCardHTML(session,{pending:true});
  assert.match(pending,/Load summary\./);
  assert.match(pending,/Load run error data\./);
  assert.doesNotMatch(pending,/No run error recorded/);
  assert.match(pending,/loading-placeholder/);
  assert.doesNotMatch(pending,/No summary recorded|>missing</);
  const error=taskSessionCardHTML(session,{unavailable:true});
  assert.match(error,/Cannot load summary/);
  assert.match(error,/Cannot load run error data/);
  assert.match(error,/data-payload-retry/);
  const loaded=taskSessionCardHTML({...session,error:'<script>bad</script>'});
  assert.match(loaded,/&lt;script&gt;bad&lt;\/script&gt;/);
  assert.doesNotMatch(loaded,/<script>/);
  assert.doesNotMatch(error,/loading-placeholder|No summary recorded/);
});

test('run summary uses shared Markdown while errors remain escaped plain text', () => {
  const summary = '# Result\n\n**Done** with `code`\n\n- tested\n\n```js\n<tag>\n```\n\n| A | B |\n| --- | --- |\n| x | y |';
  const run = {job_id:7,summary,error:'**not bold**\n<script>bad</script>'};
  const before = JSON.stringify(run);
  const html = runDetailHTML(run);
  assert.ok(html.includes(`<div class="run-summary markdown-body">${renderMarkdown(summary)}</div>`));
  assert.ok(html.includes('<pre class="run-summary">**not bold**\n&lt;script&gt;bad&lt;/script&gt;</pre>'));
  assert.equal(JSON.stringify(run),before);
});

test('task preview truncates source to 280 before rendering and prefers short summary', () => {
  for (const field of ['summary','short_summary']) {
    const source = '**Done**\n\n'+'x'.repeat(300)+'NEVER';
    const run = {job_id:7,agent:'coder',summary:'ignored', [field]:source};
    const before = JSON.stringify(run);
    const html = taskSessionCardHTML(run);
    assert.ok(html.includes(`<div class="task-session-summary markdown-body">${renderMarkdown(source.slice(0,280))}</div>`));
    assert.doesNotMatch(html,/NEVER|ignored/);
    assert.equal(JSON.stringify(run),before);
  }
});

test('summary HTML and unsafe URLs remain inert in both render paths', () => {
  for (const render of [card, summary => runDetailHTML({job_id:7,summary})]) {
    const html = render('<script>alert(1)</script>\n\n<img src=x onerror=bad>\n\n[bad](javascript:alert(1))\n\n![bad](data:image/svg+xml;base64,PHN2Zz4=)');
    assert.doesNotMatch(html,/<(?:script|img|a)\b/);
    assert.match(html,/&lt;script&gt;/);
    assert.match(render('[safe](https://example.com)'),/<a href="https:\/\/example.com">safe<\/a>/);
  }
});

test('semantic card keeps summary links separate from inspection button', () => {
  const html = card('[safe](https://example.com)');
  assert.match(html,/^<article class="session" data-refresh-key="job:7">/);
  const button = html.match(/<button\b[^>]*>[\s\S]*?<\/button>/g);
  assert.equal(button.length,1);
  assert.match(button[0],/data-task-session-job="7"/);
  assert.match(button[0],/aria-label="Inspect coder run 7"/);
  assert.doesNotMatch(button[0],/<a\b|markdown-body/);
  assert.equal((html.match(/data-task-session-job=/g)||[]).length,1);
  assert.match(taskSessionCardHTML({agent:'legacy',job_id:0}),/ disabled/);
  assert.match(taskSessionCardHTML({agent:'legacy'}),/ disabled/);
});

test('summary state and run metadata fallbacks are preserved', () => {
  const states = {pending:'Summary pending.',missing:'No summary recorded.',error:'Summary unavailable due to an error.',failed:'Run failed. No summary recorded.',interrupted:'Run interrupted. No summary recorded.'};
  for (const [summary_status,text] of Object.entries(states)) {
    assert.ok(taskSessionCardHTML({job_id:7,summary_status}).includes(`<p>${text}</p>`));
  }
  for (const [status,state] of [['running','pending'],['failed','failed'],['interrupted','interrupted'],['success','missing']]) {
    assert.ok(taskSessionCardHTML({job_id:7,status}).includes(`<p>${states[state]}</p>`));
  }
  assert.match(taskSessionCardHTML({job_id:7,summary_status:'unknown'}),/<p>No summary recorded\.<\/p>/);
  const html = runDetailHTML({job_id:7});
  for (const fallback of ['No summary recorded.','Not recorded','No task','Not assigned','No logs recorded.']) assert.ok(html.includes(fallback));
});
