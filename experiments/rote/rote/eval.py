"""The tracing evaluator.

Every value that depends on a parameter or on the world is a `Sym`: a concrete value
shadowed by a symbolic expression (see sym.py). The evaluator computes concretely,
but whenever it must *decide* something from a Sym -- which branch to take, how many
times to loop, whether a field exists, whether an index is in range, whether a divisor
is zero -- it records a guard: the expression and the value it had. Effects (observe,
act, use) are recorded as steps. The step list is the witness; the guards are the
run's inferred world-assumptions. Nothing here reasons about the world: it only
mediates every operation, which is what makes the guard set complete for the path.
"""

from __future__ import annotations

import copy
from dataclasses import dataclass, field
from typing import Any, Protocol

from . import ast as A
from .sym import INT_MAX, INT_MIN, SExpr, _contains, const, deep_eq

__all__ = [
    "Sym",
    "Closure",
    "RoteError",
    "ScriptFailure",
    "FuelExhausted",
    "World",
    "Oracle",
    "Tracer",
    "concretize",
    "lift",
    "contains_sym",
    "BUILTINS",
    "run_script",
    "RunResult",
]


class RoteError(Exception):
    def __init__(self, kind: str, msg: str, line: int = 0):
        super().__init__(f"line {line}: {msg}" if line else msg)
        self.kind = kind
        self.msg = msg
        self.line = line


class ScriptFailure(Exception):
    """`fail(msg)` was called: the script declares it cannot proceed."""


class FuelExhausted(Exception):
    pass


class _Return(Exception):
    def __init__(self, value: Any):
        self.value = value


@dataclass(frozen=True)
class Sym:
    expr: SExpr
    value: Any  # fully concrete

    def __repr__(self) -> str:
        return f"Sym({self.expr!r} = {self.value!r})"


@dataclass
class Closure:
    params: list[str]
    body: A.Block
    env: Env


class Env:
    __slots__ = ("vars", "parent")

    def __init__(self, parent: Env | None = None):
        self.vars: dict[str, Any] = {}
        self.parent = parent

    def lookup(self, name: str) -> tuple[bool, Any]:
        env: Env | None = self
        while env is not None:
            if name in env.vars:
                return True, env.vars[name]
            env = env.parent
        return False, None


class World(Protocol):
    label: str

    def signature(self) -> dict[str, tuple[str, int]]: ...
    def observe(self, name: str, args: list[Any]) -> Any: ...
    def act(self, name: str, args: list[Any]) -> Any: ...


class Oracle(Protocol):
    def ask(self, prompt: Any) -> Any: ...


def contains_sym(v: Any) -> bool:
    if isinstance(v, Sym):
        return True
    if isinstance(v, list):
        return any(contains_sym(x) for x in v)
    if isinstance(v, dict):
        return any(contains_sym(x) for x in v.values())
    return False


def concretize(v: Any) -> Any:
    if isinstance(v, Sym):
        return v.value
    if isinstance(v, list):
        return [concretize(x) for x in v]
    if isinstance(v, dict):
        return {k: concretize(x) for k, x in v.items()}
    if isinstance(v, Closure):
        raise RoteError("type", "a function cannot be passed to the world or compared")
    return v


def lift(v: Any) -> SExpr:
    """The symbolic expression for a value; containers with Sym children stay structured."""
    if isinstance(v, Sym):
        return v.expr
    if isinstance(v, Closure):
        raise RoteError("type", "a function cannot flow into observed data")
    if not contains_sym(v):
        return const(concretize(v))
    if isinstance(v, list):
        return ["list", [lift(x) for x in v]]
    return ["record", [[k, lift(x)] for k, x in v.items()]]


def _tname(v: Any) -> str:
    if isinstance(v, Sym):
        return _tname(v.value)
    if v is None:
        return "unit"
    if isinstance(v, bool):
        return "bool"
    if isinstance(v, int):
        return "int"
    if isinstance(v, str):
        return "string"
    if isinstance(v, list):
        return "list"
    if isinstance(v, dict):
        return "record"
    if isinstance(v, Closure):
        return "fn"
    return type(v).__name__


