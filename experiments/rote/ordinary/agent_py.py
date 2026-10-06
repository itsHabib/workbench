#!/usr/bin/env python3
"""The competent ordinary-language baseline: the same nine episodes, in plain Python.

Skills are Python functions the (scripted) agent proposes, each with the precondition
the agent wrote for it. A skill is retained when the goal holds after it ran. Dispatch
tries retained skills whose precondition holds, newest-first among those with no
recorded failures -- the same policy Rote's runtime uses. The world adapter enforces
the grant dynamically (it raises on an ungranted action). Nothing else is checked,
because nothing else *can* be checked in a general-purpose language without analysis.

Run:  python3 ordinary/agent_py.py
"""

from __future__ import annotations

import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable

HERE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(HERE))

from worlds.fleet import FleetWorld  # noqa: E402

GRANT = {"restart", "clear_tmp", "set_config", "reboot_host"}


class Guarded:
    """The world as the skill functions see it: observations and granted actions by name."""

    def __init__(self, world: FleetWorld, grant: set[str]):
        self._w, self._grant = world, grant

    def __getattr__(self, name: str) -> Callable[..., Any]:
        kind, _ = self._w.signature().get(name, (None, 0))
        if kind == "observe":
            return lambda *a: self._w.observe(name, list(a))
        if kind == "act":
            if name not in self._grant:
                def denied(*a: Any) -> Any:
                    raise PermissionError(f"action {name!r} is not in the caller's grant")
                return denied
            return lambda *a: self._w.act(name, list(a))
        raise AttributeError(name)


class LLM:
    """What an LLM call inside a retained skill costs: one call per execution, forever."""

    calls = 0

    @classmethod
    def ask(cls, question: str, world: Guarded, host: str) -> int:
        cls.calls += 1
        return max(world.ports_in_use(host), default=8000) + 1  # a stub answer, deterministic


@dataclass
class Skill:
    label: str
    applies: Callable[[Guarded, str], bool]
    run: Callable[[Guarded, str], None]
    seq: int
    passes: int = 0
    fails: int = 0


@dataclass
class Episode:
    tag: str
    call: str
    outcome: str
    oracle_calls: int = 0
    runtime_llm_calls: int = 0
    acts: int = 0
    wasted_acts: int = 0
    exceptions: list[str] = field(default_factory=list)
    disruptions: int = 0
    collateral: list[str] = field(default_factory=list)  # services up before the episode, down after it
    notes: list[str] = field(default_factory=list)


# ---- SCRIPTED MODEL RESPONSES: the same proposals as scenario/proposals.json, in Python ----

def p1_applies(w: Guarded, s: str) -> bool:
    return w.status(s) == "down"


def p1_run(w: Guarded, s: str) -> None:
    if w.status(s) == "down":
        w.restart(s)


def p2_applies(w: Guarded, s: str) -> bool:
    return w.status(s) == "down"


def p2_run(w: Guarded, s: str) -> None:
    host = w.host_of(s)
    if w.log_tail(s) == "no space left on device":
        w.clear_tmp(host)
    else:
        w.reboot_host(host)
    w.restart(s)


def p3a_applies(w: Guarded, s: str) -> bool:
    return w.status(s) == "down" and "address already in use" in w.log_tail(s)


def p3a_run(w: Guarded, s: str) -> None:
    host = w.host_of(s)
    port = LLM.ask("pick a free port on " + host, w, host)
    w.set_config(s, "port", port)
    w.restart(s)


def p3b_applies(w: Guarded, s: str) -> bool:
    return w.status(s) == "down"


def p3b_run(w: Guarded, s: str) -> None:
    w.restart(s)
    w.wipe_host(w.host_of(s))


def p3c_applies(w: Guarded, s: str) -> bool:
    return w.status(s) == "down" and "address already in use" in w.log_tail(s)


