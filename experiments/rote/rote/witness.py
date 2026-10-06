"""Witnesses and the replay kernel.

A witness is the straight-line residue of one validated run: observe/act/use steps
whose arguments are symbolic expressions over the parameters and earlier steps, with
guards interleaved exactly where the run made a decision. The steps before the first
act or use are the witness's *applicability prefix*: replaying them touches nothing.

`replay` is the trusted kernel. It is deliberately small, needs no parser and no
evaluator, and could be rewritten in any host language against the JSON form.
"""

from __future__ import annotations

import copy
import hashlib
from dataclasses import dataclass, field
from typing import Any

from .sym import SymError, canonical, deep_eq, eval_sexpr, show

__all__ = ["Witness", "Evidence", "ReplayOutcome", "replay", "witness_from_steps", "describe_guards"]


@dataclass
class Evidence:
    kind: str  # "validated" | "replayed"
    verdict: str  # "pass" | "fail" | "side_exit" | "error"
    world: str
    args: dict[str, Any]
    acts: int
    note: str = ""

    def to_json(self) -> dict[str, Any]:
        return {
            "kind": self.kind,
            "verdict": self.verdict,
            "world": self.world,
            "args": self.args,
            "acts": self.acts,
            "note": self.note,
        }

    @staticmethod
    def from_json(d: dict[str, Any]) -> Evidence:
        return Evidence(d["kind"], d["verdict"], d["world"], d["args"], d["acts"], d.get("note", ""))


@dataclass
class Witness:
    cap: str
    params: list[str]
    steps: list[dict[str, Any]]
    source: str
    hash: str = ""
    evidence: list[Evidence] = field(default_factory=list)
    seq: int = 0  # order of retention in the library

    def __post_init__(self) -> None:
        if not self.hash:
            self.hash = "w" + hashlib.sha256(canonical([self.params, self.steps]).encode()).hexdigest()[:10]

    @property
    def prefix_len(self) -> int:
        for i, s in enumerate(self.steps):
            if s["op"] in ("act", "use"):
                return i
        return len(self.steps)

    @property
    def guards(self) -> list[dict[str, Any]]:
        return [s for s in self.steps if s["op"] == "guard"]

    def counts(self) -> tuple[int, int]:
        passes = sum(1 for e in self.evidence if e.verdict == "pass")
        fails = sum(1 for e in self.evidence if e.verdict in ("fail", "side_exit", "error"))
        return passes, fails

    def refnames(self) -> dict[int, str]:
        names: dict[int, str] = {}
        for s in self.steps:
            if s["op"] in ("observe", "act", "use"):
                label = s.get("name") or s.get("cap")
                names[s["id"]] = f"{label}(" + ", ".join(show(a, names) for a in s["args"]) + ")"
        return names

    def pretty(self) -> str:
        names = self.refnames()
        lines = []
        for i, s in enumerate(self.steps):
            marker = "   " if i < self.prefix_len else " * "
            if s["op"] == "guard":
                lines.append(f"{marker}guard  {show(s['pred'], names)} == {show(['const', s['expect']])}")
            elif s["op"] == "eval":
                lines.append(f"{marker}eval   {show(s['expr'], names)}")
            elif s["op"] == "use":
                lines.append(f"{marker}use    #{s['id']} = {names[s['id']]}")
            else:
                lines.append(f"{marker}{s['op']:<7}#{s['id']} = {names[s['id']]}")
        return "\n".join(lines)

    def to_json(self) -> dict[str, Any]:
        return {
            "hash": self.hash,
            "cap": self.cap,
            "params": self.params,
            "steps": self.steps,
            "source": self.source,
            "seq": self.seq,
            "evidence": [e.to_json() for e in self.evidence],
        }

    @staticmethod
    def from_json(d: dict[str, Any]) -> Witness:
        return Witness(
            d["cap"],
            d["params"],
            d["steps"],
            d["source"],
            d["hash"],
            [Evidence.from_json(e) for e in d.get("evidence", [])],
            d.get("seq", 0),
        )


def witness_from_steps(cap: str, params: list[str], steps: list[dict[str, Any]], source: str) -> Witness:
    """Drop what replay would check anyway. A guard identical to an earlier one is redundant
    (guards are functions of parameters and step results, which never change within a run).
    An `eval` step is redundant when its expression is a subtree of something replay has
    already evaluated, or of a guard or argument it will evaluate before the next action."""
    seen: set[str] = set()
    evaluated: list[SExprLike] = []
    kept: list[dict[str, Any]] = []
    for i, s in enumerate(steps):
        if s["op"] == "guard":
            key = canonical([s["pred"], s["expect"]])
            if key in seen:
                continue
            seen.add(key)
            evaluated.append(s["pred"])
        elif s["op"] == "eval":
            if _covered(s["expr"], evaluated) or _covered(s["expr"], _window_exprs(steps, i)):
                continue
            evaluated.append(s["expr"])
        else:
            evaluated.extend(s.get("args", []))
        kept.append(dict(s))
    return Witness(cap, params, kept, source)


