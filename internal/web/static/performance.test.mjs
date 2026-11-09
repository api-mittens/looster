import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
import {performanceHTML,performanceChart,performanceValue} from './performance.mjs';
const days = Array.from({length:30},(_,i) => ({date:`2026-10-${String(i+1).padStart(2,'0')}`,validated:i,closed:i ? 2 : 0,ratio:i ? i/2 : null}));

test('performance renders seven accessible fixed-resolution charts and trailing counters',() => {
  const html = performanceHTML({days,week:{validated:100,closed:50,ratio:2,autonomous_questions:7}});
  assert.equal((html.match(/viewBox="0 0 640 210"/g) || []).length,7);
  assert.equal((html.match(/role="img"/g) || []).length,7);
  assert.match(html,/<strong>200.0%<\/strong>/);
  assert.match(html,/<strong>100<\/strong>/);
  assert.match(html,/<strong>7<\/strong>/);
  assert.match(html,/Trailing 7 × 24 hours/);
  assert.match(html,/aria-label="Autonomous agents"/);
  assert.match(html,/current status/);
  assert.match(html,/last_activity_at/);
  assert.match(html,/Missing counts default to 0/);
  assert.equal((html.match(/scope="row"/g) || []).length,30);
});

test('ratios are N/A with zero closed, with gaps rather than false zeroes',() => {
  assert.equal(performanceValue(null,true),'N/A');
  assert.equal(performanceValue(0,true),'0.0%');
  const html = performanceHTML({days:[{date:'2026-11-09',closed:0,ratio:100}],week:{closed:0,ratio:100}});
  assert.match(html,/<strong>N\/A<\/strong>/);
  assert.match(html,/<td>N\/A<\/td>/);
  const chart = performanceChart([{closed:2,ratio:1},{closed:0,ratio:null},{closed:2,ratio:2}], 'ratio','Ratio');
  assert.match(chart,/d="M48.00,96.00 M87.17,28.00"/);
  assert.equal((chart.match(/<circle/g) || []).length,2);
});

test('untrusted labels are escaped and missing or invalid counts have finite geometry',() => {
  const html = performanceHTML({days:[{date:'<img src=x onerror=alert(1)>',validated:Infinity,closed:-1}],week:{}});
  assert.doesNotMatch(html,/<img|NaN|Infinity/);
  assert.match(html,/&lt;img/);
  assert.match(performanceHTML(null),/role="status"/);
  assert.match(performanceHTML(null),/performance-refresh/);
});

function harness() {
  let Board;
  const location = {origin:'http://localhost',hash:''};
  const context = vm.createContext({HTMLElement:class {},customElements:{define:(name,cls) => {if(name === 'looster-board') Board=cls;}},URL,AbortController,DOMException,location,window:{scrollTo(){}},document:{},queueMicrotask,performanceHTML});
  vm.runInContext(readFileSync(new URL('./app.js',import.meta.url),'utf8').replace(/^import .*;\n/gm,''),context);
  const board = new Board();
  Object.assign(board,{namespace:'team /&',generation:0,controller:new AbortController(),page:'performance',palette:{open:false},querySelector:() => ({textContent:'',focus(){}}),render(){this.rendered=true;}});
  return {board,location,context};
}

test('performance refresh fetches endpoint and renders independently of board failure',async () => {
  const {board}=harness(), paths=[];
  board.api = async path => { paths.push(path); if(path === '/api/board') throw Error('unavailable'); return path === '/api/performance' ? {days,week:{}} : {}; };
  await board.refresh();
  assert.ok(paths.includes('/api/performance'));
  assert.equal(board.performance.days.length,30);
  assert.equal(board.rendered,true);
  board.page='board'; paths.length=0;
  await board.refresh();
  assert.ok(!paths.includes('/api/performance'));
});

test('performance API inherits namespace and stale response protection',async () => {
  const {board,context}=harness();
  let url;
  context.fetch=async input => { url=input; return {ok:true,json:async () => ({days})}; };
  await board.api('/api/performance');
  assert.equal(url.searchParams.get('namespace'),'team /&');
  context.fetch=async () => {board.generation++;return {ok:true,json:async () => ({days})};};
  await assert.rejects(board.api('/api/performance'),{name:'AbortError'});
});

test('gp routes to Performance and modified or input keystrokes do not navigate',() => {
  const {board,location}=harness();
  board.createDialog={open:false}; board.dialog={open:false};
  const event = key => ({key,target:{matches:() => false},preventDefault(){}});
  board.key(event('g')); board.key(event('p'));
  assert.equal(location.hash,'performance');
  location.hash='';
  board.key({...event('g'),ctrlKey:true}); board.key(event('p'));
  assert.equal(location.hash,'');
  board.key({...event('g'),target:{matches:selector => selector.includes('input')}});
  board.key(event('p'));
  assert.equal(location.hash,'');
});

test('namespace switching clears cached performance before the next refresh',() => {
  const {board}=harness();
  const node = {replaceChildren(){},reset(){},elements:[],value:'',textContent:''};
  board.querySelector=() => node;
  board.querySelectorAll=() => [];
  board.panel=board.board=node;
  board.search=board.paletteInput=node;
  board.palette={querySelector:() => node};
  board.performance={days};
  board.refresh=() => { assert.equal(board.performance,null); board.reloaded=true; };
  board.switchNamespace('other');
  assert.equal(board.namespace,'other');
  assert.equal(board.generation,1);
  assert.equal(board.reloaded,true);
});
