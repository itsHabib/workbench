# Approval UX — calm, phone-friendly operator decisions

Status: **Design 2 and the Phase 0 recommendation are BLOCKED** — the
committed slice (designs 1–3) is specified as a TDD in
`docs/features/gate-approval-ux/spec.md`, and that TDD's **§4.1.1 withdraws
the security argument Design 2 rests on**. A 44-bit phrase does *not* keep
the binding intact: a compromised dispatcher can grind free-form request
fields and substitute a materially different but independently valid
request carrying the same phrase, and today's full-SHA-256 comment does not
have that weakness. **Do not implement Design 2 (or the Phase 0
recommendation that includes it) until a binding remedy is selected in
§4.1.1.** Designs 1 and 3 are unaffected except that they assume the phrase
Design 2 defines. Read this document as a design-space survey, not as
current security guidance.
Date: 2026-07-31

`design.md` owns the security contract: run-specific independent environment
approval, exact canonical comment, pre-credential refusals, one-App custody,
one-time claim. This document owns the *operator experience* of exercising
that contract, which today is taxing:

1. the agent asks for a PR approval;
2. the operator opens a GitHub Actions run;
3. the operator approves the `prepare` deployment and must paste an opaque
   canonical phrase (and, upstream of that, someone pasted a full request
   JSON into the `workflow_dispatch` form);
4. the agent emits the execution request;
5. the operator approves a second deployment, pasting again;
6. the merge happens.

The goal: **one clear decision at a time, answerable from a phone**, without
weakening a single verification and without teaching the operator
cryptography. GitHub stays the source of truth and the audit surface in every
design below.

## Invariants no design may move

Every proposal is evaluated against this checklist. A design that trades any
row for convenience is rejected in this document, not discovered later.

| # | Invariant | Where it lives today |
|---|-----------|----------------------|
| I1 | Approval is a protected `gate-authorization` environment decision by an immutable actor ID different from the dispatcher; first run attempt only. | design.md §Flow 3 |
| I2 | The approval is bound to the exact request — repo, PR, head, base, merge-base, action hash, argv, expiry, replay ID — via the exact canonical comment. | design.md §Versioned artifacts |
| I3 | All refusals fire **before** App-token creation; the token is minted in-process and never leaves it. | design.md §Refusal semantics |
| I4 | The claim is a permanent one-time CAS; a durable claim's merge is never retried. | design.md §Flow 7–8 |
| I5 | No agent-minted grants; grant minting stays operator-only. | design.md §Post-bootstrap preparation |
| I6 | No admin bypass, no weakened checks, no mutable "merge latest". | enforcement.md |
| I7 | GitHub records the decision (approval history, `gate-state` commits) — no side-channel is authoritative. | design.md §State and branch identities |

Two derived observations shape everything below:

- **Dispatch grants nothing.** Every input to `workflow_dispatch` is
  re-verified by gate before any credential exists (I3). So the *agent* can
  safely perform the dispatch, with its own identity, carrying the JSON. The
  operator should never touch a JSON blob — their job is the approval, which
  is the only act that carries authority (I1). The different-actor check still
  holds: the agent dispatches, the independent reviewer approves.
- **The pasted phrase is a proof of attention, not a secret.** Gate requires
  the exact comment so an approver cannot rubber-stamp a run without having
  seen the specific request. Any replacement must preserve "the approver
  demonstrably engaged with *this* request" — but nothing requires the proof
  to be an opaque hex string.

## Design 1 — Legible run surface + GitHub Mobile (smallest)

**Change.** Make the thing the operator already opens readable, and deliver
it as a push notification.

