# Design

## The problem

An agent inspects a world, writes a bounded script, runs it, and keeps it when a
checker says the goal was reached. Over time the kept scripts are supposed to become a
library. The hard part is not writing the scripts. It is answering, later and cheaply,
three questions about a kept script:

1. Does it apply here, without running it (running means acting)?
2. Which parts of it were actually validated, and which parts merely came along in the
   same source file?
3. Can it run at all without the model that wrote it?

Ordinary languages have no vocabulary for any of these. A function is a function. Its
preconditions are whatever its author wrote, if anything. Every branch is trusted
equally. A call to an LLM inside it is just another call. The result, in every
skill-library system found in the literature survey (Voyager, CodeAct, Agent Workflow
Memory, and the rest), is dispatch by description similarity plus a fresh model call,
and failure discovered by acting.

## Three concepts considered

Each is described by what a program *means*, since syntax is the cheap part.

### A. Rote: validated paths with inferred guards (chosen)

A script denotes a strategy tree over the world's answers: internal nodes are
observations, edges are the values they may return, leaves are action sequences. One
run is one path. The evaluator records the path condition as it goes, by shadowing
every observation-derived value with a symbolic expression and recording a guard at
every point where the run concretizes such a value to decide something. The retained
artifact is the path: its effects as symbolic terms, its guards in place. Replay means
re-walking the path against fresh answers and stopping at the first guard that fails.
Meaning of a library: a set of (goal, validated path, guards, evidence) tuples; meaning
of a call: the first path whose guards hold, else synthesis.

### B. Ledger: assumption-based truth maintenance over declared world facts

A program is a set of rules `achieve G when A1..An by <procedure>` plus a belief store.
Observations are assumptions; derived facts and performed actions carry justifications;
the evaluator is an assumption-based TMS (de Kleer 1986): when an observation changes,
everything justified by it is retracted, and the capabilities whose support is gone are
flagged. Meaning of a program: a justification graph from observations through beliefs
to actions. Strengths: continuous worlds, monitoring, explanations ("why did we do X"),
multiple simultaneous hypotheses. Weaknesses for this problem: the assumptions are
*declared* by whoever writes the rule, so question 1 is answered only as well as the
author answered it; the procedures remain ordinary code, so question 2 is not answered
at all; and the machinery is heavy for episodic tasks. Ledger is the right shape for a
watchdog, not for a library of runbooks.

### C. Grove: a vocabulary that grows by compression

Programs are terms in a small combinator core. Periodically the system anti-unifies the
corpus of kept programs and introduces the abstractions that compress it most
(DreamCoder, Stitch, LILO), re-expressing old programs in the new vocabulary. Meaning
of a program is relative to a library version; the type system carries the version so
programs can be migrated. Strength: the language adapts to the domain, so later
proposals are shorter and have fewer places to be wrong. Weakness: compression is a
statement about syntax, not correctness, and it says nothing about *when* an
abstraction applies, so questions 1 and 2 are untouched. Grove is orthogonal to Rote
rather than an alternative: witnesses are straight-line programs, which is exactly the
input anti-unification likes. It is the natural second layer, not the first.

(A fourth direction, Hazel-style typed holes where the evaluator runs around the parts
the agent has not written yet, is attractive for the writing phase but does nothing for
retention, so it was dropped early.)

### Why A

Rote is the only one of the three in which the answers to all three questions are
*by-products of running the program*, computed by the evaluator with no help from the
author. Guards answer 1. Paths answer 2. The effect row answers 3. Those answers are
also exactly what a trace-JIT needs to make a compiled trace safe (Gal et al. 2009),
which is reassuring: the design is a known-good shape in a new place.

## What a program is

A library file declares one world and some capabilities:

```
world fleet {
  observe status(service): string      # reads; never changes the world
  act restart(service): bool           # changes the world
  ...
}
cap heal(service) {
  "Bring one service back to up."
  goal: status(service) == "up"        # observe-only, checked statically
}
```

A **script** is proposed at run time, by an oracle, as source text:

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

A **tactic** is an optional in-language driver for synthesis; it is the only place
`ask` and `propose` are legal:

```
tactic heal(service) {
  let ctx = {cap: "heal", status: status(service), log: log_tail(service)}
  let first = propose(ask(ctx))
  if not first.ok { propose(ask(ctx)) }
}
```

The core is small: ints, strings, bools, unit, lists, records, closures; `let`, `for`,
`if`/`else`, `return`; sixteen builtins (`len map filter fold all any contains keys has
get str range min max fail append`); world functions called by name; `use CAP(args)`
to call another capability. World functions and builtins are not first-class, which is
what keeps effect rows exact and syntactic. There is no static type system beyond the
effect row; types are checked at run time and a type error is a failed proposal.

## Semantics that matter

### Values and shadows

Every parameter and every result of `observe`, `act` or `use` enters the run as a
`Sym`: a concrete value plus a symbolic expression (`["param", "service"]`,
`["ref", 3]`). Pure operations on a `Sym` produce a `Sym` whose expression records the
operation (`["field", e, "port"]`, `["bin", "+", e, 1]`, `["len", e]`, ...). The
expression language has 21 forms and is JSON (`rote/sym.py`). Containers built by the
script may hold `Sym`s; lifting such a container yields a `list`/`record` expression.

