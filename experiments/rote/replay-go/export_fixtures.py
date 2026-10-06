#!/usr/bin/env python3
"""Export replay conformance fixtures for a replayer written in another language.

For every witness in runs/library.json, replay it in Python against a number of
worlds (the demo's and random ones) while recording, in execution order, what the
world answered to each observe/act/use step. A fixture is that answer sequence plus
the outcome Python's kernel produced: completed or side-exited at which step, and the
exact actions (name + evaluated arguments) performed. A conforming replayer must
reproduce the outcome from the witness and the answers alone.

Run:  python3 replay-go/export_fixtures.py   -> runs/replay_fixtures.json
"""

from __future__ import annotations

import copy
import json
import random
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(HERE))

from rote.library import Library  # noqa: E402
from rote.runtime import Policy, Runtime  # noqa: E402
from rote.witness import Evidence, replay  # noqa: E402
from tests.test_property import GRANT, random_world  # noqa: E402
from worlds.fleet import FleetWorld  # noqa: E402


class Recording:
    """Wraps a world and a runtime so every answer a replay receives is logged in order."""

    def __init__(self, world: FleetWorld, runtime: Runtime):
        self.world, self.runtime = world, runtime
        self.answers: list[dict] = []
        self.label = world.label

    def signature(self):
        return self.world.signature()

    def observe(self, name, args):
        v = self.world.observe(name, args)
        self.answers.append({"op": "observe", "value": v})
        return v

    def act(self, name, args):
        v = self.world.act(name, args)
        self.answers.append({"op": "act", "value": v})
        return v

    def achieve(self, cap, args, world):
        out = self.runtime.achieve(cap, args, self.world)
        self.answers.append({"op": "use", "value": out.summary(), "acts": [[n, a] for n, a in out.acts]})
        return out


# Hand-written scripts that between them touch every one of the 21 expression forms
# (const param ref field index len has get keys contains str not neg min max bin list
# record sum maxof minof), so a foreign kernel is checked on all of them, not only on
# the forms the demo's witnesses happen to use.
SYNTHETIC = [
    """script heal(service) {
  let host = host_of(service)
  let ports = ports_in_use(host)
  let free = max_of(ports, 8000) + 1
  let low = min_of(ports, 0)
  let cfg = config(service)
  if has(cfg, "port") { set_config(service, "pair", [cfg.port, host, free]) }
  if sum(ports) > low { set_config(service, "port", free) }
  restart(service)
}""",
    """script heal(service) {
  let cfg = config(service)
  let ks = keys(cfg)
  let label = str(cfg) + ":" + str(len(ks)) + ":" + str(lock_held(service))
  if has(cfg, "listen") { set_config(service, "note", label) }
  let p = get(cfg, "port", 0)
  if len(ks) > 0 and p % 2 == 0 and not (p / 2 > 5000) { set_config(service, "even", true) }
  if len(ks) > 0 { set_config(service, "share", 100 / len(ks)) }
  restart(service)
}""",
    """script heal(service) {
  let svcs = services_on(host_of(service))
  let others = filter(svcs, fn(x) { x != service })
  if len(others) > 0 {
    let first = others[0]
    if status(first) == "up" and contains(svcs, first) { set_config(service, "peer", first) }
  }
  let m = max(len(svcs), 1) - min(len(svcs), 3)
  set_config(service, "neg", -m)
  if any(map(svcs, fn(x) { status(x) == "down" })) { restart(service) }
}""",
    """script heal(service) {
  let cfg = config(service)
  let snapshot = {port: get(cfg, "port", 0), host: host_of(service), tags: [1, "a", true]}
  if snapshot == {port: 8000, host: "h0", tags: [1, "a", true]} { set_config(service, "seen", snapshot) }
  if contains(log_tail(service), "no space") { clear_tmp(host_of(service)) }
  for d in depends_on(service) { use heal(d) }
  restart(service)
}""",
]


