"""The agent loop as a runtime: dispatch retained witnesses, replay the first one
whose guards hold, let the goal judge, and only then -- if nothing applied or the
goal refused -- synthesize a new script, validate it and retain it.

Everything the caller controls arrives in `Policy`: the grant (which acts are
allowed), whether synthesis is allowed at all, how many proposals to try, fuel.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from . import ast as A
from .effects import StaticError, check_goal, check_script
from .eval import RoteError, Tracer, run_script
from .library import Capability, Library
from .parser import parse_program
from .synth import OracleExhausted
from .witness import Evidence, ReplayOutcome, Witness, describe_guards, replay, witness_from_steps

__all__ = ["Policy", "Outcome", "Event", "Runtime", "ProposalResult"]


@dataclass
class Policy:
    grant: set[str]
    mode: str = "auto"  # "auto" | "replay-only"
    max_attempts: int = 3
    fuel: int = 20_000
    max_depth: int = 6
    probe: Any = None  # optional callable(world, cap_name, args) -> dict shown to the oracle as "situation"
    revalidate_sources: bool = (
        False  # on a miss, re-run known sources under the goal before asking the oracle
    )
    stop_after_failed_replay: bool = False  # True: one witness that acted and failed ends dispatch


@dataclass
class Event:
    kind: str
    text: str
    data: dict[str, Any] = field(default_factory=dict)


@dataclass
class Outcome:
    cap: str
    args: list[Any]
    kind: str  # already_satisfied | replayed | synthesized | parked | failed
    witness: Witness | None = None
    acts: list[tuple[str, list[Any]]] = field(default_factory=list)
    events: list[Event] = field(default_factory=list)
    oracle_calls: int = 0
    reason: str = ""
    seen: dict[str, Any] = field(default_factory=dict)  # what dispatch observed, for the oracle

    @property
    def ok(self) -> bool:
        return self.kind in ("already_satisfied", "replayed", "revalidated", "synthesized")

    def summary(self) -> dict[str, Any]:
        return {"ok": self.ok, "how": self.kind, "witness": self.witness.hash if self.witness else None}


@dataclass
class ProposalResult:
    ok: bool
    kind: str  # retained | refused | error | fail
    reason: str = ""
    witness: Witness | None = None
    acts: list[tuple[str, list[Any]]] = field(default_factory=list)
    events: list[Event] = field(default_factory=list)

    def summary(self) -> dict[str, Any]:
        return {
            "ok": self.ok,
            "kind": self.kind,
            "reason": self.reason,
            "witness": self.witness.hash if self.witness else None,
        }


class Runtime:
    def __init__(self, library: Library, policy: Policy, oracle: Any = None):
        self.library = library
        self.policy = policy
        self.oracle = oracle
        self._depth = 0
        self.oracle_calls = 0

    # -- helpers -------------------------------------------------------------------
    def _tracer(self, world: Any, mode: str) -> Tracer:
        return Tracer(
            world,
            grant=set(self.policy.grant),
            mode=mode,
            fuel=self.policy.fuel,
            caps=self.library.arities(),
            runtime=self,
            oracle=self.oracle,
        )

    def goal_holds(self, cap: Capability, args: list[Any], world: Any) -> tuple[bool | None, str]:
        """The checker. None means inconclusive (the goal itself failed to evaluate)."""
        t = self._tracer(world, "goal")
        try:
            return t.eval_goal(cap.params, args, cap.goal), ""
        except RoteError as err:
            return None, f"goal could not be evaluated: {err}"
        except Exception as err:  # fuel or a world adapter error
            return None, f"goal could not be evaluated: {err}"

    # -- the loop ---------------------------------------------------------------------
    def achieve(self, cap_name: str, args: list[Any], world: Any) -> Outcome:
        cap = self.library.cap(cap_name)
        out = Outcome(cap_name, list(args), "failed")
        if len(args) != len(cap.params):
            out.reason = f"{cap_name} expects {len(cap.params)} arguments, got {len(args)}"
            return out
        if self._depth >= self.policy.max_depth:
            out.reason = "capability nesting too deep"
            return out
        self._depth += 1
        try:
            return self._achieve(cap, args, world, out)
        finally:
            self._depth -= 1

    def _achieve(self, cap: Capability, args: list[Any], world: Any, out: Outcome) -> Outcome:
        holds, why = self.goal_holds(cap, args, world)
        if holds:
            out.kind = "already_satisfied"
            out.events.append(Event("goal", "goal already holds; nothing to do"))
            return out
        if holds is None:
            out.events.append(Event("goal", why))
        if self._dispatch(cap, args, world, out):
            return out
        if self.policy.mode == "replay-only":
            out.kind = "parked"
            out.reason = "no validated path applies and the policy forbids synthesis"
            out.events.append(Event("parked", out.reason))
            return out
        if self.policy.revalidate_sources and self._revalidate(cap, args, world, out):
            return out
        return self._synthesize(cap, args, world, out)

    def _revalidate(self, cap: Capability, args: list[Any], world: Any, out: Outcome) -> bool:
        """The trace-JIT move: a side exit re-enters the interpreter on the same program
        before anyone calls the compiler. Re-running a known source takes an unvalidated
        path under the goal; the caller opts in because that path may act."""
        seen: set[str] = set()
        for w in sorted(cap.witnesses, key=lambda x: -x.seq):
            if w.source in seen:
                continue
            seen.add(w.source)
            res = self.propose(w.source, world, cap, args)
            out.events.extend(res.events)
            out.acts.extend(res.acts)
            if res.ok:
                out.kind = "revalidated"
                out.witness = res.witness
                out.events.append(
                    Event(
                        "revalidated",
                        f"known source of {w.hash} took a new path; retained {res.witness.hash}",
                    )
                )
                return True
            if res.acts:
                break  # it acted and failed: do not keep trying sources on a changed world
        return False

    def _applicable(self, cap: Capability, args: list[Any], world: Any, out: Outcome) -> list[Witness]:
        """Phase one: check every witness's prefix without acting; keep the applicable ones."""
        keep: list[Witness] = []
        for w in self.library.dispatch_order(cap):
            r = replay(w, args, world, self.policy.grant, runtime=self, prefix_only=True)
            out.seen.update(r.seen)
            if r.kind == "refused":
                out.events.append(
                    Event("refused", f"{w.hash} refused before replay: {r.reason}", {"witness": w.hash})
                )
            elif r.kind in ("side_exit", "error"):
                out.events.append(
                    Event(
                        "inapplicable",
                        f"{w.hash} does not apply: {r.reason}",
                        {"witness": w.hash, "step": r.step},
                    )
                )
            else:
                keep.append(w)
        return keep

    def _dispatch(self, cap: Capability, args: list[Any], world: Any, out: Outcome) -> bool:
        """Phase two: run applicable witnesses, best evidence first. Each replay re-checks its
        own guards against the world as it is now. Returns True when one achieved the goal."""
        for w in self._applicable(cap, args, world, out):
            r = replay(w, args, world, self.policy.grant, runtime=self)
            out.acts.extend(r.acts)
            out.seen.update(r.seen)
            if r.inapplicable:  # an earlier replay in this dispatch changed what this one assumed
                out.events.append(
                    Event(
                        "inapplicable",
                        f"{w.hash} no longer applies: {r.reason}",
                        {"witness": w.hash, "step": r.step},
                    )
                )
                continue
            if r.kind != "completed":
                self._record(w, "replayed", "side_exit", world, cap, args, r, out)
                out.events.append(
                    Event(
                        "side_exit",
                        f"{w.hash} stopped after acting: {r.reason}",
                        {"witness": w.hash, "acts": r.acts},
                    )
                )
                if self.policy.stop_after_failed_replay:
                    return False
                continue
            holds, why = self.goal_holds(cap, args, world)
            verdict = "pass" if holds else "fail"
            self._record(w, "replayed", verdict, world, cap, args, r, out, why)
            if holds:
                out.kind = "replayed"
                out.witness = w
                out.events.append(
                    Event("replayed", f"{w.hash} replayed and the goal holds", {"witness": w.hash})
                )
                return True
            out.events.append(
                Event(
                    "checker_fail",
                    f"{w.hash} replayed but the goal does not hold",
                    {"witness": w.hash, "acts": r.acts},
                )
            )
            if self.policy.stop_after_failed_replay:
                return False
        return False

    def _record(
        self,
        w: Witness,
        kind: str,
        verdict: str,
        world: Any,
        cap: Capability,
        args: list[Any],
        r: ReplayOutcome,
        out: Outcome,
        note: str = "",
    ) -> None:
        w.evidence.append(
            Evidence(
                kind,
                verdict,
                world.label,
                dict(zip(cap.params, args, strict=True)),
                len(r.acts),
                note or r.reason,
            )
        )

    def _synthesize(self, cap: Capability, args: list[Any], world: Any, out: Outcome) -> Outcome:
        if self.oracle is None:
            out.reason = "no oracle configured"
            out.events.append(Event("failed", out.reason))
            return out
        if cap.tactic is not None:
            return self._run_tactic(cap, args, world, out)
        attempts: list[dict[str, Any]] = []
        for n in range(self.policy.max_attempts):
            prompt = self._prompt(cap, args, world, attempts, out)
            try:
                src = self.oracle.ask(prompt)
            except OracleExhausted as err:
                out.reason = str(err)
                out.events.append(Event("failed", f"oracle: {err}"))
                return out
            self.oracle_calls += 1
            out.oracle_calls += 1
            out.events.append(Event("synth", f"proposal {n + 1} requested from the oracle", {"source": src}))
            res = self.propose(src, world, cap, args)
            out.events.extend(res.events)
            out.acts.extend(res.acts)
            if res.ok:
                out.kind = "synthesized"
                out.witness = res.witness
                return out
            attempts.append({"source": src, "outcome": res.kind, "reason": res.reason})
        out.reason = f"no proposal passed after {self.policy.max_attempts} attempts"
        out.events.append(Event("failed", out.reason))
        return out

    def _prompt(
        self, cap: Capability, args: list[Any], world: Any, attempts: list[dict[str, Any]], out: Outcome
    ) -> dict[str, Any]:
        return {
            "cap": cap.name,
            "params": cap.params,
            "args": dict(zip(cap.params, args, strict=True)),
            "goal": cap.goal_src,
            "doc": cap.doc,
            "world": {name: f"{kind}/{arity}" for name, (kind, arity) in self.library.signature.items()},
            "grant": sorted(self.policy.grant),
            "dispatch": [
                e.text for e in out.events if e.kind in ("inapplicable", "side_exit", "checker_fail")
            ],
            "observed": dict(out.seen),
            "situation": self.policy.probe(world, cap.name, list(args)) if self.policy.probe else None,
            "attempts": attempts,
        }

    def _run_tactic(self, cap: Capability, args: list[Any], world: Any, out: Outcome) -> Outcome:
        assert cap.tactic is not None
        calls_before = getattr(self.oracle, "calls", 0)
        ctx = _TacticContext(self, cap, args, out)
        t = self._tracer(world, "tactic")
        t.runtime = ctx
        result = run_script(t, cap.tactic.params, args, cap.tactic.body)
        out.oracle_calls += getattr(self.oracle, "calls", 0) - calls_before
        if not result.ok:
            out.events.append(Event("tactic_error", result.error or "tactic failed"))
        if not ctx.successes:
            out.reason = "the tactic validated no proposal in this invocation"
            out.events.append(Event("failed", out.reason))
            return out
        holds, why = self.goal_holds(cap, args, world)
        if not holds:
            out.reason = why or "a proposal passed during the tactic but the goal does not hold now"
            out.events.append(Event("failed", out.reason))
            return out
        out.kind = "synthesized"
        out.witness = ctx.successes[-1]
        return out

    # -- proposals ---------------------------------------------------------------------
    def propose(self, src: str, world: Any, cap: Capability, args: list[Any]) -> ProposalResult:
        """Statically check a proposed script, run it once under the goal, retain it on a pass."""
        try:
            prog = parse_program(src)
        except Exception as err:  # ParseError or LexError
            return ProposalResult(False, "refused", f"parse error: {err}")
        scripts = [d for d in prog.decls if isinstance(d, A.ScriptDecl)]
        if len(scripts) != 1 or scripts[0].name != cap.name:
            return ProposalResult(
                False, "refused", f"a proposal must be exactly one `script {cap.name}(...)`"
            )
        decl = scripts[0]
        if decl.params != cap.params:
            return ProposalResult(
                False, "refused", f"script parameters must be {cap.params}, got {decl.params}"
            )
        row, errors = check_script(
            decl.params, decl.body, self.library.signature, self.policy.grant, self.library.arities()
        )
        if decl.when is not None:
            _, when_errors = check_goal(decl.params, decl.when, self.library.signature)
            errors += [StaticError(e.kind, f"when-clause: {e.msg}", e.line) for e in when_errors]
        if errors:
            reason = "; ".join(str(e) for e in errors)
            return ProposalResult(
                False, "refused", reason, events=[Event("refused", f"static check: {reason}")]
            )
        tracer = self._tracer(world, "script")
        body = decl.body if decl.when is None else _guarded_body(decl)
        run = run_script(tracer, decl.params, args, body)
        acts = [(s["name"], s["args"]) for s in run.steps if s["op"] == "act"] + tracer.nested_acts
        events = [Event("ran", f"ran under the goal; effect row {row.describe()}", {"steps": len(run.steps)})]
        if not run.ok:
            events.append(Event("error", f"script stopped: {run.error}", {"acts": acts}))
            return ProposalResult(False, "error", run.error or "error", acts=acts, events=events)
        holds, why = self.goal_holds(cap, args, world)
        if not holds:
            reason = why or "the goal does not hold after the run"
            events.append(Event("checker_fail", reason, {"acts": acts}))
            return ProposalResult(False, "fail", reason, acts=acts, events=events)
        w = witness_from_steps(cap.name, cap.params, run.steps, decl.source)
        kept = self.library.retain(
            w, Evidence("validated", "pass", world.label, dict(zip(cap.params, args, strict=True)), len(acts))
        )
        guards = "; ".join(describe_guards(kept)) or "none"
        events.append(
            Event(
                "retained", f"goal holds; retained {kept.hash} with guards: {guards}", {"witness": kept.hash}
            )
        )
        return ProposalResult(True, "retained", witness=kept, acts=acts, events=events)


