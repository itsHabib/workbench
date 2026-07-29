# Trusted Gate judgment bridge

Status: authorized design; repository implementation dormant pending review and
operator bootstrap.

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
  receipt. The receipt records immutable actor login/ID, approval state/time,
  exact comment, run ID, and semantic digest.
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
   binds the run to this repository, `workflow_dispatch`, and the trusted
   executor workflow path; takes both initial and re-run actor identities from
   their immutable API IDs; and requires the newest decision for
   `gate-authorization` to be `approved` by a different actor, with the exact
   canonical comment.
4. Only then does the protected job mint a short-lived HMAC grant bound to the
   authorization ID, PR number, and full head SHA. The agent never invokes this
   path.
5. Gate re-reads exact repo/PR/head/base/merge-base facts, re-audits anchored
   state, requires the action to remain the newest terminal for the PR, and
   appends one permanent execution claim under the same state lock. Any existing
   claim for the action or authorization refuses. An open claim also blocks
   later Gate outcome writes for that PR.
6. GitHub Actions commits the claim and keyed anchor record to `gate-state` and
   uses a normal non-force push. The expected remote tip must match before the
   push and the published tip must match after it.
7. A fresh `gate-state` checkout re-audits the keyed chain and live PR facts.
   Only a durable, unconsumed, unexpired, still-newest claim proceeds.
8. A local Docker action starts Gate as its entrypoint. The App private key is
   an action input; Gate internally creates the App JWT, requests a
   contents-write installation token, and passes that token only to the direct
   `gh` child. The workflow never receives the installation token.
9. Gate executes the stored ten-element argv unchanged. It requires
   `gh pr merge`, squash, delete-branch, and the exact
   `--match-head-commit`; `--admin` is structurally rejected.
10. Gate appends one secret-free terminal result. Failure consumes the claim
    permanently; recovery requires a fresh Gate action and approval.

The workflow builds and runs only default-branch source. It never checks out PR
or fork code and has no status-write permission.

## State and branch identities

`GATE_ANCHOR_RECORD` allows the keyed anchor record to live beside the hosted
state tree while both signing keys remain outside it. A fresh runner must have:

- `gate-state/state/log.jsonl`
- `gate-state/anchor.json`
- protected `grant.key` and `anchor.key` restored outside that tree

The workflow's built-in token is the state writer. It has no status permission
or branch bypass. Its coarse `contents:write` permission is constrained by the
staged rules so it can update `gate-state`, not `main`.

The staged, non-effectful policy is
`docs/features/trusted-gate-judgment-bridge/ruleset-plan-v1.json`. Its validator
pins four layers:

- `main-updates`: Gate App is the sole Integration with PR-only bypass;
- `gate-state-updates`: `github-actions[bot]` is the sole non-force writer;
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
- missing, stale, rejected, self-authored, wrong-run, wrong-environment, or
  wrong-comment approval;
- expired/not-yet-valid request or malformed exact-subject grant;
- closed/moved/shared-head PR, non-default target, base retarget, base movement,
  or merge-base mismatch;
- action absent, hash/run/argv mismatch, older action superseded by any newer
  action/park, duplicate claim, open claim, or durable-state audit failure; and
- state publish/refetch mismatch.

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
