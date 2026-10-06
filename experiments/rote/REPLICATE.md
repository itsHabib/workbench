# Prompt for another agent: replicate, break, and compare

Paste everything below the line into a fresh agent session that has this repository
(or a clone of branch `claude/agent-language-design-r259vo` of `itsHabib/workbench`)
and, for the live part, an `ANTHROPIC_API_KEY` in its environment. Nothing in it
assumes the agent has seen this conversation.

---

You are evaluating an experimental programming language called Rote, in
`experiments/rote/` of this repository. Another agent designed and built it in one
session. You designed a language for the same brief independently, so you are both the
best possible reviewer and a competitor. Be adversarial, quantitative, and fair. Work in
a worktree or a fresh clone, never on `main`.

## 1. Understand the claim before touching anything (20 minutes)

Read, in this order: `experiments/rote/README.md`, `DESIGN.md` (the semantics section
and the "what is new" section), `EXAMPLES.md`, `RESULTS.md`. Then read the code that
carries the claim: `rote/eval.py` (the tracing evaluator: where guards are recorded),
`rote/witness.py` (the replay kernel), `rote/effects.py` (static effect rows). Write
down, in your own words and before running anything, the one soundness property the
design rests on and the two things it explicitly does not guarantee. You will check
your statement against the code in step 3.

## 2. Reproduce (10 minutes)

```sh
cd experiments/rote
python3 -m pytest tests -q                 # expect 77 passed
python3 demo.py                            # the nine-episode workload, scripted oracle
python3 ordinary/agent_py.py               # the plain-Python baseline, same episodes
python3 ordinary/proxy_leak.py             # the host-language tracer leak
python3 live/run_live.py --oracle heuristic --streams 10 --episodes 24   # model-free dry run
go run ./replay-go                         # Go replayer, demo fixtures: 60 conform
go run ./replay-go -lib runs/synthetic_library.json -fixtures runs/synthetic_fixtures.json   # 192 conform
go run ./replay-go -lib runs/drift_library.json -fixtures runs/drift_fixtures.json           # 20 conform
```

Confirm or refute every number in `RESULTS.md` against what you see. Any discrepancy is
a finding; record it with the command and the output.

## 3. Try to break the central property (the important part)

The claim: if replaying a witness completes, the original script run against the same
world would have performed exactly the same effects, in the same order, with the same
arguments. The mechanism: every operation on an observation-derived value is mediated by
the evaluator, and every point where such a value is concretized to make a decision
records a guard. `tests/test_property.py` checks this on random worlds for four fixed
scripts. Go further:

- Write scripts that exercise every builtin and every syntactic form on observed
  values (`filter`, `fold`, `any`, `all`, `contains`, `keys`, `get`, `str`, `range`,
  `append`, `min`/`max`, `max_of`/`min_of`/`sum`, nested records, lists of records,
  indexing with observed indices, record keys from observations, closures over observed
  values, `return` inside loops, `and`/`or` chains, division and modulo). Run the
  replay-equivalence check from `tests/test_property.py` on them with many random worlds.
  Any divergence where the replay completed is a soundness bug. Report the script and
  the two worlds.
- Look for a concretization point that records no guard. Candidates to inspect: string
  operations, `str()` of containers, comparisons between containers of different shapes,
  `get` with an observed default, `contains` on strings, anything that reaches Python
  equality on mixed types. If you find one, show the minimal script and the divergence.
- Mutation-test the evaluator: delete one `self.guard(...)` call in `rote/eval.py` and
  confirm the property test catches it. Report which deletions it does not catch; those
  are gaps in the test, and possibly in the design.
- Check the claim that a guard failing inside the applicability prefix performs no
  action. Construct a witness whose prefix is long and whose world changes between two
  observations in that prefix (the world is single-threaded here; simulate a change by a
  world adapter that mutates on observation). What happens, and is it documented?

## 4. Check the things the language says it does not guarantee

Build worlds where every guard holds and the outcome differs anyway (the dynamics
changed). Confirm the runtime records failing evidence and does not loop. Measure how
many wasted actions the policy allows before synthesis. Then check the two policy knobs
in `rote/runtime.py` (`revalidate_sources`, `stop_after_failed_replay`) and argue, with
a constructed scenario, which default is right for a production operator and which for a
simulator.

## 5. Run the live experiment (needs a key; set a budget)

The harness is `live/run_live.py`. It runs four arms over the same random fleets and
incidents: `rote` (inferred guards), `rote_reval` (guards plus re-validation of known
sources before asking the model), `precond` (the model writes a `when` precondition and
the body is trusted), `model_each` (the model is asked every episode and shown the
library). The write-up's prediction is in `RESULTS.md` under "The next experiment".

```sh
# pilot first: 2 streams x 6 episodes, all arms, ~60 calls at most
python3 live/run_live.py --oracle live --streams 2 --episodes 6 --max-calls 80 --name pilot
# then the real run; ~900 calls is roughly $10-15 at Opus 5.5 list prices
python3 live/run_live.py --oracle live --streams 10 --episodes 24 --max-calls 900
```

Read `live/results/<name>/summary.md` and `transcripts.jsonl`. Report: the learning
curves (oracle calls per episode index per arm), success rates, disruptions and
collateral outages per arm, token cost, and three model-written scripts with the guards
Rote inferred from them. Say explicitly whether the prediction held. The two outcomes
that would count against the idea are stated in `RESULTS.md`; check both.

## 6. Compare with your own design

Implement the nine-episode workload from `demo.py` (the world is `worlds/fleet.py`, the
incidents are listed in `RESULTS.md`) in your own language or system, with the same
scripted proposals translated faithfully, and fill in the same table: oracle calls,
static refusals, witnesses or skills rejected without acting, wasted actions, healthy
services disrupted, services left down although healthy before. Then do the same for the
six-cause harness in `live/` if your design can plug into `live/arms.py` as a fifth arm
(the interface is `achieve(world, service) -> EpisodeResult`). Where your design does
better, say what mechanism is responsible. Where Rote does better, say the same.

## 7. Report

Write `experiments/rote/REVIEW.md` (do not edit the other files) with: a verdict table
(each claim in `RESULTS.md`: confirmed / refuted / not tested, with evidence), every
soundness bug found with a minimal reproduction, the live-experiment results and whether
the prediction held, the head-to-head with your design, and the single next experiment
you would run. Keep it under 1,500 words; put raw outputs in `experiments/rote/review/`.
Do not fix bugs in Rote's code; report them. Do not create a pull request.
