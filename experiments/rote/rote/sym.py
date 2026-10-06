"""Symbolic expressions over a run's inputs.

Every value a script derives from a parameter or from the world is shadowed by an
expression in this little language. Guards are such expressions paired with the
value they had. Witness steps carry them as arguments. They are JSON-native lists
so a replayer in any host language can evaluate them with a ~60-line switch:

  ["const", v]            ["param", name]            ["ref", step_id]
  ["field", e, key]       ["index", e, i]            ["len", e]
  ["has", e, key]         ["get", e, key, default]   ["keys", e]
  ["contains", e, v]      ["str", e]                 ["not", e]        ["neg", e]
  ["min", a, b]           ["max", a, b]              ["bin", op, a, b]
  ["list", [e...]]        ["record", [[k, e]...]]
  ["sum", e]              ["maxof", e, default]      ["minof", e, default]   (reductions over a list of ints)
"""

from __future__ import annotations

import json
from typing import Any

__all__ = [
    "SExpr",
    "SymError",
    "const",
    "param",
    "ref",
    "eval_sexpr",
    "show",
    "canonical",
    "is_const",
    "deep_eq",
]

SExpr = list


class SymError(Exception):
    """A symbolic expression could not be evaluated against the world's fresh answers."""


INT_MIN, INT_MAX = -(2**63), 2**63 - 1


def ranged(n: int) -> int:
    """Rote ints are signed 64-bit so every kernel agrees; leaving the range is an error, not a wrap."""
    if n < INT_MIN or n > INT_MAX:
        raise SymError("integer overflow: result is outside the signed 64-bit range")
    return n


def const(v: Any) -> SExpr:
    return ["const", v]


def param(name: str) -> SExpr:
    return ["param", name]


def ref(step_id: int) -> SExpr:
    return ["ref", step_id]


def is_const(e: SExpr) -> bool:
    return e[0] == "const"


def canonical(e: Any) -> str:
    return json.dumps(e, sort_keys=True, separators=(",", ":"))


def deep_eq(a: Any, b: Any) -> bool:
    """Structural equality in which a bool is never equal to a number. Python's own
    `True == 1` would otherwise leak into Rote through `==`, `contains` and guards."""
    if isinstance(a, bool) or isinstance(b, bool):
        return isinstance(a, bool) and isinstance(b, bool) and a == b
    if isinstance(a, list) and isinstance(b, list):
        return len(a) == len(b) and all(deep_eq(x, y) for x, y in zip(a, b, strict=True))
    if isinstance(a, dict) and isinstance(b, dict):
        return a.keys() == b.keys() and all(deep_eq(a[k], b[k]) for k in a)
    if isinstance(a, (list, dict)) or isinstance(b, (list, dict)):
        return False
    return a == b


_BIN_OPS = {
    "+": lambda a, b: a + b,
    "-": lambda a, b: a - b,
    "*": lambda a, b: a * b,
    "==": deep_eq,
    "!=": lambda a, b: not deep_eq(a, b),
    "<": lambda a, b: a < b,
    "<=": lambda a, b: a <= b,
    ">": lambda a, b: a > b,
    ">=": lambda a, b: a >= b,
}


def _div(op: str, a: Any, b: Any) -> Any:
    if b == 0:
        raise SymError("division by zero")
    if op == "/":
        return a // b
    return a % b


def _typecheck_bin(op: str, a: Any, b: Any) -> None:
    if op in ("==", "!="):
        return
    if op == "+" and isinstance(a, str) and isinstance(b, str):
        return
    ints = isinstance(a, int) and isinstance(b, int) and not isinstance(a, bool) and not isinstance(b, bool)
    if not ints:
        raise SymError(
            f"operator {op} needs two ints"
            + (" or two strings" if op == "+" else "")
            + f", got {type(a).__name__} and {type(b).__name__}"
        )


