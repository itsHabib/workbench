# Results

All numbers come from `python3 demo.py`, `python3 ordinary/agent_py.py`,
`python3 ordinary/proxy_leak.py` and `python3 -m pytest tests -q`, run on 2026-10-06.
Every proposal is a **scripted model response** from `scenario/proposals.json` (and its
Python translation in `ordinary/agent_py.py`). No model was called. The live oracle
adapter (`rote/synth.py:LiveOracle`) is unexecuted and present only to make the seam
concrete.

## The workload

A simulated fleet (`worlds/fleet.py`): hosts with disks, services with a port, a status
and a log. A service can be down because it crashed, because its host's disk is full,
or because its port collides with a running neighbor's. `restart` succeeds only when
the real cause is gone. A configuration can drift from `{"port": n}` to
`{"listen": {"port": n}}`. `reboot_host` restarts every service on a host, taking
healthy ones down and back up (counted as a disruption); `wipe_host` exists in the
signature and is outside the grant.

Nine episodes, in order, on one evolving world. The same incidents, the same proposals
in the same order, and the same dispatch policy (no-failure witnesses first, newest
first) for Rote and for the baseline.

| Episode | Incident | Call |
|---|---|---|
| E1 | web crashes | heal(web) |
| E2 | api crashes (same symptom, other service) | heal(api) |
| E3 | db's host runs out of disk | heal(db) |
| E4 | api collides with web's port (web healthy, same host) | heal(api) |
| E5 | cache crashes | heal(cache) |
| E6 | db's config schema drifts, then collides with cache's port | heal(db) |
| E7 | new host h3: full disk (s1), crash (s3), healthy (s2) | recover_host(h3) |
| E8 | new host h4: drifted-schema collision (t1), crash (t3) | recover_host(h4) |
| E9 | replay-only policy; db loses its port configuration | heal(db) |

## Rote

```
episode call                outcome            oracle refused inappl  acts wasted disrupt
E1      heal(web)           synthesized             1       0      0     1      0       0
E2      heal(api)           replayed                0       0      0     1      0       0
E3      heal(db)            synthesized             1       0      0     3      1       0
E4      heal(api)           synthesized             3       2      1     3      1       0
E5      heal(cache)         replayed                0       0      2     1      0       0
E6      heal(db)            synthesized             1       0      2     3      1       0
E7      recover_host(h3)    synthesized             1       0      0     3      0       0
E8      recover_host(h4)    replayed                0       0      0     3      0       0
E9      heal(db)            parked                  0       0      3     1      1       0
```

`oracle` = proposals requested; `refused` = proposals rejected statically before
running; `inappl` = witnesses rejected inside their applicability prefix, i.e. without
acting; `wasted` = actions performed by a replay or proposal the goal then refused;
`disrupt` = healthy services taken down by an action (cumulative). Oracle calls in
total: 7, of which 2 were refused without running and 5 produced the 5 retained
witnesses. Services left down although healthy before an episode: none, ever.

## The plain-Python baseline

Skills are Python functions with the precondition the (scripted) agent wrote; the
world adapter enforces the grant dynamically; a skill is retained when the goal holds
after it ran. `runtime-llm` counts model calls made *inside* retained skills during
execution.

```
E1  heal(web)          -> synthesized   oracle 1  runtime-llm 0  acts 1 (wasted 0)  disruptions 0
E2  heal(api)          -> replayed      oracle 0  runtime-llm 0  acts 1 (wasted 0)  disruptions 0
E3  heal(db)           -> synthesized   oracle 1  runtime-llm 0  acts 3 (wasted 1)  disruptions 0
E4  heal(api)          -> replayed      oracle 0  runtime-llm 0  acts 2 (wasted 0)  disruptions 1
      left down although healthy before: web
E5  heal(cache)        -> replayed      oracle 0  runtime-llm 0  acts 2 (wasted 0)  disruptions 2
E6  heal(db)           -> synthesized   oracle 1  runtime-llm 1  acts 5 (wasted 3)  disruptions 3
      retained P3a (planted): asks the LLM at run time for a free port
E7  recover_host(h3)   -> synthesized   oracle 1  runtime-llm 0  acts 4 (wasted 0)  disruptions 5
E8  recover_host(h4)   -> replayed      oracle 0  runtime-llm 1  acts 4 (wasted 0)  disruptions 7
E9  heal(db)           -> parked        oracle 0  runtime-llm 0  acts 3 (wasted 3)  disruptions 8
```

