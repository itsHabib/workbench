# Trusted Gate judgment bridge

Status: authorized design; repository implementation dormant pending review and
operator bootstrap.

**Security hold:** the repository implementation is intentionally non-armable.
Fresh review proved that granting the coarse `github_actions` Integration
write access to `gate-state` lets any repository workflow replay an older valid
state-and-anchor pair. A protected workflow cannot make that shared identity
exclusive. The ruleset plan therefore leaves `gate-state` writerless and the
executor job has a hard false guard until the operator authorizes a revised
custody/order model.

The operator authorized this trust model at design head
`745d2bc405e07fd202c2379320afdc1745e46cc5`. That authorization covers repository
code, tests, and documentation only. App registration, secrets, environment
configuration, ruleset changes, grant minting, state bootstrap, live execution,
and merge remain operator-only.

## Decision

Gate uses a PR-specific custodied executor, not commit-status promotion.
Provider-neutral judgment may authorize exactly one Gate action for exactly one
pull request. It does not create reusable green status.

The trust root is the conjunction of:

1. one run-specific approval from a protected `gate-authorization` GitHub
   Environment, made by an immutable actor ID different from the workflow
   dispatcher/re-run actor; and
2. one repository-only Gate GitHub App whose private key is released only to
   the custodied executor action after a permanent claim is durable.

Neither side is sufficient alone. An unsigned request, login allowlist, ambient
operator token, inherited commit status, App token without a claim, or claim
without a protected approval authorizes nothing.

## Versioned artifacts

Shared vocabulary lives in `contracts/gateauthorization`:

- `GateAuthorizationRequestV1` — exact repository, PR, head, protected base
  ref, evaluated base SHA, merge-base SHA, Gate run/action ID and hash,
  `would_merge`, exact argv, canonical evidence digest, judgment question,
  issue/expiry bounds, replay ID, and trust-root name.
- `GateAuthorizationV1` — the request plus the verified workflow-run approval
  receipt. The receipt records immutable actor login/ID, approval state,
  protected-job observation time, exact comment, run ID, and semantic digest.
  GitHub does not expose an approval submission timestamp.
- `GateExecutionClaimV1` — the complete authorization plus action, exact argv,
  exact-subject grant ID, approval digest, claim/expiry times, and stable
  one-time execution identity.
- `GateExecutionResultV1` — one terminal `merged` or `failed` record with no
  credential or command output.

Schemas:

- `contracts/gateauthorization/schema/gate-authorization-request-v1.json`
- `contracts/gateauthorization/schema/gate-authorization-v1.json`
- `contracts/gateauthorization/schema/gate-execution-claim-v1.json`
- `contracts/gateauthorization/schema/gate-execution-result-v1.json`

Authorization, claim, and execution IDs are hashes of canonical semantic
content. Retrying a delivery cannot mint a new identity.

`contracts.Verdict` and `ReviewPanelV1` remain the clean/pass, escalation, and
block evidence. `ReviewFindingsV1` remains address-work-only; a clean review
never fabricates an empty findings artifact.

## Flow

1. `gate executor request` audits an existing newest `would_merge` action,
   byte-checks its stored argv, re-reads the live PR, and emits the request plus
   exact approval comment.
2. The operator dispatches `.github/workflows/gate-executor.yml` from the
   default branch with those two values. The static protected environment holds
   the job.
3. After approval, Gate reads the workflow-run and approval-history APIs. It
   binds the first run attempt to this repository, `workflow_dispatch`, and the
   trusted executor workflow path; takes both initial and triggering actor
   identities from their immutable API IDs; and requires exactly one
   unambiguous decision for `gate-authorization` to be `approved` by a
   different actor, with the exact canonical comment. Re-runs refuse because
   GitHub's approval history is not attempt-bound.
4. Only then does the protected job mint a short-lived HMAC grant bound to the
   authorization ID, PR number, and full head SHA. The agent never invokes this
   path.
5. Gate re-reads exact repo/PR/head/base/merge-base facts, re-audits anchored
   state, requires the action to remain the newest terminal for the PR, and
   appends one permanent execution claim under the same state lock. Any existing
   claim for the action or authorization refuses. An open claim also blocks
   later Gate outcome writes for that PR.
Steps 6–10 of the authorized design are not approved for activation. The held
prototype used generic GitHub Actions to publish the claim before the App
credential existed, then re-fetched the state, let Gate execute the stored
ten-element argv, and published a terminal result. Review disproved the first
of those transport assumptions: the coarse Actions identity is not an
exclusive writer. The workflow and plan preserve the prototype only as
reviewable source and are structurally non-armable.