def eval_sexpr(e: SExpr, params: dict[str, Any], refs: dict[int, Any]) -> Any:  # noqa: C901, PLR0911, PLR0912, PLR0915
    """Evaluate against fresh inputs. Raises SymError on a shape or type mismatch."""
    tag = e[0]
    if tag == "const":
        return e[1]
    if tag == "param":
        if e[1] not in params:
            raise SymError(f"unbound parameter {e[1]}")
        return params[e[1]]
    if tag == "ref":
        if e[1] not in refs:
            raise SymError(f"step {e[1]} has not produced a value")
        return refs[e[1]]
    if tag == "field":
        obj, key = eval_sexpr(e[1], params, refs), eval_sexpr(e[2], params, refs)
        if not isinstance(obj, dict):
            raise SymError(f"field access on {type(obj).__name__}")
        if not isinstance(key, str):
            raise SymError(f"field key must be a string, got {type(key).__name__}")
        if key not in obj:
            raise SymError(f"missing field {key!r}")
        return obj[key]
    if tag == "index":
        obj, i = eval_sexpr(e[1], params, refs), eval_sexpr(e[2], params, refs)
        if not isinstance(obj, list) or not isinstance(i, int) or isinstance(i, bool):
            raise SymError("index needs a list and an int")
        if i < 0 or i >= len(obj):
            raise SymError(f"index {i} out of range for length {len(obj)}")
        return obj[i]
    if tag == "len":
        obj = eval_sexpr(e[1], params, refs)
        if not isinstance(obj, (list, dict, str)):
            raise SymError(f"len of {type(obj).__name__}")
        return len(obj)
    if tag == "has":
        obj, key = eval_sexpr(e[1], params, refs), eval_sexpr(e[2], params, refs)
        if not isinstance(key, str):
            raise SymError(f"has needs a string key, got {type(key).__name__}")
        return isinstance(obj, dict) and key in obj
    if tag == "get":
        obj, key = eval_sexpr(e[1], params, refs), eval_sexpr(e[2], params, refs)
        if not isinstance(key, str):
            raise SymError(f"get needs a string key, got {type(key).__name__}")
        if isinstance(obj, dict) and key in obj:
            return obj[key]
        return eval_sexpr(e[3], params, refs)
    if tag == "keys":
        obj = eval_sexpr(e[1], params, refs)
        if not isinstance(obj, dict):
            raise SymError(f"keys of {type(obj).__name__}")
        return list(obj.keys())
    if tag == "contains":
        obj, v = eval_sexpr(e[1], params, refs), eval_sexpr(e[2], params, refs)
        return _contains(obj, v)
    if tag == "str":
        return _to_str(eval_sexpr(e[1], params, refs))
    if tag == "not":
        v = eval_sexpr(e[1], params, refs)
        if not isinstance(v, bool):
            raise SymError("not needs a bool")
        return not v
    if tag == "neg":
        v = eval_sexpr(e[1], params, refs)
        if not isinstance(v, int) or isinstance(v, bool):
            raise SymError("negation needs an int")
        return ranged(-v)
    if tag in ("min", "max"):
        a, b = eval_sexpr(e[1], params, refs), eval_sexpr(e[2], params, refs)
        _typecheck_bin("<", a, b)
        return min(a, b) if tag == "min" else max(a, b)
    if tag == "bin":
        op = e[1]
        a, b = eval_sexpr(e[2], params, refs), eval_sexpr(e[3], params, refs)
        if op in ("/", "%"):
            _typecheck_bin(op, a, b)
            return ranged(_div(op, a, b))
        _typecheck_bin(op, a, b)
        result = _BIN_OPS[op](a, b)
        return ranged(result) if op in ("+", "-", "*") and isinstance(result, int) else result
    if tag == "list":
        return [eval_sexpr(x, params, refs) for x in e[1]]
    if tag == "record":
        return {k: eval_sexpr(x, params, refs) for k, x in e[1]}
    if tag in ("sum", "maxof", "minof"):
        return _reduce(tag, e, params, refs)
    raise SymError(f"unknown expression tag {tag!r}")


def _reduce(tag: str, e: SExpr, params: dict[str, Any], refs: dict[int, Any]) -> Any:
    xs = eval_sexpr(e[1], params, refs)
    if not isinstance(xs, list) or any(not isinstance(x, int) or isinstance(x, bool) for x in xs):
        raise SymError(f"{tag} needs a list of ints")
    if tag == "sum":
        return ranged(sum(xs))
    default = eval_sexpr(e[2], params, refs)
    if not isinstance(default, int) or isinstance(default, bool):
        raise SymError(f"{tag} needs an int default")
    if not xs:
        return default
    return max(xs) if tag == "maxof" else min(xs)


def _contains(obj: Any, v: Any) -> bool:
    if isinstance(obj, str):
        if not isinstance(v, str):
            raise SymError(f"contains on a string needs a string, got {type(v).__name__}")
        return v in obj
    if not isinstance(obj, list):
        raise SymError(f"contains on {type(obj).__name__}")
    return any(deep_eq(x, v) for x in obj)


def _to_str(v: Any) -> str:
    if isinstance(v, bool):
        return "true" if v else "false"
    if v is None:
        return "unit"
    if isinstance(v, (list, dict)):
        return json.dumps(v, separators=(",", ":"))
    return str(v)


def show(e: SExpr, refnames: dict[int, str] | None = None) -> str:  # noqa: C901, PLR0911, PLR0912
    """Render for humans. `refnames` maps a step id to the call that produced it."""
    refnames = refnames or {}
    tag = e[0]
    if tag == "const":
        return json.dumps(e[1]) if not isinstance(e[1], bool) else ("true" if e[1] else "false")
    if tag == "param":
        return e[1]
    if tag == "ref":
        return refnames.get(e[1], f"#{e[1]}")
    if tag == "field":
        key = e[2]
        if is_const(key) and isinstance(key[1], str):
            return f"{show(e[1], refnames)}.{key[1]}"
        return f"{show(e[1], refnames)}[{show(key, refnames)}]"
    if tag == "index":
        return f"{show(e[1], refnames)}[{show(e[2], refnames)}]"
    if tag in ("len", "keys", "str", "sum"):
        return f"{tag}({show(e[1], refnames)})"
    if tag in ("has", "contains", "min", "max"):
        return f"{tag}({show(e[1], refnames)}, {show(e[2], refnames)})"
    if tag in ("maxof", "minof"):
        return f"{tag[:3]}_of({show(e[1], refnames)}, {show(e[2], refnames)})"
    if tag == "get":
        return f"get({show(e[1], refnames)}, {show(e[2], refnames)}, {show(e[3], refnames)})"
    if tag == "not":
        return f"not {show(e[1], refnames)}"
    if tag == "neg":
        return f"-{show(e[1], refnames)}"
    if tag == "bin":
        return f"({show(e[2], refnames)} {e[1]} {show(e[3], refnames)})"
    if tag == "list":
        return "[" + ", ".join(show(x, refnames) for x in e[1]) + "]"
    if tag == "record":
        return "{" + ", ".join(f"{k}: {show(x, refnames)}" for k, x in e[1]) + "}"
    return repr(e)
