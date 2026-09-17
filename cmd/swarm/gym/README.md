# The gym: which model is good enough for which seat

A task is a small repository fixture, a prompt for one seat, and a grader the model never
sees: hidden tests for an author, the ledger's recorded order for a ruler, a verified merged
head for a consolidator. Nothing is graded by reading prose, and a session's own account of
itself is ignored. The gym holds itself to the rule it grades by: every grader is tested to
fail on the untouched fixture and pass on a reference solution (`internal/gym/gym_test.go`).

```sh
swarm gym list
swarm gym run --models claude-haiku-4-5-20251001,claude-sonnet-5,claude-opus-5 --reps 3 --out DIR
swarm gym table --out DIR
```

Rows land in `DIR/rows.jsonl`: task, seat, level, model, pass, the grader's detail, turns,
output tokens, cost (marked unknown when a session was killed before reporting), wall.

## First pass, 2026-09-17: the gym works; the tasks are too easy

Seven tasks, three models, one attempt each, $6.20 in all. Rows: `runs/2026-09-17-smoke/`.

| task | seat | level | haiku 4.5 | sonnet 5 | opus 5 |
|---|---|---|---|---|---|
| author-intervals | author | easy | pass · $0.17 · 51 s | pass · $0.33 · 30 s | pass · $0.58 · 25 s |
| author-lru | author | mid | pass · $0.15 · 44 s | pass · $0.37 · 29 s | pass · $0.71 · 24 s |
| author-toposort | author | mid | pass · $0.20 · 75 s | pass · $0.23 · 37 s | pass · $0.44 · 32 s |
| author-bucket | author | hard | pass · $0.13 · 66 s | pass · $0.23 · 34 s | pass · $0.42 · 25 s |
| ruler-order | ruler | mid | pass · $0.07 · 18 s | pass · $0.20 · 14 s | pass · $0.34 · 13 s |
| ruler-escalate | ruler | mid | pass · $0.07 · 23 s | pass · $0.15 · 12 s | pass · $0.33 · 12 s |
| consolidate-three | consolidator | mid | pass · $0.13 · 50 s | pass · $0.36 · 34 s | pass · $0.57 · 28 s |

**What this does and does not say.** One attempt per cell is a smoke test of the gym, not a
finding about a model; the table prints that under itself. Twenty-one passes out of
twenty-one is a ceiling: these tasks cannot separate the models, so the only signal is price
and time at equal outcome (the small model was three to five times cheaper and about twice as
slow). A gym earns its keep at the tasks where some model fails.

## What it needs next

- **Harder author work with a real contract.** Ivy's authoring contract is the natural one: a
  lesson, a starter that fails, a reference that passes, named wrong behaviors that fail, from
  an MIT source bundle Ivy already holds (6.006 for a code-and-tests oracle; 2.830J for the
  operator's own field).
- **A planner task.** Given a goal and a repository, write the task rows. Graded structurally:
  every deliverable covered, files exist, no two tasks share a file without an order, and then
  by what the plan costs downstream when cheap authors execute it.
- **A mutant-writer task.** Wrong implementations that compile and that the reference's tests
  distinguish.
- **Ruler tasks with a trap**: the obvious order is wrong, or the brief does settle what looks
  like a product question.
- Three attempts a cell before anything is called a difference.

# Capacity: how many things one agent keeps straight

```sh
swarm gym capacity --threads 2,4,8,16,32 --notes both --out DIR
```

One agent, N independent threads, messages from all of them interleaved. Each thread is a
small ledger (SET, ADD, SUB, MOVE between its two quantities) with questions scattered
through it; a question always ends the batch it arrives in, so "the current value" has one
meaning. The answer key is computed from the messages shown and nothing else
(`TestCapacityAnswerKeyMatchesVisibleMessages`). Run in two conditions: nothing but its own
context, and a notes file it may keep.

## First run, 2026-09-17, claude-sonnet-5, one run per cell

| threads | messages | questions | in its head | with notes |
|---|---|---|---|---|
| 2 | 20 | 7 | 7 of 7 | 7 of 7 |
| 4 | 40 | 14 | 14 of 14 | 14 of 14 |
| 8 | 80 | 27 | 27 of 27 | 27 of 27 |
| 16 | 160 | 48 | 48 of 48 | 48 of 48 |
| 32 | 320 | 100 | not finished when this was written | 97 of 100 |

Nothing was dropped at any size. Notes cost more (about 1.8x the turns and dollars at 16
threads) and bought nothing here.

**These numbers are a re-grade.** The harness first reported a flat 78% at every size, in
both conditions. Identical counts across conditions was the tell. Two faults, both mine: a
question could be followed by updates in the same batch, so "current" was ambiguous; and the
generator applied an update it never showed before turning a thread's last slot into a
question, so the key held values no agent could know. Both are fixed and pinned by tests; the
saved mailboxes under `runs/2026-09-17-capacity/` were re-graded from the visible messages.

**What it says.** For state that stays in the transcript, sixteen interleaved threads is not
load. This test lets the agent look back: everything it was told is still in its context, so
it measures careful bookkeeping over a growing transcript, not memory. The first errors
appear around 320 messages. The knee for this kind of load is set by transcript length, and
it is past what was run.

**What it does not say.** Real load is not recoverable from the transcript: context gets
compacted, state lives in files and other agents' heads, threads need judgment and not
arithmetic, and a lead's threads are conversations it must initiate, not questions that
arrive. The next version forces that: threads longer than the window so early state is gone,
obligations the agent must raise unprompted ("remind t3 when fuel drops below 20"), and
threads that contradict each other.
