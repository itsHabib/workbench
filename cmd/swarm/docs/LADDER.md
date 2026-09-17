# The ladder: solo, fan-out, a lean team, a fleet

Status: a design from a six-agent panel (three independent designs, two adversarial critics,
one synthesis), 2026-09-17. It has not been tested. It argues against building several things
this directory was about to build; that disagreement is recorded here, not resolved.

## 1. Thesis

Structure follows four properties of the work: is the list known, do units share paths, does a unit outrun its parent's turn, and must a human answer mid-run. The bottom of the ladder already exists as `/parallel-work`, `/drive` and Workflow. A managing mind exists at every team size, so the real dial is builder count and builder kind. The deliverable is prose in `cmd/fleet/docs/desktop-fleet.md`, with no new runtime.

## 2. Ladder

| Rung | Belongs | Climb when | Descend when |
|---|---|---|---|
| 0. One session | Serial steps, shared files, under ~4 independent units, operator steering each turn | 4+ units written down, each with its own paths and a done-check | n/a |
| 1. Session plus fan-out (Workflow for reads, reviews and panels; `/parallel-work` waves for writes) | Known list, minute-sized units, disjoint paths, no mid-run human answer, the return value is the deliverable | Work must wait on CI, review or a long build, or must be answerable tomorrow: go to 1.5. Two or more concurrent units, each over ~30-45 min, steered individually, holding a resource for hours, or spawning long discovered children: go to 2 | The panel returns one decision, or the list drops to 2 or fewer |
| 1.5. One persistent session: pushed branch plus `/loop` (`/drive`) | Persistence without parallelism | The rung-2 conditions above | The wait ends |
| 2. Runbook Level 1: a lead plus 2-3 builders that prefer fan-outs | The rung-2 conditions | The lead is "answering builders faster than it can think", or "twenty finished branches" are waiting | A builder is pinned and pushed: archive it. The tail is known and short: one seat fans out. The theme map is written: archive the lead |
| 3. Runbook Levels 2-3 | The list is the hard part: goals arrive as thoughts, priorities move, branches need themes (46 into 8) | No higher rung. A second machine is a separate decision | The decomposition is stable and the themes are drawn |

These rung-1 problems do not justify a climb:

- **Path collisions:** run an ordered second wave.
- **A returned question:** answer it and re-dispatch that one item.
- **More than 16 units:** batch them.

Stay off fan-out when:

- Writers share a package.
- The list is only believed known.
- A product question is latent in the work.
- Units need a shared voice (Ivy lesson 4 builds on lesson 3).
- Ideas arrive during the run.
- One parent must integrate more than ~8 branches (the consolidator died at 19 of 30).
- Units need per-worktree installs and no disk check has run.

A large flat swarm is not a rung. Its only evidence is pre-decomposed disjoint work, which is rung 1 in batches.

**Dissent:**

- **Does the lead build?** Critic A keeps the lead non-building. Critic B lets it build at 3 or fewer seats. I keep the runbook as written, because a building lead's long turns queue messages.
- **Is Ivy 3-4 a useful comparison?** Critic A kept the full Ivy comparison. Critic B showed Ivy 3-4 cannot fire any growth rule. I side with B.

## 3. Growth rule

The lead sweeps every 20 minutes or on any message. The first matching rule wins, and the lead makes one change per sweep.

1. **Shrink first.** Archive any builder whose `RESULT.json` is pinned and whose `git diff --stat <head_sha> <tip>` names only that file.
2. **Freeze.** Admit nothing while any of these is open:
   - A landing is red at its pinned head.
   - There is a pin violation.
   - Free disk is under the floor (`df`).
   - Landed-but-unthemed branches exceed the limit (`git ls-remote --heads` minus the theme map).
3. **Operator load.** If stray builder messages reach the operator, or his messages per hour rise, write the missing decisions into the decided section. Add no seat.
4. **Overlap.** Intersect `git diff --name-only <base>...<tip>` across in-flight branches.
   - First overlap: write an order line in the decisions file.
   - Second overlap in the same package: name the seat with the most commits there as owner. This is one line and adds no session.
   - An exclusive resource clobbered once: use the runbook's owner plus `_queue/`. A `_queue/README.md` row unchanged for a full loop counts as a blocker.
5. **Silence.** If a tip is older than 20 minutes and the seat declared no fan-out, nudge it. If that fails, replace it from its last pushed SHA. The seat count stays the same.
6. **Capacity.** A written card has paths disjoint from every in-flight branch, and every builder is mid-unit. If the cards are short and known, an existing seat runs a fan-out wave. Otherwise admit one builder.
7. **Sub-lead.** Builder questions stay unanswered across two full loops. The lead counts this by hand. It is the weakest signal in the procedure.

Every number here is an example cadence, as the runbook already labels its own. None is a measured trigger.

## 4. Runaway guard

- No seat exists without a card. Splits add to the queue and never to headcount.
- The cap starts at 3 and may reach 5. Past 5 takes one operator request naming the card and the signal. Hitting the cap twice means re-plan.
- Fan-out agents that install dependencies count against the cap and the disk floor. A fan-out running past ~10 minutes must read its inbox between waves.
- Fan-out output lands as one integrated branch, never as N PRs.
- Log each change with its signal and value in the decisions file. Revert the change if the signal did not move.

## 5. Build

1. **Two pointer sentences.** The runbook's new "Below Level 1" paragraph points to `/parallel-work` routing. `/parallel-work` gains an "above this" line pointing to Level 1.
2. **Builder rules.** Add the stay-off-fan-out list and the fan-out cap lines to the builder rules block.
3. **Lead prompt.** Add the descend conditions and the procedure above. Make the board's decided section append-only, with fields for scope, author, reason and supersedes.
4. **Free calibration.** Run one fan-out sweep over the real day's 46 branches and 57 transcripts. Classify each branch by the four properties. The operator notes his own minutes on the next fleet day.
5. **Fleet observer, deferred.** Fold the read-only git and `RESULT.json` observer into Fleet only after the hand-run one-liner fails on a real run. Critic A would build this now.

## 6. Do not build

- A rung router or classifier.
- An autoscaler.
- A Workflow-to-swarm bridge.
- A second watcher, wake, inbox, lease or admission stack.
- Tier, owner or lead as typed runtime objects.
- Verbs on the plane.
- A per-seat model picker.
- `swarm plan` or `swarm grow`.
- The per-scope roll-up report.
- Wait-share additions to `swarm load`.
- A dollar budget in the watcher.
- A ceiling refusal in `admit.go`.
- A peer-trio prompt variant.
- Supersession as a growth signal. In the scale-tree ledger, 6 of 6 supersessions were routine chain extensions and none was a reversal.

## 7. Experiment

Use REVIEW.md experiment 1 at desktop scale. It gives 12-18 mixed tasks as a goal with no cards. The tasks include long installs, one ambiguous requirement and several possible themes.

- **Arm A:** one session running `/parallel-work` waves.
- **Arm B:** Level 1 with a lead and 2-3 builders.

Run three paired repeats with acceptance owned by a third party. Record accepted output, operator minutes and dollars.

**Kill condition:** if arm A matches arm B on accepted output with no more operator minutes in two of three pairs, delete rung 2. The ladder then becomes session, fan-out, and the runbook only at large-day scale.

Run Ivy lessons 3-4 only as a one-session-plus-fan-out check. If that passes Ivy's oracle, skip the swarm arms for that workload.

