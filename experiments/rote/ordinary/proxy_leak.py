#!/usr/bin/env python3
"""Why not infer guards with Python proxy objects? Because Python cannot mediate everything.

This is the best-effort "tracer in the host language": observed values come back wrapped
in a Proxy that records a guard whenever it is compared, tested for truth, indexed,
iterated or searched. It works for most operations. It cannot work for `len()`,
`bool()` of a container, `is`, `isinstance`, `type`, dict-key hashing, or any C-level
consumer: Python requires `__len__` to return a plain int, so the moment a script decides
something from `len(observed_list)` the dependency is gone, silently.

The script below is a *reasonable* safety rule: reboot a host only when it runs a single
service. Validated on a one-service host, the recorded trace has no guard on the service
count, so replaying it on a three-service host reboots two healthy services.

Run:  python3 ordinary/proxy_leak.py
"""

from __future__ import annotations

import sys
from pathlib import Path
from typing import Any

HERE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(HERE))

from worlds.fleet import FleetWorld  # noqa: E402


class Proxy:
    """An observed value that records how it is used."""

    def __init__(self, tracer: ProxyTracer, expr: str, value: Any):
        self.t, self.expr, self.v = tracer, expr, value

    def _cmp(self, op: str, other: Any, result: bool) -> Proxy:
        o = other.expr if isinstance(other, Proxy) else repr(other)
        return Proxy(self.t, f"({self.expr} {op} {o})", result)

    def __eq__(self, other: Any) -> Proxy:  # type: ignore[override]
        return self._cmp("==", other, self.v == _raw(other))

    def __ne__(self, other: Any) -> Proxy:  # type: ignore[override]
        return self._cmp("!=", other, self.v != _raw(other))

    def __gt__(self, other: Any) -> Proxy:
        return self._cmp(">", other, self.v > _raw(other))

    def __lt__(self, other: Any) -> Proxy:
        return self._cmp("<", other, self.v < _raw(other))

    def __add__(self, other: Any) -> Proxy:
        return Proxy(self.t, f"({self.expr} + {_show(other)})", self.v + _raw(other))

    def __bool__(self) -> bool:  # every `if`, `and`, `or`, `not` lands here -> guard
        self.t.guards.append((self.expr, bool(self.v)))
        return bool(self.v)

    def __getitem__(self, key: Any) -> Proxy:
        self.t.guards.append((f"has({self.expr}, {_show(key)})", True))
        return Proxy(self.t, f"{self.expr}[{_show(key)}]", self.v[_raw(key)])

    def __contains__(self, item: Any) -> bool:
        result = _raw(item) in self.v
        self.t.guards.append((f"contains({self.expr}, {_show(item)})", result))
        return result

    def __iter__(self):
        self.t.guards.append((f"len({self.expr})", len(self.v)))
        return (Proxy(self.t, f"{self.expr}[{i}]", x) for i, x in enumerate(self.v))

    def __len__(self) -> int:
        # Python insists on a real int here. There is no way to return a Proxy, so the
        # dependency of whatever is decided from this length is lost.
        return len(self.v)

    __hash__ = None  # type: ignore[assignment]


def _raw(x: Any) -> Any:
    return x.v if isinstance(x, Proxy) else x


def _show(x: Any) -> str:
    return x.expr if isinstance(x, Proxy) else repr(x)


class ProxyTracer:
    def __init__(self, world: FleetWorld):
        self.w = world
        self.guards: list[tuple[str, Any]] = []
        self.trace: list[tuple[str, list[Any]]] = []

    def __getattr__(self, name: str):
        kind, _ = self.w.signature().get(name, (None, 0))
        if kind == "observe":
            return lambda *a: Proxy(self, f"{name}({', '.join(_show(x) for x in a)})", self.w.observe(name, [_raw(x) for x in a]))
        if kind == "act":
            def act(*a: Any) -> Any:
                args = [_raw(x) for x in a]
                self.trace.append((name, args))
                return self.w.act(name, args)
            return act
        raise AttributeError(name)


def heal(w: Any, service: str) -> None:
    """A reasonable runbook: reboot the host only when the blast radius is one service."""
    host = w.host_of(service)
    if len(w.services_on(host)) > 1:
        w.restart(service)
    else:
        w.reboot_host(host)


def replay(trace: list[tuple[str, list[Any]]], world: FleetWorld) -> None:
    for name, args in trace:
        world.act(name, args)


def main() -> dict[str, Any]:
    small = FleetWorld("one-service host")
    small.add_host("h1")
    small.add_service("solo", "h1", 8000)
    small.crash("solo")
    t = ProxyTracer(small)
    heal(t, "solo")
    recorded_trace, recorded_guards = t.trace, t.guards

    big = FleetWorld("three-service host")
    big.add_host("h1")
    for n, p in (("a", 8000), ("b", 8001), ("c", 8002)):
        big.add_service(n, "h1", p)
    big.crash("a")
    replay(recorded_trace, big)  # no guard mentions the service count, so nothing stops this

    rote_guards = rote_version(small)
    out = {
        "python_trace": recorded_trace,
        "python_guards": recorded_guards,
        "python_disruptions_on_replay": big.disruptions,
        "rote_guards": rote_guards,
    }
    print("Python proxy tracer, validated on the one-service host:")
    print("  trace :", recorded_trace)
    print("  guards:", recorded_guards or "(none recorded: the only decision went through len())")
    print(f"  replayed on the three-service host -> healthy services disrupted: {big.disruptions}")
    print("Rote, same script, same validation world:")
    for g in rote_guards:
        print("  guard :", g)
    return out


def rote_version(small: FleetWorld) -> list[str]:
    from rote.eval import Tracer, run_script
    from rote.parser import parse_program
    from rote.witness import describe_guards, witness_from_steps

    src = '''script heal(service) {
  let host = host_of(service)
  if len(services_on(host)) > 1 { restart(service) } else { reboot_host(host) }
}'''
    snap = small.snapshot()
    small.restore(snap)
    small.services["solo"]["status"] = "down"
    decl = parse_program(src).decls[0]
    t = Tracer(small, grant={"restart", "reboot_host"}, caps={})
    res = run_script(t, decl.params, ["solo"], decl.body)
    assert res.ok, res.error
    return describe_guards(witness_from_steps("heal", decl.params, res.steps, src))


if __name__ == "__main__":
    main()
