import test from 'node:test';
import assert from 'node:assert/strict';
import {renderHealthRow,replaceAccountRows} from '../../internal/console/web/console.js';

function dom(){
  let document;const all=[];
  const createElement=tag=>{
    const el={tagName:tag.toUpperCase(),children:[],attrs:{},dataset:{},style:{},className:'',hidden:false,tabIndex:0,handlers:{},_text:null};all.push(el);
    Object.defineProperty(el,'textContent',{get:()=>el._text??el.children.map(c=>c.textContent??'').join(''),set:v=>{el._text=String(v);el.children=[]}});
    el.append=(...xs)=>{el.children.push(...xs);for(const x of xs)x.parentNode=el};
    el.replaceChildren=(...xs)=>{el.children=[];el.append(...xs);document.activeElement=document.body};
    el.contains=target=>el===target||el.children.some(child=>child.contains?.(target));
    el.closest=selector=>selector==='.health-block'&&el.className.split(/\s+/).includes('health-block')?el:el.parentNode?.closest?.(selector)??null;
    el.querySelectorAll=selector=>{const out=[];const walk=n=>{for(const child of n.children){if(selector==='.health-block'&&child.className.split(/\s+/).includes('health-block'))out.push(child);walk(child)}};walk(el);return out};
    el.setAttribute=(k,v)=>{el.attrs[k]=String(v)};el.getAttribute=k=>el.attrs[k]??null;
    el.addEventListener=(kind,fn)=>{(el.handlers[kind]??=[]).push(fn)};
    el.dispatch=(kind,event={})=>{for(const fn of el.handlers[kind]??[])fn({preventDefault(){},...event})};
    el.getBoundingClientRect=()=>({left:20,right:240,top:50,bottom:80,width:220,height:30});
    el.focus=()=>{document.activeElement=el;el.dispatch('focus')};
    return el;
  };
  document={createElement,activeElement:null,defaultView:{innerWidth:390,innerHeight:844},addEventListener(){},body:null};document.body=createElement('body');
  return {document,all};
}

const start=1794816000;
function health(overrides={}){
  const buckets=Array.from({length:48},(_,i)=>({start:start+i*1800,success:0,failed:0}));
  buckets[0].success=3;buckets[1].success=1;buckets[1].failed=1;buckets[2].failed=2;
  return {account_id:'a',start,end:start+86400,buckets,success:4,failed:3,complete:true,truncated:false,stale:false,synced_at:'2026-11-17T08:00:00Z',checked_at:'2026-11-17T08:00:00Z',...overrides};
}
const account={account_id:'a',label:'账号 <img src=x onerror=alert(1)>',username:'user'};
const byClass=(all,name)=>all.filter(el=>el.className.split(/\s+/).includes(name));

test('TestHealthRowSpansTheWholeAccountTable',()=>{
  const {document,all}=dom();const row=renderHealthRow(document,account,health());
  assert.equal(row.tagName,'TR');assert.equal(row.className,'account-health-row');assert.equal(row.dataset.accountId,'a');
  assert.equal(row.children.length,1);assert.equal(row.children[0].colSpan,7);
  const blocks=byClass(all,'health-block');assert.equal(blocks.length,48);
  assert.equal(blocks[0].dataset.state,'success');assert.equal(blocks[1].dataset.state,'mixed');assert.equal(blocks[2].dataset.state,'failure');assert.equal(blocks[3].dataset.state,'idle');
  assert.ok(row.textContent.includes('57.1%'),row.textContent);assert.ok(row.textContent.includes('基于站点日志'));
  assert.ok(blocks[0].getAttribute('aria-label').includes('消费 3'));
  assert.ok(row.children[0].getAttribute('aria-label').includes('<img'));
  assert.ok(all.every(el=>el.innerHTML===undefined),'untrusted account label used HTML');
});

test('TestHealthEndpointsDoNotRepeatTimeRanges',()=>{
  const {document,all}=dom();renderHealthRow(document,account,health());
  const endpoints=byClass(all,'health-time');assert.equal(endpoints.length,2);
  assert.ok(!endpoints[0].textContent.includes('–'),'start endpoint repeats its own range');
  assert.ok(!endpoints[1].textContent.includes('–'),'end endpoint repeats its own range');
});

test('TestAccountPollingPreservesHealthKeyboardFocus',()=>{
  const {document,all}=dom(),body=document.createElement('tbody');
  const first=renderHealthRow(document,account,health());body.append(first);
  const old=byClass(all,'health-block')[17];old.focus();
  const second=renderHealthRow(document,account,health());
  replaceAccountRows(document,body,[second]);
  const blocks=second.querySelectorAll('.health-block');
  assert.equal(document.activeElement,blocks[17],'polling lost the focused time bucket');
  assert.equal(blocks[17].tabIndex,0);assert.ok(!byClass(all,'health-tooltip').at(-1).hidden);
  replaceAccountRows(document,body,[]);assert.equal(body.children.length,0,'account deletion left a focus target behind');
});

test('TestHealthRowIdleDoesNotClaim100Percent',()=>{
  const {document,all}=dom();const h=health({success:0,failed:0});h.buckets.forEach(b=>{b.success=0;b.failed=0});
  const row=renderHealthRow(document,account,h);
  assert.ok(row.textContent.includes('暂无请求记录'));assert.ok(!row.textContent.includes('100%'));
  assert.ok(byClass(all,'health-block').every(b=>b.dataset.state==='idle'));
});