def synthetic_cases(rng: random.Random) -> tuple[Library, list[dict]]:
    from live.world6 import GRANT6, LIBRARY6, random_fleet
    from rote.eval import Tracer, run_script
    from rote.parser import parse_program
    from rote.witness import witness_from_steps

    lib = Library.from_source(LIBRARY6)
    cases = []
    for src in SYNTHETIC:
        decl = parse_program(src).decls[0]
        retained = []
        for _ in range(8):  # validate on several worlds so different paths become witnesses
            world = random_fleet(rng, "syn")
            arg = rng.choice(list(world.services))
            if rng.random() < 0.6:
                world.services[arg]["status"] = "down"
            rt = Runtime(lib, Policy(grant=GRANT6, mode="replay-only"), None)
            t = Tracer(world, grant=set(GRANT6), caps=lib.arities(), runtime=rt)
            res = run_script(t, decl.params, [arg], decl.body)
            if not res.ok:
                continue
            w = lib.retain(
                witness_from_steps("heal", decl.params, res.steps, src),
                Evidence("validated", "pass", world.label, {"service": arg}, 0, "synthetic"),
            )
            retained.append(w)
        for w in retained:
            for _ in range(6):
                world = random_fleet(rng, "syn")
                arg = rng.choice(list(world.services))
                rt = Runtime(lib, Policy(grant=GRANT6, mode="replay-only"), None)
                rec = Recording(world, rt)
                r = replay(w, [arg], rec, GRANT6, runtime=rec)
                cases.append(
                    {
                        "witness": w.hash,
                        "cap": "heal",
                        "args": {"service": arg},
                        "answers": rec.answers,
                        "expected": {"kind": r.kind, "step": r.step, "acts": [[n, a] for n, a in r.acts]},
                    }
                )
    return lib, cases


FORMS = {
    "const",
    "param",
    "ref",
    "field",
    "index",
    "len",
    "has",
    "get",
    "keys",
    "contains",
    "str",
    "not",
    "neg",
    "min",
    "max",
    "bin",
    "list",
    "record",
    "sum",
    "maxof",
    "minof",
}


def forms_used(lib: Library) -> set[str]:
    """Every expression tag that occurs in a library's witnesses."""
    tags: set[str] = set()

    def walk(x):
        if isinstance(x, list):
            if x and isinstance(x[0], str) and x[0] in FORMS:
                tags.add(x[0])
            for y in x:
                walk(y)
        elif isinstance(x, dict):
            for y in x.values():
                walk(y)

    for c in lib.caps.values():
        for w in c.witnesses:
            walk(w.steps)
    return tags


# Type-drift cases from the first external review: a witness validated on one shape of
# answer, replayed on another. Python's outcome is authoritative; a foreign kernel must
# refuse exactly where the interpreter would have failed.
DRIFT_LIBRARY = """
world drift {
  observe cfg(s): record
  observe n(s): int
  observe k(s): string
  observe xs(s): list
  act write(s, v): bool
}
cap f(s) { goal: true }
"""
DRIFT_SIGNATURE = {
    "cfg": ("observe", 1),
    "n": ("observe", 1),
    "k": ("observe", 1),
    "xs": ("observe", 1),
    "write": ("act", 2),
}
BIG = 2**63 - 1
DRIFT_CASES = [
    # (script, answers while validating, answers while replaying)
    ("script f(s) { write(s, str([n(s)])) }", {"n": 1}, {"n": 2}),
    ("script f(s) { write(s, str(n(s) + 1)) }", {"n": 1}, {"n": BIG}),
    (
        "script f(s) { if has(cfg(s), k(s)) { write(s, 1) } }",
        {"cfg": {}, "k": "missing"},
        {"cfg": {}, "k": 1},
    ),
    (
        "script f(s) { write(s, get(cfg(s), k(s), 0)) }",
        {"cfg": {"a": 1}, "k": "a"},
        {"cfg": {"a": 1}, "k": 1},
    ),
    (
        'script f(s) { let p = cfg(s).port + 1\n write(s, "restart") }',
        {"cfg": {"port": 80}},
        {"cfg": {"port": "eighty"}},
    ),
    ("script f(s) { write(s, -n(s)) }", {"n": 5}, {"n": -(2**63)}),
    ("script f(s) { write(s, sum([n(s), n(s)])) }", {"n": 5}, {"n": BIG}),
    ("script f(s) { write(s, n(s) * 2) }", {"n": 5}, {"n": -(2**62) - 1}),
    ("script f(s) { write(s, len(xs(s)) + sum(xs(s))) }", {"xs": [1, 2]}, {"xs": [1, "two"]}),
    ("script f(s) { write(s, cfg(s)[k(s)]) }", {"cfg": {"a": 7}, "k": "a"}, {"cfg": {"a": 7}, "k": 2}),
]


