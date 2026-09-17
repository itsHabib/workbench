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

## What this does not say yet

kvlab is too small to separate the shapes on anything except overhead. The question that
matters is where the crossover is: how big a goal has to be before one session stops being
enough. `shoplab` (about 15 packages in three dependency layers) is the next goal.
