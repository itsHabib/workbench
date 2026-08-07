**Status**: research sketch — NOT a build commitment; the artifact we decide from
**Owner**: @mh
**Date**: 2026-08-06
**Related**: [driver-state](../driver-state/spec.md) (append-only JSONL prior art; its `actor` is already `session:<id>`, so the roster join is free); [session-orchestrator](../session-orchestrator/spec.md) (one session → N tasks — the complement of this doc); motivating evidence in roxiq `docs/qa/resume-tomorrow.md`, `docs/qa/SESSION-HANDOFF-2026-05-18-1540.md`

# session-claims — mapping live sessions to units of work

## 1. Problem & hypothesis

Many agent sessions run at once — local, cloud, worktrees — and the mapping from
*session* to *unit of work* (dossier task, Jira ticket, PR) lives only in the
operator's head. The symptom is a growing genre of hand-written state
serializations in the portfolio repos: roxiq's `resume-tomorrow.md`,
`SESSION-HANDOFF-*.md`, `session-merge-summary.md` — humans doing, in prose, the
job of a missing Observability-plane view.

**Hypothesis:** one explicit, dirt-cheap verb — run from inside the session it
describes — is enough. If claiming costs one command and the roster renders
`session → work → PRs → last activity` as one table, the handoff-doc genre
becomes a generated artifact. Deliberately opt-in: a session you didn't claim
just doesn't appear, and that's fine — the sessions worth tracking are exactly
the ones you'd otherwise write a handoff doc for.

## 2. Shape: two skills + one log + one read-only view

- **`/claim <work>`** (working name — naming is open, §6) — run inside any
  session. Appends a `claim` event for *this* session: session id, work unit,
  repo, branch, worktree, cwd — everything but `<work>` is picked up from the
  environment, so the command is one line with zero flags. `<work>` is whatever
  you'd say out loud: `ROX-142`, `dossier roll-call/v3-p0-skeleton/t3`,
  `pr 71`, or free text (`"poking at leaderboard perf"`). Re-running re-claims
  (a new event, never mutation); a PR opened later is another invocation
  (`/claim pr 73`), recorded as a `link` event on the same session.
- **`/release`** — the inverse. Appends a `release` closing this session's open
  claims. An optional trailing note lands in the release event — the generated
  replacement for `resume-tomorrow.md`.
- **State**: `claims.jsonl` beside dossier's state. Dumb appends — no hash
  chain, no locks, no leases. Advisory observability input, not authoritative
  state; deletable without ceremony.
- **Observability**: `roster` — storeless read-only reducer over `claims.jsonl`,
  joined at read time against the worktree list and driver-state/ship records
  (whose `actor` is already `session:<id>`). Owns no state, decides nothing.

### Event schema

```json
{"ts":"2026-08-06T14:02:11Z","session":"session_01Ung…","event":"claim",
 "work":{"kind":"dossier","id":"roll-call/v3-p0-skeleton/t3"},
 "repo":"itsHabib/roll-call","branch":"claude/v3-t3","worktree":"epic-jennings-081f5a"}
```

- `event`: `claim` | `link` | `release`.
- `work.kind`: `dossier` | `jira` | `pr` | `free`.
- Unreleased claims from dead sessions are stale rows the roster ages out by
  last-activity — no liveness protocol.

That is the whole model. No hooks, no instrumented choke points, no registry,
no daemon. Any field not needed by the roster's one table doesn't get added.

## 3. Non-features (the earlier draft had these — dropped on purpose)

- **No Tier 0 auto-stub rows** (`SessionStart` hook on every session).
  Every-session hooks buy a never-empty roster at the price of concurrent hook
  writes, cloud-container filesystems, and a board full of unlabeled noise.
  Opt-in means every row is one someone meant to create.
- **No Tier 1 plumbing** (`/worktree-add`, `/work-driver` auto-emitting
  claims). Maybe later, if the manual verb proves out — but the design must
  stand on the explicit verb alone.
- **No commit-trailer recovery.** Squash merges strip `Claude-Session:`
  trailers (verified on roxiq). Not worth a best-effort path.
- **No optimization or concurrency story.** No locking, no scale targets, no
  contention design — one operator typing one command occasionally.

## 4. Roster output (the one deliverable view)

```
SESSION        AGE   WORK                          BRANCH / WORKTREE        PRS        LAST
session_01Un…  2h    ROX-142 pagination phase 1    claude/search-p1 (wt)    #71,#73    4m ago
session_02Rd…  3h    dossier roll-call/v3-p0/t3    claude/v3-t3 (wt)        #9         12m ago
```

A work unit claimed by two live sessions is flagged — the collision the
operator most wants to see.

## 5. Non-goals

- No session control (spawn/kill/route). Visibility only — the overwhelm is a
  visibility problem; the existing loop handles the rest.
- No sync/locking on the claims log — append-only, single operator, reducer
  tolerates duplicates and out-of-order.
- No schema fields beyond what the roster table renders.
- Not a rebuild of /wip — roster feeds it (or becomes a section of it); they
  must not become two competing boards.

## 6. Open questions (operator)

1. Naming: `/claim` + `/release` are working names (`/track-session-task` /
   `/track-session-clear` were the first sketch — too long). Pick before build.
2. Does roster subsume /wip, or render a section /wip embeds?
3. Where exactly does `claims.jsonl` live (dossier's state dir vs. its own)?
4. Does `/release` want a `--handoff` variant that drafts the open-loops note
   from the session, or is a free-text note enough?
