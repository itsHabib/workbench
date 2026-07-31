# Opt-in tier-aware review canary — delivery plan

**Status:** Implementation locally green; PR review and live canary pending
**Date:** 2026-07-30
**Primary owner:** Workbench `cmd/review`
**Repositories:** `itsHabib/workbench`, `itsHabib/ship`, `itsHabib/cc-skills`
**Rollout:** Explicit personal-repository canary only

> **Review focus:** the deterministic continuation rule, disposition/defer
> authority, engine parity, and whether the first canary's shadow
> `continuation_weight` captures enough evidence to later become an enforced
> budget.

## 1. Outcome

Build and prove an opt-in, engine-neutral review strategy that:

- classifies every pull request at its exact live head;
- selects proportionate initial reviewers from a validated, swappable policy;
- continues only when deterministic evidence says another cycle is useful;
- targets later cycles at the reviewers whose findings remain in play;
- permits explicit deferment of noncritical findings;
- binds every plan, request, finding, disposition, proof, and decision to the
  same head SHA;
- gives Ship and session executions the same review policy;
- records enough evidence to tune the policy without enforcing an uncalibrated
  cost formula.

The delivery is working code, tests, focused PRs, exact-head review, a live
personal-repository canary, telemetry, and Gate-authorized merges. This document
is the decision artifact from which that work will be split.

## 2. Non-goals

- Moving reviewer policy into Ship.
- Treating a cycle cap as a required number of cycles.
- Re-running the full initial panel after every change.
- Allowing a local model to suppress critical findings.
- Renaming `.ship.json` to `.workbench.json` in this initiative.
- Enabling reduced review globally or for employer repositories.
- Using estimated dollars or tokens as a security decision.
- Redesigning Gate, Ship, or the session ledger.

## 3. Current seams and required compatibility repair

Workbench already owns the reusable review vocabulary and evidence producers:

- `contracts/reviewpanel` defines exact-head `ReviewPanelV1`.
- `contracts/reviewfindings` defines exact-head `ReviewFindingsV1`.
- `cmd/reviewfindings` produces findings from exact-head GitHub review evidence.
- `local.Ask` provides structured local-model advice with verifier and
  confidence escalation.
- Gate already consumes panel/review evidence and excludes stale comments.

Ship already owns Ship-engine execution and terminal spend telemetry. The
session ledger already owns session-engine execution state.

One narrow Ship compatibility defect must be repaired: current Workbench
`triage-floor` emits JSON and requires `-repo owner/name` for repository path
overrides, while Ship's adapter expects a bare `T0`–`T3` line and omits
`-repo`. The repair is limited to strict JSON decoding, repository propagation,
and classifier evidence capture. It does not add review policy to Ship.

## 4. Architecture and ownership

Reviewer selection and continuation are verification policy, so they belong to
a new engine-neutral Workbench tenant: `cmd/review`.

```text
repo + PR + live head
          |
          v
  triage-floor artifact
          |
          v
  cmd/review plan  <--- checked-in policy + explicit repo opt-in
          |
          v
  cmd/review request ----> exact-head request receipt
          |
          v
 ReviewPanelV1 + ReviewFindingsV1
          |
          v
  cmd/review decide <---- tests/proofs + dispositions + local advisory
       |        |
       |        +---- continue: targeted reviewer set
       |
       +------------- stop / escalate / park
          |
          v
 accepted changes only
       /       \
 Ship adapter  session-ledger adapter
```

Concern ownership:

| Concern | Owner |
|---|---|
| Deterministic risk floor and path overrides | Workbench `cmd/triage` |
| Tier policy, reviewer selection, cycle decisions | Workbench `cmd/review` |
| Review evidence vocabulary | Workbench `contracts/*` |
| GitHub reviewer triggering and exact-head request receipt | Workbench `cmd/review request` |
| Ship-engine accepted-finding execution | Ship `driver address` |
| Session-engine accepted-finding execution | Workbench session-ledger adapter |
| Thin orchestration | Canonical `cc-skills` work-driver skill |
| Merge authorization | Gate |

