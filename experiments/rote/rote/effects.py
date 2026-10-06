"""Static analysis: name resolution, arity, and the effect row of a body.

The effect row is the set of world functions and capability uses a body can reach.
World functions are not first-class, so the row is exact and computable from syntax.
Three checks are derived from it:

  check_script  -- no `ask`/`propose`; every act is in the caller's grant
  check_goal    -- observe only (no act, use, ask, propose)
  check_tactic  -- anything goes, but names must still resolve

A failed check refuses the body before it runs: nothing is observed, nothing acted on.
"""

from __future__ import annotations

from dataclasses import dataclass, field

from . import ast as A
from .eval import BUILTINS

__all__ = ["EffectRow", "StaticError", "analyze", "check_script", "check_goal", "check_tactic"]


@dataclass
class EffectRow:
    observes: set[str] = field(default_factory=set)
    acts: set[str] = field(default_factory=set)
    uses: set[str] = field(default_factory=set)
    asks: bool = False
    proposes: bool = False

    def describe(self) -> str:
        parts = []
        if self.observes:
            parts.append("observe{" + ", ".join(sorted(self.observes)) + "}")
        if self.acts:
            parts.append("act{" + ", ".join(sorted(self.acts)) + "}")
        if self.uses:
            parts.append("use{" + ", ".join(sorted(self.uses)) + "}")
        if self.asks:
            parts.append("ask")
        if self.proposes:
            parts.append("propose")
        return " + ".join(parts) if parts else "pure"


@dataclass
class StaticError:
    kind: str
    msg: str
    line: int

    def __str__(self) -> str:
        return f"line {self.line}: {self.msg}"


class _Scope:
    def __init__(self, parent: _Scope | None = None, names: set[str] | None = None):
        self.names = set(names or ())
        self.parent = parent

    def has(self, name: str) -> bool:
        s: _Scope | None = self
        while s is not None:
            if name in s.names:
                return True
            s = s.parent
        return False