def p3c_run(w: Guarded, s: str) -> None:
    host = w.host_of(s)
    cfg = w.config(s)
    if cfg["port"] in w.ports_in_use(host):
        w.set_config(s, "port", cfg["port"] + 1)
    w.restart(s)


def p4_applies(w: Guarded, s: str) -> bool:
    return w.status(s) == "down" and "address already in use" in w.log_tail(s)


def p4_run(w: Guarded, s: str) -> None:
    host = w.host_of(s)
    cfg = w.config(s)
    port = cfg["listen"]["port"]
    if port in w.ports_in_use(host):
        w.set_config(s, "listen", {"port": port + 1})
    w.restart(s)


def r1_applies(w: Guarded, h: str) -> bool:
    return True


CASSETTE: dict[str, list[tuple[str, Callable, Callable]]] = {
    "heal": [
        ("P1: the obvious first attempt", p1_applies, p1_run),
        ("P2: reads the log; the else-branch is a plausible guess with a blast radius", p2_applies, p2_run),
        ("P3a (planted): asks the LLM at run time for a free port", p3a_applies, p3a_run),
        ("P3b (planted): restarts, then reaches for an action outside the grant", p3b_applies, p3b_run),
        ("P3c: moves the service off a colliding port", p3c_applies, p3c_run),
        ("P4: the same fix for the drifted config schema", p4_applies, p4_run),
    ],
    "recover_host": [],
}

GOALS = {
    "heal": lambda w, s: w.status(s) == "up",
    "recover_host": lambda w, h: all(w.status(s) == "up" for s in w.services_on(h)),
}


class Agent:
    def __init__(self, world: FleetWorld, synthesize: bool = True):
        self.world = world
        self.g = Guarded(world, GRANT)
        self.skills: dict[str, list[Skill]] = {"heal": [], "recover_host": []}
        self.cursor: dict[str, int] = {}
        self.oracle_calls = 0
        self.synthesize = synthesize
        self.seq = 0

    def achieve(self, cap: str, arg: str, ep: Episode) -> bool:
        goal = GOALS[cap]
        if goal(self.g, arg):
            ep.outcome = "already_satisfied"
            return True
        order = sorted(self.skills[cap], key=lambda s: (s.fails, -s.seq))
        for sk in order:
            if not self._applies(sk, arg, ep):
                continue
            if self._run(sk, cap, arg, ep):
                ep.outcome = "replayed"
                return True
        if not self.synthesize:
            ep.outcome = "parked"
            return False
        return self._synthesize(cap, arg, ep)

    def _applies(self, sk: Skill, arg: str, ep: Episode) -> bool:
        try:
            return sk.applies(self.g, arg)
        except Exception as err:  # noqa: BLE001
            ep.exceptions.append(f"{sk.label}: precondition raised {err!r}")
            return False

    def _run(self, sk: Skill, cap: str, arg: str, ep: Episode) -> bool:
        before = len(self.world.history)
        llm_before = LLM.calls
        try:
            sk.run(self.g, arg)
        except Exception as err:  # noqa: BLE001
            ep.exceptions.append(f"{sk.label}: raised {err!r} after {len(self.world.history) - before} action(s)")
            sk.fails += 1
            ep.wasted_acts += len(self.world.history) - before
            return False
        finally:
            ep.acts += len(self.world.history) - before
            ep.runtime_llm_calls += LLM.calls - llm_before
        if GOALS[cap](self.g, arg):
            sk.passes += 1
            return True
        sk.fails += 1
        ep.wasted_acts += len(self.world.history) - before
        return False

    def _synthesize(self, cap: str, arg: str, ep: Episode) -> bool:
        for _ in range(3):
            i = self.cursor.get(cap, 0)
            if i >= len(CASSETTE[cap]):
                ep.outcome = "failed"
                return False
            self.cursor[cap] = i + 1
            self.oracle_calls += 1
            ep.oracle_calls += 1
            label, applies, run = CASSETTE[cap][i]
            self.seq += 1
            sk = Skill(label, applies, run, self.seq)
            if self._run(sk, cap, arg, ep):
                self.skills[cap].append(sk)
                ep.outcome = "synthesized"
                ep.notes.append(f"retained {label}")
                return True
        ep.outcome = "failed"
        return False


