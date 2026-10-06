"""The central soundness claim, tested empirically:

    if replaying a witness against a fresh world completes, the original script run
    against that same world performs exactly the same effects, in the same order, with
    the same arguments; and if the replay side-exits at step k, the two agree up to k.

Random worlds, every scripted proposal, many trials. A missing guard anywhere in the
evaluator would show up here as a divergence.
"""

from __future__ import annotations

import copy
import json
import random
import zlib
from pathlib import Path

import pytest
from rote.eval import Tracer, run_script
from rote.parser import parse_program
from rote.witness import replay, witness_from_steps
from worlds.fleet import NO_SPACE, SEGFAULT, FleetWorld

HERE = Path(__file__).resolve().parents[1]
GRANT = {"restart", "clear_tmp", "set_config", "reboot_host"}


def proposals():
    data = json.loads((HERE / "scenario" / "proposals.json").read_text())
    out = []
    for cap, items in data.items():
        if cap.startswith("_"):
            continue
        for it in items:
            if "planted" in it["label"] or cap != "heal":
                continue
            out.append((it["label"], it["source"]))
    return out


def random_world(rng: random.Random) -> FleetWorld:
    w = FleetWorld("random")
    for h in ("h1", "h2"):
        w.add_host(h, disk=rng.choice([20, 40, 60, 95, 99]))
    names = ["a", "b", "c", "d", "e"]
    for n in names:
        host = rng.choice(["h1", "h2"])
        port = rng.choice([8000, 8001, 8002, 9000])
        w.add_service(n, host, port, nested=rng.random() < 0.3)
        svc = w.services[n]
        if rng.random() < 0.7:
            svc["status"] = "down"
            svc["log"].append(
                rng.choice([SEGFAULT, NO_SPACE, "bind: address already in use (:8000)", "weird"])
            )
        if rng.random() < 0.1:
            svc["config"] = {"version": 3}
    return w


def effects(world: FleetWorld, src: str, arg: str):
    decl = parse_program(src).decls[0]
    t = Tracer(world, grant=GRANT, caps={})
    res = run_script(t, decl.params, [arg], decl.body)
    return res, [(s["name"], s["args"]) for s in res.steps if s["op"] in ("observe", "act")]


def _seed(label: str) -> int:
    return zlib.crc32(label.encode())  # stable across processes, unlike Python's salted hash()


@pytest.mark.parametrize("label,src", proposals())
def test_replay_completes_on_the_world_it_was_validated_on(label: str, src: str):
    """Guaranteed completion: replaying a witness against the very world that produced it must
    complete and perform exactly the same effects. No luck involved."""
    rng = random.Random(_seed(label))
    checked = 0
    for _ in range(60):
        base = random_world(rng)
        arg = rng.choice(list(base.services))
        res, _ = effects(copy.deepcopy(base), src, arg)
        if not res.ok:
            continue
        w = witness_from_steps("heal", ["service"], res.steps, src)
        a, b = copy.deepcopy(base), copy.deepcopy(base)
        r = replay(w, [arg], a, GRANT)
        effects(b, src, arg)
        assert r.kind == "completed", f"{label}: {r.reason}"
        assert a.history == b.history and a.snapshot() == b.snapshot()
        checked += 1
    assert checked > 0, f"{label}: the script never validated on 60 random worlds"


def test_a_witness_side_exits_before_acting_when_its_first_guard_fails():
    """Guaranteed side exit: P1 validated on a down service, replayed on an up one."""
    src = proposals()[0][1]
    world = FleetWorld("fixed")
    world.add_host("h1")
    world.add_service("a", "h1", 8000)
    world.crash("a")
    res, _ = effects(copy.deepcopy(world), src, "a")
    w = witness_from_steps("heal", ["service"], res.steps, src)
    healthy = FleetWorld("fixed")
    healthy.add_host("h1")
    healthy.add_service("a", "h1", 8000)
    r = replay(w, ["a"], healthy, GRANT)
    assert r.kind == "side_exit" and r.inapplicable and healthy.history == []


@pytest.mark.parametrize("label,src", proposals())
def test_replay_agrees_with_the_script_on_random_worlds(label: str, src: str):
    """Random worlds on top of the guaranteed cases: whatever the outcome, the kernel and the
    interpreter must agree up to the point where replay stops."""
    rng = random.Random(_seed(label) ^ 0xA5A5)
    outcomes = {"completed": 0, "side_exit": 0}
    for _ in range(120):
        base = random_world(rng)
        arg = rng.choice(list(base.services))
        res, _ = effects(copy.deepcopy(base), src, arg)
        if not res.ok:
            continue
        w = witness_from_steps("heal", ["service"], res.steps, src)
        fresh = random_world(rng)
        arg2 = rng.choice(list(fresh.services))
        a, b = copy.deepcopy(fresh), copy.deepcopy(fresh)
        r = replay(w, [arg2], a, GRANT)
        res2, _ = effects(b, src, arg2)
        outcomes[r.kind] = outcomes.get(r.kind, 0) + 1
        if r.kind == "completed":
            assert res2.ok, f"{label}: replay completed but the script failed: {res2.error}"
            assert a.history == b.history, f"{label}: effects diverged"
            assert a.snapshot() == b.snapshot(), f"{label}: final worlds diverged"
        else:
            assert a.history == b.history[: len(a.history)], f"{label}: diverged before the side exit"
    assert sum(outcomes.values()) > 0, f"{label}: no informative trial"