- Add a `describe` job to `gate-executor.yml` that runs immediately on
  dispatch — no environment, no secrets, read-only permissions. It parses the
  dispatched request JSON and writes a step-summary card: operation, PR
  number and title, short head SHA, base, expiry time, and the exact phrase
  the approver must enter. It also sets a legible `run-name`
  (e.g. `prepare · PR #182 · 4e99892 · expires 14:32Z`) via display-only
  dispatch inputs that gate re-verifies against the JSON and refuses on
  mismatch. **The label can still lie at the moment of decision** — gate
  detects the mismatch only when the protected job runs, i.e. *after* the
  reviewer approves — so the run-name is a convenience, never an approval
  surface. The describe card is the surface of record; see
  `docs/features/gate-approval-ux/spec.md` §4.4 for the precise scope of
  what display falsification does and does not catch.
- The independent reviewer account enables GitHub Mobile push notifications
  for deployment reviews. GitHub Mobile can approve environment deployments
  and attach the comment natively.
- The agent performs the `workflow_dispatch` API call itself (see derived
  observation above), so step 2 of today's flow disappears: the operator's
  phone buzzes with a reviewable card instead.

**Operator sees/does:** push notification → run page with a plain-language
card → *Approve* → enter phrase → done. One decision, one screen.

**Security intact:** nothing gate verifies changed. The `describe` job holds
no secrets and no authority; if it lied, gate's own verification of the JSON
refuses (I2, I3). Dispatch moving to the agent is covered by I1's
different-actor check plus I3.

## Design 2 — Word-coded approval phrases ⛔ BLOCKED

> **Blocked pending a §4.1.1 remedy in the TDD.** As specified below (44
> bits / four words) this is a **regression** against today's full-digest
> comment, not a neutral re-encoding — see `docs/features/gate-approval-ux/spec.md`
> §4.1.1. The threat framing later in this section is retained only as a
> record of what was believed; it is **superseded** by §4.1.1 and must not
> be cited as justification.

**Change.** Keep the exact-comment binding; change its encoding. Instead of
`gpr_3f9c…`, gate derives the canonical comment as words:

```
prepare 182 mango-harbor-violet-inlet
execute 182 copper-lantern-mesa-drum
```

- The words are a deterministic encoding of the canonical semantic digest
  (a fixed 2048-word list; 4 words ≈ 44 bits). Gate derives the same words
  server-side and requires an exact match, exactly as today.
- The leading `prepare|execute <PR#>` makes the typing act itself the
  attention check: the operator cannot enter the phrase without stating which
  operation and which PR they believe they are approving.
- Threat framing: the digest here is a *binding label inside a
  20-minute, run-specific, replay-protected window* (I2). The realistic
  failure is "operator approves request A believing it is B", and 44 bits
  across the handful of concurrently-live requests makes an **accidental**
  collision negligible (~2⁻⁴⁴ per pair). **An engineered collision is not
  negligible** — the window does not prevent offline grinding, since an
  attacker can vary free-form fields locally and dispatch only once a
  matching prefix is found — and the design does not rely on it being hard.
  It relies on the phrase check and the full-document verification being
  independent gates, so a colliding phrase authorizes nothing. The full
  digest still rides inside the request JSON and the claim; only the
  human-facing phrase is shortened. **`docs/features/gate-approval-ux/spec.md`
  §4.1 is authoritative for this argument;** this bullet is a summary.

**Operator sees/does:** types four words and a PR number from the
notification card — comfortable on a phone keyboard, no clipboard gymnastics.

**Security intact:** the verification is byte-for-byte the same exact-match
comment check; only the canonical encoding function changed. Expiry, replay
ID, run binding, different-actor: untouched.

## Design 3 — Slack nudge through the existing escalation seam

**Change.** The workbench already ships a Slack transport (`escalate serve`)
and a routing sink (`flare`). Reuse them as the *notification and clipboard*
surface — never the authority surface.

- When the agent emits a preparation or execution request, it routes one
  Slack card: PR link and title, short head, diffstat, expiry countdown, the
  word-phrase in a copyable code block, and a deep link to the exact
  workflow run's review page.
