import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';

// Run the bridge the way provider.Command does: its source via -e, the request file in argv.
const bridge = fs.readFileSync(fileURLToPath(new URL('./runtime.mjs', import.meta.url)), 'utf8');
const fakeCodex = `#!/usr/bin/env node
const readline = require('node:readline');
if(['check-hang','check-timeout'].includes(process.env.CASE)) setInterval(()=>{},1000);
const send = x => process.stdout.write(JSON.stringify(x)+'\\n');
readline.createInterface({input:process.stdin}).on('line',line=>{
 const m=JSON.parse(line), p=m.params;
 if(m.method==='initialize') {
  if(process.env.CASE==='init-fail')return send({id:m.id,error:{message:'initialize rejected'}});
  return send({id:m.id,result:{}});
 }
 if(process.env.CASE.startsWith('check') && !['initialized','config/read','hooks/list'].includes(m.method)) throw new Error('discovery attempted '+m.method);
 if(m.method==='config/read') {
  if(process.env.CASE==='check-timeout')return;
  send({method:'config/notification',params:{secret:'DO-NOT-PRINT'}});
  if(process.env.CASE==='check-error') return send({id:m.id,error:{message:'DO-NOT-PRINT'}});
  return send({id:m.id,result:{config:{sandbox_mode:null,approval_policy:null,secret:'DO-NOT-PRINT'}}});
 }
 if(m.method==='hooks/list') return send({id:m.id,result:process.env.CASE==='check-malformed'?{}:{data:[{cwd:p.cwds[0],hooks:[{eventName:'sessionStart',enabled:true,trustStatus:'modified',sourcePath:'hooks.json',command:'DO-NOT-PRINT'}],errors:[],warnings:[]}]}});
 if(m.method==='thread/start'||m.method==='thread/resume') {
  if(process.env.CASE==='start-fail') return send({id:m.id,error:{message:'thread start rejected'}});
  if(process.env.CASE==='resume-fail') return send({id:m.id,error:{message:'no such thread'}});
  return send({id:m.id,result:{sandbox:{type:'readOnly'},approvalPolicy:'on-request',thread:{id:process.env.CASE==='mismatch'?'wrong':p.threadId||'real-thread'}}});
 }
 if(m.method==='turn/start') {
  if(process.env.CASE==='turn-fail')return send({id:m.id,error:{message:'turn rejected'}});
  send({method:'turn/started',params:{threadId:p.threadId,turn:{id:'turn-1'}}});
  send({id:m.id,result:{turn:{id:'turn-1'}}});
  if(process.env.CASE==='early-exit') return process.exit(7);
  if(['cancel','timeout'].includes(process.env.CASE)) return;
  send({method:'turn/completed',params:{threadId:p.threadId,turn:{id:'turn-1',status:'completed'}}});
 }
 if(m.method==='turn/interrupt') {
  if(process.env.CASE==='timeout')return;
  send({id:m.id,result:{}});
  send({method:'turn/completed',params:{threadId:p.threadId,turn:{id:p.turnId,status:'interrupted'}}});
 }
});
`;
const fakeClaude = `export function query({options}) {
 if(process.env.CASE==='query-fail')throw new Error('SDK rejected options before spawn');
 const child=options.spawnClaudeCodeProcess({command:process.env.CASE==='spawn-fail'?process.execPath+'-missing':process.execPath,args:['-e','process.stdin.resume()'],env:process.env});
 const ready=new Promise((resolve,reject)=>{child.once('spawn',resolve);child.once('error',reject)});
 ready.catch(()=>{});child.stdin.on('error',()=>{});
 let interrupted=false;
 return { interrupt:async()=>{interrupted=true},close(){child.stdin.end()},async *[Symbol.asyncIterator](){
  await ready;
  const session_id=process.env.CASE==='missing-identity'?undefined:process.env.CASE==='mismatch'?'wrong':options.resume||'real-session';
  yield {type:'system',subtype:'init',session_id};
  if(process.env.CASE==='early-exit')return;
  if(process.env.CASE==='approval-then-ok')await options.canUseTool('Bash',{command:'echo x'},{});
  if(process.env.CASE==='cancel')while(!interrupted)await new Promise(r=>setTimeout(r,10));
  yield {type:'result',subtype:'success',is_error:false,session_id,total_cost_usd:0.125,num_turns:2};
 }};
}`;
async function run(provider, scenario, resume) {
 const home=fs.mkdtempSync(path.join(os.tmpdir(),'fleet-provider-test-'));
 const req={provider,check:scenario.startsWith('check'),cwd:home,prompt:'fixture',attempt:'attempt-1',state_file:path.join(home,'state.json'),cancel_file:path.join(home,'cancel'),output:path.join(home,'out.log'),resume,process_observer:process.env.FLEET_TEST_OBSERVER};
 const bin=path.join(home,'bin');fs.mkdirSync(bin);
 if(scenario!=='spawn-fail') {
  if(process.env.FLEET_TEST_OBSERVER && scenario!=='wrapped-init-fail')fs.symlinkSync(process.env.FLEET_TEST_OBSERVER,path.join(bin,'codex'));
  else fs.writeFileSync(path.join(bin,'codex'),fakeCodex,{mode:0o700});
 }
 const script=path.join(home,'fake-codex.cjs');fs.writeFileSync(script,fakeCodex);
 const mod=path.join(home,'node_modules/@anthropic-ai/claude-agent-sdk');fs.mkdirSync(mod,{recursive:true});
 fs.writeFileSync(path.join(mod,'package.json'),JSON.stringify({type:'module',exports:'./index.mjs'}));
 fs.writeFileSync(path.join(mod,'index.mjs'),fakeClaude);
 const requestFile=path.join(home,'request.json');fs.writeFileSync(requestFile,JSON.stringify(req),{mode:0o600});
 const proc=spawn(process.execPath,['--input-type=module','-e',bridge,requestFile],{stdio:['ignore','pipe','pipe'],env:{...process.env,PATH:scenario==='spawn-fail'?bin:bin+path.delimiter+process.env.PATH,FLEET_RUNTIME_HOME:home,FLEET_TEST_CODEX_SCRIPT:script,FLEET_TEST_NODE:process.execPath,CASE:scenario==='wrapped-init-fail'?'init-fail':scenario}});
 let out='',err='';proc.stdout.on('data',b=>out+=b);proc.stderr.on('data',b=>err+=b);
 const deadline=setTimeout(()=>proc.kill('SIGKILL'),['timeout','check-timeout'].includes(scenario)?22000:scenario==='check-hang'?12000:5000);
 let control;
 if(['cancel','timeout'].includes(scenario))control=setInterval(()=>{if(fs.existsSync(req.state_file)&&JSON.parse(fs.readFileSync(req.state_file)).provider_state==='running')fs.writeFileSync(req.cancel_file,'{}')},10);
 const code=await new Promise(resolve=>proc.on('close',resolve));
 clearTimeout(deadline);clearInterval(control);
 const state=JSON.parse(fs.readFileSync(req.state_file));
 fs.rmSync(home,{recursive:true,force:true});
 return {code,state,out,err};
}
test('a refused approval does not outlive a completed Claude turn',async()=>{
 const r=await run('claude','approval-then-ok');assert.equal(r.code,0,r.err);
 assert.equal(r.state.provider_state,'completed');assert.equal(r.state.error,undefined);
 assert.match(r.state.earlier_error,/requires approval/);
});
for(const provider of ['claude','codex']) {
 test(provider+' starts, retains actual identity and reports terminal state',async()=>{
  const r=await run(provider,'ok');assert.equal(r.code,0,r.err);assert.equal(r.state.provider_state,'completed');assert.equal(r.state.attempt,'attempt-1');assert.ok(r.state.provider_session);assert.match(r.out,/"type":"result"/);
  assert.equal(r.state.provider_started,true);
 });
 test(provider+' resumes the explicit identity',async()=>{
  const r=await run(provider,'ok','retained-id');assert.equal(r.code,0,r.err);assert.equal(r.state.provider_session,'retained-id');
 });
 test(provider+' rejects a different resumed identity',async()=>{
  const r=await run(provider,'mismatch','retained-id');assert.equal(r.code,1);assert.equal(r.state.provider_state,'failed');
 });
 test(provider+' premature EOF is failure',async()=>{
  const r=await run(provider,'early-exit');assert.equal(r.code,1);assert.equal(r.state.provider_state,'failed');
  assert.equal(r.state.provider_started,true);assert.equal(r.state.provider_terminal,false);
 });
 test(provider+' interrupts a running turn',async()=>{
  const r=await run(provider,'cancel');assert.equal(r.code,130,r.err);assert.equal(r.state.provider_state,'interrupted');
 });
 test(provider+' spawn failure proves no provider started',async()=>{
  const r=await run(provider,'spawn-fail');assert.equal(r.code,1,r.err);
  assert.equal(r.state.provider_started,false);assert.equal(r.state.provider_terminal,false);
  assert.equal(r.state.provider_state,'failed');assert.match(r.state.error,/ENOENT|app-server closed/);
 });
}
test('Claude query failure before spawn retains never-started evidence',async()=>{
 const r=await run('claude','query-fail');assert.equal(r.code,1);
 assert.equal(r.state.provider_started,false);assert.equal(r.state.provider_terminal,false);
});
test('Codex failed resume does not start fresh',async()=>{const r=await run('codex','resume-fail','missing');assert.equal(r.code,1);assert.match(r.state.error,/no such thread/)});