def _to_str(v: Any) -> str:
    from .sym import _to_str as s

    return s(v)


@dataclass
class RunResult:
    steps: list[dict[str, Any]]
    value: Any
    error: str | None = None
    error_kind: str | None = None
    fuel_used: int = 0

    @property
    def ok(self) -> bool:
        return self.error is None


@dataclass
class Tracer:
    """One run of one body against one world. `mode` is "script", "goal" or "tactic"."""

    world: World
    grant: set[str] = field(default_factory=set)
    mode: str = "script"
    fuel: int = 20_000
    caps: dict[str, int] = field(default_factory=dict)  # cap name -> arity, for `use`
    runtime: Any = None  # provides achieve(cap, args, world) and propose(cap, source)
    oracle: Oracle | None = None
    steps: list[dict[str, Any]] = field(default_factory=list)
    nested_acts: list[tuple[str, list[Any]]] = field(default_factory=list)
    _next_id: int = 1
    fuel_used: int = 0

    # -- recording -----------------------------------------------------------
    def new_id(self) -> int:
        sid = self._next_id
        self._next_id += 1
        return sid

    def guard(self, pred: SExpr, expect: Any) -> None:
        self.steps.append({"op": "guard", "pred": pred, "expect": expect})

    def note(self, v: Any) -> Any:
        """A pure operation on observed data succeeded here. Replay must re-evaluate it, even if
        the script never uses the result, so a type or overflow failure the interpreter would
        have hit stays a side exit instead of being skipped."""
        if isinstance(v, Sym):
            self.steps.append({"op": "eval", "expr": v.expr})
        return v

    def decide_bool(self, v: Any, line: int, what: str = "condition") -> bool:
        raw = v.value if isinstance(v, Sym) else v
        if not isinstance(raw, bool):
            raise RoteError("type", f"{what} must be a bool, got {_tname(v)}", line)
        if isinstance(v, Sym):
            self.guard(v.expr, raw)
        return raw

    def elements(self, v: Any, line: int) -> list[Any]:
        """The elements of a list value; a Sym list pins its length and yields Sym elements."""
        if isinstance(v, Sym):
            if not isinstance(v.value, list):
                raise RoteError("type", f"expected a list, got {_tname(v)}", line)
            self.guard(["len", v.expr], len(v.value))
            return [Sym(["index", v.expr, const(i)], x) for i, x in enumerate(v.value)]
        if isinstance(v, list):
            return v
        raise RoteError("type", f"expected a list, got {_tname(v)}", line)

    def tick(self, line: int) -> None:
        self.fuel_used += 1
        if self.fuel_used > self.fuel:
            raise FuelExhausted(f"line {line}: fuel exhausted after {self.fuel} steps")

    # -- entry points --------------------------------------------------------
    def run_body(self, params: list[str], args: list[Any], body: A.Block) -> Any:
        if len(params) != len(args):
            raise RoteError("arity", f"expected {len(params)} arguments, got {len(args)}")
        env = Env()
        for p, a in zip(params, args, strict=True):
            env.vars[p] = Sym(["param", p], copy.deepcopy(a))
        try:
            return self.eval(body, env)
        except _Return as r:
            return r.value

    def eval_goal(self, params: list[str], args: list[Any], goal: A.Expr) -> bool:
        env = Env()
        for p, a in zip(params, args, strict=True):
            env.vars[p] = Sym(["param", p], copy.deepcopy(a))
        v = self.eval(goal, env)
        raw = concretize(v)
        if not isinstance(raw, bool):
            raise RoteError("type", f"goal must evaluate to a bool, got {_tname(v)}", goal.line)
        return raw

    # -- the evaluator -------------------------------------------------------
    def eval(self, e: A.Expr, env: Env) -> Any:  # noqa: C901, PLR0911, PLR0912
        self.tick(e.line)
        if isinstance(e, A.IntLit):
            return _ranged(e.value, e.line)
        if isinstance(e, A.StrLit | A.BoolLit):
            return e.value
        if isinstance(e, A.UnitLit):
            return None
        if isinstance(e, A.Var):
            found, v = env.lookup(e.name)
            if found:
                return v
            if e.name in self.world.signature():
                raise RoteError(
                    "name", f"world function {e.name!r} must be called, not used as a value", e.line
                )
            if e.name in BUILTINS:
                raise RoteError("name", f"builtin {e.name!r} must be called, not used as a value", e.line)
            raise RoteError("name", f"unknown name {e.name!r}", e.line)
        if isinstance(e, A.ListLit):
            return [self.eval(x, env) for x in e.items]
        if isinstance(e, A.RecordLit):
            return {k: self.eval(x, env) for k, x in e.fields}
        if isinstance(e, A.Lambda):
            return Closure(e.params, e.body, env)
        if isinstance(e, A.Block):
            return self.eval_block(e, env)
        if isinstance(e, A.If):
            return self.eval_if(e, env)
        if isinstance(e, A.Unary):
            return self.eval_unary(e, env)
        if isinstance(e, A.Binary):
            return self.eval_binary(e, env)
        if isinstance(e, A.FieldAccess):
            return self.eval_field(e, env)
        if isinstance(e, A.IndexAccess):
            return self.eval_index(e, env)
        if isinstance(e, A.Call):
            return self.eval_call(e, env)
        if isinstance(e, A.Use):
            return self.eval_use(e, env)
        if isinstance(e, A.Ask):
            return self.eval_ask(e, env)
        if isinstance(e, A.Propose):
            return self.eval_propose(e, env)
        if isinstance(e, A.Return):
            raise _Return(None if e.value is None else self.eval(e.value, env))
        raise RoteError("internal", f"cannot evaluate {type(e).__name__}", e.line)

    def eval_block(self, b: A.Block, env: Env) -> Any:
        scope = Env(env)
        result: Any = None
        for s in b.stmts:
            result = self.eval_stmt(s, scope)
        return result

    def eval_stmt(self, s: A.Stmt, scope: Env) -> Any:
        if isinstance(s, A.Let):
            scope.vars[s.name] = self.eval(s.value, scope)
            return None
        if isinstance(s, A.For):
            items = self.elements(self.eval(s.iterable, scope), s.line)
            for item in items:
                inner = Env(scope)
                inner.vars[s.var] = item
                self.eval(s.body, inner)
            return None
        assert isinstance(s, A.ExprStmt)
        return self.eval(s.expr, scope)

    def eval_if(self, e: A.If, env: Env) -> Any:
        if self.decide_bool(self.eval(e.cond, env), e.line, "if condition"):
            return self.eval(e.then, env)
        if e.otherwise is None:
            return None
        return self.eval(e.otherwise, env)

    def eval_unary(self, e: A.Unary, env: Env) -> Any:
        v = self.eval(e.operand, env)
        raw = concretize(v)
        if e.op == "not":
            if not isinstance(raw, bool):
                raise RoteError("type", f"not needs a bool, got {_tname(v)}", e.line)
            return self.note(Sym(["not", v.expr], not raw)) if isinstance(v, Sym) else (not raw)
        if not isinstance(raw, int) or isinstance(raw, bool):
            raise RoteError("type", f"negation needs an int, got {_tname(v)}", e.line)
        result = _ranged(-raw, e.line)
        return self.note(Sym(["neg", v.expr], result)) if isinstance(v, Sym) else result

    def eval_binary(self, e: A.Binary, env: Env) -> Any:
        if e.op in ("and", "or"):
            left = self.eval(e.left, env)
            lraw = self.decide_bool(left, e.line, f"left side of {e.op}")
            if (e.op == "and" and not lraw) or (e.op == "or" and lraw):
                return lraw
            right = self.eval(e.right, env)
            rraw = right.value if isinstance(right, Sym) else right
            if not isinstance(rraw, bool):
                raise RoteError("type", f"right side of {e.op} must be a bool, got {_tname(right)}", e.line)
            return right
        left, right = self.eval(e.left, env), self.eval(e.right, env)
        return self.binop(e.op, left, right, e.line)

    def binop(self, op: str, left: Any, right: Any, line: int) -> Any:
        a, b = concretize(left), concretize(right)
        symbolic = contains_sym(left) or contains_sym(right)
        if op in ("==", "!="):
            result = deep_eq(a, b) if op == "==" else not deep_eq(a, b)
            return Sym(["bin", op, lift(left), lift(right)], result) if symbolic else result
        if op == "+" and isinstance(a, str) and isinstance(b, str):
            return Sym(["bin", "+", lift(left), lift(right)], a + b) if symbolic else a + b
        if not (_is_int(a) and _is_int(b)):
            raise RoteError(
                "type", f"operator {op} needs two ints, got {_tname(left)} and {_tname(right)}", line
            )
        if op in _COMPARE:
            result = _COMPARE[op](a, b)
            return self.note(Sym(["bin", op, lift(left), lift(right)], result)) if symbolic else result
        if op in ("/", "%"):
            if isinstance(right, Sym):
                self.guard(["bin", "==", right.expr, const(0)], False)
            if b == 0:
                raise RoteError("arith", "division by zero", line)
            result = a // b if op == "/" else a % b
        else:
            result = _ARITH[op](a, b)
        result = _ranged(result, line)
        return self.note(Sym(["bin", op, lift(left), lift(right)], result)) if symbolic else result

    def eval_field(self, e: A.FieldAccess, env: Env) -> Any:
        obj = self.eval(e.obj, env)
        raw = concretize(obj) if isinstance(obj, Sym) else obj
        if not isinstance(raw, dict):
            raise RoteError("type", f"field access .{e.name} on {_tname(obj)}", e.line)
        if isinstance(obj, Sym):
            self.guard(["has", obj.expr, const(e.name)], e.name in raw)
            if e.name not in raw:
                raise RoteError("field", f"observed record has no field {e.name!r}", e.line)
            return Sym(["field", obj.expr, const(e.name)], raw[e.name])
        if e.name not in raw:
            raise RoteError("field", f"record has no field {e.name!r}", e.line)
        return raw[e.name]

    def eval_index(self, e: A.IndexAccess, env: Env) -> Any:
        obj, idx = self.eval(e.obj, env), self.eval(e.index, env)
        robj, ridx = concretize(obj) if isinstance(obj, Sym) else obj, concretize(idx)
        if isinstance(robj, dict):
            return self.index_record(obj, robj, idx, ridx, e.line)
        if not isinstance(robj, list):
            raise RoteError("type", f"indexing a {_tname(obj)}", e.line)
        if not _is_int(ridx):
            raise RoteError("type", f"list index must be an int, got {_tname(idx)}", e.line)
        symbolic = isinstance(obj, Sym) or isinstance(idx, Sym)
        if symbolic:
            lo = lift(obj)
            li = lift(idx)
            if isinstance(idx, Sym):
                self.guard(["bin", ">=", li, const(0)], ridx >= 0)
            self.guard(["bin", "<", li, ["len", lo]], ridx < len(robj))
        if ridx < 0 or ridx >= len(robj):
            raise RoteError("index", f"index {ridx} out of range for length {len(robj)}", e.line)
        if symbolic:
            return Sym(["index", lift(obj), lift(idx)], concretize(robj[ridx]))
        return robj[ridx]

    def index_record(self, obj: Any, robj: dict, idx: Any, ridx: Any, line: int) -> Any:
        if not isinstance(ridx, str):
            raise RoteError("type", f"record key must be a string, got {_tname(idx)}", line)
        if isinstance(obj, Sym) or isinstance(idx, Sym):
            self.guard(["has", lift(obj), lift(idx)], ridx in robj)
            if ridx not in robj:
                raise RoteError("field", f"record has no field {ridx!r}", line)
            return Sym(["field", lift(obj), lift(idx)], concretize(robj[ridx]))
        if ridx not in robj:
            raise RoteError("field", f"record has no field {ridx!r}", line)
        return robj[ridx]

    # -- calls -----------------------------------------------------------------
    def eval_call(self, e: A.Call, env: Env) -> Any:
        if isinstance(e.func, A.Var):
            found, fv = env.lookup(e.func.name)
            if not found:
                return self.call_named(e.func.name, e, env)
            callee = fv
        else:
            callee = self.eval(e.func, env)
        args = [self.eval(a, env) for a in e.args]
        return self.apply(callee, args, e.line)

    def apply(self, callee: Any, args: list[Any], line: int) -> Any:
        if isinstance(callee, Sym):
            raise RoteError("type", "cannot call a value derived from the world", line)
        if not isinstance(callee, Closure):
            raise RoteError("type", f"calling a {_tname(callee)}", line)
        if len(callee.params) != len(args):
            raise RoteError(
                "arity", f"function expects {len(callee.params)} arguments, got {len(args)}", line
            )
        inner = Env(callee.env)
        for p, a in zip(callee.params, args, strict=True):
            inner.vars[p] = a
        try:
            return self.eval(callee.body, inner)
        except _Return as r:
            return r.value

    def call_named(self, name: str, e: A.Call, env: Env) -> Any:
        sig = self.world.signature()
        if name in sig:
            kind, arity = sig[name]
            args = [self.eval(a, env) for a in e.args]
            if len(args) != arity:
                raise RoteError("arity", f"{name} expects {arity} arguments, got {len(args)}", e.line)
            return self.effect(kind, name, args, e.line)
        if name in BUILTINS:
            args = [self.eval(a, env) for a in e.args]
            return BUILTINS[name](self, args, e.line)
        raise RoteError("name", f"unknown function {name!r}", e.line)

    def effect(self, kind: str, name: str, args: list[Any], line: int) -> Any:
        cargs = [concretize(a) for a in args]
        if kind == "act":
            if self.mode != "script" and self.mode != "tactic":
                raise RoteError("effect", f"a goal may only observe; it called act {name}", line)
            if name not in self.grant:
                raise RoteError("grant", f"action {name!r} is not in the caller's grant", line)
            value = self.world.act(name, cargs)
        else:
            value = self.world.observe(name, cargs)
        sid = self.new_id()
        self.steps.append({"op": kind, "id": sid, "name": name, "args": [lift(a) for a in args]})
        return Sym(["ref", sid], copy.deepcopy(value))

    def eval_use(self, e: A.Use, env: Env) -> Any:
        if self.mode == "goal":
            raise RoteError("effect", "a goal may not use a capability", e.line)
        if e.cap not in self.caps:
            raise RoteError("name", f"unknown capability {e.cap!r}", e.line)
        if self.runtime is None:
            raise RoteError("effect", "no runtime available for `use`", e.line)
        args = [self.eval(a, env) for a in e.args]
        if len(args) != self.caps[e.cap]:
            raise RoteError("arity", f"{e.cap} expects {self.caps[e.cap]} arguments, got {len(args)}", e.line)
        outcome = self.runtime.achieve(e.cap, [concretize(a) for a in args], self.world)
        self.nested_acts.extend(outcome.acts)
        sid = self.new_id()
        self.steps.append({"op": "use", "id": sid, "cap": e.cap, "args": [lift(a) for a in args]})
        return Sym(["ref", sid], outcome.summary())

    def eval_ask(self, e: A.Ask, env: Env) -> Any:
        if self.mode != "tactic":
            raise RoteError(
                "effect", "`ask` is only allowed in a tactic; a script must run without an oracle", e.line
            )
        if self.oracle is None:
            raise RoteError("effect", "no oracle available", e.line)
        return self.oracle.ask(concretize(self.eval(e.prompt, env)))

    def eval_propose(self, e: A.Propose, env: Env) -> Any:
        if self.mode != "tactic":
            raise RoteError("effect", "`propose` is only allowed in a tactic", e.line)
        if self.runtime is None:
            raise RoteError("effect", "no runtime available for `propose`", e.line)
        src = concretize(self.eval(e.source, env))
        if not isinstance(src, str):
            raise RoteError("type", "propose needs script source text", e.line)
        return self.runtime.propose(src, self.world).summary()