test('TestHealthRowMissingPartialAndMalformedAreUnknown',()=>{
  for(const h of [undefined,health({complete:false,error:'permission_denied'}),health({complete:false,truncated:true,error:'health_truncated'}),health({buckets:[]}),health({buckets:Array(48).fill(null)}),health({success:-1})]){
    const {document,all}=dom();const row=renderHealthRow(document,account,h);
    assert.equal(byClass(all,'health-block').length,48);
    assert.ok(byClass(all,'health-block').every(b=>b.dataset.state==='unknown'));
    assert.ok(!row.textContent.includes('57.1%'),'incomplete data rendered a success rate');
    assert.ok(!row.textContent.includes('100%'));assert.ok(!row.textContent.includes('Infinity'));assert.ok(!row.textContent.includes('NaN'));
  }
});

test('TestHealthRowStaleRetainsItsOriginalWindowWithWarning',()=>{
  const {document,all}=dom();const row=renderHealthRow(document,account,health({stale:true,error:'network_error',checked_at:'2026-11-17T08:05:00Z'}));
  assert.ok(row.textContent.includes('过期'));assert.ok(row.textContent.includes('上次'));assert.ok(row.textContent.includes('57.1%'));
  assert.equal(byClass(all,'health-block')[0].dataset.state,'success');
  assert.equal(byClass(all,'health-block')[0].dataset.start,String(start));
});

test('TestHealthTooltipSupportsMouseKeyboardAndTouch',()=>{
  const {document,all}=dom();renderHealthRow(document,account,health());
  const blocks=byClass(all,'health-block'),tip=byClass(all,'health-tooltip')[0];
  assert.equal(tip.hidden,true);blocks[1].dispatch('pointerenter',{pointerType:'mouse'});
  assert.equal(tip.hidden,false);assert.ok(tip.textContent.includes('消费 1'));assert.ok(tip.textContent.includes('错误 1'));assert.ok(tip.textContent.includes('50.0%'));
  blocks[1].dispatch('pointerleave',{pointerType:'mouse'});assert.equal(tip.hidden,true);
  blocks[0].focus();assert.equal(tip.hidden,false);
  blocks[0].dispatch('keydown',{key:'ArrowRight'});assert.equal(document.activeElement,blocks[1]);
  blocks[1].dispatch('keydown',{key:'End'});assert.equal(document.activeElement,blocks[47]);
  blocks[47].dispatch('keydown',{key:'Home'});assert.equal(document.activeElement,blocks[0]);
  blocks[0].dispatch('keydown',{key:'Escape'});assert.equal(tip.hidden,true);
  blocks[2].dispatch('pointerdown',{pointerType:'touch'});assert.equal(tip.hidden,false);assert.ok(tip.textContent.includes('错误 2'));
  blocks[2].dispatch('pointerdown',{pointerType:'touch'});assert.equal(tip.hidden,true);
});

test('TestModelsSectionRendersBelowStatusBar',()=>{
  const {document,all}=dom();
  const row=renderHealthRow(document,account,health(),{account_id:'a',models:['gpt-4o','claude-3-7 <img src=x onerror=alert(1)>','deepseek-chat'],complete:true,stale:false,synced_at:'2026-11-17T08:00:00Z',checked_at:'2026-11-17T08:00:00Z'});
  const wrap=row.children[0].children[0];
  const modelsBlock=byClass(all,'health-models')[0];
  assert.equal(wrap.children.at(-1),modelsBlock,'models block is not the last element below the status bar');
  assert.ok(wrap.children.indexOf(byClass(all,'health-foot')[0])<wrap.children.indexOf(modelsBlock),'models block is not below the status bar');
  assert.ok(modelsBlock.textContent.includes('可用模型'));assert.ok(modelsBlock.textContent.includes('3 个'));
  const chips=byClass(all,'model-chip');
  assert.equal(chips.length,3);
  assert.equal(chips[0].textContent,'gpt-4o');
  assert.equal(chips[1].title,'claude-3-7 <img src=x onerror=alert(1)>');
  assert.ok(all.every(el=>el.innerHTML===undefined),'model names used HTML');
});

test('TestModelsUnknownWaitingEmptyAndStaleStates',()=>{
  for(const [models,expected,noChips] of [
    [undefined,'等待刷新',true],
    [null,'等待刷新',true],
    [{models:[],complete:false,error:'permission_denied'},'数据不可确认',true],
    [{models:['gpt-4o',null],complete:true},'数据不可确认',true],
    [{models:[],complete:true,synced_at:'2026-11-17T08:00:00Z'},'站点未返回可用模型',true],
    [{models:['gpt-4o'],complete:true,stale:true,synced_at:'2026-11-17T08:00:00Z'},'已过期',false],
  ]){
    const {document,all}=dom();const row=renderHealthRow(document,account,health(),models);
    const block=byClass(all,'health-models')[0];
    assert.ok(block.textContent.includes(expected),`${JSON.stringify(models)} missing "${expected}"`);
    assert.equal(byClass(all,'model-chip').length===0,noChips,`unexpected chips for ${JSON.stringify(models)}`);
    assert.equal(byClass(all,'health-block').length,48,'models payload broke the status bar');
    assert.ok(!row.textContent.includes('NaN')&&!row.textContent.includes('Infinity'));
  }
});