for (const resume of [undefined, 'requested-only']) {
 test('Claude requires observed identity on '+(resume?'resume':'start'),async()=>{
  const r=await run('claude','missing-identity',resume);
  assert.equal(r.code,1);assert.equal(r.state.provider_session,undefined);
  assert.equal(r.state.provider_terminal,false);assert.match(r.state.error,/no observed session identity/);
 });
}

test('interrupt timeout preserves its cause and is not provider-terminal evidence',async()=>{const r=await run('codex','timeout');assert.equal(r.code,130,r.err);assert.equal(r.state.provider_state,'failed');assert.equal(r.state.provider_terminal,false);assert.match(r.state.error,/did not acknowledge interrupt/)});

for (const scenario of ['init-fail','start-fail','resume-fail']) {
 test('Codex '+scenario+' has separate pre-turn quiescence proof',{skip:!process.env.FLEET_TEST_OBSERVER},async()=>{
  const r=await run('codex',scenario,scenario==='start-fail'?'':'missing');assert.equal(r.code,1,r.err);
  assert.equal(r.state.provider_terminal,false);assert.equal(r.state.provider_quiescent,true);
  assert.equal(r.state.turn_may_have_been_sent,false);assert.ok(r.state.pre_turn_rejection);
 });
}
test('Codex persists possible turn dispatch before a rejected turn',{skip:!process.env.FLEET_TEST_OBSERVER},async()=>{
 const r=await run('codex','turn-fail');assert.equal(r.code,1);
 assert.equal(r.state.turn_may_have_been_sent,true);assert.equal(r.state.provider_quiescent,false);
});

