# P0 — falsification findings

**Date:** 2026-08-22
**Status:** items 1 and 2 run. Items 3 and 4 outstanding.
**Method:** analysis of `~/dev/gate/state/log.jsonl` (4,818 records) and 325
Claude Code session transcripts under `~/.claude/projects`.

P0 is the no-code week in [`vision.md`](vision.md) §7: four questions
answerable from data already on the machine, any one of which can invalidate a
plane. This file records what the data actually said, including where it
refused to answer.

---

## Item 1 — Classify the 87 blocked merges. **Gate passes; the finding is elsewhere.**

The vision's threshold: *if false positives exceed 20%, the authority plane
needs a redesign, not a rollout.*

All 87 `action` records with `outcome: blocked` were joined to their verdicts
and classified by the verdict's `why`:

| Count | Share | Category |
| ---: | ---: | --- |
| 63 | 72.4% | **Evidence not ready** — review panel incomplete, `completed=0`, or GitHub reporting `mergeability UNKNOWN` |
| 22 | 25.3% | **Judgment** — a substantive risk call |
| 1 | 1.1% | Review findings outstanding |
| 1 | 1.1% | `CHANGES_REQUESTED` |

Spread across 11 repositories; by tier T2 (41), T1 (22), T3 (17), T0 (7).

**The 22 judgment blocks are good.** Representative:

- *"The PR changes the review policy itself by removing cursor from the
  required panel, while the recorded panel evaluation for this head still…"*
- *"This is a substantial T2 change (+1015 lines) affecting concurrency and
  locking behavior."*
- *"…without requiring an exact triggering build, the workflow can attribute…"*

That is a verifier reasoning about real risk on real changes. **The
false-positive rate on risk judgment is low and the authority plane does not
need a redesign.** Item 1 clears its gate.

### But 72% of blocks are a scheduling bug, not a policy one

Nearly three-quarters of every block gate has ever emitted says, in effect,
*you invoked me before my evidence existed*. The change was not unsafe; the
panel had not finished or GitHub had not computed mergeability. Two readings,
both true:

- **Safety reading** — not a false positive. Declining to authorize on
  incomplete evidence is exactly correct, and a gate that passed there would
  be broken.
- **Friction reading** — entirely a false positive. The change was fine, the
  block cost a cycle, and re-running later passes.

This is the same failure already recorded once as the post-force-push race
(poll head and `mergeable_state` before invoking gate). At 72% it is not an
edge case; it is the dominant mode.

**Design consequence: `blocked` conflates "no" with "not yet."** They are
different verdicts with different remedies — one needs a fix, the other needs
a wait — and gate already has a park outcome these are not using. Splitting
them takes the apparent block rate from 35% to roughly 9% and makes every
surviving block meaningful. This lands before any enforcement level is armed,
because arming `enforce` on a verifier whose blocks are 72% timing noise is
how a plane gets switched back off in week two.

---

## Item 2 — Count the collisions. **Unanswerable retrospectively. That is the result.**

The question: *how many times did two sessions hold the same work item at
once?*

**The data does not go back 90 days.** 325 transcripts span 2026-08-02 to
2026-08-22 — a 20-day window.

Three measures, progressively tighter:

| Measure | Count | Verdict |
| --- | ---: | --- |
| Two sessions overlapping in the same directory | 226 | Meaningless — 214 are chat sessions at the portfolio root |
| Two sessions whose open windows overlap and that both mention one PR | 314 pairs / 124 PRs | Inflated — a session left open for days overlaps everything; one window was 5,378 minutes |
| Two **worker** sessions (excluding those touching ≥10 PRs) active on one PR within 15 minutes | **30 pairs / 25 PRs** | Upper bound; see below |

Inspecting the 30 by hand dissolves most of them:

- Several are one session in a `gate` worktree referencing PRs from `drive`
  and `rung` — a cross-repo sweep that fell just under the reader threshold.
- Six are three sessions in the same `/private/tmp/claude-501/…` directory
  touching the same two PRs — parallel subagents of one parent, which is one
  logical worker, not a collision.
- The remainder are two worktrees of the same repo referencing the same PRs
  within a minute, which is **indistinguishable from both having run
  `gh pr list`**.

**"Mentioned the same PR" is not "held the same work item," and no log
archaeology closes that gap** — because holding was never recorded. Which is
exactly why POC-A is specified as a live instrumented run (attach and mark
from the hook, no refusals, count for seven days) rather than a query. That
specification is now confirmed by evidence rather than assumed.

**Honest read on the ownership thesis:** it is neither supported nor refuted
by this data. The near-miss rate is non-trivial and nothing in the current
setup would have prevented a genuine collision, but the collision count itself
remains unmeasured until POC-A runs.

---

## Items 3 and 4 — outstanding

- **Read 20 of the 81 stalled parley traces.** Defect or life? Until someone
  does, "32% stalled" measures the protocol's optimism rather than the
  system's health.
- **Price a role-day.** Eight concurrent ICs, one real day, read
  `spend-audit`, multiply by 60. Do this before designing for 75 roles.

---

## What P0 has changed so far

1. Split gate's `blocked` into *no* and *not yet*, and fix the trigger, before
   arming any enforcement level. This is now the highest-value single change
   to the existing system.
2. POC-A must run live. Budget a week of instrumented sessions; do not attempt
   to reconstruct the number.
3. The authority plane's judgment survives contact with its own history, which
   is the one thing P0 could have killed and did not.
