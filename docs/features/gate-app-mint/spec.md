# gate App-mint — Technical Design Document

**Status:** draft / proposal — NOT a build commitment. The artifact we decide from.
**Owner:** @itsHabib
**Date:** 2026-07-27
**Related:** `docs/features/gate-enforcement-arming/kickoff.md` (the merge door — enforce),
`docs/features/physical-custody-tap/kickoff.md` (the mint door's physical anchor),
`cmd/gate/docs/enforcement.md` (threat model; names mint authentication as an open residual),
design capture: operator notes 2026-07-26 ("gate in the GitHub world — enforce vs. MINT").

> **Reviewers — focus areas:** §4.2 (where the minted grant lives so ENFORCE can see it —
> the load-bearing open fork), §4.3 (what counts as a human-authenticated trigger, fail-closed),
> §7.2 (the forged-trigger flow), §8 (what happens when the App key leaks).

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
GitHub-authenticated `MintedBy` bound to the exact PR head. "Minting is a human act"
becomes a property of the mechanism, not the operator's discipline.

**Non-goals (explicit):**
- **ENFORCE via App.** Branch protection on a commit status enforces exactly as well as
  an App Check Run; an App adds zero enforcement power there. Enforce stays
  `gate-enforcement-arming`'s commit-status path. (The App is wrong for job #2, right
  for job #3.)
- **EXECUTE.** `-live` merge execution stays deferred. The mint can be real before
  execute is. (The App is also the natural custodied merge identity later — job #4 —
  but that is out of scope here.)
- **Replacing local `gate grant`.** The CLI mint stays for local/manual flows; App-mint
  is the governed path on the canary.
- **custody (`cmd/custody`) integration.** Same problem shape (physical-custody-tap),
  different tool. Cross-link, don't merge the efforts.

## 2. Functional & non-functional requirements

**FR:**
1. A human-authenticated event on a PR (allowlisted identity) can mint a grant scoped
   `(repo, action=merge, tier ceiling, TTL)` and **bound to the exact PR head SHA**.
2. The grant records an authenticated `MintedBy` — the GitHub login of the triggering
   human plus the event that authorized it — not a free-form label.
3. No qualifying human event → no grant (the load-bearing property; the tap analog).
4. The mint lands in gate's **hash-chained audit** as a normal grant artifact,
   reconstructable from state alone.
5. gate's ENFORCE check can verify "this head stands on a live, valid, head-bound grant"
   before the required status goes green.
6. A grant bound to head H authorizes nothing for head H′ (force-push invalidates).

**NFR:**

| Dimension | Target |
|---|---|
| Security | Fail closed everywhere: unrecognized trigger identity, missing/expired App token, malformed grant, head mismatch → refuse, never a default-allow |
| Auditability | Every mint is one artifact in the hash-chained log + visible in the App's own GitHub audit trail; two independent trails that must corroborate |
| Latency | Mint completes within one Actions run (< ~2 min); never blocks DECIDE |
| Blast radius | App private key is the new root of trust; compromise must be bounded by installation scope (one canary repo) + grant TTL + tier ceiling |
| Cost | Zero marginal API spend (Actions minutes only; no model calls on the mint path) |
| Reversibility | Disarm = disable the mint workflow / uninstall the App; local `gate grant` still works |

## 3. Architecture overview

```
 human approves / comments "/gate authorize"        (authenticated by GitHub)
        │  (identity allowlist check, fail-closed)
        ▼
 gate-mint.yml (Actions, base context — never fork code)
        │  actions/create-github-app-token  →  short-lived App installation token
        ▼
 gate grant --bound-head <PR head SHA> --minted-by "<login>@<event-id>" ...
        │  (HMAC-signed grant artifact, head-bound, short TTL)
        ▼
 grant artifact  ──►  gate's hash-chained state        (audit)
        │
        ▼
 gate.yml ENFORCE check verifies: live grant ∧ head-bound ∧ within ceiling
        └─►  `gate` commit status green only then      (branch protection requires it)
```

**Reused:** `capability.Mint`/`Check` + the state store (the grant mechanism ships);
`gate.yml`'s fork-safe `workflow_run` pattern, SHA-binding discipline, and fail-closed
status mapping (all proven in `gate-enforcement-arming`). **New:** the App identity +
key custody, the mint workflow, a `BoundHead` field on `Grant`, and the
grant-transport seam (§4.2). Two layers of scope, named honestly: the App token
carries the *coarse* GitHub scope; the grant carries the *fine* gate vocabulary
(tier ceiling, cycles, TTL). Neither substitutes for the other.

## 4. Key decisions & trade-offs

### 4.1 App as minter identity, mint runs in Actions — no hosted server
**Choice:** the App is only an *identity*; `actions/create-github-app-token` produces
its token inside a workflow triggered by the human event. **Alternative:** a hosted
webhook server holding the App key. **Why:** the server adds an always-on attack
surface and an ops burden for zero mint-power; Actions gives us the trigger, the
runner, and the token exchange with nothing persistent. (This mirrors the enforce-side
finding: the App-as-server was pure overhead there too.)

### 4.2 Where the minted grant lives — OPEN FORK, reviewers weigh in
The mint runs on a hosted runner; gate's canonical state lives on the operator's
machine. ENFORCE (also on a hosted runner) must be able to verify the grant. Options:

- **(a) Repo-hosted grant state.** The mint workflow appends the grant artifact to a
  dedicated state tree the enforce workflow can read (e.g. an orphan `gate-state`
  branch or Actions artifact keyed by head SHA). HMAC key must then exist in CI
  (a repo secret) — key custody weakens from "operator's disk" to "repo secret";
  scope-bound by the canary.
- **(b) Grant-as-signed-status.** The mint workflow posts the grant *content* as a
  commit status / check output on the head, signed; enforce re-verifies the signature.
  No shared state tree, but invents a second artifact channel outside the hash chain —
  violates "state is the only channel" unless the mint ALSO ships the artifact home.
- **(c) Mint locally, authenticate remotely.** The human event only *authorizes*; a
  listener on the operator's machine (or the physical tap device) performs the mint
  into canonical state and pushes a status. Strongest key custody, weakest availability
  (bench must be up).

**Leaning (a)** for the canary: simplest end-to-end proof, honest about the key-custody
trade (named in §8), and (c) remains the physical-custody-tap convergence path.
This is the decision the review must lock.

### 4.3 What counts as a human-authenticated trigger
**Choice:** start with exactly two: (1) an approving PR review from an allowlisted
login; (2) a `/gate authorize` issue comment from an allowlisted login. Both arrive
with GitHub-verified actor identity; the workflow re-checks the login against a
committed allowlist (base context) and refuses anything else. **Alternative:**
`deployment_protection_rule` / environment approvals — richer, but couples minting to
deploy machinery. **Why:** the two chosen events are the ones a human already performs;
the allowlist is fail-closed; the north star (a passkey/physical tap behind the event)
slots in behind either without changing the contract.

### 4.4 Head binding is a schema change, not a convention
**Choice:** add `BoundHead string` to `capability.Grant`, covered by the HMAC pre-image.
Empty `BoundHead` = repo-scoped grant (back-compat, today's CLI mints). Non-empty =
authorizes only that head. Extending the pre-image breaks signatures on pre-extension
grants; grants are per-run and short-TTL, so migration is "mint fresh" (the documented
policy in `capability.sign`). **Alternative:** encode the head in `Repo` or `MintedBy` —
rejected: forgeable-by-convention, invisible to `Check`.

### 4.5 Authenticated MintedBy format
**Choice:** `gh:<login> via <event-kind>:<delivery-id>` written by the workflow from
GitHub-provided context (never from user-controlled text), e.g.
`gh:itsHabib via review:12345678`. It stays a string — the *authentication* comes from
the trigger mechanics + the App's audit trail corroborating it, not from parsing the
label. **Alternative:** structured `MintEvidence` sub-object — more honest but a bigger
schema bump; deferred to §10 unless reviewers want it now.

## 5. Data model

`capability.Grant` today: `Repo, Action, MaxTier, MaxCycles, ExpiresAt, MintedBy, Sig`
(HMAC-SHA256 over a fixed-position pre-image).

Changes:

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
- `Check` grows a head parameter: callers that know the head pass it; a head-bound
  grant checked against a different or absent head fails with a new coded
  `ErrHeadMismatch`. Repo-scoped grants (empty `BoundHead`) behave exactly as today.
- State layout, artifact kinds, and the hash chain are unchanged — a mint is still one
  `KindGrant` artifact appended to the log.

## 6. API contract

```
gate grant -repo <owner/repo> [-action merge] [-max-tier T] [-max-cycles N]
           [-ttl D] [-bound-head <sha>] [-minted-by <label>]
```

- `-bound-head` (new): full head SHA the grant is bound to. Refused if not a full SHA.
- `-minted-by` (new, replaces the hardcoded "operator"): defaults to `operator` for the
  local CLI path; the mint workflow passes the authenticated form (§4.5). The flag is
  labeling, not authentication — the doc must say so; authentication is the trigger.
- `capability.Check(st, keyPath, grantID, repo, action, head, now)` — head added;
  `""` means "caller has no head context" and only matches repo-scoped grants.
- Coded errors extend: `ErrHeadMismatch`, `ErrBadHead`.
- New workflow `.github/workflows/gate-mint.yml`: triggers
  `pull_request_review (submitted, approved)` + `issue_comment (created)`, base
  context only, allowlist check first step, then token mint → `gate grant` →
  grant transport per §4.2 decision. Guarded by repo var `GATE_MINT` (default off),
  mirroring `GATE_ENFORCE`'s dormant-until-armed pattern.

## 7. Key flows

### 7.1 Happy path
1. PR open on the canary; CI green; reviews land.
2. Allowlisted human approves (or comments `/gate authorize`).
3. `gate-mint.yml` fires in base context: verifies the actor against the allowlist;
   refuses drafts/forks per policy.
4. `actions/create-github-app-token` exchanges the App key (repo secret) for a
   short-lived installation token (proves the App identity; scopes the run).
5. Workflow resolves the PR head SHA **at trigger time** and mints:
   `gate grant -repo ... -bound-head <sha> -ttl 2h -minted-by "gh:<login> via review:<id>"`.
6. Grant artifact lands in the state tree (§4.2); workflow summary prints the grant ID.
7. `gate.yml` (ENFORCE) runs on the head: finds a live grant, `BoundHead` == judged
   head == target head, tier within ceiling → posts `gate=success`. Merge unblocks.

### 7.2 Forged / unauthorized trigger
- Comment `/gate authorize` from a non-allowlisted login → allowlist step refuses;
  no token exchange, no mint, workflow annotates why. **No grant.**
- A fork PR replays the comment body → base-context workflow; fork code never runs;
  actor check still governs. **No grant.**
- An agent with repo write tries to mint by pushing a workflow edit → the edit is a PR
  like any other, itself gated; `GATE_MINT` + allowlist live in the base branch.

### 7.3 Force-push after mint (TOCTOU)
- Grant bound to H; author force-pushes H′. ENFORCE evaluates H′, finds only an
  H-bound grant → `ErrHeadMismatch` → status red. Re-authorization requires a fresh
  human event on H′. (Same SHA-binding discipline the enforce side already proved.)

### 7.4 Degraded modes
- App key secret missing/rotated → token step fails → no mint; local `gate grant`
  remains the fallback. Never a silent fallback inside the workflow.
- Grant transport unavailable (per §4.2 choice) → ENFORCE sees no grant → red.
  Fail-closed is the degraded mode.

## 8. Concurrency / consistency / failure model

- **Root of trust moves:** today `grant.key` on the operator's disk; with option (a)
  the HMAC key also exists as a canary repo secret, and the App private key becomes a
  second root. Compromise bounds: App installed on the canary only; grants are
  head-bound + short-TTL + tier-capped; the hash chain makes retroactive forgery
  evident. Name this in the doc PR — it is the honest cost of the design.
- **Two audit trails must corroborate:** gate's chain says a grant exists; GitHub's
  App/Actions logs say a human event triggered that run. An artifact in one without
  the other is an incident, not noise.
- **Concurrent mints** (two approvals racing): both append; grants are idempotent in
  effect (ENFORCE needs ≥1 live matching grant). No coordination needed.
- **Chain consistency:** mints append via the existing store; no rewrites. If §4.2(a),
  the repo-hosted tree is append-only and the operator's canonical tree ingests it
  (reconcile step documented in the runbook).

## 9. Rollout / implementation plan

| Phase | Goal | High-level tasks | Depends on | Gate | ~Scope (wLOC) |
|---|---|---|---|---|---|
| P0 `ground-truth` | Re-verify anchors; write the mint-auth threat model into `enforcement.md`'s frame | confirm `capability.go` / `cmdGrant` anchors; doc the trigger allowlist + key-custody trade | — | — | ~80 (docs) |
| P1 `head-bound-grants` | Grant schema knows heads; CLI + Check enforce it | `BoundHead` + pre-image + coded errors; `Check` head param; `-bound-head`/`-minted-by` flags; pinned tests (mismatch, malformed, back-compat empty) | P0 | — | ~250 |
| **VALIDATION GATE** | P1 merged + local proof: a head-bound grant refuses a different head in a real `gate gate` run | | | **go/no-go** | |
| P2 `app-mint-canary` | The App mints for real on the canary | App registration + key custody (OPERATOR ACT); `gate-mint.yml` (allowlist → token → mint → transport per §4.2); dormant behind `GATE_MINT` | P1, §4.2 locked | operator arms | ~300 |
| P3 `enforce-integration` | ENFORCE requires a live head-bound grant | `gate.yml` verifies grant before green; dry-observe first (post, don't require) | P2 + `gate-enforcement-arming` P3 | dry-observe evidence | ~150 |
| P4 `adversarial-pass` | Skeptics-break-the-mint (house policy for gate/security changes) | forged trigger, replayed delivery, fork PR, key-leak blast radius, TOCTOU race, transport tamper | P3 | report attached | — |
| P5 `honest-close` | Docs tell the truth | `enforcement.md` mint-auth residual → "closed on canary"; runbook; what stays open (EXECUTE, custody) | P4 | — | ~60 (docs) |

Phases ≤ the validation gate are the commitment; P2+ is gated on P1 proving the
head-binding mechanism and on the §4.2 review decision.

## 10. Open questions

1. **§4.2 grant transport** — (a) repo-hosted state vs (b) signed status vs (c)
   local mint on remote authorization. Needs the review to lock. Leaning (a).
2. **Structured `MintEvidence`** vs the §4.5 string convention — take the schema bump
   now or defer?
3. **Allowlist location** — committed file in the base branch vs repo variable. A file
   is itself gated by branch protection (nice); a variable is faster to rotate.
4. **TTL for App-minted grants** — 2h strawman (covers a merge window, ~2× the App
   token's own life). Right default?
5. **Convergence with physical-custody-tap** — the tap is the strongest form of the
   §4.3 trigger (a physical act behind the GitHub event). Do we name option (c) as the
   explicit end-state now, or keep the efforts merely cross-linked?

## 11. Validation plan

The gate after P1 is binary and baseline-free: in a real `gate gate` run against a
canary PR, a grant minted `-bound-head H` **passes** for head H and **fails coded
`ErrHeadMismatch`** for head H′ — plus the standing property test suite over the
extended pre-image (no field outside the signature). For P2, the tap-analog property:
**no qualifying human event → no grant exists anywhere** (prove by exhaustively listing
state after a run with only non-qualifying events), and every minted grant's
`MintedBy` matches a corroborating GitHub event in the Actions log.