class CannedWorld:
    label = "drift"

    def __init__(self, answers):
        self.answers = answers
        self.history = []

    def signature(self):
        return DRIFT_SIGNATURE

    def observe(self, name, args):
        return copy.deepcopy(self.answers[name])

    def act(self, name, args):
        self.history.append((name, list(args)))
        return True


def drift_cases() -> tuple[Library, list[dict]]:
    from rote.eval import Tracer, run_script
    from rote.parser import parse_program
    from rote.witness import witness_from_steps

    lib = Library.from_source(DRIFT_LIBRARY)
    cases = []
    for src, validate_with, replay_with in DRIFT_CASES:
        decl = parse_program(src).decls[0]
        t = Tracer(CannedWorld(validate_with), grant={"write"}, caps={})
        res = run_script(t, decl.params, ["a"], decl.body)
        assert res.ok, (src, res.error)
        w = lib.retain(
            witness_from_steps("f", decl.params, res.steps, src),
            Evidence("validated", "pass", "drift", {"s": "a"}, 0, "drift fixture"),
        )
        for answers in (validate_with, replay_with):
            rec = Recording(CannedWorld(answers), None)
            r = replay(w, ["a"], rec, {"write"}, runtime=rec)
            cases.append(
                {
                    "witness": w.hash,
                    "cap": "f",
                    "args": {"s": "a"},
                    "answers": rec.answers,
                    "expected": {"kind": r.kind, "step": r.step, "acts": [[n, a] for n, a in r.acts]},
                }
            )
    return lib, cases


def main() -> None:
    _export_demo_and_synthetic()
    drift_lib, drift = drift_cases()
    drift_lib.save(HERE / "runs" / "drift_library.json")
    (HERE / "runs" / "drift_fixtures.json").write_text(json.dumps(drift, indent=1) + "\n")
    print(
        f"wrote {len(drift)} drift cases over {len(drift_lib.caps['f'].witnesses)} witnesses: {_kinds(drift)}"
    )


def _export_demo_and_synthetic() -> None:
    lib = Library.load(HERE / "runs" / "library.json")
    rng = random.Random(7)
    cases = []
    for cap in lib.caps.values():
        for w in cap.witnesses:
            for _ in range(12):
                world = random_world(rng)
                if cap.name == "recover_host":
                    world.add_host("h3", disk=40)
                    for i in range(rng.choice([2, 3, 3, 4])):
                        world.add_service(f"x{i}", "h3", 9100 + i)
                        if rng.random() < 0.5:
                            world.crash(f"x{i}")
                    args = ["h3"]
                else:
                    args = [rng.choice(list(world.services))]
                rt = Runtime(lib, Policy(grant=GRANT, mode="replay-only"), None)
                rec = Recording(world, rt)
                r = replay(w, args, rec, GRANT, runtime=rec)
                cases.append(
                    {
                        "witness": w.hash,
                        "cap": cap.name,
                        "args": dict(zip(w.params, args, strict=True)),
                        "answers": rec.answers,
                        "expected": {"kind": r.kind, "step": r.step, "acts": [[n, a] for n, a in r.acts]},
                    }
                )
    out = HERE / "runs" / "replay_fixtures.json"
    out.write_text(json.dumps(cases, indent=1) + "\n")
    print(f"wrote {len(cases)} cases to {out}: {_kinds(cases)}")
    syn_lib, syn_cases = synthetic_cases(random.Random(11))
    syn_lib.save(HERE / "runs" / "synthetic_library.json")
    (HERE / "runs" / "synthetic_fixtures.json").write_text(json.dumps(syn_cases, indent=1) + "\n")
    used = forms_used(syn_lib)
    print(
        f"wrote {len(syn_cases)} synthetic cases over {sum(len(c.witnesses) for c in syn_lib.caps.values())} "
        f"witnesses: {_kinds(syn_cases)}; forms used: {len(used)}/21 {sorted(used)}"
    )


def _kinds(cases: list[dict]) -> dict[str, int]:
    kinds: dict[str, int] = {}
    for c in cases:
        kinds[c["expected"]["kind"]] = kinds.get(c["expected"]["kind"], 0) + 1
    return kinds


if __name__ == "__main__":
    main()
