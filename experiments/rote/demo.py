#!/usr/bin/env python3
"""The workload: nine episodes of healing a simulated fleet, with changes, failures and reuse.

Every proposal the "agent" makes is a SCRIPTED response from scenario/proposals.json.
No model is called. Run:  python3 demo.py            (add --json for machine-readable output)
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

from rote.library import Library  # noqa: E402
from rote.runtime import Outcome, Policy, Runtime  # noqa: E402
from rote.synth import ScriptedOracle  # noqa: E402
from rote.witness import describe_guards  # noqa: E402
from worlds.fleet import FleetWorld  # noqa: E402

GRANT = {"restart", "clear_tmp", "set_config", "reboot_host"}  # wipe_host exists in the world but is not granted


def build_world() -> FleetWorld:
    w = FleetWorld("fleet")
    w.add_host("h1", disk=40)
    w.add_host("h2", disk=40)
    w.add_service("web", "h1", 8080)
    w.add_service("api", "h1", 8081)
    w.add_service("db", "h2", 5432)
    w.add_service("cache", "h2", 6379)
    return w


EPISODES = [
    ("E1", "web crashes", lambda w: w.crash("web"), "heal", ["web"]),
    ("E2", "api crashes (same symptom, other service)", lambda w: w.crash("api"), "heal", ["api"]),
    ("E3", "db host runs out of disk", lambda w: w.disk_full("db"), "heal", ["db"]),
    ("E4", "api collides with web's port", lambda w: w.port_conflict("api", "web"), "heal", ["api"]),
    ("E5", "cache crashes", lambda w: w.crash("cache"), "heal", ["cache"]),
    ("E6", "db's config schema drifts, then it collides with cache's port",
     lambda w: (w.drift_config("db"), w.port_conflict("db", "cache")), "heal", ["db"]),
    ("E7", "", None, "recover_host", ["h3"]),
    ("E8", "new host h4: one drifted-schema port collision, one crash", None, "recover_host", ["h4"]),
    ("E9", "replay-only policy: db loses its port entirely (a symptom no witness covers)",
     lambda w: w.lose_port("db"), "heal", ["db"]),
]


def setup_h3(w: FleetWorld) -> None:
    w.add_host("h3", disk=40)
    w.add_service("s1", "h3", 9001)
    w.add_service("s2", "h3", 9002)
    w.add_service("s3", "h3", 9003)
    w.disk_full("s1")  # fills h3's disk and takes s1 down with the no-space log line
    w.crash("s3")


def setup_h4(w: FleetWorld) -> None:
    w.add_host("h4", disk=40)
    w.add_service("t1", "h4", 9101, nested=True)
    w.add_service("t2", "h4", 9102, nested=True)
    w.add_service("t3", "h4", 9110)
    w.port_conflict("t1", "t2")
    w.crash("t3")


EPISODES[6] = ("E7", "new host h3: full disk (s1), a crash (s3), one healthy service (s2)", setup_h3, "recover_host", ["h3"])
EPISODES[7] = ("E8", "new host h4: drifted-schema port collision (t1), a crash (t3)", setup_h4, "recover_host", ["h4"])


def tally(out: Outcome) -> dict[str, int]:
    kinds = [e.kind for e in out.events]
    wasted = sum(len(e.data.get("acts", [])) for e in out.events if e.kind in ("side_exit", "checker_fail", "error"))
    return {
        "oracle_calls": out.oracle_calls,
        "static_refusals": sum(1 for e in out.events if e.kind == "refused" and e.text.startswith("static check")),
        "inapplicable": kinds.count("inapplicable"),
        "replays_run": kinds.count("replayed") + kinds.count("checker_fail") + kinds.count("side_exit"),
        "acts": len(out.acts),
        "wasted_acts": wasted,
    }


def run(verbose: bool = True) -> dict:
    lib = Library.from_source((HERE / "scenario" / "fleet.rote").read_text())
    oracle = ScriptedOracle.from_file(str(HERE / "scenario" / "proposals.json"))
    world = build_world()
    assert lib.signature == world.signature(), "world and library signatures disagree"
    rt = Runtime(lib, Policy(grant=GRANT), oracle)
    rows = []
    for tag, story, incident, cap, args in EPISODES:
        world.label = f"fleet@{tag}"
        if incident is not None:
            incident(world)
        if tag == "E9":
            rt = Runtime(lib, Policy(grant=GRANT, mode="replay-only"), oracle)
        up_before = {n for n, svc in world.services.items() if svc["status"] == "up"}
        out = rt.achieve(cap, args, world)
        t = tally(out)
        t["collateral"] = sorted(n for n in up_before if world.services[n]["status"] == "down")
        t["disruptions"] = world.disruptions
        row = {"episode": tag, "story": story, "call": f"{cap}({', '.join(args)})", "outcome": out.kind,
               "witness": out.witness.hash if out.witness else None, "reason": out.reason, **t,
               "events": [f"{e.kind}: {e.text}" for e in out.events]}
        rows.append(row)
        if verbose:
            print_episode(row, out, world)
    report = {"episodes": rows, "oracle_calls_total": oracle.calls,
              "witnesses": [{"cap": w.cap, "hash": w.hash, "guards": describe_guards(w), "steps": w.pretty(),
                             "evidence": [e.to_json() for e in w.evidence]}
                            for c in lib.caps.values() for w in c.witnesses]}
    (HERE / "runs").mkdir(exist_ok=True)
    lib.save(HERE / "runs" / "library.json")
    (HERE / "runs" / "report.json").write_text(json.dumps(report, indent=1) + "\n")
    if verbose:
        print_summary(report, lib)
    return report


def print_episode(row: dict, out: Outcome, world: FleetWorld) -> None:
    print(f"\n{row['episode']}  {row['story']}")
    print(f"    {row['call']}  ->  {row['outcome']}" + (f"  via {row['witness']}" if row["witness"] else "") +
          (f"  ({row['reason']})" if row["reason"] else ""))
    for e in out.events:
        text = e.text if e.kind != "synth" else e.text
        print(f"      {e.kind:<13} {text}")
        if e.kind == "synth":
            for ln in e.data["source"].rstrip().splitlines():
                print(f"                    | {ln}")
    print(f"    oracle calls {row['oracle_calls']}, static refusals {row['static_refusals']}, "
          f"inapplicable-without-acting {row['inapplicable']}, acts {row['acts']} (wasted {row['wasted_acts']})")
    print(f"    healthy services disrupted so far: {row['disruptions']}; left down although healthy before: "
          f"{', '.join(row['collateral']) or 'none'}")


def print_summary(report: dict, lib: Library) -> None:
    rows = report["episodes"]
    print("\n" + "=" * 78)
    print(f"{'episode':<8}{'call':<20}{'outcome':<18}{'oracle':>7}{'refused':>8}{'inappl':>7}{'acts':>6}{'wasted':>7}{'disrupt':>8}")
    for r in rows:
        print(f"{r['episode']:<8}{r['call']:<20}{r['outcome']:<18}{r['oracle_calls']:>7}{r['static_refusals']:>8}"
              f"{r['inapplicable']:>7}{r['acts']:>6}{r['wasted_acts']:>7}{r['disruptions']:>8}")
    print(f"\noracle calls in total: {report['oracle_calls_total']} across {len(rows)} episodes")
    print("\nretained witnesses:")
    for c in lib.caps.values():
        for w in c.witnesses:
            p, f = w.counts()
            print(f"  {w.hash}  {c.name:<13} pass {p} fail {f}  guards: " + " ; ".join(describe_guards(w)))


if __name__ == "__main__":
    rep = run(verbose="--json" not in sys.argv)
    if "--json" in sys.argv:
        print(json.dumps(rep, indent=1))