def _is_int(v: Any) -> bool:
    return isinstance(v, int) and not isinstance(v, bool)


def _ranged(n: int, line: int) -> int:
    if n < INT_MIN or n > INT_MAX:
        raise RoteError("arith", "integer overflow: result is outside the signed 64-bit range", line)
    return n


_ARITH = {"+": lambda a, b: a + b, "-": lambda a, b: a - b, "*": lambda a, b: a * b}
_COMPARE = {
    "<": lambda a, b: a < b,
    "<=": lambda a, b: a <= b,
    ">": lambda a, b: a > b,
    ">=": lambda a, b: a >= b,
}


# -- builtins ---------------------------------------------------------------------


def _b_len(t: Tracer, args: list[Any], line: int) -> Any:
    (v,) = _arity("len", args, 1, line)
    raw = concretize(v)
    if not isinstance(raw, (list, dict, str)):
        raise RoteError("type", f"len of {_tname(v)}", line)
    return t.note(Sym(["len", v.expr], len(raw))) if isinstance(v, Sym) else len(raw)


def _b_map(t: Tracer, args: list[Any], line: int) -> Any:
    xs, f = _arity("map", args, 2, line)
    return [t.apply(f, [x], line) for x in t.elements(xs, line)]


def _b_filter(t: Tracer, args: list[Any], line: int) -> Any:
    xs, f = _arity("filter", args, 2, line)
    return [x for x in t.elements(xs, line) if t.decide_bool(t.apply(f, [x], line), line, "filter result")]


