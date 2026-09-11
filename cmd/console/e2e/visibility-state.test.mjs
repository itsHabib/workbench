// Pure source-level state regression; no browser or dependency install required.
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import assert from 'node:assert/strict';
const source = readFileSync(new URL('../internal/web/static/fleet.html', import.meta.url), 'utf8');
const body = source.match(/function evidenceIdentity\(x\) \{([\s\S]*?)\n        \}/)?.[1];
assert.ok(body, 'use the production evidence identity function');
const identity = new Function('x', body);
const matches = new Function('snapshot', 'result', source.match(/function diagnosticMatches\(snapshot, result\) \{([\s\S]*?)\n        \}/)[1]);
test('same-session trace changes invalidate diagnostic evidence', () => {
  const first = {agent:{session:'s'},trace:{source:'a',modified_at:10,file_bytes:100,fingerprint:'one'}};
  for (const patch of [{source:'b'},{modified_at:11},{file_bytes:101},{fingerprint:'two'}]) {
    assert.notEqual(identity(first),identity({...first,trace:{...first.trace,...patch}}));
  }
  assert.equal(identity(first),identity({...first,at:200}),'observation clock alone is not new trace evidence');
  assert.notEqual(identity(first),identity({...first,agent:{session:'replacement'}}));
});
test('a diagnostic must describe the currently displayed trace bytes', () => {
  const snapshot = {trace:{source:'trace.jsonl',fingerprint:'old'}};
  assert.equal(matches(snapshot,{source:'trace.jsonl',fingerprint:'old'}),true);
  for (const result of [{source:'trace.jsonl',fingerprint:'new'},{source:'replacement.jsonl',fingerprint:'old'},{source:'trace.jsonl'}]) {
    assert.equal(matches(snapshot,result),false);
  }
});
test('unchanged inspect evidence still refreshes Attempts when the report changes or fails', async () => {
  const load = source.match(/async function loadDetail\([^)]*\) \{[\s\S]*?\n        \}/)[0];
  const run = new Function('view','reportChanged',`return (async () => {
    let selected = 'agent', generation = 1, diagnosticVersion = 0, diagnostic = null, diagnosing = false;
    let detail = {agent:{session:'s'}}, detailError = '', tab = view, renders = 0;
    const api = async () => ({agent:{session:'s'}});
    const renderDetail = () => { renders++; };
    const evidenceIdentity = () => 'same trace';
    ${load}
    await loadDetail(reportChanged);
    return renders;
  })()`);
  assert.equal(await run('attempts',true),1);
  assert.equal(await run('attempts',false),0);
  assert.equal(await run('overview',true),0);
});
