// Score the advisory eval: join the model's proposals back to the dataset,
// apply the §4.2 verifier, compute final = max(floor, advisory), and report
// recall on the residuals + inflation on the none-expected set.
//
// The verifier (an escalating result must pass ALL): trigger is one of the three
// REAL §6 triggers (not the schema-legal "none"); evidence >= 20 chars; evidence
// is a whitespace-normalized substring of the diff the model saw. A failing
// escalation is FLAGGED — treated as escalated-to-cloud, i.e. credited as
// catching consensus (the design's escalate-safe fallback), never as a miss.
//
//   node labels/score-advisory.mjs
import { readFileSync } from "node:fs";

const REAL = new Set(["trust-boundary-widening", "production-default", "invariant-relocation"]);
const RANK = { T0: 0, T1: 1, T2: 2, T3: 3, none: -1 };
const norm = (s) => s.toLowerCase().replace(/[^a-z0-9]+/g, " ").trim();
const jsonl = (p) => readFileSync(p, "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l));

const data = new Map(); // pr -> {input, expected, floor, consensus}
for (const r of jsonl("labels/advisory-e01.jsonl")) {
  const [pr, fl, co] = r.meta.split(" ");
  data.set(pr, { input: r.input, expected: r.expected, floor: fl.split("=")[1], consensus: co.split("=")[1] });
}

const rows = [];
for (const r of jsonl("labels/advisory-results.jsonl")) {
  const pr = r.meta.split(" ")[0];
  const d = data.get(pr);
  if (!d) {
    console.error(`orphan result ${pr} — not in advisory-e01.jsonl (stale results?); skipped`);
    continue;
  }
  const o = r.output ?? {};
  const escalate = o.escalate ?? "none";
  const evidence = o.evidence ?? "";
  let flagged = false, verify = "n/a";
  if (escalate !== "none") {
    const ne = norm(evidence);
    const okTrig = REAL.has(o.trigger);
    const okLen = evidence.length >= 20;
    // ne !== "" first: norm() of a punctuation-only quote is "", and every string
    // .includes("") — without this a non-alphanumeric "evidence" would pass the substring check.
    const okSub = ne !== "" && norm(d.input).includes(ne);
    flagged = !(okTrig && okLen && okSub);
    verify = flagged ? `FLAG(${!okTrig ? "trig" : !okLen ? "len" : "subst"})` : "ok";
  }
  // final: flagged escalation -> cloud -> credit consensus; trusted escalation -> max(floor,escalate); none -> floor
  const effective = flagged ? d.consensus : escalate === "none" ? d.floor : escalate;
  const final = RANK[effective] > RANK[d.floor] ? effective : d.floor;
  rows.push({ pr, ...d, escalate, trigger: o.trigger ?? "none", conf: o.confidence ?? 0, evidence, flagged, verify, final });
}

const residual = rows.filter((r) => r.expected !== "none");
const clean = rows.filter((r) => r.expected === "none");
const caught = residual.filter((r) => RANK[r.final] >= RANK[r.consensus]);
const missed = residual.filter((r) => RANK[r.final] < RANK[r.consensus]);
// Inflation is over-escalation ANYWHERE — a none-expected PR pushed above consensus OR a residual
// over-tiered past its consensus (e.g. T3 on a T2 residual). Counting only clean rows would let an
// over-aggressive model hide over-tiering on the residual set and still clear the backstop.
const inflated = rows.filter((r) => RANK[r.final] > RANK[r.consensus]);
const localHandled = rows.filter((r) => !r.flagged).length;

const pad = (s, n) => String(s).padEnd(n);
console.log(pad("PR", 13) + pad("flr", 4) + pad("con", 4) + pad("exp", 5) + pad("→esc", 5) + pad("trigger", 24) + pad("vfy", 12) + pad("fin", 4) + "result");
for (const r of rows) {
  const res = r.expected === "none" ? (RANK[r.final] > RANK[r.consensus] ? "INFLATED" : "ok-none") : RANK[r.final] >= RANK[r.consensus] ? (r.flagged ? "caught(cloud)" : "caught") : "MISS";
  console.log(pad(r.pr, 13) + pad(r.floor, 4) + pad(r.consensus, 4) + pad(r.expected, 5) + pad(r.escalate, 5) + pad(r.trigger, 24) + pad(r.verify, 12) + pad(r.final, 4) + res);
}
console.log(`\nresiduals ${residual.length}: caught ${caught.length} (recall ${(100 * caught.length / residual.length).toFixed(0)}%), missed ${missed.length}`);
console.log(`  missed: ${missed.map((r) => r.pr).join(", ") || "none"}`);
console.log(`local-handled (not flagged): ${localHandled}/${rows.length} (${(100 * localHandled / rows.length).toFixed(0)}%)`);
console.log(`inflation (final > consensus, ALL rows): ${inflated.length}/${rows.length} = ${(100 * inflated.length / rows.length).toFixed(0)}% (hard gate: <=30% backstop) — ${inflated.map((r) => r.pr).join(", ") || "none"}`);
