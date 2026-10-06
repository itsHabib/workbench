#!/usr/bin/env python3
"""The live experiment: random fleets, random incident streams, three arms.

  python3 live/run_live.py --oracle heuristic --streams 10 --episodes 24      # model-free dry run
  python3 live/run_live.py --oracle live --streams 10 --episodes 24 --max-calls 900  # needs ANTHROPIC_API_KEY

Each stream is a fresh random fleet and a fresh library per arm; every arm sees the same
(cause, service) incidents in the same order. Per episode we record the arm's outcome,
oracle calls, actions, healthy services disrupted, services left down although healthy
before, and whether the goal holds at the end. Results go to live/results/<name>/.
"""

from __future__ import annotations

import argparse
import copy
import json
import random
import sys
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

HERE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(HERE))

from live.arms import make_arm  # noqa: E402
from live.oracles import CallBudget, HeuristicOracle, LoggedLiveOracle  # noqa: E402
from live.world6 import apply_incident, incident_stream, random_fleet  # noqa: E402

PRICES = {"claude-opus-5-5": (4.0, 20.0), "claude-sonnet-5-5": (2.0, 10.0), "claude-haiku-4-5": (1.0, 5.0)}


def run_stream(
    stream_id: int, args: argparse.Namespace, budget: CallBudget | None, transcript: str | None
) -> list[dict]:
    rows: list[dict] = []
    for arm_name in args.arms.split(","):
        rng = random.Random(args.seed * 1000 + stream_id)
        fleet = random_fleet(rng, f"s{stream_id}")
        incidents = incident_stream(rng, fleet, args.episodes)
        world = copy.deepcopy(fleet)
        oracle = (
            HeuristicOracle()
            if args.oracle == "heuristic"
            else LoggedLiveOracle(args.model, args.effort, budget=budget, transcript_path=transcript)
        )
        arm = make_arm(arm_name, oracle)
        irng = random.Random(args.seed * 7919 + stream_id)
        for i, (cause, s) in enumerate(incidents):
            if world.services[s]["status"] != "up":
                s = next((n for n in sorted(world.services) if world.services[n]["status"] == "up"), s)
            info = apply_incident(world, cause, s, irng)
            up_before = {n for n, svc in world.services.items() if svc["status"] == "up"}
            d0, h0, t0 = world.disruptions, len(world.history), time.time()
            res = arm.achieve(world, s)
            rows.append(
                {
                    "stream": stream_id,
                    "arm": arm_name,
                    "episode": i,
                    "cause": info["cause"],
                    "service": s,
                    "outcome": res.outcome,
                    "success": world.obs_status(s) == "up",
                    "oracle_calls": res.oracle_calls,
                    "acts": len(world.history) - h0,
                    "disruptions": world.disruptions - d0,
                    "collateral": sorted(n for n in up_before if world.services[n]["status"] == "down"),
                    "retained": res.retained,
                    "seconds": round(time.time() - t0, 2),
                    "notes": res.notes,
                }
            )
        rows.append(
            {
                "stream": stream_id,
                "arm": arm_name,
                "episode": -1,
                "oracle_total": getattr(oracle, "calls", 0),
                "tokens_in": getattr(oracle, "tokens_in", 0),
                "tokens_out": getattr(oracle, "tokens_out", 0),
            }
        )
    return rows


