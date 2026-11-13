//go:build uitest

package web

// Behavioral probes execute only through their Go test entrypoints.
const graphproductionProbe = `  await call('Network.enable');await call('Page.navigate',{url:base+'/'});
  await until(()=>evaluate(` + "`" + `document.querySelector('task-agent-graph')?.graph?.nodes.filter(n=>n.kind==='task').length===30` + "`" + `));
  check(await evaluate(` + "`" + `(()=>{window.board=document.querySelector('looster-board');clearInterval(board.timer);window.graph=board.querySelector('task-agent-graph');return graph.querySelectorAll('.tg-task').length===30&&!graph.querySelector('[data-key="task:31"]')&&!graph.querySelector('[data-key="task:32"]')})()` + "`" + `),'real CLI-created32tasks render only30unfinished through production API');
  check(await evaluate(` + "`" + `board.page==='graph'&&board.querySelector('nav a').hash==='#graph'&&getComputedStyle(board.querySelector('.footer')).display==='none'&&board.querySelectorAll('nav a kbd').length===5&&!board.querySelector('.nav-hint')` + "`" + `),'bare production URL defaults to leftmost Graph with header hints visible and graph ribbon hidden');
  check(await evaluate(` + "`" + `(()=>{const select=board.querySelector('#namespace'),nav=board.querySelector('nav'),option=select.selectedOptions[0],text=option.textContent,center=()=>{const r=nav.getBoundingClientRect();return r.x+r.width/2};if(![...select.options].every(o=>o.textContent.endsWith(' namespace')))return false;const before=center();option.textContent='a-very-long-project-and-security-boundary-name-'.repeat(8)+' namespace';const after=center(),r=nav.getBoundingClientRect(),brand=board.querySelector('.header-brand').getBoundingClientRect();option.textContent=text;return Math.abs(before-document.documentElement.clientWidth/2)<1&&Math.abs(after-before)<1&&brand.right<=r.left})()` + "`" + `),'namespace labels include namespace suffix and long selected names cannot move centered navigation');
  await call('Emulation.setDeviceMetricsOverride',{width:390,height:844,deviceScaleFactor:1,mobile:false});
  check(await evaluate(` + "`" + `(()=>{const r=board.querySelector('nav').getBoundingClientRect();return Math.abs(r.x+r.width/2-document.documentElement.clientWidth/2)<1&&document.documentElement.scrollWidth<=innerWidth&&[...board.querySelectorAll('.header-actions button')].every(b=>b.getBoundingClientRect().right<=innerWidth)})()` + "`" + `),'narrow header wraps centered navigation without horizontal overflow');
  await evaluate(` + "`" + `window.scrollTo(0,document.documentElement.scrollHeight)` + "`" + `);
  // CDP evaluation can finish before Chromium applies the requested scroll.
  await until(()=>evaluate(` + "`" + `window.scrollY>0&&Math.abs(window.scrollY-(document.documentElement.scrollHeight-innerHeight))<1` + "`" + `));
  check(await evaluate(` + "`" + `window.scrollY>0&&getComputedStyle(board.querySelector('header')).position==='sticky'&&Math.abs(board.querySelector('header').getBoundingClientRect().top)<1&&board.querySelector('header').contains(document.elementFromPoint(20,20))` + "`" + `),'mobile header remains visible above content while scrolled');
  await evaluate(` + "`" + `window.scrollTo(0,0)` + "`" + `);
  await until(()=>evaluate(` + "`" + `window.scrollY===0` + "`" + `));
  await call('Emulation.setDeviceMetricsOverride',{width:1440,height:1100,deviceScaleFactor:1,mobile:false});
  for(const page of ['board','graph']){
   check(await evaluate(` + "`" + `(()=>{board.page='${page}';board.render();return board.querySelector('#refresh kbd')?.textContent==='r'&&board.querySelector('#new-task kbd')?.textContent==='n'&&board.querySelectorAll('nav a kbd').length===5&&!board.querySelector('.nav-hint')})()` + "`" + `),page+' shows header hints and Refresh r / New task n keycaps');
  }
  const press=async key=>{await call('Input.dispatchKeyEvent',{type:'keyDown',key});await call('Input.dispatchKeyEvent',{type:'keyUp',key});};
  for(const [suffix,page,title] of [['l','board','Task list'],['s','servers','Servers & agents'],['b','system','Behaviours'],['g','graph','Task graph']]){
   await press('g');await press(suffix);
   await until(()=>evaluate(` + "`" + `board.page==='${page}'&&!board.loading` + "`" + `));
   check(await evaluate(` + "`" + `board.querySelector('h1').textContent==='${title}'&&board.querySelector('nav a[aria-current="page"] kbd').textContent==='g${suffix}'` + "`" + `),'native g'+suffix+' navigates to '+title+' with matching header hint');
  }
  await press('H');await press('L');
  check(await evaluate(` + "`" + `board.page==='graph'&&!board.querySelector('.nav-hint')` + "`" + `),'legacy H/L tab navigation and hints removed');
  await evaluate(` + "`" + `board.search.focus()` + "`" + `);await press('g');await press('l');
  check(await evaluate(` + "`" + `board.page==='graph'` + "`" + `),'go-to shortcuts do not navigate while typing in search');
  await evaluate(` + "`" + `board.search.value='';board.search.blur();board.render();board.showHelp()` + "`" + `);
  await press('g');await press('s');
  check(await evaluate(` + "`" + `board.dialog.open&&board.page==='graph'&&board.panel.textContent.includes('gl')&&!board.panel.textContent.includes('previous / next tab')` + "`" + `),'dialog keeps scrolling context and help documents new shortcuts only');
  await evaluate(` + "`" + `board.panel.style.maxHeight='80px';board.panel.style.overflow='auto';board.panel.focus()` + "`" + `);
  await press('G');
  check(await evaluate(` + "`" + `board.panel.scrollTop>0` + "`" + `),'G still scrolls the actual dialog to bottom');
  await press('g');await press('g');
  check(await evaluate(` + "`" + `board.panel.scrollTop===0&&board.dialog.open` + "`" + `),'gg inside dialog still scrolls to top without page navigation');
  await evaluate(` + "`" + `board.dialog.close();board.panel.style.maxHeight='';board.panel.style.overflow='';board.querySelector('nav a').focus()` + "`" + `);
  await press('G');await delay(100);
  check(await evaluate(` + "`" + `window.scrollY>0` + "`" + `),'G retains page bottom scrolling');
  check(await evaluate(` + "`" + `Math.abs(board.querySelector('header').getBoundingClientRect().top)<1&&getComputedStyle(board.querySelector('header')).position==='sticky'&&board.querySelector('header').contains(document.elementFromPoint(20,20))` + "`" + `),'desktop header stays pinned above scrolled page content');
  await press('g');await press('g');
  await until(()=>evaluate(` + "`" + `window.scrollY===0` + "`" + `));
  await press('g');await delay(750);await press('b');
  check(await evaluate(` + "`" + `board.page==='graph'` + "`" + `),'expired go-to prefix does not navigate');
  await evaluate(` + "`" + `window.graph=board.querySelector('task-agent-graph')` + "`" + `);
  check(await evaluate(` + "`" + `!graph.querySelector('[data-key="agent:unrelated"]')&&!graph.querySelector('[data-key="agent:finished"]')&&graph.querySelectorAll('.tg-agent').length===4` + "`" + `),'online unrelated and completed-only agents are hidden in the real graph');
  // Earlier navigation checks can outlast the initial force simulation under load.
  // Reheat before sampling so this checks autonomous D3 ticks, not whether startup is still settling.
  await evaluate('graph.reheat(1)');
  const before=await evaluate(` + "`" + `graph.graph.nodes.map(n=>({x:n.x,y:n.y}))` + "`" + `);await delay(120);
  check(await evaluate(` + "`" + `graph.graph.nodes.some((n,i)=>Math.hypot(n.x-${JSON.stringify(before)}[i].x,n.y-${JSON.stringify(before)}[i].y)>.01)` + "`" + `),'real production graph moves autonomously with live D3');
  await evaluate('graph.settle()');
  check(await evaluate(` + "`" + `graph.querySelectorAll('.tg-active').length===1&&getComputedStyle(graph.querySelector('.tg-active')).animationName!=='none'&&graph.querySelectorAll('.tg-edge').length===30` + "`" + `),'production session query distinguishes animated active and historical edges');
  check(await evaluate(` + "`" + `graph.querySelector('[data-key="agent:coder"]').getAttribute('aria-label').includes('online')&&graph.querySelector('[data-key="agent:reviewer"]').getAttribute('aria-label').includes('offline')` + "`" + `),'real worker heartbeat query communicates online and offline agents');
  for(const theme of ['light','dark']){
   await call('Emulation.setEmulatedMedia',{features:[{name:'prefers-color-scheme',value:theme}]});
   check(await evaluate(` + "`" + `(()=>{const stroke=s=>getComputedStyle(graph.querySelector(s)).stroke;const blue='rgb(59, 130, 246)';return stroke('[data-key="agent:coder"] circle')===blue&&stroke('[data-key="task:1"] .tg-progress-segment[data-filled="true"]')===blue&&stroke('[data-key="agent:reviewer"] circle')===stroke('[data-key="task:2"] .tg-progress-segment[data-filled="true"]')&&stroke('[data-key="agent:reviewer"] circle')===${theme==='dark'?"'rgb(228, 228, 231)'":"'rgb(24, 24, 27)'"}})()` + "`" + `),theme+' theme: inactive rings/agents neutral, live tasks/agents blue');
  }
  await call('Emulation.setEmulatedMedia',{features:[{name:'prefers-color-scheme',value:'light'}]});
  check(await evaluate(` + "`" + `graph.graph.nodes.find(n=>n.key==='task:6').radius<13&&graph.graph.nodes.find(n=>n.key==='task:1').radius===30` + "`" + `),'seventy-day inactive task shrinks below13 versus active30 radius');
  check(await evaluate(` + "`" + `(()=>{const old=graph.querySelector('[data-key="task:6"]'),fresh=graph.querySelector('[data-key="task:1"]'),scale=graph.graph.nodes.find(n=>n.key==='task:6').radius/30;return ['circle','.tg-count','.tg-ref','.tg-progress'].every(selector=>Math.abs(old.querySelector(selector).getBoundingClientRect().height-fresh.querySelector(selector).getBoundingClientRect().height*scale)<1.1&&Math.abs(old.querySelector(selector).getCTM().a/fresh.querySelector(selector).getCTM().a-scale)<.00001)&&old.querySelector('circle').getAttribute('r')==='30'&&getComputedStyle(old.querySelector('.tg-node-content')).transform.startsWith('matrix(')&&Math.abs(old.querySelector('circle').getCTM().e-old.getCTM().e)<.01&&Math.abs(old.querySelector('circle').getCTM().f-old.getCTM().f)<.01})()` + "`" + `),'CSS scales aged circle, counter, label and progress ring by identical factor around fixed node center');
  check(await evaluate(` + "`" + `graph.querySelectorAll('[data-key="task:1"] .tg-progress-segment[data-filled="true"]').length===1&&graph.querySelectorAll('[data-key="task:2"] .tg-progress-segment[data-filled="true"]').length===2&&!graph.querySelector('.tg-task .tg-state')` + "`" + `),'production status rings show planning1todo2 without task status text');
  check(await evaluate(` + "`" + `graph.querySelector('[data-key="task:1"] .tg-count').textContent==='1/1'&&graph.graph.nodes.find(n=>n.key==='task:6').radius<graph.graph.nodes.find(n=>n.key==='task:1').radius` + "`" + `),'real comments and status counts render inside inactivity-sized circles');
  check(await evaluate(` + "`" + `(()=>{const labels=[...graph.querySelectorAll('.tg-node')].map(g=>[...g.querySelectorAll('text')].map(t=>t.getBoundingClientRect()));for(let i=0;i<labels.length;i++)for(let j=0;j<i;j++)for(const a of labels[i])for(const b of labels[j])if(a.width&&b.width&&a.left<b.right&&a.right>b.left&&a.top<b.bottom&&a.bottom>b.top)return false;return true})()` + "`" + `),'thirty real tasks have zero overlapping compact labels');
  check(await evaluate(` + "`" + `(()=>{const n=k=>graph.graph.nodes.find(n=>n.key===k),d=(a,b)=>Math.hypot(n(a).x-n(b).x,n(a).y-n(b).y);return d('task:1','agent:coder')<d('task:5','agent:coder')&&d('task:1','task:5')<d('task:1','task:2')})()` + "`" + `),'real session relationships cluster with stronger active attraction');
  check(await evaluate(` + "`" + `JSON.stringify([...graph.querySelectorAll('.tg-controls button')].map(b=>b.dataset.action))===JSON.stringify(['in','out','heatmap'])` + "`" + `),'only plus/minus and Heatmap view buttons remain');
  await evaluate(` + "`" + `graph.querySelector('[data-tag-summary]').click();graph.querySelector('input[value="src/alpha"]').click()` + "`" + `);
  check(await evaluate(` + "`" + `graph.querySelectorAll('.tg-task').length===2&&graph.querySelectorAll('.tg-edge').length===2&&graph.querySelectorAll('.tg-agent').length===2&&graph.querySelector('.tg-tag-filter').open` + "`" + `),'one tag filters real tasks, edges and agents while dropdown remains open');
  await evaluate('board.refresh()');
  check(await evaluate(` + "`" + `graph.querySelector('input[value="src/alpha"]').checked&&graph.querySelectorAll('[data-tag-filter]').length===2&&graph.querySelectorAll('.tg-task').length===2` + "`" + `),'tag choice survives actual polling and all tag options remain available');
  await evaluate(` + "`" + `graph.querySelector('input[value="src/beta"]').click()` + "`" + `);
  check(await evaluate(` + "`" + `graph.querySelectorAll('.tg-task').length===3&&board.graphTags.length===2` + "`" + `),'multiple tags use union matching without duplicated task nodes');
  await evaluate(` + "`" + `graph.querySelector('input[value="src/alpha"]').click();graph.querySelector('input[value="src/beta"]').click()` + "`" + `);
  check(await evaluate(` + "`" + `graph.querySelectorAll('.tg-task').length===30&&board.graphTags.length===0&&graph.querySelector('[data-tag-all]').checked` + "`" + `),'unchecking every tag restores all unfinished tasks including untagged work');
  await evaluate(` + "`" + `graph.querySelector('input[value="src/alpha"]').click();graph.querySelector('[data-tag-all]').click();graph.querySelector('[data-tag-summary]').click()` + "`" + `);
  check(await evaluate(` + "`" + `graph.querySelectorAll('.tg-task').length===30&&board.graphTags.length===0` + "`" + `),'All tags option clears selection without an extra button');
  await evaluate(` + "`" + `graph.querySelector('[data-action="heatmap"]').click();graph.querySelector('[data-action="in"]').click();window.camera=graph.view.scale;` + "`" + `);
  check(await evaluate(` + "`" + `graph.querySelectorAll('[class*="tg-heat-"]').length===30` + "`" + `),'production heatmap colors all unfinished tasks');
  check(await evaluate(` + "`" + `graph.querySelector('[data-key="task:7"] circle').style.fill==='rgb(234, 179, 8)'&&graph.querySelector('[data-key="task:9"] circle').style.fill==='rgb(239, 68, 68)'&&graph.querySelector('[data-key="task:3"]').classList.contains('tg-heat-green')` + "`" + `),'real CLI comment totals map to green, yellow10 and red25 heatmap anchors');
  cli(['task','comment','TSK-6'],'Fresh activity revives the old task');
  await evaluate('board.refresh()');
  check(await evaluate(` + "`" + `graph===board.querySelector('task-agent-graph')&&graph.view.scale===camera&&graph.heatmap&&graph.graph.nodes.find(n=>n.key==='task:6').radius===30&&graph.graph.nodes.find(n=>n.key==='task:6').comments===1` + "`" + `),'real CLI comment crosses SQL/API boundary and revives old task while camera and heatmap persist');
  cli(['task','status','TSK-2','validated']);await evaluate('board.refresh()');
  check(await evaluate(` + "`" + `!graph.querySelector('[data-key="task:2"]')&&!graph.graph.edges.some(e=>e.source==='task:2')` + "`" + `),'real CLI validation removes task and links after production refresh');
  await evaluate(` + "`" + `graph.querySelector('[data-key="task:1"]').dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}))` + "`" + `);
  await until(()=>evaluate(` + "`" + `board.dialog.open&&board.querySelector('#dialog-title').textContent.includes('TSK-1')&&board.dialog.textContent.includes('Actual comment from CLI')` + "`" + `));
  check(await evaluate(` + "`" + `board.dialog.textContent.includes('Actual comment from CLI')` + "`" + `),'task node opens real API details and stored comment');await evaluate('board.dialog.close()');
  // Native trusted mouse events exercise actual pointer capture, not a stub.
  await evaluate(` + "`" + `graph.querySelector('[data-action="out"]').click();graph.settle()` + "`" + `);
  const point=await evaluate(` + "`" + `(()=>{const r=graph.querySelector('[data-key="task:1"] circle').getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()` + "`" + `);
  await call('Input.dispatchMouseEvent',{type:'mousePressed',...point,button:'left',clickCount:1});
  await call('Input.dispatchMouseEvent',{type:'mouseMoved',x:point.x+35,y:point.y+20,button:'left',buttons:1});
  await call('Input.dispatchMouseEvent',{type:'mouseReleased',x:point.x+35,y:point.y+20,button:'left',clickCount:1});
  check(await evaluate(` + "`" + `graph.pinned.has('task:1')` + "`" + `),'native browser pointer capture creates client-only drag pin');
  await evaluate('board.refresh()');check(await evaluate(` + "`" + `graph.pinned.has('task:1')` + "`" + `),'client pin survives real API polling');
  check(requests.filter(r=>r.url.startsWith(base+'/api/')).every(r=>r.method==='GET'),'all browser graph actions issue reads only, no layout database writes');
  releaseLease();await evaluate('board.refresh()');
  check(await evaluate(` + "`" + `!graph.querySelector('.tg-active')&&graph.graph.edges.some(e=>e.source==='task:1')` + "`" + `),'release of real session lease stops active animation but retains history link');
  sql(` + "`" + `SET search_path TO "${schemas[0]}"; UPDATE workers SET last_seen=now()-interval '1 hour';` + "`" + `);await evaluate('board.refresh()');
  check(await evaluate(` + "`" + `graph.querySelector('[data-key="agent:coder"]').getAttribute('aria-label').includes('offline')` + "`" + `),'expired actual heartbeat changes agent to offline on refresh');
  await call('Page.reload');await until(()=>evaluate(` + "`" + `document.querySelector('task-agent-graph')?.graph?.nodes.filter(n=>n.kind==='task').length===29` + "`" + `));
  check(await evaluate(` + "`" + `document.querySelector('task-agent-graph').pinned.size===0` + "`" + `),'full production page reload discards local drag pins');
  await evaluate(` + "`" + `window.board=document.querySelector('looster-board');board.querySelector('task-agent-graph [data-tag-summary]').click();board.querySelector('input[value="src/alpha"]').click();board.switchNamespace(${JSON.stringify(other)})` + "`" + `);
  await until(()=>evaluate(` + "`" + `document.querySelector('task-agent-graph')?.querySelectorAll('.tg-task').length===1` + "`" + `));
  check(await evaluate(` + "`" + `board.graphTags.length===0&&board.data.tasks[0].name==='Other namespace only'&&board.querySelector('task-agent-graph').pinned.size===0` + "`" + `),'real namespace dropdown boundary loads isolated PostgreSQL tasks with fresh client layout');
  check(requests.some(r=>r.url===base+'/vendor/d3.v7.9.0.min.js')&&!requests.some(r=>!r.url.startsWith(base)&&/^https?:/.test(r.url)),'compiled executable serves vendored D3 with no CDN or JS build server');
`
