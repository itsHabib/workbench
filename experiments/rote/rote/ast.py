"""Syntax tree for Rote. Small on purpose: expressions, four statement forms, four declarations."""

from __future__ import annotations

from dataclasses import dataclass, field

__all__ = [
    "Node", "Expr", "Stmt", "Decl",
    "IntLit", "StrLit", "BoolLit", "UnitLit", "Var", "ListLit", "RecordLit", "Lambda",
    "Call", "FieldAccess", "IndexAccess", "Unary", "Binary", "If", "Block",
    "Use", "Ask", "Propose", "Return",
    "Let", "For", "ExprStmt",
    "SigItem", "WorldDecl", "CapDecl", "ScriptDecl", "TacticDecl", "Program",
]


@dataclass
class Node:
    line: int = field(default=0, kw_only=True)


class Expr(Node):
    pass


class Stmt(Node):
    pass


class Decl(Node):
    pass


@dataclass
class IntLit(Expr):
    value: int


@dataclass
class StrLit(Expr):
    value: str


@dataclass
class BoolLit(Expr):
    value: bool


@dataclass
class UnitLit(Expr):
    pass


@dataclass
class Var(Expr):
    name: str


@dataclass
class ListLit(Expr):
    items: list[Expr]


@dataclass
class RecordLit(Expr):
    fields: list[tuple[str, Expr]]


@dataclass
class Lambda(Expr):
    params: list[str]
    body: Block


@dataclass
class Call(Expr):
    func: Expr
    args: list[Expr]


@dataclass
class FieldAccess(Expr):
    obj: Expr
    name: str


@dataclass
class IndexAccess(Expr):
    obj: Expr
    index: Expr


@dataclass
class Unary(Expr):
    op: str
    operand: Expr


@dataclass
class Binary(Expr):
    op: str
    left: Expr
    right: Expr


@dataclass
class If(Expr):
    cond: Expr
    then: Block
    otherwise: Expr | None  # Block or nested If


@dataclass
class Block(Expr):
    stmts: list[Stmt]


@dataclass
class Use(Expr):
    cap: str
    args: list[Expr]


@dataclass
class Ask(Expr):
    prompt: Expr


@dataclass
class Propose(Expr):
    source: Expr


@dataclass
class Return(Expr):
    value: Expr | None


@dataclass
class Let(Stmt):
    name: str
    value: Expr


@dataclass
class For(Stmt):
    var: str
    iterable: Expr
    body: Block


@dataclass
class ExprStmt(Stmt):
    expr: Expr


@dataclass
class SigItem(Node):
    kind: str  # "observe" | "act"
    name: str
    params: list[str]
    type: str | None


@dataclass
class WorldDecl(Decl):
    name: str
    items: list[SigItem]


@dataclass
class CapDecl(Decl):
    name: str
    params: list[str]
    goal: Expr
    doc: str | None = None


@dataclass
class ScriptDecl(Decl):
    name: str
    params: list[str]
    body: Block
    source: str = ""
    when: Expr | None = None  # optional author-written applicability condition


@dataclass
class TacticDecl(Decl):
    name: str
    params: list[str]
    body: Block
    source: str = ""


@dataclass
class Program(Node):
    decls: list[Decl]
