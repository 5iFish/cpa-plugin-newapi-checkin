const API_BASE = '/v0/management/plugins/newapi-checkin';
const STORAGE_KEY = 'newapi-checkin:auth';
export const renderText = (node,value) => { node.textContent = String(value ?? ''); return node; };
export const clearSensitiveFields = form => { for (const input of form.querySelectorAll('input[type="password"],textarea[data-secret]')) input.value = ''; };
// Credential channels a bind dialog may offer: standard check-in sites can use
// a token, local password, or the site's own refresh cookie; login-reward sites
// need a credential the server can act on daily (token, refresh cookie, or a
// third-party session bootstrap) and reject password binds.
export const allowedChannels = mode => mode === 'login' ? ['token', 'refresh_cookie', 'oauth'] : ['token', 'password', 'refresh_cookie'];
// The site string is normalized to https by the plugin before it reaches the
// console; parsing it again keeps a corrupted state file from turning the
// link into anything other than an http(s) navigation.
export function renderSiteLink(doc,url){
  const link=doc.createElement('a');
  renderText(link,url);
  link.className='secondary site site-link';
  try{const parsed=new URL(String(url));if(parsed.protocol==='https:'||parsed.protocol==='http:'){link.href=parsed.href;link.target='_blank';link.rel='noopener noreferrer'}}catch{}
  return link;
}
const failure = kind => Object.assign(new Error(kind),{kind});

export function createHealthController(options={}) {
  const later=options.setTimeout??globalThis.setTimeout,clear=options.clearTimeout??globalThis.clearTimeout,now=options.now??Date.now;
  const interval=300000;
  let authenticated=false,visible=options.visible??true,stopped=false,ids='',epoch=0,last=null,timer=null,pending=null,controller=null;
  const active=()=>authenticated&&visible&&!stopped&&!!ids;
  function clearTimer(){if(timer!==null)clear(timer);timer=null}
  function invalidate(){epoch++;last=null;clearTimer();controller?.abort()}
  function schedule(){clearTimer();if(!active()||pending)return;const age=last===null?interval:now()-last;timer=later(()=>{timer=null;void refresh(false)},Math.max(0,interval-age))}
  function auto(){if(!active()){clearTimer();return}if(pending)return;if(last===null||now()-last>=interval||now()<last)void refresh(false);else schedule()}
  function refresh(force=false){
    if(!active())return Promise.resolve();if(pending)return pending;
    if(!force&&last!==null&&now()>=last&&now()-last<interval){schedule();return Promise.resolve()}
    clearTimer();const generation=epoch,c=new AbortController();controller=c;last=now();
    pending=Promise.resolve().then(()=>options.request('/health',{method:'POST',body:{refresh:!!force},signal:c.signal})).then(r=>{
      if(generation!==epoch||!active())return;if(!r?.health)throw failure('invalid_response');options.onData?.({health:r.health,models:r.models});
    }).catch(e=>{if(generation!==epoch||e.name==='AbortError'||e.kind==='request_stopped')return;if(e.kind==='management_auth_required'){authenticated=false;invalidate()}else options.onError?.(e)}).finally(()=>{pending=null;controller=null;if(last===null)auto();else schedule()});
    return pending;
  }
  return {refresh,setAuthenticated(value){authenticated=!!value;if(!authenticated)invalidate();else auto()},setVisible(value){const next=!!value;if(next===visible)return;visible=next;if(!visible)invalidate();else auto()},setAccounts(accounts){const next=[...new Set(accounts??[])].sort().join(',');if(next!==ids){ids=next;invalidate()}auto()},stop(){stopped=true;invalidate()}};
}

const healthCount=n=>Number.isSafeInteger(n)&&n>=0;
const healthColor=rate=>{const stops=[[239,68,68],[250,204,21],[34,197,94]],segment=rate<0.5?0:1,t=segment===0?rate*2:(rate-0.5)*2;return 'rgb('+stops[segment].map((v,i)=>Math.round(v+(stops[segment+1][i]-v)*t)).join(', ')+')'};

// A colspan row keeps the timeline below the account without squeezing it into
// the identity column. All account and log labels are rendered as plain text.
export function replaceAccountRows(doc,body,rows){
  const active=doc.activeElement?.closest?.('.health-block');
  const focus=active&&body.contains(active)?{id:active.dataset.accountId,index:active.dataset.index}:null;
  body.replaceChildren(...rows);
  if(focus){const target=body.querySelectorAll('.health-block');for(const block of target)if(block.dataset.accountId===focus.id&&block.dataset.index===focus.index){block.focus();break}}
}