### Decision points record guards

| The run does this | Guard recorded (expression, value it had) |
|---|---|
| `if c`, `and`/`or` left operand, `filter`/`all`/`any` element | `c`, true or false |
| `for x in xs`, `map`, `fold`, `append` over an observed list | `len(xs)`, n |
| `xs[i]` with observed list or index | `i < len(xs)`, true (and `i >= 0` if `i` is observed) |
| `r.k` / `r[k]` on an observed record | `has(r, k)`, true |
| `a / b`, `a % b` with observed `b` | `b == 0`, false |
| `range(n)` with observed `n` | `n`, its value |

Everything else is data flow and needs no guard: an observed value passed to an action
becomes a symbolic argument, re-evaluated at replay. `get(r, k, default)` and
`has(r, k)` are symbolic and guard-free, so the script author chooses between "assume
the field exists" (`r.k`, guarded) and "handle its absence" (`get`, not guarded). The
reductions `sum`, `max_of` and `min_of` over an observed list are symbolic too: they do
not pin the list's length the way a `fold` with a closure must (the closure's body may
act, the reduction cannot). They were added after the dry run showed `fold` guards
costing oracle calls (`RESULTS.md`).

A script may carry an author-written `when` clause (`script heal(s) when <expr> { ... }`).
In Rote it is not trusted as a precondition: it runs first, inside the traced run, so its
decisions become ordinary guards and a false clause is a failed proposal. The live
experiment's `precond` arm uses the same clause the way ordinary systems would, as a
trusted dispatch predicate, to measure the difference.

### Witnesses and replay

A witness is the step list of a run that the goal accepted: `observe`, `act`, `use`
steps with symbolic arguments, and `guard` steps. Identical guards are deduplicated
(guards are functions of parameters and step results, which never change within a run).
Its `prefix` is everything before the first `act`/`use`. Its hash is the content hash
of params and steps, so the same path validated twice is one witness with two pieces
of evidence.

`replay(witness, args, world, grant)`:
1. Refuse if any `act` is outside the grant. Nothing runs.
2. Walk the steps. `observe`: call the world, bind the result. `guard`: evaluate the
   expression against fresh bindings; if it differs from the recorded value, stop and
   report (`side_exit`), with the list of actions performed so far (empty inside the
   prefix). `act`: call the world. `use`: call the runtime recursively.
3. If every step ran: `completed`. The goal then decides.

The soundness property the design rests on: **if replay completes, the script run
against the same world would have issued the same effects with the same arguments in
the same order.** Argument: the script's control flow depends on observation-derived
values only at the decision points above, each of which is guarded; its effect
arguments are the recorded symbolic terms; everything else is pure and deterministic.
This is a claim about the evaluator's completeness, not a proof; `tests/test_property.py`
checks it on random worlds for every scripted proposal (and an earlier version of the
evaluator failed it, which is the point of having it).

What guards do **not** guarantee: that the actions will have the same outcome. The
world's dynamics are outside the language. A witness can complete and the goal can
still fail; that is recorded as failing evidence and the runtime moves on to synthesis.

### Effect rows, grants, goals

`rote/effects.py` computes, from syntax alone, the set of observations, actions, and
capability uses a body can reach, and whether it asks or proposes. Three checks:

- a script may not `ask` or `propose`, and its actions must be within the grant;
- a goal may only observe;
- a tactic may do anything, but names and arities must still resolve.

A failed check refuses the body before it runs. The same grant is enforced dynamically
too, in the evaluator and before each replay, so a hand-edited witness cannot escape it.

### The runtime loop

`achieve(cap, args, world)`:
1. If the goal already holds, do nothing (`already_satisfied`).
2. Dispatch in two phases. First, check every witness's applicability prefix without
   acting; this costs observations only and rejects most witnesses. Then run the
   applicable ones in evidence order (fewest failures, most passes, newest), each replay
   re-checking its own guards against the world as it now is. A replay that acts and
   fails is recorded and, by default, dispatch continues to the next applicable witness
   (`stop_after_failed_replay` makes it end instead).
3. If the policy is `replay-only`: park, with the reason.
4. If `revalidate_sources` is set: re-run the sources of retained witnesses under the
   goal, newest first, before asking anyone. This is the trace-JIT move of re-entering
   the interpreter on the same program after a side exit. It takes unvalidated paths
   and may act, which is why it is opt-in; `replay-only` still forbids it.
5. Otherwise synthesize: run the tactic, or the default loop that asks the oracle for
   up to N proposals, each statically checked, run once under the goal, and retained
   with evidence on a pass. The prompt carries the goal, the signature, the grant, what
   dispatch observed, an optional caller-supplied probe of the situation, and the prior
   attempts with their failure reasons.

The caller supplies the goal (in the library file), the grant and policy (`Policy`),
the world (any object with `signature/observe/act`), and the oracle. Validation acts on
whatever world the caller passes, so a caller who wants to validate in a sandbox and
replay in production passes two worlds.

## What is new and what is borrowed

