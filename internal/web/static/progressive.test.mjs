import {test} from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import {readFileSync} from 'node:fs';
import {loadingHTML} from './loading.mjs';

const deferred = () => { let resolve, reject; const promise = new Promise((yes,no) => { resolve=yes; reject=no; }); return {promise,resolve,reject}; };
const tick = () => new Promise(resolve => setImmediate(resolve));
function harness() {
  const classes = new Map();
  const context = vm.createContext({HTMLElement:class {},customElements:{define:(name,type)=>classes.set(name,type)},AbortController,DOMException,queueMicrotask,loadingHTML,patchHTML:(node,html)=>{node.innerHTML=html;},renderMarkdown:value=>value,taskSessionCardHTML:session=>JSON.stringify(session),attachmentUploadHTML:()=>'',chronologicalEntries:(comments,logs)=>[...comments,...logs],document:{activeElement:null}});
  vm.runInContext(readFileSync(new URL('./app.js',import.meta.url),'utf8').replace(/^import .*;\n/gm,''),context);
  const board = new (classes.get('looster-board'))();
  const nodes = new Map();
  const node = selector => { if(!nodes.has(selector)) nodes.set(selector,{innerHTML:'',textContent:'',dataset:{},querySelector:()=>null,replaceChildren(){this.innerHTML='';}}); return nodes.get(selector); };
  board.querySelector=node;
  board.panel={querySelector:node,querySelectorAll:()=>[]};
  board.dialog={open:true,showModal(){this.open=true;}};
  board.palette={open:false};
  board.search={value:''};
  board.page='graph'; board.generation=1; board.outputs=new Map(); board.comments=new Map(); board.attachments=[]; board.detailID=1;
  board.render=()=>{board.renders=(board.renders || 0)+1;};
  return {board,node};
}

test('metadata refresh does not wait for other endpoints',async()=>{
  const {board}=harness(), slow=deferred(), calls=[];
  board.api=path=>{calls.push(path);return path==='/api/board'?Promise.resolve({tasks:[{id:1}]}):slow.promise;};
  await board.refresh();
  assert.equal(board.data.tasks[0].id,1);
  assert.equal(board.loading,false);
  assert.ok(board.renders>0);
  await board.refresh();
  assert.equal(calls.filter(path=>path==='/api/library').length,1);
  slow.resolve([]); await tick();
});

test('metadata renders while payload and attachments are pending',async()=>{
  const {board,node}=harness(), slow=deferred(), calls=[];
  board.api=path=>{calls.push(path);return path==='/api/tasks/TSK-1'?Promise.resolve({task:{name:'First',status:'todo'},sessions:[]}):slow.promise;};
  await board.loadDetail(1);
  assert.match(node('#dialog-title').innerHTML,/First/);
  assert.equal(node('#task-status').value,'todo');
  assert.match(node('#task-comments').innerHTML,/loading-placeholder/);
  assert.equal(node('#task-description').innerHTML,'No description.');
  assert.ok(calls.includes('/api/tasks/TSK-1/payloads?limit=50'));
  assert.ok(calls.includes('/api/tasks/TSK-1/attachments'));
  board.detailID=2; slow.resolve({task:{description:'Old data'}}); await tick();
  assert.doesNotMatch(node('#task-description').innerHTML,/Old data/);
});

test('payload failures preserve completed content and allow retry',async()=>{
  const {board,node}=harness();
  board.payloadData={task:{description:'Saved content'}};
  node('#task-description').innerHTML='Saved content';
  board.api=async()=>{throw Error('offline');};
  await board.loadPayloads(1,board.outputs);
  assert.equal(node('#task-description').innerHTML,'Saved content');
  assert.match(node('#payload-notice').innerHTML,/data-payload-retry/);
  assert.equal(board.payloadBusy,null);
  board.api=async()=>({task:{description:'New content'},comments:[],sessions:[]});
  await board.loadPayloads(1,board.outputs);
  assert.equal(node('#task-description').innerHTML,'New content');
  assert.equal(node('#payload-notice').textContent,'');
});

test('task switch aborts previous requests and ignores late metadata',async()=>{
  const {board,node}=harness(), old=deferred(), next=deferred();
  board.api=path=>path==='/api/tasks/TSK-1'?old.promise:next.promise;
  const first=board.open(1), signal=board.detailController.signal;
  const second=board.open(2);
  assert.equal(signal.aborted,true);
  old.resolve({task:{name:'Old task',status:'todo'}}); await first;
  assert.doesNotMatch(node('#dialog-title').innerHTML,/Old task/);
  next.resolve({task:{name:'New task',status:'todo'},comments:[],sessions:[]}); await second; await tick();
  assert.match(node('#dialog-title').innerHTML,/New task/);
});

test('loading state has accessible text and reduced-motion styles',()=>{
  assert.match(loadingHTML('<data>'),/role="status" aria-busy="true"/);
  assert.match(loadingHTML('<data>'),/&lt;data&gt;/);
  const css=readFileSync(new URL('./style.css',import.meta.url),'utf8');
  assert.match(css,/@media\(prefers-reduced-motion:reduce\)\{\.loading-line\{animation:none\}\}/);
});

test('old auxiliary responses cannot change the new namespace',async()=>{
  const {board}=harness(), old=deferred();
  board.api=()=>old.promise;
  board.refreshAuxiliary(1);
  board.generation=2;
  board.library={agents:[{name:'new'}]};
  old.resolve({agents:[{name:'old'}]}); await tick();
  assert.equal(board.library.agents[0].name,'new');
});

test('session refresh keeps loaded text and uses current status',async()=>{
  const {board,node}=harness();
  board.payloadData={sessions:[{job_id:7,status:'running',summary:'Saved summary'}]};
  board.api=path=>path==='/api/tasks/TSK-1'?Promise.resolve({task:{name:'Task',status:'todo'},sessions:[{job_id:7,status:'done',summary:''}]}):new Promise(()=>{});
  await board.loadDetail(1);
  const session=JSON.parse(node('#sessions').innerHTML);
  assert.equal(session.summary,'Saved summary');
  assert.equal(session.status,'done');
});

test('a metadata failure keeps completed board data and shows Retry',async()=>{
  const {board,node}=harness();
  board.data={tasks:[{id:80}],agents:[],sessions:[]};
  board.api=path=>path==='/api/board'?Promise.reject(Error('offline')):new Promise(()=>{});
  await board.refresh();
  assert.equal(board.data.tasks[0].id,80);
  assert.match(node('#notice').innerHTML,/metadata-retry/);
  assert.equal(board.loading,false);
});
