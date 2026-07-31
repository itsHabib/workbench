# Gate approval UX — Technical Design Document

**Status:** draft / proposal — NOT a build commitment. The artifact we decide from.
**Owner:** @itsHabib
**Date:** 2026-07-31
**Related:** `docs/features/trusted-gate-judgment-bridge/design.md` (the security
contract this must not move), `docs/features/trusted-gate-judgment-bridge/approval-ux.md`
(the design-space survey this TDD commits a slice of), `cmd/gate/docs/enforcement.md`.

> **Reviewers — focus areas:** §4.1 (word-phrase binding strength — is 44 bits
> + prefix an acceptable attention proof?), §4.2 (agent-performed dispatch —
> is "dispatch grants nothing" airtight?), §7.3–§7.5 (refusal flows), §8
> (races). This is a design review, not a code review.

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
four words → done" while every byte gate verifies stays the same, except
one canonical encoding.

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
`<operation> <pr-number> <w1>-<w2>-<w3>-<w4>`, e.g.
`prepare 182 mango-harbor-violet-inlet` — four words = the first 44 bits of
the request's *existing* canonical semantic digest, indexed into a vendored
2048-word list (the BIP-39 English list as embedded data; no BIP-39
library, no checksum semantics — just 2048 well-reviewed, distinct,
phone-typeable words). Verification stays an exact-match comparison in the
same pre-credential slot.

**Alternatives:** (a) keep the full hex paste — maximally collision-proof,
but it is the UX being removed, and it proves clipboard possession, not
attention; (b) drop the comment entirely and rely on run-specific approval
— rejected: loses the only proof the approver engaged with *this* request
rather than blind-approving a pending run.

**The honest trade-off:** 44 bits is not a cryptographic commitment. It
does not need to be: the phrase is an attention proof inside a window that
is already run-specific, replay-identified, 20-minute-bounded, and
approver-restricted. The threat is "approver approves A believing it is
B"; the `<operation> <pr-number>` prefix makes the typing act assert both,
and 44 bits makes engineered collision between concurrently-live requests
for the same PR and operation infeasible at this system's scale. The full
256-bit digest still rides in the request JSON, authorization, and claim,
unchanged. **Reviewers: challenge this paragraph.**

To be explicit about layering (v2, from review): the phrase check and the
full-document verification are **parallel defenses in the same
pre-credential block, not sequential**. A phrase-only match authorizes
nothing — gate independently re-verifies every field of the request
document (repo, PR, head, base, merge-base, action hash, argv, expiry,
replay ID) against live GitHub and hosted state, so even an (infeasible)
phrase collision cannot carry a wrong document past verification. The
phrase is the attention gate; the document verification is the correctness
gate.

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
  residuals.
- **Document transport.** The request JSON travels as a
  `workflow_dispatch` input — exactly as it does today when pasted by
  hand; `submit` changes who types, not the channel. The documents are
  bounded by construction (IDs, SHAs, argv, and digests — the evidence
  digest, never the evidence or diff bodies), so GitHub's ~64 KB dispatch
  payload bound is not a practical constraint; `submit` still checks the
  encoded size and refuses locally with a clear error rather than letting
  the API reject it.

### 4.3 Unprivileged `describe` job, not a gate on approval

**Choice:** the card-rendering job has read-only permissions, no secrets,
no environment — and the protected job does **not** `needs:` it.

**Why:** a describe failure must not be able to park an approval, and an
approval must never skip verification anyway. The card is the anti-spoof
anchor (derived on trusted `main` from the actually-dispatched JSON), so
whatever a Slack message claims, the page the operator approves on shows
the truth. **Alternative:** make approval depend on describe — couples
authority to a display job, inverting the trust direction. Rejected.

### 4.4 Display inputs are claims that gate falsifies

**Choice:** `run-name` is built from display-only dispatch inputs
(`display-operation`, `display-pr`, `display-head`); `prepare`/`execute`
gain matching flags and refuse pre-credential if any non-empty display
value disagrees with the verified document. A mislabeled run can exist as a
label but can never authorize anything. **Alternative:** parse the JSON in
workflow expressions — not expressible in `run-name` context; and trusting
labels without falsification is how display lies become approvals.

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
  `cmd/gate/internal/`, byte-hash pinned by a golden test (a changed list
  silently changes every phrase — the test makes that loud).
- **Workflow display inputs**: exist only in the dispatch payload and
  run-name; never stored, never trusted (§4.4).

## 6. API contract

### 6.1 Phrase encoder (pure, `cmd/gate/internal/`)

```go
// ApprovalPhrase derives the canonical approval comment for a request.
// digest: the request's canonical semantic digest (existing bytes).
// op: "prepare" | "execute". pr: the request's PR number.
// Words: digest's first 44 bits, big-endian, as four 11-bit indices into
// the embedded 2048-word list. Errors on unknown op or pr < 1. Never panics.
func ApprovalPhrase(digest [32]byte, op string, pr int) (string, error)
```

Comment verification normalizes the received comment, then requires exact
equality with `ApprovalPhrase(...)` recomputed from the verified document.
Normalization (resolved from §10.2, v2 — phone keyboards specifically
attack hyphens with smart-punctuation substitution):

1. map the Unicode hyphen family (U+2010–U+2014, U+2212, U+FE58, U+FE63,
   U+FF0D) to ASCII `-`;
2. accept `-` or a single space as the separator between the four words
   (`mango harbor violet inlet` and `mango-harbor-violet-inlet` both
   pass);
