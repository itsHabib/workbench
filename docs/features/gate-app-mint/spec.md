# gate App-mint — Technical Design Document

**Status:** draft v2 / proposal — NOT a build commitment. The artifact we decide from.
v2 folds the design-review findings from PR #143 (codex 4×P1 + 1×P2, claude full pass).
**Owner:** @itsHabib
**Date:** 2026-07-27
**Related:** `docs/features/gate-enforcement-arming/kickoff.md` (the merge door — enforce),
`docs/features/physical-custody-tap/kickoff.md` (the mint door's physical anchor),
`cmd/gate/docs/enforcement.md` (threat model; names mint authentication as an open residual),
design capture: operator notes 2026-07-26 ("gate in the GitHub world — enforce vs. MINT").

> **Reviewers — v2 focus:** §4.3's honest human-act residual (the account-vs-human finding),
> §4.2's anchor-transport requirement, §7.3's payload-SHA rule, §8's two-root asymmetry.
> The §4.2 storage fork from v1 is now LOCKED (orphan branch); say so if you disagree.

## 1. Problem & hypothesis

gate decomposes into four jobs: **DECIDE** (evaluate the exact head, emit a verdict +
hash-chained artifact — shipped), **ENFORCE** (make GitHub refuse an un-gated merge —
in flight via `gate.yml` commit status + branch protection, `gate-enforcement-arming`),
**MINT** (create the temporary scoped authority a merge runs under), and **EXECUTE**
(perform the merge as a custodied identity — deferred, `-live` is still
`merge_not_implemented`).

MINT is the open gap. Today a grant is minted by `gate grant` on the operator's machine:

- `MintedBy` is a **free-form, unauthenticated string** — `cmdGrant` hardcodes
  `"operator"` (`cmd/gate/main.go` `cmdGrant`); `enforcement.md` explicitly says to treat
  it as a label, never as authorization evidence.
- "Minting is a human act, never agent-run" is enforced by a pretool hook + discipline,
  not by mechanism.
- The signing key (`grant.key`, 32-byte HMAC) is a file on disk; mint authority == file
  read access.

**Hypothesis:** a GitHub App is the right *identity* for MINT. An App is a first-class,
scoped, auditable GitHub identity whose installation tokens are inherently temporary
(~1h) and fine-grained. Run the mint in Actions off a **human-authenticated event** using
`actions/create-github-app-token` — no hosted server — and the grant gains a real,
GitHub-authenticated `MintedBy` bound to the exact PR head.

**Honesty clause (v2, from review):** GitHub authenticates the *account* behind an
event, not that a *human interactively performed it*. In today's threat model
(`enforcement.md`: agents run with the operator's merge-capable `gh` credential), an
agent can post `/gate authorize` **as the operator** and pass any login allowlist. This
design therefore establishes *authenticated, auditable, head-bound, expiring* minting —
a large step up from a free-form string — but "minting is a human act" is only fully
closed by an authentication factor agents do not hold (§4.3, §7.2). The interim
mitigation and the end-state (physical-custody-tap) are specified below; the residual
is named, not papered over.

**Non-goals (explicit):**
- **ENFORCE via App.** Branch protection on a commit status enforces exactly as well as
  an App Check Run; an App adds zero enforcement power there. Enforce stays
  `gate-enforcement-arming`'s commit-status path.
- **EXECUTE.** `-live` merge execution stays deferred. The mint can be real before
  execute is. (The App is also the natural custodied merge identity later — out of
  scope here.)
- **Replacing local `gate grant`.** The CLI mint stays for local/manual flows; App-mint
  is the governed path on the canary.
- **custody (`cmd/custody`) integration.** Same problem shape (physical-custody-tap),
  different tool. Cross-link, don't merge the efforts.

## 2. Functional & non-functional requirements

**FR:**
1. An authenticated event on a PR from an allowlisted **approver identity** can mint a
   grant scoped `(repo, action=merge, tier ceiling, TTL)` **bound to the exact PR head
   SHA carried by the trigger event** (§7.3 — never a live API lookup).
2. The grant records an authenticated `MintedBy` — the GitHub login of the triggering
   identity plus the event that authorized it — not a free-form label.
3. No qualifying event → no grant. An event from a non-allowlisted identity, a fork, a
   plain issue, or with a SHA mismatch → no grant, with the refusal visible in the
   Actions log.
4. The mint lands in gate's **hash-chained, anchor-verified audit** as a normal grant
   artifact, reconstructable from state alone — including the keyed anchor record the
   store's `Audit` requires (§5).
5. gate's ENFORCE check can verify "this head stands on a live, valid, head-bound grant"
   before the required status goes green.
6. A grant bound to head H authorizes nothing for head H′ (force-push invalidates).

**NFR:**

| Dimension | Target |
|---|---|
| Security | Fail closed everywhere: unrecognized identity, missing/expired App token, malformed grant, head mismatch, non-PR comment, absent anchor → refuse; never default-allow |
| Auditability | Every mint = one chain artifact + a corroborating GitHub event (App token exchange + Actions run); the corroboration check is a named runbook step (§8) |
| Latency | Mint completes within one Actions run (< ~2 min); never blocks DECIDE |
| Blast radius | App installed on the canary only, **minimal enumerated permissions** (§8); grants bounded by head + TTL + tier; HMAC key custody matches App-key custody |
| Supply chain | Every action in `gate-mint.yml` pinned by full commit SHA; actionlint in CI |
| Cost | Zero marginal API spend (Actions minutes only; no model calls on the mint path) |
| Reversibility | Disarm = `GATE_MINT` off / uninstall App; rotation procedures specified (§8); local `gate grant` still works |

## 3. Architecture overview

```
 approver's PR review approval / "/gate authorize <sha>" comment   (GitHub-authenticated identity)
        │  guards: PR-attached? identity allowlisted? SHA == trigger-payload head?  (all fail-closed)
        ▼
 gate-mint.yml (Actions, base context — never fork code; actions pinned by commit SHA)
        │  actions/create-github-app-token → short-lived App installation token
        ▼
 gate grant --bound-head <payload head SHA> --minted-by "gh:<login> via <event>:<delivery-id>"
        │  (HMAC-signed grant artifact, head-bound, short TTL)
        ▼
 orphan gate-state branch (append-only; state tree + keyed anchor record)   ← the LOCKED §4.2 choice
        │
        ▼
 gate.yml ENFORCE check verifies: live grant ∧ head-bound ∧ within ceiling ∧ chain+anchor intact
        └─►  `gate` commit status green only then      (branch protection requires it)
```

**Reused:** `capability.Mint`/`Check` + the anchored state store; `gate.yml`'s fork-safe
`workflow_run` pattern, SHA-binding discipline, fail-closed status mapping.
**New:** the App identity + key custody, the mint workflow, `BoundHead` on `Grant`, the
orphan-branch state transport. Two scope layers, named honestly: the App token carries
the *coarse* GitHub permission set; the grant carries the *fine* gate vocabulary (tier
ceiling, cycles, TTL, head). Neither substitutes for the other.

## 4. Key decisions & trade-offs

### 4.1 App as minter identity, mint runs in Actions — no hosted server
**Choice:** the App is only an *identity*; `actions/create-github-app-token` produces
its token inside a workflow triggered by the qualifying event. **Alternative:** a hosted
webhook server holding the App key. **Why:** the server adds an always-on attack
surface and ops burden for zero mint-power; Actions gives the trigger, the runner, and
the token exchange with nothing persistent.

### 4.2 Grant transport — LOCKED (v2): orphan `gate-state` branch
v1 left a three-way fork; review resolved it:

- **(a) orphan `gate-state` branch — CHOSEN.** Append-only branch in the canary repo
  holding the CI-side state tree. Permanent (unlike Actions artifacts, which expire at
  ~90 days and are deletable — fatal to "reconstructable from state alone"), carries
  its own commit history, and is itself protectable (no force-push, linear history
  required). ENFORCE reads it directly on the runner.
- (b) grant-as-signed-status — REJECTED: a second artifact channel outside the hash
  chain; violates "state is the only channel."
- **(c) local mint on remote authorization — NAMED END-STATE, not the canary path.**
  The physical-custody-tap provides the human event; the same `gate-mint.yml` contract
  fires; the transport stays the orphan branch. (a) proves the mechanism at canary
  scale; the workflow contract does not change between (a) and (c). Availability on the
  operator's bench is why (c) can't be the canary proof.

**Anchor transport (v2, codex P1).** gate's store is *anchored*: `Append` maintains a
keyed anchor record under the key directory, and `Audit` reports deletion when a
non-empty `log.jsonl` lacks its anchor. Transporting the state tree alone would make
every hosted-runner audit fail. The orphan branch therefore carries the **anchor record
alongside the tree**, and the workflow provisions `anchor.key` + `grant.key` as repo
secrets restored to the key directory before any store open. This is a hard P2
requirement, pinned by an e2e that runs `gate audit` on a fresh runner clone.

**Concurrent mints (v2, codex P2).** v1 claimed racing mints "append without
coordination" — false across runners: two clones append with the same `Prev` and race
the push; rebasing the loser would break its chain. `gate-mint.yml` therefore declares
a workflow **`concurrency` group per repo** (serialize mints; `cancel-in-progress:
false`), plus a compare-and-swap push retry that re-fetches the tip and **rebuilds** the
losing entry on the new head rather than rebasing bytes.

**App write permission, named.** Pushing the branch needs the installation token to
carry `contents: write` on the canary. GitHub App permissions are repo-wide (no
branch-level scoping), so the bound is: canary-only installation + `main` protected by
the required `gate` check + the `gate-state` branch protected against force-push. §8
carries the honest consequence.

**Reconciliation runbook is a P2 precondition** (not P4 polish): ENFORCE reads the
repo-hosted tree, so the operator's canonical tree ingests *from* the branch, and the
runbook fixes that direction before the first armed mint.

### 4.3 What counts as a qualifying trigger — and the human-act residual
**Events:** exactly two: (1) an approving `pull_request_review` from an allowlisted
identity; (2) a `/gate authorize <head-sha>` `issue_comment` from an allowlisted
identity. Guards, in order, all before any token exchange:

1. **PR-attached:** `issue_comment` fires on plain issues too — first step exits unless
   `github.event.issue.pull_request != null` (review events are PR-scoped by
   definition).
2. **Identity allowlist** (fail-closed, see below).
3. **SHA discipline** (§7.3): review path uses the trigger payload's head SHA; comment
   path requires the SHA *named in the comment* to equal the PR head at mint time —
   mismatch refuses.

**The human-act residual (v2, codex P1 — the load-bearing one).** An allowlist of
GitHub logins authenticates *accounts*, and agents in this environment run with the
operator's own `gh` credential — so `operator ∈ allowlist` alone lets an agent
self-authorize by posting as the operator. Mitigation tiers, all named in the doc the
canary arms under:

- **Interim (P2 requirement):** the allowlist names a **dedicated approver identity**
  (a second GitHub account) whose credential exists only on the operator's phone/
  browser session and is **never provisioned into any agent environment, keyring, or
  CI secret**. The operator's day-to-day (agent-shared) login is deliberately NOT
  allowlisted. This converts "account ≠ human" into "account agents don't hold" —
  mechanism, not discipline, at the cost of a second account.
- **End-state:** the physical-custody-tap / passkey behind the event (§4.2 option (c)):
  the trigger only fires off an authentication factor that is physically human.
- **Residual named:** compromise of the approver account itself is not defended here
  (§7.2).

**Allowlist location (v2, decided):** a **committed file** in the base branch — changes
to it are themselves gated PRs, so the mechanism protects its own configuration — plus
a repo variable `GATE_MINT_ALLOWLIST_OVERRIDE` that, when set, *replaces* the file
entirely: the emergency rotation path that needs no PR, visible in the Actions log.

**Dismissal residual, named:** grant TTL, not live review state, governs validity after
mint. A dismissed review does not revoke an already-minted grant; it rides out its TTL
(2h strawman). Operators must not read dismiss as revoke.

### 4.4 Head binding is a schema change, not a convention
**Choice:** add `BoundHead string` to `capability.Grant`, covered by the HMAC pre-image.
Empty `BoundHead` = repo-scoped grant (back-compat, today's CLI mints). Non-empty =
authorizes only that head. Extending the pre-image breaks signatures on pre-extension
grants; grants are per-run and short-TTL, so migration is "mint fresh" (the documented
policy in `capability.sign`). **Alternative:** encode the head in `Repo` or `MintedBy` —
rejected: forgeable-by-convention, invisible to `Check`.

### 4.5 Authenticated MintedBy format
**Choice:** `gh:<login> via <event-kind>:<delivery-id>` written by the workflow from
GitHub-provided context (never from user-controlled text). It stays a string — the
authentication comes from the trigger mechanics + the App's audit trail corroborating
it, not from parsing the label. Structured `MintEvidence` is deferred until a consumer
needs structured access (review concurred); the string is verifiable from the audit log
without parsing.

## 5. Data model

`capability.Grant` today: `Repo, Action, MaxTier, MaxCycles, ExpiresAt, MintedBy, Sig`
(HMAC-SHA256 over a fixed-position pre-image).

```go
type Grant struct {
    Repo      string    `json:"repo"`
    Action    string    `json:"action"`
    MaxTier   string    `json:"max_tier"`
    MaxCycles int       `json:"max_cycles"`
    ExpiresAt time.Time `json:"expires_at"`
    BoundHead string    `json:"bound_head,omitempty"` // NEW: full 40-char SHA or empty
    MintedBy  string    `json:"minted_by"`
    Sig       string    `json:"sig"`
}
```

- `BoundHead` joins the signed pre-image at a fixed position. A malformed value
  (non-empty but not a full SHA) is refused at mint time, like `ErrBadTier`.
- `Check` grows a head parameter: a head-bound grant checked against a different or
  absent head fails with coded `ErrHeadMismatch`. Repo-scoped grants (empty
  `BoundHead`) behave exactly as today.
- State layout, artifact kinds, hash chain unchanged — a mint is one `KindGrant`
  artifact appended to the log, **with its anchor record maintained** (§4.2): the
  CI-side store is a full anchored store, not a bare log file.

## 6. API contract

```
gate grant -repo <owner/repo> [-action merge] [-max-tier T] [-max-cycles N]
           [-ttl D] [-bound-head <sha>] [-minted-by <label>]
```

- `-bound-head` (new): full head SHA the grant is bound to. Refused if not a full SHA.
- `-minted-by` (new; replaces the hardcoded "operator"): defaults to `operator` for the
  local CLI path; the mint workflow passes the authenticated form (§4.5). The flag is
  labeling, not authentication — the doc must say so; authentication is the trigger.
- `capability.Check(st, keyPath, grantID, repo, action, head, now)` — head added;
  `""` means "caller has no head context" and only matches repo-scoped grants.
- Coded errors extend: `ErrHeadMismatch`, `ErrBadHead`.
- New workflow `.github/workflows/gate-mint.yml`:
  - triggers `pull_request_review (submitted, approved)` + `issue_comment (created)`;
    base context only; the §4.3 guard order; dormant behind repo var `GATE_MINT`
    (mirroring `GATE_ENFORCE`).
  - **every action pinned by full commit SHA** (tag rebasing on an action with access
    to both secrets is the workflow's sharpest supply-chain vector); actionlint
    enforces the pin style in CI.
  - `concurrency: gate-mint-${{ github.repository }}` (§4.2 serialization).
  - **head source rule:** review path binds `github.event.pull_request.head.sha`
    (immutable payload capture); comment path binds the SHA named in the comment after
    verifying it equals the current head. The workflow never treats a live API lookup
    as the authorization subject.

## 7. Key flows

### 7.1 Happy path
1. PR open on the canary; CI green; reviews land.
2. The **approver identity** (§4.3 — not the agent-shared login) approves, or comments
   `/gate authorize <head-sha>`.
3. `gate-mint.yml` fires in base context: PR-attached guard → allowlist → SHA
   discipline. Refusals annotate the run and stop before any token exchange.
4. `actions/create-github-app-token` (SHA-pinned) exchanges the App key for a
   short-lived installation token.
5. Workflow mints with the **payload-carried** head SHA:
   `gate grant -repo … -bound-head <sha> -ttl 2h -minted-by "gh:<login> via review:<delivery-id>"`.
6. The grant artifact + updated anchor record land on the `gate-state` branch (CAS
   push, concurrency-serialized); the run summary prints the grant ID.
7. `gate.yml` (ENFORCE) runs on the head: chain+anchor verify, live grant found,
   `BoundHead` == judged head == target head, tier within ceiling → `gate=success`.

### 7.2 Forged / unauthorized trigger
- Comment or approval from a non-allowlisted login → refused; no token exchange, no
  mint. This includes the operator's own agent-shared login — by design (§4.3).
- A fork PR replays the comment body → base-context workflow; fork code never runs;
  identity check still governs. No grant.
- An agent with repo write pushes a workflow/allowlist edit → the edit is a PR like any
  other, itself gated; `GATE_MINT` + the allowlist live in the base branch.
- **Compromised approver account (named residual):** the allowlist is no stronger than
  the authentication of the accounts it names. An attacker holding the approver
  account's session mints real grants. Not defended here; the physical-custody-tap
  (§4.2 option (c)) is the intended convergence. Interim bound: canary-only scope +
  head-binding + 2h TTL + the corroboration check (§8).
- **Manual Actions re-run (named):** a user with `actions: write` re-running an old
  mint run would re-mint from a stale human event after the original grant expired.
  The workflow refuses when a grant for the same head + delivery-id already exists in
  state (live or expired) — a re-run can never extend the window the human authorized.

### 7.3 Force-push after mint (TOCTOU) — and at mint
- **After mint:** grant bound to H; author force-pushes H′; ENFORCE evaluates H′ →
  `ErrHeadMismatch` → red. Re-authorization requires a fresh qualifying event on H′.
- **At mint (v2, codex P1):** the race between the human event and the workflow's head
  resolution is closed by the §6 head-source rule — the review path binds the SHA the
  event *payload* carried (captured at delivery, immutable), and the comment path binds
  only the SHA the human *named*, verified against the current head; a force-push
  between comment and mint produces a mismatch and a refusal, never an H′-bound grant
  on H's authority.

### 7.4 Degraded modes
- App key secret missing/rotated → token step fails → no mint; local `gate grant`
  remains the fallback. Never a silent fallback inside the workflow.
- `gate-state` branch unreachable / anchor absent → ENFORCE sees no verifiable grant →
  red. Fail-closed is the degraded mode.

## 8. Roots of trust, failure model, detection

**Three secrets, two asymmetric roots (v2).**

| Root | What compromise yields | Where evidence appears |
|---|---|---|
| App private key | Mint installation tokens with the App's full permission set — **independent of grant fields**: with `contents: write`, push to unprotected branches (incl. forging `gate-state` history up to its protection rules); none of head/TTL/tier bound this | App's GitHub audit log records token exchanges — visible |
| HMAC `grant.key` (repo secret, v2) | **Forge validly-signed grants directly** — no App, no workflow, no GitHub event. Breaks the corroboration invariant silently | Nothing GitHub-side; only the chain itself |
| `anchor.key` (repo secret) | Re-anchor a tampered log | Nothing GitHub-side |

The HMAC key is therefore the *worse* leak for auditability and gets the same custody
discipline as the App key. Bounds that hold regardless: App installed on the canary
only; permission set minimal and enumerated in the registration runbook (`contents:
write` + the minimum metadata read; explicitly NO `administration`, NO
`pull_requests: write`); grants head-bound, tier-capped, 2h TTL.

**Corroboration is checked, not asserted (v2):** every chain grant must have a matching
Actions run + App token exchange. Pre-P4 this is a named runbook step (`gate audit` +
an App-audit-log query) run before each arming decision; P4's adversarial pass decides
whether it must become a scheduled check before scale-out.

**Rotation (distinct from disarm):**
- App key: regenerate in App settings → update secret → old key dead immediately.
- HMAC/anchor keys: replace secret; in-flight grants signed with the old key ride out
  their TTL (no revocation today) — the 2h TTL is the accepted soft edge; purge state
  to hard-revoke.

**Concurrency/consistency:** mints serialize via the workflow concurrency group + CAS
push (§4.2); the chain never rewrites. Two approvals still yield ≥1 live matching
grant; ENFORCE needs any one.

## 9. Rollout / implementation plan

| Phase | Goal | High-level tasks | Depends on | Gate | ~Scope (wLOC) |
|---|---|---|---|---|---|
| P0 `ground-truth` | Re-verify anchors; mint-auth threat model into `enforcement.md`'s frame | anchor check; threat model incl. the human-act residual + two-root asymmetry | — | — | ~100 (docs) |
| P1 `head-bound-grants` | Grant schema knows heads; CLI + Check enforce it | `BoundHead` + pre-image + coded errors; `Check` head param; `-bound-head`/`-minted-by` flags; pinned tests (mismatch, malformed, back-compat, pre-image property) | P0 | — | ~250 |
| **VALIDATION GATE** | P1 merged + local proof: a head-bound grant refuses a different head in a real `gate gate` run | | | **go/no-go** | |
| P2 `app-mint-canary` | The App mints for real on the canary | App registration (minimal perms enumerated) + key custody (OPERATOR ACT); **approver identity created + allowlist file**; `gate-mint.yml` (guards → SHA-pinned token action → mint → anchored orphan-branch transport, concurrency + CAS); reconciliation runbook; dormant behind `GATE_MINT` | P1 + §4.2 anchor e2e green | operator arms | ~350 |
| P3 `enforce-integration` | ENFORCE requires a live head-bound grant | `gate.yml` verifies grant + chain + anchor before green; dry-observe first | P2 + `gate-enforcement-arming` P3 | dry-observe evidence | ~150 |
| P4 `adversarial-pass` | Skeptics-break-the-mint (house policy) | forged trigger, agent-as-operator, replayed delivery + re-run, fork PR, key-leak drills per root, TOCTOU race, transport/anchor tamper, supply-chain (action pin) | P3 | report attached | — |
| P5 `honest-close` | Docs tell the truth | `enforcement.md` mint-auth residual → "closed on canary **for the approver-identity model**"; what stays open (human-act proof until the tap, EXECUTE, custody) | P4 | — | ~80 (docs) |

Phases ≤ the validation gate are the commitment; P2+ is gated on P1's proof.

## 10. Open questions

1. **Second GitHub account friction** — the approver-identity mitigation (§4.3) costs a
   real second account (phone-only session). Acceptable at canary scale? The
   alternative is accepting agent-as-operator risk until the tap ships — a worse trade,
   but the operator should confirm.
2. **TTL for App-minted grants** — 2h strawman (§8 names it as the accepted
   rotation/dismissal soft edge). Right default?
3. **Corroboration cadence post-canary** — manual runbook step now; does P4 promote it
   to a scheduled check before any second repo arms?

(Resolved in v2: transport → orphan branch; allowlist → committed file + override var;
convergence → option (c) is the named end-state; `MintEvidence` → deferred until a
structured consumer exists.)

## 11. Validation plan

The gate after P1 is binary and baseline-free: in a real `gate gate` run against a
canary PR, a grant minted `-bound-head H` **passes** for head H and **fails coded
`ErrHeadMismatch`** for head H′ — plus the standing property suite over the extended
pre-image (no field outside the signature). For P2, three proofs:
1. Tap-analog: **no qualifying event → no grant exists anywhere** (exhaustively list
   state after a run fed only non-qualifying events: non-allowlisted login, plain-issue
   comment, SHA mismatch).
2. Anchor e2e: a fresh hosted-runner clone of `gate-state` passes `gate audit` (chain
   intact, anchor verified).
3. Corroboration: every minted grant's `MintedBy` matches an Actions run + App token
   exchange in GitHub's logs.
