"""A tiny dict-backed world for unit tests."""

from __future__ import annotations

import copy
from typing import Any


class DictWorld:
    def __init__(self, obs: dict[str, Any], sig: dict[str, tuple[str, int]], label: str = "test"):
        self.obs = obs  # key: "name(arg1,arg2)" -> value
        self.sig = sig
        self.label = label
        self.acts: list[tuple[str, list[Any]]] = []
        self.act_results: dict[str, Any] = {}

    def signature(self) -> dict[str, tuple[str, int]]:
        return self.sig

    @staticmethod
    def key(name: str, args: list[Any]) -> str:
        return f"{name}(" + ",".join(str(a) for a in args) + ")"

    def observe(self, name: str, args: list[Any]) -> Any:
        return copy.deepcopy(self.obs[self.key(name, args)])

    def act(self, name: str, args: list[Any]) -> Any:
        self.acts.append((name, list(args)))
        return copy.deepcopy(self.act_results.get(self.key(name, args), True))


SIG = {
    "status": ("observe", 1), "cfg": ("observe", 1), "items": ("observe", 1), "n": ("observe", 1),
    "do": ("act", 1), "set": ("act", 2),
}
