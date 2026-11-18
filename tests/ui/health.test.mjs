import test from 'node:test';
import assert from 'node:assert/strict';
import {createHealthController,createApiController} from '../../internal/console/web/console.js';

const tick=()=>new Promise(r=>setImmediate(r));
function fixture(request){
  const timers=new Map();let next=0,now=1000000;const data=[],errors=[];
  const options={request,onData:value=>data.push(value),onError:e=>errors.push(e),now:()=>now,setTimeout:(fn,ms)=>{timers.set(++next,{fn,ms});return next},clearTimeout:id=>timers.delete(id)};
  const controller=createHealthController(options);
  return {controller,timers,data,errors,advance:ms=>{now+=ms},fire:async()=>{const [id,timer]=timers.entries().next().value;timers.delete(id);timer.fn();await tick()}};
}

test('TestHealthAutoRefreshRequiresVisibleAuthenticatedAccounts',async()=>{
  const seen=[];const f=fixture(async(path,init)=>{seen.push([path,init]);return {health:{accounts:{},refreshing:true}}});
  f.controller.setAccounts(['a']);await tick();assert.equal(seen.length,0);
  f.controller.setVisible(false);f.controller.setAuthenticated(true);await tick();assert.equal(seen.length,0);
  f.controller.setVisible(true);await tick();assert.equal(seen.length,1);
  assert.equal(seen[0][0],'/health');assert.equal(seen[0][1].method,'POST');assert.deepEqual(seen[0][1].body,{refresh:false});
  assert.equal(f.data[0].health.refreshing,true);assert.equal(f.data[0].models,undefined);assert.equal([...f.timers.values()][0].ms,300000);
  f.controller.stop();assert.equal(f.timers.size,0);
});

test('TestHealthAutomaticRefreshIsLimitedToFiveMinutes',async()=>{
  let hits=0;const f=fixture(async()=>{hits++;return {health:{accounts:{},refreshing:false}}});
  f.controller.setAuthenticated(true);f.controller.setAccounts(['a']);await tick();
  assert.equal(hits,1);f.controller.setAccounts(['a']);await tick();assert.equal(hits,1);
  f.advance(300000);await f.fire();assert.equal(hits,2);
  await f.controller.refresh(true);assert.equal(hits,3);
  f.controller.setAccounts([]);assert.equal(f.timers.size,0);await f.controller.refresh(true);assert.equal(hits,3);
  f.controller.stop();
});

test('TestHealthPendingRefreshDeduplicatesAndStopsOnAuthLoss',async()=>{
  let release,hits=0,signal;const f=fixture(async(path,init)=>{hits++;signal=init.signal;return new Promise(r=>{release=r})});
  f.controller.setAuthenticated(true);f.controller.setAccounts(['a']);await tick();
  const pending=f.controller.refresh(true);assert.equal(hits,1);
  f.controller.setAuthenticated(false);assert.equal(signal.aborted,true);assert.equal(f.timers.size,0);
  release({health:{accounts:{},refreshing:true}});await pending;await tick();
  assert.equal(f.data.length,0);assert.equal(f.timers.size,0);
  f.controller.stop();
});

test('TestHealthHiddenRefreshAbortsAndResumeChecksCache',async()=>{
  let resolve,hits=0,signal;const f=fixture((path,init)=>{hits++;signal=init.signal;return hits===1?new Promise(r=>{resolve=r}):Promise.resolve({health:{accounts:{},refreshing:false}})});
  f.controller.setAuthenticated(true);f.controller.setAccounts(['a']);await tick();
  f.controller.setVisible(false);assert.equal(signal.aborted,true);
  resolve({health:{accounts:{},refreshing:true}});await tick();assert.equal(f.data.length,0);
  f.controller.setVisible(true);await tick();assert.equal(hits,2);assert.equal(f.data.length,1);
  f.controller.stop();
});

test('TestHealthRefreshFailureRetriesLaterNotContinuously',async()=>{
  let hits=0;const f=fixture(async()=>{hits++;throw Object.assign(new Error('network'),{kind:'network_error'})});
  f.controller.setAuthenticated(true);f.controller.setAccounts(['a']);await tick();
  assert.equal(hits,1);assert.equal(f.errors.length,1);assert.equal([...f.timers.values()][0].ms,300000);
  f.controller.setAccounts(['a']);await tick();assert.equal(hits,1);
  f.controller.stop();
});

function apiFixture(fetch){const timers=new Map();let next=0;return {fetch,setTimeout:(fn,ms)=>{timers.set(++next,{fn,ms});return next},clearTimeout:id=>timers.delete(id),timers}}
const ok=data=>({status:200,ok:true,json:async()=>({success:true,data})});

test('TestHealthRequestValidationAndRefreshingPollInterval',async()=>{
  const seen=[];const options=apiFixture(async(url,init)=>{seen.push([url,init]);return ok({jobs:{},health:{accounts:{},refreshing:true}})});
  const api=createApiController(options);await api.setKey('test-key');
  assert.equal([...options.timers.values()][0].ms,3000,'in-flight health refresh should update cached status promptly');
  await api.request('/health',{method:'POST',body:{refresh:true}});
  assert.deepEqual(JSON.parse(seen.at(-1)[1].body),{refresh:true});
  await assert.rejects(api.request('/health?refresh=1'),e=>e.kind==='invalid_input');
  await assert.rejects(api.request('/health',{method:'DELETE'}),e=>e.kind==='invalid_input');
  await assert.rejects(api.request('/health',{method:'POST',body:{site:'https://other.test'}}),e=>e.kind==='invalid_input');
  await assert.rejects(api.request('/health',{method:'POST',body:{refresh:1}}),e=>e.kind==='invalid_input');
  api.stop();
});

test('TestCanceledQueuedHealthRequestDoesNotFetch',async()=>{
  let release;const seen=[];const options=apiFixture((url)=>{seen.push(url);return seen.length===1?new Promise(r=>{release=()=>r(ok({jobs:{}}))}):Promise.resolve(ok({health:{accounts:{}}}))});
  const api=createApiController(options);const connecting=api.setKey('test-key');await tick();
  const abort=new AbortController();const health=api.request('/health',{method:'POST',body:{refresh:false},signal:abort.signal}).catch(e=>e);
  abort.abort();release();await connecting;const result=await health;
  assert.equal(result.kind,'request_stopped');assert.equal(seen.length,1,'hidden queued refresh still sent a request');
  api.stop();
});
