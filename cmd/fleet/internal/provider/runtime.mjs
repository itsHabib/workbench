// One provider turn. Fleet Go owns all scheduling and attempt reservations.
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';
import { spawn } from 'node:child_process';
import { createInterface } from 'node:readline';

// The request file is complete before this process exists; argv holds only its path.
const request = JSON.parse(fs.readFileSync(process.argv[1], 'utf8'));
const state = { attempt: request.attempt, provider: request.provider,
  provider_state: 'starting', provider_started: false, provider_terminal: false,
  trace: request.trace || request.output + ".trace.jsonl" };
let interrupted = false;
let interrupt = async () => {};
let close = async () => {};
let timer;
let interruptTimeout;
function publish(fields = {}) {
  Object.assign(state, fields);
  const tmp = request.state_file + '.tmp';
  fs.writeFileSync(tmp, JSON.stringify(state) + '\n', { mode: 0o600 });
  fs.renameSync(tmp, request.state_file);
}
// settledError moves a mid-turn error (an approval the runtime refused, a provider
// input request) aside once the turn completes: status must show the terminal truth,
// and the earlier refusal stays on the record as earlier_error and in the trace.
function settledError() {
  return state.error ? { earlier_error: state.error, error: undefined } : {};
}
function output(record) { process.stdout.write(JSON.stringify(record) + '\n'); }
function event(record) {
  if (request.check) return; // Discovery notifications may contain configuration secrets.
  fs.appendFileSync(state.trace, JSON.stringify(record) + '\n', {mode: 0o600});
  output(record);
}
function activity(name, fields = {}) {
  publish({ last_provider_event: name, last_provider_event_at: Date.now() / 1000, ...fields });
}
function spawnProvider(command, args, options) {
  // Persist uncertainty before spawning: a killed bridge must not hide a child.
  publish({ provider_started: true });
  const child = spawn(command, args, options);
  let spawned = false;
  child.once('spawn', () => { spawned = true; });
  child.once('error', () => {
    // Node failed to create a process. Errors after spawn/abort are not this proof.
    if (!spawned && child.pid === undefined) publish({ provider_started: false });
  });
  return child;
}
// Resolve only the official npm entrypoint using its same platform package.
// Other wrappers remain intact; their forks cannot earn a no-fork proof.
function codexExecutable() {
  if (process.platform !== 'darwin') return { path: 'codex', native: false };
  for (const dir of (process.env.PATH || '').split(path.delimiter)) {
    let exe;
    try { exe = fs.realpathSync(path.join(dir, 'codex')); fs.accessSync(exe, fs.constants.X_OK); }
    catch { continue; }
    if (isMachO(exe)) return { path: exe, native: true };
    try {
      const root = path.dirname(path.dirname(exe));
      const pkg = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8'));
      if (pkg.name !== '@openai/codex' || fs.realpathSync(path.join(root, pkg.bin.codex)) !== exe) return { path: exe, native: false };
      const require = createRequire(path.join(root, 'package.json'));
      const platformPackage = require.resolve(`@openai/codex-darwin-${process.arch}/package.json`);
      const platform = JSON.parse(fs.readFileSync(platformPackage, 'utf8'));
      if (platform.version !== `${pkg.version}-darwin-${process.arch}`) return { path: exe, native: false };
      const arch = { arm64: 'aarch64', x64: 'x86_64' }[process.arch];
      const native = path.join(path.dirname(platformPackage), 'vendor', `${arch}-apple-darwin`, 'bin', 'codex');
      fs.accessSync(native, fs.constants.X_OK);
      if (!isMachO(native)) return { path: exe, native: false };
      return { path: native, native: true };
    } catch { return { path: exe, native: false }; }
  }
  return { path: 'codex', native: false };
}
function isMachO(file) {
  let fd;
  try {
    fd = fs.openSync(file, 'r');
    const magic = Buffer.alloc(4);
    fs.readSync(fd, magic, 0, 4, 0);
    return ['cffaedfe', 'cefaedfe', 'feedfacf', 'feedface', 'cafebabe', 'bebafeca', 'cafebabf', 'bfbafeca'].includes(magic.toString('hex'));
  } catch { return false; }
  finally { if (fd !== undefined) fs.closeSync(fd); }
}
async function cancel() {
  if (interrupted) return;
  interrupted = true;
  publish({ provider_state: 'interrupting' });
  timer = setTimeout(() => {
    interruptTimeout = 'provider did not acknowledge interrupt within 10 seconds';
    publish({ provider_state: 'failed', error: interruptTimeout });
    void close().catch(e => publish({error: interruptTimeout, close_error: e.message}));
  }, 10000);
  try { await interrupt(); }
  catch (e) { publish({ error: `interrupt: ${e.message}` }); }
}
const control = setInterval(() => {
  if (fs.existsSync(request.cancel_file)) void cancel();
}, 200);
process.on('SIGINT', cancel);
process.on('SIGTERM', cancel);

