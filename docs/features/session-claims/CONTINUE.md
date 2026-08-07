# Resume — session-claims (paste into a fresh session)

Continuation prompt for picking up the session-claims work locally. Paste the block
below as your first message in a new Claude Code session in the workbench repo.
Delete this file once the work is underway (it's a handoff artifact, not spec).

---

```
I'm resuming the session-claims design work. Don't build anything until I
answer the placement question below.

Orientation:

1. Read docs/features/session-claims/spec.md — a research sketch on PR #220
   (draft, docs-only, no reviews yet, branch
   claude/session-organization-tooling-ku15uv). It proposes mapping live
   agent sessions to units of work (dossier task / Jira ticket / PRs) via an
   append-only claims log plus a storeless `roster` view.

2. The design's center is adoption, not representation:
   - Tier 0: SessionStart hook emits a stub row for every session (roster
     never empty; opt-in only upgrades rows).
   - Tier 1: claim/link appends from choke points that already know the
     answer — /worktree-add, /work-driver, driver-state/ship records.
   - Tier 2: /claim + /release, the only explicit opt-in.
   - Claude-Session commit trailers are backfill only — squash merges strip
     them (verified on roxiq).

3. Known context you'd otherwise rediscover:
   - driverstate/ already has append-only JSONL ledgers, a validating
     reducer, hash-chained appends, and Claim/Release lease verbs — spec §5
     weighs extending it vs. a separate advisory claims.jsonl beside dossier
     (the doc leans separate: claims are observability input, not
     authoritative state).
   - session-orchestrator is the complement (one session → N tasks); this is
     N sessions → their work. Tier 1 gives the orchestrator its stream map
     for free.
   - Motivating evidence: roxiq docs/qa/resume-tomorrow.md and
     SESSION-HANDOFF-*.md — the hand-written genre this replaces.

4. Decisions I need to make (spec §5 + §7) — ask me, don't pick:
   (a) Placement: extend driver-state's ledger, or advisory claims.jsonl
       beside dossier?
   (b) Does roster subsume /wip or feed it?
   (c) Cloud sessions: CCR list at roster time, or Tier 0 hooks in cloud
       containers?
   (d) First Tier-1 instrumentation: /worktree-add (smallest) or
       /work-driver (highest value)?

5. Likely next steps once (a) is answered, in order:
   - Tier 0 SessionStart hook + the claims append helper (tiny).
   - `roster` reader (storeless; joins claims + worktrees + driver-state).
   - /claim + /release skills.
   - Then decide whether the spec graduates from research sketch to a real
     feature spec and PR #220 leaves draft.
```