| | Rote | Python baseline |
|---|---|---|
| Model calls at synthesis | 7 (2 refused before running) | 4 |
| Model calls inside retained code at run time | 0, by static rule | 2 in this run; one per port collision forever |
| Healthy services disrupted | 0 | 8 |
| Episodes that reported success while leaving a healthy service down | 0 | 1 (E4; `web` stays down through E9) |
| Wasted actions | 4 | 7 |
| Witnesses/skills rejected without acting (top-level dispatch) | 8 of 12 | n/a: applicability is discovered by running |
| Schema drift | prefix guard fails, no action, old witness kept for old shape | `KeyError` at run time, caught by try/except |
| Ungranted action in a proposal | refused statically, nothing runs | `PermissionError` after the preceding `restart` ran |

The baseline makes *fewer* synthesis calls, because its skill P2 "works" in E4 and E5
by rebooting a shared host: the goal for the named service holds and the collateral is
invisible to it. This is the central comparison. Both systems saw the same proposal
with the same two branches. Rote retained only the branch the goal validated; the
baseline retained the function, whose author-written precondition (`status == "down"`)
is true and insufficient.

## What the language caught

1. **An unvalidated branch with a blast radius (consequential).** Proposal P2's
   `else { reboot_host(host) }` was never exercised under the goal. Rote's witness has
   the guard `log_tail(service) == "no space left on device"`, so in E4 it is
   inapplicable and nothing runs. The baseline reboots `h1`, moves the outage from
   `api` to `web`, and records a success; `web` is still down at the end of the run.
   Where the guarantee comes from: the witness is the validated path, by construction.
2. **Schema drift before acting.** `cfg.port` on an observed record records
   `has(config(service), "port")`. In E6 the guard fails in the prefix; zero actions;
   the drifted shape gets its own witness. Runtime enforcement of an inferred guard.
3. **A model call inside a retained script.** Planted proposal P3a calls `ask`. The
   effect row rejects it before it runs. The baseline retains the equivalent and pays
   a model call on every later execution. Static.
4. **An action outside the grant.** Planted proposal P3b restarts, then wipes the host.
   Rote refuses the whole script statically. The baseline runs the restart, then raises.
   Static versus dynamic.
5. **Self-declared success.** A proposal that sets a flag and changes nothing is refused
   by the goal (`tests/test_runtime.py::test_only_the_goal_can_pass_a_proposal`).
   Only the goal, evaluated observe-only after the run, can pass anything.
6. **Why a host-language tracer is not enough.** `ordinary/proxy_leak.py`: a Python
   proxy tracer records nothing for `if len(services_on(host)) > 1` because `__len__`
   must return a plain int. The trace validated on a one-service host replays on a
   three-service host and disrupts two healthy services. Rote records
   `(len(services_on(host_of(service))) > 1) == false` and side-exits.

## The replay-equivalence property test

For each non-planted proposal, 120 trials: validate on a random world to get a witness,
replay it on a second random world, and run the script on a copy of that world. If the
replay completes, the effect sequences and final worlds must be identical; if it
side-exits at step k, the effects must agree up to k.

```
P1: the obvious first attempt         completed  61  side-exits  59  validation failed   0
P2: reads the log; ...                completed  88  side-exits  32  validation failed   0
P3c: moves the service off a port     completed  22  side-exits  51  validation failed  47
P4: the same fix, drifted schema      completed   3  side-exits  26  validation failed  91
```

"Validation failed" means the script itself errored on that random world (P4 needs a
nested config, which 30% of random services have), so there was nothing to retain. An
earlier evaluator failed this test (comparison operators were unhandled, found by a
unit test first); it is the test that would catch a missed decision point.

## Failure cases and costs

- **Guards pin behavior, not outcome.** The first witness (`status == "down"` then
  `restart`) applies to every down service and is wrong for most causes. It wasted one
  restart in each of E3, E4, E6 and E9. Dispatch order demotes it after its first
  failure, but when nothing more specific applies it is still tried. A policy that
  requires a witness to have *passed* on a world with the same guard values would avoid
  this at the cost of more synthesis.
- **Over-specific length guards.** `recover_host`'s witness applies only to hosts with
  exactly three services. Loops over observed lists pin their length because the body
  may act. This is sound and annoying; witnesses with bounded loops ("trace trees") are
  the fix.
