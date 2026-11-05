import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import {attachmentsHTML,attachmentURL,attachmentUploadHTML} from './attachments.mjs';

function harness(fetch) {
  const classes = new Map();
  const context = vm.createContext({document:{activeElement:null},HTMLElement:class {},customElements:{define:(name,type) => classes.set(name,type)},URL,AbortController,DOMException,FormData,location:{origin:'http://localhost'},fetch,queueMicrotask,attachmentsHTML,patchHTML:(root,html) => { root.innerHTML = html; }});
  vm.runInContext(readFileSync(new URL('./app.js',import.meta.url),'utf8').replace(/^import .*;\n/gm,''),context);
  const board = new (classes.get('looster-board'))();
  Object.assign(board,{namespace:'alpha /&',generation:0,controller:new AbortController(),detailID:33});
  board.loadDetail = async id => { board.refreshed = id; };
  return board;
}
function form(count = 2) {
  const input = {files:Array.from({length:count},(_,i) => new File([`body ${i}`],`${i}.txt`)),disabled:false};
  const button = {disabled:false,textContent:'Post comment',type:'submit'}, result = {textContent:''};
  const elements = [input,button]; elements.files=input;
  return {id:'comment-form',elements,isConnected:true,querySelector:s => s === '[type="submit"]' ? button : result,
    reset(){this.wasReset=true;},button,result};
}

test('saved attachments retain isolated grouping, safe previews and downloads',() => {
  const files = [{id:1,name:'description.pdf',size:10},{id:2,event_id:12,name:'<comment>.png',size:20,previewable:true},{id:3,event_id:13,name:'other.txt',size:30}];
  assert.match(attachmentsHTML(files,33,null),/description.pdf/);
  assert.doesNotMatch(attachmentsHTML(files,33,null),/comment|other.txt/);
  const html = attachmentsHTML(files,33,12,'team /&');
  assert.match(html,/&lt;comment&gt;.png/);
  assert.doesNotMatch(html,/description.pdf|other.txt/);
  for(const action of ['preview','download']) assert.ok(html.includes(attachmentURL(33,2,action,'team /&')));
  assert.equal(attachmentsHTML(files,33,14),'');
  assert.doesNotMatch(attachmentsHTML([{id:1,key:'https://evil.test',name:'fake.png'}],33,null),/<img|evil.test/);
  assert.match(attachmentUploadHTML(),/multiple/);
  assert.doesNotMatch(attachmentUploadHTML(),/required|<form|data-event-id/);
});

test('creation precedes multiple uploads using returned IDs and exact namespace',async () => {
  const calls=[];
  let created=false;
  const board=harness(async (url,options) => {assert.ok(created);calls.push({url,options});return {ok:true};});
  const f=form();
  const saved=await board.saveCreation(f,async () => {created=true;return {taskID:77,eventID:123};});
  assert.equal(saved.taskID,77);
  assert.equal(calls.length,2);
  for(const {url,options} of calls) {
    assert.equal(url.pathname,'/api/tasks/TSK-77/attachments');
    assert.equal(url.searchParams.get('namespace'),'alpha /&');
    assert.equal(options.body.get('event_id'),'123');
    assert.ok(options.body.get('file') instanceof File);
    assert.equal(options.headers,undefined);
  }
  assert.equal(f.wasReset,true);
  assert.equal(f.button.disabled,false);
  assert.equal(f.creation,undefined);
});

test('partial failure retries only remaining files without duplicate creation',async () => {
  const names=[]; let creates=0, fail=true;
  const board=harness(async (url,options) => {
    const name=options.body.get('file').name; names.push(name);
    return name === '1.txt' && fail ? {ok:false,text:async () => 'S3 unavailable'} : {ok:true};
  });
  const f=form(3), create=async () => {creates++;return {taskID:77,eventID:12};};
  assert.equal(await board.saveCreation(f,create),false);
  assert.match(f.result.textContent,/1 of 3.*S3 unavailable.*Retry remaining files/);
  assert.equal(f.elements.files.disabled,true);
  assert.equal(board.refreshed,77);
  assert.equal(f.wasReset,undefined);
  fail=false;
  assert.ok(await board.saveCreation(f,create));
  assert.equal(creates,1);
  assert.deepEqual(names,['0.txt','1.txt','1.txt','2.txt']);
  assert.equal(f.elements.files.disabled,false);
});

test('optional files create without upload and creation errors allow retry',async () => {
  const board=harness(() => assert.fail('no upload expected')), f=form(0);
  assert.equal(await board.saveCreation(f,async () => {throw Error('Invalid task');}),false);
  assert.match(f.result.textContent,/Invalid task/);
  assert.equal(f.button.disabled,false);
  assert.ok(await board.saveCreation(f,async () => ({taskID:88})));
});

test('task attachments omit event ID and storage errors retain resumable state',async () => {
  const board=harness(async (url,options) => {
    assert.equal(options.body.get('event_id'),null);
    return {ok:false,text:async () => 'S3 is not configured'};
  }), f=form(1);
  await board.saveCreation(f,async () => ({taskID:77}));
  assert.match(f.result.textContent,/Saved.*0 of 1.*S3 is not configured.*Retry/);
  assert.equal(f.creation.taskID,77);
  assert.equal(f.wasReset,undefined);
});

test('namespace switch during upload stops later files and stale UI writes',async () => {
  let resolve, calls=0;
  const board=harness(() => {calls++;return new Promise(r => {resolve=r;});}), f=form();
  const pending=board.saveCreation(f,async () => ({taskID:77,eventID:12}));
  await new Promise(setImmediate);
  board.generation++; board.namespace='beta'; resolve({ok:true});
  await pending;
  assert.equal(calls,1);
  assert.equal(f.wasReset,undefined);
  assert.equal(board.refreshed,undefined);
});

test('duplicate submissions while creation is pending are ignored',async () => {
  let resolve, creates=0;
  const board=harness(async () => ({ok:true})), f=form(0);
  const create=() => {creates++; return new Promise(r => {resolve=r;});};
  const pending=board.saveCreation(f,create);
  assert.equal(await board.saveCreation(f,create),false);
  resolve({taskID:77}); await pending;
  assert.equal(creates,1);
});

test('closing prior detail while task creation resolves does not discard saved ID',async () => {
  let resolve;
  const board=harness(async () => ({ok:true})), f=form(1);
  f.id='create-form';
  const pending=board.saveCreation(f,() => new Promise(r => {resolve=r;}));
  board.detailID=null;
  resolve({taskID:77});
  assert.equal((await pending).taskID,77);
  assert.equal(f.button.disabled,false);
  assert.equal(f.wasReset,true);
});

test('reopening pending creation preserves selections and disabled submit',async () => {
  let resolve;
  const board=harness(async () => ({ok:true})), f=form(1);
  f.id='create-form';
  board.dialog={open:false};
  board.querySelector=() => f;
  board.createDialog={showModal(){this.open=true;}};
  const pending=board.saveCreation(f,() => new Promise(r => {resolve=r;}));
  board.openCreate('task');
  assert.equal(board.createDialog.open,true);
  assert.equal(f.wasReset,undefined);
  assert.equal(f.saving,true);
  assert.equal(f.button.disabled,true);
  resolve({taskID:77});
  await pending;
  assert.equal(f.saving,undefined);
});

test('attachment read errors retain explicit backend diagnostics',async () => {
  const board=harness(async () => ({ok:false,text:async () => 'S3 is not configured'}));
  board.outputs=new Map(); board.dialog={open:true};
  const notice={textContent:''}; board.panel={querySelector:() => notice};
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