def summarize(rows: list[dict], args: argparse.Namespace) -> str:
    arms = args.arms.split(",")
    eps = [r for r in rows if r["episode"] >= 0]
    totals = [r for r in rows if r["episode"] < 0]
    lines = [
        f"# Live experiment summary ({args.oracle} oracle, {args.streams} streams x {args.episodes} "
        f"episodes, seed {args.seed})",
        "",
    ]
    if args.oracle == "heuristic":
        lines.append(
            "**The oracle is a deterministic stand-in, not a model.** Differences between arms are due to "
            "the "
            "dispatch mechanism only; every arm received the same proposals for the same diagnosis."
        )
    lines += [
        "",
        "| arm | oracle calls | calls/episode | success | acts | disruptions | collateral outages "
        "| parked/failed |",
        "|---|---|---|---|---|---|---|---|",
    ]
    for a in arms:
        rs = [r for r in eps if r["arm"] == a]
        n = len(rs) or 1
        calls = sum(r["oracle_calls"] for r in rs)
        lines.append(
            f"| {a} | {calls} | {calls / n:.2f} | {sum(r['success'] for r in rs) / n:.0%} | "
            f"{sum(r['acts'] for r in rs)} | {sum(r['disruptions'] for r in rs)} | "
            f"{sum(len(r['collateral']) for r in rs)} | "
            f"{sum(r['outcome'] in ('failed', 'parked') for r in rs)} |"
        )
    lines += [
        "",
        "Oracle calls per episode index, mean over streams (the learning curve):",
        "",
        "| episode | " + " | ".join(arms) + " |",
        "|---|" + "---|" * len(arms),
    ]
    for i in range(args.episodes):
        cells = []
        for a in arms:
            rs = [r for r in eps if r["arm"] == a and r["episode"] == i]
            cells.append(f"{sum(r['oracle_calls'] for r in rs) / max(1, len(rs)):.2f}")
        lines.append(f"| {i} | " + " | ".join(cells) + " |")
    lines += [
        "",
        "By cause (success rate / oracle calls per episode):",
        "",
        "| cause | " + " | ".join(arms) + " |",
        "|---|" + "---|" * len(arms),
    ]
    for c in sorted({r["cause"] for r in eps}):
        cells = []
        for a in arms:
            rs = [r for r in eps if r["arm"] == a and r["cause"] == c]
            n = len(rs) or 1
            cells.append(
                f"{sum(r['success'] for r in rs) / n:.0%} / {sum(r['oracle_calls'] for r in rs) / n:.2f}"
            )
        lines.append(f"| {c} | " + " | ".join(cells) + " |")
    if args.oracle == "live":
        tin = sum(t["tokens_in"] for t in totals)
        tout = sum(t["tokens_out"] for t in totals)
        pin, pout = PRICES.get(args.model, (0, 0))
        lines += [
            "",
            f"Tokens: {tin} in, {tout} out; estimated cost at list prices: "
            f"${tin / 1e6 * pin + tout / 1e6 * pout:.2f} ({args.model}).",
        ]
    return "\n".join(lines) + "\n"


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--oracle", choices=["heuristic", "live"], default="heuristic")
    ap.add_argument("--arms", default="rote,rote_reval,precond,model_each")
    ap.add_argument("--streams", type=int, default=10)
    ap.add_argument("--episodes", type=int, default=24)
    ap.add_argument("--seed", type=int, default=1)
    ap.add_argument("--workers", type=int, default=4)
    ap.add_argument("--model", default="claude-opus-5-5")
    ap.add_argument("--effort", default="medium")
    ap.add_argument("--max-calls", type=int, default=900)
    ap.add_argument("--name", default=None)
    args = ap.parse_args()
    name = args.name or f"{args.oracle}-{args.streams}x{args.episodes}-seed{args.seed}"
    out = HERE / "live" / "results" / name
    out.mkdir(parents=True, exist_ok=True)
    transcript = str(out / "transcripts.jsonl") if args.oracle == "live" else None
    if transcript and Path(transcript).exists():
        Path(transcript).unlink()
    budget = CallBudget(args.max_calls) if args.oracle == "live" else None
    t0 = time.time()
    with ThreadPoolExecutor(max_workers=args.workers) as pool:
        batches = list(pool.map(lambda i: run_stream(i, args, budget, transcript), range(args.streams)))
    rows = [r for b in batches for r in b]
    (out / "results.json").write_text(json.dumps({"args": vars(args), "rows": rows}, indent=1) + "\n")
    summary = summarize(rows, args)
    (out / "summary.md").write_text(summary)
    print(summary)
    print(f"wrote {out} in {time.time() - t0:.0f}s")


if __name__ == "__main__":
    main()