test('arbitrary Codex wrapper stays unknown after rejection',{skip:!process.env.FLEET_TEST_OBSERVER},async()=>{
 const r=await run('codex','wrapped-init-fail');assert.equal(r.code,1);
 assert.notEqual(r.state.provider_quiescent,true);assert.equal(r.state.provider_started,true);
});

test('Codex setup discovery never starts a thread or exposes unrelated configuration',async()=>{
 const r=await run('codex','check');assert.equal(r.code,0,r.err);
 const result=JSON.parse(r.out);
 assert.equal(result.type,'setup');assert.equal(result.configured_sandbox,null);
 assert.equal(result.configured_approval,null);assert.equal(result.hooks[0].trust,'modified');
 assert.match(result.effective_permissions,/unknown/);assert.equal(result.ready,undefined);
 assert.doesNotMatch(r.out+r.err,/DO-NOT-PRINT/);
 assert.equal(r.state.provider_session,undefined);assert.equal(r.state.provider_turn,undefined);
 assert.equal(r.state.turn_may_have_been_sent,false);assert.equal(r.state.provider_terminal,false);
 assert.equal(r.state.provider_exit_code,0);
});
for(const scenario of ['check-error','check-malformed']) {
 test('Codex '+scenario+' is explicit failure without a model fallback',async()=>{
  const r=await run('codex',scenario);assert.equal(r.code,1,r.err);
  assert.doesNotMatch(r.out+r.err,/DO-NOT-PRINT/);
  assert.equal(r.state.provider_session,undefined);assert.equal(r.state.turn_may_have_been_sent,false);
 });
}
test('Codex records resolved thread permissions on start and resume',async()=>{
 for(const resume of [undefined,'retained-id']) {
  const r=await run('codex','ok',resume);assert.equal(r.code,0,r.err);
  assert.deepEqual(r.state.sandbox_policy,{type:'readOnly'});
  assert.equal(r.state.approval_policy,'on-request');
 }
});

test('Codex setup closes a provider that ignores EOF after successful discovery',async()=>{
 const r=await run('codex','check-hang');assert.equal(r.code,0,r.err);
 assert.equal(JSON.parse(r.out).type,'setup');assert.equal(r.state.provider_exit_signal,'SIGKILL');
 assert.equal(r.state.provider_turn,undefined);
});
test('Codex setup times out an unanswered discovery RPC and reaps the provider',async()=>{
 const r=await run('codex','check-timeout');assert.equal(r.code,1,r.err);
 assert.match(r.state.error,/config\/read timed out/);
 assert.equal(r.state.provider_exit_signal,'SIGKILL');assert.equal(r.state.provider_turn,undefined);
});