- **Guard proliferation is a cost, not a hypothesis.** Minton's utility problem applies:
  each inapplicable witness costs its prefix observations (one to three here). With
  hundreds of witnesses per capability, dispatch needs an index over first guards.
- **Nothing is learned from a failed replay beyond "it failed".** The runtime cannot
  tell which observation explains a goal failure, so it cannot add negative guards. The
  next proposal has to encode the reason.
- **A novel symptom still needs the oracle.** E9 parks under replay-only. That is the
  intended behavior; it is also a reminder that the library is only as wide as the
  incidents it has validated against.
- **Validation acts on the world it is given.** The runtime does not distinguish a
  sandbox from production; the caller must pass the right world object.

## Limitations of the prototype

- Ints, strings, bools, lists, records only; no floats, no string operations beyond
  `+` and `str`; no pattern matching; no mutation; recursion bounded only by fuel.
- No static types. A type error in a proposal is a failed proposal, found by running.
- Symbolic expressions are trees, not DAGs; a fold over a long observed list builds a
  large term. Hash-consing would fix it.
- One world, one thread, no time. Observations are assumed side-effect-free by the
  host's declaration; nothing checks it.
- The replay kernel is specified by its Python implementation and the JSON shape, not
  by a written spec; the Go/TypeScript replayer is the next experiment, not done.
- The scripted oracle is deterministic by construction. Nothing here measures how a
  live model behaves against this runtime; `LiveOracle` is unexecuted.

## Assessment

The evidence supports a narrow, useful claim: for capabilities that observe, decide,
and act, inferring the path condition as the evaluator's by-product makes "does this
still apply" cheap, exact for the validated path, and model-free, and it makes
unvalidated code visibly unvalidated. It does not make retained code correct; the goal
does that, one world at a time. The right form is an embedded language in Starlark's
sense, with a replay kernel small enough to port, not a general-purpose language.

Why someone would learn it: it takes an afternoon, and in exchange every runbook an
agent writes comes with a machine-derived applicability check, an audit trail of where
it was validated and where it failed, a static guarantee that it runs without a model
and within a grant, and a side exit that says exactly which assumption broke.

## The replay kernel in Go

`replay-go/main.go` re-implements the replay kernel and the symbolic evaluator in Go
(standard library only, no parser, no evaluator for Rote itself, no world). The Python
side exports conformance fixtures: for each of the five witnesses in `runs/library.json`,
twelve replays against random worlds, each recorded as the sequence of world answers
plus the outcome Python produced (completed, or side-exited at which step, with the
exact actions and their evaluated arguments). The Go kernel reproduces all 60 outcomes:

```
$ go run ./replay-go
60 cases, 60 conform
```

That is the "retained artifacts are host-language independent" claim tested as far as
fixtures allow. It is not an end-to-end test: replaying through a Go world adapter is
listed under the next experiments. (`gofmt` and `go vet` are clean; `golangci-lint`
could not run in this environment because of a toolchain version mismatch.)

## The live experiment: harness built, dry run measured, live run blocked on a key

The experiment proposed below was built as `live/run_live.py`: random fleets with six
causes (crash, full disk, port collision in flat or nested config, lost port
configuration, stale lock, dependency down), random incident streams, and four arms
over identical incidents:

| arm | how a retained script is chosen for reuse |
|---|---|
| `rote` | inferred guards (this design) |
| `rote_reval` | inferred guards, plus re-running known sources under the goal before asking the model |
| `precond` | the author (the oracle) writes a `when` precondition; it is evaluated and the body is trusted |
| `model_each` | the model is asked every episode and shown the retained library (Voyager-style dispatch) |

This environment has no model credentials, so the live run did not happen here. What
did run is the same harness with a **deterministic heuristic oracle**: a stand-in that
diagnoses the cause from the probe and returns, first, a generic "kitchen-sink" runbook
(disk, lock, dependencies, restart) and, if that fails, the targeted fix for the cause.
It never writes a destructive branch, so this dry run measures one thing only: the
oracle-call economics of the four dispatch mechanisms under a benign, ideal
synthesizer. Safety is not exercised (every arm: 100% success, 0 disruptions, 0
collateral outages, 240 episodes each).

Three dry runs, 10 streams x 24 episodes, seed 1, same incidents, as the design changed
in response to what each run showed (`live/results/dryrun-v*`):

