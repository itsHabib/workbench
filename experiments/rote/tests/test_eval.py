import pytest

from rote.eval import RoteError, Sym, Tracer, concretize, run_script
from rote.parser import parse_program
from tests.helpers import SIG, DictWorld


def run(src, world, args, mode="script", grant=("do", "set"), fuel=5000):
    prog = parse_program(src)
    decl = prog.decls[0]
    t = Tracer(world, grant=set(grant), mode=mode, fuel=fuel, caps={})
    res = run_script(t, decl.params, args, decl.body)
    return res, t


def guards(res):
    return [(s["pred"], s["expect"]) for s in res.steps if s["op"] == "guard"]


def test_branch_on_observation_records_a_guard_and_act_args_are_symbolic():
    w = DictWorld({"status(a)": "down"}, SIG)
    res, _ = run('script f(s) { if status(s) == "down" { do(s) } }', w, ["a"])
    assert res.ok
    assert guards(res) == [(["bin", "==", ["ref", 1], ["const", "down"]], True)]
    act = [s for s in res.steps if s["op"] == "act"][0]
    assert act["args"] == [["param", "s"]]
    assert w.acts == [("do", ["a"])]


def test_untaken_branch_leaves_no_trace_but_its_decision_is_guarded():
    w = DictWorld({"status(a)": "up"}, SIG)
    res, _ = run('script f(s) { if status(s) == "down" { do(s) } }', w, ["a"])
    assert guards(res) == [(["bin", "==", ["ref", 1], ["const", "down"]], False)]
    assert w.acts == []


def test_field_access_on_observed_record_guards_shape():
    w = DictWorld({"cfg(a)": {"port": 80}}, SIG)
    res, _ = run("script f(s) { let c = cfg(s)\n set(s, c.port + 1) }", w, ["a"])
    assert guards(res) == [(["has", ["ref", 1], ["const", "port"]], True)]
    act = [s for s in res.steps if s["op"] == "act"][0]
    assert act["args"][1] == ["bin", "+", ["field", ["ref", 1], ["const", "port"]], ["const", 1]]
    assert w.acts == [("set", ["a", 81])]


def test_missing_field_is_an_error_at_validation_time():
    w = DictWorld({"cfg(a)": {"listen": {}}}, SIG)
    res, _ = run("script f(s) { cfg(s).port }", w, ["a"])
    assert not res.ok and res.error_kind == "field"


def test_get_and_has_do_not_guard_but_stay_symbolic():
    w = DictWorld({"cfg(a)": {"listen": {}}}, SIG)
    res, _ = run('script f(s) { let p = get(cfg(s), "port", 8080)\n set(s, p) }', w, ["a"])
    assert guards(res) == []
    assert w.acts == [("set", ["a", 8080])]


def test_loop_over_observed_list_pins_length_and_indexes_elements():
    w = DictWorld({"items(a)": ["x", "y"], "status(x)": "down", "status(y)": "up"}, SIG)
    res, _ = run('script f(s) { for i in items(s) { if status(i) == "down" { do(i) } } }', w, ["a"])
    g = guards(res)
    assert g[0] == (["len", ["ref", 1]], 2)
    obs = [s for s in res.steps if s["op"] == "observe"]
    assert obs[1]["args"] == [["index", ["ref", 1], ["const", 0]]]
    assert w.acts == [("do", ["x"])]


def test_division_by_observed_value_guards_nonzero():
    w = DictWorld({"n(a)": 4}, SIG)
    res, _ = run("script f(s) { set(s, 100 / n(s)) }", w, ["a"])
    assert guards(res) == [(["bin", "==", ["ref", 1], ["const", 0]], False)]
    assert w.acts == [("set", ["a", 25])]


def test_short_circuit_guards_only_the_left_side():
    w = DictWorld({"status(a)": "up", "n(a)": 1}, SIG)
    res, _ = run('script f(s) { if status(s) == "down" and n(s) > 0 { do(s) } }', w, ["a"])
    assert len(guards(res)) == 1 and [s for s in res.steps if s["op"] == "observe"][-1]["name"] == "status"


