"""A wider fleet for the live experiment: six causes of outage instead of three.

  crash     restart fixes it
  disk      host disk full: clear_tmp, then restart
  port      collides with a running neighbor (flat or nested config): set_config, restart
  lostport  config has no port at all: set_config a free port, restart
  lock      stale lock file: remove_lock, then restart
  dep       a dependency is down (itself crashed or locked): heal the dependency, then restart

Plus random fleets and random incident streams, so every stream is a different world
and no script written for one fleet trivially fits another.
"""

from __future__ import annotations

import random
from typing import Any

from worlds.fleet import NO_SPACE, SIGNATURE, FleetWorld

__all__ = ["FleetWorld6", "SIGNATURE6", "CAUSES", "random_fleet", "incident_stream", "LIBRARY6", "GRANT6"]

SIGNATURE6: dict[str, tuple[str, int]] = {
    **SIGNATURE,
    "lock_held": ("observe", 1),
    "depends_on": ("observe", 1),
    "remove_lock": ("act", 1),
}
CAUSES = ["crash", "disk", "port", "lostport", "lock", "dep"]
GRANT6 = {"restart", "clear_tmp", "set_config", "reboot_host", "remove_lock"}

LIBRARY6 = """
world fleet6 {
  observe status(service): string
  observe host_of(service): string
  observe disk(host): int
  observe config(service): record
  observe log_tail(service): string
  observe ports_in_use(host): list
  observe services_on(host): list
  observe lock_held(service): bool
  observe depends_on(service): list
  act restart(service): bool
  act clear_tmp(host): int
  act set_config(service, key, value): bool
  act reboot_host(host): int
  act wipe_host(host): bool
  act remove_lock(service): bool
}

cap heal(service) {
  "Bring one service back to up."
  goal: status(service) == "up"
}
"""


class FleetWorld6(FleetWorld):
    def signature(self) -> dict[str, tuple[str, int]]:
        return SIGNATURE6

    def add_service(
        self, name: str, host: str, port: int, nested: bool = False, deps: list[str] | None = None
    ) -> None:
        super().add_service(name, host, port, nested)
        self.services[name]["lock"] = False
        self.services[name]["deps"] = list(deps or [])

    # observations
    def obs_lock_held(self, s: str) -> bool:
        return bool(self._svc(s)["lock"])

    def obs_depends_on(self, s: str) -> list[str]:
        return list(self._svc(s)["deps"])

    # actions
    def act_remove_lock(self, s: str) -> bool:
        self._svc(s)["lock"] = False
        return True

    def act_restart(self, s: str) -> bool:
        svc = self._svc(s)
        if svc["status"] == "up":
            return True
        if self._host(svc["host"])["disk"] > 90:
            svc["log"].append(NO_SPACE)
            return False
        if svc["lock"]:
            svc["log"].append("lock file exists: /var/run/" + s + ".lock")
            return False
        down_deps = [d for d in svc["deps"] if self.services[d]["status"] != "up"]
        if down_deps:
            svc["log"].append(f"dependency {down_deps[0]} unavailable")
            return False
        return super().act_restart(s)

    # incidents
    def stale_lock(self, s: str) -> None:
        svc = self._svc(s)
        svc["lock"] = True
        svc["status"] = "down"
        svc["log"].append("lock file exists: /var/run/" + s + ".lock")

    def dependency_down(self, s: str, rng: random.Random) -> str | None:
        svc = self._svc(s)
        if not svc["deps"]:
            return None
        dep = rng.choice(svc["deps"])
        if rng.random() < 0.5:
            self.crash(dep)
        else:
            self.stale_lock(dep)
        svc["status"] = "down"
        svc["log"].append(f"dependency {dep} unavailable")
        return dep


def random_fleet(rng: random.Random, label: str) -> FleetWorld6:
    w = FleetWorld6(label)
    hosts = [f"h{i}" for i in range(rng.choice([2, 3]))]
    for h in hosts:
        w.add_host(h, disk=rng.choice([30, 40, 50, 60]))
    n = rng.choice([5, 6, 7])
    names = [f"svc{i}" for i in range(n)]
    ports = rng.sample(range(8000, 8000 + 4 * n), n)
    for i, name in enumerate(names):
        deps = [d for d in names[:i] if rng.random() < 0.25][:2]
        w.add_service(name, rng.choice(hosts), ports[i], nested=rng.random() < 0.3, deps=deps)
    return w


def apply_incident(w: FleetWorld6, cause: str, s: str, rng: random.Random) -> dict[str, Any]:
    """Inject one cause on one service. Returns a description for the record."""
    if cause == "crash":
        w.crash(s)
    elif cause == "disk":
        w.disk_full(s)
    elif cause == "port":
        host = w.obs_host_of(s)
        neighbors = [n for n in w.obs_services_on(host) if n != s and w.services[n]["status"] == "up"]
        if not neighbors:
            w.crash(s)
            return {"cause": "crash", "service": s, "note": "no running neighbor; crashed instead"}
        w.port_conflict(s, rng.choice(neighbors))
    elif cause == "lostport":
        w.lose_port(s)
    elif cause == "lock":
        w.stale_lock(s)
    elif cause == "dep":
        dep = w.dependency_down(s, rng)
        if dep is None:
            w.crash(s)
            return {"cause": "crash", "service": s, "note": "no dependency; crashed instead"}
        return {"cause": "dep", "service": s, "dependency": dep}
    else:
        raise ValueError(cause)
    return {"cause": cause, "service": s}


def incident_stream(rng: random.Random, w: FleetWorld6, episodes: int) -> list[tuple[str, str]]:
    """(cause, service) pairs; the service is chosen among those currently up when applied."""
    out = []
    names = sorted(w.services)
    for _ in range(episodes):
        out.append((rng.choice(CAUSES), rng.choice(names)))
    return out
