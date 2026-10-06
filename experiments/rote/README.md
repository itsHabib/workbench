# Rote

A tiny language for capabilities that agents build, keep, and reuse. Its one idea:
**the evaluator infers, from a single validated run, the exact world-assumptions that
run depended on, and the retained artifact refuses to run anywhere those assumptions
no longer hold.** No model is needed to execute a retained capability. A model is
needed only when none applies.

The name is provisional: "by rote" is how a retained capability runs, without thinking.
(PyPI already has an unrelated `rote` package; nothing here is published.)

Status: a runnable prototype (~2,578 lines of stdlib Python for the language, 65 tests), one
scripted nine-episode workload, a model-free dry run of the larger live experiment, and a
Go replayer checked against exported fixtures. Every number in `RESULTS.md` comes from a
scripted or heuristic oracle; **no model was called anywhere in this experiment**. The
live run needs a key (see `RESULTS.md`, "The live experiment").

## The idea in plain English

An agent writes a script to achieve a goal in some world. In Rote the script can only
touch the world through declared `observe` and `act` functions, and every value it
derives from an observation (or from its parameters) is shadowed by a symbolic
expression. The evaluator computes concretely, but each time the run *decides*
something from such a value (which branch, how many loop iterations, whether a field
exists, whether an index is in range, whether a divisor is zero) it records a **guard**:
the expression and the value it had.

If the host-supplied goal holds after the run, the straight line of effects the run
performed, with the guards interleaved where the decisions were made, is retained as a
**witness**. A witness is the trace-compiler's view of the script: one validated path,
specialized to one history of observations, with side exits.

Later, to achieve the same goal, the runtime replays witnesses: it re-observes, checks
each guard against the fresh value, and performs the recorded actions only while every
guard holds. The guards before the first action form the **applicability prefix**,
which can be checked without changing anything. A guard that fails there means "this
witness does not apply here" and costs nothing. A guard that fails after an action is
a side exit with a precise report. Only when no witness applies does the runtime ask
the oracle (an LLM, or here a cassette of scripted proposals) for a new script, which
goes through the same validation.

Two static rules complete the picture. A script's effect row is computed from its
syntax, so a proposal that calls `ask` (the oracle) is refused before it runs:
retained capabilities are oracle-free by construction. A proposal whose actions exceed
the caller's grant is refused the same way. And only the goal can declare success: a
script has no way to say "it worked".

## What this buys you

- **Applicability is derived, not written.** Preconditions humans and models write are
  incomplete; the inferred guard set is exactly what the validated path depended on.
- **Unvalidated code never runs with validated trust.** A branch that was never taken
  under the goal has no witness. The ordinary baseline runs it; Rote parks or re-validates.
- **Dispatch without a model and mostly without acting.** Across the workload: 7 oracle
  calls in 9 episodes; in top-level dispatch 8 witness rejections cost observations only,
  and the 4 that acted first were all the earliest, most general witness.
- **Drift is a side exit, not a crash.** A changed record shape fails a `has(...)` guard
  in the prefix. Nothing acts. The old witness stays valid for the old shape.
- **Retained artifacts are data.** A witness is JSON over a 21-form expression language;
  the replayer is ~140 lines and needs no parser or evaluator (`rote/witness.py`,
  `rote/sym.py`), so a Go or TypeScript host could replay a library without the rest.
- **Grants and oracle-freedom are static.** The plain-Python baseline can only refuse an
  ungranted action when it is reached, after earlier actions already ran.

## Run it

```sh
cd experiments/rote
python3 demo.py                 # the nine-episode workload, Rote (scripted oracle)
python3 ordinary/agent_py.py    # the same episodes in plain Python (the baseline)
python3 ordinary/proxy_leak.py  # why a host-language tracer cannot infer guards
python3 -m pytest tests -q      # 65 tests, including the replay-equivalence property test
python3 live/run_live.py --oracle heuristic --streams 10 --episodes 24   # model-free dry run, 4 arms
python3 live/run_live.py --oracle live --streams 10 --episodes 24 --max-calls 900  # needs ANTHROPIC_API_KEY
go run ./replay-go             # Go replayer vs the demo's witnesses: '60 cases, 60 conform'
go run ./replay-go -lib runs/synthetic_library.json -fixtures runs/synthetic_fixtures.json  # all 21 forms: 192 conform
```

Python 3.12+ and pytest are the only requirements. `demo.py` rewrites `runs/library.json`
(the retained witnesses with their evidence) and `runs/report.json`.

Expected headline from `demo.py`:

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

## Where the guarantees come from

| Claim | Source |
|---|---|
| If a replay completes, the script would have performed exactly those effects | Construction of the evaluator (complete mediation of observation-derived values); checked empirically by `tests/test_property.py` on random worlds; not proven |
| A retained witness never calls the oracle; never exceeds the grant | Static effect-row check before validation; grant re-checked before every replay |
| Nothing acts before a prefix guard fails | Replay evaluates steps in order; the prefix contains no actions |
| Success | Only the goal's verdict, evaluated after the run, observe-only by static check |
| Every run terminates | Fuel |
| The outcome of an action | Not guaranteed. Guards pin the script's behavior, not the world's response; evidence records both passes and failures |

## Layout

```
rote/        the language: lexer, parser, ast, sym (symbolic expressions), eval (the
             tracing evaluator), effects (static rows), witness (witness + replay
             kernel), library, runtime (dispatch/validate/retain), synth (oracle seam)
worlds/      the simulated fleet (hosts, services, ports, disks, logs) and its incidents
scenario/    fleet.rote (world signature + capabilities) and proposals.json (the cassette)
ordinary/    the plain-Python baseline and the proxy-leak demonstration
live/        the live experiment: six-cause world, four arms, runner, dry-run results
replay-go/   a Go re-implementation of the replay kernel + the fixture exporter
tests/       unit tests, runtime tests, the replay-equivalence property test
runs/        output of the last demo run and the Go conformance fixtures (demo and synthetic)
demo.py      the workload
REPLICATE.md a self-contained prompt for another agent to replicate, break and compare
```

Read `DESIGN.md` for the semantics, the alternatives considered, and what is new;
`EXAMPLES.md` for programs and how they execute; `RESULTS.md` for the numbers, the
failure cases, the limitations, and the next experiment.
