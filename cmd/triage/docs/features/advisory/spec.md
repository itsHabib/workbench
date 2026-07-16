# advisory — the verified escalate-only advisory tier

**Status:** built (v1). Supersedes the local-advisory TDD (PR #1) — the eval NO-GO'd a
LOCAL backend at 7B (EVAL-01, recall 0/13), so the advisory backend is **cloud** (the host
agent). What that TDD designed as durable — the extract-shaped schema, the three-part
verifier, whole-diff redo, the §11 hard gates — carries forward here; what it designed as
architecture (Ollama, per-file split, max-merge) is dropped: a cloud host reads whole diffs.

**Owner:** @itsHabib · **Date:** 2026-07-08 · **Related:** `pr-risk-engine/spec.md` (the
engine), `RUBRIC.md` §6 (the triggers), `HELDOUT-01.md` (the residual this tier owes).

## 1. Problem

The deterministic floor is the reproducible safety layer; by construction it only expresses
signals that pattern-match. HELDOUT-01 measured its residual on 30 fresh PRs: **15 under-calls,
all semantic** — merge/gate machinery in other repos, trust boundaries without telltale
content, invariant relocation, plan-of-record docs, secrets plumbing below the keyword
threshold. None can become a deterministic signature without over-firing. That residual is the
advisory's job: an escalate-only pass that may only *raise* above the floor, and must *earn*
every raise.

## 2. The contract (unchanged from the floor's design)

```
final = max(floor, verified advisory escalation)
```

The advisory can never lower the floor and a rejected escalation contributes nothing. All
nondeterminism lives in the advisory; the verifier that admits it is deterministic.

## 3. Mechanism

`internal/advisory` + `cmd/triage-advisory`. The host agent (the `/pr-risk` skill) reads the
whole diff and RUBRIC §6, and emits a **Proposal**:

```json
{ "escalate": "none|T2|T3", "trigger": "<§6 trigger>", "evidence": "<verbatim diff quote>",
  "confidence": 0.0-1.0, "why": "<one line>" }
```

`triage-advisory` runs the floor, verifies the proposal, and prints the **Verdict** (floor,
escalate, verified/rejected, final, route). The proposal is inline or `@file`; stdin is the
diff.

### 3.1 The verifier (spec §4.2, carried forward)

An escalating proposal must pass ALL three or it is **rejected** (contributes nothing — the
floor stands):

1. `trigger` is a real §6 trigger, not the schema-legal `none`. (Fail-open caught in the PR #1
   review: enum membership alone let `escalate=T2, trigger=none` through.)
2. `evidence` is ≥ 20 chars — a single common word is a substring of almost any diff.
3. `evidence` is a whitespace-normalized substring of the diff the agent read — the model
   cannot escalate on content it didn't see. Checked against both the raw diff and a
   marker-stripped view (agents quote code, not `+`/`-` markup).

**Verifier > confidence.** `confidence` is recorded, never gated on — EVAL-01 and the S2 gate
both measured it carrying zero signal. A confabulated escalation at confidence 1.0 is exactly
what the substring check kills.

### 3.2 Whole-diff, not per-file

The local TDD split per file because a 7B drops items in dense input; a cloud host doesn't, so
the advisory reads the whole diff in one pass. This also removes the local design's only
structural gap (a cross-file risk invisible to every per-file call): the host always sees the
whole diff, so the advisory can subtract nothing from recall — it can only add escalations.

### 3.3 Triggers

The five §6 triggers, each a `Triggers` key the verifier enforces: `trust-boundary-widening`,
`production-default`, `invariant-relocation` (from Experiment 01) and — added from HELDOUT-01 —
`gate-machinery` (code deciding what merges/ships/passes in any repo; 8 of 15 held-out
under-calls) and `plan-of-record` (a design doc that sets policy). Growing the set is a RUBRIC
§6 edit, which is itself a T3 control-plane change.

## 4. Validation (the §11 hard gates, carried forward)

The advisory is scored on the HELDOUT-01 residual (the 15 semantic under-calls + the E01
residual), host-agent runs logged to `labels/mismatches.jsonl`. All three gates are hard:

1. **Per-trigger recall.** Not just overall — with ≤8 PRs per trigger, an overall pass can hide
   a trigger the agent misses entirely. `final` must reach consensus on each residual PR,
   reported per trigger.
2. **Inflation parity.** The advisory's over-escalation rate (`final > consensus`) must not
   exceed a cloud baseline; absolute backstop ≤ 30% of PRs raised above consensus. triage's
   whole thesis is to *reduce* review load — a T3-happy agent that quotes real lines can satisfy
   recall while routing the safe majority to owner review. This gate tensions against recall so
   the agent must be *calibrated*, not trigger-happy.
3. **Escalate ≠ none ⟹ real trigger + verbatim evidence** — enforced structurally by the
   verifier, not measured after the fact.

## 5. Non-goals

No local backend (EVAL-01). No cloud-API escalator function (the host agent IS the escalator;
a headless `cloud.Escalator` waits for a seam that runs without a host — auth-gated). No change
to the floor, the tiers, or the raise-only contract.