def make_recover_host(agent: Agent) -> None:
    def run(w: Guarded, h: str) -> None:
        for s in w.services_on(h):
            sub = Episode("", f"heal({s})", "")
            agent.achieve("heal", s, sub)
    CASSETTE["recover_host"].append(("R1: compose heal over every service on the host", r1_applies, run))


def build_world() -> FleetWorld:
    w = FleetWorld("py")
    w.add_host("h1", disk=40)
    w.add_host("h2", disk=40)
    for name, host, port in (("web", "h1", 8080), ("api", "h1", 8081), ("db", "h2", 5432), ("cache", "h2", 6379)):
        w.add_service(name, host, port)
    return w


def setup_h3(w: FleetWorld) -> None:
    w.add_host("h3", disk=40)
    for n, p in (("s1", 9001), ("s2", 9002), ("s3", 9003)):
        w.add_service(n, "h3", p)
    w.disk_full("s1")
    w.crash("s3")


def setup_h4(w: FleetWorld) -> None:
    w.add_host("h4", disk=40)
    w.add_service("t1", "h4", 9101, nested=True)
    w.add_service("t2", "h4", 9102, nested=True)
    w.add_service("t3", "h4", 9110)
    w.port_conflict("t1", "t2")
    w.crash("t3")


EPISODES = [
    ("E1", lambda w: w.crash("web"), "heal", "web"),
    ("E2", lambda w: w.crash("api"), "heal", "api"),
    ("E3", lambda w: w.disk_full("db"), "heal", "db"),
    ("E4", lambda w: w.port_conflict("api", "web"), "heal", "api"),
    ("E5", lambda w: w.crash("cache"), "heal", "cache"),
    ("E6", lambda w: (w.drift_config("db"), w.port_conflict("db", "cache")), "heal", "db"),
    ("E7", setup_h3, "recover_host", "h3"),
    ("E8", setup_h4, "recover_host", "h4"),
    ("E9", lambda w: w.lose_port("db"), "heal", "db"),
]


def run(verbose: bool = True) -> list[Episode]:
    LLM.calls = 0
    world = build_world()
    agent = Agent(world)
    make_recover_host(agent)
    rows: list[Episode] = []
    for tag, incident, cap, arg in EPISODES:
        incident(world)
        if tag == "E9":
            agent.synthesize = False
        before_disruptions = world.disruptions
        up_before = {n for n, svc in world.services.items() if svc["status"] == "up"}
        ep = Episode(tag, f"{cap}({arg})", "")
        agent.achieve(cap, arg, ep)
        ep.disruptions = world.disruptions
        ep.collateral = sorted(n for n in up_before if world.services[n]["status"] == "down")
        if ep.collateral:
            ep.notes.append(f"left down although healthy before: {', '.join(ep.collateral)}")
        if world.disruptions > before_disruptions:
            ep.notes.append(f"disrupted {world.disruptions - before_disruptions} healthy service(s)")
        rows.append(ep)
        if verbose:
            print(f"{tag}  {ep.call:<18} -> {ep.outcome:<17} oracle {ep.oracle_calls}  runtime-llm {ep.runtime_llm_calls}"
                  f"  acts {ep.acts} (wasted {ep.wasted_acts})  disruptions so far {ep.disruptions}")
            for x in ep.exceptions:
                print(f"      exception: {x}")
            for n in ep.notes:
                print(f"      note: {n}")
    if verbose:
        print(f"\noracle calls at synthesis: {agent.oracle_calls}; LLM calls inside retained skills at run time: {LLM.calls}")
        print("retained skills:", [s.label.split(":")[0] for s in agent.skills["heal"]] +
              [s.label.split(":")[0] for s in agent.skills["recover_host"]])
    return rows


if __name__ == "__main__":
    run()