- On terminal outcomes the card is edited in place: ✅ merged (with merge
  commit link), ⌛ expired, 🚫 refused (with the refusal reason). An optional
  *Regenerate* button routes back to the agent as an ordinary escalation
  resolution — the agent re-emits a fresh request with a new replay ID, which
  still needs a fresh independent approval, so the button moves no authority.

**Operator sees/does:** Slack message → tap deep link → GitHub app approval
screen (Design 1's card) → approve, pasting the phrase from the code block or
typing the words. Everything readable in one thread; history of decisions
visible in the channel.

**Security intact:** Slack can neither approve nor dispatch; it holds no
keys and no token with any gate authority. A compromised Slack workspace can
at worst send a misleading card — and the GitHub run page's `describe` card,
derived from the actually-dispatched JSON, is the surface the operator
approves on. The habit to teach is one comparison: *PR number on the approval
page matches PR number in the phrase you're typing.* GitHub remains the sole
audit record (I7); the Slack thread is a convenience mirror.

## Design 4 — Reviewer-device approval inbox (ambitious)

**Change.** A small static PWA — an approval inbox — running **on the
operator's phone**, authenticated to GitHub as the independent reviewer
account.

- A static page (hostable from this public repo via Pages; `api.github.com`
  supports CORS, so no server exists at all). On the phone it stores a
  fine-grained PAT of the reviewer account — scoped to this repository,
  Actions read/write only — in device-local storage behind a WebAuthn/passkey
  gate.
- It lists pending `gate-authorization` deployments via the pending-
  deployments API, renders each as a decision card (decoded request: PR,
  head, base, action, expiry countdown, diff link), and on *Approve*
  requires a deliberate arming step — type the PR number — then calls the
  review-pending-deployments API with `approved` and the canonical comment
  auto-filled.
- Decline is a first-class equal-sized button, recorded as a rejected
  deployment with a reason comment.

**Operator sees/does:** open inbox (FaceID/passkey) → one card at a time →
read → type PR number → tap Approve. No pasting anywhere in the whole flow.

**Security intact — with one custody rule that must be stated in bold:**
the approval still lands as a GitHub environment decision recorded against
the reviewer's immutable actor ID; every gate verification (I1–I4) is
unchanged, including the exact comment — the inbox merely types it
faithfully. Auto-filling the comment removes typing-as-attention, which is
why the arming step (type the PR number) exists: the deliberate act is
preserved, just aimed at a human-meaningful value instead of a hash.

**The custody rule: the reviewer token lives only on the operator's personal
device.** If that PAT ever sits on the agent's box, the agent can approve its
own requests and the independence in I1 is dead — this is the *only* way this
design rots into an agent-minted grant, so it is named here as a hard
prohibition, not a deployment detail. Equivalent framing: the inbox is
"GitHub Mobile logged into the reviewer account, with a better screen for
this one decision." It inherits exactly that trust model, nothing more.

## Design 5 — Escorted two-phase: one decision at a time

**Change.** Keep both approvals — they answer different questions — but
remove all navigation and waiting between them by letting the agent drive the
sequencing.

- Phase 1 card: *"Publish Gate's evaluation of PR #182 at 4e99892 to the
  hosted ledger, under your grant `g_…`? (prepare — no merge authority)"*
- On approval, the prepare run publishes the action; the agent immediately
  emits the execution request pinned to the fresh action hash and dispatches
  the execute run. Within about a minute the phone buzzes again:
- Phase 2 card: *"Gate recorded `would_merge` for PR #182 at 4e99892.
  Execute the exact commit-pinned squash merge? (one-time claim)"*

Why the two phases cannot soundly collapse into one approval, stated once:
**the execution approval binds to the newest action hash, which does not
exist until preparation has run** (I2). A single up-front approval covering
"prepare and then execute whatever comes out" is precisely the mutable
"merge latest" delegation I6 forbids. The escort model keeps both decisions
meaningful while making them feel like one short conversation.

Optionally, the Gate App posts a decision-record card on the PR itself
(check run or comment) after each phase — a rich, durable receipt on the
surface where the operator already reads code. One idea considered and
**rejected**: check-run `requested_action` buttons as an approval surface.
The button's webhook is handled by the Gate App — the custodian would become
its own approver, collapsing the independence between executor and reviewer
that I1 exists to guarantee. Checks display and record; they never decide.

## Failure and retry UX

All refusals are pre-credential (I3), so every failure below is a clean stop
with nothing half-done. The UX job is to say so calmly.

- **Expired request.** Gate refuses; the card flips to ⌛ with the expiry
  that passed and a one-tap *Regenerate* (agent re-emits: fresh replay ID,
  fresh window, fresh approval required). Windows are never extended —
  regeneration is the only path.
- **Stale head.** The PR moved after the request was cut. Gate refuses,
  naming expected vs. live head; the agent (already watching PR events)
  voids the card proactively: *"PR #182 moved to `a1b2c3d` — this request is
  void."* The regenerated request re-enters the full loop, panel coverage of
  the new head included. That round-trip is the security model working, and
  the card should say so rather than apologize.
- **Double tap.** GitHub records one decision per environment per run —
  a second tap is a no-op at the source of truth. At the execution layer the
  claim is a one-time CAS (I4): a duplicate execute dispatch refuses on
  open/duplicate claim. Render as *"already in flight — claim `gxc_…`"*,
  linking the claim, not as an error.
- **Action already consumed.** Refusal names the terminal result; the card
  shows the receipt — *"merged at `<merge commit>`"* with the `gate-state`
  record link. Orphaned expired claims keep their separate, deliberately
  boring path: operator-dispatched `reconcile` with its own approval.
- **Approved after expiry (the race).** The approval lands in history, gate
  refuses pre-token, the run summary explains, the card updates. The
  approval-that-authorized-nothing is itself part of the audit record —
  which is the correct outcome, not a bug to paper over.

## What is deliberately not built

- No admin bypass, no auto-approve, no approval quorum shortcuts (I6).
- No bot or agent identity as environment reviewer, ever (I1, I5).
- No custom signing UX for the operator — passkeys/YubiKeys are used where
  they already exist (GitHub sign-in and sudo prompts for the reviewer
  account; WebAuthn gate on the Design-4 inbox), never as bespoke crypto the
  operator must reason about.
- No approval authority in Slack, checks, comments, or any surface the App
  itself controls.
- No reviewer credential co-located with the agent (Design 4's custody rule).

## Recommendation — Phase 0 ⛔ BLOCKED (item 3 below)

> **This recommendation cannot be implemented as written.** It includes
> word-coded phrases (Design 2), whose security argument is withdrawn in
> TDD §4.1.1. Items 1, 2, 4 and 5 stand on their own; item 3 waits on a
> binding remedy. The TDD, not this section, is the live plan.

Ship **Designs 1 + 2, with Design 3 riding the already-built Slack seam**:

1. agent-performed `workflow_dispatch` (operator never touches JSON);
2. the `describe` job, verified display inputs, and legible `run-name`;
3. word-coded canonical phrases (`prepare 182 mango-harbor-violet-inlet`);
4. one Slack card per decision via `escalate`/`flare` with deep link,
   copyable phrase, and edited-in-place terminal state;
5. GitHub Mobile deployment-review notifications on the reviewer account.

This is a few small, legible Go/YAML changes in a public repo: a phrase
encoder (pure function + golden tests), a display job with no authority, and
one more card type on an existing Slack transport. Every byte gate verifies
is unchanged except the canonical comment encoding, which stays an
exact-match check. The operator's day becomes: *phone buzzes → read card →
tap link → type four words → done; buzzes again → confirm the merge → done.*

Design 5's escort sequencing is agent-side orchestration and can land any
time after Phase 0. Design 4 is the later investment if phone friction still
hurts once the paste is gone — adopt it only with the custody rule enforced
as written.
