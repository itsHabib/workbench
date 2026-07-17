// Score the cloud advisory pass on the held-out corpus. Join each host-agent
// proposal to the corpus consensus, run it through triage-advisory (which applies
// the deterministic verifier + max-merge), and report the §11 hard gates:
//   1. per-trigger recall on the advisory-addressable residual (consensus >= T2,
//      floor < consensus) — final must reach consensus;
//   2. inflation across ALL rows (final > consensus) — the review-load backstop.
//
//   node labels/score-advisory-heldout.mjs <proposals.jsonl> [corpus.tsv]
import { readFileSync, writeFileSync, existsSync } from "node:fs";
import { execSync } from "node:child_process";
import { resolve } from "node:path";
import { tmpdir } from "node:os";
import { join } from "node:path";

const PROPS = process.argv[2];
const CORPUS = process.argv[3] ?? "labels/corpus-heldout.tsv";
const cand = ["bin/triage-advisory.exe", "bin/triage-advisory"].map((p) => resolve(p)).find(existsSync);
const ADV = cand ? `"${cand}"` : "go run ./triage-advisory";
const RANK = { T0: 0, T1: 1, T2: 2, T3: 3 };
const norm = (t) => t.replace("?", "").trim();

const consensus = new Map(); // "repo#num" -> consensus tier
for (const line of readFileSync(CORPUS, "utf8").split("\n")) {
  const c = line.split("\t");
  if (c[0] && /^[a-z][a-z0-9-]*#\d+$/.test(c[0])) consensus.set(c[0], norm(c[3]));
}

const proposals = readFileSync(PROPS, "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l));
const pad = (s, n) => String(s).padEnd(n);

// verdict for one corpus id under a given proposal (escalate=none when absent).
const idOf = (raw) => (raw.includes("#") ? raw : raw.replace("-", "#"));
const seen = new Set();
const rows = [];
const verdict = (id, p) => {
  const [repo, num] = id.split("#");
  const diffPath = `labels/diffs/${repo}-${num}.diff`;
  if (!existsSync(diffPath)) { console.error(`MISS diff ${id}`); return null; }
  const proposalJSON = JSON.stringify({ escalate: p.escalate ?? "none", trigger: p.trigger ?? "none", evidence: p.evidence ?? "", confidence: p.confidence ?? 0, why: p.why ?? "" });
  const pf = join(tmpdir(), `triage-prop-${repo}-${num}.json`);
  writeFileSync(pf, proposalJSON);
  const v = JSON.parse(execSync(`${ADV} -proposal "@${pf}"`, { input: readFileSync(diffPath, "utf8"), maxBuffer: 1 << 26 }));
  return { id, con: consensus.get(id) ?? "?", Floor: v.floor, Escalate: v.escalate, Trigger: v.trigger, Final: v.final, Rejected: v.rejected ?? [], proposedTrigger: p.trigger ?? "none" };
};

for (const p of proposals) {
  const id = idOf(p.pr);
  seen.add(id);
  const r = verdict(id, p);
  if (r) rows.push(r);
}
// A partial proposal set must not shrink the denominator: every corpus id without
// a submitted proposal is scored as escalate=none (floor stands), and flagged — else
// omitting the hard residual rows would report misleadingly high recall (codex, PR #4).
for (const id of consensus.keys()) {
  if (seen.has(id)) continue;
  console.error(`NO-PROPOSAL ${id} — scored as escalate=none (floor stands)`);
  const r = verdict(id, {});
  if (r) rows.push(r);
}

console.log(pad("PR", 14) + pad("flr", 4) + pad("con", 4) + pad("esc", 5) + pad("trigger", 24) + pad("rej?", 6) + pad("fin", 4) + "result");
for (const r of rows) {
  const rejected = (r.Rejected && r.Rejected.length) ? "REJ" : "-";
  const addressable = RANK[r.con] >= 2 && RANK[r.Floor] < RANK[r.con];
  let result = "ok";
  if (RANK[r.Final] > RANK[r.con]) result = "INFLATED";
  else if (addressable) result = RANK[r.Final] >= RANK[r.con] ? "caught" : "MISS";
  console.log(pad(r.id, 14) + pad(r.Floor, 4) + pad(r.con, 4) + pad(r.Escalate, 5) + pad(r.Trigger, 24) + pad(rejected, 6) + pad(r.Final, 4) + result);
}

const addressable = rows.filter((r) => RANK[r.con] >= 2 && RANK[r.Floor] < RANK[r.con]);
const caught = addressable.filter((r) => RANK[r.Final] >= RANK[r.con]);
const missed = addressable.filter((r) => RANK[r.Final] < RANK[r.con]);
const inflated = rows.filter((r) => RANK[r.Final] > RANK[r.con]);

console.log(`\nADDRESSABLE RESIDUAL (consensus>=T2, floor<consensus): ${addressable.length}`);
console.log(`  recall: caught ${caught.length}/${addressable.length} = ${(100 * caught.length / addressable.length).toFixed(0)}%  missed: ${missed.map((r) => r.id).join(", ") || "none"}`);

// per-trigger recall — group addressable rows by the trigger the consensus implies
// (we key on the proposed trigger for caught rows, and report misses explicitly).
const byTrig = {};
for (const r of addressable) {
  const t = r.Trigger !== "none" ? r.Trigger : r.proposedTrigger !== "none" ? r.proposedTrigger : "(none-proposed)";
  byTrig[t] ??= { caught: 0, total: 0 };
  byTrig[t].total++;
  if (RANK[r.Final] >= RANK[r.con]) byTrig[t].caught++;
}
console.log(`  per-trigger recall:`);
for (const [t, s] of Object.entries(byTrig)) console.log(`    ${pad(t, 24)} ${s.caught}/${s.total}`);

console.log(`\nINFLATION (final > consensus, all rows): ${inflated.length}/${rows.length} = ${(100 * inflated.length / rows.length).toFixed(0)}% (hard gate: <=30%) — ${inflated.map((r) => r.id).join(", ") || "none"}`);
