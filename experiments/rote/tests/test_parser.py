import pytest

from rote import ast as A
from rote.lexer import LexError
from rote.parser import ParseError, parse_expr, parse_program


def test_parses_declarations_and_keeps_script_source():
    src = '''
world w { observe status(s): string
          act do(s): bool }
cap heal(s) { "doc" goal: status(s) == "up" }
script heal(s) {
  let x = status(s)
  if x == "down" { do(s) } else { unit }
}
tactic heal(s) { propose(ask({cap: "heal"})) }
'''
    prog = parse_program(src)
    kinds = [type(d).__name__ for d in prog.decls]
    assert kinds == ["WorldDecl", "CapDecl", "ScriptDecl", "TacticDecl"]
    cap = prog.decls[1]
    assert cap.doc == "doc" and isinstance(cap.goal, A.Binary)
    script = prog.decls[2]
    assert script.source.startswith("script heal(s) {") and script.source.rstrip().endswith("}")


def test_newline_ends_a_statement_instead_of_continuing_a_call():
    prog = parse_program("script f(a) {\n  a\n  (a)\n}")
    body = prog.decls[0].body
    assert len(body.stmts) == 2
    assert isinstance(body.stmts[1].expr, A.Var)


def test_precedence_and_records():
    e = parse_expr("not a and b or c == 1 + 2 * 3")
    assert isinstance(e, A.Binary) and e.op == "or"
    rec = parse_expr("{a: 1, b: [1, 2], c: {d: x.y[0]}}")
    assert isinstance(rec, A.RecordLit) and [k for k, _ in rec.fields] == ["a", "b", "c"]


def test_errors_are_positioned():
    with pytest.raises(ParseError, match="line 2"):
        parse_program("script f(a) {\n  let = 3\n}")
    with pytest.raises(LexError, match="unterminated"):
        parse_program('script f(a) { "abc }')
    with pytest.raises(ParseError, match="trailing"):
        parse_expr("1 2")


def test_else_if_chain_and_return():
    prog = parse_program("script f(a) { if a { return 1 } else if a { 2 } else { 3 } }")
    stmt = prog.decls[0].body.stmts[0].expr
    assert isinstance(stmt, A.If) and isinstance(stmt.otherwise, A.If)
