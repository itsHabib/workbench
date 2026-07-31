# Approval UX Phase 0 — spec

Status: proposed
Date: 2026-07-31

Implements the Phase 0 recommendation of `approval-ux.md` (designs 1–3):
agent-performed dispatch, a legible run surface, word-coded approval
phrases, and a Slack decision card over the existing escalation seam. The
security contract is `design.md`; this spec changes **no verification
semantics** — only one canonical encoding and the surfaces around it. Every
refusal added here fires pre-credential, like all the others.

The operator-visible outcome: the phone buzzes with a readable card, the
operator taps through to GitHub's approval screen, types
`prepare 182 mango-harbor-violet-inlet`, and is done. No JSON, no hex, no
laptop.

## 1. Word-coded approval phrase

### 1.1 Canonical form

The canonical approval comment for preparation and execution requests
becomes:

```
<operation> <pr-number> <w1>-<w2>-<w3>-<w4>
```

- `<operation>` — literal `prepare` or `execute`.
- `<pr-number>` — the request's PR number, base-10, no padding.
- `<w1>..<w4>` — four words from the pinned wordlist (§1.2), joined by `-`.

Examples: `prepare 182 mango-harbor-violet-inlet`,
`execute 182 copper-lantern-mesa-drum`.

The `reconcile` operation keeps its existing claim-identity comment. It is
rare, deliberately boring, and names a `gxc_` identity the operator is
looking directly at; re-encoding it buys nothing.

### 1.2 Derivation

A pure function, colocated with the existing canonical-digest code under
`cmd/gate/internal/`:

```go
// ApprovalPhrase derives the canonical approval comment for a request.
// digest is the request's existing canonical semantic digest (the same
// bytes already bound into the artifact IDs); op is "prepare" or
// "execute"; pr is the request's PR number.
func ApprovalPhrase(digest [32]byte, op string, pr int) (string, error)
```

- Words: take the digest's first 44 bits, big-endian, as four 11-bit
  indices into a 2048-word English wordlist (the BIP-39 English list,
  vendored into the repo as a `go:embed` text file). The list is data, not
  a dependency — no BIP-39 library, no mnemonic semantics, no checksum
  word. We use it only as a well-reviewed set of 2048 short, distinct,
  phone-typeable words.
- Unknown `op` or non-positive `pr` returns an error; the function never
  panics.
- The digest input is the **existing** canonical semantic digest already
  computed for `GatePreparationRequestV1` / `GateAuthorizationRequestV1`.
  This spec adds no new hash and no new canonicalization.

### 1.3 Verification

Where gate today compares the approval comment byte-exactly against the
emitted `gpr_...`/request comment, it now:

1. normalizes the received comment — trim leading/trailing whitespace,
   collapse internal whitespace runs to one space, lowercase ASCII; and
2. requires exact equality with `ApprovalPhrase(...)` computed from the
   verified request document.

Everything around the comparison is untouched: same protected environment,
same different-actor and immutable-ID checks, same first-attempt rule, same
expiry/replay refusals, same position in the pre-credential sequence. The
normalization exists so a phone keyboard's stray trailing space or
auto-capitalized first letter does not refuse a correct approval;
word-order, word-identity, operation, and PR number remain exact.

There is **no dual acceptance window**: emitter and verifier are the same
binary, requests live at most 20 minutes, and hosted state stores request
documents, not comments. One cutover commit changes both sides.

### 1.4 Binding strength, stated honestly

44 bits is not a cryptographic commitment and does not need to be. The
phrase is a proof of attention inside a window that is already run-specific,
replay-identified, expiry-bounded, and approver-restricted (design.md §Flow
3). The threat it addresses is "approver approves request A believing it is
B"; the `<operation> <pr-number>` prefix makes the typing act assert both,
and 44 bits makes an engineered collision between concurrently-live
requests for the *same PR and operation* infeasible at this system's scale.
The full 256-bit digest still rides inside the request JSON, the
authorization, and the claim, unchanged.

## 2. Agent-performed dispatch: `gate executor submit`

A new verb so no human ever pastes JSON into the `workflow_dispatch` form:

```
gate executor submit -request <emitted-request.json> [-workflow gate-executor.yml]
```