def _b_fold(t: Tracer, args: list[Any], line: int) -> Any:
    xs, acc, f = _arity("fold", args, 3, line)
    for x in t.elements(xs, line):
        acc = t.apply(f, [acc, x], line)
    return acc


def _b_all(t: Tracer, args: list[Any], line: int) -> Any:
    (xs,) = _arity("all", args, 1, line)
    return all(t.decide_bool(x, line, "all element") for x in t.elements(xs, line))


def _b_any(t: Tracer, args: list[Any], line: int) -> Any:
    (xs,) = _arity("any", args, 1, line)
    return any(t.decide_bool(x, line, "any element") for x in t.elements(xs, line))


def _b_contains(t: Tracer, args: list[Any], line: int) -> Any:
    xs, v = _arity("contains", args, 2, line)
    rxs, rv = concretize(xs), concretize(v)
    try:
        result = _contains(rxs, rv)
    except Exception as err:  # noqa: BLE001  a SymError from the shared helper
        raise RoteError("type", str(err), line) from None
    if contains_sym(xs) or contains_sym(v):
        return t.note(Sym(["contains", lift(xs), lift(v)], result))
    return result


def _b_keys(t: Tracer, args: list[Any], line: int) -> Any:
    (r,) = _arity("keys", args, 1, line)
    raw = concretize(r)
    if not isinstance(raw, dict):
        raise RoteError("type", f"keys of {_tname(r)}", line)
    return t.note(Sym(["keys", r.expr], list(raw.keys()))) if isinstance(r, Sym) else list(raw.keys())


