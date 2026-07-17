# local-advisory — Technical Design Document

> **SUPERSEDED (2026-07-08).** The phase-2 eval NO-GO'd a LOCAL backend at 7B
> (`EVAL-01.md`, T2/T3 recall 0/13), so the advisory tier stays **cloud** — see
> `docs/features/advisory/spec.md`, which carries this doc's durable contract
> (extract-shaped schema, three-part verifier, whole-diff redo, §11 hard gates)
> forward onto the cloud backend. Kept as the design-exploration record.

**Status:** superseded (design record) — the local backend was measured and rejected.
**Owner:** @itsHabib
**Date:** 2026-07-06 · **v2** 2026-07-06 (folded design review — PR #1: cursor, codex, @claude)
**Related:** `docs/features/pr-risk-engine/spec.md` (the engine this extends) · `RUBRIC.md` §6 (the
advisory contract) · portfolio plan `pers/docs/local-integration-plan-2026-07-05.md` §S1 (this is
its Phase 1) · dossier project `triage`

> **Reviewers — focus areas:** §4.2 (the extract-shaped schema — is the verifier actually
> sufficient to stop confabulated escalations?), §4.3 (per-file calls + max-merge — what does a
> cross-file escalation signal lose?), §7.2 (the flagged-file path — does the agent-as-escalator
> contract leak?), §9 (is the eval gate placed before the right phases?).

## 1. Problem & hypothesis

The advisory tier today is prose: the `/pr-risk` skill has the host cloud agent read the full diff
plus RUBRIC §6 and propose an escalation above the deterministic floor. That works, but every PR
classification spends cloud tokens on a pass that is mostly "no escalation needed," the result is
non-repeatable across host models, and the pass can't run headless (no host agent, no advisory).

**Hypothesis:** an on-box local model (Ollama, free) can run the advisory pass for the majority of
per-file calls, with the minority escalating to the host agent — at zero recall loss, because the
contract already protects us twice: the advisory may only RAISE above the floor, and anything the
local pass can't answer trustworthily is handed back to the cloud agent (today's behavior).

**Non-goals:** no cloud-API escalator function (deferred, auth-gated — the host agent IS the
escalator in v1); no sense integration (the local primitive stays its own lib — see §4.1); no
model upgrades unless the eval demands one (§10); no change to the floor, the tiers, the routing
table, or the raise-only contract.

## 2. Functional & non-functional requirements

**FR**
- FR1 — `cmd/triage-advisory` reads a unified diff on stdin, prints one merged advisory JSON:
  proposed escalation (or none), the §6 trigger, verbatim evidence, and per-file source results.
- FR2 — Per-file results that fail the verifier or confidence gate are *flagged*, listed in the
  output, and excluded from the merged proposal — the caller (the `/pr-risk` host agent) re-runs
  the advisory pass on exactly those files.
- FR3 — `final = max(floor, advisory)` is unchanged; the advisory can never lower a tier.
- FR4 — Every run is loggable to `labels/mismatches.jsonl` in the existing record shape.
  `agent_tier` / `agent_escalation` reflect the **post-merge** advisory — the max over trusted
  local results *and* any host-agent redo of flagged files — never raw tool output alone;
  `advisory_source` ∈ {`local`, `agent`, `mixed`} records which produced the merged result.
  (Review: cursor FR4/§7.2, @claude §7.2.)
- FR5 — The `local` primitive is importable by triage as a library (no HTTP plumbing in triage).

**NFR**