Any replacement must continue to build only default-branch source, never check
out PR/fork code, never gain status-write permission, keep the App token inside
Gate, execute only the exact stored argv, and publish a reconstructable
terminal result.

## State and branch identities

`GATE_ANCHOR_RECORD` allows the keyed anchor record to live beside the hosted
state tree while both signing keys remain outside it. A fresh runner must have:

- `gate-state/state/log.jsonl`
- `gate-state/anchor.json`
- protected `grant.key` and `anchor.key` restored outside that tree

No state writer is selected. The workflow's built-in token is read-only, and
the plan deliberately grants no identity write access to `gate-state`.

The staged, non-effectful policy is
`docs/features/trusted-gate-judgment-bridge/ruleset-plan-v1.json`. Its validator
pins four layers:

- `main-updates`: Gate App is the sole Integration with PR-only bypass;
- `gate-state-updates`: writerless while the security decision is blocked;
- `other-branches`: repository roles and approved existing integrations, never
  the Gate App; and
- `main-required-gate`: retain the app-pinned `gate` check, with only the same
  Gate App receiving PR-only bypass.

Consequences:

- a second PR sharing the head cannot inherit execution authority;
- ambient users and Actions cannot update `main`, even if a status is green;
- the Gate App cannot merge a retargeted PR to a non-`main` branch; and
- the Gate App can close the existing red-check deadlock only for its exact
  claimed PR.

## Refusal semantics

All of these refuse before App token creation:

- malformed/unknown major, noncanonical repo, bad IDs/SHA/times, unsupported
  outcome, changed method/flags/order, or `--admin`;
- missing, stale, rejected, self-authored, wrong-run, re-run, ambiguous,
  wrong-environment, or wrong-comment approval;
- expired/not-yet-valid request or malformed exact-subject grant;
- closed/moved/shared-head PR, non-default target, base retarget, base movement,
  or merge-base mismatch;
- a merge target outside `main`; the staged `~ALL` other-branch rule restricts
  updates to explicit normal writers and gives the Gate App no bypass;
- action absent, hash/run/argv mismatch, older action superseded by any newer
  action/park, duplicate claim, open claim, or durable-state audit failure; and
- state publish/refetch mismatch.

## Open security decision: state writer custody

The authorized design put the claim on `gate-state` before creating the Gate
App installation token, using `github-actions[bot]` as the state writer.
GitHub rulesets identify that actor only as the repository-wide
`github_actions` Integration. A PR workflow with `contents: write` can
therefore append a commit that restores an older, internally valid
`state/log.jsonl` plus `anchor.json`. Non-force history remains, but the
current Gate audit does not treat branch ancestry as a monotonic external pin.

The smallest one-App correction changes the ordering: after exact protected
approval and all pre-credential refusals pass, Gate creates a narrowly scoped
installation token inside its process; that process CAS-publishes the claim,
refetches and verifies the durable claim, executes only the stored exact merge
argv, and CAS-publishes the terminal result. Rulesets allow the Gate App on
`main` and `gate-state`, deny it everywhere else, and give generic Actions no
write authority. A crash-safe, no-merge-token reconciliation path must close
an orphaned claim or confirm an observed merge.

That ordering differs materially from the authorized claim-before-token model.
It is a security decision, not an implementation detail, so this PR stops
before selecting or arming it.

Token exchange, command, confirmation, or terminal-state write failure never
creates reusable success. Once a claim lands it is never retried.

## Dormant bootstrap boundary

Repository presence is intentionally insufficient to run this path. The
workflow references App/environment/key values that do not exist until the
operator follows `operator-runbook.md`. The implementation does not register an
App, create or populate secrets, configure the environment, apply rulesets,
initialize `gate-state`, mint a grant, bootstrap itself, or merge.

The required live canary begins with `gate` red and must prove:

- exact stored argv succeeds as the Gate App without `--admin`;
- ambient-user and Actions updates to `main` refuse;
- Gate App update to a non-`main` branch refuses;
- shared-head, retarget, stale, malformed, duplicate, and replay cases create no
  App token or merge; and
- claim/result, GitHub actor, argv, PR head/base, and merge commit agree.

Until that proof passes, this is reviewed dormant code, not an armed security
boundary.
