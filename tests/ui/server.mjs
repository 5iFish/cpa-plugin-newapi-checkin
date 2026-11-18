// Isolated visual test server. No real CPA or site credentials are read.
import http from 'node:http';
import {readFile} from 'node:fs/promises';
const root=new URL('../../internal/console/web/',import.meta.url);
let calls=0;
const longName='用于验证手机端换行的很长账号名称与特殊字符 <img src=x onerror=alert(1)>';
const accounts=[{account_id:'a'.repeat(64),site:'https://example.test/a-long-deployment-path',username:'daily-user',label:longName,kind:'pat',channel:'token',status:'ready',token_hint:'…1234',expires_at:Math.floor(Date.now()/1000)+3600,total_quota:125000,total_checkins:18,stats_synced_at:new Date().toISOString(),last_result:{status:'success',server_date:'2026-11-08',quota_awarded:5000}}, {account_id:'b'.repeat(64),site:'https://blocked.example.test',username:'second-user',label:'需要人机验证的站点',kind:'session',channel:'password',status:'turnstile_blocked',total_quota:60000,total_checkins:9}, {account_id:'c'.repeat(64),site:'https://scope.example.test',username:'restricted',label:'待补充权限',kind:'pat',status:'permission_denied',total_quota:0,total_checkins:0}];
accounts.push({account_id:'d'.repeat(64),site:'https://delete.example.test',username:'retained-user',label:'凭证删除失败，保留重试入口',kind:'pat',status:'delete_failed',total_quota:800,total_checkins:1});
const end=Math.floor(Date.now()/1000),start=end-86400;
const healthAccounts={};
for(const [index,a] of accounts.entries()){
 const buckets=Array.from({length:48},(_,i)=>({start:start+i*1800,success:index===2?0:i<3?0:12+(i%5)*3,failed:index===2?0:i>=17&&i<=22?3:i===30?15:0}));
 healthAccounts[a.account_id]={account_id:a.account_id,start,end,buckets,success:buckets.reduce((n,b)=>n+b.success,0),failed:buckets.reduce((n,b)=>n+b.failed,0),complete:index!==1,truncated:false,stale:index===3,synced_at:new Date((end-(index===3?900:0))*1000).toISOString(),checked_at:new Date().toISOString(),error:index===1?'permission_denied':index===3?'network_error':''};
}
let refreshing=false;
const health=()=>({accounts:healthAccounts,refreshing});
const modelNames=['gpt-4o','gpt-4o-mini','claude-3-7-sonnet','claude-sonnet-4-5','deepseek-chat','deepseek-reasoner','gemini-2.0-flash','qwen-max','qwen-plus','llama-3.3-70b','mistral-large','kimi-k2','grok-3','doubao-pro-32k','glm-4.7'];
const modelsAccounts={};
for(const [index,a] of accounts.entries()){
  modelsAccounts[a.account_id]=index===1
    ?{account_id:a.account_id,models:[],complete:false,stale:false,synced_at:'0001-01-01T00:00:00Z',checked_at:new Date().toISOString(),error:'permission_denied'}
    :index===2
      ?{account_id:a.account_id,models:[],complete:true,stale:false,synced_at:new Date().toISOString(),checked_at:new Date().toISOString(),error:''}
      :{account_id:a.account_id,models:modelNames.slice(0,index===3?6:modelNames.length),complete:true,stale:index===3,synced_at:new Date((end-(index===3?46800:0))*1000).toISOString(),checked_at:new Date().toISOString(),error:index===3?'network_error':''};
}
const models=()=>({accounts:modelsAccounts,refreshing});
const server=http.createServer(async(req,res)=>{
 const url=new URL(req.url,'http://localhost');
 if(url.pathname==='/__test/stats'){res.setHeader('Content-Type','application/json');res.end(JSON.stringify({calls}));return}
 if(url.pathname.startsWith('/v0/management/')){calls++;res.setHeader('Content-Type','application/json');if(req.headers.authorization!=='Bearer ui-test-key'){res.writeHead(req.headers.authorization==='Bearer ui-forbidden-key'?403:401);res.end('{}');return}
	   if(url.pathname.endsWith('/status')){res.end(JSON.stringify({success:true,data:{accounts,jobs:{},work_items:{},schedules:{},history:[],settings:{checkin_at:'09:00',jitter_minutes:20,timezone:'Asia/Shanghai'},health:health(),models:models()}}));return}
	   if(url.pathname.endsWith('/health')){if(req.method==='POST'&&!refreshing){refreshing=true;setTimeout(()=>{refreshing=false},1200)}res.writeHead(refreshing?202:200);res.end(JSON.stringify({success:true,data:{health:health(),models:models()}}));return}
   if(url.pathname.endsWith('/stats')){res.end(JSON.stringify({success:true,data:{from_cache:true,stats:{range:url.searchParams.get('range'),start,end,synced_at:new Date().toISOString(),totals:{},accounts:[],models:[],series:[],failed:{}}}}));return}
   if(url.pathname.endsWith('/balance')){res.end(JSON.stringify({success:true,data:{accounts,failed:{}}}));return}
   if(url.pathname.endsWith('/accounts')&&req.method==='POST'){res.writeHead(422);res.end(JSON.stringify({success:false,error:{kind:'refresh_outcome_unknown'}}));return}
   res.end(JSON.stringify({success:true,data:{job_id:'test-job'}}));return;
 }
 const file=url.pathname.endsWith('console.css')?'console.css':url.pathname.endsWith('console.js')?'console.js':'console.html';
 res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/html');res.end(await readFile(new URL(file,root)));
});
server.listen(18400,'127.0.0.1',()=>console.log('Visual test server: http://127.0.0.1:18400/v0/resource/plugins/newapi-checkin/console'));
