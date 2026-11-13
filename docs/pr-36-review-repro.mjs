// Review-only characterization checks for PR #36. No network or database access.
// These assertions document the observed defects, not the desired behaviour.
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFileSync} from 'node:fs';
import {loadingHTML} from '../internal/web/static/loading.mjs';
import {sessionLogsHTML} from '../internal/web/static/session-logs.mjs';

function harness() {
  const classes = new Map();
  const context = vm.createContext({
    HTMLElement: class {}, customElements: {define: (name, type) => classes.set(name, type)},
    AbortController, DOMException, queueMicrotask, loadingHTML, sessionLogsHTML,
    patchHTML: (node, html) => { node.innerHTML = html; }, renderMarkdown: value => value,
    taskSessionCardHTML: value => JSON.stringify(value), attachmentUploadHTML: () => '',
    chronologicalEntries: comments => comments, document: {activeElement: null},
  });
  vm.runInContext(readFileSync(new URL('../internal/web/static/app.js', import.meta.url), 'utf8').replace(/^import .*;\n/gm, ''), context);
  const board = new (classes.get('looster-board'))();
  const nodes = new Map();
  const node = selector => {
    if (!nodes.has(selector)) nodes.set(selector, {innerHTML: '', textContent: '', dataset: {}, querySelector: () => null});
    return nodes.get(selector);
  };
  board.panel = {querySelector: node};
  board.querySelector = node;
  board.dialog = {open: true};
  board.outputs = new Map();
  board.comments = new Map();
  board.detailID = 1;
  board.renderTaskComments = () => {};
  return {board, node};
}

{
  const {board, node} = harness();
  board.api = async () => ({task: {session_logs: []}, sessions: [], runs: [], has_more_logs: false, next_before_log_id: 0});
  await board.loadPayloads(1, board.outputs);
  // More than one page arrives while this dialog remains open.
  board.api = async () => ({task: {session_logs: Array.from({length: 200}, (_, i) => ({id: 102 + i, body: 'log'}))}, sessions: [], runs: [], has_more_logs: true, next_before_log_id: 102});
  await board.loadPayloads(1, board.outputs);
  assert.equal(board.taskLogs.size, 200);
  assert.equal(board.logCursor, 0);
  assert.equal(board.logsHasMore, false);
  assert.equal(node('#task-log-pagination').innerHTML, '');
  console.log('CONFIRMED: refresh receives an older-log cursor but discards it, hiding older logs until the dialog is reopened.');
}

{
  const {board, node} = harness();
  let resolve;
  board.api = () => new Promise(yes => { resolve = yes; });
  const pending = board.loadPayloads(1, board.outputs);
  // A subsequent metadata refresh completes before the older lake request.
  board.detailMetadata = {task: {description: 'New description'}, sessions: []};
  node('#task-description').innerHTML = 'New description';
  resolve({task: {description: 'Old description', session_logs: []}, sessions: [], runs: []});
  await pending;
  assert.equal(node('#task-description').innerHTML, 'Old description');
  console.log('CONFIRMED: an older payload response overwrites a newer metadata description for the same task.');
}
