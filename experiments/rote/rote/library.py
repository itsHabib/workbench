"""Capabilities and their retained witnesses, with JSON persistence.

A library file is the declared world signature, the capabilities (each a goal that
only observes, plus an optional in-language tactic), and the witnesses retained so
far with their evidence. The library makes no decisions beyond dispatch order.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from . import ast as A
from .effects import StaticError, check_goal, check_tactic
from .parser import parse_program
from .witness import Evidence, Witness

__all__ = ["Capability", "Library", "LibraryError"]


class LibraryError(Exception):
    pass


@dataclass
class Capability:
    name: str
    params: list[str]
    goal: A.Expr
    goal_src: str
    doc: str | None = None
    tactic: A.TacticDecl | None = None
    witnesses: list[Witness] = field(default_factory=list)

    def find(self, h: str) -> Witness | None:
        for w in self.witnesses:
            if w.hash == h:
                return w
        return None


@dataclass
class Library:
    world_name: str
    signature: dict[str, tuple[str, int]]
    caps: dict[str, Capability]
    program_src: str = ""
    retained: int = 0

    # -- construction ----------------------------------------------------------
    @staticmethod
    def from_source(src: str) -> Library:
        prog = parse_program(src)
        worlds = [d for d in prog.decls if isinstance(d, A.WorldDecl)]
        if len(worlds) != 1:
            raise LibraryError("a library needs exactly one world declaration")
        sig = {it.name: (it.kind, len(it.params)) for it in worlds[0].items}
        caps: dict[str, Capability] = {}
        for d in prog.decls:
            if isinstance(d, A.CapDecl):
                caps[d.name] = Capability(d.name, d.params, d.goal, _goal_source(src, d), d.doc)
        for d in prog.decls:
            if isinstance(d, A.TacticDecl):
                if d.name not in caps:
                    raise LibraryError(f"tactic {d.name!r} has no capability")
                caps[d.name].tactic = d
            if isinstance(d, A.ScriptDecl):
                raise LibraryError("scripts are proposed at run time, not declared in a library")
        lib = Library(worlds[0].name, sig, caps, src)
        lib.check()
        return lib

    def check(self) -> None:
        arities = {c.name: len(c.params) for c in self.caps.values()}
        problems: list[str] = []
        for c in self.caps.values():
            _, errs = check_goal(c.params, c.goal, self.signature)
            problems += [f"goal of {c.name}: {e}" for e in errs]
            if c.tactic is not None:
                _, terrs = check_tactic(c.tactic.params, c.tactic.body, self.signature, arities)
                problems += [f"tactic of {c.name}: {e}" for e in terrs]
        if problems:
            raise LibraryError("; ".join(problems))

    def arities(self) -> dict[str, int]:
        return {c.name: len(c.params) for c in self.caps.values()}

    def cap(self, name: str) -> Capability:
        if name not in self.caps:
            raise LibraryError(f"unknown capability {name!r}")
        return self.caps[name]

    # -- retention and dispatch ------------------------------------------------
    def retain(self, w: Witness, evidence: Evidence) -> Witness:
        """Keep a witness; an identical path already retained just gains evidence."""
        cap = self.cap(w.cap)
        existing = cap.find(w.hash)
        if existing is not None:
            existing.evidence.append(evidence)
            return existing
        self.retained += 1
        w.seq = self.retained
        w.evidence.append(evidence)
        cap.witnesses.append(w)
        return w

    def dispatch_order(self, cap: Capability) -> list[Witness]:
        """Fewest recorded failures first, then most passes, then newest."""
        return sorted(cap.witnesses, key=lambda w: (w.counts()[1], -w.counts()[0], -w.seq))

    # -- persistence ---------------------------------------------------------------
    def to_json(self) -> dict[str, Any]:
        return {
            "world": self.world_name,
            "program": self.program_src,
            "retained": self.retained,
            "witnesses": [w.to_json() for c in self.caps.values() for w in c.witnesses],
        }

    def save(self, path: str | Path) -> None:
        Path(path).write_text(json.dumps(self.to_json(), indent=1, sort_keys=True) + "\n")

    @staticmethod
    def load(path: str | Path) -> Library:
        data = json.loads(Path(path).read_text())
        lib = Library.from_source(data["program"])
        lib.retained = data.get("retained", 0)
        for wd in data.get("witnesses", []):
            w = Witness.from_json(wd)
            lib.cap(w.cap).witnesses.append(w)
        return lib


def _goal_source(src: str, d: A.CapDecl) -> str:
    lines = src.splitlines()
    for ln in lines[d.line - 1 :]:
        if "goal:" not in ln:
            continue
        text = ln.split("goal:", 1)[1].strip()
        if text.endswith("}") and text.count("}") > text.count("{"):
            text = text[:-1].rstrip()
        return text
    return "<goal>"


def static_errors_text(errs: list[StaticError]) -> str:
    return "; ".join(str(e) for e in errs)
