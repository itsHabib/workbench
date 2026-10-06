from rote.effects import check_goal, check_script, check_tactic
from rote.parser import parse_expr, parse_program
from tests.helpers import SIG


def body(src):
    return parse_program(src).decls[0]


def test_effect_row_is_exact():
    d = body('script f(s) { if status(s) == "down" { do(s) }\n set(s, 1) }')
    row, errs = check_script(d.params, d.body, SIG, {"do", "set"})
    assert not errs
    assert row.observes == {"status"} and row.acts == {"do", "set"} and not row.asks


def test_ask_is_rejected_in_scripts_but_allowed_in_tactics():
    d = body('script f(s) { set(s, ask("port")) }')
    _, errs = check_script(d.params, d.body, SIG, {"set"})
    assert [e.kind for e in errs] == ["effect"]
    t = body('tactic f(s) { propose(ask({cap: "f"})) }')
    row, errs = check_tactic(t.params, t.body, SIG)
    assert not errs and row.asks and row.proposes


def test_ungranted_action_is_refused_statically():
    d = body("script f(s) { do(s)\n set(s, 1) }")
    _, errs = check_script(d.params, d.body, SIG, {"do"})
    assert [(e.kind, "set" in e.msg) for e in errs] == [("grant", True)]


def test_goal_may_only_observe():
    g = parse_expr('status(s) == "up"')
    _, errs = check_goal(["s"], g, SIG)
    assert not errs
    g = parse_expr("do(s)")
    _, errs = check_goal(["s"], g, SIG)
    assert [e.kind for e in errs] == ["effect"]


def test_names_arity_and_first_class_misuse():
    d = body("script f(s) { let g = status\n nope(s)\n do(s, s)\n let h = fn(x) { y } }")
    _, errs = check_script(d.params, d.body, SIG, {"do"})
    assert sorted(e.kind for e in errs) == ["arity", "name", "name", "name"]


def test_use_needs_a_known_capability_with_matching_arity():
    d = body("script f(s) { use heal(s)\n use heal(s, s)\n use missing(s) }")
    _, errs = check_script(d.params, d.body, SIG, set(), {"heal": 1})
    assert sorted(e.kind for e in errs) == ["arity", "name"]


def test_lambda_parameters_and_lets_scope():
    d = body("script f(s) { let xs = items(s)\n map(xs, fn(x) { x + 1 })\n for y in xs { y } }")
    _, errs = check_script(d.params, d.body, SIG, set())
    assert not errs
