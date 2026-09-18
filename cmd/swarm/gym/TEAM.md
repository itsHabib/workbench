# One goal, four team shapes

`swarm gym team` gives the same goal to four shapes of team and grades what lands on
`origin main` with hidden tests. Nobody gets a task card. The goal is a spec
(`internal/gym/testdata/<goal>/SPEC.md`); the team decides how to cut it up.

| shape | what it is |
|---|---|
| solo | one session, allowed to fan out to subagents inside its own turn. This is the bar: if it wins, a team is overhead |
| flat | n equal peers. The first to look breaks the goal into units with `swarm work add`; everyone claims from the list |
| swat | two peers agree a plan, then work. The harness adds a peer while open units outnumber idle seats, read from the work list alone |
| tree | one lead who writes no product code and hands units to n-1 builders by note |

Every seat is a headless `claude -p` session in its own clone. Seats share the git remote and
the coordination store (Redis here) and nothing else.

## Run 1: kvlab (5 packages, 20 hidden tests), 2026-09-17, claude-sonnet-5

| shape | seats | hidden tests | wall | cost | turns |
|---|---|---|---|---|---|
| solo | 1 | 20/20 | 117s | $0.75 | 21 |
| flat | 3 | 20/20 | 140s | $2.04 | 94 |
| swat | 2, grew to 4 | 20/20 | 141s | $2.30 | 107 |
| tree | 1 lead + 3 builders | 20/20 | 179s | $3.94 | 181 |

One attempt per shape, so this is a reading, not a finding. Raw results are in
`runs/team-kvlab/`.

What it says:

- **A goal that fits in one session should stay in one session.** Solo was fastest and cost
  a third of the cheapest team. Every team shape paid for coordination and got nothing back,
  because five small packages never stretched one context.
- **Among teams, the lead was the most expensive seat.** The tree's lead spent $1.33 and 63
  turns writing no code. Flat and swat did the same breakdown in one peer's first few turns.
- **Growth from the work list worked.** Swat went from two seats to four within 40 seconds,
  triggered by nothing but "open units outnumber idle seats". No seat asked for help.
- **The first flat run scored 15/20 and exposed the real gap** (`flat-run1-dead-seat.json`).
  A peer claimed the last unit, said it was "waiting in the background" for its dependencies,
  and ended its turn. A headless session is gone when its turn ends. The unit stayed claimed
  by a seat that no longer existed, and the other two peers saw nothing open and stopped.

What changed because of that run:

- `swarm idle` wakes when a unit finishes, so a seat blocked on another seat's unit wakes when it lands.
- `swarm work drop` gives a unit back.
- The harness resumes a stopped seat (`claude -p --resume`) when it holds a unit or when
  `swarm idle` has something for it. This is the message-as-wake-up idea made concrete.
- The seat card says plainly that a session ends with its turn.

After those changes flat scored 20/20 and no seat needed a wake.

## Run 2: shoplab (16 packages in 8 dependency layers, 86 hidden tests, about 1,700 reference lines)

| shape | seats | hidden tests | wall | cost | turns |
|---|---|---|---|---|---|
| solo | 1 | 86/86 | 621s | $2.89 | 72 |
| swat | 2, grew to 6 | 85/86 | 465s | $8.88 | 188 |
| flat | 4 | 86/86 | 717s | $9.53 | 355 |
| tree | 1 lead + 4 builders | 85/86 | 856s | $10.59 | 406 |

One attempt per shape, all four run at once on one Mac, so wall times include contention for
the same CPU. Raw results are in `runs/team-shoplab/`.

What it says:

- **One session still did the whole thing**, for a third of what any team cost. A goal five
  times bigger did not move the crossover. For greenfield code with an exact spec, the size at
  which one session stops being enough is above 16 packages.
- **Only swat was faster than solo**, by a quarter, for three times the cost. It was the only
  team that matched seats to the work: two seats planned, four more arrived within 100
  seconds as the list filled, and nobody sat idle before there was something to claim.
- **The tree was slowest and dearest again.** The lead cost $1.73 and wrote nothing. One
  builder carried the dependency chain's tail alone (order, report, app) for the last 450
  seconds while three others had already stopped.
- **The dependency chain sets the floor, not the seat count.** Eight layers means eight
  steps that cannot overlap. Flat with four seats was slower than solo because seats waited
  on each other's layers and paid a rebase, build and test on every landing.
- **The wake path fired for real in the tree.** Three builders ended their turns early and
  were resumed by the harness when units opened or a note arrived. Without it that run stalls
  the way the first flat run did.
- **A shared decision went wrong quietly.** The swat pair agreed to put every package under
  `pkg/`. The spec names packages, not directories, so the grader now finds the layout rather
  than scoring that choice zero. A team decision nobody outside the team can see is a risk a
  single session does not have.
- Nobody used `swarm ask` in any run. The spec was exact enough that there was nothing to
  rule on. The decision ledger earns nothing on a goal like this.

## What this says about when to use a team

A team is not a faster way to build something one session can hold. Both runs say the same
thing at two sizes. The cases left where a team can win are the ones this harness does not
reach yet: a goal too large for one context, work that outlives a session, a spec that is
wrong or vague so that decisions have to be made and remembered, and a person who answers
partway through. Those are the next goals to build, not a bigger copy of this one.

## What this does not say yet

One attempt per cell. No vague or wrong spec, no goal beyond one context, no work that spans
sessions, no person in the loop. Wall times were taken with all four shapes sharing one machine.

## Run 3: a vague goal, SWAT shape, with a reconcile controller

