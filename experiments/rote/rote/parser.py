"""Recursive-descent parser. Grammar summary (see DESIGN.md for the full table):

program  := decl*
decl     := 'world' NAME '{' (('observe'|'act') NAME '(' names ')' (':' NAME)?)* '}'
          | 'cap' NAME '(' names ')' '{' 'goal' ':' expr '}'
          | 'script' NAME '(' names ')' ('when' expr)? block | 'tactic' NAME '(' names ')' block
block    := '{' stmt* '}'
stmt     := 'let' NAME '=' expr | 'for' NAME 'in' expr block | expr
expr     := or ; or := and ('or' and)* ; and := not ('and' not)* ; not := 'not' not | cmp
cmp      := add (CMPOP add)? ; add := mul (('+'|'-') mul)* ; mul := unary (('*'|'/'|'%') unary)*
unary    := '-' unary | postfix ; postfix := primary ('(' args ')' | '.' NAME | '[' expr ']')*
primary  := INT | STR | 'true' | 'false' | 'unit' | NAME | '(' expr ')' | '[' args ']' | '{' fields '}'
          | 'fn' '(' names ')' block | 'if' expr block ('else' (block | if))? | 'return' expr?
          | 'use' NAME '(' args ')' | 'ask' '(' expr ')' | 'propose' '(' expr ')'
"""

from __future__ import annotations

from . import ast as A
from .lexer import Token, tokenize

__all__ = ["ParseError", "parse_program", "parse_expr"]

_CMP = {"==", "!=", "<", "<=", ">", ">="}


class ParseError(Exception):
    pass