`cmd/review request` is the single reviewer-triggering write boundary. It
re-checks the live head immediately before requesting reviewers and emits a
head-bound receipt. Work-driver invokes it but does not reinterpret the policy.

Workbench tools continue to share contracts, not decision call stacks.
`cmd/review` consumes triage output as an artifact rather than importing
triage's internal policy.

## 5. Content-addressed policy and opt-in

The tier mapping is validated, swappable configuration consumed by
`cmd/review`, not prose or scattered conditionals. Its schema is versioned and
each plan records the digest of the exact validated policy content it used.
The checked-in canary policy is the command's default policy definition, but
reduced routing has no implicit enablement: both repository opt-ins must agree.

For the first canary:

- Workbench ships one named canary policy definition and its schema.
- The integration selects that checked-in policy; `cmd/review` records its ID
  and content digest automatically. Callers do not select or increment a
  separate policy revision.
- The target repository must explicitly opt in.
- Existing `.ship.json` may carry only the temporary enablement/policy
  selection needed by the current work-driver integration; it does not contain
  reviewer-routing logic.
- The desired `.workbench.json` rename/migration is recorded as follow-up work,
  not smuggled into this canary.
- Missing, disabled, unknown, or invalid configuration chooses the existing
  full safe panel.

No employer/work repository configuration is changed. Tests enumerate the
canary allowlist and prove non-enabled repositories retain their current full
panel.

## 6. Initial tier policy

Caps are ceilings, never quotas.

| Tier | Initial review | Hard cap | Additional requirement |
|---|---|---:|---|
| T0 | One local adversarial pass; no cloud bots | 1 | Deterministic proof after any accepted change; uncertainty raises tier |
| T1 | Codex | 3 | Targeted continuation only |
| T2 | Configured panel excluding Claude | 3 | Coordinator/findings artifact required |
| T3 | Full configured four-bot panel | 8 | Coordinator and adversarial verification required |

The actual configured roster is inspected before implementation. Policy names
reviewer identities; code does not assume that shorthand labels equal the live
roster.

## 7. Deterministic continuation and stop rule

Each cycle produces a complete, exact-head disposition ledger. Every finding is
one of:

- `fixed` — accepted and changed;
- `proved_safe` — rejected with deterministic evidence;
- `deferred` — intentionally not addressed under the rules below;
- `unresolved` — still requires action or judgment.

### Continue

Start another cycle only when at least one condition holds:

1. a critical finding remains unresolved;
2. an accepted finding caused a code/config change and lacks required closure;
3. the proof intended to close a finding failed or is missing;
4. the new head classifies at a higher tier;
5. for T3 cycles 4–8 only, an explicit continuation rationale names the
   unresolved finding or failed proof that justifies the extra cycle.

The next reviewer set is the union of reviewers whose findings triggered those
conditions. Do not re-request reviewers whose findings are closed and who have
no new relevant evidence to inspect.

### Proof substitution

For noncritical T0–T2 findings, a focused fail-before/pass-after deterministic
test or equivalent reproducible proof may close the finding without asking the
same bot to review again. The proof reference is stored in the disposition
ledger.

Proof substitution is not allowed when:

- the finding is critical;
- the proof is nondeterministic, missing, or failing;
- the change raises the tier;
- the policy explicitly requires reviewer closure.

T3 critical findings require closure from the finding's reviewer plus the
configured adversarial verification.

### Stop

Stop before the cap when all of the following are true:

1. deterministic checks pass on the live head;
2. no critical finding is unresolved or deferred;
3. every noncritical finding has a valid terminal disposition;
4. every accepted change has reviewer closure or an allowed proof;
5. exact-head panel/coordinator requirements are satisfied;
6. the current tier is not higher than the tier used to produce the evidence.

