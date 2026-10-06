# Programs and how they execute

Everything below is taken from `python3 demo.py` and `python3 -m pytest tests`.
Witness listings are printed by `Witness.pretty()`; a `*` marks steps after the
applicability prefix, i.e. steps that change the world.

## The library file

```
world fleet {
  observe status(service): string
  observe host_of(service): string
  observe disk(host): int
  observe config(service): record
  observe log_tail(service): string
  observe ports_in_use(host): list
  observe services_on(host): list
  act restart(service): bool
  act clear_tmp(host): int
  act set_config(service, key, value): bool
  act reboot_host(host): int
  act wipe_host(host): bool
}

cap heal(service) {
  "Bring one service back to up."
  goal: status(service) == "up"
}

cap recover_host(host) {
  "Every service on the host is up."
  goal: all(map(services_on(host), fn(s) { status(s) == "up" }))
}
```

The host (here `worlds/fleet.py`, 210 lines of Python) implements the same signature;
the runtime checks they agree. The caller's grant for the workload is
`{restart, clear_tmp, set_config, reboot_host}`: `wipe_host` exists but is not granted.

## 1. A first proposal becomes a witness

`web` has crashed. No witness exists, so the runtime asks the oracle. The cassette's
first proposal:

```
script heal(service) {
  if status(service) == "down" {
    restart(service)
  }
}
```

Execution under the goal: `status(service)` is an observation, so its result enters
as `Sym(["ref", 1], "down")`. Comparing it with `"down"` yields a symbolic bool. The
`if` must decide, so it records a guard. `restart(service)` is an action whose
argument is the parameter. The goal `status(service) == "up"` holds afterwards, so
the step list is retained:

```
   observe#1 = status(service)
   guard  (status(service) == "down") == true
 * act    #2 = restart(service)
```

Note what the witness does *not* contain: the untaken branch. If `status` had been
`"up"` the run would have recorded the guard with value `false` and no action, and the
goal would already have held, so nothing would have been retained.

Episode 2 (`api` crashes) replays this witness: observe `status(api)`, check the
guard, restart. Zero oracle calls.

## 2. A branch that was never validated never runs

`db`'s host runs out of disk. Dispatch tries the witness above: the guard holds,
`restart(db)` runs, the goal still fails (restart cannot succeed on a full disk). That
is recorded as failing evidence (one wasted action) and synthesis starts. The second
proposal reads the log:

```
script heal(service) {
  let host = host_of(service)
  if log_tail(service) == "no space left on device" {
    clear_tmp(host)
  } else {
    reboot_host(host)
  }
  restart(service)
}
```

It passes, and the witness is the *then*-branch only:

```
   observe#1 = host_of(service)
   observe#2 = log_tail(service)
   guard  (log_tail(service) == "no space left on device") == true
 * act    #3 = clear_tmp(host_of(service))
 * act    #4 = restart(service)
```

In episode 4, `api` is down with a port collision on a host where `web` is healthy.
This witness is newest and has no failures, so it is tried first:

```
inapplicable  w4d36485553 does not apply: guard (log_tail(service) == "no space left
              on device") expected true, got false
```

Two observations, no action. The `reboot_host` branch, which exists in the source and
which the author's own precondition (`status == "down"`) would have admitted, is not
in any witness, so it cannot run. The plain-Python baseline runs it here: the reboot
takes `web` down, `api` comes up on the port `web` was using, `web` fails to come back,
the goal for `api` holds, and the baseline records a success (`RESULTS.md`).

## 3. Shape guards and schema drift

The third accepted proposal moves a service off a colliding port:

```
script heal(service) {
  let host = host_of(service)
  let cfg = config(service)
  if contains(ports_in_use(host), cfg.port) {
    set_config(service, "port", cfg.port + 1)
  }
  restart(service)
}
```

`cfg` is an observed record. `cfg.port` is a decision (the field might not exist), so
it records `has(config(service), "port") == true`. `contains` is pure and symbolic;
the `if` guards it. The second `cfg.port` records the same guard again and is
deduplicated. The action argument `cfg.port + 1` is kept symbolic:

```
   observe#1 = host_of(service)
   observe#2 = config(service)
   observe#3 = ports_in_use(host_of(service))
   guard  has(config(service), "port") == true
   guard  contains(ports_in_use(host_of(service)), config(service).port) == true
 * act    #4 = set_config(service, "port", (config(service).port + 1))
 * act    #5 = restart(service)
```

In episode 6, `db`'s configuration has drifted to `{"listen": {"port": ...}}` and it
collides with `cache`. Replay: observe `host_of`, observe `config`, observe
`ports_in_use`, evaluate the first guard:

