import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import {attachmentsHTML,attachmentURL,attachmentUploadHTML} from './attachments.mjs';

function harness(fetch) {
  const classes = new Map();
  const context = vm.createContext({HTMLElement:class {},customElements:{define:(name,type) => classes.set(name,type)},URL,AbortController,DOMException,FormData,location:{origin:'http://localhost'},fetch,queueMicrotask,attachmentsHTML});
  vm.runInContext(readFileSync(new URL('./app.js',import.meta.url),'utf8').replace(/^import .*;\n/gm,''),context);
  const board = new (classes.get('looster-board'))();
  Object.assign(board,{namespace:'alpha /&',generation:0,controller:new AbortController(),detailID:33});
  board.loadDetail = async id => { board.refreshed = id; };
  return board;
}
function uploadForm(eventId, count = 2) {
  const input = {files:Array.from({length:count},(_,i) => new File([`body ${i}`],`${i}.txt`)),disabled:false};
  const button = {disabled:false}, result = {textContent:''};
  return {elements:{files:input},dataset:eventId ? {eventId:String(eventId)} : {},isConnected:true,
    querySelector:selector => selector === 'button' ? button : result,
    reset(){this.wasReset=true;},button,result};
}

test('attachments belong below description or their exact comment, never both',() => {
  const files = [{id:1,name:'description.pdf',size:10},{id:2,event_id:12,name:'comment.png',size:20,previewable:true},{id:3,event_id:13,name:'other.txt',size:30}];
  assert.match(attachmentsHTML(files,33,null),/description.pdf/);
  assert.doesNotMatch(attachmentsHTML(files,33,null),/comment.png|other.txt/);
  const html = attachmentsHTML(files,33,12,'team /&');
  assert.match(html,/comment.png/);
  assert.doesNotMatch(html,/description.pdf|other.txt/);
  assert.match(html,/attachments\/2\/preview\?namespace=team%20%2F%26/);
  assert.match(html,/attachments\/2\/download\?namespace=team%20%2F%26/);
  assert.equal(attachmentsHTML(files,33,14),'');
});

test('only validated backend preview route is used and all files have downloads',() => {
  const html = attachmentsHTML([{id:1,name:'fake.png',key:'https://evil.test/file',preview_url:'https://evil.test/preview',size:1},{id:2,name:'<img src=x onerror="alert(1)">',size:2,previewable:true}],33,null);
  assert.equal((html.match(/<img /g) || []).length,1);
  assert.equal((html.match(/ download>/g) || []).length,2);
  assert.doesNotMatch(html,/evil.test|<img src=x/);
  assert.match(html,/&lt;img/);
  assert.equal(attachmentURL(33,2,'download','a&b'),'/api/tasks/TSK-33/attachments/2/download?namespace=a%26b');
  assert.doesNotMatch(attachmentUploadHTML(),/data-event-id/);
  assert.match(attachmentUploadHTML(12),/data-event-id="12"/);
});

test('upload sends each file with exact comment event and namespace',async () => {
  const calls = [];
  const board = harness(async (url,options) => {calls.push({url,options});return {ok:true};});
  const form = uploadForm(12);
  await board.uploadAttachments(form);
  assert.equal(calls.length,2);
  for(const {url,options} of calls) {
    assert.equal(url.searchParams.get('namespace'),'alpha /&');
    assert.equal(options.method,'POST');
    assert.equal(options.body.get('event_id'),'12');
    assert.ok(options.body.get('file') instanceof File);
    assert.equal(options.headers,undefined,'browser must generate multipart boundary');
  }
  assert.match(form.result.textContent,/Uploaded 2 files/);
  assert.equal(form.wasReset,true);
  assert.equal(form.button.disabled,false);
  assert.equal(board.refreshed,33);
});

test('partial upload is explicit and retains selections, saved files refresh',async () => {
  let calls = 0;
  const board = harness(async () => ++calls === 1 ? {ok:true} : {ok:false,text:async () => 'S3 upload unavailable'});
  const form = uploadForm(null,3);
  await board.uploadAttachments(form);
  assert.equal(calls,2,'stop instead of sending later files after failure');
  assert.match(form.result.textContent,/Partial upload: 1 of 3 files uploaded/);
  assert.match(form.result.textContent,/S3 upload unavailable/);
  assert.match(form.result.textContent,/Select only the remaining files/);
  assert.equal(form.wasReset,undefined);
  assert.equal(board.refreshed,33);
  assert.equal(form.elements.files.disabled,false);
});

test('missing S3 failure is visible and does not clear selected files',async () => {
  const board = harness(async () => ({ok:false,text:async () => 'S3 is not configured'}));
  const form = uploadForm(null,1);
  await board.uploadAttachments(form);
  assert.match(form.result.textContent,/Upload failed.*S3 is not configured/);
  assert.equal(form.wasReset,undefined);
  assert.equal(board.refreshed,undefined);
});

test('namespace switch during upload stops further files and discards stale UI result',async () => {
  let resolve, calls = 0;
  const board = harness(() => {calls++;return new Promise(r => {resolve=r;});});
  const form = uploadForm(12);
  const pending = board.uploadAttachments(form);
  board.generation++; board.namespace = 'beta';
  resolve({ok:true});
  await pending;
  assert.equal(calls,1);
  assert.equal(form.wasReset,undefined);
  assert.equal(board.refreshed,undefined);
});

test('attachment read errors retain explicit backend S3 diagnostics',async () => {
  const board = harness(async () => ({ok:false,text:async () => 'Attachment storage unavailable: S3 is not configured'}));
  board.outputs = new Map(); board.dialog = {open:true};
  const notice = {textContent:''};
  board.panel = {querySelector:() => notice};
  await board.loadAttachments(33,board.outputs);
  assert.match(notice.textContent,/Could not load attachments:.*S3 is not configured/);
});

test('attachment load uses array contract and ignores stale detail response',async () => {
  const board = harness(async () => {}), controls = new Map();
  board.outputs = new Map(); board.dialog = {open:true};
  board.panel = {querySelector:selector => {if(!controls.has(selector)) controls.set(selector,{innerHTML:'',textContent:''});return controls.get(selector);}};
  board.api = async () => [{id:1,name:'saved.txt',size:1}];
  await board.loadAttachments(33,board.outputs);
  assert.equal(controls.get('#attachment-notice').textContent,'');
  assert.match(controls.get('#description-attachments').innerHTML,/saved.txt/);
  const old = board.outputs; board.outputs = new Map();
  board.api = async () => [{id:2,name:'stale.txt',size:1}];
  await board.loadAttachments(33,old);
  assert.doesNotMatch(controls.get('#description-attachments').innerHTML,/stale.txt/);
});
