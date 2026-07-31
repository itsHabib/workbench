# Tier-aware review canary report

**Run date:** 2026-07-30 PDT / 2026-07-31 UTC

**Policy:** `tier-aware-canary`

**Policy digest:** `sha256:914b78444c4d19a2c2d0c7019bacfddc873d272a33cc7d3257abe6dd63ba15ee`

**Enabled repository:** `itsHabib/ship`

## Status

The opt-in boundary is working and remains fail-closed. Three required live
cases are complete. The higher-tier case proved T2 selection and deterministic
target reduction, but cannot yet produce a terminal clean decision because the
required Codex and Cursor reviewers reported account usage limits. Missing
reviewer evidence remains `panel_incomplete`; it has not been waived or
relabelled as success.

| Case | PR and exact head | Result |
|---|---|---|
| Low risk | [`ship#245`](https://github.com/itsHabib/ship/pull/245) `6f72de30fd860e40f897c8a11afedeac0b1b3a6d` | `T0`, no cloud reviewers, one local adversarial pass, cycle 1 `stop` |
| Higher tier | [`ship#244`](https://github.com/itsHabib/ship/pull/244) `0dbdf43f3d41d66122524037b500b19e5ff9e499` | `T2`; initial `codex,copilot,cursor`, then only missing required `codex,cursor`; terminal completion pending reviewer availability |
| Forced failure | [`ship#245`](https://github.com/itsHabib/ship/pull/245) `49d31433615c82b258d0ec3ebe09509c6844b311` | missing classifier selected all four safe reviewers and recorded the executable-not-found reason |
| Head change | [`ship#245`](https://github.com/itsHabib/ship/pull/245) head A → `6f72de30fd860e40f897c8a11afedeac0b1b3a6d` | old request/observe/decide each refused with exit 3 and wrote no artifact; new T0 plan received a distinct plan ID and stopped cleanly |

Raw artifacts live under [`evidence/2026-07-30`](evidence/2026-07-30).
The `local-adversarial.json` files are raw canary captures rather than a
versioned `Review*V1` contract. Case 1 uses an `attempts` envelope to preserve
both the rejected and accepted local outputs; case 4 records its single
accepted output directly. They intentionally do not claim `schema_version`.
Their exact content digests, selected result/confidence, and exact-head
subjects are bound into the corresponding `ReviewCycleInputV1` and copied into
`ReviewDecisionV1`.

## Case 1 — legitimate reduced route

The fixture PR is stacked on Ship's implementation branch so the repository
opt-in is present while the fixture's own diff remains one inert documentation
file. `triage-floor -repo itsHabib/ship` classified it as T0.

- Plan: `tier_routed`, T0, `max_cycles: 1`, no reviewers,
  `local_adversarial: true`.
- Request receipt: `requested` with an empty request set; no cloud bot was
  invoked by the T0 route.
- Local adversarial pass: local Ollama result `pass`, no findings,
  `confidence: 1.0`. An earlier malformed `confidence: 100` result was rejected
  rather than counted.
- Decision: `stop_conditions_satisfied`, continuation weight 1, cumulative
  weight 1; the local artifact is bound by
  `sha256:85eaba77c10c19262b52bf1e9d4898927245fabffc96c9e59229840bbb9b89da`.

## Case 2 — broader route and deterministic target reduction

Ship #244 classified T2 from driver-machinery changes and the repository path
override. Its initial plan selected `codex,copilot,cursor`, excluding Claude,
with required completion from Codex and Cursor.

The exact-head observation found both required reviewers missing: Codex
reported review quota exhaustion and Cursor reported a usage limit. Copilot's
API calls produced no requested-reviewer response, fresh timeline event, or
exact-head review, so the preserved initial [`request.json`](evidence/2026-07-30/case-2-higher-tier/request.json)
truthfully records Copilot as failed.

The deterministic cycle decision is `continue` with
`panel_incomplete,coordinator_incomplete`. Its next-reviewer set is only
`codex,cursor`; the optional Copilot reviewer is not blindly re-requested. The
targeted request receipt contains exactly those two reviewers.

This case is not terminal evidence yet. A clean exact-head response from the
required reviewers, or a real accepted finding followed by author-only closure,
is still required before expansion.

## Case 3 — forced full-panel fallback

Planning the harmless fixture with
`-triage-bin definitely-missing-triage-floor` produced:

- disposition `full_panel_fallback`;
- reason `triage-floor failed: ... executable file not found`;
- reviewers and required set `codex,claude,cursor,copilot`;
- no reduced T0 route.

The fallback omits a policy reference because classifier execution failed
before policy loading or evaluation. That is deliberate: the hard-coded safe
panel is usable without trusting any policy bytes, and the executable-not-found
reason preserves why routing could not occur.

The request boundary invoked the mention reviewers and attempted Copilot. The
receipt is `failed`, not `requested`, because GitHub supplied no verifiable
Copilot request evidence.

## Case 4 — head invalidation and recomputation

The preserved [`head-A plan`](evidence/2026-07-30/case-4-head-change/head-a-plan.json)
had plan ID `rp_26f7dd4978d32a4d0ba53cadef37b10d`. After a second
documentation-only commit moved the PR to head B:

- request with head-A plan: exit 3, no artifact;
- observe with head-A plan: exit 3, no artifact;
- decide with head-A plan/input: exit 3, no artifact.

Replanning head B produced plan ID
`rp_cfd71b06c7476f83567f01be33e472a0`, again classified T0, and required a
fresh local adversarial pass before a new `stop` decision.

Case 1 later planned the same head independently and therefore has another
valid plan ID. Plan IDs identify the complete planning event—including its
generation time—not merely the repository/head pair. Every downstream
artifact must join the specific plan ID and exact head it consumed.

## Canary telemetry

The exact-head `ReviewPlanV1`, `ReviewRequestV1`, `ReviewPanelV1`, and
`ReviewDecisionV1` files are the live per-cycle routing telemetry. Together
they record repository/PR/head, tier and reasons, policy ID/digest, requested
and completed reviewers, cycle and continuation weight, findings, route
disposition, and terminal or continuation reason.

| Case | Telemetry record |
|---|---|
| Low risk | [`plan`](evidence/2026-07-30/case-1-low-risk/plan.json), [`request`](evidence/2026-07-30/case-1-low-risk/request.json), [`local adversarial capture`](evidence/2026-07-30/case-1-low-risk/local-adversarial.json), [`cycle input`](evidence/2026-07-30/case-1-low-risk/cycle-input.json), and [`decision`](evidence/2026-07-30/case-1-low-risk/decision.json) |
| Higher tier | [`plan`](evidence/2026-07-30/case-2-higher-tier/plan.json), initial [`request`](evidence/2026-07-30/case-2-higher-tier/request.json), [`panel`](evidence/2026-07-30/case-2-higher-tier/panel.json), [`cycle input`](evidence/2026-07-30/case-2-higher-tier/cycle-input.json), [`decision`](evidence/2026-07-30/case-2-higher-tier/decision.json), and [`targeted request`](evidence/2026-07-30/case-2-higher-tier/targeted-request.json) |
| Forced failure | fallback [`plan`](evidence/2026-07-30/case-3-forced-fallback/plan.json) and failed [`request`](evidence/2026-07-30/case-3-forced-fallback/request.json) |
| Head change | [`head-A plan`](evidence/2026-07-30/case-4-head-change/head-a-plan.json), head-B [`plan`](evidence/2026-07-30/case-4-head-change/head-b-plan.json), [`request`](evidence/2026-07-30/case-4-head-change/head-b-request.json), [`local adversarial capture`](evidence/2026-07-30/case-4-head-change/head-b-local-adversarial.json), [`cycle input`](evidence/2026-07-30/case-4-head-change/head-b-cycle-input.json), [`decision`](evidence/2026-07-30/case-4-head-change/head-b-decision.json), and [`stale probe`](evidence/2026-07-30/case-4-head-change/stale-probe.json) |

Ship does not emit a `review_decision` line to `review-spend.jsonl` until its
adapter consumes an `address` decision. These live cases stop, continue, or
fall back without authorizing address work, so the report does not fabricate a
Ship spend-log line. The adapter's emission path remains covered by Ship's
complete test suite.

## Implementation delivery status

| PR | Exact head | CI / review status |
|---|---|---|
| [`workbench#182`](https://github.com/itsHabib/workbench/pull/182) | `3f1e9f785e873748c44560172fbadd0a088abc1c` | merged; exact-head CI green |
| [`workbench#183`](https://github.com/itsHabib/workbench/pull/183) | `840de19f4f71ceb8ff7bbab49a757193b1fae5ac` | exact-head CI green; Gate parked on missing required reviewer evidence |
| [`ship#244`](https://github.com/itsHabib/ship/pull/244) | `0dbdf43f3d41d66122524037b500b19e5ff9e499` | `make check` and CI green; required T2 reviewers unavailable |
| [`cc-skills#17`](https://github.com/itsHabib/cc-skills/pull/17) | `0577160448525206b6ba4e99e69848b401057f32` | projection validation passed; configured reviewers unavailable |

Copilot request truth is consistent across the runs: an empty successful POST
response is not treated as proof. A populated reviewer response, fresh
`review_requested`/`copilot_work_started` timeline event, or exact-head Copilot
review is required.

## Rollback

Remove `review.tier_aware: true` from the canary repository. Workbench then
retains the contracts and evidence, while work-driver uses the pre-existing full
safe panel. No policy or artifact deletion is required.

## Expansion gate

Do not expand beyond `itsHabib/ship` until:

1. the T2 case reaches a terminal exact-head decision with its required panel;
2. all implementation PRs have final exact-head review evidence and green CI;
3. Ship and session adapters remain equivalent on the shared corpus;
4. no stale-head or cross-head replay succeeds;
5. targeted-cycle and deferment evidence is reviewed by the operator;
6. continuation weight remains shadow-only until the canary supplies enough
   observations to calibrate a threshold.

No employer repository is enabled or modified by this canary.