- Validates the document locally (schema, expiry not yet passed), derives
  the display inputs (§3) and the approval phrase, then calls the GitHub
  workflow-dispatch API on the default branch with the operation, the
  request document, and the display inputs.
- Authenticates with the caller's ordinary `GH_TOKEN` (needs `actions:
  write` on this repository). This is safe by construction: **dispatch
  grants nothing** — every input is re-verified by the protected job before
  App-token creation, and an unapproved request is inert. The
  different-actor invariant is preserved because the approver must still be
  a different immutable actor ID than the dispatching identity.
- The dispatch API returns no run ID, so `submit` polls the workflow's run
  list (bounded, ~60 s) for a run created after dispatch whose run-name
  matches its display inputs, then prints exactly what the notification
  layer needs, as text and as `-json`:

  ```
  run_url:  https://github.com/<owner>/<repo>/actions/runs/<id>
  phrase:   prepare 182 mango-harbor-violet-inlet
  pr:       182  head: 4e99892  base: main
  expires:  2026-07-31T14:32:00Z
  ```

- If the poll cannot find the run, `submit` exits non-zero with the
  dispatch still performed and says so; the operator path degrades to the
  Actions tab, never to a silent miss.

`submit` holds no signing keys, mints nothing, and never touches
`gate-state`. It is transport for a document the protected job will
re-verify from scratch.

## 3. Legible run surface

### 3.1 Display inputs and run-name

`gate-executor.yml` gains three optional `workflow_dispatch` inputs used
only for display: `display-operation`, `display-pr`, `display-head`
(short SHA). The workflow sets:

```yaml
run-name: "${{ inputs.display-operation }} · PR #${{ inputs.display-pr }} · ${{ inputs.display-head }}"
```

Display inputs are claims, so gate makes lying refuse: the `prepare` and
`execute` verbs gain `-display-operation`, `-display-pr`, `-display-head`
flags (wired from the workflow inputs) and refuse, pre-credential, if any
non-empty display value disagrees with the verified request document. A
mislabeled run can therefore exist as a *label* but can never authorize
anything, and the refusal names the mismatch.

### 3.2 The `describe` job

A new first job in `gate-executor.yml`, running immediately on dispatch
while the protected job waits for approval:

- `permissions: contents: read` only; **no** environment, **no** secrets,
  no `gate-state` checkout.
- Checks out the dispatched trusted `main` commit (same `github.sha` pin as
  the protected job), builds gate, and runs a new read-only verb:

  ```
  gate executor describe -request <file>
  ```

  which validates the document shape and prints a Markdown card to the step
  summary: operation, PR number/title, head (short + full), base,
  merge-base, expiry, replay ID, and the exact phrase to enter. Malformed
  input produces a card that says *malformed request* and the job fails —
  visible, not silent.
- The protected job does not depend on `describe` (`needs:` is not used);
  a describe failure must not be able to park an approval, and an approval
  must never be able to skip verification anyway.

The card is intentionally the anti-spoof anchor: it is derived on trusted
`main` from the actually-dispatched JSON, so whatever a Slack message
claims, the page the operator approves on shows the truth. The phrase is
not a secret (only an environment reviewer's approval counts), so printing
it on the run page is safe — and teaching the operator to type it *from
this card* is the habit that makes Slack spoofing pointless.

## 4. Slack decision card

Rides the existing `escalate`/`flare` transport; no new authority surface.
The agent (or a thin wrapper around `submit -json`) posts one message per
decision:

```
🔐 gate: prepare · PR #182 — tier-aware review policy
head 4e98928 → main · expires 14:32 UTC (in 18m)
phrase: `prepare 182 mango-harbor-violet-inlet`
→ Review & approve: <run_url>
```

- The phrase sits in an inline code span (one-tap copy on mobile); the
  link lands on the run page whose `describe` card shows the same facts.
- On terminal outcomes the same message is edited in place, using the
  refusal vocabulary gate already emits:
  - ✅ `merged` — merge commit link and result record ID;
  - ⌛ `expired` — with an explicit *ask the agent to regenerate* line;
  - 🚫 `refused: <reason>` — e.g. *head moved (expected 4e98928, live
    a1b2c3d); request void*;
  - ⏸ `already in flight — claim gxc_…` for duplicate execute attempts.
- Slack remains notification-and-clipboard only. It cannot approve,
  dispatch, or resolve; a compromised workspace can mislead at worst, and
  the describe card (§3.2) is the surface of record. GitHub stays the sole
  audit trail (design.md I7); the thread is a mirror.

## 5. Failure and retry UX

No new states — this section fixes the *rendering* of refusals gate already
produces. All of these are clean pre-credential stops; nothing half-happens.

| Event | Gate behavior (unchanged) | Surface behavior (new) |
|---|---|---|
| Request expired | refuse pre-token | card → ⌛ with regenerate prompt; windows are never extended |
| Head moved | refuse, naming expected vs live | agent voids the card proactively on PR push; regeneration re-enters the full loop, including panel re-coverage of the new head |
| Double approval tap | GitHub records one decision per run | nothing to do; second tap is a no-op at the source of truth |
| Duplicate execute dispatch | refuse on open/duplicate claim | card → ⏸ naming the claim, linking the in-flight run — an in-progress state, not an error |
| Action already consumed | refuse, terminal result exists | card → ✅ receipt (merge commit + result record) |
| Approved after expiry | approval lands in history; refuse pre-token | run summary + card both explain; the authorized-nothing approval is itself audit record, which is correct |

## 6. Operator setup (non-code, one-time)

Recorded here because Phase 0's phone flow assumes it; these are operator
actions, not repository code:

1. On the **independent reviewer account**: register the hardware security
   key as a passkey and as 2FA, then **remove weaker 2FA methods**
   (TOTP/SMS) and store recovery codes (or a second key) offline. The
   reviewer login *is* the merge authority; this puts physical possession
   in front of it.
2. Install GitHub Mobile signed into the reviewer account; enable push
   notifications for **deployment reviews** on this repository.
3. Slack channel for gate decision cards confirmed reachable by the
   existing `escalate serve` configuration.

## 7. Tests

- **Wordlist pin** — golden test over the embedded list's byte hash and
  length (2048); a changed list is a failing test, since it silently
  changes every phrase.
- **Phrase derivation** — golden vectors: fixed digests → exact phrases;
  first-44-bits/big-endian boundary cases (indices 0, 2047); error on
  unknown op / non-positive PR.
- **Normalization** — table test: trailing space, leading space, internal
  double space, capitalized words accepted; wrong word order, wrong word,
  wrong PR, wrong operation, missing hyphens refused.
- **Comment verification** — existing approval-comment refusal tests
  updated to the new canonical form; the refusal position in the
  pre-credential sequence is asserted unchanged.
- **Display mismatch** — `prepare`/`execute` refuse when any non-empty
  display flag disagrees with the request document; empty display flags
  remain accepted (dispatches from the raw Actions UI stay valid).
- **`describe` golden** — request JSON → exact Markdown card; malformed
  JSON → malformed-request card + non-zero exit.
- **`submit`** — against a stubbed API: dispatch payload shape, poll-match
  by run-name, bounded-poll timeout exits non-zero after dispatch,
  `-json` output shape.
- **Workflow assertions** — extend the existing pinned-workflow tests:
  `describe` job has no environment/secrets and read-only permissions;
  protected job has no `needs: describe`; run-name references only
  display inputs.

## 8. Out of scope (named, so absence reads as decided)

- The reviewer-device approval inbox (approval-ux.md design 4) and any
  storage of reviewer credentials anywhere but the operator's person.
- Check-run `requested_action` approval buttons — rejected in
  approval-ux.md: the App must not be its own approver.
- Custom hardware-key signing of request digests — rejected: GitHub cannot
  verify it, it rebuilds an identity system gate would have to custody, and
  it reintroduces the opaque string this spec removes.
- Escorted prepare→execute sequencing (design 5) — agent-side
  orchestration, welcome later, no repo change required here.
- Any change to grants, claims, artifacts, schemas, rulesets, or the
  refusal sequence beyond the comment encoding and the added display
  mismatch refusal.

## 9. Rollout

1. Land phrase encoder + verification cutover + tests (one commit — both
   sides of the comparison move together).
2. Land `describe` verb, display inputs/flags, run-name, workflow job.
3. Land `submit` verb.
4. Wire the Slack card in the agent layer; confirm edit-in-place on a
   dry-run refusal.
5. Operator completes §6; one live `prepare` canary approved entirely from
   the phone before calling Phase 0 done.
