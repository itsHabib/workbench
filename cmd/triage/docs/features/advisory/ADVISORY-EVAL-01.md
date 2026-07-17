# ADVISORY-EVAL-01 — the cloud advisory pass on the held-out residual (2026-07-08)

The floor's held-out gate (HELDOUT-01) left 15 semantic under-calls the advisory tier owes an
escalation for. This eval measures the **cloud** advisory (a host agent per PR, verified by
`internal/advisory`) against the §11 hard gates on those same 30 held-out PRs. One run,
qwen-free (the host agent IS the model), proposals in `labels/advisory-heldout-results.jsonl`,
scored by `labels/score-advisory-heldout.mjs`, raw output in `eval-heldout-run.txt`.

## Method

Each held-out PR → one advisory agent, given only the diff + the RUBRIC §6 triggers, asked to
propose an escalation (or `none`) with a verbatim evidence quote. Each proposal runs through
`triage-advisory`, which applies the deterministic verifier (real trigger + evidence ≥20 chars +
verbatim substring) and computes `final = max(floor, verified escalation)`. Scored on the
**addressable residual**: consensus ≥ T2 and floor < consensus (14 PRs — the advisory escalates
to T2/T3, so a T0/T1→T1 gap like ship#182 isn't advisory-addressable).

## Result vs the three hard gates

| Gate | Bar | Measured | |
|---|---|---|---|
| **Inflation** (final > consensus) | ≤ 30% | **1/30 = 3%** | **PASS** |
| **Per-trigger recall** | reported | see below | reported |
| **Verifier** (escalate⟹real trigger+verbatim) | structural | 2 proposals rejected, both correctly | **PASS** |

Overall recall on the addressable residual: **10/14 = 71%.** The floor alone caught **0/14** of
these — the advisory reclaims 10 of the 15 semantic under-calls HELDOUT-01 identified.

Per-trigger recall:

| trigger | recall |
|---|---|
| trust-boundary-widening | 2/2 |
| invariant-relocation | 2/2 |
| gate-machinery | 5/7 |
| plan-of-record | 1/2 |
| (agent proposed none) | 0/1 |

## The verifier is double-edged — by design

Two proposals were rejected for non-verbatim evidence, and the two cases show the verifier doing
exactly its job in both directions:

- **rooms#65** (consensus T1): the agent proposed T2 (`gate-machinery`) on a doctor-check that
  merely relaxes a missing-key check to a warning. The evidence didn't verify → rejected → floor
  T1 stood = consensus. **The verifier prevented an over-call.** This is the inflation gate
  working structurally, not statistically.
- **roxiq#132** (consensus T2): the agent proposed T2 (`gate-machinery`, correct) but quoted
  non-verbatim → rejected → floor T1 → a MISS. **The verifier cost a real catch** because the
  agent quoted badly. In the live `/pr-risk` skill a rejection triggers a host retry with a
  proper quote; in this batch eval there's no retry, so it scores as a miss. Verifier > trust:
  an unquotable escalation is not admitted even when the tier was right.

## The four misses, categorized

- **gate#3, gate#5** (consensus T3, advisory T2): the agent named the right trigger and escalated
  to owner, but under-tiered T3→T2 (missed the adversarial-pass bar). The T2-vs-T3 severity call
  on merge-gate machinery is the hardest judgment in the corpus — both blind labelers split ±1 on
  several gate PRs too. Owner review still fires; the adversarial pass doesn't.
- **ship#176** (consensus T2): the agent judged the gateway auth-token carrier "symmetric to
  existing API-key handling, no boundary widened" and said none. A lenient miss — labelers wanted
  owner review for forwarding a credential into a subprocess env. Calibration, not mechanism.
- **roxiq#132**: the verifier rejection above.

Every miss lands at **peer (T1) or owner (T2)** — none drops a consensus-T2/T3 PR to auto-merge.
The advisory never inflated in the dangerous direction and never lowered a floor.

## Verdict

**The mechanism passes: inflation 3% (structurally protected by the verifier), 10/14 residual
reclaimed, zero dangerous-direction errors.** It does **not** hit recall 1.0 — the residual is
the same T2-vs-T3 **severity-grading** wobble the project has documented since Experiment 01
(the model comprehends the risk and escalates to owner, but the owner-vs-owner+adversarial line
is genuinely judgment). That is the intended posture: the advisory is **recommend-only and
escalate-only** — it routes attention, a human owns the final severity call. Floor-alone would
have sent all 14 of these to peer review or auto-clear; the advisory routes 10 to owner and
under-tiers 4 to owner-or-peer. Net: review effort moves toward the risky minority, which is the
whole thesis.

**Follow-ups** (not blockers): the live-skill retry-on-rejection loop (roxiq#132 would flip to
caught); a T3-severity sharpener for gate machinery (the two gate misses share a signature —
"decides what merges + a bug fails open" is arguably a deterministic `gate-machinery`→T3 floor
signal candidate, if the pattern stabilizes across more live runs).
