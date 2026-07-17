# EVAL-01 — local advisory gate verdict (2026-07-06)

**Verdict: NO-GO for the local 7B advisory.** Strict T2/T3 recall on the residual set was
**0/13**. The semantic residual the advisory owes is cloud-tier judgment — exactly what the
"content density, not task type" principle predicts. Reproduce with `labels/run-eval.sh`.

## Setup
- **Corpus:** the 24 Experiment-01 PRs (`labels/corpus-e01.tsv`), full diffs vendored to
  `labels/diffs/` by `fetch-diffs.sh`.
- **Floor on the full diff; model on a 1500-line cap.** The deterministic floor must see the whole
  diff or it misses a late-sorting signal file; only the model input is capped (`build-dataset.mjs`).
- **Expected** = `consensus > floor ? consensus : none` — the advisory owes an escalation only where
  the floor under-called. 13 residuals, 11 none-expected.
- **Model:** qwen2.5:7b, temp 0, prompt `internal/advisory/prompt.txt` + schema
  `internal/advisory/schema.json` (the extract-shaped {escalate, trigger, evidence, confidence}).
- **Verifier:** an escalating result must name a real §6 trigger, quote ≥20 chars of verbatim diff,
  and that quote must be a substring of the diff; a fail is FLAGGED (credited as escalated-to-cloud).

## Result

| metric | value | bar | |
|---|---|---|---|
| T2/T3 strict recall (residuals) | **0/13 (0%)** | 1.0 | ❌ |
| inflation (final > consensus, all PRs) | 13% | ≤30% backstop | ✓ (moot) |
| local-handled (not flagged) | 23/24 (96%) | ≥60% | ✓ (moot) |

Recall is the gate and it is emphatically failed. The other two pass but are moot under a recall of 0.

## Why it failed — two illustrative cases (not noise)

- **ship#163** (win32 `danger-full-access` sandbox drop; needs T3). The model got the trigger
  *right* — `trust-boundary-widening`, evidence `+    sandboxMode: resolveSandboxMode(),`,
  confidence 0.95 — but graded it **T2, not T3**. It saw the risk and could not judge its severity.
- **dossier#67** (extracts the task state-machine guards to `domain.rs`; the design's poster case
  for `invariant-relocation`; needs T2). The model output `escalate: none` — while its `evidence`
  field held a *perfect prose description* of the relocation ("introduction of
  `assert_invariants_after_step` to check state transitions and invariants"). It **comprehended the
  change but could not map that comprehension to the structured decision.** (It also returned
  `confidence: 85`, ignoring the 0–1 scale — schema-instruction slippage.)

Across the residuals the pattern is consistent: where the model escalated it **under-tiered**
(T2 where T3 was owed) or **mislabeled the trigger**; where the case needed the subtlest read
(invariant-relocation) it **declined outright**. This is the POC's finding one level up: small
models extract reliably but do not *judge* dense semantic risk. The advisory residual is judgment,
so it stays cloud.

## The escalate-safe design already absorbs this
A NO-GO is not a dead end — it is the gate doing its job. Per the TDD §9/§11 NO-GO path, the
advisory tier **stays on cloud** (the current `/pr-risk` host-agent pass), unchanged. Nothing
regresses: the seam was never going to ship confident-wrong tiers, because the gate measured first.

## Orthogonal finding — the floor under-calls (and over-calls) on real diffs
Deriving the dataset surfaced a floor-quality issue independent of local models. Of the 13
residuals, only ~4 are true *semantic* residuals (the three §6 triggers). The rest are cases the
RUBRIC's own **deterministic** signals claim to catch but didn't fire on:
- **under-calls:** `dossier#88` new public API → T1 (RUBRIC §5.1 says T2); `dossier#82` sync→async
  Mutex → T1 (§5.2 concurrency → T2); `ship#169` persisted-trace telemetry → T1 (§5.1 persisted
  shape → T2); `rooms#53` SIGKILL/reap → T2 (§5.1 irreversible → T3).
- **over-calls:** `dossier#80` doc-comments on `S3Config` → **T3** (should be ~T0); `dossier#77`
  CAS-gate-mostly-tests → T3 (consensus T1).

These are floor detection gaps on real (not synthetic) diffs — the actionable follow-up here, and
it's deterministic work with no model in it. Tracked as a separate triage concern.

**Update — addressed on branch `floor-precision`.** The over-calls were false positives from content
signals firing on comment/test/doc lines (`dossier#80` matched a `/// Secret access key` doc-comment;
`dossier#77` a test fixture) and from a path rule matching a source filename (`firecracker.rs`); the
one true deterministic under-call (`dossier#82` concurrency) was a missing §5.2 signal. Fixed:
content signals now skip comments + test/doc files, the infra path rule no longer matches
source-name keywords, a comment-only change to a code file is T0, and a concurrency signal was added.
Corpus exact-match 8→11/24, over-calls 3→1 (the last is RUBRIC-correct T1 vs a T0 label). The
remaining under-calls are confirmed **semantic residuals** (invariant-relocation, trust-boundary,
public-API, on-disk-format) the floor structurally can't express — i.e. exactly the advisory's job,
which is why it stays cloud. Guarded by `labels/score-floor.mjs` + new `floor_test.go` cases.

## Recommendation
1. **Advisory tier stays cloud** — accept the NO-GO; the design already routes here. Close seam S1's
   local-advisory ambition.
2. **Skip the 14B measurement** (or treat as low priority). The failure is severity-grading and
   comprehension→decision mapping on dense diffs, not a marginal accuracy gap a 2× model closes to
   recall 1.0. The TDD's NO-GO protocol offers it; the result doesn't justify the ~9GB pull.
3. **Pivot to the floor gaps** — the higher-value, model-free work this eval surfaced. Tighten the
   deterministic signals (public-API, concurrency, persisted-shape, irreversible detection; docs/test
   over-calls) against these real diffs, which now exist as a vendored, reproducible corpus.
