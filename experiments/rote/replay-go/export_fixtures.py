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
from rote.witness import replay  # noqa: E402
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


def main() -> None:
    lib = Library.load(HERE / "runs" / "library.json")
    rng = random.Random(7)
    cases = []
    for cap in lib.caps.values():
        for w in cap.witnesses:
            for trial in range(12):
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
                cases.append({
                    "witness": w.hash, "cap": cap.name, "args": dict(zip(w.params, args, strict=True)),
                    "answers": rec.answers,
                    "expected": {"kind": r.kind, "step": r.step, "acts": [[n, a] for n, a in r.acts]},
                })
    out = HERE / "runs" / "replay_fixtures.json"
    out.write_text(json.dumps(cases, indent=1) + "\n")
    kinds = {}
    for c in cases:
        kinds[c["expected"]["kind"]] = kinds.get(c["expected"]["kind"], 0) + 1
    print(f"wrote {len(cases)} cases to {out}: {kinds}")


if __name__ == "__main__":
    main()