def test_filter_all_any_decide_per_element():
    w = DictWorld({"items(a)": [1, 5, 10]}, SIG)
    res, _ = run("script f(s) { let big = filter(items(s), fn(x) { x > 3 })\n set(s, len(big)) }", w, ["a"])
    g = guards(res)
    assert g[0] == (["len", ["ref", 1]], 3)
    assert [e for _, e in g[1:]] == [False, True, True]
    assert w.acts == [("set", ["a", 2])]


def test_equality_of_partially_symbolic_containers_is_symbolic():
    w = DictWorld({"cfg(a)": {"port": 80}}, SIG)
    res, _ = run('script f(s) { let c = cfg(s)\n if {port: c.port} == {port: 80} { do(s) } }', w, ["a"])
    g = guards(res)
    assert g[-1][0][0] == "bin" and g[-1][1] is True
    assert w.acts == [("do", ["a"])]


def test_act_results_are_symbolic_too():
    w = DictWorld({}, SIG)
    w.act_results["do(a)"] = False
    res, _ = run('script f(s) { let ok = do(s)\n if not ok { do(s) } }', w, ["a"])
    assert guards(res) == [(["not", ["ref", 1]], True)]
    assert len(w.acts) == 2


def test_goal_mode_may_only_observe():
    w = DictWorld({"status(a)": "up"}, SIG)
    res, _ = run("script f(s) { do(s) }", w, ["a"], mode="goal")
    assert not res.ok and res.error_kind == "effect"


def test_script_mode_cannot_ask_and_grant_is_enforced_dynamically_too():
    w = DictWorld({}, SIG)
    res, _ = run('script f(s) { ask("x") }', w, ["a"])
    assert not res.ok and res.error_kind == "effect"
    res, _ = run("script f(s) { do(s) }", w, ["a"], grant=())
    assert not res.ok and res.error_kind == "grant" and w.acts == []


def test_fuel_bounds_every_run():
    w = DictWorld({}, SIG)
    res, _ = run("script f(s) { let loop = fn(x) { x }\n let g = fn(h, x) { h(h, x) }\n g(g, 1) }", w, ["a"], fuel=200)
    assert not res.ok and res.error_kind == "fuel"


def test_closures_cannot_flow_into_the_world():
    w = DictWorld({}, SIG)
    res, _ = run("script f(s) { set(s, fn(x) { x }) }", w, ["a"])
    assert not res.ok and res.error_kind == "type"


def test_fail_and_return():
    w = DictWorld({"status(a)": "down"}, SIG)
    res, _ = run('script f(s) { if status(s) == "down" { return 7 }\n fail("unreachable") }', w, ["a"])
    assert res.ok and res.value == 7
    res, _ = run('script f(s) { fail("nope") }', w, ["a"])
    assert res.error == "fail: nope"


def test_unknown_names_and_arity_fail_fast():
    w = DictWorld({}, SIG)
    res, _ = run("script f(s) { nothing(s) }", w, ["a"])
    assert res.error_kind == "name"
    res, _ = run("script f(s) { do(s, s) }", w, ["a"])
    assert res.error_kind == "arity"


def test_sym_values_concretize_deeply():
    v = {"a": [Sym(["ref", 1], 1), 2], "b": Sym(["ref", 2], {"x": 1})}
    assert concretize(v) == {"a": [1, 2], "b": {"x": 1}}
    with pytest.raises(RoteError):
        concretize({"f": __import__("rote.eval", fromlist=["Closure"]).Closure([], None, None)})


def test_reductions_stay_symbolic_without_pinning_length():
    w = DictWorld({"items(a)": [3, 9, 4]}, SIG)
    res, _ = run("script f(s) { set(s, max_of(items(s), 0) + 1)\n set(s, sum(items(s))) }", w, ["a"])
    assert res.ok and guards(res) == []
    assert w.acts == [("set", ["a", 10]), ("set", ["a", 16])]
    act = [s for s in res.steps if s["op"] == "act"][0]
    assert act["args"][1] == ["bin", "+", ["maxof", ["ref", 1], ["const", 0]], ["const", 1]]
    w2 = DictWorld({"items(a)": []}, SIG)
    res, _ = run("script f(s) { set(s, min_of(items(s), 7)) }", w2, ["a"])
    assert res.ok and w2.acts == [("set", ["a", 7])]
