"""The three arms. Each exposes achieve(world, service) -> EpisodeResult and shares the
same world, incidents, goal, grant and oracle; they differ only in how a retained
script is chosen for reuse:

  rote        inferred guards (the runtime from rote/)
  precond     the author (the oracle) writes a `when` clause; it is evaluated, the body is trusted
  model_each  the model is asked every episode, shown the retained library
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any

from rote import ast as A
from rote.effects import check_goal, check_script
from rote.eval import Tracer, concretize, run_script
from rote.library import Library
from rote.parser import parse_program
from rote.runtime import Policy, Runtime
from rote.synth import OracleExhausted

from .oracles import probe6
from .world6 import GRANT6, LIBRARY6

__all__ = ["EpisodeResult", "RoteArm", "PrecondArm", "ModelEachArm", "make_arm"]


@dataclass
class EpisodeResult:
    outcome: str
    oracle_calls: int = 0
    notes: list[str] = field(default_factory=list)
    retained: int = 0  # library size after the episode


def _prompt(
    cap_goal: str, world: Any, s: str, attempts: list[dict[str, Any]], fmt: str, **extra: Any
) -> dict[str, Any]:
    return {
        "cap": "heal",
        "params": ["service"],
        "args": {"service": s},
        "goal": cap_goal,
        "world": {n: f"{k}/{a}" for n, (k, a) in world.signature().items()},
        "grant": sorted(GRANT6),
        "situation": probe6(world, "heal", [s]),
        "attempts": attempts,
        "format": fmt,
        **extra,
    }


class RoteArm:
    name = "rote"
    revalidate = False

    def __init__(self, oracle: Any):
        self.lib = Library.from_source(LIBRARY6)
        self.oracle = oracle
        self.rt = Runtime(
            self.lib,
            Policy(grant=GRANT6, max_attempts=3, probe=probe6, revalidate_sources=self.revalidate),
            oracle,
        )

    def achieve(self, world: Any, s: str) -> EpisodeResult:
        out = self.rt.achieve("heal", [s], world)
        notes = [
            e.text
            for e in out.events
            if e.kind in ("inapplicable", "checker_fail", "side_exit", "refused", "failed")
        ]
        return EpisodeResult(out.kind, out.oracle_calls, notes[:8], len(self.lib.caps["heal"].witnesses))


@dataclass
class _Skill:
    when: A.Expr
    body: A.Block
    source: str
    seq: int
    passes: int = 0
    fails: int = 0


class _Base:
    def __init__(self, oracle: Any):
        self.lib = Library.from_source(LIBRARY6)
        self.cap = self.lib.caps["heal"]
        self.oracle = oracle
        self.rt = Runtime(
            self.lib, Policy(grant=GRANT6, max_attempts=3, probe=probe6), oracle
        )  # for `use` and the goal

    def goal(self, world: Any, s: str) -> bool:
        holds, _ = self.rt.goal_holds(self.cap, [s], world)
        return bool(holds)

    def parse(self, src: str) -> tuple[A.ScriptDecl | None, str]:
        try:
            prog = parse_program(src)
        except Exception as err:  # noqa: BLE001
            return None, f"parse error: {err}"
        decls = [d for d in prog.decls if isinstance(d, A.ScriptDecl)]
        if len(decls) != 1 or decls[0].name != "heal" or decls[0].params != ["service"]:
            return None, "a proposal must be exactly one `script heal(service)`"
        _, errs = check_script(["service"], decls[0].body, self.lib.signature, GRANT6, self.lib.arities())
        if decls[0].when is not None:
            _, werrs = check_goal(["service"], decls[0].when, self.lib.signature)
            errs += werrs
        if errs:
            return None, "static check: " + "; ".join(str(e) for e in errs)
        return decls[0], ""

    def run_body(self, body: A.Block, world: Any, s: str) -> tuple[bool, str, int]:
        """Trusted execution: no guards are kept. Returns (ok, error, acts performed)."""
        before = len(world.history)
        t = Tracer(world, grant=set(GRANT6), mode="script", caps=self.lib.arities(), runtime=self.rt)
        res = run_script(t, ["service"], [s], body)
        return res.ok, res.error or "", len(world.history) - before


class PrecondArm(_Base):
    name = "precond"

    def __init__(self, oracle: Any):
        super().__init__(oracle)
        self.skills: list[_Skill] = []

    def applies(self, sk: _Skill, world: Any, s: str) -> bool:
        t = Tracer(world, mode="goal", caps={})
        try:
            v = concretize(t.run_body(["service"], [s], A.Block([A.ExprStmt(sk.when)])))
        except Exception:  # noqa: BLE001  a precondition that cannot be evaluated does not apply
            return False
        return v is True

    def achieve(self, world: Any, s: str) -> EpisodeResult:
        if self.goal(world, s):
            return EpisodeResult("already_satisfied", retained=len(self.skills))
        notes: list[str] = []
        for sk in sorted(self.skills, key=lambda k: (k.fails, -k.seq)):
            if not self.applies(sk, world, s):
                continue
            ok, err, acts = self.run_body(sk.body, world, s)
            if ok and self.goal(world, s):
                sk.passes += 1
                return EpisodeResult("replayed", 0, notes, len(self.skills))
            sk.fails += 1
            notes.append(f"skill {sk.seq} applied but failed ({err or 'goal false'}) after {acts} act(s)")
            if acts:
                break
        return self.synthesize(world, s, notes)

    def synthesize(self, world: Any, s: str, notes: list[str]) -> EpisodeResult:
        attempts: list[dict[str, Any]] = []
        calls = 0
        for _ in range(3):
            try:
                src = self.oracle.ask(
                    _prompt(self.cap.goal_src, world, s, attempts, "when", dispatch=notes[-4:])
                )
            except OracleExhausted as err:
                return EpisodeResult("failed", calls, [*notes, str(err)], len(self.skills))
            calls += 1
            decl, err = self.parse(src)
            if decl is None or decl.when is None:
                attempts.append(
                    {"source": src, "outcome": "refused", "reason": err or "missing `when` clause"}
                )
                continue
            sk = _Skill(decl.when, decl.body, src, len(self.skills) + 1)
            if not self.applies(sk, world, s):
                attempts.append(
                    {
                        "source": src,
                        "outcome": "refused",
                        "reason": "the when-clause is false in the current situation",
                    }
                )
                continue
            ok, rerr, _ = self.run_body(decl.body, world, s)
            if ok and self.goal(world, s):
                self.skills.append(sk)
                return EpisodeResult("synthesized", calls, notes, len(self.skills))
            attempts.append(
                {"source": src, "outcome": "fail", "reason": rerr or "the goal does not hold after the run"}
            )
        return EpisodeResult("failed", calls, [*notes, "no proposal passed"], len(self.skills))


class ModelEachArm(_Base):
    name = "model_each"

    def __init__(self, oracle: Any):
        super().__init__(oracle)
        self.library: list[dict[str, Any]] = []

    def achieve(self, world: Any, s: str) -> EpisodeResult:
        if self.goal(world, s):
            return EpisodeResult("already_satisfied", retained=len(self.library))
        attempts: list[dict[str, Any]] = []
        calls = 0
        for _ in range(3):
            shown = [{"source": e["source"], "solved": e["solved"]} for e in self.library]
            try:
                src = self.oracle.ask(
                    _prompt(self.cap.goal_src, world, s, attempts, "library", library=shown)
                )
            except OracleExhausted as err:
                return EpisodeResult("failed", calls, [str(err)], len(self.library))
            calls += 1
            decl, err = self.parse(src)
            if decl is None:
                attempts.append({"source": src, "outcome": "refused", "reason": err})
                continue
            ok, rerr, _ = self.run_body(decl.body, world, s)
            if ok and self.goal(world, s):
                self.remember(src, probe6(world, "heal", [s]))
                return EpisodeResult("synthesized", calls, [], len(self.library))
            attempts.append(
                {"source": src, "outcome": "fail", "reason": rerr or "the goal does not hold after the run"}
            )
        return EpisodeResult("failed", calls, ["no proposal passed"], len(self.library))

    def remember(self, src: str, sit: dict[str, Any]) -> None:
        summary = {k: sit[k] for k in ("log_tail", "lock_held", "disk") if k in sit}
        for e in self.library:
            if e["source"].strip() == src.strip():
                e["solved"].append(summary)
                return
        self.library.append({"source": src, "solved": [summary]})


class RoteRevalArm(RoteArm):
    """Rote with `revalidate_sources`: on a miss, known sources are re-run under the goal
    (taking unvalidated paths) before the oracle is asked."""

    name = "rote_reval"
    revalidate = True


def make_arm(name: str, oracle: Any) -> Any:
    return {"rote": RoteArm, "rote_reval": RoteRevalArm, "precond": PrecondArm, "model_each": ModelEachArm}[
        name
    ](oracle)