Reaching a cap does not turn failure into success. If stop conditions are not
met at the cap, the run parks/escalates for operator judgment.

### T0's one-cycle edge

T0 has exactly one local adversarial review opportunity. An accepted local
finding may be fixed and closed by deterministic proof on the new head. If the
proof cannot close it, uncertainty remains, or the change raises risk, the PR
is reclassified and enters the applicable higher-tier route rather than
silently consuming a second T0 cycle.

## 8. Exponential continuation weight

Each attempted review cycle records:

```text
continuation_weight = 2^(cycle - 1)
```

The per-cycle weights are `1, 2, 4, 8, ...`; cumulative weights are
`1, 3, 7, 15, ...`.

The first canary records this in shadow telemetry only. It is not yet an
authorization threshold because no calibrated boundary exists. The
deterministic stop rule and hard tier caps are enforced now; canary evidence
will show whether cumulative weight can later replace or complement raw cycle
count.

For cycles 4–8, the weight is recorded alongside the mandatory T3 continuation
rationale. This makes late cycles visibly expensive without requesting the full
panel merely to satisfy a numeric budget.

## 9. Deferment

Frontier agents may defer noncritical findings. A deferment must record:

- finding ID, source reviewer, severity, and exact head;
- concise rationale;
- whether it is genuine debt or intentionally out of scope;
- an issue/task reference when it is genuine debt;
- any evidence supporting safety of deferral.

A deferred finding does not automatically trigger another review cycle.

The following cannot be deferred:

- critical findings;
- failing deterministic tests or proofs;
- missing required exact-head evidence;
- policy/configuration validation failures;
- a risk-tier increase;
- uncertainty that would otherwise cause a fail-closed route.

The local model may advise `fix`, `prove`, `defer`, or `rereview` using the
finding and evidence verbatim. Its answer is advisory. Low confidence,
verifier failure, ambiguity, or a critical finding escalates to the frontier
agent. The local model never independently suppresses or downgrades a critical
finding.

## 10. Engine parity

Ship and session engines receive the same `ReviewPlanV1` and
`ReviewDecisionV1`. Engine type is not a reason to choose a different panel,
cap, stop rule, or disposition.

They differ only after a finding has been accepted:

- Ship applies work through the existing `driver address` boundary.
- Session applies work through a Workbench ledger/subagent adapter.

The session adapter now proves the same exact-head decision, finding-set,
cycle, and live-head contract in integration tests. Both engines therefore
consume the same route. An absent or unproven adapter parks or falls back to the
full safe panel; engine identity never weakens T2/T3.

## 11. Engine-neutral artifacts

All artifacts are schema-versioned and exact-head bound.

### `ReviewPolicyV1`

- policy ID; its schema version is part of the content covered by the digest;
- allowed tiers and initial reviewer sets;
- hard cycle caps;
- coordinator/adversarial requirements;
- proof-substitution rules;
- deferment rules;
- supported request providers;
- explicit opt-in constraints.

### `ReviewPlanV1`

- repository, PR, and exact head;
- classifier artifact digest, tier, and reasons;
- policy ID and validated content digest;
- initial reviewers and requirements;
- hard cap;
- route disposition and fallback reason.

### `ReviewRequestV1`

- plan digest and exact head;
- requested reviewers;
- provider request IDs/timestamps;
- before/after live-head checks;
- request outcome.

### Existing evidence

- `ReviewPanelV1` for declared/completed panel evidence.
- `ReviewFindingsV1` for selected exact-head findings.

### `ReviewDecisionV1`

- policy ID/digest, route disposition/reason, and classified tier/reasons;
- cycle number and continuation weight;
- exact head, plan ID, and cycle-input content digest;
- every finding's disposition and proof/follow-up references;
- `stop`, `continue`, `escalate`, or `park`;
- targeted next-reviewer set;
- deterministic reason codes;
- local advisory result/confidence when used.

