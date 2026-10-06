"""Regressions for the seven findings of the first external review (coil PR #4)."""

import pytest
from rote.eval import Tracer, run_script
from rote.library import Library
from rote.parser import parse_program
from rote.runtime import Policy, Runtime
from rote.sym import INT_MAX, INT_MIN, SymError, eval_sexpr
from rote.synth import ScriptedOracle
from rote.witness import replay, witness_from_steps

from tests.helpers import SIG, DictWorld


def trace(src, world, args=("a",), grant=("do", "set")):
    decl = parse_program(src).decls[0]
    t = Tracer(world, grant=set(grant), caps={})
    res = run_script(t, decl.params, list(args), decl.body)
    return res, witness_from_steps("f", decl.params, res.steps, src)


def test_unused_pure_binding_keeps_its_type_check_at_replay():
    src = "script f(s) { let port = cfg(s).port + 1\n do(s) }"
    res, w = trace(src, DictWorld({"cfg(a)": {"port": 80}}, SIG))
    assert res.ok and [st["op"] for st in w.steps] == ["observe", "guard", "eval", "act"]
    drifted = DictWorld({"cfg(a)": {"port": "eighty"}}, SIG)
    r = replay(w, ["a"], drifted, {"do", "set"})
    assert r.kind == "side_exit" and drifted.acts == [], (
        "replay acted where the interpreter would have failed"
    )
    res2, _ = trace(src, DictWorld({"cfg(a)": {"port": "eighty"}}, SIG))
    assert not res2.ok and res2.error_kind == "type"


def test_eval_steps_are_dropped_when_a_guard_or_argument_already_covers_them():
    src = 'script f(s) { if status(s) == "down" { set(s, n(s) + 1) } }'
    _, w = trace(src, DictWorld({"status(a)": "down", "n(a)": 1}, SIG))
    assert [st["op"] for st in w.steps] == ["observe", "guard", "observe", "act"]


def test_str_of_a_container_with_observed_children_stays_symbolic():
    src = "script f(s) { set(s, str([n(s)])) }"
    _, w = trace(src, DictWorld({"n(a)": 1}, SIG))
    fresh = DictWorld({"n(a)": 2}, SIG)
    r = replay(w, ["a"], fresh, {"do", "set"})
    assert r.kind == "completed" and fresh.acts == [("set", ["a", "[2]"])]


def test_replay_copies_world_answers_so_later_mutation_cannot_rewrite_bindings():
    class LiveWorld(DictWorld):
        def __init__(self):
            super().__init__({}, SIG)
            self.live = {"n": 1}

        def observe(self, name, args):
            return self.live  # the adapter hands out its own mutable object

        def act(self, name, args):
            self.acts.append((name, list(args)))
            self.live["n"] = 2
            return True

    src = "script f(s) { let c = cfg(s)\n set(s, 2)\n do(c.n) }"
    _, w = trace(src, LiveWorld())
    world = LiveWorld()
    r = replay(w, ["a"], world, {"do", "set"})
    assert r.kind == "completed" and world.acts == [("set", ["a", 2]), ("do", [1])]


def test_ints_are_signed_64_bit_in_the_reference():
    res, w = trace("script f(s) { set(s, n(s) + 1) }", DictWorld({"n(a)": 1}, SIG))
    big = DictWorld({"n(a)": INT_MAX}, SIG)
    r = replay(w, ["a"], big, {"do", "set"})
    assert r.kind == "side_exit" and big.acts == []
    res2, _ = trace("script f(s) { set(s, n(s) + 1) }", DictWorld({"n(a)": INT_MAX}, SIG))
    assert not res2.ok and "overflow" in res2.error
    res3, _ = trace(f"script f(s) {{ set(s, {INT_MAX + 1}) }}", DictWorld({}, SIG))
    assert not res3.ok and "overflow" in res3.error
    with pytest.raises(SymError):
        eval_sexpr(["neg", ["const", INT_MIN]], {}, {})
    with pytest.raises(SymError):
        eval_sexpr(["sum", ["const", [INT_MAX, 1]]], {}, {})
    assert eval_sexpr(["bin", "*", ["const", -1], ["const", INT_MIN + 1]], {}, {}) == INT_MAX


def test_non_string_record_keys_are_errors_at_replay():
    src = "script f(s) { if has(cfg(s), k(s)) { do(s) } }"
    sig = {**SIG, "k": ("observe", 1)}
    _, w = trace(src, DictWorld({"cfg(a)": {}, "k(a)": "missing"}, sig))
    drifted = DictWorld({"cfg(a)": {}, "k(a)": 1}, sig)
    r = replay(w, ["a"], drifted, {"do", "set"})
    assert r.kind == "side_exit" and drifted.acts == []


LIB = """
world w {
  observe allowed(): bool
  observe ready(): bool
  act fix(): bool
}
cap heal() { goal: ready() }
tactic heal() {
  if allowed() { propose("script heal() when allowed() { fix() }") }
}
"""


class Flags:
    def __init__(self):
        self.allowed, self.ready, self.history = True, False, []
        self.label = "same-label"

    def signature(self):
        return {"allowed": ("observe", 0), "ready": ("observe", 0), "fix": ("act", 0)}

    def observe(self, name, args):
        return self.allowed if name == "allowed" else self.ready

    def act(self, name, args):
        self.history.append(name)
        self.ready = True
        return True


def test_tactic_success_is_decided_by_this_invocation_and_the_final_goal():
    lib = Library.from_source(LIB)
    rt = Runtime(lib, Policy(grant={"fix"}), ScriptedOracle({}))
    world = Flags()
    assert rt.achieve("heal", [], world).kind == "synthesized"
    world.ready, world.allowed = False, False  # same world label; the old witness no longer applies
    out = rt.achieve("heal", [], world)
    assert out.kind == "failed" and not out.ok and world.history == ["fix"]
    assert "validated no proposal" in out.reason
