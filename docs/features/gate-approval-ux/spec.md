# Gate approval UX — Technical Design Document

**Status:** approved design — implementation is gated on P0. The approval
phrase uses eight words (88 bits) and names the requested decision, closing
the feasible offline-grinding attack found in review. The protected job waits
for the trusted card to render before exposing approval. P0 still must prove
the mobile comment and card surfaces before P1 begins.
**Owner:** @itsHabib
**Date:** 2026-07-31
**Related:** `docs/features/trusted-gate-judgment-bridge/design.md` (the security
contract this must not move), `docs/features/trusted-gate-judgment-bridge/approval-ux.md`
(the design-space survey this TDD commits a slice of), `cmd/gate/docs/enforcement.md`.

> **Implementation focus (v7):** §9.1 P0 is the next move — the
> phone-surface assumptions block implementation and the spike has not run
> yet. §11's single-operator drill limitation remains an explicit validation
> constraint. The exact-request binding and card-ordering findings are closed
> by §4.1 and §4.3.
>
> *Review history: v2 folded round 1, v3 round 2 (§4.1 collision-claim
> correction, P0 elevation), v4 round 3 (encoder ownership moved to
> `contracts`), v5 round 4 (§6.3 field gap, drill mechanics), v6 locked the
> eight-word decision-bearing phrase and fail-closed card ordering; v7 closed
> retry correlation, operation-specific cards, and fallback credential scope.*

## 1. Problem & hypothesis

The custodied Gate executor works and its security model is sound, but
exercising it is taxing: for one merge the operator pastes a request JSON
into a `workflow_dispatch` form, navigates to the run, approves a protected
`gate-authorization` deployment while pasting an opaque canonical phrase
(`gpr_3f9c…`), and then repeats the whole loop for the execute phase. It
requires a laptop, clipboard gymnastics, and reading hex.

**The bet:** every element of that friction is an *encoding or transport*
choice, not a security requirement. The approval comment is a proof of
attention (the approver demonstrably engaged with *this* request), and
nothing requires proofs of attention to be hex. The dispatch carries no
authority (everything is re-verified pre-credential), so an agent can
perform it. The run page can explain itself. A phone push can replace the
navigation. We can make the operator's day "phone buzzes → read card → type
eight words → done" while every verification step stays the same, except one
canonical encoding.

**Non-goals (and why):**

- No new approval authority — no Slack approvals, no check-run buttons, no
  bot approvers. Only the independent reviewer's GitHub environment
  decision counts, exactly as today.
- No reviewer-device approval inbox (approval-ux.md design 4) — deferred
  behind the validation gate; only worth its custody rules if friction
  survives this phase.
- No custom hardware-key signing of digests — GitHub cannot verify it, it
  rebuilds an identity system gate would have to custody, and it
  reintroduces the opaque string this work removes.
- No changes to grants, claims, artifacts, schemas, rulesets, or the
  refusal sequence beyond what §5–§6 name.