Every piece has a precedent, and the honest claim is narrow.

- Conditions inferred by backtracing one successful episode's dependencies: Soar
  chunking (Laird, Rosenbloom, Newell 1986) and explanation-based generalization
  (Mitchell, Keller, Kedar-Cabelli 1986). Minton (1988) is the standing warning that
  learned conditions cost something to test.
- A straight-line residual with guards and side exits to a slow path: trace-based JITs
  (Bala et al. 2000; Gal et al. 2009; Bolz et al. 2009). Rote moves the guards from
  type assumptions to observed values and the slow path from an interpreter to a
  synthesizer.
- The path condition of one concrete run: concolic testing (DART, CUTE 2005), which
  negates it to explore; Rote keeps it as a reuse predicate.
- Residual programs: partial evaluation (Jones, Gomard, Sestoft 1993).
- Effect rows that make oracle calls and grants visible in types: Plotkin and Pretnar's
  handlers, Koka's effect types, Unison's abilities and content-addressed definitions.
- A hermetic embedded dialect with host-supplied world access: Starlark.
- Re-checking recorded dependencies before reusing a result: verifying traces (Mokhov,
  Mitchell, Peyton Jones 2018), Adapton, the ATMS.
- An untrusted producer in front of a small trusted checker: Lean's elaborator/kernel
  split and the de Bruijn criterion.
- Verdict-gated libraries of LLM-written code: Voyager. Learned skill preconditions in
  robotics: Konidaris, Kaelbling, Lozano-Pérez 2018 (statistical, over many runs).

The survey (21 sources, run against primary texts where the sandbox could reach them)
found no prior work that infers applicability guards for LLM-generated code skills by
complete mediation of observation-derived values as a language semantics, combined
with static exclusion of oracle effects from retained artifacts. The nearest system is
PreAct (2026), which compiles verified GUI runs into replayable state machines with
per-step screen checks and hands back to the agent when a check fails; its checks are
screen matches rather than inferred path conditions, and it has no effect typing. So:
a combination of known mechanisms, in a place none of them has been put, with one
genuinely new element (guards as the evaluator's by-product, complete by construction).

## Why a language rather than a library

The whole design is the mediation discipline, and a host language cannot provide it.
`ordinary/proxy_leak.py` shows the best-effort Python version: proxy objects that record
a guard on every comparison, truth test, index, and iteration. Python requires
`__len__` to return a plain int, `__bool__` a plain bool, `__hash__` a plain int, and
intercepts nothing for `is`, `isinstance`, `type`, or C-implemented consumers. So the
decision `if len(services_on(host)) > 1` leaves no guard, and a trace validated on a
one-service host reboots two healthy services when replayed on a three-service host.
CrossHair's and JAX's own documentation describe the same wall from the other side
("the illusion is not complete"; "we don't know which branch to take, and can't
continue tracing"). A dedicated evaluator is ~650 lines and closes it.

That said, the right framing is *embedded language*, in Starlark's sense: a small
hermetic dialect that lives inside a host application, where the host supplies the
world, the goals, the grants, and the oracle. It is not a general-purpose language
and should not grow into one. The pieces are separable: the replay kernel (JSON
witnesses, 21 expression forms, ~150 lines in Python, ~700 in Go) can be reimplemented in a host language
without the parser, evaluator, or runtime.

## Decisions and their reasons

- **Dynamic types, static effects.** A static type system would catch little the goal
  does not, and would cost the agent-facing simplicity. Effect rows are the one static
  property that changes what may run at all.
- **Length guards for loops over observed lists.** A loop body may have effects, so the
  iteration count is a real dependency. Pinning it is sound and over-specific
  (`recover_host`'s witness only applies to three-service hosts). The fix is witnesses
  with bounded loops, which is the first next experiment.
- **No negative guards learned from failures.** When a witness completes and the goal
  fails, the runtime knows *that* the guards were insufficient, not *which* observation
  explains it. It records the failure and lets the next proposal encode the reason as a
  new guard. Deriving negative guards automatically would require the world's dynamics.
- **Two-phase, evidence-ordered dispatch.** The first version ran the first applicable
  witness immediately and stopped after any replay that acted and failed. The dry run
  showed a fresh path of a generic script pre-empting a better-evidenced targeted
  witness over and over (`RESULTS.md`, dispatch v1 vs v3). Checking all prefixes first
  is free of actions, so there is no reason not to.
- **The goal runs before as well as after.** A goal that already holds means nothing to
  do and no evidence; validated witnesses therefore always carry evidence of changing a
  goal-false world into a goal-true one.
- **`use` as a step.** Composition is by capability, not by source inclusion, so the
  inner capability repairs itself locally and the outer witness stays valid.
- **A bool is never a number.** The first reference let Python's `True == 1` leak into
  `==`, `contains`, list indexing and guard comparison. The Go conformance kernel, written
  to a stricter reading of the spec, disagreed on exactly those cases, which is how the
  leak was found. Equality is now structural and typed in both the evaluator and the
  kernel (`deep_eq`), record keys must be strings, and `contains` on a string needs a
  string. A second implementation is the cheapest spec check there is.