def _b_has(t: Tracer, args: list[Any], line: int) -> Any:
    r, k = _arity("has", args, 2, line)
    raw, rk = concretize(r), concretize(k)
    if not isinstance(rk, str):
        raise RoteError("type", f"has needs a string key, got {_tname(k)}", line)
    result = isinstance(raw, dict) and rk in raw
    if contains_sym(r) or isinstance(k, Sym):
        return t.note(Sym(["has", lift(r), lift(k)], result))
    return result


def _b_get(t: Tracer, args: list[Any], line: int) -> Any:
    r, k, d = _arity("get", args, 3, line)
    raw, rk = concretize(r), concretize(k)
    if not isinstance(rk, str):
        raise RoteError("type", f"get needs a string key, got {_tname(k)}", line)
    if isinstance(r, Sym) or isinstance(k, Sym):
        result = raw[rk] if isinstance(raw, dict) and rk in raw else concretize(d)
        return t.note(Sym(["get", lift(r), lift(k), lift(d)], result))
    if isinstance(r, dict) and rk in r:
        return r[rk]
    return d


def _b_str(t: Tracer, args: list[Any], line: int) -> Any:
    (v,) = _arity("str", args, 1, line)
    raw = concretize(v)
    if contains_sym(v):  # a constructed container may hold observed children
        return Sym(["str", lift(v)], _to_str(raw))
    return _to_str(raw)


