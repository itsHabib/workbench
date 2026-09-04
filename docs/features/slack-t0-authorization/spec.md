# Slack T0 authorization

**Status:** implementation contract  
**Owner:** @itsHabib  
**Date:** 2026-08-27

## Problem

Gate can already be driven and resolved remotely, but starting a run still
requires an operator-minted grant token. That is awkward from a phone. The
existing Slack buttons resolve a run that already parked under a grant; they do
not create authority.

This slice adds a deliberately small authority surface: an operator may approve
one ten-minute, T0-only capability for `merge` on one repository, pull request,
and exact head SHA from a signed Slack interaction. Gate then evaluates that
subject normally and emits its commit-pinned merge command. This slice does not
execute the merge.

## Command and flow

```text
gate gate -repo OWNER/REPO -pr N -slack
  -> read current PR head
  -> append grant_request(repo, PR, exact head, T0, 3 cycles, 10 minutes)
  -> wait for one terminal response

flare (read only)
  -> render request as a Slack card
  -> Approve T0 / Deny buttons carry only the immutable request artifact id

Slack -> escalate serve
  -> verify Slack HMAC, freshness, and immutable-user allowlist
  -> ack quickly and pass the original signed body to gate grant-callback

gate grant-callback
  -> independently verify the same HMAC, freshness, and allowlist
  -> load all scope from the immutable request artifact
  -> re-read the PR head
  -> atomically append exactly one of:
       bound grant(parent=request), grant_denied(parent=request)

waiting gate process
  -> approved: evaluate with the exact-subject grant
  -> denied/expired/stale: refuse
```

## Authority contract

- Slack is an operator input surface, not a credential store or decision
  engine. A tap has no meaning until Gate verifies it.
- The request fixes `action=merge`, `max_tier=T0`, `max_cycles=3`, and a maximum
  validity of ten minutes. These are not caller-selectable Slack fields.
- The button carries only a request artifact id. Repo, PR, SHA, tier, cycles,
  expiry, and identity never come from button value or CLI flags.
- Gate derives `minted_by` from the verified Slack payload and records both the
  readable name and immutable Slack user id.
- Approval and denial are mutually exclusive and single-use under Gate's
  cross-process state lock. A retry, double tap, or approve/deny race cannot
  mint twice or change the winner.
- A moved head, expired request, bad signature, stale callback, unallowlisted
  user, malformed request, or replay refuses before minting.
- The resulting grant is already supported by Gate's bound capability shape:
  exact head, exact PR, and the request's semantic authorization id are covered
  by the grant HMAC.
- Gate remains the only writer of Gate state and the only minter. Flare remains
  read-only. Escalate shells Gate through its CLI seam and never imports Gate.
- T1 and above are out of scope and continue to require the existing stronger
  operator/protected-environment paths.
- A successful Gate result still stops at the existing exact
  `gh pr merge ... --match-head-commit ...` action. No Slack callback executes
  that command in this slice.

## Contract artifacts

`GrantRequestV1` carries:

- `schema_version`
- semantic `authorization_id`
- `subject {repo, number, head_sha}`
- fixed `action`, `max_tier`, and `max_cycles`
- `issued_at` and `expires_at`

`GrantDenialV1` carries the complete request plus `decision`, verified `who`,
`at`, and a bounded reason. The approved terminal is the existing signed grant
artifact, parented to the request.

## Failure and recovery

- The waiting CLI polls the append-only state. If it exits, the request remains
  auditable; it cannot be approved after expiry.
- If Slack delivery fails, the request expires harmlessly and can be retried as
  a fresh request for the then-current head.
- Escalate acknowledges before Gate finishes so Slack does not time out. It
  drains accepted callbacks during graceful shutdown, as it already does for
  park resolution.
- Slack card updates are best effort. Gate state, not the card, is the source of
  truth.

## Adversarial review

| Attack | Control | Residual |
|---|---|---|
| Forge Escalate's internal CLI call | Gate accepts only the original raw Slack body and independently checks its HMAC and timestamp | Compromise of Slack signing secret remains a trusted-boundary compromise |
| Edit button value to widen scope | Value is only an artifact id; Gate loads and validates all scope from state | A valid tap can approve only the immutable request it names |
| Replay or double tap | Terminal append is single-use across grant and denial kinds under the state lock | A Slack retry may show an already-resolved receipt |
| Approve after a force-push | Gate re-reads the head immediately before the terminal append; the later run also checks the bound subject | A push after mint but before action is caught by the bound grant/action check |
| Channel member taps Approve | Slack signature authenticates Slack; immutable user-id allowlist authorizes the operator; Gate repeats both checks | Allowlist maintenance is operator configuration |
| Agent requests broad authority | Request validator accepts exactly T0, merge, three cycles, ten minutes | Repeated T0 requests remain visible in the append-only log |
| Race Approve against Deny | Both conflict on the request parent across the same terminal family | First valid terminal fact wins |
| Escalate/Flare writes a grant | Neither receives the signing-key API; Escalate shells a Gate callback verb and Flare only reads | Host compromise is outside this local control plane |

## Acceptance

- Contract schema and semantic-id conformance tests pass.
- Gate tests cover approval, denial, expiry, bad HMAC, stale callback,
  unauthorized user, malformed request, moved head, replay, and concurrent
  approve/deny.
- Flare renders an exact-scope card on an existing `kind=escalation` phone route
  and closes it on grant/denial.
- Escalate preserves existing park resolution and routes grant callbacks without
  widening callback-controlled arguments.
- An isolated end-to-end test proves signed Slack callback -> bound grant ->
  waiting Gate continuation without touching live Gate state.