`gym/goals/cycletime.md` is a one-paragraph brief for a machinist's cycle-time estimator
that says nothing about input, output, formulas or testing, and requires the team to record
those choices as decisions before coding. Run with `--shape swat --n 5 --reconcile`.

| seats | own suite | wall | cost | turns | units | questions |
|---|---|---|---|---|---|---|
| 2, grew to 4 | green | 353s | $4.38 | 181 | 4 | 1 |

What happened, in order: the first peer wrote the whole architecture, formulas, input format,
output and test strategy as one question with options accept or amend; the second peer ruled
it accepted within two minutes; the ruler then added four units; two more seats joined within
120 seconds as the list filled; each seat landed its package with tests; the last seat wired
the CLI, wrote the README example and a golden test. The reconcile controller, which is
harness code and not a session, built and tested every landing in a fresh clone: four green,
no nudges. Raw results in `runs/launch-cycletime/`.

This is the first run where the decision ledger did work: a vague brief produced one binding
ruling that every later seat read before claiming. It is also the first run of a standing
role as a controller rather than a seat. Reconcile cost nothing and would have sent a red
landing back to its author by note.

Not tested: a decision that turns out wrong partway through, and a landing that goes red.

## Run 4: the customer changes their mind partway

Same brief, same shape, `--reconcile`, plus `--twist` at four minutes: every live seat gets a
note and SPEC.md on origin gains a section saying jobs come as CSV from the ERP, not JSON,
that every estimate needs a per-material setup time, and that the README example must be CSV.

| seats | own suite | wall | cost | turns | units | rulings | landings verified |
|---|---|---|---|---|---|---|---|
| 2, grew to 5 | green | 689s | $12.28 | 500 | 16 | 3 | 16, all green |

Before the twist: one architecture ruling, five seats, five packages landed. After it, within
two minutes, two new rulings: one accepting CSV as primary input with JSON kept behind a flag
and naming where setup time lives, and one fixing the CSV columns. Then: a CSV loader, a
setup-minutes table, the CLI adapted twice, and a README example rewritten in CSV, all as
new units on the same list. Three seats that had stopped were woken by the harness when
units finished. The reconcile controller verified sixteen landings in fresh clones; none went
red, so the nudge path is still unexercised. Raw results in `runs/launch-cycletime/`.

Cost of the change: about $8 and six minutes on top of the first run's $4.38. The ledger
carried it: later commits cite the ruling id in their messages.

## Run 5: the same flat run with the seats inside Rooms microVMs

Run by the rooms lead on a rented box: each seat a Firecracker clone (2 GiB, 2 vCPU) started
from one snapshot, a sleeping room per seat that the runner sends turns into, the token
delivered at clone time and read by `swarm seat run` from the guest's secrets file, seats
pushing their own branches over git:// to the box, store on the box's Redis.

| shape | seats | hidden tests | wall | cost | turns |
|---|---|---|---|---|---|
| flat, local clones (run 1) | 2 | 20/20 | 180s | $1.84 | 88 |
| flat, Rooms clones | 2 | 20/20 | 230s | $2.62 | 106 |

Same result, same shape of work (one peer added all five units, the other took two). The
first attempt failed authentication in five seconds per seat with exit 3 and cost nothing: the
token file held a double paste. The grader needs Go on the runner's machine even when the
seats do not, which the harness now checks before starting. Raw results in
`runs/team-kvlab-rooms/`.

## Run 6: SWAT on shoplab with the seats inside Rooms

Same arguments as the local shoplab SWAT run, seats in Rooms clones on the rented box,
`--reconcile` on, swarm at 51476aa.

| where | seats | hidden tests | wall | cost | turns | seconds per turn |
|---|---|---|---|---|---|---|
| local clones | 2, grew to 6 | 85/86 | 465s | $8.88 | 188 | 13.1 |
| Rooms clones | 2, grew to 6 | 78/86 | 1655s | $25.65 | 616 | 14.9 |

One run each. The per-turn cost is the same to within two seconds, so the host to guest hop,
git over the network and the per-guest build cache are not where the time went. The Rooms
run took three times as many turns: nine wakes across five seats against none locally, and
the integrating package (`app`, seven of the eight failures) was marked done without passing.
Three of the local run's seat turn counts are known to be under-reported by the session
output, so the turn ratio is an upper bound. Reconcile verified fifteen landings green; the
red path is still unexercised. Raw results in `runs/team-shoplab-rooms/`.

What to take from runs 5 and 6 together: the seam works, a seat inside a microVM behaves as a
seat, and the substrate's own overhead per turn is small. Why the same team needed more turns
in guests is the open question; the per-turn prompts in `logs/` are where to look.

## Run 7: the customer lands a failing acceptance test

Run 4 again, plus `--twist-test`: with the change, the customer commits `customer/customer_test.go`
to main, which fails until a CSV example exists under testdata and the README shows CSV and setup
time. Receipts now go to the store, keyed by commit, written once.

| seats | own suite | wall | cost | turns | units | red landings | then green |
|---|---|---|---|---|---|---|---|
| 2, grew to 4 | green | 536s | $10.19 | 425 | 11 | 5 | yes, at 521s |

The customer's commit went red at 244s and every seat got the verdict by note. The next four
seat landings were red too, each rebased onto the failing test while its own unit was still
green, and each author was nudged with the failing output. The fifth landing made the
acceptance test pass and the run finished green. Four questions were asked and ruled during the
rework. Raw results and the receipt list in `runs/launch-cycletime/`.

That is the red path exercised end to end: a verdict on the store, a note to the author, a fix,
a green verdict for the fixing commit. Cost of the change with the acceptance test: about $6 and
three minutes more than the change without one.
