**Status**: research sketch — NOT a build commitment; the artifact we decide from
**Owner**: @mh
**Date**: 2026-08-06
**Related**: locked [driver-state](../driver-state/spec.md) plane (append-only JSONL ledger + reducer, actor/lease vocabulary); [session-orchestrator](../session-orchestrator/spec.md) (drives N tasks from one session — the complement of this doc, which maps N sessions to their work); motivating evidence in roxiq `docs/qa/resume-tomorrow.md`, `docs/qa/SESSION-HANDOFF-2026-05-18-1540.md`

# session-claims — mapping live sessions to units of work

## 1. Problem & hypothesis

Many agent sessions run at once — local, cloud, worktrees — and the mapping from
*session* to *unit of work* (dossier task, Jira ticket, PR) lives only in the
operator's head. The symptom is a growing genre of hand-written state
serializations in the portfolio repos: roxiq's `resume-tomorrow.md`,
`SESSION-HANDOFF-*.md`, `session-merge-summary.md`. Every one is a human doing,
in prose, the job of a missing Observability-plane view.

The session is the one entity the workbench does not model. dossier owns *what
needs doing*, worktrees own *where*, ship/driver-state own *runs and their
transitions* — but a session has no identity in any store, so nothing can answer
"what are my open sessions doing, and which work does each one own?" Sessions
routinely hold multiple PRs; that mapping is head-state too.

**Hypothesis:** if every session gets a row by default (hook-emitted, zero
discipline) and the row is *enriched* — never *created* — by opt-in claims and
by events emitted from choke points that already know the answer, then a
storeless roster view can render session → work → PRs → last-activity, and the
handoff-doc genre becomes a generated artifact.

## 2. Shape: one append-only log + one read-only view

Two pieces, split along the plane boundaries:

- **State**: a claims log — append-only JSONL events. See §5 for the placement
  question (own file vs. driver-state ledger extension).
- **Observability**: `roster` — a storeless read-only reducer over the log plus
  worktree list and driver-state/ship run records; prints
  `session → work → PRs → last activity` as one table. Sibling of console and
  /wip; owns no state, decides nothing.

### Event schema

```json
{"ts":"2026-08-06T14:02:11Z","session":"session_01Ung…","event":"claim",
 "work":{"kind":"dossier","id":"roll-call/v3-p0-skeleton/t3"},
 "repo":"itsHabib/roll-call","branch":"claude/v3-t3","worktree":"epic-jennings-081f5a"}
```