**What this concentrates (v3, from review round 2).** This TDD is one
committed slice beneath the workbench UX overhaul (#205). If that doc's
D4 lands — `-readiness {human|panel}` as an operator-minted grant field —
then the phone approval optimized here becomes the *only* human act in a
portfolio merge, and the describe card becomes the single human control on
the path. That concentration is intended (it is the point of the pair), but
it raises the stakes on §4.3: every reviewer of this document should weight
the card's reachability accordingly, and it is why P0 blocks rather than
advises.

## 2. Functional & non-functional requirements

**Functional:**

- FR1 — Canonical approval comments for `prepare`/`execute` are
  human-typeable word phrases bound to the request digest.
- FR2 — The agent can dispatch the executor workflow carrying the request
  document; no human touches JSON.
- FR3 — The workflow run names itself legibly and renders a plain-language
  decision card (including the phrase) before approval, from an
  unprivileged job.
- FR4 — One Slack message per decision (via the existing `escalate`/`flare`
  transport) with facts, phrase, and a deep link; edited in place on
  terminal outcomes.
- FR5 — Refusals render as calm, specific card states (expired / head moved
  / in-flight claim / already consumed), never raw errors.

**Non-functional:**

| Dimension | Target |
|---|---|
| Security | Zero change to design.md verification semantics: same environment, same different-actor/first-attempt/expiry/replay checks, same pre-credential refusal position. Diff to the verified byte-stream: comment encoding only, plus one *added* refusal (display mismatch). |
| Auditability | GitHub remains the sole record: approval history, run logs, `gate-state`. Slack is a mirror; losing it loses nothing authoritative. |
| Operability | A decision is answerable from a phone in ≤60 s from push notification, with no laptop in the loop. |
| Cost | One extra unprivileged CI job per dispatch (~1 min build). No new services, no new hosted state. |
| Simplicity | Pure functions + two CLI verbs + YAML. No daemon, no database, no new module dependency (wordlist is vendored data). |

## 3. Architecture overview

Everything hangs off existing seams; nothing new holds authority.

```
agent box                          GitHub                         operator phone
─────────                          ──────                         ──────────────
gate executor request ──emit──▶ request.json
gate executor submit  ──POST──▶ workflow_dispatch ──▶ run
                                  ├─ describe job (no secrets):   ◀── deep link
                                  │    step-summary card + phrase
                                  └─ protected job: waits on
                                     gate-authorization approval  ◀── approve +
escalate/flare ──Slack card──────────────────────────────────────▶    type phrase
                                     then: verify everything,
                                     mint App token in-process,
                                     claim / merge / record (unchanged)
```

New: a phrase encoder (pure function), `gate executor submit` and
`gate executor describe` (verbs), display inputs + `describe` job in
`gate-executor.yml`, one Slack card type. Reused: the entire verification
and custody path, the escalation transport, GitHub Mobile's native
deployment-review UI.

## 4. Key decisions & trade-offs

### 4.1 Word-coded phrase instead of hex digest — **the load-bearing decision**

**Choice:** canonical comment becomes
`<operation> <pr-number> <intent> <w1>-…-<w8>`, e.g.
`prepare 182 block mango-harbor-violet-inlet-copper-lantern-mesa-drum`.
The intent is the preparation decision (`pass` or `block`) or the execution
outcome (`would_merge`). Eight words encode the first 88 bits of the request's
*existing* canonical semantic digest, indexed into a vendored
2048-word list (the BIP-39 English list as embedded data; no BIP-39 library,
no checksum semantics — just 2048 well-reviewed, distinct, phone-typeable
words). Verification stays an exact-match comparison in the same
pre-credential slot.

**Alternatives:** (a) keep the full hex paste — maximally collision-proof,
but it is the UX being removed, and it proves clipboard possession, not
attention; (b) drop the comment entirely and rely on run-specific approval
— rejected: loses the only proof the approver engaged with *this* request
rather than blind-approving a pending run.

**Binding strength:** this design deliberately does ask the human encoding to
be a cryptographic request binding. The earlier four-word design provided 44
bits and was grindable offline. Eight words provide 88 bits: a targeted
second-preimage search costs about 2⁸⁸ SHA-256 evaluations, beyond a feasible
attacker budget. The full 256-bit digest still rides in the request JSON,
authorization, and claim, unchanged.

### 4.1.1 Resolved attack and decision record (v6)

The original four-word design was unsafe. Its argument assumed that
*mechanically valid ⇒ materially equivalent*. That assumption is false
against this contract, and the reviewer's attack works.

**The attack.** A compromised dispatcher builds a **second, fully valid**
request for the same repo, PR, head, base and operation, differing only in
fields the operator cares about, and grinds a free field until its 44-bit
phrase equals the phrase the operator was given for the request they
intended. The operator types their phrase; it matches; the substituted
document passes every document check on its own merits; the wrong request
is approved.

**Verified against the contract, and it is worse than the report:**

- `PreparationRequest.Decision` is validated as `"pass"` **or** `"block"`
  (`preparation.go:59`). Both are valid. So the two documents can carry
  **opposite judgments** for the same PR and head.
- The grinding space is not just `ReplayID` (`evt_` + 32 hex, shape-checked
  only): `Why` is **up to 4096 free-form bytes** (`preparation.go:63`) and
  `JudgmentQuestion` is free text on the execute side
  (`gateauthorization.go:67`). All feed the canonical digest. Finding a
  44-bit collision here is trivially parallel and offline.
- `GrantID` also varies — a substituted request can spend a *different*
  operator-minted grant.

**This is a regression, not a pre-existing gap.** Today's comments bind the
**full** canonical digest — `gate approve <authorizationID> …` and
`approve gate preparation <gpr_…> …`, both full SHA-256 identities. That is
ungrindable. Replacing them with 44 bits *removes* binding strength that
exists today. The v3 restructure moved the argument off "grinding is
expensive" and onto "independence"; round 4 shows independence does not
hold either, because the phrase is the **only** thing distinguishing two
valid documents that differ in what they authorize. The typed prefix does
not help: `<operation> <pr-number>` is identical across both — it names the
operation, never the *decision*.

**Decision:** combine the first two remedies. The phrase carries eight words
(88 bits), and the preparation comment names `pass` or `block`. This restores
an infeasible-to-grind exact-request binding without changing request schemas
or constraining existing free-form fields. P0 must include an eight-word phone
typing check; failure sends the design back to a different authority surface,
not to a shorter phrase.

To be explicit about layering: the phrase check and the full-document
verification are **parallel defenses in the same pre-credential block, not
sequential**. A phrase-only match authorizes nothing — gate independently
re-verifies every field of the request document (repo, PR, head, base,
merge-base, action hash, argv, expiry, replay ID) against live GitHub and
hosted state. The phrase is the attention gate; the document verification
is the correctness gate.

**What the phrase is therefore load-bearing for:** the operator asserting
*which exact request* they engaged with. Its security depends on both the
88-bit binding and the trusted surface the attention lands on (§4.3, §9 P0).


### 4.2 The agent performs the dispatch

**Choice:** new `submit` verb; the agent's ordinary token (`actions:
write`) triggers `workflow_dispatch` carrying the document.

**Why it's safe:** dispatch grants nothing — every input is re-verified by
the protected job before App-token creation, and an unapproved request is
inert. The different-actor invariant is *strengthened* in practice: the
dispatcher is now always the agent identity, so the approver is structurally
never the dispatcher. **Alternative:** operator keeps dispatching —
preserves nothing (the dispatch was never an authority act) and costs the
entire laptop-and-JSON step.

Two residuals named (v2, from review):

- **`actions: write` blast radius.** The permission can dispatch *any*
  `workflow_dispatch` workflow in the repository, not just
  `gate-executor.yml`. A compromised agent token still cannot make
  `gate-executor.yml` do anything (I3 re-verifies everything
  pre-credential), but it could trigger unrelated workflows. That is a
  property of GitHub's permission granularity, not of this design; it is
  the same exposure the agent's push-and-open-PR token already carries and
  is accepted with the same eyes-open posture as enforcement.md's named
  residuals. It is also **not silent**: every dispatch, including misuse of
  the wider permission, lands in the Actions log with its actor, so the
  residual is covered by detection even though it is not prevented (v3).
- **Document transport.** The request JSON travels as a
  `workflow_dispatch` input — exactly as it does today when pasted by
  hand; `submit` changes who types, not the channel. The documents are
  bounded by construction (IDs, SHAs, argv, and digests — the evidence
  digest, never the evidence or diff bodies), so GitHub's ~64 KB dispatch
  payload bound is not a practical constraint; `submit` still checks the
  encoded size and refuses locally with a clear error rather than letting
  the API reject it.

### 4.3 Unprivileged `describe` job gates approval availability

**Choice:** the card-rendering job has read-only permissions, no secrets,
and no environment. The protected job declares `needs: describe`, so GitHub
cannot expose the environment approval until the trusted card exists.

**Why:** the card is the anti-spoof anchor, derived on trusted `main` from
the actually-dispatched JSON. A slow or failed render withholds the approval:
that is fail-closed availability loss, not an authority inversion. P2 accepts
the roughly one-minute latency rather than introducing a second fast-render
path. `describe` keeps its read-only, secretless, no-`gate-state` posture.

**The anchor must be reachable from the approval surface, or it anchors
nothing (v3, from review round 2).** This is an *empirical* dependency on
GitHub Mobile, not a design property, and it is the single largest
unvalidated assumption in this TDD. If the step-summary card is not
legible from — or reachable from — the mobile deployment-review screen,
the operator's only readable surfaces at decision time are the Slack card
(explicitly untrusted, §4.6) and the run-name (built from agent-supplied
display inputs, §4.4). The taught habit then degrades into *transcribing
the phrase from Slack*, and the attention proof proves attention to the
untrusted surface — the exact failure this design charges against the hex
paste ("proves clipboard possession, not attention", §4.1). Hence P0 in
§9: verify before building, not at the validation gate.

### 4.4 Display inputs are claims that gate falsifies

**Choice:** `run-name` is built from display-only dispatch inputs
(`display-operation`, `display-pr`, `display-head`); `prepare`/`execute`
gain matching flags and refuse pre-credential if any non-empty display
value disagrees with the verified document *(pre-credential in the
protected job — i.e. after the environment approval but before the App
token is minted; this is the same slot as every other refusal in §7, not a
pre-approval check)*. A mislabeled run can exist as a label, and **can
therefore still lie at the moment the operator decides**; it simply can
never authorize anything. **Alternative:** parse the JSON in
workflow expressions — not expressible in `run-name` context; and trusting
labels without falsification is how display lies become approvals.

**Scope of what this catches, stated precisely (v3, from review round 2):**
display falsification catches *incoherent* relabeling — a display value
that disagrees with the document it ships with. It does **not** catch a
*coherent lie*: a compromised `submit` that derives its display inputs
faithfully from a malicious document produces no mismatch anywhere. Against
that case the defenses are the describe card (which renders the real
document, so the operator sees the true PR and head) and the full
pre-credential verification (which refuses the malicious document on its
own merits). Neither the run-name nor the mismatch refusal is a defense
against a coherent lie, and this document should not be read as claiming
otherwise.

### 4.5 One-commit cutover, no dual acceptance

Emitter and verifier are the same binary; requests live ≤20 minutes; hosted
state stores documents, not comments. Accepting both encodings would be
complexity buying nothing.

### 4.6 Slack is notification-and-clipboard, never authority

The card rides the shipped `escalate serve` transport. A compromised
workspace can mislead at worst; the describe card is the surface of record,
and the habit taught is one comparison (PR number on the approval page
matches the phrase being typed). GitHub stays the sole audit trail.

### 4.7 Considered and rejected (recorded so absence reads as decided)

- Check-run `requested_action` buttons as approval surface — the App would
  handle its own approval webhook: custodian becomes approver. Rejected.
- Hardware-key signing of the digest as the comment — see non-goals.
- Re-encoding the `reconcile` comment — it names a `gxc_` claim identity
  the operator is looking directly at, rarely; boring is correct there.

## 5. Data model

No schema, artifact, grant, claim, or `gate-state` changes. What changes:

- **Canonical comment encoding** for prepare/execute approval comments
  (derivation in §6.1). The digest input is the existing canonical semantic
  digest — no new hash, no new canonicalization.
- **Vendored wordlist**: 2048 words, `go:embed` text file under
  `contracts/gateauthorization` (§10.1), byte-hash pinned by a golden test (a changed list
  silently changes every phrase — the test makes that loud). The same
  golden also pins the eight-word output for a fixed digest fixture (v3,
  from review round 2), so an off-by-one in the bit-slicing cannot survive
  a wordlist that still hashes correctly — the two assertions fail
  independently and name different bugs.
- **Workflow display inputs**: exist only in the dispatch payload and
  run-name; never stored, never trusted (§4.4).

## 6. API contract

### 6.1 Phrase encoder (pure, `contracts/gateauthorization`)

Placed beside the existing `ExpectedApprovalComment` /
`ExpectedPreparationApprovalComment`, whose call sites it replaces — see
§10.1 for why `cmd/gate/internal/` is not viable.

```go
// ApprovalPhrase derives the canonical approval comment for a request.
// digest: the request's canonical semantic digest (existing bytes).
// op: "prepare" | "execute". pr: the request's PR number.
// intent: "pass" | "block" for prepare; "would_merge" for execute.
// Words: digest's first 88 bits, big-endian, as eight 11-bit indices into
// the embedded 2048-word list. Errors on unknown op or pr < 1. Never panics.
func ApprovalPhrase(digest [32]byte, op string, pr int, intent string) (string, error)
```

**One consequence to accept explicitly (v4).** Today's
`ExpectedApprovalComment` is `gate approve <authorizationID>
evidence=<digest> question=sha256:<hash>` — it deliberately "repeats the
two dense evidence fields for operator inspection." The word phrase drops
both from the *typed string*. That is the point (they are unreadable and
un-typeable on a phone), but it means the evidence digest and judgment
question move from the comment to the **describe card**, which §6.3
already renders — so the card must carry them, and P0's reachability
question (§4.3) now also governs whether those fields are inspectable at
all. This is a strict improvement in legibility only if P0 passes.

Comment verification normalizes the received comment, then requires exact
equality with `ApprovalPhrase(...)` recomputed from the verified document.
Normalization (resolved from §10.2, v2 — phone keyboards specifically
attack hyphens with smart-punctuation substitution):

1. map the Unicode hyphen family (U+2010–U+2014, U+2212, U+FE58, U+FE63,
   U+FF0D) to ASCII `-`;
2. accept `-` or a single space as the separator between the eight words;
3. trim, collapse internal whitespace runs to one space;
4. lowercase ASCII.

Word identity, word order, operation, PR number, and intent stay exact — the
normalization widens only the typeable surface, never the binding.

### 6.2 `gate executor submit`

```
gate executor submit -request <emitted-request.json> [-workflow gate-executor.yml] [-json]
```

Validates the document (schema, expiry unexpired), derives its stable request
identity (`PreparationID` or `AuthorizationID`), display inputs, and phrase.
Before dispatch, it queries runs for the earliest non-terminal run carrying
that request identity; if one exists, `submit` reuses it and does not dispatch.
Otherwise it POSTs the workflow dispatch, then polls the run list (bounded,
~60 s) for the earliest non-terminal run carrying that request identity. Once
the survivor is selected, `submit` cancels every later non-terminal run with
the same stable identity and confirms each reaches a terminal cancelled state
before it returns the survivor's URL.
Prints `run_url`, `phrase`, `pr/head/base`, `expires` (text and `-json`)
**only once the run is found** (v3, from review round 2): the phrase is the
string the operator will type, so it must never appear in output that
points at no discoverable run. Poll miss: non-zero exit that *says the
dispatch happened*, naming the workflow and time window but **withholding
the phrase** — degrades to the Actions tab, never a silent miss. Holds no
keys, touches no `gate-state`, mints nothing.

### 6.3 `gate executor describe`

```
gate executor describe -request <file>
```

Read-only; validates shape and prints the Markdown decision card for
`$GITHUB_STEP_SUMMARY`. Common fields: operation, stable request ID, PR +
title, head short+full, base, merge-base, expiry, replay ID, and exact phrase.
Operation-specific fields are required:

- **prepare:** grant ID, decision (`pass`/`block`), and `why`;
- **execute:** action ID + hash, merge argv, evidence digest, and
  judgment-question text + hash.

The execution evidence and question fields are required, not optional (v5,
from review round 4): §6.1 moves them off the typed string, so the card is the
only place they remain inspectable. Preparation displays the fields its
contract actually carries rather than inventing execution-only data.
Malformed input → a card that says *malformed request* + non-zero exit —
visible, not silent.

### 6.4 Changed surfaces

- `prepare`/`execute` verbs: new `-display-operation`, `-display-pr`,
  `-display-head` flags; non-empty mismatch with the document → refusal
  (pre-credential). Empty display flags stay accepted, so raw Actions-UI
  dispatches remain valid. A fourth display input, the stable **request
  identity** (§8), is carried in `run-name` for `submit` to match on. Gate
  derives and falsifies it against the document like the other display
  claims. Because it is stable across retries, a restarted `submit` can find
  and reuse the run already holding the executor queue slot.
- `gate-executor.yml`: four optional display inputs; `run-name` from
  them; new first job `describe` (`permissions: contents: read`, no
  environment, no secrets, no `gate-state` checkout, checks out the same
  pinned `github.sha`, builds gate, runs §6.3); the protected job declares
  `needs: describe` so approval cannot be exposed before the card renders.
- Error model: everything is gate's existing refusal vocabulary; the only
  new reason is the display mismatch. Like every post-approval refusal
  (§7.3), a display mismatch exits non-zero so the run and its approved
  deployment end in a terminal **failure** state, never a lingering
  pending one (v3).

## 7. Key flows

### 7.1 Prepare (happy path)

1. Agent: `gate executor request` → document + digest.
2. Agent: `submit` → dispatch → run URL + phrase; posts Slack card.
3. `describe` job renders the card on the run page; protected job waits.
4. Phone push (GitHub Mobile deployment review). Operator reads the card,
   approves, types `prepare 182 block mango-harbor-violet-inlet-copper-lantern-mesa-drum`.
5. Gate verifies everything per design.md — the only changed comparison is
   comment encoding — evaluates against hosted state, publishes the action.
6. Slack card → ✅ with the action reference.

### 7.2 Execute

Same shape: agent emits the execution request pinned to the fresh action
hash, `submit`s, operator gets the second push (*"Gate recorded
`would_merge` for #182 at 4e99892 — execute the exact merge?"*), types the
`execute` phrase, custodied claim/merge/record runs unchanged.

### 7.3 Expired request

Approval lands after the window (or dispatch sat idle). Gate refuses
pre-token; the job exits non-zero, so the run — and the approved
deployment it carries — ends in a terminal **failure** state, never a
lingering pending one (v2, from review: the audit surface shows a closed
refusal, not an open question). Run summary explains; Slack card → ⌛
*"expired 14:32 — ask the agent to regenerate."* Windows are never
extended; regeneration (fresh replay ID, fresh approval) is the only path.

### 7.4 Head moved

PR gets a new commit while a card is pending. The agent (already watching
PR events) voids the card proactively: *"PR #182 moved to `a1b2c3d` — this
request is void."* **The void is UX only and safety is non-contingent on
it** (v2, from review): gate refuses on head mismatch during its own
pre-credential re-read whether or not the agent was alive to void the
card — implementers must not build the security check into the
event-watching path. Regeneration re-enters the full loop including panel
re-coverage of the new head — that round-trip is the security model
working, and the card says so rather than apologizing.

### 7.5 Duplicate / already consumed

Duplicate execute dispatch → refusal on open/duplicate claim → card ⏸
*"already in flight — claim `gxc_…`"* linking the run (in-progress state,
not an error). The CAS guarantee this leans on is the existing one
(design.md §Flow 6–8): the claim is appended by advancing `gate-state`
**without force from the exact expected parent**, so of two
milliseconds-apart executors, exactly one commit lands and the loser's
CAS conflict fails closed — no new mechanics here, just a pointer for
reviewers (v2). Action already consumed → refusal names the terminal result
→ card ✅ receipt (merge commit + result record). Orphaned expired claims
keep the separate, deliberately boring `reconcile` path.

## 8. Concurrency / consistency / failure model

- **Double approval tap:** GitHub records one decision per environment per
  run; the second tap is a no-op at the source of truth. Nothing to build.
- **Approve-after-expiry race:** the approval enters history, gate refuses
  pre-token. An authorized-nothing approval in the audit record is the
  correct outcome, not a bug.
- **Execution idempotency:** unchanged — the one-time claim CAS; a durable
  claim's merge is never retried.
- **`submit` dispatch/poll gap:** dispatch is fire-and-forget by API
  design; the poll is bounded and a miss is loud (§6.2). A retry after a
  crashed-post-dispatch `submit`, or two concurrent submits for the same
  request, produce two runs: the first approved `prepare` publishes the
  action and the second refuses on duplicate-action/newest-action grounds;
  for `execute` the claim CAS arbitrates (§7.5). When two submits are both
  polling, run-name matching alone is ambiguous.

  **⛔ The v2 "newest-created run" tie-breaker was wrong and is withdrawn
  (v6, review round 5).** `gate-executor.yml` serializes every executor run
  through a single repo-wide concurrency group with
  `cancel-in-progress: false` (workflow lines 36–38). So a second dispatch
  does **not** supersede the first — it *queues behind* it. The newest run
  is therefore the **queued** one, while the run that actually reaches the
  approval gate is the **older, active** one. Printing the newest URL sends
  the operator to a run that cannot be approved yet; if they approve it
  later it only advances to a duplicate refusal, and the real pending
  approval sits unattended. The v2 claim that "a wrong pick is harmless"
  assumed the runs were interchangeable, which the concurrency group makes
  false.

  **Correct rule (v7):** correlate on the stable request identity, not a
  per-dispatch nonce. Before dispatch, `submit` searches for the earliest
  non-terminal run whose display request ID equals the document's derived
  `PreparationID` or `AuthorizationID`; if found, it returns that run and
  does not dispatch. After dispatch it performs the same search. Concurrent
  callers can still both pass the preflight before either run is visible, but
  both then select the **earliest non-terminal** stable-ID match (the run
  holding the queue slot) and report that reuse explicitly. They cancel every
  later non-terminal match through the Actions API and poll until GitHub
  reports each duplicate terminal. This cleanup is mandatory: a cancellation
  failure makes `submit` exit non-zero and withhold the phrase rather than
  leave a duplicate that can later occupy the repo-wide executor slot. The
  operation is convergent when callers race—both keep the same earliest run,
  and duplicate cancel requests are idempotent. Gate falsifies the displayed
  ID against the document, so correlation and cleanup add no authority.
- **`describe` failure:** the protected job never becomes approval-eligible
  (§4.3). The run fails closed and must be regenerated after the render bug is
  fixed; there is no cardless fallback.
- **Slack outage:** notifications degrade to GitHub Mobile push alone;
  nothing authoritative is lost.

## 9. Rollout / implementation plan

Weighted scope is small; the value is concentrated in P1–P2. **P0 is a
sequencing fix from review round 2:** the phone surface is load-bearing for
the whole design (§4.3) and was previously validated only at VG — four
phases after the code that assumes it.

| Phase | Goal | High-level tasks | Depends on | Gate | Model/effort |
|---|---|---|---|---|---|
| **P0 `phone-spike`** | **Verify the two empirical assumptions before building on them** | one throwaway workflow with a protected environment + a step summary; on the reviewer account's phone confirm (a) the deployment-review dialog accepts a typed comment, and (b) the card meets the §9.1 reachability threshold | — | **binary, blocking** — see §9.1 for the pass bar and the fallback list | ~30 min, operator + phone, no repo code |
| P1 `phrase` | Word-coded canonical comment, cutover in one commit | encoder + embedded wordlist + goldens; normalization; verification cutover; refusal-position test updates | P0 | unit gate: goldens + existing refusal suite green | opus/extra (touches verification) |
| P2 `legible-run` | Run explains itself | `describe` verb + golden card; display inputs/flags + mismatch refusal; workflow job + run-name; pinned-workflow test updates | P0, P1 | workflow assertions green; card renders on a dry dispatch **and is reachable on the P0-verified surface** | sonnet/extra |
| P3 `submit` | Agent-performed dispatch | `submit` verb (validate/dispatch/poll/`-json`); stubbed-API tests | P1 | stub suite green | sonnet/extra |
| P4 `slack-card` | Push + copyable phrase + edited terminal states | card composer on `escalate`/`flare` seam; edit-in-place on refusal vocabulary | P3 | dry-run refusal edits the card correctly | sonnet/extra |
| **VG** | **VALIDATION GATE** | one live `prepare` + `execute` canary approved **entirely from the phone**: push → read → type words, ≤60 s each, zero laptop; **plus the adversarial drill** (§11) | P0–P4 + §op setup | binary: it happened or it didn't | — |
| P5 `escort` *(gated)* | Agent sequences prepare→execute so decision 2 arrives as a follow-up push | agent-side orchestration only | VG | — | sonnet/extra |
| P6 `inbox` *(gated)* | Reviewer-device PWA (approval-ux.md design 4) | only if friction survives VG; custody rule (reviewer token never on agent box) is a hard precondition | VG | — | opus/extra |

Operator setup (non-code, before VG): hardware key as passkey + 2FA on the
reviewer account **with weaker 2FA methods removed** and a backup stored
offline; GitHub Mobile on the reviewer account with deployment-review
notifications; Slack channel reachable via existing `escalate serve`
config.

### 9.1 P0 pass criteria and fallback list

"Reachable" needs a bar, or the spike returns a judgment call (v4, from
review round 3).

- **(a) Comment slot — binary.** The mobile deployment-review dialog
  accepts a typed comment that lands as the approval's comment. No partial
  credit: if it cannot, there is no phone flow and the design stops here.
- **(b) Card reachability — the threshold is *visible on, or one tap from,
  the deployment-review screen, without leaving the approval context and
  returning*.** Two or more taps plus a back-navigate is a **fail**, even
  though the operator could reach the card in principle: the habit this
  design depends on is "read the card, then approve" in one flow, and
  navigation that breaks the flow is navigation that gets skipped under
  routine.

**If (b) fails, this is an execution task, not a restart.** Candidate
carriers below. **Rule: try them in the listed order and stop at the first
one that satisfies both the §4.3 anti-spoof property (content rendered
from the actually-dispatched document, on trusted `main`) and the (b)
threshold above** (v5, from review round 4) — this is a first-pass-wins
search, not a survey of all three. Any surface meeting both inherits the
anti-spoof property; the ordering is by how little else it disturbs:

1. **Check-run summary** on the PR head — rich markdown and visible in GitHub
   Mobile. The secretless `describe` job posts it with that run's ephemeral
   `GITHUB_TOKEN` and job-level `checks: write`; it never receives the Gate
   App private key and never mints an App token. The check name includes the
   stable request-ID suffix and remains `in_progress` while approval is
   pending, so it cannot be configured as a reusable required green context.
   It is display only: check-run *actions* remain rejected (§4.7), and the
   protected job still performs every authority check before App-token
   creation. If repository policy cannot grant this narrow permission, this
   candidate fails and P0 proceeds to candidate 2 rather than widening
   credentials.
2. **Deployment description / environment URL** — smaller, but sits
   directly on the approval screen.
3. **Pre-approval acknowledgement** — the Slack deep link resolves to the
   card first and the operator acks it before the approval screen opens.
   Weakest of the three: it puts an untrusted-transport surface earlier in
   the flow, so it is a last resort and would need §4.6 revisited.

A (b) failure also downgrades the §11 drill's meaning until a carrier is
chosen, since the drill tests the card habit.

## 10. Open questions

1. **Encoder location — RESOLVED, and the v2 leaning was wrong (v4, review
   round 3):** it goes in **`contracts/gateauthorization`**, beside the
   existing `ExpectedApprovalComment` / `ExpectedPreparationApprovalComment`.
   The "wait for a second consumer" reasoning failed because the second
   consumer already exists and is the contract itself: `ValidateReceipt`
   and `ValidatePreparationApproval` live in `contracts` and *recompute the
   expected comment* to compare against the receipt. `contracts` is a leaf
   that may import nothing else in the module, so an encoder under
   `cmd/gate/internal/` would have forced either dropping that binding
   check or duplicating it — both unacceptable. Verified against
   `contracts/gateauthorization/gateauthorization.go:392` and
   `preparation.go:130`. This is placement of *format*, not decision logic,
   so it respects the charter's leaf rule exactly as the existing expected-
   comment builders do.
2. **Normalization scope — RESOLVED (v2, review round 1):** expanded
   normalization adopted into §6.1 — Unicode hyphen family → ASCII
   hyphen, spaces accepted as word separators, whitespace collapse, ASCII
   lowercase. Binding unchanged; only the typeable surface widened. P1
   goldens encode this table.
3. **Expiry in `run-name` — resolve by P2** (it is a YAML decision made
   when display inputs and `run-name` are built, not a later refactor):
   legible but goes stale as a label; the card carries the live value.
   Leaning omit.
4. **Slack card ownership — resolve by P4** (it determines which repo the
   change lands in): agent layer composing from `submit -json` vs a
   first-class `escalate` message kind. Leaning `submit -json`, but
   recorded as a *leaning, not a resolution* — both reviewers favour it,
   and it stays open until P4 begins.

## 11. Validation plan

The gate is binary and baseline-free: **one real PR driven prepare →
execute with both approvals performed on a phone from push notifications —
no laptop, no paste, each decision ≤60 s from buzz to done.** Alongside it:
the full existing executor refusal suite passes with no semantic diffs
beyond the comment encoding and the added display-mismatch refusal, and
one deliberate wrong-phrase and one stale-head attempt both refuse with the
card states in §7. The phone canary also exercises the normalization table
against a real keyboard (v2, from review): confirm autocorrect/smart
punctuation does not silently substitute near-neighbor wordlist words or
non-ASCII hyphens that survive normalization — a failed match here is an
operability bug to fix in the table, never a security event.

**Adversarial drill (v3, from review round 2).** The happy canary proves
the flow works; it does not prove the *habit* formed. So VG also includes
one dispatch where the Slack card deliberately misstates the PR title and
intent while carrying the correct phrase for the real request. Pass
condition: the operator notices at the describe card and declines —
**the thing under test is the human flow, not gate.** Gate is not expected
to catch this and would not: a coherent lie produces no display mismatch
(§4.4), and if the underlying document is legitimate there is nothing for
verification to refuse. If the drill fails, the finding is that the card is
not actually being read at decision time, which invalidates the §4.6
"Slack can mislead at worst" posture and sends the design back to §4.3's
carrier question — not a tweak to the wording of the card.

**How the deceptive card is injected (decided, v4):** by **editing the
real card in place** after `submit` sends it. The alternatives were a
hand-composed second message (reads as a drill — the operator recognizes
the seam, so it tests nothing) and a test-only flag in card composition
(test-only code on a security-adjacent path, rejected on principle). Edit
-in-place keeps the format, the timing, and the sender identical to what a
compromised `submit` would produce, and needs no product code.

**Who performs the edit, and with what (v5, from review round 4).** A bot
message can only be edited through the Slack API with the sending app's
token, so this is an **out-of-band operator action**: a manual
`chat.update` call using the `escalate serve` Slack app credentials the
operator already holds, at drill time. Explicitly **no `--corrupt-card`
flag and no test-only branch in card composition** — that is the
test-code-on-a-security-path option already rejected above, and it would
also be a capability this spec has not authorized.

**The single-operator problem, named rather than hidden.** That mechanism
puts the edit in the hands of the same person who then approves, which
means a one-operator drill cannot be genuinely unwarned — the operator
knows the lie is coming and roughly what it says. Two honest options, in
preference order:

1. **A second person triggers it.** Anyone with the app token can run the
   `chat.update`; they need no repository access and no approval rights,
   so this borrows nobody's authority. This is the only variant that
   yields a true unwarned pass.
2. **Randomized timing, accepted at lower strength.** The operator
   pre-arranges corruption of *one* card among the next N real approvals
   without choosing which. Surprise about *which* is preserved; surprise
   about *whether* is not. A pass here must be recorded as
   *"habit fired under known-drill conditions"* — weaker than an unwarned
   pass, and **not** sufficient on its own to close VG if option 1 is
   available.

If neither is arranged, the drill has not been run — an untested habit
must not be written up as a tested one.

**Warned vs unwarned (decided, v4):** the **first run is unwarned**. A
warned drill tests whether the operator can perform the check once told to
look; only an unwarned run tests whether the habit fires unprompted, which
is the property VG is trying to establish. If the unwarned run fails,
debrief and re-run warned — but record the outcome honestly as *"habit not
yet formed, check performed under instruction"*, which is a weaker claim
and must not be written up as a VG pass. Only an unwarned pass counts.
