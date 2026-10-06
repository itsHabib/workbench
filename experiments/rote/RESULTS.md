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

## The next experiment

**Strengthen or disprove:** replace the cassette with `LiveOracle` (one Claude call
per proposal), generate 50 random incident streams of 30 episodes each over a fleet
with 6 causes instead of 3, and measure per-episode oracle calls over time, wasted
actions, disruptions, and collateral outages, against a Voyager-style baseline (skills
retrieved by description similarity, re-executed through the model). The prediction is
that Rote's oracle calls per episode fall toward zero as the library covers the cause
space while the baseline's stay roughly constant, with zero disruptions throughout.
Two outcomes would disprove the idea's value: if the model's scripts branch on
observations so rarely that guards are nearly always just `status == "down"` (then
guards add little over the goal), or if witness proliferation makes dispatch cost more
observations than synthesis saves (Minton's problem, measured rather than assumed).

Two smaller experiments follow directly: witnesses with bounded loops, to remove the
length-guard limitation; and a ~150-line Go replayer over `runs/library.json`, to test
the claim that the retained artifacts are host-language-independent.
