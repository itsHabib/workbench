# Real work, and which model for which seat

Status: a design, not a result. Nothing here has run. Two gaps it closes, both named by the
independent review and by the operator: every experiment so far ran on a generated sandbox
whose task cards did the decomposition for free, and every seat ran on one model chosen
because it was cheap.

## The workload: an Ivy course from a thought, in an hour

Ivy (`~/dev/ivy`) already defines what a finished piece of course is, mechanically:

- a lesson in prose marked `**Status:** filled`;
- a runnable exercise: a starter that compiles and **fails** its tests, a reference solution
  that **passes**, and separately named wrong behaviors that **fail** (`author/verify.mjs`
  counts a mutation as caught only after real compiler success);
- a lifecycle test of the lab runner (`lab.test.mjs`);
- a plan entry that `scripts/course-plan-emit.mjs --check` accepts as `ready`.

That is an oracle the swarm did not write and cannot argue with. The pilot course
(`courses/gleam-job-service`) has two lessons built and six planned, each depending on the
one before, which also makes it a workload with real ordering.

Two runs, in this order:

1. **Extend.** Goal handed to the swarm as one paragraph: "build lessons 3 and 4 of the Gleam
   job-service course to the pilot's authoring contract." No task cards. The curriculum table
   is the only decomposition given, and it is two lines per lesson.
2. **From a thought.** Goal: one sentence naming a topic and a learner. The swarm produces a
   course map, then two ready lessons, inside a one-hour wall.

Work happens on a branch of a scratch clone of Ivy, never Ivy's main, and nothing is pushed
to Ivy without the operator's say.

## The seats, which are the unit of model choice

| seat | job | a mind or a loop | what "good" means, measurably |
|---|---|---|---|
| planner | turn the goal into a course map, task rows, files each will touch, the order | mind; runs once, and again if the goal moves | downstream rework: splits, re-asks, tasks abandoned, operator questions |
| author | write one lesson and its exercise to the contract | mind | oracle pass on first landing; turns and cost to pass |
| mutant-writer | write the named wrong behaviors the verifier must catch | mind, adversarial | mutants that compile and that a correct reference distinguishes |
| verifier | run the oracle at the pinned head, record a receipt | loop (no model) | none; it is a process |
| ruler (any peer, or a lead in tree mode) | answer a request from evidence or escalate | mind, short | rulings later superseded; escalations that carried no product question |
| consolidator | merge in ledger order, resolve conflicts | loop for clean merges, mind for conflicts | theme verifies; conflicts left unreconciled |
| operator | intent | the human | requests received; how many needed them |

## Which model for which seat

One axis at a time, everything else fixed, same goal, same seed of the plan where a plan is
reused. A seat's model is a field on its task row, so a run is a table of assignments.

| question | arms | fixed | primary measure |
|---|---|---|---|
| Does the planner need the strongest model? | planner on the top model vs the mid model | authors, rulers on the mid model | downstream rework and oracle pass rate of the lessons that plan produced |
| Can authors be cheaper? | authors on mid vs small | one plan, produced once and reused | first-landing oracle pass; cost per ready lesson |
| Do rulings need more than the small model? | rulers on small vs mid | one plan, mid authors | rulings superseded; wrong escalations |
| Is the adversary better on a different model than the author? | mutant-writer same as author vs different | one plan | mutants caught by the author's own tests vs surviving them |

Three paired repeats per question, order rotated, before any difference is called a
difference. Cost is read from the provider's own accounting per session, and a session that
was killed is recorded as unknown cost, not zero.

## Tree and flat as launch profiles

The same run is launched two ways over one store: `flat` (peers rule, nobody ticks) and
`tree` (a lead seat with a higher tier and a loop, which also owns the planner's job and the
theme). This time the lead does real work, authoring the decomposition, which is what the
first experiments took away from it. A third arm, prompts-and-loop with no substrate at all,
is the operator's existing practice and the honest baseline.

## Kill conditions, fixed now

- **The swarm did not do real work** if either lesson fails the Ivy oracle at the wall, or
  passes only after a human edits lesson, exercise or tests.
- **Flat does not replace a lead** if, against the tree arm on the same goal, it needs more
  operator interventions, or produces fewer ready lessons, in two of three pairs.
- **A cheaper model is not good enough for a seat** if its arm's oracle pass rate on first
  landing is lower in two of three pairs, whatever it saves.
- **The substrate does not earn its place** if the prompts-and-loop arm produces as many ready
  lessons with no more operator minutes in two of three pairs.
- Any run whose accounting is incomplete is inconclusive, not a pass.

The acceptance checklist is owned by someone other than the session that built the swarm.

## What has to exist first

1. The `swarm` verbs on the plane, so seats can be microVM clones as well as local worktrees.
2. A planner seat: a prompt plus `swarm plan` that writes task rows (with files and model)
   from a prose goal, in place of the sandbox generator.
3. A per-row `model`, read by whatever starts a seat.
4. The verifier's command per repository (`--verify`), which for Ivy is the three author
   checks above.