async function claude() {
  const root = process.env.FLEET_RUNTIME_HOME || path.join(os.homedir(), '.local/share/fleet/runtime');
  const require = createRequire(path.join(root, 'package.json'));
  const { query } = await import(pathToFileURL(require.resolve('@anthropic-ai/claude-agent-sdk')));
  const options = { cwd: request.cwd, pathToClaudeCodeExecutable: 'claude', settingSources: ['user', 'project', 'local'],
    spawnClaudeCodeProcess: ({ command, args, cwd, env, signal }) =>
      spawnProvider(command, args, { cwd, env, signal, stdio: ['pipe', 'pipe', 'inherit'] }),
    systemPrompt: { type: 'preset', preset: 'claude_code' },
    ...(request.model ? { model: request.model } : {}),
    ...(request.resume ? { resume: request.resume } : {}),
    ...(request.permission_mode ? { permissionMode: request.permission_mode } : {}),
    canUseTool: async () => {
      activity('approval_required', { provider_state: 'blocked', error: 'tool requires approval; headless runtime cannot approve it' });
      return { behavior: 'deny', message: 'Requires operator approval; report the blocker and end this turn.' };
    } };
  // Streaming input enables the SDK control channel; no token streaming needed.
  async function* input() {
    yield { type: 'user', message: { role: 'user', content: request.prompt }, parent_tool_use_id: null, ...(request.resume ? { session_id: request.resume } : {}) };
  }
  const q = query({ prompt: input(), options });
  interrupt = () => q.interrupt();
  close = async () => q.close();
  if (interrupted) await interrupt();
  let terminal = false;
  try {
    for await (const message of q) {
      if (message.session_id) {
        if (request.resume && message.session_id !== request.resume) throw new Error('provider resumed a different session');
        state.provider_session = message.session_id;
      }
      activity(message.type + (message.subtype ? '/' + message.subtype : ''),
        state.provider_state === 'starting' ? { provider_state: 'running' } : {});
      if (typeof message.error === 'string') state.error = message.error;
      event(message);
      if (message.type !== 'result') continue;
      if (!state.provider_session) throw new Error('Claude returned no observed session identity');
      terminal = true;
      publish({provider_terminal: true});
      publish({ provider_state: interrupted ? 'interrupted' : message.is_error ? 'failed' : 'completed',
        reason: message.subtype, ...(message.is_error ? {} : settledError()) });
      process.exitCode = interrupted ? 130 : message.is_error ? 1 : 0;
      break;
    }
    if (!terminal) throw new Error('Claude SDK ended without a result');
  } finally { await close(); }
}