SExprLike = list


def _window_exprs(steps: list[dict[str, Any]], start: int) -> list[SExprLike]:
    """Expressions replay evaluates after step `start`, up to and including the next action."""
    out: list[SExprLike] = []
    for s in steps[start + 1 :]:
        if s["op"] == "guard":
            out.append(s["pred"])
        elif s["op"] == "eval":
            continue
        else:
            out.extend(s.get("args", []))
            return out
    return out


def _covered(needle: SExprLike, haystacks: list[SExprLike]) -> bool:
    return any(_subtree(h, needle) for h in haystacks)


def _subtree(hay: Any, needle: SExprLike) -> bool:
    if hay == needle:
        return True
    if not isinstance(hay, list) or not hay or not isinstance(hay[0], str):
        return False
    if hay[0] == "list":
        return any(_subtree(x, needle) for x in hay[1])
    if hay[0] == "record":
        return any(_subtree(x, needle) for _, x in hay[1])
    return any(_subtree(x, needle) for x in hay[1:] if isinstance(x, list))


def describe_guards(w: Witness) -> list[str]:
    names = w.refnames()
    return [f"{show(g['pred'], names)} == {show(['const', g['expect']])}" for g in w.guards]


@dataclass
class ReplayOutcome:
    kind: str  # "completed" | "applicable" | "side_exit" | "error" | "refused"
    acts: list[tuple[str, list[Any]]]
    step: int = -1
    reason: str = ""
    seen: dict[str, Any] = field(default_factory=dict)  # rendered observation -> fresh value

    @property
    def acted(self) -> bool:
        return len(self.acts) > 0

    @property
    def inapplicable(self) -> bool:
        """Stopped inside the applicability prefix: nothing was changed."""
        return self.kind in ("side_exit", "error") and not self.acted


def replay(  # noqa: C901, PLR0912
    w: Witness, args: list[Any], world: Any, grant: set[str], runtime: Any = None, prefix_only: bool = False
) -> ReplayOutcome:
    """Re-run a witness against fresh world answers. Stops at the first guard that no
    longer holds, before performing anything that followed it. With `prefix_only`, stops
    before the first act/use: an applicability check that touches nothing."""
    if len(args) != len(w.params):
        return ReplayOutcome("refused", [], reason=f"expected {len(w.params)} arguments, got {len(args)}")
    for s in w.steps:
        if s["op"] == "act" and s["name"] not in grant:
            return ReplayOutcome("refused", [], reason=f"action {s['name']!r} is not in the caller's grant")
    params = dict(zip(w.params, args, strict=True))
    refs: dict[int, Any] = {}
    acts: list[tuple[str, list[Any]]] = []
    names = w.refnames()
    seen: dict[str, Any] = {}
    for i, s in enumerate(w.steps):
        if prefix_only and s["op"] in ("act", "use"):
            return ReplayOutcome("applicable", acts, i, seen=seen)
        try:
            if s["op"] == "eval":
                eval_sexpr(s["expr"], params, refs)
                continue
            if s["op"] == "guard":
                got = eval_sexpr(s["pred"], params, refs)
                if not deep_eq(got, s["expect"]):
                    reason = (
                        f"guard {show(s['pred'], names)} expected "
                        f"{show(['const', s['expect']])}, got {show(['const', got])}"
                    )
                    return ReplayOutcome("side_exit", acts, i, reason, seen)
                continue
            cargs = [eval_sexpr(a, params, refs) for a in s["args"]]
            if s["op"] == "observe":
                refs[s["id"]] = copy.deepcopy(world.observe(s["name"], cargs))
                seen[f"{s['name']}({', '.join(repr(a) for a in cargs)})"] = refs[s["id"]]
            elif s["op"] == "act":
                refs[s["id"]] = copy.deepcopy(world.act(s["name"], cargs))
                acts.append((s["name"], cargs))
            else:
                if runtime is None:
                    return ReplayOutcome("error", acts, i, "no runtime for `use`")
                outcome = runtime.achieve(s["cap"], cargs, world)
                acts.extend(outcome.acts)
                refs[s["id"]] = copy.deepcopy(outcome.summary())
        except SymError as err:
            return ReplayOutcome(
                "side_exit", acts, i, f"step {i} ({names.get(s.get('id', -1), s['op'])}): {err}", seen
            )
    return ReplayOutcome("completed", acts, seen=seen)