def _guarded_body(decl: A.ScriptDecl) -> A.Block:
    """A `when` clause runs first, inside the traced run, so its decisions become guards:
    `if not (when) { fail("when-clause is false") }` followed by the body."""
    assert decl.when is not None
    check = A.If(
        A.Unary("not", decl.when, line=decl.line),
        A.Block(
            [
                A.ExprStmt(
                    A.Call(
                        A.Var("fail", line=decl.line),
                        [A.StrLit("when-clause is false", line=decl.line)],
                        line=decl.line,
                    ),
                    line=decl.line,
                )
            ],
            line=decl.line,
        ),
        None,
        line=decl.line,
    )
    return A.Block([A.ExprStmt(check, line=decl.line), *decl.body.stmts], line=decl.line)


class _TacticContext:
    """What a running tactic sees as its runtime: `use` and `propose` bound to its capability."""

    def __init__(self, rt: Runtime, cap: Capability, args: list[Any], out: Outcome):
        self.rt, self.cap, self.args, self.out = rt, cap, args, out
        self.successes: list[Witness] = []  # proposals validated by this invocation, in order

    def achieve(self, cap: str, args: list[Any], world: Any) -> Outcome:
        return self.rt.achieve(cap, args, world)

    def propose(self, src: str, world: Any) -> ProposalResult:
        self.out.events.append(Event("synth", "tactic proposed a script", {"source": src}))
        res = self.rt.propose(src, world, self.cap, self.args)
        self.out.events.extend(res.events)
        self.out.acts.extend(res.acts)
        if res.ok and res.witness is not None:
            self.successes.append(res.witness)
        return res
