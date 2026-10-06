"""The live-experiment harness runs end to end with the model-free oracle."""

import argparse

from live.oracles import HeuristicOracle, diagnose
from live.run_live import run_stream, summarize


def test_dry_run_small_all_arms():
    args = argparse.Namespace(oracle="heuristic", arms="rote,rote_reval,precond,model_each", streams=1,
                              episodes=6, seed=3, model="x", effort="medium")
    rows = run_stream(0, args, None, None)
    eps = [r for r in rows if r["episode"] >= 0]
    assert len(eps) == 24 and all(r["success"] for r in eps)
    assert all(r["disruptions"] == 0 for r in eps)
    me = [r for r in eps if r["arm"] == "model_each" and r["outcome"] != "already_satisfied"]
    assert all(r["oracle_calls"] >= 1 for r in me), "model_each asks the model every episode by construction"
    text = summarize(rows, args)
    assert "not a model" in text and "| rote |" in text


def test_heuristic_diagnosis_is_a_stand_in_not_a_model():
    assert diagnose({"disk": 95}) == "disk"
    assert diagnose({"disk": 40, "lock_held": True}) == "lock"
    assert diagnose({"disk": 40, "config": {"version": 3}}) == "lostport"
    assert diagnose({"disk": 40, "config": {"listen": {"port": 1}}, "ports_in_use": [1]}) == "port_nested"
    assert HeuristicOracle().ask({"situation": {"disk": 40, "config": {"port": 5}}, "attempts": []}).startswith("script heal")