class _Analyzer:
    def __init__(self, sig: dict[str, tuple[str, int]], caps: dict[str, int]):
        self.sig = sig
        self.caps = caps
        self.row = EffectRow()
        self.errors: list[StaticError] = []

    def err(self, kind: str, msg: str, line: int) -> None:
        self.errors.append(StaticError(kind, msg, line))

    def body(self, params: list[str], body: A.Expr) -> None:
        self.expr(body, _Scope(None, set(params)))

    def block(self, b: A.Block, scope: _Scope) -> None:
        inner = _Scope(scope)
        for s in b.stmts:
            self.stmt(s, inner)

    def stmt(self, s: A.Stmt, scope: _Scope) -> None:
        if isinstance(s, A.Let):
            self.expr(s.value, scope)
            scope.names.add(s.name)
            return
        if isinstance(s, A.For):
            self.expr(s.iterable, scope)
            self.block(s.body, _Scope(scope, {s.var}))
            return
        assert isinstance(s, A.ExprStmt)
        self.expr(s.expr, scope)

    def expr(self, e: A.Expr, scope: _Scope) -> None:  # noqa: C901, PLR0912
        if isinstance(e, A.IntLit | A.StrLit | A.BoolLit | A.UnitLit):
            return
        if isinstance(e, A.Var):
            if not scope.has(e.name):
                self.unknown_var(e)
            return
        if isinstance(e, A.ListLit):
            for x in e.items:
                self.expr(x, scope)
            return
        if isinstance(e, A.RecordLit):
            for _, x in e.fields:
                self.expr(x, scope)
            return
        if isinstance(e, A.Lambda):
            self.block(e.body, _Scope(scope, set(e.params)))
            return
        if isinstance(e, A.Block):
            self.block(e, scope)
            return
        if isinstance(e, A.If):
            self.expr(e.cond, scope)
            self.block(e.then, scope)
            if e.otherwise is not None:
                self.expr(e.otherwise, scope)
            return
        if isinstance(e, A.Unary):
            self.expr(e.operand, scope)
            return
        if isinstance(e, A.Binary):
            self.expr(e.left, scope)
            self.expr(e.right, scope)
            return
        if isinstance(e, A.FieldAccess):
            self.expr(e.obj, scope)
            return
        if isinstance(e, A.IndexAccess):
            self.expr(e.obj, scope)
            self.expr(e.index, scope)
            return
        if isinstance(e, A.Call):
            self.call(e, scope)
            return
        if isinstance(e, A.Use):
            self.use(e, scope)
            return
        if isinstance(e, A.Ask):
            self.row.asks = True
            self.expr(e.prompt, scope)
            return
        if isinstance(e, A.Propose):
            self.row.proposes = True
            self.expr(e.source, scope)
            return
        if isinstance(e, A.Return):
            if e.value is not None:
                self.expr(e.value, scope)
            return
        self.err("internal", f"unhandled node {type(e).__name__}", e.line)

    def unknown_var(self, e: A.Var) -> None:
        if e.name in self.sig or e.name in BUILTINS:
            self.err("name", f"{e.name!r} must be called, not used as a value", e.line)
            return
        self.err("name", f"unknown name {e.name!r}", e.line)

    def call(self, e: A.Call, scope: _Scope) -> None:
        for a in e.args:
            self.expr(a, scope)
        if not isinstance(e.func, A.Var) or scope.has(e.func.name):
            if not isinstance(e.func, A.Var):
                self.expr(e.func, scope)
            return
        name = e.func.name
        if name in self.sig:
            kind, arity = self.sig[name]
            if len(e.args) != arity:
                self.err("arity", f"{name} expects {arity} arguments, got {len(e.args)}", e.line)
            (self.row.observes if kind == "observe" else self.row.acts).add(name)
            return
        if name in BUILTINS:
            return
        self.err("name", f"unknown function {name!r}", e.line)

    def use(self, e: A.Use, scope: _Scope) -> None:
        for a in e.args:
            self.expr(a, scope)
        if e.cap not in self.caps:
            self.err("name", f"unknown capability {e.cap!r}", e.line)
            return
        if len(e.args) != self.caps[e.cap]:
            self.err("arity", f"{e.cap} expects {self.caps[e.cap]} arguments, got {len(e.args)}", e.line)
        self.row.uses.add(e.cap)


def analyze(
    params: list[str], body: A.Expr, sig: dict[str, tuple[str, int]], caps: dict[str, int] | None = None
) -> tuple[EffectRow, list[StaticError]]:
    an = _Analyzer(sig, caps or {})
    an.body(params, body)
    return an.row, an.errors


def check_script(
    params: list[str],
    body: A.Expr,
    sig: dict[str, tuple[str, int]],
    grant: set[str],
    caps: dict[str, int] | None = None,
) -> tuple[EffectRow, list[StaticError]]:
    row, errors = analyze(params, body, sig, caps)
    if row.asks:
        errors.append(
            StaticError(
                "effect",
                "a script may not `ask`: retained capabilities must run without an oracle",
                body.line,
            )
        )
    if row.proposes:
        errors.append(StaticError("effect", "a script may not `propose`", body.line))
    for act in sorted(row.acts - grant):
        errors.append(StaticError("grant", f"action {act!r} is not in the caller's grant", body.line))
    return row, errors


def check_goal(
    params: list[str], goal: A.Expr, sig: dict[str, tuple[str, int]]
) -> tuple[EffectRow, list[StaticError]]:
    row, errors = analyze(params, goal, sig, {})
    for act in sorted(row.acts):
        errors.append(StaticError("effect", f"a goal may only observe; it calls act {act!r}", goal.line))
    if row.asks or row.proposes:
        errors.append(StaticError("effect", "a goal may not ask or propose", goal.line))
    return row, errors


def check_tactic(
    params: list[str], body: A.Expr, sig: dict[str, tuple[str, int]], caps: dict[str, int] | None = None
) -> tuple[EffectRow, list[StaticError]]:
    return analyze(params, body, sig, caps)
