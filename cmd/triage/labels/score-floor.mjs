// Score the deterministic floor against the corpus consensus. This is the floor's
// own regression guard: run triage-floor on each vendored diff and compare its tier
// to the held-out consensus label, showing the signal that set the floor so an
// over-call (false positive) is debuggable at a glance.
//
// Over-calls (floor > consensus) are unambiguous bugs — wasted human review.
// Under-calls (floor < consensus) split into floor gaps (a signal RUBRIC claims
// deterministically) and semantic residuals (the advisory's job, correctly deferred);
// this scorer reports them, the reader categorizes.
//
//   node labels/score-floor.mjs                             # Experiment-01 corpus
//   node labels/score-floor.mjs labels/corpus-heldout.tsv   # any corpus TSV (same columns)
import { readFileSync, existsSync } from "node:fs";
import { execSync } from "node:child_process";
import { resolve } from "node:path";

// Prefer a built binary (either OS name), else fall back to `go run` — cross-platform.
const cand = ["bin/triage-floor.exe", "bin/triage-floor"].map((p) => resolve(p)).find(existsSync);
const FLOOR = cand ? `"${cand}"` : "go run ./cmd/triage-floor";
const RANK = { T0: 0, T1: 1, T2: 2, T3: 3 };
const norm = (t) => t.replace("?", "").trim();

const rows = readFileSync(process.argv[2] ?? "labels/corpus-e01.tsv", "utf8")
  .split("\n").map((l) => l.split("\t"))
  .filter((c) => c[0] && /^[a-z][a-z0-9-]*#\d+$/.test(c[0]))
  .map(([pr, , , consensus, note]) => ({ pr, consensus: norm(consensus), note }));

const pad = (s, n) => String(s).padEnd(n);
let match = 0; const over = [], under = [];
console.log(pad("PR", 13) + pad("flr", 4) + pad("con", 4) + pad("Δ", 8) + "top signal (why the floor is what it is)");
for (const { pr, consensus, note } of rows) {
  const [repo, num] = pr.split("#");
  const path = `labels/diffs/${repo}-${num}.diff`;
  if (!existsSync(path)) { console.error(`MISS ${pr}`); process.exitCode = 1; continue; }
  const res = JSON.parse(execSync(FLOOR, { input: readFileSync(path, "utf8"), maxBuffer: 1 << 26 }));
  const floor = res.floor;
  // the signal(s) at the floor tier — what's actually driving it
  const top = (res.signals || []).filter((s) => s.tier === floor).map((s) => s.why).slice(0, 2).join(" · ") || "(none)";
  const d = RANK[floor] - RANK[consensus];
  const mark = d === 0 ? "match" : d > 0 ? `OVER +${d}` : `under ${d}`;
  if (d === 0) match++; else if (d > 0) over.push({ pr, floor, consensus, top }); else under.push({ pr, floor, consensus, note });
  console.log(pad(pr, 13) + pad(floor, 4) + pad(consensus, 4) + pad(mark, 8) + top.slice(0, 80));
}
console.log(`\nexact ${match}/${rows.length}; over-calls ${over.length}; under-calls ${under.length}`);
console.log(`OVER-CALLS (false-positive review load — fix these):`);
for (const o of over) console.log(`  ${pad(o.pr, 12)} floor ${o.floor} > consensus ${o.consensus}  <= ${o.top.slice(0, 70)}`);
console.log(`UNDER-CALLS (floor gap OR semantic residual — categorize):`);
for (const u of under) console.log(`  ${pad(u.pr, 12)} floor ${u.floor} < consensus ${u.consensus}  | ${u.note ?? ""}`);
