// Derive the advisory eval dataset from the corpus + the deterministic floor.
//
// For each corpus PR: expected advisory = consensus > floor ? consensus : "none".
// The advisory only owes an escalation where the floor under-called the consensus
// tier; everywhere else the correct answer is "none" (the floor already caught it,
// and the advisory must NOT inflate). Emits labels/advisory-e01.jsonl of
// {input: <capped diff>, expected, meta} for local/cmd/eval.
//
//   node labels/build-dataset.mjs
import { readFileSync, writeFileSync, existsSync } from "node:fs";
import { execSync } from "node:child_process";
import { resolve } from "node:path";

// Prefer a built binary (either OS name), else fall back to `go run` — cross-platform.
// Absolute + quoted: cmd.exe won't take a forward-slash relative path.
const cand = ["bin/triage-floor.exe", "bin/triage-floor"].map((p) => resolve(p)).find(existsSync);
const FLOOR = cand ? `"${cand}"` : "go run ./cmd/triage-floor";

const RANK = { T0: 0, T1: 1, T2: 2, T3: 3 };
const norm = (t) => t.replace("?", "").trim(); // "T2?" (labeler disagreement, higher shown) -> "T2"
const MODEL_CAP = 1500; // lines of diff the advisory model sees (density limit); the floor sees the full diff
const cap = (diff) => diff.split("\n").slice(0, MODEL_CAP).join("\n");

// node labels/build-dataset.mjs [corpus.tsv] [out.jsonl] — defaults are the E01 pair.
const CORPUS = process.argv[2] ?? "labels/corpus-e01.tsv";
const OUT = process.argv[3] ?? "labels/advisory-e01.jsonl";

const rows = readFileSync(CORPUS, "utf8")
  .split("\n")
  .map((l) => l.split("\t"))
  .filter((c) => c[0] && /^[a-z][a-z0-9-]*#\d+$/.test(c[0]))
  .map(([pr, , , consensus, note]) => ({ pr, consensus: norm(consensus), note }));

const out = [];
let missing = 0;
for (const { pr, consensus, note } of rows) {
  const [repo, num] = pr.split("#");
  const path = `labels/diffs/${repo}-${num}.diff`;
  if (!existsSync(path)) {
    console.error(`MISS  ${pr} — no vendored diff (run fetch-diffs.sh)`);
    missing++;
    continue;
  }
  const diff = readFileSync(path, "utf8"); // FULL diff
  const floor = JSON.parse(execSync(FLOOR, { input: diff, maxBuffer: 1 << 26 })).floor; // floor sees the full diff
  const expected = RANK[consensus] > RANK[floor] ? consensus : "none";
  out.push({ input: cap(diff), expected, meta: `${pr} floor=${floor} consensus=${consensus}` }); // model sees the capped diff
  const flag = expected === "none" ? "  " : "→↑"; // →↑ marks a residual the advisory must catch
  console.error(`${flag} ${pr.padEnd(12)} floor=${floor} consensus=${consensus}  expect=${expected}  | ${note ?? ""}`);
}

if (missing) {
  console.error(`\nrefusing to write a partial dataset: ${missing} corpus diff(s) missing — run labels/fetch-diffs.sh first.`);
  console.error(`(a shrunk corpus makes the residual denominator and recall look better than Experiment-01.)`);
  process.exit(1);
}
writeFileSync(OUT, out.map((r) => JSON.stringify(r)).join("\n") + "\n");
const residual = out.filter((r) => r.expected !== "none");
console.error(`\n${out.length} rows -> ${OUT}`);
console.error(`residual (advisory must escalate): ${residual.length} — ${residual.map((r) => r.meta.split(" ")[0]).join(", ")}`);
