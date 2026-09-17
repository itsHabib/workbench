# Results at scale, 2026-09-17

> **Status, 2026-09-17: development evidence, not a result.** An independent adversarial review
> ([review-2026-09-17-codex/REVIEW.md](review-2026-09-17-codex/REVIEW.md)) found the headline claim
> unsupported. Both arms ran on this substrate, so nothing here compares against the
> prompts-and-loop hierarchy that actually worked; the generator supplied the decomposition and
> handoffs a lead would author; kill condition 4 cannot fail; the scored scale runs predate the
> fix that made `check` read intent; and one run per mode gives no variance. What survives: a
> derived board, scoped rulings and wakes can carry some coordination on a pre-decomposed local
> workload. The review also reproduced ten safety defects in the substrate; see
> [KNOWN-DEFECTS.md](../KNOWN-DEFECTS.md).

The six-task runs (`RESULTS.md`) said a flat fleet does at least as well as a tree. The
question a fifty-builder day raises is whether that holds where the tree started to hurt.
These runs are five times the size: 30 generated tasks over 6 packages, so five builders
contend for every package; 8 seats; one task in five needs the exclusive fixture lease;
faults A, B and D as before, plus fault E, a task whose card says it was scoped wrong and is
really three. A consolidator merges everything into a theme at the end. Same model, same
binary (`9e206ed`) for both scored runs. The tool was named `flat` when these ran; the logs
under `runs/` keep that name.

## Verdict

**At 30 builders both modes pass every kill condition, and the lead buys nothing.** The
substrate carries the fleet either way. The tree's lead ran 8 ticks and wrote 18,729 output
tokens. One metric is better for it: its worst wait was shorter (0.9 vs 1.4 minutes). It raised median wait from 0.1 to 0.6 minutes, produced
the run's only operator request, and that request had no product question in it. Cost and
wall clock are 8% and 7% worse. The margin is modest because the substrate does the work in
both modes, which is the point: nothing is left for a rank to do.

## What scale broke first

The first 30-builder flat run (kept under `runs/scale-flat-run1-before-intent/`) landed 29
of 30 in 13 minutes and failed three ways the six-task runs never did. Each became a fix
before the scored runs.

| what broke | why six tasks hid it | fix |
|---|---|---|
| Builders that start together all run `check`, all see clear, all edit, all land; rulings arrive after the landings. Kill condition 2 failed on six files. | With two builders per file the race is narrow; with five it is certain. The board only saw committed work. | A seat's first commit declares the paths it will touch (`INTENT.json`, `swarm intend`). Contention exists from second zero. |
| The consolidator session ran out of turns at 19 of 30 merges. | Six merges fit in any budget. | `swarm consolidate` merges what merges cleanly and verifies, in ledger order, with no session; only conflicts go to a mind. |
| One builder's spawn failed in one second and the runner said nothing. | One in thirty. | The runner retries a session that never took a turn and logs why. |
| A branch rebased onto a peer whose tip later moved counted the peer's old commits as its own. | Needs a re-pin after a rebase; rare at six. | Own changes are the commits above the seat's START marker that are not inherited from a tip it is built on. |

## Kill conditions, scored runs

| condition | flat | tree |
|---|---|---|
| 1. no request unclaimed over 20 minutes | pass (max 1.4 min) | pass (max 0.9 min) |
| 2. no contended landing without a prior ruling | pass (13 contended, 0 unruled) | pass (13, 0) |
| 3. no guessed product decision | not planted at this size | not planted |
| 4. fault A flagged by the pin check | pass | pass |
| 5. flat operator requests within tree + 2 | pass (0 vs 1) | |
| tree output tokens within 1.5x flat | | pass (1.18x) |

## Numbers

| metric | flat | tree |
|---|---|---|
| seats landed | 33 of 33 | 33 of 33 |
| consolidation | 32 branches merged, tests pass | 32 merged, tests pass |
| requests (peer / lead) | 7 (7 / 0) | 13 (11 / 2) |
| minutes to ruling, median / max | 0.1 / 1.4 | 0.6 / 0.9 |
| operator requests / without a product question | 0 / 0 | 1 / 1 |
| nudges / wakes | 40 / 4 | 58 / 13 |
| sessions (builders / lead ticks / wakes) | 38 (34 / 0 / 4) | 55 (34 / 8 / 13) |
| output tokens (lead / wakes) | 131,796 (0 / 1,648) | 156,122 (18,729 / 6,810) |
| cost | $17.89 | $19.39 |
| wall clock | 15.7 min | 16.8 min |

Fault E in both modes: the seat kept one key, queued two children with `split`, admission
seated them when seats freed, all three landed, and the theme merged them. Nobody was asked.

## Reading it honestly

- The tree did not fail here. It landed everything. What it did not do is earn its cost.
- The flat run's worst wait (1.4 min) is higher than the tree's (0.9). One request sat until a
  wake carried it; a lead on a tick caps that tail. A tighter wake cadence does the same for
  less.
- One run per mode, a synthetic app, tasks that are small and alike. Real work has long
  installs, flaky tests and cards that are wrong in ways fault E does not imitate. The next
  test is a real repository.
- The biggest finding is not flat versus tree. It is that the failures at scale were all
  substrate failures (a race, a budget, a silent spawn), and all three would have hit a tree
  the same way. Fifty sessions need a better substrate before they need a better org chart.
