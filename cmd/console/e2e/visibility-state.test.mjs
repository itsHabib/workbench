// Pure source-level state regression; no browser or dependency install required.
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import assert from 'node:assert/strict';
const source = readFileSync(new URL('../internal/web/static/fleet.html', import.meta.url), 'utf8');
const body = source.match(/function evidenceIdentity\(x\) \{([\s\S]*?)\n        \}/)?.[1];
assert.ok(body, 'use the production evidence identity function');
const identity = new Function('x', body);
test('same-session trace changes invalidate diagnostic evidence', () => {
  const first = {agent:{session:'s'},trace:{source:'a',modified_at:10,file_bytes:100,fingerprint:'one'}};
  for (const patch of [{source:'b'},{modified_at:11},{file_bytes:101},{fingerprint:'two'}]) {
    assert.notEqual(identity(first),identity({...first,trace:{...first.trace,...patch}}));
  }
  assert.equal(identity(first),identity({...first,at:200}),'observation clock alone is not new trace evidence');
  assert.notEqual(identity(first),identity({...first,agent:{session:'replacement'}}));
});