| Dimension | Target |
|---|---|
| Cost | $0 marginal per classification for locally-handled files |
| Recall | T2/T3 strict recall 1.0 on the labeled corpus (same bar as Experiment 01) — the gate |
| Coverage | ≥60% of per-file calls handled locally without escalation (else the seam isn't worth it) |
| Latency | Seconds-per-file is acceptable; `/pr-risk` is interactive-but-not-hot. Cold model load ~30–60s, amortized |
| Availability | Ollama down → advisory pass falls back to the host agent wholesale (today's behavior); tiers still fail closed per RUBRIC |
| Reproducibility | temperature 0; every eval and live run records `rubric_sha` + model name |

## 3. Architecture overview

```
                    pers/local  (graduates from pers/local-poc/local — NEW tiny repo)
                    ┌─────────────────────────────────────────┐
                    │ local.Ask(req, opts)                     │
                    │   verifier-fail > low-confidence gate    │
                    │   Escalate injected (nil = flag only)    │
                    └───────────────▲─────────────────────────┘
                                    │ go.mod replace ../local
   triage                           │
   ┌────────────────────────────────┴───────────────┐
   │ internal/advisory   per-file local calls,       │
   │                     verifier, max-merge         │
   │ cmd/triage-advisory stdin diff → advisory JSON  │
   └───────────────▲────────────────────────────────┘
                   │ shell-out (same discipline as triage-floor)
   /pr-risk skill  │
   floor → advisory (local) → flagged files redone by host agent → final = max(floor, advisory)
```

New: the `pers/local` repo (moved, not rewritten), `internal/advisory`, `cmd/triage-advisory`,
`labels/advisory-e01.jsonl` + `labels/diffs/`. Reused: the floor engine, the corpus, the
mismatches log, the `/pr-risk` skill frame, the eval harness (`local/cmd/eval`). The seam is the
advisory *backend*: the contract (raise-only, §6 triggers, logged) doesn't move.

## 4. Key decisions & trade-offs

### 4.1 The primitive graduates to its own tiny lib — not into sense
sense's `caller` seam is unexported, `WithCaller` is designed-but-unbuilt, two production
consumers pin sense v0.5.0, and sense's own boundary treats pick-between-callers policy as above
its seam. The escalate gate IS that policy. So: new repo `pers/local`, module
`github.com/itsHabib/local`, containing exactly what exists today — `local.go` (~150 lines),
`cmd/local`, `cmd/eval` — moved, not rewritten. `cmd/toolhealth` stays behind (workbench-specific).
Triage consumes it via a `replace github.com/itsHabib/local => ../local` directive until the lib
earns a remote (§10 Q2). *Alternative rejected:* sense grows a local caller — a fine future move
for sense's consumers, but it's a sense feature, not a dependency of this one.

### 4.2 Extract-shaped schema, not classify
The POC's hard lesson: a simple-looking *classify* confabulates on dense content at confidence
1.0; *extract* survives. Diffs are the densest content in the portfolio. So the advisory schema
forces the model to point at things that can be checked, and the verifier checks them:

```json
{
  "escalate":   "none | T2 | T3",
  "trigger":    "none | trust-boundary-widening | production-default | invariant-relocation",
  "evidence":   "<verbatim quote from the diff hunk>",
  "confidence": 0.0-1.0
}
```

Verifier — for an **escalating** result (`escalate` ∈ {T2, T3}) all three must hold; a result that
escalates without passing all three is **flagged**, never merged:
1. `escalate != "none"` ⟹ `trigger` ∈ {`trust-boundary-widening`, `production-default`,
   `invariant-relocation`} — the *non-none* §6 triggers only. `none` is a schema-legal enum value
   (for non-escalating results), so "trigger ∈ enum" is NOT sufficient: an escalation must name a
   *real* trigger. (Caught in review — cursor L111 + @claude §4.2: the enum-membership check as
   first written let `escalate=T2, trigger=none` through.)
2. `evidence` is a verbatim substring of the file's diff text (whitespace-normalized) — the model
   cannot escalate on content it didn't actually see; and
3. `len(evidence) ≥ 20` chars — a single common word or a run of whitespace is a substring of
   almost any diff, so the substring check alone is not specificity. (Review: @claude §4.2
   secondary.)

A `"none"` result needs no evidence and no trigger; its only risk is a *missed* escalation, which
the confidence gate plus the corpus recall bar (§11) police instead (§10 Q3 — @claude concurs the
verifier can't tell genuine reassurance from confabulated reassurance, so evidence-for-none adds no
signal). The trigger set mirrors §6's three known triggers; growing §6 means growing the set +
re-running the eval (RUBRIC stays the control plane — the set is derived from it, and a §6 edit is
already a T3 change).

### 4.3 Per-file calls, deterministic max-merge
Proven in the POC: nine dense items in one call dropped three; one call each was 9/9. So the diff
is split per file (`ParseUnifiedDiff` already yields per-file changes), one `local.Ask` per file,
then a pure-Go merge: highest proposed tier wins, ties keep every (trigger, evidence, file) tuple.
*Cost:* a cross-file pattern (e.g. a guard removed in one file and its caller loosened in another)
is invisible to any single call. *Accepted because:* the floor's content signals already fire
per-line on each file independently, and §6's known triggers are file-local in practice — an
*empirical* claim, not a structural guarantee, so it's logged as an open question (§10 Q5), not
just asserted here. The flagged-file path is the structural backstop: any flag sends the **whole
diff** to the host agent (§4.4), so a cross-file risk touching a flagged file is still caught. The
only residual gap is a cross-file risk where *every* involved file is locally trusted as `none`.
Revisit if a live miss shows one (log it to `mismatches.jsonl`, grow §6, re-eval).

### 4.4 Agent-as-escalator (v1) — no cloud call from the primitive
`Opts.Escalate` stays nil in triage. When any file is flagged, the `/pr-risk` skill directs the
host agent to redo the advisory pass **over the whole diff** (not just the flagged hunks) and
merges its proposal via max. Giving it the whole diff is what preserves the "never worse than
today" property: a risk spanning a flagged file and a trusted-`none` file is invisible to the
per-file local pass, so a flagged-hunks-only redo would be *weaker* than today's all-cloud
advisory, not equal to it. Whole-diff redo on any flag restores cross-file visibility, needs no
auth, and is the cheapest escalator. (Review: codex §7.2 P2, @claude §4.3/§7.2 — the original
"redo exactly those hunks" phrasing was the gap.) *Alternative deferred:* a real `cloud.Escalator`
fn (Anthropic API on the Max subscription) — only needed when advisory runs headless, and it
touches auth, so it waits for an explicit go.

### 4.5 The eval gates the wiring — and needs no advisory Go code to run
`local/cmd/eval` drives prompt+schema against a dataset directly, so the gate (phase 2) runs
before the engine (phase 3) is built. If the eval NO-GOs at 7B, we measure 14B once; if that
NO-GOs, phases 3–4 don't happen and the advisory stays cloud — the plan doesn't stall (§9).

### 4.6 Expected labels derive from the corpus, mechanically
`labels/corpus-e01.tsv` holds 24 real PRs with consensus tiers. For each: run the floor on the
vendored diff; `expected escalation = consensus > floor ? consensus : none`. No new labeling
judgment — the corpus IS the oracle (its two blind labelers already did the judging). Diffs are
vendored (capped) into `labels/diffs/` so the eval is offline and repeatable — which also gives
triage its first *automated* corpus harness (Experiment 01 was agent runs, hand-scored).

## 5. Data model

- **Advisory output** (stdout of `cmd/triage-advisory`):
  `{advisory_tier, trigger, evidence, files: [{file, escalate, trigger, evidence, confidence, source, reason}], flagged: [file...], model, rubric_sha}`
  — `source`/`reason` come straight from `local.Result` (`"local"`, or the flag reason).
- **Eval dataset** `labels/advisory-e01.jsonl`: one row per corpus PR-file… no — one row per
  corpus **PR**: `{input: <capped unified diff>, expected: <"none"|"T2"|"T3">, meta: "<repo>#<pr> floor=<T>"}`
  scored per-PR on the merged result (recall is a per-PR property; per-file scoring would
  double-count multi-file PRs). The harness's per-file splitting happens inside the scored call.
- **Vendored diffs** `labels/diffs/<repo>-<pr>.diff`: capped (~1500 lines, matching what the
  advisory sees live), fetched once by a small script committed next to them.
- **Mismatch log**: existing `labels/mismatches.jsonl` shape + `advisory_source: "local"|"agent"`.
- **Prompt + schema**: `internal/advisory/prompt.txt` + `schema.json`, embedded via `go:embed` —
  policy-as-data, same files the eval consumes (one source of truth for gate and runtime).

## 6. API contract

```go
// internal/advisory
type FileResult struct {
    File, Escalate, Trigger, Evidence string
    Confidence                        float64
    Source, Reason                    string // from local.Result; Reason non-empty = flagged
}
type Advisory struct {
    Tier    string       // "none" | "T2" | "T3" — max over trusted file results
    Trigger string       // trigger of the max-tier result
    Evidence string
    Files   []FileResult
    Flagged []string     // files the caller must redo (verifier/confidence failed)
}
func Run(ctx context.Context, diff floor.Diff) (Advisory, error)  // per-file local.Ask + merge
func Verify(fileDiff string) func(json.RawMessage) bool           // §4.2 verifier (escalate⟹real-trigger + evidence substring + ≥20 chars); exported for the eval
```

- `cmd/triage-advisory`: stdin = unified diff; stdout = `Advisory` JSON (indented); `-v` human
  table, same conventions as `cmd/triage-floor`. Exit 0 on success (including flagged files);
  exit 1 only on operational failure (Ollama unreachable, parse failure) — callers treat exit 1
  as "advisory unavailable, fall back to host agent," never as a tier.
- `local` lib: unchanged public API (`Req`, `Result`, `Opts`, `Ask`, `Local`) — the graduation
  moves code and renames the module path only.
- Error model: per-file model errors don't abort the run; the file lands in `Flagged` with the
  error as `Reason`. Only stdin/parse/no-Ollama failures abort.

## 7. Key flows

### 7.1 Advisory pass (happy path)
1. `/pr-risk` obtains the diff (as today) and runs the floor (as today).
2. Skill pipes the diff to `triage-advisory`.
3. The tool parses per-file, calls `local.Ask` per file with prompt+schema, `Verify(fileDiff)`
   as the verifier, `MinConfidence: 0.7`, `Escalate: nil`.
4. Trusted results max-merge; output printed.
5. **If `Flagged` is empty:** skill computes `final = max(floor, advisory.Tier)`, logs with
   `advisory_source: "local"`, routes by tier. **If `Flagged` is non-empty:** defer the `final`
   computation to §7.2 step 3 — step 5 must not set `final` from local-only output while a redo is
   pending. (Raise-only holds by construction either way: `max` with the floor as one operand.)

### 7.2 Flagged files (the escalation path)
1. Same as above, but `Flagged` is non-empty (verifier fail, low confidence, or model error).
2. The skill instructs the host agent: *redo the §6 advisory pass yourself over the **whole diff**
   (§4.4 — not just the flagged hunks, so cross-file risk touching a trusted file is still seen);
   this yields a host advisory tier.*
3. Skill computes `final = max(floor, advisory.Tier_trusted, host_advisory.Tier)` — the max over
   the floor, the trusted local per-file results, and the host redo — then logs with
   `advisory_source: "mixed"` (or `"agent"` if every file was flagged) and routes by tier. This is
   the *only* place `final` is set on a flagged run (§7.1 step 5 is deferred here).
4. Worst case — every file flagged — is exactly today's behavior (host agent does the whole-diff
   pass; trusted set empty). Because the host always sees the whole diff, the local tier can
   subtract nothing from recall; it can only fail to add savings. (Review: cursor L42/L208, codex
   P2, @claude §7.2 — the original stopped at step 2 and never set `final` post-merge.)

### 7.3 Ollama down / tool missing
`triage-advisory` exits 1 with a clear stderr; the skill falls back to the full host-agent pass
(today's behavior). RUBRIC's fail-closed floors are untouched (floor already ran). No tier is ever
derived from an operational failure.

### 7.4 The eval (phase 2, gates phases 3–4)
1. `labels/fetch-diffs.sh` (committed) vendors capped diffs for the 24 corpus PRs.
2. A small derivation step emits `advisory-e01.jsonl` per §4.6 (floor via `triage-floor` on each
   vendored diff).
3. `local/cmd/eval` runs prompt+schema over the dataset, per-PR; `-verbatim`-style verification
   uses the same `Verify` logic (harness may need a small hook — acceptable growth, it stays
   generic).
4. Score per §11. Record the verdict in this doc + the README, GO/NO-GO decides phases 3–4.

## 8. Concurrency / consistency / failure model

Single process, sequential per-file calls (Ollama serializes a single model anyway); no shared
state; no retries at the advisory layer (a failed call = flagged file = escalation — retrying
against a deterministic temp-0 model buys nothing). Consistency: `rubric_sha` recorded per run;
an advisory produced under one rubric version is never compared against another. The one
consistency invariant that matters — advisory can only raise — is enforced structurally by `max`
(the floor always an operand) wherever `final` is computed: §7.1 step 5 on a clean run, §7.2 step 3
on a flagged run.

## 9. Rollout / implementation plan

| # | Phase | Goal | High-level tasks | Depends on | Gate |
|---|---|---|---|---|---|
| 1 | `graduate-lib` | `pers/local` exists as its own repo; triage can import it | move `local.go` + `cmd/local` + `cmd/eval` from `pers/local-poc/local`; module `github.com/itsHabib/local`; README (API, the rule, eval verdicts); update local-poc pointers + `/review-digest`-adjacent docs; `go vet`/build green | — | — |
| 2 | `advisory-eval` | The gate: measured GO/NO-GO for local advisory | `labels/fetch-diffs.sh` + vendored `labels/diffs/`; derive `advisory-e01.jsonl`; write `prompt.txt` + `schema.json`; **measure the cloud advisory's over-escalation baseline on the corpus** (§11.3); **per-trigger recall split** in `cmd/eval`; run the eval; record verdict here + README | 1 | **VALIDATION GATE — §11 (all three hard)** |
| 3 | `advisory-engine` | `internal/advisory` + `cmd/triage-advisory`, tested | `Run`/`Verify` + max-merge; per-file `local.Ask` wiring; table tests incl. verifier rejections + flagged paths; `-v` output | 2 = GO | — |
| 4 | `skill-wiring` | `/pr-risk` runs local-first advisory live | registry skill step-3 edit (run tool, redo flagged, log `advisory_source`); prove on 5–10 live runs via `mismatches.jsonl`; record live coverage % | 3 | live-proof: verdict parity with cloud advisory on those runs |

Scope: each phase is one PR-sized unit (weighted-LOC well under the ideal band; the vendored
diffs in phase 2 are fixtures at 0×). Phases 3–4 are **committed only on a GO** from phase 2;
a NO-GO at 7B triggers one 14B measurement (same dataset), then either GO-at-14B or the advisory
stays cloud and this feature closes with the eval harness as its residue (still a win: triage
gains an automated corpus harness).

## 10. Open questions

1. **Does `pers/local` need a remote?** The `replace ../local` directive works locally but breaks
   GitHub CI and any cloud agent building triage. Triage has no CI today, so phase 1 can ship with
   `replace`; the moment triage grows CI or a cloud-driven implementation run touches Go code, the
   lib needs a private remote + `GOPRIVATE`. Recommendation: push `pers/local` private when phase 3
   starts; cheap, and it removes a class of "works on my machine."
2. **Capped-diff size.** 1500 lines is a guess at "what the advisory should see per PR." Too small
   truncates evidence for big PRs; too large re-invites the density failure per file. The eval can
   measure this cheaply (two runs, two caps) — worth doing if the first run's misses cluster on
   truncated PRs.
3. **Should `"none"` results require evidence too?** Forcing a "why not" quote could catch
   miss-shaped errors, but it also invites confabulated reassurance the verifier can't
   distinguish from real reassurance. v1 says no; the recall bar polices misses. Reviewer input
   welcome.
4. **Enum growth protocol.** §6 grows via dogfood; each growth means enum + prompt + re-eval. Is
   re-running the full eval per trigger addition acceptable cadence, or does the dataset need
   per-trigger slices so a new trigger only needs its own rows? v1: full re-run (24 PRs is cheap).
5. **Cross-file escalation among all-trusted files.** §4.3's "§6 triggers are file-local in
   practice" is empirical. The flagged-file path sends the whole diff to the host agent (§4.4), so
   the only residual gap is a cross-file risk where *every* involved file is locally trusted as
   `none`. Believed rare (content signals fire per-line on each file), but unproven — logged here,
   watched via `mismatches.jsonl`, not designed-around in v1. (Promoted from §4.3 per @claude.)
6. **`MinConfidence: 0.7` provenance.** Inherited from the POC default, not tuned for diffs. It's
   the *weak* gate (the verifier is the strong one), so a loose value mostly costs a few extra
   flagged files (= cloud redos), never recall. Record the basis in the prompt/schema notes when
   phase 3 lands; revisit only if phase-2 coverage (§11.2) comes in low because of it. (Review:
   @claude — don't ship a magic number unexplained.)

## 11. Validation plan

The phase-2 gate, on `labels/advisory-e01.jsonl` (24 PRs, consensus-labeled), qwen2.5:7b, temp 0.
All three are **hard** — each is NO-GO on its own:

1. **T2/T3 strict recall = 1.0** on `final = max(floor, advisory-with-flagged-redone-as-escalated)`
   — counting a flagged file as "escalated to cloud" (the design working), no consensus-T2/T3 PR
   may land below its consensus tier. Same bar Experiment 01 failed at 16/17; the floor fixes since
   then carry most of it — the advisory must cover the semantic residual (`dossier#67`-class miss).
   **Reported per-trigger** as well as overall: with ≤8 PRs per trigger type, an overall pass can
   hide a trigger the model misses entirely, so `local/cmd/eval` splits recall by trigger. (Review:
   @claude eval-corpus note.)
2. **Local-handled ≥ 60%** of per-file calls trusted (not flagged) — below that the token savings
   don't justify the seam.
3. **Inflation ≤ the cloud advisory's — HARD, measured, not just tracked.** triage's whole thesis
   is to *reduce* review load; a T3-happy model that quotes real diff lines can satisfy recall (1)
   and coverage (2) while routing many low-consensus PRs to owner/adversarial review — a regression
   vs today's cloud advisory that a recall-only gate waves through. So phase 2 **also runs the
   current cloud/agent advisory over the same 24 vendored diffs** and records its over-escalation
   rate (`final > consensus`); the local advisory's over-escalation rate must be **≤ that cloud
   baseline**. Absolute backstop: **≤ 30% of PRs raised above consensus** if the cloud baseline
   can't be measured that cycle. (Review: codex "gate inflation before wiring" P2, @claude §11 —
   promoted from "tracked" to a hard parity gate; this is the change that protects the thesis, and
   it deliberately tensions against (1) so the model must be *calibrated*, not trigger-happy.)

Binary, offline, repeatable. Re-run on any model, prompt, schema, or §6 change. NO-GO at 7B on any
of the three → one qwen2.5:14b measurement; still NO-GO → advisory stays cloud (§9). Live
confirmation after phase 4: 5–10 real `/pr-risk` runs logged with verdict parity against what the
cloud advisory would have said (spot-check via the host agent on the same PRs).
