#!/usr/bin/env python3
"""Freeze trial ordering before paid runs; summarize all outcomes, including failures."""
import argparse
import json
from pathlib import Path
import random
import subprocess
import sys

import lab


def plan(directory, fleet, repeats, model, reference, seed, max_calls, timeout):
    if repeats < 1:
        raise ValueError("repeats must be positive")
    if directory.exists():
        raise ValueError("matrix already exists")
    directory.mkdir(parents=True)
    entries = []
    arms = [("solo", model), ("lead", model), ("pair", model)]
    if reference:
        arms.append(("reference-solo", reference))
    for repeat in range(repeats):
        order = list(arms)
        random.Random(seed + repeat).shuffle(order)
        for label, arm_model in order:
            name = f"{repeat + 1:02d}-{label}"
            run = directory / name
            config = {"fleet": fleet, "mode": "solo" if label == "reference-solo" else label,
                      "model": arm_model, "lead_model": arm_model, "critic_model": arm_model,
                      "max_calls": max_calls, "call_timeout": timeout,
                      "wall_seconds": timeout * max_calls}
            lab.initialize(run, config)
            entries.append({"name": name, "arm": label, "repeat": repeat + 1})
    lab.atomic(directory / "plan.json", {"version": 1, "seed": seed, "trials": entries,
               "qualification": "fixed-call pilot; not a matched-dollar or fresh-task evaluation"})
    return entries


def readouts(directory):
    plan_data = json.loads((directory / "plan.json").read_text())
    result = []
    for entry in plan_data["trials"]:
        item = dict(entry, **lab.summary(directory / entry["name"]))
        item["all_checks_pass"] = bool(item["checks"] and item["checks"]["passed"] and item["checked_source_matches"])
        # Mechanically correct code and the controller actually terminating are distinct outcomes.
        item["accepted"] = item["status"] == "complete" and item["all_checks_pass"]
        result.append(item)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("plan", "run", "report"))
    parser.add_argument("directory", type=Path)
    parser.add_argument("--fleet")
    parser.add_argument("--repeats", type=int, default=3)
    parser.add_argument("--model", default="gpt-5.6-luna")
    parser.add_argument("--reference", help="optional stronger solo model; omit for cheap-only pilot")
    parser.add_argument("--seed", type=int, default=29)
    parser.add_argument("--max-calls", type=int, default=8)
    parser.add_argument("--call-timeout", type=float, default=180)
    args = parser.parse_args()
    directory = args.directory.resolve()
    if args.command == "plan":
        if not args.fleet:
            parser.error("--fleet is required when planning")
        plan(directory, args.fleet, args.repeats, args.model, args.reference, args.seed,
             args.max_calls, args.call_timeout)
    if args.command == "run":
        data = json.loads((directory / "plan.json").read_text())
        for entry in data["trials"]:
            if (directory / "STOP").exists():
                break
            run = directory / entry["name"]
            print(json.dumps({"starting": entry["name"]}), flush=True)
            # Each trial has its own STOP control; an interrupted call halts the matrix.
            result = subprocess.run([sys.executable, str(lab.ROOT / "lab.py"), "run", str(run)])
            if result.returncode or lab.load(run)["status"] == "interrupted":
                break
    rows = readouts(directory)
    lab.atomic(directory / "report.json", {"trials": rows,
               "cost_usd": None, "invoice_cost_available": False})
    print(json.dumps(rows, indent=2))


if __name__ == "__main__":
    main()
