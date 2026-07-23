// gen-fixtures.mjs — deterministic gate-state fixtures for the console e2e suite.
//
// The console shells the real `gate` binary; these fixtures are the on-disk gate
// state it serves. We build a genuine hash-chained log.jsonl here rather than let
// a test hand-wave the chain: every line's `hash` is computed EXACTLY as gate's
// state.hashArtifact does (see cmd/gate/internal/state/state.go), so the real
// `gate audit` replays it as intact once an anchor is bound over it at run time.
//
// Why no anchor is written here: gate's anchor record is keyed by an HMAC secret
// held outside the state dir AND its filename embeds a hash of the state dir's
// ABSOLUTE path (state.stateDirTag). Both are only knowable at run time, in the
// temp dir the harness copies the fixture into — so the harness binds the anchor
// there by minting one grant through the real `gate` binary (an append rebinds
// the anchor over the whole prior log). This keeps the committed fixture path-
// independent: it audits intact wherever the repo is checked out.
//
// hashArtifact input bytes (must match Go byte-for-byte):
//   id "|" kind "|" run "|" time(RFC3339Nano) "|" prev "|" (p+","  for each parent) body
// Times use whole-second UTC ("...Z"), whose RFC3339Nano form is identical, so
// gate re-formats them to the same string it hashes.

import { createHash, randomBytes } from "node:crypto";
import { mkdirSync, writeFileSync, rmSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const fixturesDir = join(here, "..", "fixtures");

const KIND_PREFIX = {
  evidence: "evd",
  verdict: "vrd",
  grant: "grt",
  action: "act",
  escalation: "esc",
  judgment: "jdg",
};

function id(kind) {
  return KIND_PREFIX[kind] + "_" + randomBytes(8).toString("hex");
}
function runID() {
  return "run_" + randomBytes(8).toString("hex");
}

// hashArtifact mirrors state.hashArtifact exactly. `bodyRaw` is the verbatim
// JSON text stored as the line's "body" value — the same bytes gate re-hashes on
// audit (json.RawMessage preserves the source bytes), so we hash them, not a
// re-encoding.
function hashArtifact(a, bodyRaw) {
  const h = createHash("sha256");
  h.update(a.id + "|" + a.kind + "|" + a.run + "|" + a.time + "|" + a.prev + "|");
  for (const p of a.parents || []) h.update(p + ",");
  h.update(bodyRaw);
  return h.digest("hex");
}

// Chain builds a log: each entry links prev->hash like gate's Append.
class Chain {
  constructor() {
    this.prev = "";
    this.lines = [];
  }
  append(kind, run, parents, body) {
    const bodyRaw = JSON.stringify(body);
    const a = {
      id: id(kind),
      kind,
      run,
      time: this.nextTime(),
      parents: parents && parents.length ? parents : undefined,
      prev: this.prev,
    };
    a.hash = hashArtifact(a, bodyRaw);
    // Construct the line by hand so the "body" bytes are exactly bodyRaw.
    const head =
      `{"id":${JSON.stringify(a.id)},"kind":${JSON.stringify(a.kind)},` +
      `"run":${JSON.stringify(a.run)},"time":${JSON.stringify(a.time)},` +
      (a.parents ? `"parents":${JSON.stringify(a.parents)},` : "") +
      `"body":${bodyRaw},"prev":${JSON.stringify(a.prev)},"hash":${JSON.stringify(a.hash)}}`;
    this.lines.push(head);
    this.prev = a.hash;
    return a;
  }
  nextTime() {
    // Deterministic, whole-second, increasing UTC timestamps.
    this._t = (this._t || Date.UTC(2026, 6, 20, 12, 0, 0)) + 60_000;
    return new Date(this._t).toISOString().replace(/\.\d{3}Z$/, "Z");
  }
  text() {
    return this.lines.join("\n") + "\n";
  }
}

// buildDocket returns a log with one grant, then a parked run (evidence +
// reduced verdict + escalation) for a single PR subject.
function buildDocket() {
  const c = new Chain();
  const repo = "example/console-e2e";
  const number = 42;
  const head = "9f2c1a7bd4e6f0a1b2c3d4e5f60718293a4b5c6d";

  const grant = c.append("grant", "run_mint01", null, {
    repo,
    action: "merge",
    max_tier: "T1",
    max_cycles: 3,
    // Far-future so the ledger renders it live regardless of when tests run.
    expires_at: "2099-01-01T00:00:00Z",
    minted_by: "operator",
    sig: "fixture-signature-not-verified-by-console",
  });

  const run = runID();
  c.append("evidence", run, null, {
    type: "pr-view",
    subject: { repo, number, head_sha: head },
    data: {
      title: "Add a retry budget to the dispatcher loop",
      headRefOid: head,
    },
  });
  const verdict = c.append("verdict", run, null, {
    source: "reducer",
    subject: { repo, number, head_sha: head },
    decision: "escalate",
    tier: "T1",
    confidence: 1,
    why: "reviewer flagged a possibly unbounded retry",
  });
  c.append("escalation", run, [verdict.id, grant.id], {
    outcome: "parked_for_judgment",
    verdict: verdict.id,
    grant: grant.id,
    question:
      "Reviewer flagged a possibly unbounded retry loop; confirm the budget caps at 5 attempts before this may merge.",
    repo,
    number,
  });
  return c.text();
}

function writeFixture(name, logText) {
  const dir = join(fixturesDir, name, "state");
  rmSync(join(fixturesDir, name), { recursive: true, force: true });
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, "log.jsonl"), logText);
  return join(dir, "log.jsonl");
}

const docket = buildDocket();
writeFixture("good", docket);

// Tampered = the good log with one byte of an existing body mutated and its hash
// left stale, so gate's chain replay reports a body-hash mismatch. The harness
// still anchors it first (so a would-be "intact" render over a broken chain is a
// loud, real failure — the guard's teeth).
const tampered = docket.replace(
  "caps at 5 attempts",
  "caps at 9 attempts", // mutate the question; hash line is NOT recomputed
);
if (tampered === docket) {
  throw new Error("tamper mutation did not apply — fixture text drifted");
}
writeFixture("tampered", tampered);

// Empty inbox: a grant, no parked run. Audits intact; docket shows a clean empty
// state instead of an error.
function buildEmpty() {
  const c = new Chain();
  c.append("grant", "run_mint02", null, {
    repo: "example/console-e2e",
    action: "merge",
    max_tier: "T1",
    max_cycles: 3,
    expires_at: "2099-01-01T00:00:00Z",
    minted_by: "operator",
    sig: "fixture-signature-not-verified-by-console",
  });
  return c.text();
}
writeFixture("empty", buildEmpty());

console.log("wrote fixtures: good, tampered, empty under", fixturesDir);
