# Resume — session-claims (paste into a fresh session)

Continuation prompt for picking up the session-claims work locally. Paste the block
below as your first message in a new Claude Code session in the workbench repo.
Delete this file once the work is underway (it's a handoff artifact, not spec).

---

```
I'm resuming the session-claims design work. Don't build anything until I
answer the open questions below.

Orientation:

1. Read docs/features/session-claims/spec.md — a research sketch on PR #220
   (draft, docs-only, branch claude/session-organization-tooling-ku15uv).
   It maps live agent sessions to units of work via one opt-in verb plus a
   storeless `roster` view.

2. The design is deliberately minimal — this was a decision, not an
   oversight. An earlier draft had adoption tiers (SessionStart hooks
   stubbing a row for every session, /worktree-add and /work-driver
   auto-emitting claims, commit-trailer recovery). All dropped: the operator
   wants pure opt-in, no optimization/concurrency story. Two skills
   (/claim <work>, /release — working names), one dumb claims.jsonl, one
   roster table. Don't reintroduce the tiers.

3. Known context you'd otherwise rediscover:
   - driverstate/ has append-only JSONL prior art and its actor field is
     already session:<id>, so roster joins it at read time — no need to
     extend its ledger (that placement question was resolved toward a
     separate advisory claims.jsonl).
   - session-orchestrator is the complement (one session → N tasks).
   - Motivating evidence: roxiq docs/qa/resume-tomorrow.md and
     SESSION-HANDOFF-*.md — the hand-written genre this replaces.

4. Decisions I need to make (spec §6) — ask me, don't pick:
   (a) Final skill names (/claim + /release are placeholders the operator
       hasn't blessed; /track-session-task / /track-session-clear were the
       first sketch — too long).
   (b) Does roster subsume /wip or feed a section of it?
   (c) Where claims.jsonl lives (dossier's state dir vs. its own).
   (d) Does /release grow a --handoff variant that drafts the open-loops
       note?

5. Likely next steps once named:
   - The claim/release skills + the claims append helper (tiny).
   - `roster` reader (storeless; joins claims + worktrees + driver-state).
   - Then decide whether the spec graduates from research sketch to a real
     feature spec and PR #220 leaves draft.
```
