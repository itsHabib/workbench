# P0 — falsification findings

**Date:** 2026-08-22, corrected and completed 2026-08-23
**Status:** all four items answered. Item 1's headline is corrected below —
the 72% figure does not survive re-derivation.
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

## Item 3 — The stalls. **Neither "life" nor "abandoned." The observer is keyed wrong.**

Two sessions answered this on 2026-08-23 and reached opposite verdicts —
"0 defects, life not defect" and "not life, abandoned" — from different
denominators (133 vs 81; 81 is the stale 2026-08-16 README snapshot). Both
framings are wrong, and the disagreement is itself the finding.

Measured directly against `~/dev/gate/state/log.jsonl`, without going through
parley at all — for every run that emitted an escalation, did that run later
record a judgment or an action?

| | count | share |
| --- | ---: | ---: |
| runs that parked | 342 | — |
| resolved in gate (judgment or action on the same run) | 236 | 69% |
| **never resolved on that run** | **106** | **31%** |

Of those 106, the decisive split:

| | runs | |
| --- | ---: | --- |
| a **sibling run on the same PR** reached an action | 92 | gate did decide — on a different run id |
| no run on that PR ever reached an action | 14 | across only **7 distinct PRs** |

And the 7: `roll-call#8` and `workbench#214` merged with no gate decision at
all; `roxiq#205` closed; `ivy#22`, `workbench#247`, `#249`, `#253` still open
— three of those four are current in-flight work, not abandonment.

**The defect is in the observer, not the protocol.** A trace is keyed by *run
id*; a decision is keyed by *subject*. A park on run A that run B resolves is
invisible to a run-keyed reader, which is 87% of what "stalled" was counting.
Until the observer folds runs by subject, the stalled percentage measures its
own keying and nothing about system health — so it cannot be used as evidence
for or against any plane.

The residual worth acting on is small and specific: **two PRs merged with no
recorded gate decision.** That is a hole in the authorization record, and it is
the honest version of what item 3 was reaching for.

## Item 4 — A role-day. **Affordable at 75 only under the cheap definition.**

Measured from `spend-audit` over 591 sessions, 27,697 billed messages,
2026-08-02..23. Public-rate equivalents, not an invoice — the corpus carries
no billing field, so it cannot say whether this was API-rate or absorbed by a
flat subscription.

A "role-day" has no single value; it has a 21x spread across defensible
definitions of the same unit:

| definition | n | mean $/role-day | 75 roles/month |
| --- | ---: | ---: | ---: |
| any session-day as observed (29 active min) | 700 | $9.10 | $14,327 |
| >= 60 active min ("half shift") | 100 | $40.04 | $63,057 |
| >= 120 active min ("long shift") | 32 | $62.82 | $98,937 |

**A true 8-hour agent shift does not exist in this data.** Longest session-day
ever recorded is 268 active minutes; the median is 14.

Concurrency is the one clean invariant: cost per agent-minute is flat at
$0.26-0.35 from 1 to 15 concurrent agents. No measured coordination penalty —
so linear extrapolation holds *on that axis*.

**Verdict.** Against a one-engineer budget, the affordable ceiling is ~109
roles at the observed-session-day rate and **~25 roles at the half-shift
rate**. If Baton roles do sustained work rather than fire in bursts, 25 is the
honest ceiling and the design's 75 is 3x outside it. 83% of spend is cache
mechanics, which is where any optimization has to aim.

## Correction to item 1's headline — the 72% does not survive re-derivation

The 72% figure above is not reproducible, and the reason matters more than the
number. Re-classifying the same 87 blocked actions by *which producer emitted
the block*:

| | count | share |
| --- | ---: | ---: |
| a **judge** decided to block | 56 | 64% |
| deterministic **readiness** block | 31 | 36% |

Within the 31 deterministic blocks: 24 purely transient (would clear by
waiting), 2 purely terminal, 5 mixed. So blocks that a wait would have fixed
are **24-29 of 87 — 28-33%, not 72%.**

Reaching 72-75% requires counting *judge* blocks whose prose mentions an
evidence gap as "evidence not ready." That is defensible as a description of
why the judge was called, but it is not the operational claim the split rests
on: by then a cycle has already been spent and a judgment already recorded.
Three sessions produced three numbers (72%, 75%, 33%) because the classifier
boundary was never stated, not because anyone mis-counted.

**Consequence for the split.** "Split `blocked` into no and not-yet" was
recorded below as *the highest-value single change* on the strength of the 72%.
At 28-33%, and with a separate analysis rejecting the split on termination
grounds (its backstops derive from prior artifacts for the same subject, and
CI mints state into a `mktemp -d` that is deleted on exit, so they are
structurally unreachable there), that ranking no longer holds. Fix the
observer keying and settle evidence before opening a run; revisit the terminal
vocabulary only if that leaves a real residue.

---

## What P0 has changed so far

1. Split gate's `blocked` into *no* and *not yet*, and fix the trigger, before
   arming any enforcement level. This is now the highest-value single change
   to the existing system.
2. POC-A must run live. Budget a week of instrumented sessions; do not attempt
   to reconstruct the number.
3. The authority plane's judgment survives contact with its own history, which
   is the one thing P0 could have killed and did not.
