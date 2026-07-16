# Experiment 01 — first empirical validation-gate run

**Date:** 2026-07-02
**Question:** does the `RUBRIC.md` classifier actually reproduce sound risk labels on *real* PRs, and does it ever *under*-classify danger?
**Verdict:** the approach works on the majority, but this rubric **fails the strict zero-false-negative gate** (16/17 high-risk recall, one T2→T1 miss) and has one clear over-fire bug. That is the gate doing its job — it named exactly what to fix before this could be trusted.

## Method

- **Corpus:** 24 real merged PRs pulled via `gh` from `itsHabib/{ship,dossier,rooms}`, deliberately spanning the risk spectrum — docs/badges, CI, a 3,048-line refactor, DB migrations, supply-chain (sha256 pinning, RUSTSEC dep swap, cargo-audit), sandbox/network isolation, CAS/concurrency, public-API/data-shape changes. Title + changed-file list + capped diff each.
- **Held-out oracle (independence per spec §11):** two labelers (A, B) given *only* a neutral "how much human review did this need?" prompt — **they never saw `RUBRIC.md`**. Their agreement measures how noisy ground truth itself is.
- **Classifier:** three independent runs of an agent applying `RUBRIC.md` literally (deterministic floor → escalate-only agent advisory). Three runs measure nondeterminism.
- Tiers: T0 auto / T1 peer / T2 owner / T3 critical. Aggregation was scripted, not eyeballed.

## Results

| Metric | Result | Reading |
|---|---|---|
| Inter-labeler exact agreement | **20/24 (83%)**, all 4 disagreements ±1 tier | "Ground truth" has ~17% one-tier boundary wobble. No classifier can be *measured* tighter than this. |
| Classifier determinism | **19/24 stable** across 3 runs | All 5 unstable PRs varied **only in the agent-escalation layer**; deterministic floors were identical every run. |
| High-risk PRs (both labelers ≥T2) | 17 | — |
| **Dangerous auto-merges (high-risk → T0)** | **0** | Nothing a labeler flagged risky was ever dropped to auto-merge. The fail-safe held at the critical boundary. |
| Below **both** labelers | **2** — dossier#67 (T2→T1), rooms#43 (T3→T2) | The gate failures. One crosses owner→peer (real), one stays in owner but loses the adversarial pass. |
| Strict recall on T2/T3 | **16/17 (94%)** | **Gate = FAIL** (target 1.0). |
| Over-fire (above both labelers) | 4/24 | One clear bug (dev-dep), rest ±1 boundary noise. |
| Clean auto-merge (both T0, clf ≤T0) | 2/24 | Low — but corpus was weighted to substantive PRs; see caveat. |

## What the failures teach

**1. The one real safety miss — dossier#67 (policy-core extraction), T2→T1, stable across all 3 runs.**
Both labelers wanted owner review because the refactor *relocates the task state-machine transition guards / domain invariants*, even though it's behavior-preserving. The rubric matched "internal behavior change → T1" and stopped. No deterministic signal expresses "this mechanical-looking move touches invariant-bearing code," and the agent didn't escalate it. → this is the **semantic residual the agent exists for, and it whiffed.** Fix is an explicit §6 escalation trigger ("refactor that moves policy/invariant/state-machine code → propose T2"), not a new path glob.

**2. rooms#43 (sha256 build-input pinning), T3→T2, stable.**
Both labelers rated supply-chain *integrity* work T3; the rubric caps lockfile/dep at T2 and reserves T3 for source/registry overrides. Genuine boundary disagreement (pinning is *hardening* — it strengthens, so T2-with-owner is defensible). Left as a rubric-vs-labeler calibration open, not silently "fixed."

**3. Over-fire bug — rooms#31 (proptest), T0→T2 (+2), stable.**
`§5.3 new dependency → T2` fired on a **dev/test** dependency. A tests-only PR should not become owner-review because it adds `proptest`. → split runtime deps (T2) from dev/test deps (T0/T1). **Fixed in this commit.**

**4. Nondeterminism lives entirely in the agent layer — architecturally load-bearing confirmation.**
The 5 unstable PRs (ship#174, ship#158, ship#163, ship#150, dossier#77) all had *identical deterministic floors* across runs; only the escalation flip-flopped (e.g. ship#174 escalated to T3 in runs 1&3 but stayed T2 in run 2). This is the empirical case for v2's core decision: **keep the safety guarantee on the reproducible floor; treat the agent as an advisory raiser, gated worst-case-across-k-runs, and graduate its stable patterns into deterministic signals.**

**5. The agent still earns its slot.**
Escalations correctly reclaimed ship#163 (floor T1 → labeler T2/T3), ship#174, rooms#42/44, ship#158 — real trust-boundary/gate cases the deterministic floor under-rated. Deleting the agent would *add* misses. But its win patterns are narrow and repeatable (sandbox-disable, CI-checks-out-PR-head, network/VM isolation, merge-gate) → promote those into deterministic §5.2 content signals so trust shifts off the nondeterministic layer over time.

## Caveats (don't over-read this)

- **n is tiny.** 17 high-risk positives; zero-miss would only bound the true FN rate to <~20% (rule of three). 16/17 with a known miss just says "not ready." The corpus gate is a screen, not a certificate (spec §11).
- **Corpus selection bias.** PRs were hand-picked to span risk, over-weighting substantive changes. The low 2/24 clean-auto-merge rate is *not* the real skip rate — a live PR stream has far more trivia. But there's a real signal too: **agentic-infra merged PRs skew high-risk** (they touch sandboxes, migrations, wire contracts constantly), so the auto-merge lever on *this* kind of codebase is smaller than the generic "70% auto-merge" hope.
- **The oracle is two agents, not humans.** Independent from the rubric (they never read it), but still LLM judgment. Real validation needs operator/human held-out labels + forward validation on unseen PRs.

## Immediate actions

- [x] **Rubric fix:** split dev/test deps from runtime deps in §5.3 (kills the rooms#31 over-fire).
- [x] **Rubric fix:** add an explicit §6 agent-escalation trigger for refactors that relocate policy/invariant/state-machine code (targets the dossier#67 miss).
- [ ] Promote stable escalation patterns (sandbox-disable, CI PR-head checkout, network/VM isolation) into deterministic §5.2 content signals — *next iteration.*
- [ ] Re-run the gate after fixes; add operator-labeled held-out rows + forward validation before trusting any auto-merge.

## Raw data

Per-PR table and the 5 agent outputs are in the scratchpad results dir; the labels feed the P0 corpus in `labels/`.