def _b_range(t: Tracer, args: list[Any], line: int) -> Any:
    (n,) = _arity("range", args, 1, line)
    raw = concretize(n)
    if not _is_int(raw):
        raise RoteError("type", f"range needs an int, got {_tname(n)}", line)
    if raw > 1_000_000:
        raise RoteError("arith", "range is limited to 1,000,000 elements", line)
    if isinstance(n, Sym):
        t.guard(n.expr, raw)
    return list(range(raw))


def _b_minmax(which: str):
    def f(t: Tracer, args: list[Any], line: int) -> Any:
        a, b = _arity(which, args, 2, line)
        ra, rb = concretize(a), concretize(b)
        if not (_is_int(ra) and _is_int(rb)):
            raise RoteError("type", f"{which} needs two ints", line)
        result = min(ra, rb) if which == "min" else max(ra, rb)
        if isinstance(a, Sym) or isinstance(b, Sym):
            return t.note(Sym([which, lift(a), lift(b)], result))
        return result

    return f


def _b_fail(t: Tracer, args: list[Any], line: int) -> Any:
    (msg,) = _arity("fail", args, 1, line)
    raise ScriptFailure(_to_str(concretize(msg)))


def _b_append(t: Tracer, args: list[Any], line: int) -> Any:
    xs, v = _arity("append", args, 2, line)
    return [*t.elements(xs, line), v]


