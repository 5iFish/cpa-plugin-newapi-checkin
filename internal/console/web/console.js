const API_BASE = '/v0/management/plugins/newapi-checkin';
const STORAGE_KEY = 'newapi-checkin:auth';
export const renderText = (node,value) => { node.textContent = String(value ?? ''); return node; };
export const clearSensitiveFields = form => { for (const input of form.querySelectorAll('input[type="password"],textarea[data-secret]')) input.value = ''; };
const failure = kind => Object.assign(new Error(kind),{kind});

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
      if(stopped||!key||generation!==epoch||(poll&&!visible))throw failure('request_stopped');
      if(!/^\/[a-z]+(?:\?id=[a-f0-9]+)?$/.test(path))throw failure('invalid_input');
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
      activeJobs=Object.values(data.jobs??{}).some(j=>j.status==='queued'||j.status==='running');options.onStatus?.(data);
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

const labels={ready:'可自动签到',success:'签到成功',already:'已签到',network_error:'连接失败，可重试',rate_limited:'站点限流，等待重试',upstream_error:'站点暂时不可用',result_unknown:'正在确认签到结果',credential_expired:'凭证已到期，需要重新绑定',legacy_retired:'旧令牌已退役，请使用 PAT',permission_denied:'权限不足，请检查 profile:read、wallet:read、wallet:write',refresh_outcome_unknown:'刷新结果不确定，请用新会话重新绑定',persistence_error:'新凭证保存失败，已暂停账号',credential_unreadable:'凭证文件无法读取',external_change:'凭证被外部修改，请重新检查',turnstile_blocked:'签到需要人机验证，无法自动执行',not_enabled:'站点未开启签到',origin_forbidden:'站点拒绝刷新来源，请检查反向代理',unsupported_site:'站点接口不符合要求',unknown_error:'操作未完成，请重新检查',login_verification_required:'登录需要二次验证，请使用 PAT 或专用会话',invalid_input:'输入无效，请检查填写内容',identity_mismatch:'新凭证属于其他账号，未覆盖原账号',session_already_managed:'此会话已被管理或结果不确定，请使用全新的专用会话',storage_error:'状态文件不可用，自动任务已停止',engine_error:'排程未能生效，自动任务已停止',disabled:'账号已禁用',deleted:'已删除',delete_pending:'正在删除凭证，自动任务已暂停',delete_failed:'凭证删除失败，自动任务已暂停，可重试删除',canceled:'已取消',no_runnable_accounts:'没有可运行的账号，请先修复账号状态',management_auth_required:'管理密钥无效。已停止请求，请重新输入',request_stopped:'请求已停止',invalid_response:'管理服务返回了无法识别的数据',body_too_large:'输入内容超过大小限制',retry_exhausted:'本轮重试已结束，可手动重试'};
const message=kind=>labels[kind]??'操作未完成，请重新检查';
const when=value=>{if(!value||String(value).startsWith('0001-'))return '尚无记录';const d=new Date(typeof value==='number'?value*1000:value);return Number.isNaN(d.getTime())?'时间未知':d.toLocaleString('zh-CN',{hour12:false})};
const quota=n=>Number(n??0).toLocaleString('zh-CN')+' quota';

