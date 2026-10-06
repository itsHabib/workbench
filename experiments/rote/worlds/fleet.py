"""A small simulated fleet: hosts with disks, services with configs, ports and logs.

The world is the mechanism under test, not the point. It has three ways for a
service to be down -- a crash, a full disk, a port collision -- and one way for
its configuration schema to drift. `restart` succeeds only when the real cause
is gone, which is what makes "the goal decides" matter.

Scripts see the world only through `observe` / `act` by name. Incidents (the
methods after `# incidents`) are how the scenario changes the world between
episodes; they are not in the signature, so no script can call them.
"""

from __future__ import annotations

import copy
from typing import Any

__all__ = ["FleetWorld", "SIGNATURE"]

SIGNATURE: dict[str, tuple[str, int]] = {
    "status": ("observe", 1),
    "host_of": ("observe", 1),
    "disk": ("observe", 1),
    "config": ("observe", 1),
    "log_tail": ("observe", 1),
    "ports_in_use": ("observe", 1),
    "services_on": ("observe", 1),
    "restart": ("act", 1),
    "clear_tmp": ("act", 1),
    "set_config": ("act", 3),
    "reboot_host": ("act", 1),
    "wipe_host": ("act", 1),
}

NO_SPACE = "no space left on device"
SEGFAULT = "segfault (core dumped)"


def _bind_error(port: int) -> str:
    return f"bind: address already in use (:{port})"


class FleetWorld:
    def __init__(self, label: str = "fleet"):
        self.label = label
        self.hosts: dict[str, dict[str, Any]] = {}
        self.services: dict[str, dict[str, Any]] = {}
        self.history: list[tuple[str, list[Any]]] = []
        self.disruptions = 0  # healthy services taken down by an action

    # -- setup -----------------------------------------------------------------
    def add_host(self, name: str, disk: int = 40) -> None:
        self.hosts[name] = {"disk": disk}

    def add_service(self, name: str, host: str, port: int, nested: bool = False) -> None:
        cfg = {"listen": {"port": port}, "version": 2} if nested else {"port": port}
        self.services[name] = {"host": host, "status": "up", "config": cfg, "log": ["started"], "pid": 100}

    def snapshot(self) -> dict[str, Any]:
        return copy.deepcopy(
            {"hosts": self.hosts, "services": self.services, "disruptions": self.disruptions}
        )

    def restore(self, snap: dict[str, Any]) -> None:
        snap = copy.deepcopy(snap)
        self.hosts, self.services = snap["hosts"], snap["services"]
        self.disruptions = snap.get("disruptions", 0)

    # -- the interface scripts see ------------------------------------------
    def signature(self) -> dict[str, tuple[str, int]]:
        return SIGNATURE

    def observe(self, name: str, args: list[Any]) -> Any:
        fn = getattr(self, f"obs_{name}", None)
        if fn is None:
            raise KeyError(f"unknown observation {name}")
        return copy.deepcopy(fn(*args))

    def act(self, name: str, args: list[Any]) -> Any:
        fn = getattr(self, f"act_{name}", None)
        if fn is None:
            raise KeyError(f"unknown action {name}")
        self.history.append((name, list(args)))
        return copy.deepcopy(fn(*args))

    # observations
    def obs_status(self, s: str) -> str:
        return self._svc(s)["status"]

    def obs_host_of(self, s: str) -> str:
        return self._svc(s)["host"]

    def obs_disk(self, h: str) -> int:
        return self._host(h)["disk"]

    def obs_config(self, s: str) -> dict[str, Any]:
        return self._svc(s)["config"]

    def obs_log_tail(self, s: str) -> str:
        log = self._svc(s)["log"]
        return log[-1] if log else ""

    def obs_ports_in_use(self, h: str) -> list[int]:
        self._host(h)
        ports = {
            self._port(svc) for svc in self.services.values() if svc["host"] == h and svc["status"] == "up"
        }
        return sorted(p for p in ports if p is not None)

    def obs_services_on(self, h: str) -> list[str]:
        self._host(h)
        return sorted(n for n, svc in self.services.items() if svc["host"] == h)

    # actions
    def act_restart(self, s: str) -> bool:
        svc = self._svc(s)
        host = self._host(svc["host"])
        if svc["status"] == "up":
            return True
        if host["disk"] > 90:
            svc["log"].append(NO_SPACE)
            return False
        port = self._port(svc)
        if port is None:
            svc["log"].append("config error: no port configured")
            return False
        if port in self.obs_ports_in_use(svc["host"]):
            svc["log"].append(_bind_error(port))
            return False
        if port <= 0:
            svc["log"].append("config error: invalid port")
            return False
        svc["status"] = "up"
        svc["pid"] += 1
        svc["log"].append("started")
        return True

    def act_clear_tmp(self, h: str) -> int:
        host = self._host(h)
        freed = max(0, min(50, host["disk"] - 10))
        host["disk"] -= freed
        return freed

    def act_set_config(self, s: str, key: str, value: Any) -> bool:
        if not isinstance(key, str):
            raise TypeError("config key must be a string")
        self._svc(s)["config"][key] = copy.deepcopy(value)
        return True

    def act_reboot_host(self, h: str) -> int:
        """Restart every service on the host. Healthy ones go down and come back; that is a disruption."""
        self._host(h)
        rebooted = 0
        for name in self.obs_services_on(h):
            svc = self.services[name]
            if svc["status"] == "up":
                self.disruptions += 1
                svc["status"] = "down"
                svc["log"].append("rebooted")
            rebooted += 1
        for name in self.obs_services_on(h):
            self.act_restart(name)
        return rebooted

    def act_wipe_host(self, h: str) -> bool:
        self._host(h)["disk"] = 0
        for svc in self.services.values():
            if svc["host"] == h:
                svc["status"], svc["config"], svc["log"] = "down", {}, []
        return True

    # incidents (scenario-only; not in the signature)
    def crash(self, s: str) -> None:
        svc = self._svc(s)
        svc["status"] = "down"
        svc["log"].append(SEGFAULT)

    def disk_full(self, s: str, pct: int = 95) -> None:
        svc = self._svc(s)
        self._host(svc["host"])["disk"] = pct
        svc["status"] = "down"
        svc["log"].append(NO_SPACE)

    def port_conflict(self, s: str, other: str) -> None:
        svc, port = self._svc(s), self._port(self._svc(other))
        assert port is not None
        if "listen" in svc["config"]:
            svc["config"]["listen"]["port"] = port
        else:
            svc["config"]["port"] = port
        svc["status"] = "down"
        svc["log"].append(_bind_error(port))

    def drift_config(self, s: str) -> None:
        svc = self._svc(s)
        port = self._port(svc)
        svc["config"] = {"listen": {"port": port}, "version": 2}

    def lose_port(self, s: str) -> None:
        svc = self._svc(s)
        svc["config"] = {"version": 3}
        svc["status"] = "down"
        svc["log"].append("config error: no port configured")

    # helpers
    def _svc(self, s: str) -> dict[str, Any]:
        if not isinstance(s, str) or s not in self.services:
            raise KeyError(f"unknown service {s!r}")
        return self.services[s]

    def _host(self, h: str) -> dict[str, Any]:
        if not isinstance(h, str) or h not in self.hosts:
            raise KeyError(f"unknown host {h!r}")
        return self.hosts[h]

    @staticmethod
    def _port(svc: dict[str, Any]) -> int | None:
        cfg = svc["config"]
        if isinstance(cfg.get("port"), int):
            return cfg["port"]
        listen = cfg.get("listen")
        if isinstance(listen, dict) and isinstance(listen.get("port"), int):
            return listen["port"]
        return None
