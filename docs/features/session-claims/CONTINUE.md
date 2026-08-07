# Resume — session-claims + roll-call PR 11 (paste into a fresh session)

Continuation prompt for picking this up on another machine. Paste the block
below as your first message in a new Claude Code session. Delete this file once
the work is underway (it's a handoff artifact, not spec).

---

```
I'm resuming two threads from a prior session (2026-08-07, personal Mac).

THREAD 1 — session-claims (this repo, PR #220, branch
claude/session-organization-tooling-ku15uv):

1. docs/features/session-claims/spec.md is the design: pure opt-in, two verbs
   (/claim <work>, /release — working names, naming still open §7), one dumb
   claims.jsonl at ~/.claude/session-claims/, one storeless roster view.
   §4: liveness is DERIVED at read time (transcript mtime, PR state, aging);
   v1 polish is auto-release closure hooks (gh pr merge, dossier
   task_complete) with the invariant: hooks may only CLOSE claims, never
   create them. An earlier tiered-adoption draft was deliberately dropped —
   don't reintroduce it.
2. v0 is BUILT and dogfooded: skills/session-claims/{claim,release,roster}/
   in this repo are the source of truth — copy them to ~/.claude/skills/ on
   this machine (see skills/session-claims/README.md). The prior session
   claimed itself against PR 220 and PR roll-call#11 as the first log events.
3. Open operator decisions: final verb names; roster-vs-/wip relationship;
   log location; /release --handoff variant.

THREAD 2 — roll-call PR #11 (v3 p0: Codex Voice amendment + debrief kernel):

1. Fully driven to the merge boundary: docs folded per review direction,
   p0 LOC gate raised 1,800→1,900 by operator decision (spec §2.2 records
   why; head measures 1,899), codex cross-process revision race fixed (CAS
   under O_EXCL record.lock in save(), in-process mutex deleted, atomic
   rename stale-reclaim, Confirm artifact cleanup), all review threads
   resolved (ctx-plumbing deferred post-p0 with rationale on-thread),
   cycle-3 claude review verified all findings closed at head 64e3b4a,
   CI green. Review caps are EXHAUSTED (3/3 cycles, 5/5 requests) — do not
   request another automated review.
2. Gate state (machine-local to the personal Mac, ~/dev/gate/state):
   run_31cfc4c894af863f is parked with an operator PASS resolution already
   recorded, but grant grt_37b5def8677ba290 hit its cycle ceiling
   (grant_cycle_exceeded, cycle 5 > 3). Next step ON THE PERSONAL MAC:
   operator mints
     gate grant -repo itsHabib/roll-call -max-tier T2 -max-cycles 8 -ttl 24h \
       -state ~/dev/gate/state
   then retry gate resolve under the new grant, run the pinned merge command
   gate emits, close #9 and #10 as consolidated, record the merge.
   From another machine: the PR itself is mergeable-ready; only the gate
   custody lives on the Mac.
3. After merge, the next milestone is the p0 trial checklist
   (codex-voice-mode.md Appendix A) — operator judgment, not agent work.

Context worth knowing: session-claims, roll-call, and the maintenance plane
are one program at three time scales (live sessions / daily debrief / weekly
health). roll-call's root-scoped-work-graph.md Q1 now carries the empirical
session-metadata answer that also validates session-claims' roster signals.
```