// A configured policy is not the effective policy of a future thread. Discovery
// never starts a thread, executes hooks, or turns missing defaults into readiness.
function object(value) { return value !== null && typeof value === 'object' && !Array.isArray(value); }
async function checkCodex(call) {
  const config = await call('config/read', { cwd: request.cwd, includeLayers: false });
  const inventory = await call('hooks/list', { cwds: [request.cwd] });
  const entry = inventory?.data?.find(row => row.cwd === request.cwd);
  if (!object(config?.config) || !object(entry) || !Array.isArray(entry.hooks) || !Array.isArray(entry.errors) || !Array.isArray(entry.warnings)) {
    throw new Error('unsupported Codex setup response');
  }
  const hooks = entry.hooks.map(hook => {
    if (!object(hook) || typeof hook.eventName !== 'string' || typeof hook.enabled !== 'boolean' ||
        typeof hook.trustStatus !== 'string' || typeof hook.sourcePath !== 'string') {
      throw new Error('unsupported Codex hook response');
    }
    return { event: hook.eventName, enabled: hook.enabled, trust: hook.trustStatus, source: hook.sourcePath };
  });
  return { type: 'setup', provider: 'codex', cwd: request.cwd,
    configured_sandbox: config.config.sandbox_mode ?? null,
    configured_approval: config.config.approval_policy ?? null,
    hooks, hook_errors: entry.errors.length, hook_warnings: entry.warnings.length,
    effective_permissions: 'unknown until thread start',
    hook_execution: 'not tested; discovery does not prove Fleet identity or event coverage' };
}