def _b_reduce(which: str):
    """A reduction over a list of ints stays symbolic: it does not pin the list's length."""
    tag = {"sum": "sum", "max_of": "maxof", "min_of": "minof"}[which]
    n = 1 if which == "sum" else 2

    def f(t: Tracer, args: list[Any], line: int) -> Any:
        xs, *rest = _arity(which, args, n, line)
        rxs = concretize(xs)
        if not isinstance(rxs, list) or any(not _is_int(x) for x in rxs):
            raise RoteError("type", f"{which} needs a list of ints", line)
        default = concretize(rest[0]) if rest else None
        if rest and not _is_int(default):
            raise RoteError("type", f"{which} needs an int default", line)
        if which == "sum":
            result = _ranged(sum(rxs), line)
        else:
            result = default if not rxs else (max(rxs) if which == "max_of" else min(rxs))
        if not contains_sym(xs) and not (rest and contains_sym(rest[0])):
            return result
        expr = [tag, lift(xs)] if which == "sum" else [tag, lift(xs), lift(rest[0])]
        return t.note(Sym(expr, result))

    return f


def _arity(name: str, args: list[Any], n: int, line: int) -> list[Any]:
    if len(args) != n:
        raise RoteError("arity", f"{name} expects {n} arguments, got {len(args)}", line)
    return args


BUILTINS = {
    "len": _b_len,
    "map": _b_map,
    "filter": _b_filter,
    "fold": _b_fold,
    "all": _b_all,
    "any": _b_any,
    "contains": _b_contains,
    "keys": _b_keys,
    "has": _b_has,
    "get": _b_get,
    "str": _b_str,
    "range": _b_range,
    "min": _b_minmax("min"),
    "max": _b_minmax("max"),
    "fail": _b_fail,
    "append": _b_append,
    "sum": _b_reduce("sum"),
    "max_of": _b_reduce("max_of"),
    "min_of": _b_reduce("min_of"),
}


def run_script(tracer: Tracer, params: list[str], args: list[Any], body: A.Block) -> RunResult:
    """Run a body to completion or to its first error; the steps so far are kept either way."""
    try:
        value = tracer.run_body(params, args, body)
        return RunResult(
            tracer.steps,
            concretize(value) if not isinstance(value, Closure) else "<fn>",
            fuel_used=tracer.fuel_used,
        )
    except RoteError as err:
        return RunResult(tracer.steps, None, str(err), err.kind, tracer.fuel_used)
    except ScriptFailure as err:
        return RunResult(tracer.steps, None, f"fail: {err}", "fail", tracer.fuel_used)
    except FuelExhausted as err:
        return RunResult(tracer.steps, None, str(err), "fuel", tracer.fuel_used)
