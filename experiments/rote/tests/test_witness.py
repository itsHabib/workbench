import json

from rote.eval import Tracer, run_script
from rote.parser import parse_program
from rote.witness import Witness, replay, witness_from_steps
from tests.helpers import SIG, DictWorld

SRC = '''script f(s) {
  let c = cfg(s)
  if c.port > 1000 { set(s, c.port + 1) }
  if status(s) == "down" { do(s) }
}'''


def make(world, args=("a",)):
    decl = parse_program(SRC).decls[0]
    t = Tracer(world, grant={"do", "set"}, caps={})
    res = run_script(t, decl.params, list(args), decl.body)
    assert res.ok, res.error
    return witness_from_steps("f", decl.params, res.steps, SRC)


def world(port, status):
    return DictWorld({"cfg(a)": {"port": port}, "status(a)": status}, SIG)


def test_prefix_and_json_roundtrip_and_stable_hash():
    w = make(world(8080, "down"))
    assert w.prefix_len == 3  # observe cfg, has-guard, gt-guard; then the first act
    again = Witness.from_json(json.loads(json.dumps(w.to_json())))
    assert again.hash == w.hash and again.steps == w.steps
    assert make(world(9000, "down")).hash == w.hash  # same path, different values


def test_replay_completes_when_guards_hold_and_reproduces_the_actions():
    w = make(world(8080, "down"))
    fresh = world(2000, "down")
    r = replay(w, ["a"], fresh, {"do", "set"})
    assert r.kind == "completed" and fresh.acts == [("set", ["a", 2001]), ("do", ["a"])]


def test_inapplicable_witness_touches_nothing():
    w = make(world(8080, "down"))
    fresh = world(80, "down")  # port <= 1000: the first guard fails inside the prefix
    r = replay(w, ["a"], fresh, {"do", "set"})
    assert r.kind == "side_exit" and r.inapplicable and fresh.acts == []
    assert "expected true, got false" in r.reason


def test_shape_drift_is_caught_before_acting():
    w = make(world(8080, "down"))
    fresh = DictWorld({"cfg(a)": {"listen": {"port": 8080}}, "status(a)": "down"}, SIG)
    r = replay(w, ["a"], fresh, {"do", "set"})
    assert r.inapplicable and fresh.acts == [] and "has(cfg(s)" in r.reason


def test_side_exit_after_acting_is_reported_with_the_acts_done():
    w = make(world(8080, "down"))
    fresh = world(8080, "up")  # second guard fails after set() already ran
    r = replay(w, ["a"], fresh, {"do", "set"})
    assert r.kind == "side_exit" and not r.inapplicable and r.acts == [("set", ["a", 8081])]


def test_replay_is_refused_outside_the_grant_before_anything_runs():
    w = make(world(8080, "down"))
    fresh = world(8080, "down")
    r = replay(w, ["a"], fresh, {"do"})
    assert r.kind == "refused" and fresh.acts == []


def test_type_drift_in_an_observation_is_a_side_exit_not_a_crash():
    w = make(world(8080, "down"))
    fresh = DictWorld({"cfg(a)": {"port": "8080"}, "status(a)": "down"}, SIG)
    r = replay(w, ["a"], fresh, {"do", "set"})
    assert r.kind == "side_exit" and r.inapplicable


def test_identical_guards_are_deduplicated():
    src = 'script f(s) { let c = cfg(s)\n set(s, c.port)\n set(s, c.port) }'
    decl = parse_program(src).decls[0]
    t = Tracer(world(1, "up"), grant={"set"}, caps={})
    res = run_script(t, decl.params, ["a"], decl.body)
    w = witness_from_steps("f", decl.params, res.steps, src)
    assert len(w.guards) == 1 and len([s for s in res.steps if s["op"] == "guard"]) == 2


def test_a_bool_is_never_a_number():
    from rote.sym import SymError, deep_eq, eval_sexpr

    assert not deep_eq(True, 1) and not deep_eq([True], [1]) and deep_eq({"a": [1, "x"]}, {"a": [1, "x"]})
    assert eval_sexpr(["bin", "==", ["param", "t"], ["const", 1]], {"t": True}, {}) is False
    assert eval_sexpr(["contains", ["list", [["const", True]]], ["const", 1]], {}, {}) is False
    import pytest

    with pytest.raises(SymError):
        eval_sexpr(["index", ["const", [1, 2]], ["const", True]], {}, {})
    with pytest.raises(SymError):
        eval_sexpr(["contains", ["const", "abc"], ["const", 1]], {}, {})
    with pytest.raises(SymError):
        eval_sexpr(["field", ["const", {"a": 1}], ["const", ["a"]]], {}, {})
    with pytest.raises(SymError):
        eval_sexpr(["has", ["const", {"a": 1}], ["const", 1]], {}, {})


def test_guard_comparison_is_typed():
    w = make(world(8080, "down"))
    # forge a guard that expects the bool True where the fresh world will deliver the int 1
    forged = Witness("f", ["s"], [{"op": "observe", "id": 1, "name": "n", "args": [["param", "s"]]},
                                  {"op": "guard", "pred": ["ref", 1], "expect": True},
                                  {"op": "act", "id": 2, "name": "do", "args": [["param", "s"]]}], "forged")
    fresh = DictWorld({"n(a)": 1}, SIG)
    r = replay(forged, ["a"], fresh, {"do"})
    assert r.kind == "side_exit" and fresh.acts == [] and w.hash != forged.hash