- `event`: `claim` | `link` | `release`.
- `work.kind`: `dossier` | `jira` | `pr` | `free` (free-text label when no
  tracked unit exists — don't force ceremony on exploratory sessions).
- **Sessions with multiple PRs**: PRs are `link` events, appended as each PR is
  opened — never fields on the claim. A session accretes links over its life;
  the reducer groups them. A session that picks up a second task appends a
  second `claim` — no mutation, ever.
- `release` closes a claim (task done, session handed off). Unreleased claims
  from dead sessions are stale rows the roster ages out by last-activity — no
  liveness protocol. (If the log lands inside driver-state, its existing
  expiry-based lease semantics are the same idea already built.)

That is the whole model. No registry, no daemon, no session manager. Any field
not needed by the roster's one table doesn't get added.

## 3. Adoption — the actual hard part

Representation is easy; the design lives or dies on whether claims exist without
discipline. Three tiers, cheapest first, each independent:

### Tier 0 — free rows (hooks, zero behavior change)

A `SessionStart` hook appends a stub claim: session id, cwd, branch, worktree,
`work.kind: "free"`. Costs nothing, requires remembering nothing, and
guarantees the roster is never empty — every session shows up, just possibly
unlabeled. Opt-in only *upgrades* a row, never creates it. This inverts the
adoption problem: the default is participation.

### Tier 1 — instrumented choke points (adoption via plumbing)

Emit claim/link events from the skills that already sit at the moments where
identity is known — one append each, no new operator behavior:

- **/worktree-add** knows the task the worktree is for → `claim`.
- **/work-driver** knows the dossier task at dispatch → `claim`; knows the PR
  when the driver lands it → `link`. (In `--engine session` mode the
  orchestrator session claims the parent phase; delegated subagent sessions
  claim their task — which also gives session-orchestrator its stream map for
  free.)
- **driver-state ledger / ship record step** already persist run + PR →
  roster reads those directly rather than duplicating `link` events (§5).
- **/pr-risk**, **/review-coordinator** run in a session already pointed at a
  PR → `link` if none exists.

This is where multi-PR sessions get covered for free: every PR-producing path
already flows through the driver or ship.

### Tier 2 — explicit `/claim` (the only opt-in)

`/claim ROX-142` · `/claim dossier roll-call/v3-p0-skeleton/t3` · `/claim
"poking at leaderboard perf"`. Enriches the Tier-0 stub with the real unit of
work. The only tier that asks the operator for anything, and it's for exactly
the sessions Tiers 0–1 can't label: ad-hoc sessions started outside the driver
loop. `/release` is its inverse; a handoff variant dumps open loops into the
release event, replacing the hand-written `resume-tomorrow.md` genre with a
generated one.

### Recovery — the retroactive edge (best-effort only)

Commits made under the harness carry a `Claude-Session:` trailer, so PR →
session is sometimes recoverable from git history with zero adoption cost.
**But squash merges strip trailers** (verified on roxiq: merged commits retain
`Co-authored-by` only), so this is a backfill/repair path, not the mechanism.
The authoritative PR↔session edge is Tier 1.

## 4. Roster output (the one deliverable view)

```
SESSION        AGE   WORK                          BRANCH / WORKTREE        PRS        LAST
session_01Un…  2h    ROX-142 pagination phase 1    claude/search-p1 (wt)    #71,#73    4m ago
session_09Kq…  1d    free: "leaderboard perf?"     main                     —          19h ago  (stale?)
session_02Rd…  3h    dossier roll-call/v3-p0/t3    claude/v3-t3 (wt)        #9         12m ago
```

Grouping is by session; a work unit claimed by two live sessions is flagged —
that's the collision the operator most wants to see.

## 5. Placement — the load-bearing open question

driver-state already is an append-only JSONL event ledger with a validating
reducer, hash-chained appends, actor identity, and expiry-based leases — and
`Claim`/`Release` are literally its verbs. Two candidate placements:

**(a) Extend driver-state.** Session claims become event kinds in the existing
ledger; the session id is the `actor`; roster is another reader like /wip.
Pros: one substrate, one write mechanism, the Windows/locking lessons already
paid for. Cons: driver-state's vocabulary is deliberately "exactly the driver
lifecycle we run" — session claims are *about* sessions, not runs, and many
claimed sessions (Tier 0 stubs, exploratory `/claim free`) never touch a run.
Widening a locked plane's vocabulary for non-run events is exactly the scope
creep its non-goals warn about.

**(b) Own tiny log beside dossier.** A separate `claims.jsonl`, dumb appends
(no hash chain — nothing authoritative depends on it), roster joins it against
driver-state/ship at read time. Pros: claims are "what's being worked", which
is dossier's domain; the log stays deletable without touching an authoritative
ledger. Cons: a second JSONL substrate with slightly different rules.

Lean: **(b)** — claims are advisory observability input, not authoritative
state; they don't deserve (or want) the ledger's guarantees. But this is an
operator call, and (a) should be argued for if driver-state's owners see
session identity as a natural `actor` extension.

## 6. Non-goals

- No session control (spawn/kill/route). Visibility only — the overwhelm is a
  visibility problem; the existing loop handles the rest.
- No sync/locking on the claims log — append-only, single operator, reducer
  tolerates duplicates and out-of-order.
- No schema fields beyond what the roster table renders.
- Not a rebuild of /wip — roster feeds it (or becomes it); they must not become
  two competing boards.

## 7. Open questions (operator)

1. Placement §5: extend driver-state or own log beside dossier?
2. Does roster subsume /wip, or render a section /wip embeds?
3. Cloud sessions: enumerate via the CCR session list at roster time, or rely
   on Tier 0 hooks firing in cloud containers too?
4. Which skill grows the Tier-1 appends first — /worktree-add (smallest) or
   /work-driver (highest value)?