async function codex() {
  const selected = codexExecutable();
  const executable = selected.path;
  const proofFile = request.state_file + '.process.json';
  // Discovery needs no durable turn proof. Own its process group directly so
  // timeout cleanup cannot kill an observer while stranding the app-server.
  const observer = selected.native && !request.check ? request.process_observer : undefined;
  const checkGroup = request.check && process.platform !== 'win32';
  publish({ turn_may_have_been_sent: false, provider_executable: executable });
  const command = observer || executable;
  const args = observer ? ['_provider-process', request.attempt, proofFile, executable, 'app-server'] : ['app-server'];
  const child = spawnProvider(command, args, { cwd: request.cwd, detached: checkGroup, stdio: ['pipe', 'pipe', request.check ? 'ignore' : 'inherit'] });
  const pending = new Map();
  let sequence = 0;
  let finish, fail;
  const completed = new Promise((resolve, reject) => { finish = resolve; fail = reject; });
  // Attach the rejection handler before initialization so an early exit is handled.
  completed.catch(() => {});
  function rejectAll(error) {
    for (const { reject } of pending.values()) reject(error);
    pending.clear();
    fail(error);
  }
  const exited = new Promise(resolve => child.on('close', (code, signal) => {
    publish({ provider_exit_code: code, provider_exit_signal: signal });
    rejectAll(new Error(`Codex app-server closed (${code ?? signal})`));
    resolve();
  }));
  child.on('error', rejectAll);
  child.stdin.on('error', rejectAll);
  function send(value) { child.stdin.write(JSON.stringify(value) + '\n'); }
  function call(method, params) {
    const id = ++sequence;
    return new Promise((resolve, reject) => {
      const timeout = request.check ? setTimeout(() => {
        pending.delete(id);
        reject(new Error(`${method} timed out`));
      }, 10000) : undefined;
      pending.set(id, { resolve: value => { clearTimeout(timeout); resolve(value); },
        reject: error => { clearTimeout(timeout); reject(error); }, method });
      send({ id, method, params });
    });
  }
  close = async () => {
    child.stdin.end();
    const kill = setTimeout(() => {
      if (!checkGroup) { child.kill('SIGKILL'); return; }
      try { process.kill(-child.pid, 'SIGKILL'); }
      catch (e) { if (e.code !== 'ESRCH') rejectAll(e); }
    }, 5000);
    await exited;
    clearTimeout(kill);
  };
  interrupt = async () => {
    if (state.provider_turn) await call('turn/interrupt', { threadId: state.provider_session, turnId: state.provider_turn });
  };
  const lines = createInterface({ input: child.stdout });
  lines.on('line', line => {
    try {
      const msg = JSON.parse(line);
      if (msg.id !== undefined && !msg.method) {
        const waiter = pending.get(msg.id);
        if (!waiter) return;
        pending.delete(msg.id);
        if (msg.error) { waiter.reject(Object.assign(new Error(msg.error.message), { rejectedMethod: waiter.method })); return; }
        waiter.resolve(msg.result);
        return;
      }
      if (msg.id !== undefined) {
        // Never invent an approval or answer on behalf of the operator.
        activity(msg.method, { provider_state: 'blocked', error: 'provider requested operator input' });
        send({ id: msg.id, error: { code: -32601, message: 'Headless Fleet cannot supply operator approval or input' } });
        return;
      }
      const p = msg.params || {};
      if (p.threadId && state.provider_session && p.threadId !== state.provider_session) return;
      event(msg);
      activity(msg.method);
      if (msg.method === 'turn/started') {
        publish({ turn_may_have_been_sent: true, provider_turn: p.turn.id, provider_state: interrupted ? 'interrupting' : 'running' });
        if (interrupted) void interrupt().catch(rejectAll);
      }
      if (msg.method === 'turn/completed') finish(p.turn);
    } catch (e) { rejectAll(e); }
  });
  try {
    await call('initialize', { clientInfo: { name: 'fleet', title: 'Fleet', version: '1' },
      ...(request.check ? { capabilities: { experimentalApi: true } } : {}) });
    send({ method: 'initialized', params: {} });
    if (request.check) {
      output(await checkCodex(call));
      return;
    }
    const params = { cwd: request.cwd, ...(request.model ? { model: request.model } : {}),
      ...(request.resume ? { threadId: request.resume } : {}) };
    const result = await call(request.resume ? 'thread/resume' : 'thread/start', params);
    if (!result.thread?.id) throw new Error('Codex returned no thread identity');
    if (request.resume && result.thread.id !== request.resume) throw new Error('provider resumed a different thread');
    publish({ sandbox_policy: result.sandbox ?? null, approval_policy: result.approvalPolicy ?? null,
      provider_session: result.thread.id, provider_state: interrupted ? 'interrupting' : 'running' });
    if (interrupted) throw new Error('interrupted before turn start');
    publish({ turn_may_have_been_sent: true });
    const turn = await call('turn/start', { threadId: result.thread.id, input: [{ type: 'text', text: request.prompt }] });
    publish({ provider_turn: turn.turn.id });
    const terminal = await completed;
    if (terminal.id !== state.provider_turn) throw new Error('terminal result belongs to another turn');
    const status = terminal.status;
    if (!['completed', 'interrupted', 'failed'].includes(status)) throw new Error(`unknown terminal state: ${status}`);
    publish({ provider_terminal: true, provider_state: status, reason: status, ...(terminal.error ? { error: terminal.error.message } : status === 'failed' ? {} : settledError()) });
    output({ type: 'result', session_id: state.provider_session, subtype: status,
      is_error: status !== 'completed' });
    process.exitCode = status === 'completed' ? 0 : status === 'interrupted' ? 130 : 1;
  } catch (e) {
    if (state.turn_may_have_been_sent === false && ['initialize', 'thread/start', 'thread/resume'].includes(e.rejectedMethod)) {
      publish({ pre_turn_rejection: e.rejectedMethod });
    }
    throw e;
  } finally {
    await close();
    if (observer) {
      let proof;
      try { proof = JSON.parse(fs.readFileSync(proofFile, 'utf8')); } catch { /* Unknown cleanup keeps the reservation. */ }
      const matching = proof?.schema === 'fleet.process-proof.v1' && proof.attempt === request.attempt && proof.provider === 'codex';
      if (matching && proof.never_started === true) publish({ provider_started: false });
      publish({ process_proof: proofFile, provider_quiescent: !!(matching && proof.quiescent === true && state.pre_turn_rejection && state.turn_may_have_been_sent === false) });
    }
  }
}

try {
  publish();
  if (!['claude', 'codex'].includes(request.provider)) throw new Error(`unsupported provider: ${request.provider}`);
  if (request.provider === 'claude') await claude();
  if (request.provider === 'codex') await codex();
} catch (e) {
  publish({ provider_state: 'failed', error: interruptTimeout || e.message });
  output({ type: 'result', session_id: state.provider_session, subtype: 'runtime_error', is_error: true, error: request.check ? 'Codex setup inspection failed; check provider compatibility and availability' : e.message });
  process.exitCode = interrupted ? 130 : 1;
} finally {
  clearInterval(control);
  clearTimeout(timer);
}