| | rote | rote_reval | precond | model_each |
|---|---|---|---|---|
| v1: `fold` reductions, one-phase dispatch | 0.59 | 0.36 | 0.24 | 1.31 |
| v2: + symbolic `max_of`/`min_of`/`sum` | 0.57 | 0.35 | 0.24 | 1.31 |
| v3: + two-phase, evidence-ordered dispatch | **0.43** | **0.23** | 0.24 | 1.31 |

(oracle calls per episode, mean over 240 episodes per arm)

The v3 numbers hold across seeds: seed 2 gives 0.49 / 0.24 / 0.27 / 1.34 and seed 3
gives 0.44 / 0.23 / 0.23 / 1.29 for the same four arms (`live/results/dryrun-v3-seed*`),
again at 100% success with no disruptions.

What the runs taught, in order:

1. **A validated path is narrower than its source, and that costs oracle calls.** The
   generic runbook handles four causes in one script. `precond` trusts the whole script
   under `when status == "down"` and reuses it everywhere it works. Rote retains one
   path per combination of branch outcomes (disk full or not, lock held or not, how many
   dependencies), so a new combination is a miss. That is the price of refusing to run
   unvalidated branches, and the scripted workload showed what that refusal buys (8
   disruptions avoided). `revalidate_sources` lets a caller pay the other way: re-run the
   known source on the new path under the goal, model-free, accepting that the path is
   unvalidated until the goal passes. With it, Rote matches `precond` on calls.
2. **`fold` over an observed list pins its length.** The lost-port fix computed a free
   port with `fold(ports_in_use(host), ...)`, so every host with a different number of
   running services was a miss (1.06 calls per lost-port episode). Symbolic reductions
   remove the length guard, but only moved the number to 0.98, because the dominant cost
   was the next item.
3. **One-phase dispatch let a fresh generic path pre-empt a proven targeted witness.**
   Each new path of the generic script arrived with one pass and no failures, sorted
   first, applied (its guards held), restarted, failed, and ended dispatch. Checking
   every prefix first (observe-only, no actions) and then running applicable witnesses
   by evidence, continuing past a failed replay, took lost-port episodes from 0.98 to
   0.41 calls and the `rote` arm from 0.57 to 0.43. The scripted workload's numbers did
   not change under the new policy.

Per cause after v3 (oracle calls per episode): crash 0.21 / 0.03 / 0.05 / 1.00; disk
0.55 / 0.12 / 0.10 / 1.00; lock 0.67 / 0.17 / 0.03 / 1.00; dependency 0.12 / 0.12 /
0.06 / 1.00; lost port 0.41 / 0.41 / 0.53 / 2.00; port collision 0.69 / 0.69 / 0.85 /
2.00 (rote / rote_reval / precond / model_each). `model_each` never learns by
construction; its library only shortens the model's job.

What the dry run cannot say: whether a live model writes scripts whose branches matter
(if its scripts rarely branch on observations, guards collapse toward `status ==
"down"` and the design adds little over the goal), whether its author-written `when`
clauses are as careful as the heuristic's, and whether unvalidated branches in its
scripts ever carry a blast radius. Those three questions are the live run. The harness
records per-episode outcome, oracle calls, actions, disruptions, collateral, token
usage and a full transcript; `REPLICATE.md` is a self-contained brief for running it.

To run it here, add `ANTHROPIC_API_KEY` to the cloud environment's settings (edit the
environment, API credentials or an environment variable) and start a new session; the
harness reads that variable. Budget: 10 streams x 24 episodes x 4 arms is at most 900
proposals, roughly $10-15 at Claude Opus 5.5 list prices with the cached system prompt.

## The next experiment

**Run the live arm.** The harness exists and the prediction is unchanged: as the
library covers the cause space, `rote`'s oracle calls per episode fall toward zero while
`model_each`'s stay near one, with zero disruptions throughout; `precond` is the
interesting comparison, because the live model's `when` clauses, unlike the heuristic's,
will sometimes be insufficient, and the dry run shows `rote_reval` already matching it on
calls. Two outcomes would count against the idea: a live model whose scripts rarely
branch on observations (guards collapse toward `status == "down"`), or witness
proliferation that costs more observations than synthesis saves (Minton's utility
problem; measure prefix observations per episode, which the harness can be extended to
log).

Two smaller experiments follow directly: witnesses with bounded loops, to remove the
remaining length guards (`for`/`map`/`filter` over observed lists); and replaying a
library through the Go kernel against a Go world adapter rather than fixtures, to test
host independence end to end.