Artifacts for head A cannot authorize, close, or request review for head B.
Every push invalidates the plan, request receipt, panel, findings, proofs, and
decision for routing purposes.

## 12. Fail-closed behavior

Use the existing full safe panel, or park when it cannot be produced, if:

- the classifier is missing, fails, or emits malformed/incomplete output;
- the tier is unknown;
- the policy/configuration is missing, invalid, or incomplete;
- the repository is not explicitly opted in;
- the live head changes during planning, requesting, observing, or deciding;
- an artifact digest, repository, PR, or head does not join exactly;
- required reviewer, coordinator, adversarial, test, or proof evidence is
  missing;
- the request provider or execution adapter is not proven;
- an old artifact is replayed against a new head.

Terminal dispositions distinguish:

- `tier_routed`;
- `deliberately_overridden`;
- `full_panel_fallback`;
- `parked_unverified`.

Infrastructure uncertainty never reduces review.

## 13. Repository and PR split

### PR 1 — Workbench contracts and `cmd/review`

Depends on: nothing.

- add the new policy/plan/request/decision schemas and strict validators;
- implement `review plan`, `request`, `observe`, and `decide`;
- reuse `ReviewPanelV1`, `ReviewFindingsV1`, and `local.Ask`;
- implement exact-head joins, targeted continuation, proof substitution,
  deferment validation, caps, and shadow continuation weight;
- add provider interfaces with a GitHub implementation;
- add both engine-neutral decision tests and session-adapter parity tests.

### PR 2 — Ship compatibility and execution adapter

Depends on: PR 1 contracts.

- repair `triage-floor` JSON decoding and `-repo owner/name` propagation;
- remove/reject any Ship-owned review router from the abandoned partial work;
- consume Workbench review artifacts without re-deciding policy;
- apply accepted findings through `driver address`;
- extend terminal telemetry to join Workbench review evidence.

### PR 3 — canonical `cc-skills` work-driver orchestration

Depends on: PRs 1 and 2.

- update `cc-skills/skills/work-driver/SKILL.md`;
- invoke `cmd/review` for both Ship and session engines;
- select only the execution adapter after Workbench decides;
- invalidate and regenerate review evidence after every push;
- preserve the existing full-panel path without explicit opt-in;
- update `catalog.yaml` only as required and verify Claude/Codex projections.

### PR 4 — canary evidence/status

Depends on: PRs 1–3.

- record all four live canary cases and raw exact-head evidence;
- update the previously parked strategy status;
- record rollback and expansion criteria;
- add the `.workbench.json` migration as separate follow-up work.

Keep PR 4 separate only if the evidence does not fit cleanly in PR 1 or PR 3.

## 14. Test matrix

### Policy and classification

- Each T0–T3 tier selects its configured initial strategy and cap.
- Repository path overrides raise classification deterministically.
- Malformed, unknown, failed, or missing classification falls back to the full
  panel.
- Invalid/incomplete policy cannot reduce review.
- A repository without explicit opt-in keeps the current full panel.
- Employer/work repository configuration and behavior remain unchanged.

### Cycle decisions

- A clean first cycle stops below the cap.
- Only reviewers with findings still in play are targeted on a later cycle.
- An accepted noncritical T0–T2 finding closes with valid
  fail-before/pass-after proof.
- Missing/failing proof continues or parks; it never closes the finding.
- A critical finding cannot be deferred or proof-substituted.
- A valid noncritical deferment stops without another cycle.
- An invalid deferment continues or parks.
- T3 cycle 4+ requires a qualifying reason and records exponential weight.
- Any tier stops early when the stop predicate is satisfied.
- Any tier parks at its cap when the stop predicate is not satisfied.
- A T0 uncertainty reclassifies upward instead of consuming another T0 cycle.

### Exact-head and replay

- A head change at each boundary invalidates the route.
- A push invalidates prior classification, requests, findings, decisions, and
  proof references.