```
inapplicable  w61d79abf0c does not apply: guard has(config(service), "port") expected
              true, got false
```

Nothing acted. The fourth proposal, written for the new shape (`cfg.listen.port`),
is validated and retained alongside, with guards `has(config, "listen")`,
`has(config.listen, "port")`, and the `contains`. From then on the two shapes dispatch
to the two witnesses by their guards. Nobody wrote a schema check.

## 4. Composition and local repair

```
script recover_host(host) {
  for s in services_on(host) {
    use heal(s)
  }
}
```

`services_on(host)` is an observed list; iterating it pins its length. Each `use` is a
step that calls the runtime recursively:

```
   observe#1 = services_on(host)
   guard  len(services_on(host)) == 3
 * use    #2 = heal(services_on(host)[0])
 * use    #3 = heal(services_on(host)[1])
 * use    #4 = heal(services_on(host)[2])
```

In episode 7 the inner calls dispatch on their own: `s1` (full disk) to the second
witness, `s2` (healthy) to "already satisfied", `s3` (crash) to the first witness. One
oracle call, for the outer script. Episode 8 replays the outer witness on a different
host with different symptoms; the inner dispatch handles them; zero oracle calls. The
length guard is also this witness's main limitation: it applies only to hosts with
exactly three services (see `RESULTS.md`).

## 5. A tactic: the elaborator in the language

From `tests/test_runtime.py`:

```
tactic heal(service) {
  let ctx = {cap: "heal", status: status(service), log: log_tail(service)}
  let first = propose(ask(ctx))
  if not first.ok { propose(ask(ctx)) }
}
```

A tactic runs in `tactic` mode: it may observe to build context, `ask` the oracle,
and `propose` source text. `propose` parses, statically checks, runs the script under
the goal, and retains it on a pass, returning `{ok, kind, reason, witness}`. The
tactic's own observations produce no guards and no witness; only the proposed script's
run does. The same syntax, two effect rows, one static line between them: a script
that contains `ask` is refused before it runs.

## 6. The static refusals

Two planted proposals from episode 4, exactly as the runtime reports them:

```
script heal(service) {
  let host = host_of(service)
  let port = ask("pick a free port on " + host)
  set_config(service, "port", port)
  restart(service)
}
refused   static check: line 1: a script may not `ask`: retained capabilities must
          run without an oracle

script heal(service) {
  restart(service)
  wipe_host(host_of(service))
}
refused   static check: line 1: action 'wipe_host' is not in the caller's grant
```

Neither ran. In the baseline the first is retained and costs a model call on every
later execution; the second restarts the service and only then raises.

## 7. The replay-only policy

Episode 9 runs with `Policy(mode="replay-only")` and a symptom no witness covers
(`db` has lost its port configuration):

```
inapplicable  w5943be240a does not apply: guard has(config(service), "listen") ...
inapplicable  w61d79abf0c does not apply: guard has(config(service), "port") ...
inapplicable  w4d36485553 does not apply: guard (log_tail(service) == "no space ...
checker_fail  wacedcf7c9f replayed but the goal does not hold
parked        no validated path applies and the policy forbids synthesis
```

Three witnesses rejected without acting; the most general one acted once (a restart)
and the goal refused it. The outcome names what is missing instead of inventing a fix.

## 8. The witness as data

`runs/library.json` holds each witness in the form a host in another language would
replay. The third witness above, abbreviated:

```json
{"cap": "heal", "params": ["service"], "hash": "w61d79abf0c",
 "steps": [
  {"op": "observe", "id": 1, "name": "host_of", "args": [["param", "service"]]},
  {"op": "observe", "id": 2, "name": "config", "args": [["param", "service"]]},
  {"op": "observe", "id": 3, "name": "ports_in_use", "args": [["ref", 1]]},
  {"op": "guard", "pred": ["has", ["ref", 2], ["const", "port"]], "expect": true},
  {"op": "guard", "pred": ["contains", ["ref", 3], ["field", ["ref", 2], ["const", "port"]]], "expect": true},
  {"op": "act", "id": 4, "name": "set_config",
   "args": [["param", "service"], ["const", "port"], ["bin", "+", ["field", ["ref", 2], ["const", "port"]], ["const", 1]]]},
  {"op": "act", "id": 5, "name": "restart", "args": [["param", "service"]]}],
 "evidence": [
  {"kind": "validated", "verdict": "pass", "world": "fleet@E4", "args": {"service": "api"}, "acts": 2}]}
```

A replayer needs an evaluator for the 18 expression forms and the loop in
`rote/witness.py:replay`. It needs no parser and no interpreter.