3. trim, collapse internal whitespace runs to one space;
4. lowercase ASCII.

Word identity, word order, operation, and PR number stay exact — the
normalization widens only the typeable surface, never the binding.

### 6.2 `gate executor submit`

```
gate executor submit -request <emitted-request.json> [-workflow gate-executor.yml] [-json]
```

Validates the document (schema, expiry unexpired), derives display inputs
and phrase, POSTs the workflow dispatch, then polls the run list (bounded,
~60 s) for a post-dispatch run whose run-name matches its display inputs.
Prints `run_url`, `phrase`, `pr/head/base`, `expires` (text and `-json`).
Poll miss: non-zero exit that *says the dispatch happened* — degrades to
the Actions tab, never a silent miss. Holds no keys, touches no
`gate-state`, mints nothing.

### 6.3 `gate executor describe`

```
gate executor describe -request <file>
```

Read-only; validates shape and prints the Markdown decision card
(operation, PR + title, head short+full, base, merge-base, expiry, replay
ID, exact phrase) for `$GITHUB_STEP_SUMMARY`. Malformed input → a card that
says *malformed request* + non-zero exit — visible, not silent.

### 6.4 Changed surfaces

- `prepare`/`execute` verbs: new `-display-operation`, `-display-pr`,
  `-display-head` flags; non-empty mismatch with the document → refusal
  (pre-credential). Empty display flags stay accepted, so raw Actions-UI
  dispatches remain valid.
- `gate-executor.yml`: three optional display inputs; `run-name` from
  them; new first job `describe` (`permissions: contents: read`, no
  environment, no secrets, no `gate-state` checkout, checks out the same
  pinned `github.sha`, builds gate, runs §6.3).
- Error model: everything is gate's existing refusal vocabulary; the only
  new reason is the display mismatch.

## 7. Key flows

### 7.1 Prepare (happy path)

1. Agent: `gate executor request` → document + digest.
2. Agent: `submit` → dispatch → run URL + phrase; posts Slack card.
3. `describe` job renders the card on the run page; protected job waits.
4. Phone push (GitHub Mobile deployment review). Operator reads the card,
   approves, types `prepare 182 mango-harbor-violet-inlet`.
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
  polling, run-name matching alone is ambiguous — the tie-breaker is the
  **newest-created run at or after this submit's dispatch time** (v2, from
  review). A wrong pick is harmless (both runs carry the same document and
  the loser refuses); the tie-breaker exists so the printed URL points at
  the surviving run.
- **`describe` failure:** independent of approval by construction (§4.3);
  worst case is a run without a pretty card, which degrades to today's UX.
- **Slack outage:** notifications degrade to GitHub Mobile push alone;
  nothing authoritative is lost.

## 9. Rollout / implementation plan

Weighted scope is small; the value is concentrated in P1–P2.

| Phase | Goal | High-level tasks | Depends on | Gate | Model/effort |
|---|---|---|---|---|---|
| P1 `phrase` | Word-coded canonical comment, cutover in one commit | encoder + embedded wordlist + goldens; normalization; verification cutover; refusal-position test updates | — | unit gate: goldens + existing refusal suite green | opus/extra (touches verification) |
| P2 `legible-run` | Run explains itself | `describe` verb + golden card; display inputs/flags + mismatch refusal; workflow job + run-name; pinned-workflow test updates | P1 | workflow assertions green; card renders on a dry dispatch | sonnet/extra |
| P3 `submit` | Agent-performed dispatch | `submit` verb (validate/dispatch/poll/`-json`); stubbed-API tests | P1 | stub suite green | sonnet/extra |
| P4 `slack-card` | Push + copyable phrase + edited terminal states | card composer on `escalate`/`flare` seam; edit-in-place on refusal vocabulary | P3 | dry-run refusal edits the card correctly | sonnet/extra |
| **VG** | **VALIDATION GATE** | one live `prepare` + `execute` canary approved **entirely from the phone**: push → read → type words, ≤60 s each, zero laptop | P1–P4 + §op setup | binary: it happened or it didn't | — |
| P5 `escort` *(gated)* | Agent sequences prepare→execute so decision 2 arrives as a follow-up push | agent-side orchestration only | VG | — | sonnet/extra |
| P6 `inbox` *(gated)* | Reviewer-device PWA (approval-ux.md design 4) | only if friction survives VG; custody rule (reviewer token never on agent box) is a hard precondition | VG | — | opus/extra |

Operator setup (non-code, before VG): hardware key as passkey + 2FA on the
reviewer account **with weaker 2FA methods removed** and a backup stored
offline; GitHub Mobile on the reviewer account with deployment-review
notifications; Slack channel reachable via existing `escalate serve`
config.

## 10. Open questions

1. **Encoder location:** `cmd/gate/internal/` (only gate computes it) vs
   `contracts/gateauthorization` (if another tool ever needs to *render*
   phrases). Leaning internal until a second consumer exists — lazy
   migration per repo charter.
2. **Normalization scope — RESOLVED (v2, review round 1):** expanded
   normalization adopted into §6.1 — Unicode hyphen family → ASCII
   hyphen, spaces accepted as word separators, whitespace collapse, ASCII
   lowercase. Binding unchanged; only the typeable surface widened. P1
   goldens encode this table.
3. **Expiry in `run-name`:** legible but goes stale as a label; the card
   carries the live value. Leaning omit.
4. **Slack card ownership:** agent layer composing from `submit -json`
   vs a first-class `escalate` message kind. Affects which repo the P4
   change lands in.

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