- Evidence for head A cannot be replayed against head B.
- Targeted rereview still produces a complete panel/decision join for the new
  head.

### Engine parity

- Ship and session inputs yield byte-equivalent policy decisions.
- Only the post-decision execution adapter differs.
- An unproven session adapter receives the full safe fallback.
- Missing adapter evidence cannot accidentally reduce T2/T3.

### Telemetry

- repository, PR, head, tier/reasons, policy ID/digest;
- initial/requested/completed and targeted reviewers;
- cycle number, per-cycle and cumulative continuation weight;
- findings and dispositions by severity/verdict;
- proof, deferment, follow-up, and local-advisory references;
- coordinator/adversarial outcomes;
- timing and available token/cost data;
- fallback, override, continuation, park, and terminal reasons.

## 15. Live canary

Use a dedicated personal repository/fixture. Do not enable the policy globally.

1. **Low risk:** a T0/T1 PR legitimately stops after its first required review.
2. **Higher tier:** a T2/T3 PR receives the broader initial panel, one accepted
   finding causes a change, and only that finding's reviewer is re-requested.
3. **Forced failure:** missing/invalid classifier or policy visibly selects the
   full panel and records the reason.
4. **Head change:** route/review head A, push head B, and prove all head-A
   evidence is rejected and recomputed.

If naturally produced during the cases, also preserve:

- one noncritical deferment with rationale/follow-up;
- one proof-substituted closure;
- one shadow-weight trace across at least two cycles.

Each report records the PR/head, classification, policy, reviewer plan,
requests/completions, findings/dispositions, proofs, decision, telemetry, and
freshness result.

## 16. Validation and delivery

Run each repository's documented complete checks. For Workbench:

```text
gofmt -l .
go vet ./...
golangci-lint run ./...
go test ./...
```

For each implementation PR:

1. verify the exact head;
2. request the configured panel through the new review boundary;
3. consolidate and disposition every finding;
4. after any push, discard old authorization evidence and rerun;
5. verify complete checks, CI, panel, coordinator, and decision artifacts all
   name the final head.

Gate's default grant ceiling is three cycles. A T3 run that legitimately needs
cycles 4–8 requires an operator-minted grant with a sufficient
`-max-cycles` value. The agent never mints grants.

When a PR is ready, request the precise grant command from the operator, run
Gate with the handed grant, and merge only with Gate's emitted
`--match-head-commit` command.

## 17. Rollout and rollback

Rollout order:

1. land contracts and Workbench review policy behind explicit opt-in;
2. land both execution adapters and parity proof;
3. update canonical work-driver orchestration;
4. enable one personal canary repository;
5. run and publish the four cases;
6. decide whether to expand, revise, or disable.

Rollback is removal of the explicit repository opt-in. The existing full safe
panel remains the default throughout, so rollback does not require reverting
contracts or deleting evidence.

Expansion requires:

- all four canary cases passing;
- no stale-head or replay escape;
- no policy divergence between Ship and session engines;
- useful targeted-cycle telemetry;
- operator review of deferment quality;
- an evidence-based decision on whether exponential weight should become an
  enforced continuation budget.

## 18. Locked decisions

- Review policy lives in Workbench, not Ship.
- Ship and session receive the same review decision.
- T0: one local pass, no cloud.
- T1: Codex, cap 3.
- T2: panel excluding Claude, cap 3.
- T3: full four-bot initial panel, cap 8.
- Later cycles target only reviewers whose findings remain in play.
- Deterministic proof may replace noncritical T0–T2 bot rereview.
- Critical findings and failing proof cannot be deferred.
- Noncritical deferment is allowed with recorded rationale and follow-up.
- Cycles 4–8 are T3-only and require a qualifying continuation reason.
- Exponential continuation weight is collected in shadow mode first.
- `.workbench.json` is desirable follow-up work, not part of this canary.
