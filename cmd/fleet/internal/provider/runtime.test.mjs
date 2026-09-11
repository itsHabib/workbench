import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const bridge = fileURLToPath(new URL('./runtime.mjs', import.meta.url));
const fakeCodex = `#!/usr/bin/env node
const readline = require('node:readline');
const send = x => process.stdout.write(JSON.stringify(x)+'\\n');
readline.createInterface({input:process.stdin}).on('line',line=>{
 const m=JSON.parse(line), p=m.params;
 if(m.method==='initialize') return send({id:m.id,result:{}});
 if(m.method==='thread/start'||m.method==='thread/resume') {
  if(process.env.CASE==='resume-fail') return send({id:m.id,error:{message:'no such thread'}});
  return send({id:m.id,result:{thread:{id:process.env.CASE==='mismatch'?'wrong':p.threadId||'real-thread'}}});
 }
 if(m.method==='turn/start') {
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
 let interrupted=false;
 return { interrupt:async()=>{interrupted=true},close(){},async *[Symbol.asyncIterator](){
  const session_id=process.env.CASE==='mismatch'?'wrong':options.resume||'real-session';
  yield {type:'system',subtype:'init',session_id};
  if(process.env.CASE==='early-exit')return;
  if(process.env.CASE==='cancel')while(!interrupted)await new Promise(r=>setTimeout(r,10));
  yield {type:'result',subtype:'success',is_error:false,session_id,total_cost_usd:0.125,num_turns:2};
 }};
}`;
async function run(provider, scenario, resume) {
 const home=fs.mkdtempSync(path.join(os.tmpdir(),'fleet-provider-test-'));
 const req={provider,cwd:home,prompt:'fixture',attempt:'attempt-1',state_file:path.join(home,'state.json'),cancel_file:path.join(home,'cancel'),output:path.join(home,'out.log'),resume};
 const bin=path.join(home,'bin');fs.mkdirSync(bin);
 fs.writeFileSync(path.join(bin,'codex'),fakeCodex,{mode:0o700});
 const mod=path.join(home,'node_modules/@anthropic-ai/claude-agent-sdk');fs.mkdirSync(mod,{recursive:true});
 fs.writeFileSync(path.join(mod,'package.json'),JSON.stringify({type:'module',exports:'./index.mjs'}));
 fs.writeFileSync(path.join(mod,'index.mjs'),fakeClaude);
 const proc=spawn(process.execPath,[bridge],{env:{...process.env,PATH:bin+path.delimiter+process.env.PATH,FLEET_RUNTIME_HOME:home,CASE:scenario}});
 let out='',err='';proc.stdout.on('data',b=>out+=b);proc.stderr.on('data',b=>err+=b);
 proc.stdin.end(JSON.stringify(req));
 const deadline=setTimeout(()=>proc.kill('SIGKILL'),scenario==='timeout'?19000:5000);
 let control;
 if(['cancel','timeout'].includes(scenario))control=setInterval(()=>{if(fs.existsSync(req.state_file)&&JSON.parse(fs.readFileSync(req.state_file)).provider_state==='running')fs.writeFileSync(req.cancel_file,'{}')},10);
 const code=await new Promise(resolve=>proc.on('close',resolve));
 clearTimeout(deadline);clearInterval(control);
 const state=JSON.parse(fs.readFileSync(req.state_file));
 fs.rmSync(home,{recursive:true,force:true});
 return {code,state,out,err};
}
for(const provider of ['claude','codex']) {
 test(provider+' starts, retains actual identity and reports terminal state',async()=>{
  const r=await run(provider,'ok');assert.equal(r.code,0,r.err);assert.equal(r.state.provider_state,'completed');assert.equal(r.state.attempt,'attempt-1');assert.ok(r.state.provider_session);assert.match(r.out,/"type":"result"/);
 });
 test(provider+' resumes the explicit identity',async()=>{
  const r=await run(provider,'ok','retained-id');assert.equal(r.code,0,r.err);assert.equal(r.state.provider_session,'retained-id');
 });
 test(provider+' rejects a different resumed identity',async()=>{
  const r=await run(provider,'mismatch','retained-id');assert.equal(r.code,1);assert.equal(r.state.provider_state,'failed');
 });
 test(provider+' premature EOF is failure',async()=>{
  const r=await run(provider,'early-exit');assert.equal(r.code,1);assert.equal(r.state.provider_state,'failed');
 });
 test(provider+' interrupts a running turn',async()=>{
  const r=await run(provider,'cancel');assert.equal(r.code,130,r.err);assert.equal(r.state.provider_state,'interrupted');
 });
}
test('Codex failed resume does not start fresh',async()=>{const r=await run('codex','resume-fail','missing');assert.equal(r.code,1);assert.match(r.state.error,/no such thread/)});

test('interrupt timeout preserves its cause and is not provider-terminal evidence',async()=>{const r=await run('codex','timeout');assert.equal(r.code,130,r.err);assert.equal(r.state.provider_state,'failed');assert.equal(r.state.provider_terminal,false);assert.match(r.state.error,/did not acknowledge interrupt/)});