function mount(){
  const $=id=>document.getElementById(id);
  const node=(tag,text,className)=>{const el=document.createElement(tag);if(text!==undefined)renderText(el,text);if(className)el.className=className;return el};
  let snapshot={accounts:[],jobs:{},history:[],schedules:{}};
  let attemptedConnection=false;
  const notify=(text,error=false)=>{renderText($('notice'),text);$('notice').className=error?'notice error':'notice';$('notice').hidden=false;if($('bind-dialog').open){renderText($('bind-notice'),text);$('bind-notice').hidden=false;}};
  const api=createApiController({storage:sessionStorage,onAuth:valid=>{
    $('auth-panel').hidden=valid;renderText($('connection'),valid?'已连接管理服务':'等待管理密钥');$('workspace').inert=!valid;
    if(valid){attemptedConnection=true;$('notice').hidden=true;}else if(attemptedConnection)notify(message('management_auth_required'),true);
  },onError:e=>notify(message(e.kind),true),onStatus:render});
  async function run(action,button,success){if(button)button.disabled=true;try{await action();if(success)notify(success);await api.poll()}catch(e){notify(message(e.kind),true)}finally{if(button)button.disabled=false}}
  function button(text,fn,className='quiet'){const b=node('button',text,className);b.type='button';b.addEventListener('click',()=>fn(b));return b}
  function openBind(account){$('bind-form').reset();$('bind-notice').hidden=true;$('bind-id').value=account?.account_id??'';$('bind-site').value=account?.site??'';$('bind-site').readOnly=!!account;$('bind-label').value=account?.label??'';renderText($('bind-title'),account?'重新绑定账号':'添加账号');channel('token');$('bind-dialog').showModal();$('bind-site').focus()}
  function channel(name){$('channel').value=name;for(const panel of document.querySelectorAll('[data-channel-panel]')){const show=panel.dataset.channelPanel===name;panel.hidden=!show;for(const input of panel.querySelectorAll('input'))input.disabled=!show;}for(const b of document.querySelectorAll('[data-channel]'))b.setAttribute('aria-selected',String(b.dataset.channel===name));}
  function render(data){snapshot=data;const accounts=(data.accounts??[]).filter(a=>a.status!=='deleted');const ready=accounts.filter(a=>a.status==='ready').length;
    renderText($('account-count'),`${accounts.length} 个账号`);renderText($('available-count'),`${ready} 个可自动签到`);const s=data.settings??{};renderText($('schedule-summary'),`每日 ${s.checkin_at??'09:00'} · 随机偏移 ±${s.jitter_minutes??20} 分钟 · ${s.timezone||'宿主本地时区'}`);
    const running=Object.values(data.jobs??{}).some(j=>['queued','running'].includes(j.status));renderText($('job-status'),running?'任务执行中，每 3 秒更新':'后台自动签到已就绪');
    if(data.error)notify(message(data.error),true);
    const body=$('account-rows');body.replaceChildren();$('empty').hidden=accounts.length!==0;$('account-table').hidden=accounts.length===0;
    for(const a of accounts){const tr=node('tr');const identity=node('td');identity.dataset.label='账号';identity.append(node('strong',a.label||a.username),node('span',a.site,'secondary site'),node('span',a.username,'secondary'));tr.append(identity);
      const stateCell=node('td');stateCell.dataset.label='状态';stateCell.append(node('span',message(a.status),'badge '+(a.status==='ready'?'good':'attention')));if(a.last_result)stateCell.append(node('span',message(a.last_result.status),'secondary'));tr.append(stateCell);
      const cred=node('td');cred.dataset.label='凭证';cred.append(node('span',a.kind==='pat'?'PAT':a.kind==='session'?'登录会话':'旧版令牌'));let expiry=a.expires_at===0&&a.kind!=='session'?'用户标注不设到期':a.expires_at?when(a.expires_at):'到期时间未知';cred.append(node('span',expiry,'secondary'));if(a.expires_at>0&&a.expires_at*1000-Date.now()<86400000)cred.append(node('span','即将或已经到期，请重新绑定','expiry'));if(a.token_hint)cred.append(node('span',a.token_hint,'secondary'));tr.append(cred);
      const totals=node('td');totals.dataset.label='站点累计签到奖励';totals.append(node('strong',quota(a.total_quota)),node('span',`${a.total_checkins??0} 次签到`,'secondary'),node('span',`${a.stats_stale?'统计待更新 · ':''}${when(a.stats_synced_at)}`,'secondary small'));tr.append(totals);
      const planned=node('td');planned.dataset.label='下次运行';const schedule=data.schedules?.[a.account_id];planned.append(node('span',schedule?.work_id?'本日任务已入队':schedule?when(schedule.planned_at):a.status==='ready'?'等待排程':'已暂停'));tr.append(planned);
      const actions=node('td');actions.dataset.label='操作';const group=node('div',undefined,'row-actions');const check=button('签到',b=>run(()=>api.request('/checkin',{method:'POST',body:{account_id:a.account_id}}),b,'任务已加入队列'));check.disabled=a.status!=='ready';group.append(check,button('重新检查',b=>run(()=>api.request('/recheck',{method:'POST',body:{account_id:a.account_id}}),b,'已重新检查')),button('重绑',()=>openBind(a)),button(['delete_pending','delete_failed'].includes(a.status)?'重试删除':'删除',()=>{$('delete-id').value=a.account_id;renderText($('delete-name'),a.label||a.username);$('delete-dialog').showModal()},'quiet danger'));actions.append(group);tr.append(actions);body.append(tr);
    }
    const history=$('history-rows');history.replaceChildren();const names=new Map((data.accounts??[]).map(a=>[a.account_id,a.label||a.username]));const entries=[...(data.history??[])].reverse();$('history-empty').hidden=entries.length>0;
    for(const entry of entries){const row=node('tr');for(const text of [when(entry.result.observed_at),names.get(entry.account_id)??'已移除账号',message(entry.result.status),entry.result.server_date||'站点日期未知',quota(entry.result.quota_awarded)])row.append(node('td',text));history.append(row)}
    if(!document.activeElement?.closest('#settings-form')){$('setting-time').value=s.checkin_at??'09:00';$('setting-jitter').value=s.jitter_minutes??20;$('setting-zone').value=s.timezone??''}
  }
  $('auth-form').addEventListener('submit',e=>{e.preventDefault();const key=$('management-key').value;$('management-key').value='';void api.setKey(key)});
  $('change-key').addEventListener('click',()=>api.logout());$('add-account').addEventListener('click',()=>openBind());$('empty-add').addEventListener('click',()=>openBind());
  $('check-all').addEventListener('click',e=>void run(()=>api.request('/checkin',{method:'POST',body:{}}),e.currentTarget,'可运行账号已加入队列'));
  for(const b of document.querySelectorAll('[data-channel]'))b.addEventListener('click',()=>channel(b.dataset.channel));
  for(const b of document.querySelectorAll('[data-close]'))b.addEventListener('click',()=>{$(b.dataset.close).close();clearSensitiveFields($('bind-form'))});
  $('bind-dialog').addEventListener('close',()=>clearSensitiveFields($('bind-form')));
  $('bind-form').addEventListener('submit',e=>{e.preventDefault();const form=e.currentTarget;const chosen=$('channel').value;const payload={site:$('bind-site').value,label:$('bind-label').value,channel:chosen,account_id:$('bind-id').value};
    if(chosen==='token'){payload.token=$('bind-token').value;if($('bind-expiry').value)payload.expires_at_hint=Math.floor(new Date($('bind-expiry').value+'T23:59:59').getTime()/1000)}else if(chosen==='password'){payload.username=$('bind-username').value;payload.password=$('bind-password').value}else{payload.refresh_cookie=$('bind-cookie').value;payload.dedicated_session_confirmed=$('dedicated-session').checked}
    clearSensitiveFields(form);void run(async()=>{await api.request('/accounts',{method:'POST',body:payload});$('bind-dialog').close()},$('bind-submit'),'账号已验证并保存');
  });
  $('delete-confirm').addEventListener('click',e=>void run(async()=>{await api.request('/accounts?id='+encodeURIComponent($('delete-id').value),{method:'DELETE'});$('delete-dialog').close()},e.currentTarget,'凭证已删除，执行历史保留'));
  $('settings-form').addEventListener('submit',e=>{e.preventDefault();void run(()=>api.request('/settings',{method:'POST',body:{checkin_at:$('setting-time').value,jitter_minutes:Number($('setting-jitter').value),timezone:$('setting-zone').value.trim()}}),$('settings-save'),'排程设置已生效')});
  $('settings-reset').addEventListener('click',e=>void run(()=>api.request('/settings',{method:'POST',body:{checkin_at:null,jitter_minutes:null,timezone:null}}),e.currentTarget,'已恢复宿主配置默认值'));
  for(const tab of document.querySelectorAll('[data-view]'))tab.addEventListener('click',()=>{for(const t of document.querySelectorAll('[data-view]'))t.setAttribute('aria-selected',String(t===tab));for(const panel of document.querySelectorAll('[data-view-panel]'))panel.hidden=panel.dataset.viewPanel!==tab.dataset.view});
  document.addEventListener('visibilitychange',()=>void api.setVisible(!document.hidden));window.addEventListener('pagehide',()=>api.stop());void api.start();
}
if(typeof document!=='undefined'&&document.getElementById('workspace'))mount();