class _Parser:
    def __init__(self, src: str):
        self.src = src
        self.toks = tokenize(src)
        self.pos = 0

    # -- token helpers -------------------------------------------------------
    def peek(self, off: int = 0) -> Token:
        return self.toks[min(self.pos + off, len(self.toks) - 1)]

    def advance(self) -> Token:
        t = self.toks[self.pos]
        self.pos += 1
        return t

    def at(self, kind: str, text: str | None = None) -> bool:
        t = self.peek()
        return t.kind == kind and (text is None or t.text == text)

    def at_punct(self, text: str) -> bool:
        return self.at("punct", text)

    def at_kw(self, text: str) -> bool:
        return self.at("kw", text)

    def expect(self, kind: str, text: str | None = None) -> Token:
        if not self.at(kind, text):
            t = self.peek()
            want = text or kind
            raise ParseError(f"line {t.line}: expected {want!r}, found {t.text!r}")
        return self.advance()

    def skip_newlines(self) -> None:
        while self.at("newline"):
            self.advance()

    def ident(self) -> str:
        return self.expect("ident").text

    # -- declarations --------------------------------------------------------
    def program(self) -> A.Program:
        decls: list[A.Decl] = []
        self.skip_newlines()
        while not self.at("eof"):
            decls.append(self.decl())
            self.skip_newlines()
        return A.Program(decls)

    def decl(self) -> A.Decl:
        t = self.peek()
        if self.at_kw("world"):
            return self.world_decl()
        if self.at_kw("cap"):
            return self.cap_decl()
        if self.at_kw("script") or self.at_kw("tactic"):
            return self.script_decl()
        raise ParseError(f"line {t.line}: expected a declaration, found {t.text!r}")

    def names(self) -> list[str]:
        self.expect("punct", "(")
        out: list[str] = []
        self.skip_newlines()
        while not self.at_punct(")"):
            out.append(self.ident())
            self.skip_newlines()
            if self.at_punct(","):
                self.advance()
                self.skip_newlines()
        self.expect("punct", ")")
        return out

    def world_decl(self) -> A.WorldDecl:
        line = self.expect("kw", "world").line
        name = self.ident()
        self.expect("punct", "{")
        items: list[A.SigItem] = []
        self.skip_newlines()
        while not self.at_punct("}"):
            kind = self.advance()
            if kind.kind != "kw" or kind.text not in ("observe", "act"):
                raise ParseError(f"line {kind.line}: expected 'observe' or 'act', found {kind.text!r}")
            fname = self.ident()
            params = self.names()
            typ = None
            if self.at_punct(":"):
                self.advance()
                typ = self.ident()
            items.append(A.SigItem(kind.text, fname, params, typ, line=kind.line))
            self.skip_newlines()
        self.expect("punct", "}")
        return A.WorldDecl(name, items, line=line)

    def cap_decl(self) -> A.CapDecl:
        line = self.expect("kw", "cap").line
        name = self.ident()
        params = self.names()
        self.expect("punct", "{")
        self.skip_newlines()
        doc = None
        if self.at("str"):
            doc = self.advance().text
            self.skip_newlines()
        self.expect("kw", "goal")
        self.expect("punct", ":")
        self.skip_newlines()
        goal = self.expr()
        self.skip_newlines()
        self.expect("punct", "}")
        return A.CapDecl(name, params, goal, doc, line=line)

    def script_decl(self) -> A.Decl:
        start = self.advance()
        name = self.ident()
        params = self.names()
        when = None
        if start.text == "script" and self.at("ident", "when"):
            self.advance()
            when = self.expr()
        body = self.block()
        end_line = self.toks[self.pos - 1].line
        source = "\n".join(self.src.splitlines()[start.line - 1 : end_line])
        if start.text == "tactic":
            return A.TacticDecl(name, params, body, source, line=start.line)
        return A.ScriptDecl(name, params, body, source, when, line=start.line)

    # -- statements ----------------------------------------------------------
    def block(self) -> A.Block:
        line = self.expect("punct", "{").line
        stmts: list[A.Stmt] = []
        self.skip_newlines()
        while not self.at_punct("}"):
            stmts.append(self.stmt())
            if not (self.at("newline") or self.at_punct("}")):
                t = self.peek()
                raise ParseError(f"line {t.line}: expected end of statement, found {t.text!r}")
            self.skip_newlines()
        self.expect("punct", "}")
        return A.Block(stmts, line=line)

    def stmt(self) -> A.Stmt:
        if self.at_kw("let"):
            line = self.advance().line
            name = self.ident()
            self.expect("punct", "=")
            self.skip_newlines()
            return A.Let(name, self.expr(), line=line)
        if self.at_kw("for"):
            line = self.advance().line
            var = self.ident()
            self.expect("kw", "in")
            it = self.expr()
            body = self.block()
            return A.For(var, it, body, line=line)
        e = self.expr()
        return A.ExprStmt(e, line=e.line)

    # -- expressions ---------------------------------------------------------
    def expr(self) -> A.Expr:
        return self.or_expr()

    def or_expr(self) -> A.Expr:
        left = self.and_expr()
        while self.at_kw("or"):
            line = self.advance().line
            self.skip_newlines()
            left = A.Binary("or", left, self.and_expr(), line=line)
        return left

    def and_expr(self) -> A.Expr:
        left = self.not_expr()
        while self.at_kw("and"):
            line = self.advance().line
            self.skip_newlines()
            left = A.Binary("and", left, self.not_expr(), line=line)
        return left

    def not_expr(self) -> A.Expr:
        if self.at_kw("not"):
            line = self.advance().line
            return A.Unary("not", self.not_expr(), line=line)
        return self.cmp_expr()

    def cmp_expr(self) -> A.Expr:
        left = self.add_expr()
        if self.at("punct") and self.peek().text in _CMP:
            op = self.advance()
            self.skip_newlines()
            return A.Binary(op.text, left, self.add_expr(), line=op.line)
        return left

    def add_expr(self) -> A.Expr:
        left = self.mul_expr()
        while self.at_punct("+") or self.at_punct("-"):
            op = self.advance()
            self.skip_newlines()
            left = A.Binary(op.text, left, self.mul_expr(), line=op.line)
        return left

    def mul_expr(self) -> A.Expr:
        left = self.unary()
        while self.at_punct("*") or self.at_punct("/") or self.at_punct("%"):
            op = self.advance()
            self.skip_newlines()
            left = A.Binary(op.text, left, self.unary(), line=op.line)
        return left

    def unary(self) -> A.Expr:
        if self.at_punct("-"):
            line = self.advance().line
            return A.Unary("-", self.unary(), line=line)
        return self.postfix()

    def postfix(self) -> A.Expr:
        e = self.primary()
        while True:
            if self.at_punct("("):
                line = self.advance().line
                e = A.Call(e, self.args(")"), line=line)
            elif self.at_punct("."):
                line = self.advance().line
                e = A.FieldAccess(e, self.ident(), line=line)
            elif self.at_punct("["):
                line = self.advance().line
                self.skip_newlines()
                idx = self.expr()
                self.skip_newlines()
                self.expect("punct", "]")
                e = A.IndexAccess(e, idx, line=line)
            else:
                return e

    def args(self, closer: str) -> list[A.Expr]:
        out: list[A.Expr] = []
        self.skip_newlines()
        while not self.at_punct(closer):
            out.append(self.expr())
            self.skip_newlines()
            if self.at_punct(","):
                self.advance()
                self.skip_newlines()
        self.expect("punct", closer)
        return out

    def primary(self) -> A.Expr:
        t = self.peek()
        if t.kind == "int":
            self.advance()
            return A.IntLit(int(t.text), line=t.line)
        if t.kind == "str":
            self.advance()
            return A.StrLit(t.text, line=t.line)
        if t.kind == "ident":
            self.advance()
            return A.Var(t.text, line=t.line)
        if t.kind == "punct":
            return self.primary_punct(t)
        if t.kind == "kw":
            return self.primary_kw(t)
        raise ParseError(f"line {t.line}: unexpected {t.text!r}")

    def primary_punct(self, t: Token) -> A.Expr:
        if t.text == "(":
            self.advance()
            self.skip_newlines()
            e = self.expr()
            self.skip_newlines()
            self.expect("punct", ")")
            return e
        if t.text == "[":
            self.advance()
            return A.ListLit(self.args("]"), line=t.line)
        if t.text == "{":
            return self.record()
        raise ParseError(f"line {t.line}: unexpected {t.text!r}")

    def record(self) -> A.RecordLit:
        line = self.expect("punct", "{").line
        fields: list[tuple[str, A.Expr]] = []
        self.skip_newlines()
        while not self.at_punct("}"):
            key = self.advance()
            if key.kind not in ("ident", "str", "kw"):
                raise ParseError(f"line {key.line}: expected a field name, found {key.text!r}")
            self.expect("punct", ":")
            self.skip_newlines()
            fields.append((key.text, self.expr()))
            self.skip_newlines()
            if self.at_punct(","):
                self.advance()
                self.skip_newlines()
        self.expect("punct", "}")
        return A.RecordLit(fields, line=line)

    def primary_kw(self, t: Token) -> A.Expr:
        self.advance()
        if t.text == "true":
            return A.BoolLit(True, line=t.line)
        if t.text == "false":
            return A.BoolLit(False, line=t.line)
        if t.text == "unit":
            return A.UnitLit(line=t.line)
        if t.text == "fn":
            params = self.names()
            return A.Lambda(params, self.block(), line=t.line)
        if t.text == "if":
            return self.if_expr(t.line)
        if t.text == "return":
            if self.at("newline") or self.at_punct("}"):
                return A.Return(None, line=t.line)
            return A.Return(self.expr(), line=t.line)
        return self.primary_effect_kw(t)

    def primary_effect_kw(self, t: Token) -> A.Expr:
        if t.text == "use":
            cap = self.ident()
            self.expect("punct", "(")
            return A.Use(cap, self.args(")"), line=t.line)
        if t.text == "ask":
            self.expect("punct", "(")
            self.skip_newlines()
            e = self.expr()
            self.skip_newlines()
            self.expect("punct", ")")
            return A.Ask(e, line=t.line)
        if t.text == "propose":
            self.expect("punct", "(")
            self.skip_newlines()
            e = self.expr()
            self.skip_newlines()
            self.expect("punct", ")")
            return A.Propose(e, line=t.line)
        raise ParseError(f"line {t.line}: unexpected keyword {t.text!r}")

    def if_expr(self, line: int) -> A.If:
        cond = self.expr()
        then = self.block()
        otherwise: A.Expr | None = None
        if self.at_kw("else"):
            self.advance()
            if self.at_kw("if"):
                l2 = self.advance().line
                otherwise = self.if_expr(l2)
            else:
                otherwise = self.block()
        return A.If(cond, then, otherwise, line=line)


def parse_program(src: str) -> A.Program:
    return _Parser(src).program()


def parse_expr(src: str) -> A.Expr:
    p = _Parser(src)
    p.skip_newlines()
    e = p.expr()
    p.skip_newlines()
    if not p.at("eof"):
        t = p.peek()
        raise ParseError(f"line {t.line}: trailing input {t.text!r}")
    return e