export function renderHealthRow(doc,account,health,models){
  const node=(tag,text,cls)=>{const el=doc.createElement(tag);if(text!==undefined)renderText(el,text);if(cls)el.className=cls;return el};
  const h=health??{},windowValid=Number.isSafeInteger(h.start)&&h.start>0&&h.end-h.start===86400;
  const countsValid=windowValid&&Array.isArray(h.buckets)&&h.buckets.length===48&&healthCount(h.success)&&healthCount(h.failed)&&h.buckets.every((b,i)=>b&&b.start===h.start+i*1800&&healthCount(b.success)&&healthCount(b.failed))&&h.buckets.reduce((n,b)=>n+b.success,0)===h.success&&h.buckets.reduce((n,b)=>n+b.failed,0)===h.failed;
  const known=countsValid&&h.complete===true&&(!h.error||h.stale),total=known?h.success+h.failed:0,rate=total?((h.success/total)*100).toFixed(1)+'%':null;
  const start=windowValid?h.start:Math.floor(Date.now()/1000)-86400,end=start+86400;
  const row=node('tr',undefined,'account-health-row');row.dataset.accountId=account.account_id;
  const cell=node('td',undefined,'health-cell');cell.colSpan=7;cell.setAttribute('aria-label',`${account.label||account.username||'账号'} · 近 24 小时请求状态与可用模型`);row.append(cell);
  const wrap=node('div',undefined,'account-health'+(h.stale?' is-stale':'')),head=node('div',undefined,'health-head');cell.append(wrap);wrap.append(head);
  head.append(node('span','近 24 小时请求状态','health-title'));
  let summary=known?(rate?`记录成功率 ${rate}`:'暂无请求记录'):(health?'数据不可确认':'等待刷新');
  if(h.stale&&known)summary=`上次 ${summary} · 已过期`;
  head.append(node('span',summary,'health-summary'));
  const meta=health?(h.error?message(h.error)+' · ':'')+(h.synced_at&&!String(h.synced_at).startsWith('0001-')?'更新于 '+when(h.synced_at):'尚未读取到有效日志'):'连接后自动读取站点日志';
  head.append(node('span',meta,'health-meta'));
  const track=node('div',undefined,'health-track');track.setAttribute('role','group');track.setAttribute('aria-label','24 小时请求记录，使用左右方向键查看');wrap.append(track);
  const tip=node('div',undefined,'health-tooltip');tip.id='health-tip-'+String(account.account_id??'account').replace(/[^a-zA-Z0-9_-]/g,'_');tip.setAttribute('role','tooltip');tip.hidden=true;wrap.append(tip);
  const blocks=[];let selected=-1;
  const range=(from,to)=>`${new Date(from*1000).toLocaleString('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false})} – ${new Date(to*1000).toLocaleString('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false})}`;
  function hide(){tip.hidden=true;selected=-1}
  function show(i){
    selected=i;const b=countsValid?h.buckets[i]:{success:0,failed:0},n=b.success+b.failed;
    const text=known?(n?`消费 ${b.success} · 错误 ${b.failed} · 记录成功率 ${(b.success/n*100).toFixed(1)}%`:'暂无请求记录'):(countsValid?`数据不完整 · 已读取消费 ${b.success} · 错误 ${b.failed}`:'暂无可确认的数据');
    renderText(tip,`${range(start+i*1800,start+(i+1)*1800)}\n${text}${h.stale?'\n上次结果已过期':''}`);tip.hidden=false;
    const rect=blocks[i].getBoundingClientRect(),size=tip.getBoundingClientRect(),view=doc.defaultView;
    const width=view?.innerWidth??1024,height=view?.innerHeight??768;
    tip.style.left=Math.max(8,Math.min(rect.left,width-size.width-8))+'px';
    tip.style.top=Math.max(8,Math.min(rect.top>size.height+12?rect.top-size.height-8:rect.bottom+8,height-size.height-8))+'px';
  }
  for(let i=0;i<48;i++){
    const b=countsValid?h.buckets[i]:{success:0,failed:0},n=b.success+b.failed;
    const state=!known?'unknown':!n?'idle':!b.failed?'success':!b.success?'failure':'mixed';
    const block=node('button',undefined,'health-block');block.type='button';block.tabIndex=i===0?0:-1;block.dataset.state=state;block.dataset.start=String(start+i*1800);block.dataset.accountId=account.account_id;block.dataset.index=String(i);
    block.setAttribute('aria-label',`${range(start+i*1800,start+(i+1)*1800)} · ${known?(n?`消费 ${b.success} · 错误 ${b.failed}`:'暂无请求记录'):'数据不可确认'}`);block.setAttribute('aria-describedby',tip.id);
    if(known&&n)block.style.backgroundColor=healthColor(b.success/n);
    block.addEventListener('pointerenter',e=>{if(e.pointerType==='mouse')show(i)});block.addEventListener('pointerleave',e=>{if(e.pointerType==='mouse')hide()});
    block.addEventListener('pointerdown',e=>{if(e.pointerType==='touch'){e.preventDefault();if(selected===i&&!tip.hidden)hide();else show(i)}});
    block.addEventListener('focus',()=>{blocks.forEach((x,j)=>{x.tabIndex=j===i?0:-1});show(i)});block.addEventListener('blur',hide);
    block.addEventListener('keydown',e=>{let target;if(e.key==='ArrowRight')target=Math.min(47,i+1);else if(e.key==='ArrowLeft')target=Math.max(0,i-1);else if(e.key==='Home')target=0;else if(e.key==='End')target=47;else if(e.key==='Escape'){hide();return}if(target!==undefined){e.preventDefault();blocks[target].focus()}});
    blocks.push(block);track.append(block);
  }
  const endpoint=ts=>new Date(ts*1000).toLocaleString('zh-CN',{month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',hour12:false});
  const foot=node('div',undefined,'health-foot');foot.append(node('span',endpoint(start),'health-time'),node('span','基于站点日志 · 错误可能包含重试或漏记','health-source'),node('span',endpoint(end),'health-time'));wrap.append(foot);
  // Available models sit below the status bar, inside the same full-width
  // cell. Names are rendered as text-only chips with the full name on hover.
  const m=models??{};
  const listKnown=Array.isArray(m.models)&&m.models.every(name=>typeof name==='string')&&m.complete===true;
  const modelsBlock=node('div',undefined,'health-models');
  const modelsHead=node('div',undefined,'health-models-head');
  modelsHead.append(node('span','可用模型','health-title'));
  let modelsSummary=listKnown?(m.models.length?`${m.models.length} 个`:'站点未返回可用模型'):(models?'数据不可确认':'等待刷新');
  if(m.stale&&listKnown)modelsSummary+=' · 已过期';
  modelsHead.append(node('span',modelsSummary,'health-summary'));
  modelsHead.append(node('span',(m.error?message(m.error)+' · ':'')+(m.synced_at&&!String(m.synced_at).startsWith('0001-')?'更新于 '+when(m.synced_at):'尚未读取到模型列表'),'health-meta'));
  modelsBlock.append(modelsHead);
  if(listKnown&&m.models.length){
    const chips=node('div',undefined,'model-chips');
    for(const name of m.models){const chip=node('span',name,'model-chip');chip.title=name;chips.append(chip)}
    modelsBlock.append(chips);
  }
  wrap.append(modelsBlock);
  return row;
}

export function createApiController(options={}) {
  const fetcher=options.fetch ?? globalThis.fetch.bind(globalThis);
  const storage=options.storage;
  const later=options.setTimeout ?? globalThis.setTimeout;
  const clear=options.clearTimeout ?? globalThis.clearTimeout;
  let key='',epoch=0,stopped=false,visible=true,timer=null,queue=Promise.resolve(),polling=null,activeJobs=false;
  const controllers=new Set();
  let pollController;
  function clearTimer(){ if(timer!==null)clear(timer);timer=null; }
  function halt(){epoch++;key='';clearTimer();for(const c of controllers)c.abort();try{storage?.removeItem(STORAGE_KEY)}catch{};options.onAuth?.(false);}
  function request(path,{method='GET',body,poll=false,signal}={}) {
    const generation=epoch;
    const task=async()=>{
      if(stopped||!key||generation!==epoch||signal?.aborted||(poll&&!visible))throw failure('request_stopped');
      if(!/^\/(?:[a-z]+(?:\?(?:id=[a-f0-9]+|provider=(?:github|linuxdo)))?|stats\?range=(?:24h|7d|30d)(?:&refresh=1)?)$/.test(path))throw failure('invalid_input');
      if(path.startsWith('/health')&&(path!=='/health'||!['GET','POST'].includes(method)||(method==='GET'&&body!==undefined)||(method==='POST'&&(!body||typeof body!=='object'||Array.isArray(body)||Object.keys(body).some(k=>k!=='refresh')||(body.refresh!==undefined&&typeof body.refresh!=='boolean')))))throw failure('invalid_input');
      const c=new AbortController();controllers.add(c);if(poll)pollController=c;
      const abort=()=>c.abort();signal?.addEventListener('abort',abort,{once:true});
      try {
        const res=await fetcher(API_BASE+path,{method,headers:{Authorization:'Bearer '+key,'Content-Type':'application/json'},body:body===undefined?undefined:JSON.stringify(body),signal:c.signal,cache:'no-store',redirect:'error'});
        if(res.status===401||res.status===403){halt();throw failure('management_auth_required');}
        let value;try{value=await res.json()}catch{throw failure('invalid_response')}
        if(!res.ok||value.success!==true)throw failure(value.error?.kind ?? 'unknown_error');
        if(generation!==epoch)throw failure('request_stopped');
        return value.data;
      } finally {controllers.delete(c);signal?.removeEventListener('abort',abort);if(pollController===c)pollController=undefined;}
    };
    const result=queue.then(task);queue=result.catch(()=>{});return result;
  }
  function schedule(){clearTimer();if(!stopped&&visible&&key)timer=later(()=>{timer=null;void poll()},activeJobs?3000:30000);}
  function poll(){
    if(stopped||!key||!visible)return Promise.resolve();if(polling)return polling;clearTimer();
    polling=request('/status',{poll:true}).then(data=>{
      activeJobs=data.health?.refreshing===true||Object.values(data.jobs??{}).some(j=>j.status==='queued'||j.status==='running');options.onStatus?.(data);
    }).catch(e=>{if(e.name!=='AbortError'&&e.kind!=='request_stopped'&&e.kind!=='management_auth_required')options.onError?.(e)}).finally(()=>{polling=null;schedule()});
    return polling;
  }
  async function setKey(value){
    epoch++;clearTimer();for(const c of controllers)c.abort();key=String(value).trim();stopped=false;
    if(!key||/[\r\n]/.test(key)){halt();return}
    try{storage?.setItem(STORAGE_KEY,JSON.stringify({version:1,key}))}catch{}
    options.onAuth?.(true);if(polling)await polling;return poll();
  }
  return {
    request,poll,setKey,
    get authenticated(){return !!key&&!stopped},
    start(){let candidate;try{candidate=JSON.parse(storage?.getItem(STORAGE_KEY)??'null')}catch{};if(candidate?.version===1&&typeof candidate.key==='string')return setKey(candidate.key);options.onAuth?.(false);return Promise.resolve()},
    setVisible(value){visible=!!value;clearTimer();if(!visible){pollController?.abort();return Promise.resolve()};return poll()},
    stop(){stopped=true;epoch++;clearTimer();for(const c of controllers)c.abort()},
    logout(){halt()}
  };
}

const labels={health_timeout:'读取日志超时，稍后重试',health_truncated:'日志超过读取上限，数据不完整',health_invalidated:'账号已变更，请重新刷新',user_id_required:'站点要求与令牌匹配的用户 ID：请在站点个人设置查看该账号的数字 ID 后填入绑定表单（填错或留空都会被拒绝）',ready:'可自动签到',success:'签到成功',already:'已签到',network_error:'连接失败，可重试',rate_limited:'站点限流，等待重试',upstream_error:'站点暂时不可用',result_unknown:'正在确认签到结果',credential_expired:'凭证已到期，需要重新绑定',legacy_retired:'旧令牌已退役，请使用 PAT',permission_denied:'权限不足，请检查 profile:read、wallet:read、wallet:write',refresh_outcome_unknown:'刷新结果不确定，请用新会话重新绑定',persistence_error:'新凭证保存失败，已暂停账号',credential_unreadable:'凭证文件无法读取',external_change:'凭证被外部修改，请重新检查',turnstile_blocked:'签到需要人机验证，无法自动执行',not_enabled:'站点未开启签到',origin_forbidden:'站点拒绝刷新来源，请检查反向代理',unsupported_site:'站点接口不符合要求',unknown_error:'操作未完成，请重新检查',login_verification_required:'登录需要二次验证，请使用 PAT 或专用会话',invalid_input:'输入无效，请检查填写内容',identity_mismatch:'新凭证属于其他账号，未覆盖原账号',session_already_managed:'此会话已被管理或结果不确定，请使用全新的专用会话',storage_error:'状态文件不可用，自动任务已停止',engine_error:'排程未能生效，自动任务已停止',disabled:'账号已禁用',deleted:'已删除',delete_pending:'正在删除凭证，自动任务已暂停',delete_failed:'凭证删除失败，自动任务已暂停，可重试删除',canceled:'已取消',no_runnable_accounts:'没有可运行的账号，请先修复账号状态',management_auth_required:'管理密钥无效。已停止请求，请重新输入',request_stopped:'请求已停止',invalid_response:'管理服务返回了无法识别的数据',body_too_large:'输入内容超过大小限制',retry_exhausted:'本轮重试已结束，可手动重试',pending_login:'今日待登录领奖',reward_unknown:'今日已登录，奖励金额未知',cookie_expired:'登录会话失效，请重新导入 Cookie',challenge_blocked:'第三方登录域名对服务器 IP 弹出人机验证（如 Cloudflare），服务器无法自动通过：请点「直接登录」在浏览器完成当天登录（需要你的网络能访问 LinuxDO），或为服务器配置出口代理／换用 GitHub 渠道',needs_attention:'登录被站点拦截或需要确认授权，需人工处理',waf_blocked:'站点对服务器读取用户信息启用了人机验证（如阿里云验证码），暂时读不到余额与奖励状态；浏览器里的登录和奖励发放不受影响，稍后可点「重新检查」重试'};
const message=kind=>labels[kind]??'操作未完成，请重新检查';
const loginResultText={success:'奖励已到账',already:'今日已完成'};
const resultText=(account,kind)=>account&&account.checkin_mode==='login'&&loginResultText[kind]?loginResultText[kind]:message(kind);
const when=value=>{if(!value||String(value).startsWith('0001-'))return '尚无记录';const d=new Date(typeof value==='number'?value*1000:value);return Number.isNaN(d.getTime())?'时间未知':d.toLocaleString('zh-CN',{hour12:false})};
const quota=n=>Number(n??0).toLocaleString('zh-CN')+' quota';

function mount(){
  const $=id=>document.getElementById(id);
  const node=(tag,text,className)=>{const el=document.createElement(tag);if(text!==undefined)renderText(el,text);if(className)el.className=className;return el};
  let snapshot={accounts:[],jobs:{},history:[],schedules:{}};
  let attemptedConnection=false;
  let balanceSynced=false;
  let healthPayload={accounts:{},refreshing:false},modelsPayload={accounts:{},refreshing:false},healthController;
  // Older builds registered a relay Service Worker on this origin; remove it.
  if('serviceWorker' in navigator)navigator.serviceWorker.getRegistrations().then(rs=>{for(const r of rs){for(const w of [r.active,r.waiting,r.installing])if(w&&w.scriptURL.includes('relay-sw.js')){r.unregister();break}}}).catch(()=>{});
  const notify=(text,error=false)=>{renderText($('notice'),text);$('notice').className=error?'notice error':'notice';$('notice').hidden=false;if($('bind-dialog').open){renderText($('bind-notice'),text);$('bind-notice').hidden=false;}};
  const api=createApiController({storage:sessionStorage,onAuth:valid=>{
    $('auth-panel').hidden=valid;renderText($('connection'),valid?'已连接管理服务':'等待管理密钥');$('workspace').inert=!valid;
    if(valid){attemptedConnection=true;$('notice').hidden=true;}else{healthPayload={accounts:{},refreshing:false};modelsPayload={accounts:{},refreshing:false};healthController?.setAccounts([]);if(attemptedConnection)notify(message('management_auth_required'),true)}healthController?.setAuthenticated(valid);
  },onError:e=>notify(message(e.kind),true),onStatus:data=>{if(data.health)healthPayload=data.health;if(data.models)modelsPayload=data.models;render(data);healthController?.setAccounts((snapshot.accounts??[]).filter(a=>a.status!=='deleted'&&!['delete_pending','delete_failed'].includes(a.status)).map(a=>a.account_id));autoBalance()}});
  healthController=createHealthController({request:(path,init)=>api.request(path,init),visible:!document.hidden,onData:payload=>{healthPayload=payload?.health??{accounts:{},refreshing:false};modelsPayload=payload?.models??{accounts:{},refreshing:false};render(snapshot);void api.poll()},onError:e=>notify('请求状态刷新失败：'+message(e.kind),true)});
  async function run(action,button,success){if(button)button.disabled=true;try{await action();if(success)notify(success);await api.poll()}catch(e){notify(message(e.kind),true)}finally{if(button)button.disabled=false}}
  function button(text,fn,className='quiet'){const b=node('button',text,className);b.type='button';b.addEventListener('click',()=>fn(b));return b}
  function openBind(account){$('bind-form').reset();$('bind-notice').hidden=true;$('bind-id').value=account?.account_id??'';$('bind-oauth-provider').dataset.prev=account?.oauth_provider??'';$('bind-site').value=account?.site??'';$('bind-site').readOnly=!!account;$('bind-label').value=account?.label??'';$('bind-user-id').value=account?.upstream_user_id??'';$('bind-expiry').value=account&&account.expires_at>0?new Date(account.expires_at*1000).toLocaleDateString('sv-SE'):'';renderText($('bind-title'),account?'重新绑定账号':'添加账号');$('bind-mode').value=account?.checkin_mode==='login'?'login':'api';bindMode();const stored=account?.channel;channel(stored&&allowedChannels($('bind-mode').value).includes(stored)?stored:'token');if(account?.oauth_provider){$('bind-oauth-provider').value=account.oauth_provider;oauthProvider()}$('bind-token').required=!account;$('bind-token').placeholder=account&&account.token_hint?`留空沿用已保存的令牌（尾号 ${account.token_hint}）`:'nap_…';$('bind-dialog').showModal();$('bind-site').focus()}
  function channel(name){$('channel').value=name;for(const panel of document.querySelectorAll('[data-channel-panel]')){const show=panel.dataset.channelPanel===name;panel.hidden=!show;for(const input of panel.querySelectorAll('input'))input.disabled=!show;}for(const b of document.querySelectorAll('[data-channel]'))b.setAttribute('aria-selected',String(b.dataset.channel===name));syncLoginExtra()}
  function bindMode(){const login=$('bind-mode').value==='login';$('bind-mode-help').hidden=login;const allowed=allowedChannels($('bind-mode').value);for(const b of document.querySelectorAll('[data-channel]')){const ok=allowed.includes(b.dataset.channel);b.hidden=!ok;b.disabled=!ok;b.title=''}if(!allowed.includes($('channel').value))channel('token');syncLoginExtra()}
  function syncLoginExtra(){const login=$('bind-mode').value==='login';const chosen=$('channel').value;const show=login&&['token','refresh_cookie','oauth'].includes(chosen);$('login-extra').hidden=!show;if(!show){oauthProvider();return}const required=chosen==='oauth';renderText($('login-extra-title'),required?'第三方登录方式（必填）':'每日自动登录（可选）');const manual=$('bind-oauth-provider').querySelector('option[value=""]');if(manual)manual.hidden=required;renderText($('oauth-cookie-label'),required?'第三方会话 Cookie（必填）':'会话 Cookie 请求头（可选）');oauthProvider()}
  function attachLoginSession(payload){const p=$('bind-oauth-provider').value;const ck=$('bind-oauth-cookie').value.trim();if(!p)return true;if(!ck&&(!$('bind-id').value||$('bind-oauth-provider').dataset.prev!==p)){notify('已选择自动登录但未粘贴会话 Cookie：请粘贴 Cookie，或改选「手动」',true);return false}payload.oauth_provider=p;if(ck)payload.oauth_cookie=ck;return true}
  const probeCache=new Map();const providerNames={github:'GitHub',linuxdo:'LinuxDO'};
  function oauthProvider(){const p=$('bind-oauth-provider').value;$('oauth-cookie-field').hidden=!p;probeProvider(p);const link=$('oauth-get-link');if(p==='github'){link.href='https://github.com/';renderText(link,'打开 GitHub 获取会话 ↗')}else if(p==='linuxdo'){link.href='https://connect.linux.do/';renderText(link,'打开 LinuxDO Connect 获取会话 ↗')}}
  // A live reachability check from the server: warn before the operator spends
  // effort importing a cookie that the server's IP can never use.
  function probeProvider(p){const hint=$('oauth-probe-hint');hint.hidden=true;renderText(hint,'');if(!p)return;const cached=probeCache.get(p);if(cached&&Date.now()-cached.at<300000){showProbeHint(p,cached.status);return}api.request('/probe?provider='+p).then(r=>{if(!r?.status)return;probeCache.set(p,{at:Date.now(),status:r.status});if($('bind-oauth-provider').value===p)showProbeHint(p,r.status)}).catch(()=>{})}
  function showProbeHint(p,status){const hint=$('oauth-probe-hint');if(status==='blocked'){renderText(hint,`当前服务器访问 ${providerNames[p]} 登录域名被人机验证拦截（如 Cloudflare），自动登录会失败：请改用另一渠道、为服务器配置出口代理，或改选“手动”每天在浏览器登录。`);hint.hidden=false}else if(status==='unreachable'){renderText(hint,`当前服务器连不上 ${providerNames[p]}，自动登录会失败，请先检查服务器网络。`);hint.hidden=false}}
  // A page load or reload shows the wallet amounts saved by the last scheduled
  // attempt. Refresh them once in the background so a balance changed outside
  // the plugin (spent via API, topped up, granted) is current without clicking
  // the toolbar button. Later polls reuse these saved values; only the button
  // and full reloads re-query the sites.
  function autoBalance(){if(balanceSynced)return;const active=(snapshot.accounts??[]).filter(a=>a.status!=='deleted');if(!active.length)return;balanceSynced=true;api.request('/balance',{method:'POST',body:{}}).then(r=>{const updated=new Map((r?.accounts??[]).map(a=>[a.account_id,a]));if(!updated.size)return;snapshot={...snapshot,accounts:(snapshot.accounts??[]).map(a=>updated.get(a.account_id)??a)};render(snapshot)}).catch(()=>{})}
  function render(data){snapshot=data;const accounts=(data.accounts??[]).filter(a=>a.status!=='deleted');const perUnits=new Map((data.accounts??[]).map(a=>[a.account_id,a.balance_per_unit>0?a.balance_per_unit:0]));const usd=(id,v)=>{const pu=perUnits.get(id)||0;return pu>0?('$'+(v/pu).toFixed(2)):quota(v)};const ready=accounts.filter(a=>a.status==='ready').length;
    renderText($('account-count'),`${accounts.length} 个账号`);renderText($('available-count'),`${ready} 个可运行`);const s=data.settings??{};renderText($('schedule-summary'),`每日 ${s.checkin_at??'09:00'} · 随机偏移 ±${s.jitter_minutes??20} 分钟 · ${s.timezone||'宿主本地时区'}`);
    const running=Object.values(data.jobs??{}).some(j=>['queued','running'].includes(j.status));renderText($('job-status'),running?'任务执行中，每 3 秒更新':healthPayload.refreshing?'请求状态更新中，每 3 秒读取缓存':'后台自动签到已就绪');
    $('refresh-health').disabled=healthPayload.refreshing===true;renderText($('refresh-health'),healthPayload.refreshing?'请求状态更新中…':'刷新请求状态');
    if(data.error)notify(message(data.error),true);
    const body=$('account-rows'),accountRows=[];$('empty').hidden=accounts.length!==0;$('account-table').hidden=accounts.length===0;
    for(const a of accounts){const tr=node('tr',undefined,'account-main-row');tr.dataset.accountId=a.account_id;const identity=node('td');identity.dataset.label='账号';identity.append(node('strong',a.label||a.username),renderSiteLink(document,a.site),node('span',a.username,'secondary'));tr.append(identity);
      const stateCell=node('td');stateCell.dataset.label='状态';stateCell.append(node('span',a.checkin_mode==='login'&&a.status==='ready'?(a.oauth_provider?'登录领奖·自动':'登录领奖·半自动'):message(a.status),'badge '+(a.status==='ready'?'good':'attention')));if(a.last_result)stateCell.append(node('span',resultText(a,a.last_result.status),'secondary'));if(a.checkin_mode==='login'&&a.reward_state&&a.reward_state!==a.last_result?.status)stateCell.append(node('span',resultText(a,a.reward_state),'secondary'));tr.append(stateCell);
      const cred=node('td');cred.dataset.label='凭证';cred.append(node('span',a.kind==='pat'?'PAT':a.kind==='session'?'登录会话':'旧版令牌'));if(a.checkin_mode==='login')cred.append(node('span','登录领奖','secondary'));let expiry=a.expires_at===0&&a.kind!=='session'?'用户标注不设到期':a.expires_at?when(a.expires_at):'到期时间未知';cred.append(node('span',expiry,'secondary'));if(a.expires_at>0&&a.expires_at*1000-Date.now()<86400000)cred.append(node('span','即将或已经到期，请重新绑定','expiry'));if(a.token_hint)cred.append(node('span',a.token_hint,'secondary'));tr.append(cred);
      const bal=node('td');bal.dataset.label='钱包余额';bal.append(node('span',a.balance_quota==null?'—':usd(a.account_id,a.balance_quota),'strong'));tr.append(bal);
      const totals=node('td');totals.dataset.label=a.checkin_mode==='login'?'累计授予额度':'站点累计签到奖励';
      if(a.checkin_mode==='login'){totals.append(node('strong',usd(a.account_id,a.reward_total??0)),node('span',a.reward_state?resultText(a,a.reward_state):'等待首次检测','secondary'))}
      else{totals.append(node('strong',usd(a.account_id,a.total_quota)),node('span',`${a.total_checkins??0} 次签到`,'secondary'),node('span',`${a.stats_stale?'统计待更新 · ':''}${when(a.stats_synced_at)}`,'secondary small'))}
      tr.append(totals);
      const planned=node('td');planned.dataset.label='下次运行';const schedule=data.schedules?.[a.account_id];planned.append(node('span',schedule?.work_id?'本日任务已入队':schedule?when(schedule.planned_at):a.status==='ready'?'等待排程':'已暂停'));tr.append(planned);
      const actions=node('td');actions.dataset.label='操作';const group=node('div',undefined,'row-actions');
      if(a.checkin_mode==='login'&&a.oauth_provider!=='github')group.append(button('直接登录',()=>{window.open(new URL('/login',a.site).href,'_blank','noopener')}));
      const login=a.checkin_mode==='login';const check=button(login?'检测到账':'签到',b=>run(()=>api.request(login?'/recheck':'/checkin',{method:'POST',body:{account_id:a.account_id}}),b,login?'已重新检测到账':'任务已加入队列'));check.disabled=a.status!=='ready';group.append(check,button('重新检查',b=>run(()=>api.request('/recheck',{method:'POST',body:{account_id:a.account_id}}),b,'已重新检查')),button('重绑',()=>openBind(a)),button(['delete_pending','delete_failed'].includes(a.status)?'重试删除':'删除',()=>{$('delete-id').value=a.account_id;renderText($('delete-name'),a.label||a.username);$('delete-dialog').showModal()},'quiet danger'));actions.append(group);tr.append(actions);accountRows.push(tr,renderHealthRow(document,a,healthPayload.accounts?.[a.account_id],modelsPayload.accounts?.[a.account_id]));
    }
    replaceAccountRows(document,body,accountRows);
    const history=$('history-rows');history.replaceChildren();const names=new Map((data.accounts??[]).map(a=>[a.account_id,a.label||a.username]));const modes=new Map((data.accounts??[]).map(a=>[a.account_id,a.checkin_mode]));const entries=[...(data.history??[])].reverse();$('history-empty').hidden=entries.length>0;
    for(const entry of entries){const row=node('tr');const login=modes.get(entry.account_id)==='login';const awarded=login&&entry.result.status==='success'?'推断 '+usd(entry.account_id,entry.result.quota_awarded):usd(entry.account_id,entry.result.quota_awarded);for(const text of [when(entry.result.observed_at),names.get(entry.account_id)??'已移除账号',resultText({checkin_mode:modes.get(entry.account_id)},entry.result.status),entry.result.server_date||'站点日期未知',awarded])row.append(node('td',text));history.append(row)}
    // Statistics payloads carry the account label captured at aggregation
    // time; adopt the live names and redraw the charts once when a rename is
    // detected, instead of waiting for the next explicit refresh.
    if(statsPayload){let renamed=false;for(const x of statsPayload.stats?.accounts??[]){const a=accounts.find(y=>y.account_id===x.id);const label=a&&(a.label||a.username);if(label&&label!==x.label){x.label=label;renamed=true}}if(renamed)renderStats()}
    if(!statsLoaded&&accounts.length){statsLoaded=true;void loadStats(false)}
    if(!document.activeElement?.closest('#settings-form')){$('setting-time').value=s.checkin_at??'09:00';$('setting-jitter').value=s.jitter_minutes??20;$('setting-zone').value=s.timezone??''}
  }
  // —— 用量统计区块 ——
  let statsRange='7d',statsDim='account',statsPayload=null,statsLoaded=false,statsSeq=0;
  const STATS_COLORS=['#3b5bdb','#f08c00','#2f9e44','#c2255c','#7048e8','#0c8599','#e8590c','#868e96'];
  const statsSeconds=()=>statsRange==='24h'?86400:statsRange==='7d'?7*86400:30*86400;
  const fmtCompact=v=>{v=Number(v??0);if(v>=1e9)return (v/1e9).toFixed(2)+'B';if(v>=1e6)return (v/1e6).toFixed(2)+'M';if(v>=1e3)return (v/1e3).toFixed(1)+'K';return String(v)};
  const ratioPct=(a,b)=>b>0?((a/b*100).toFixed(2)+'%'):'—';
  const svgNode=(tag,attrs)=>{const el=document.createElementNS('http://www.w3.org/2000/svg',tag);for(const [k,v] of Object.entries(attrs??{}))el.setAttribute(k,String(v));return el};
  const svgText=(x,y,text,cls)=>{const t=svgNode('text',{x,y});if(cls)t.setAttribute('class',cls);t.textContent=String(text);return t};
  function modelColors(){return new Map((statsPayload?.stats?.models??[]).map((m,i)=>[m.model,STATS_COLORS[i%STATS_COLORS.length]]))}
  // Long names keep both ends visible (the prefix identifies the site/account
  // and the suffix usually carries the distinguishing number); the middle is
  // replaced with an ellipsis.
  const middleEllipsis=(text,max=28)=>{const chars=[...String(text??'')];if(chars.length<=max)return chars.join('');const keep=max-1,head=Math.ceil(keep/2),tail=keep-head;return chars.slice(0,head).join('')+'…'+chars.slice(-tail).join('')};
  function statsRows(dim){const s=statsPayload?.stats;if(!s)return [];const total=s.totals?.total_tokens||0;const pal=modelColors();const src=dim==='account'?(s.accounts??[]):(s.models??[]);const liveLabels=new Map((snapshot?.accounts??[]).map(a=>[a.account_id,a.label||a.username]));return src.filter(x=>x.total_tokens>0).sort((a,b)=>b.total_tokens-a.total_tokens).map((x,i)=>{const full=dim==='account'?(liveLabels.get(x.account_id)||x.label||x.account_id):x.model;return{name:middleEllipsis(full),full,site:dim==='account'?x.site:'',tokens:x.total_tokens,calls:x.calls??0,prompt:x.prompt_tokens??0,cache:x.cache_tokens??0,cost:x.cost_usd??0,perUnit:dim==='account'?(x.per_unit??0):1,share:total>0?x.total_tokens/total*100:0,color:dim==='model'?(pal.get(x.model)??'#adb5bd'):STATS_COLORS[i%STATS_COLORS.length]}})}
  function loadStats(refresh){const seq=++statsSeq;renderText($('stats-meta'),'统计中…');api.request(`/stats?range=${statsRange}${refresh?'&refresh=1':''}`).then(r=>{if(seq!==statsSeq)return;statsPayload=r??null;renderStats()}).catch(e=>{if(seq!==statsSeq)return;renderText($('stats-meta'),'统计失败：'+message(e.kind??'unknown_error'))})}
  function renderStats(){const s=statsPayload?.stats;if(!s){renderText($('stats-meta'),'暂无统计数据');return}const parts=[`数据截至 ${when(s.synced_at)}`,statsPayload.from_cache?'已缓存':'实时'];const failed=Object.keys(s.failed??{}).length;if(s.truncated)parts.push('已达页上限，统计不完整');if(failed)parts.push(`${failed} 个账号统计失败`);if(s.partial_cost)parts.push('部分站点未公布换算比例，成本不含该站点');renderText($('stats-meta'),parts.join(' · '));const total=s.totals?.total_tokens||0;renderText($('stats-model-count'),`${(s.models??[]).length} 个模型 · 总 Token ${fmtCompact(total)}`);const rows=statsRows(statsDim);renderDonut(rows,total);renderStatsRows($('stats-account-list'),rows,$('stats-donut'));renderBars(s.series??[]);renderStatsRows($('stats-model-list'),statsRows('model'),$('stats-bars'))}
  function lighten(hex,ratio){const n=parseInt(hex.slice(1),16);const mix=c=>Math.round(c+(255-c)*ratio);return '#'+[mix(n>>16&255),mix(n>>8&255),mix(n&255)].map(v=>v.toString(16).padStart(2,'0')).join('')}
  const charW=ch=>/[\u4e00-\u9fff]/.test(ch)?11:6.2;
  function truncateLabel(s,maxPx){const chars=[...String(s)];const budget=Math.max(0,maxPx-12);let total=0;for(const ch of chars)total+=charW(ch);if(total<=budget)return chars.join('');let head='',tail='',hw=0,tw=0,i=0,j=chars.length-1;while(i<=j){if(hw<=tw){const cw=charW(chars[i]);if(hw+tw+cw<=budget){head+=chars[i];hw+=cw;i++;continue}}const cw=charW(chars[j]);if(hw+tw+cw<=budget){tail=chars[j]+tail;tw+=cw;j--;continue}break}return head+'…'+tail}
  function setHl(host,key){if(!host)return;for(const el of host.querySelectorAll('[data-hl-key]')){const on=key!==null&&el.getAttribute('data-hl-key')===key;el.classList.toggle('is-hl',on);el.classList.toggle('is-dim',key!==null&&!on)}}
  function hideStatsTip(){$('stats-tooltip').hidden=true}
  function showStatsTip(ev,title,rows,sub){const tip=$('stats-tooltip');tip.replaceChildren();tip.append(node('strong',title));for(const r of rows){const line=node('div',undefined,'tip-row');const dot=node('span',undefined,'stats-dot');dot.style.background=r.color;line.append(dot,node('span',r.text));tip.append(line)}if(sub)tip.append(node('div',sub,'stats-sub'));tip.hidden=false;const maxX=(typeof window!=='undefined'?window.innerWidth:1200)-300;const maxY=(typeof window!=='undefined'?window.innerHeight:800)-180;tip.style.left=Math.max(8,Math.min((ev?.clientX??0)+14,maxX))+'px';tip.style.top=Math.max(8,Math.min((ev?.clientY??0)+16,maxY))+'px'}
  function renderDonut(rows,total){const host=$('stats-donut');host.replaceChildren();const W=430,H=252,cx=215,cy=126,r=78,sw=30,circ=2*Math.PI*r;const svg=svgNode('svg',{viewBox:`0 0 ${W} ${H}`,role:'img'});const defs=svgNode('defs');svg.append(defs);const slice=(()=>{const sorted=[...rows].sort((a,b)=>b.tokens-a.tokens);if(sorted.length>6){const rest=sorted.slice(6);return [...sorted.slice(0,6),{name:'其他',tokens:rest.reduce((q,v)=>q+v.tokens,0),calls:rest.reduce((q,v)=>q+v.calls,0),prompt:rest.reduce((q,v)=>q+v.prompt,0),cache:rest.reduce((q,v)=>q+v.cache,0),cost:rest.reduce((q,v)=>q+v.cost,0),perUnit:1,share:rest.reduce((q,v)=>q+v.share,0),color:'#adb5bd'}]}return sorted})();
    if(!slice.length||total<=0){svg.append(svgText(cx,cy+4,'暂无数据','donut-caption'))}
    let acc=0;const labelSeeds=[];
    slice.forEach((seg,i)=>{const gid=`statsGrad${i}`;const grad=svgNode('linearGradient',{id:gid,x1:'0%',y1:'0%',x2:'100%',y2:'100%'});grad.append(svgNode('stop',{offset:'0%','stop-color':lighten(seg.color,.45)}),svgNode('stop',{offset:'100%','stop-color':seg.color}));defs.append(grad);
      const len=seg.share/100*circ;const mid=-90+(acc+len/2)/circ*360;
      const g=svgNode('g',{transform:`rotate(-90 ${cx} ${cy})`});
      const circle=svgNode('circle',{cx,cy,r,fill:'none',stroke:`url(#${gid})`,'stroke-width':sw,'stroke-dasharray':`${Math.max(len-1.6,.6)} ${circ-len+1.6}`,'stroke-dashoffset':-acc,'stroke-linecap':'round','data-hl-key':seg.name});
      circle.setAttribute('class','donut-seg');
      circle.addEventListener('mouseenter',()=>{circle.style.transform=`translate(${Math.cos(mid*Math.PI/180)*10}px, ${Math.sin(mid*Math.PI/180)*10}px)`;setHl(host,seg.name)});
      circle.addEventListener('mousemove',ev=>showStatsTip(ev,seg.full||seg.name,[{color:seg.color,text:`${fmtCompact(seg.tokens)} tokens · ${seg.share.toFixed(2)}%`}],`缓存率 ${ratioPct(seg.cache,seg.prompt)} · 请求 ${fmtCompact(seg.calls)} · 消耗 ${seg.perUnit>0?'$'+seg.cost.toFixed(2):'—'}`));
      circle.addEventListener('mouseleave',()=>{circle.style.transform='';setHl(host,null);hideStatsTip()});
      g.append(circle);svg.append(g);
      if(seg.share>=0.4){const a=mid*Math.PI/180,side=Math.cos(a)>=0?1:-1;labelSeeds.push({seg,side,anchorX:cx+Math.cos(a)*(r+sw/2),anchorY:cy+Math.sin(a)*(r+sw/2),elbowX:cx+side*(r+sw/2+8),textX:cx+side*(r+sw/2+16),y:cy+Math.sin(a)*(r+sw/2+10)})}
      acc+=len});
    for(const side of [-1,1]){const top=14,bottom=H-14,gap=18,capacity=Math.max(1,Math.floor((bottom-top)/gap));const col=labelSeeds.filter(l=>l.side===side).sort((a,b)=>b.seg.share-a.seg.share).slice(0,capacity).sort((a,b)=>a.y-b.y);
      let prev=top-gap;col.forEach(l=>{l.y=Math.max(l.y,prev+gap);prev=l.y});
      let next=bottom+gap;for(let i=col.length-1;i>=0;i--){col[i].y=Math.min(col[i].y,next-gap);next=col[i].y}
      for(const l of col){const line=svgNode('polyline',{points:`${l.anchorX.toFixed(1)},${l.anchorY.toFixed(1)} ${l.elbowX.toFixed(1)},${l.y.toFixed(1)} ${l.textX.toFixed(1)},${l.y.toFixed(1)}`,fill:'none',stroke:l.seg.color,'stroke-width':1,'data-hl-key':l.seg.name,'class':'donut-label-line'});const avail=l.side===1?W-6-l.textX:l.textX-6;const t=svgText(l.textX,l.y+3.5,`${truncateLabel(l.seg.name,avail)} ${l.seg.share.toFixed(1)}%`);t.setAttribute('class','donut-label');t.setAttribute('text-anchor',l.side===1?'start':'end');t.setAttribute('data-hl-key',l.seg.name);svg.append(line,t)}}
    svg.append(svgText(cx,cy-4,fmtCompact(total),'donut-total'),svgText(cx,cy+18,'tokens','donut-caption'));host.append(svg)}
  function renderBars(series){const host=$('stats-bars');host.replaceChildren();if(!series.length){host.append(node('p','暂无数据','stats-empty'));return}const W=640,H=250,padL=58,padR=10,padT=12,padB=28,plotW=W-padL-padR,plotH=H-padT-padB;const totals=series.map(b=>(b.tokens??[]).reduce((q,v)=>q+v.tokens,0));const max=Math.max(...totals,1);const pal=modelColors();const svg=svgNode('svg',{viewBox:`0 0 ${W} ${H}`,role:'img'});for(const frac of [0,0.5,1]){const y=padT+plotH*(1-frac);svg.append(svgNode('line',{x1:padL,y1:y,x2:W-padR,y2:y,stroke:'#e6eaf1','stroke-width':1}));const t=svgText(padL-6,y+4,fmtCompact(max*frac),'axis');t.setAttribute('text-anchor','end');svg.append(t)}const step=plotW/series.length,bw=Math.min(step*0.62,86),every=Math.max(1,Math.ceil(series.length/10));series.forEach((b,idx)=>{const x=padL+step*idx+(step-bw)/2;let acc=0;const sorted=[...(b.tokens??[])].sort((a,c)=>c.tokens-a.tokens);for(const v of sorted){const hgt=v.tokens/max*plotH;if(hgt<=0)continue;svg.append(svgNode('rect',{x,y:padT+plotH-acc-hgt,width:bw,height:hgt,fill:pal.get(v.model)??'#adb5bd','data-hl-key':v.model,class:'bar-seg'}));acc+=hgt}if(idx%every===0){const label=svgText(padL+step*idx+step/2,H-8,bucketLabel(b.start),'axis');label.setAttribute('text-anchor','middle');svg.append(label)}const zone=svgNode('rect',{x:padL+step*idx,y:padT,width:step,height:plotH,fill:'transparent'});zone.addEventListener('mousemove',ev=>showStatsTip(ev,`${bucketLabel(b.start)} · 合计 ${fmtCompact(totals[idx])} tokens`,sorted.slice(0,8).map(v=>({color:pal.get(v.model)??'#adb5bd',text:`${v.model} · ${fmtCompact(v.tokens)} (${totals[idx]>0?(v.tokens/totals[idx]*100).toFixed(1):'0.0'}%)`})),null));zone.addEventListener('mouseleave',hideStatsTip);svg.append(zone)});host.append(svg)}
  function bucketLabel(start){const d=new Date(start*1000);if(statsRange==='24h')return `${String(d.getHours()).padStart(2,'0')}:00`;return `${d.getMonth()+1}/${d.getDate()}`}
  function renderStatsRows(host,rows,hlHost){host.replaceChildren();if(!rows.length){host.append(node('p','暂无数据','stats-empty'));return}const minutes=statsSeconds()/60;
    rows.forEach((r,i)=>{const item=node('div',undefined,'rank-item');item.style.setProperty('--rank-color',r.color);item.addEventListener('mouseenter',()=>{item.classList.add('is-hl');setHl(hlHost,r.name)});item.addEventListener('mouseleave',()=>{item.classList.remove('is-hl');setHl(hlHost,null)});
      const top=node('div',undefined,'rank-top');const name=node('strong',r.name,'rank-name');name.setAttribute('title',r.site?`${r.full||r.name} · ${r.site}`:r.full||r.name);top.append(node('span',String(i+1),'rank-no'),node('span',undefined,'rank-color'),name,node('strong',fmtCompact(r.tokens),'rank-tokens'),node('span',`${r.share.toFixed(2)}%`,'rank-share'));
      const track=node('div',undefined,'rank-track');const fill=node('span');fill.style.width=`${Math.max(0,Math.min(100,r.share))}%`;track.append(fill);
      const metrics=node('div',undefined,'rank-metrics');const met=(label,value,strongCls)=>{const s=node('span');s.append(label+' ',node('strong',value,strongCls));return s};const costTxt=r.perUnit>0?('$'+r.cost.toFixed(2)):'—';
      metrics.append(met('缓存率',ratioPct(r.cache,r.prompt),'rank-strong-cache'),met('请求数',fmtCompact(r.calls)),met('总成本',costTxt),met('RPM',(r.calls/minutes).toFixed(2)),met('TPM',fmtCompact(r.tokens/minutes)));
      item.append(top,track,metrics);host.append(item)})}
  for(const b of document.querySelectorAll('[data-stats-range]'))b.addEventListener('click',()=>{statsRange=b.dataset.statsRange;for(const x of document.querySelectorAll('[data-stats-range]'))x.setAttribute('aria-selected',String(x===b));void loadStats(false)});
  for(const b of document.querySelectorAll('[data-stats-dim]'))b.addEventListener('click',()=>{statsDim=b.dataset.statsDim;for(const x of document.querySelectorAll('[data-stats-dim]'))x.setAttribute('aria-selected',String(x===b));if(statsPayload)renderStats();else void loadStats(false)});
  $('stats-refresh').addEventListener('click',()=>void loadStats(true));
  $('auth-form').addEventListener('submit',e=>{e.preventDefault();const key=$('management-key').value;$('management-key').value='';void api.setKey(key)});
  $('change-key').addEventListener('click',()=>api.logout());$('add-account').addEventListener('click',()=>openBind());$('empty-add').addEventListener('click',()=>openBind());
  $('check-all').addEventListener('click',e=>void run(()=>api.request('/checkin',{method:'POST',body:{}}),e.currentTarget,'可运行账号已加入队列'));
  $('refresh-health').addEventListener('click',()=>void healthController.refresh(true));
  $('refresh-balances').addEventListener('click',e=>void run(async()=>{const r=await api.request('/balance',{method:'POST',body:{}});const failed=Object.values(r?.failed??{});if(failed.length)notify(`${r.accounts.length} 个账号余额已刷新，${failed.length} 个失败`,true)},e.currentTarget,'余额已刷新'));
  for(const b of document.querySelectorAll('[data-channel]'))b.addEventListener('click',()=>channel(b.dataset.channel));
  $('bind-mode').addEventListener('change',bindMode);$('bind-oauth-provider').addEventListener('change',oauthProvider);
  for(const b of document.querySelectorAll('[data-close]'))b.addEventListener('click',()=>{$(b.dataset.close).close();clearSensitiveFields($('bind-form'))});
  $('bind-dialog').addEventListener('close',()=>clearSensitiveFields($('bind-form')));
  $('bind-form').addEventListener('submit',e=>{e.preventDefault();const form=e.currentTarget;const chosen=$('channel').value;const payload={site:$('bind-site').value,label:$('bind-label').value,channel:chosen,account_id:$('bind-id').value,checkin_mode:$('bind-mode').value};
    if(chosen==='token'){payload.token=$('bind-token').value;if($('bind-user-id').value)payload.upstream_user_id=Number($('bind-user-id').value);if($('bind-expiry').value)payload.expires_at_hint=Math.floor(new Date($('bind-expiry').value+'T23:59:59').getTime()/1000);if(payload.checkin_mode==='login'&&!attachLoginSession(payload))return}else if(chosen==='password'){payload.username=$('bind-username').value;payload.password=$('bind-password').value}else if(chosen==='oauth'){payload.oauth_provider=$('bind-oauth-provider').value;payload.oauth_cookie=$('bind-oauth-cookie').value.trim();if(!payload.oauth_provider||!payload.oauth_cookie){notify('请选择第三方登录方式并粘贴会话 Cookie',true);return}}else{payload.refresh_cookie=$('bind-cookie').value;payload.dedicated_session_confirmed=$('dedicated-session').checked;if(payload.checkin_mode==='login'&&!attachLoginSession(payload))return}
    clearSensitiveFields(form);void run(async()=>{await api.request('/accounts',{method:'POST',body:payload});$('bind-dialog').close()},$('bind-submit'),'账号已验证并保存');
  });
  $('delete-confirm').addEventListener('click',e=>void run(async()=>{await api.request('/accounts?id='+encodeURIComponent($('delete-id').value),{method:'DELETE'});$('delete-dialog').close()},e.currentTarget,'凭证已删除，执行历史保留'));
  $('settings-form').addEventListener('submit',e=>{e.preventDefault();void run(()=>api.request('/settings',{method:'POST',body:{checkin_at:$('setting-time').value,jitter_minutes:Number($('setting-jitter').value),timezone:$('setting-zone').value.trim()}}),$('settings-save'),'排程设置已生效')});
  $('settings-reset').addEventListener('click',e=>void run(()=>api.request('/settings',{method:'POST',body:{checkin_at:null,jitter_minutes:null,timezone:null}}),e.currentTarget,'已恢复宿主配置默认值'));
  const healthVisible=()=>!document.hidden&&!document.querySelector('[data-view-panel="accounts"]').hidden;
  for(const tab of document.querySelectorAll('[data-view]'))tab.addEventListener('click',()=>{for(const t of document.querySelectorAll('[data-view]'))t.setAttribute('aria-selected',String(t===tab));for(const panel of document.querySelectorAll('[data-view-panel]'))panel.hidden=panel.dataset.viewPanel!==tab.dataset.view;healthController.setVisible(healthVisible())});
  document.addEventListener('visibilitychange',()=>{healthController.setVisible(healthVisible());void api.setVisible(!document.hidden)});window.addEventListener('pagehide',()=>{healthController.stop();api.stop()});void api.start();
}
if(typeof document!=='undefined'&&document.getElementById('workspace'))mount();
