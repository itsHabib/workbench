#!/usr/bin/env python3
"""Local intervention experiments. Fleet owns claims; this driver owns the trial."""
from __future__ import annotations

import argparse
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parent
EDITABLE = ("service.py", "README.md")
SCHEMA = {
    "type": "object", "additionalProperties": False,
    "properties": {
        "action": {"type": "string", "enum": ["dispatch", "edit", "challenge", "assess", "ask", "finish"]},
        "worker": {"type": "string", "enum": ["", "worker-1", "worker-2"]},
        "message": {"type": "string"}, "memory": {"type": "string"},
        "files": {"type": "array", "items": {"type": "object", "additionalProperties": False,
            "properties": {"path": {"type": "string", "enum": list(EDITABLE)},
                           "content": {"type": "string"}}, "required": ["path", "content"]}},
    }, "required": ["action", "worker", "message", "memory", "files"],
}


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True).encode()).hexdigest()


def atomic(path, value):
    path = Path(path)
    temporary = path.with_suffix(path.suffix + ".tmp")
    with temporary.open("w") as stream:
        json.dump(value, stream, indent=2)
        stream.write("\n")
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)
    directory = os.open(path.parent, os.O_RDONLY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)


@contextmanager
def locked(run):
    with (run / "lock").open("a") as stream:
        try:
            fcntl.flock(stream, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise RuntimeError("another driver owns this trial") from None
        yield


def load(run):
    return json.loads((run / "mission.json").read_text())


def save(run, state):
    atomic(run / "mission.json", state)


def source_files(workspace):
    result = {}
    for name in EDITABLE:
        path = workspace / name
        if path.is_symlink():
            raise ValueError("candidate symlinks are not supported")
        if path.exists():
            result[name] = path.read_text()
    return result


def fleet(run, config, op, **fields):
    command = [config["fleet"], "job", op, "--state", str(run / "jobs")]
    for key, value in fields.items():
        command.extend(["--" + key.replace("_", "-"), str(value)])
    result = subprocess.run(command, capture_output=True, text=True, timeout=20)
    if result.returncode:
        raise RuntimeError("Fleet " + op + ": " + result.stderr.strip())
    return json.loads(result.stdout)


def initialize(run, config, source=None, context=None):
    if run.exists():
        raise ValueError("trial directory already exists; resume it or choose a new one")
    if config["mode"] not in ("solo", "lead", "pair"):
        raise ValueError("unknown mode")
    if config["max_calls"] < 1 or not 1 <= config["call_timeout"] <= 900 or config["wall_seconds"] < 1:
        raise ValueError("positive call/time limits required; per-call maximum is 900 seconds")
    files = source_files(Path(source) if source else ROOT / "workload")
    if "service.py" not in files:
        raise ValueError("source must contain service.py")
    history = json.loads(Path(context).read_text()) if context else []
    if not isinstance(history, list) or any(not isinstance(row, dict) for row in history):
        raise ValueError("team context must be a JSON array of handoff/message objects")
    config = dict(config, fleet=str(Path(config["fleet"]).resolve()))
    # Read-only feature probe, before creating a trial or calling any model.
    probe = subprocess.run([config["fleet"], "job", "metrics", "--state", str(run / "jobs")],
                           capture_output=True, text=True, timeout=20)
    if probe.returncode:
        raise ValueError("Fleet binary lacks the job API: " + probe.stderr.strip())
    run.mkdir(parents=True)
    os.chmod(run, 0o700)
    workspace = run / "versions" / "0000"
    workspace.mkdir(parents=True)
    for name, content in files.items():
        (workspace / name).write_text(content)
    atomic(run / "config.json", config)
    implementation = {str(p.relative_to(ROOT)): hashlib.sha256(p.read_bytes()).hexdigest()
                      for p in sorted(ROOT.rglob("*.py")) if ".runs" not in p.parts}
    manifest = {"version": 1, "config_sha256": digest(config), "source_sha256": digest(files),
                "spec": (ROOT / "workload" / "TASK.md").read_text(),
                "protocol_sha256": hashlib.sha256((ROOT / "PROTOCOL.md").read_bytes()).hexdigest(),
                "implementation": implementation,
                "fleet_sha256": hashlib.sha256(Path(config["fleet"]).read_bytes()).hexdigest(),
                "evidence_kind": "live"}
    manifest["initial_context_sha256"] = digest(history)
    atomic(run / "manifest.json", manifest)
    state = {"status": "ready", "phase": 1, "version": "0000", "calls": [], "pending": None,
             "next": "worker-1" if config["mode"] == "solo" else "lead",
             "task": "Deliver the requested service.", "history": history, "memories": {},
             "checks": None, "human_interventions": 0, "active_seconds": 0.0,
             "tokens_known": 0, "unknown_usage_calls": 0, "changes": []}
    save(run, state)
    return state


def verify(run, state):
    from verifier import check
    workspace = run / "versions" / state["version"]
    before = digest(source_files(workspace))
    result = check(workspace, phase=state["phase"])
    if digest(source_files(workspace)) != before:
        result["passed"] = False
        result["checks"].append({"name": "source_integrity", "passed": False,
                                 "detail": "candidate source changed during verification"})
    # The checker observes the integrated immutable version, never a worker's claim.
    state["checks"] = dict(result, version=state["version"], phase=state["phase"],
                           source_sha256=before)
    return state["checks"]


def prompt_for(run, state, config):
    manifest = json.loads((run / "manifest.json").read_text())
    role = state["next"]
    instructions = {
        "lead": "You are the delivery lead. Diagnose failures, clarify assignments, reuse either worker, "
                "and integrate results. Dispatch a concrete task, request a challenger (pair mode only), "
                "ask a genuinely necessary human question, or finish. Do not edit files yourself. "
                "Distinguish a concrete violation of the requested contract from optional hardening. "
                "A critic's suggestion is a hypothesis, not a new requirement. Finish when the checks "
                "pass and no concrete required repair remains; do not commission another review merely "
                "to reconfirm passing evidence. "
                "Procedural advice is not an extra approval requirement. Existing authorization covers "
                "local implementation and tests; it does not authorize public deployment.",
        "challenger": "Independently challenge the current implementation and direction. Find concrete "
                      "counterexamples and omissions, avoid cosmetic churn. Return assess with actionable "
                      "findings and evidence. You cannot edit or authorize completion.",
    }
    duty = instructions.get(role, "You own implementation and repair. If the current phase checks pass "
                            "and no concrete contract violation remains, return finish with files=[]. "
                            "Return edit only when changing code, with complete contents of changed files. "
                            "Never re-emit unchanged code. Use the actual specification and observed failures. "
                            "Do not invent approval requirements. Ask only for a decision genuinely "
                            "missing from the task. You may finish when the goal is met.")
    specification = manifest["spec"]
    if state["phase"] == 1:
        specification = specification.split("## Phase 2")[0]
    context = {"role": role, "mode": config["mode"], "phase": state["phase"],
               "assignment": state["task"], "remaining_calls": config["max_calls"] - len(state["calls"]),
               "spec": specification, "changes": state["changes"], "checks": state["checks"],
               "memory": state["memories"].get(role, ""), "team_history": state["history"][-12:],
               "files": source_files(run / "versions" / state["version"])}
    if role == "challenger":
        context["memory"] = ""
        context["team_history"] = []
    return (duty + "\nReturn one JSON action matching the schema. Work only from the supplied context; "
            "do not use shell, filesystem, network or other tools. The experiment applies edits and "
            "runs the checker. Unused worker is an empty string; unused files is []. Memory is a short "
            "continuity note for your next turn. Finish is independently checked.\n" + json.dumps(context))


def validate_action(response, role, mode):
    if not isinstance(response, dict) or set(response) != set(SCHEMA["required"]):
        raise ValueError("response must contain exactly the six action fields")
    if any(not isinstance(response[x], str) for x in ("action", "worker", "message", "memory")):
        raise ValueError("action text fields must be strings")
    if len(response["message"]) > 12000 or len(response["memory"]) > 6000:
        raise ValueError("action note too large")
    allowed = {"lead": {"dispatch", "challenge", "ask", "finish", "assess"},
               "challenger": {"assess"}}.get(role, {"edit", "ask", "finish", "assess"})
    action = response["action"]
    if action not in allowed or (action == "challenge" and mode != "pair"):
        raise ValueError("action is not available to this role")
    if response["worker"] not in ("", "worker-1", "worker-2"):
        raise ValueError("unknown worker")
    if action == "dispatch" and response["worker"] == "":
        raise ValueError("dispatch requires a worker")
    files = response["files"]
    if not isinstance(files, list) or (action != "edit" and files) or (action == "edit" and not files):
        raise ValueError("only edit may provide nonempty files")
    seen = set()
    for item in files:
        if not isinstance(item, dict) or set(item) != {"path", "content"}:
            raise ValueError("invalid file edit")
        if item["path"] not in EDITABLE or item["path"] in seen or not isinstance(item["content"], str):
            raise ValueError("invalid or duplicate candidate path")
        if len(item["content"].encode()) > 100000:
            raise ValueError("candidate file exceeds 100 KB")
        seen.add(item["path"])


def apply_action(run, state, config, response, check_fn=verify):
    role = state["pending"]["role"]
    validate_action(response, role, config["mode"])
    state["memories"][role] = response["memory"]
    state["history"].append({"role": role, "action": response["action"], "message": response["message"]})
    state["next"] = "worker-1" if config["mode"] == "solo" else "lead"
    state["status"] = "ready"
    action = response["action"]
    if action == "dispatch":
        state["task"] = response["message"]
        state["next"] = response["worker"]
    if action == "challenge":
        state["task"] = response["message"]
        state["next"] = "challenger"
    if action == "ask":
        if role != "lead" and config["mode"] != "solo":
            state["task"] = "Resolve the worker's blocker or ask the human only if their decision is needed: " + response["message"]
            return action
        state["status"] = "needs_input"
        state["question"] = response["message"]
    if action == "edit":
        files = source_files(run / "versions" / state["version"])
        before = dict(files)
        files.update({item["path"]: item["content"] for item in response["files"]})
        if files == before:
            raise ValueError("no source changed; use finish if checks pass and no required repair remains")
        version = f'{state["pending"]["number"]:04d}'
        destination = run / "versions" / version
        # Replay after a crash rebuilds the same unpublished version, not another edit.
        destination.mkdir(exist_ok=True)
        for name, content in files.items():
            path = destination / name
            with path.open("w") as stream:
                stream.write(content)
                stream.flush()
                os.fsync(stream.fileno())
        state["version"] = version
        check_fn(run, state)
        if config["mode"] == "pair":
            state["next"] = "challenger"
            state["task"] = "Challenge the integrated candidate against the specification; identify consequential defects."
    if action == "finish":
        if check_fn(run, state)["passed"]:
            state["status"] = "complete"
    return action


def account(state, pending, result):
    elapsed = result.get("elapsed_seconds")
    reserved = pending.get("reserved_seconds", 0)
    if isinstance(elapsed, (int, float)) and 0 <= elapsed <= reserved:
        state["active_seconds"] -= reserved - elapsed
    usage = result.get("usage")
    # Native CLI token usage is observable; invoice cost is not supplied.
    known = isinstance(usage, dict) and all(isinstance(usage.get(k), int) and usage[k] >= 0
                                          for k in ("input_tokens", "output_tokens"))
    if known:
        state["tokens_known"] += usage["input_tokens"] + usage["output_tokens"]
    if not known:
        state["unknown_usage_calls"] += 1
    state["calls"].append({"number": pending["number"], "role": pending["role"],
                           "model": pending["model"], "usage": usage,
                           "error": result.get("error"), "elapsed_seconds": result.get("elapsed_seconds"),
                           "cost_usd": None})


def finish_pending(run, state, config, result, check_fn=verify):
    pending = state["pending"]
    if (run / "STOP").exists():
        account(state, pending, result)
        state["pending"] = None
        state["status"] = "stopped"
        save(run, state)
        return
    if result.get("tool_execution_count", 0):
        result = dict(result, error="model used tools outside the experiment action protocol")
    if result.get("error") or result.get("response") is None:
        # The consumed call stays in the ledger. It is never invisibly retried.
        account(state, pending, result)
        state["history"].append({"role": pending["role"], "error": result.get("error") or "no response"})
        state["pending"] = None
        state["status"] = "interrupted"
        save(run, state)
        return
    result_text = json.dumps(result["response"], sort_keys=True)
    job = fleet(run, config, "get", id=pending["job"])
    attempt = job["attempts"][-1]
    if attempt["token"] != pending["token"]:
        raise RuntimeError("claim replaced; refusing stale model response")
    if job["state"] == "running":
        job = fleet(run, config, "complete", id=pending["job"], worker=pending["worker"],
                    token=pending["token"], result=result_text)
    if job["state"] not in ("reported", "accepted") or job["attempts"][-1].get("result") != result_text:
        raise RuntimeError("action is no longer reported by this claim; refusing candidate execution")
    try:
        apply_action(run, state, config, result["response"], check_fn)
    except ValueError as error:
        state["history"].append({"role": pending["role"], "error": str(error)})
        state["status"] = "ready"
        account(state, pending, dict(result, error=str(error)))
        fleet(run, config, "retry", id=pending["job"], token=pending["token"], evidence=str(error))
        state["pending"] = None
        save(run, state)
        return
    # This accepts delivery of a structurally valid action, not completion of the mission.
    fleet(run, config, "accept", id=pending["job"], token=pending["token"],
          evidence="valid role action recorded; mission acceptance uses separate black-box checks")
    account(state, pending, result)
    state["pending"] = None
    save(run, state)


def assert_frozen(run, config):
    manifest = json.loads((run / "manifest.json").read_text())
    if digest(config) != manifest["config_sha256"]:
        raise RuntimeError("trial config changed; create a new trial")
    if hashlib.sha256((ROOT / "PROTOCOL.md").read_bytes()).hexdigest() != manifest["protocol_sha256"]:
        raise RuntimeError("trial protocol changed; create a new trial")
    for name, expected in manifest["implementation"].items():
        if hashlib.sha256((ROOT / name).read_bytes()).hexdigest() != expected:
            raise RuntimeError("trial implementation changed: " + name)
    if hashlib.sha256(Path(config["fleet"]).read_bytes()).hexdigest() != manifest["fleet_sha256"]:
        raise RuntimeError("Fleet binary changed during the trial")


def supervised_complete(prompt, **kwargs):
    """A helper survives driver SIGKILL long enough to stop its native child."""
    output = kwargs["output_dir"]
    request = dict(kwargs, prompt=prompt, owner_pid=os.getpid())
    for key in ("cwd", "output_dir", "stop_file"):
        request[key] = str(request[key])
    atomic(output / "request.json", request)
    process = subprocess.Popen([sys.executable, str(ROOT / "call_worker.py"), str(output / "request.json")])
    try:
        code = process.wait(timeout=kwargs["timeout"] + 20)
    except subprocess.TimeoutExpired:
        (output / "helper-timeout").touch()
        raise RuntimeError("call helper did not terminate within its timeout; inspect before continuing") from None
    result_path = output / "result.json"
    if code or not result_path.exists():
        raise RuntimeError("call helper failed; call outcome is uncertain")
    return json.loads(result_path.read_text())


def run_steps(run, steps=None, complete_fn=None, check_fn=verify):
    complete_fn = complete_fn or supervised_complete
    if steps is not None and steps < 1:
        raise ValueError("steps must be positive")
    config = json.loads((run / "config.json").read_text())
    assert_frozen(run, config)
    with locked(run):
        state = load(run)
        if state["pending"]:
            receipt = run / "calls" / str(state["pending"]["number"]) / "result.json"
            if not receipt.exists():
                raise RuntimeError("uncertain interrupted call; use abandon to record it before continuing")
            finish_pending(run, state, config, json.loads(receipt.read_text()), check_fn)
        for _ in range(steps or config["max_calls"]):
            if (run / "STOP").exists():
                state["status"] = "stopped"
                save(run, state)
                break
            if state["status"] in ("complete", "needs_input", "interrupted", "stopped", "budget"):
                break
            if len(state["calls"]) >= config["max_calls"] or state["active_seconds"] >= config["wall_seconds"]:
                state["status"] = "budget"
                save(run, state)
                break
            if state["checks"] is None:
                check_fn(run, state)
            expected_source = state["checks"].get("source_sha256")
            if expected_source and digest(source_files(run / "versions" / state["version"])) != expected_source:
                raise RuntimeError("integrated candidate changed outside a recorded action; start a new trial")
            prompt = prompt_for(run, state, config)
            number = len(state["calls"]) + 1
            role = state["next"]
            model = config["model"]
            if role == "lead":
                model = config["lead_model"]
            if role == "challenger":
                model = config["critic_model"]
            output = run / "calls" / str(number)
            output.mkdir(parents=True, exist_ok=True)
            (output / "prompt.txt").write_text(prompt)
            timeout = min(config["call_timeout"], config["wall_seconds"] - state["active_seconds"])
            pending = {"number": number, "role": role, "worker": f"{role}-{number}",
                       "model": model, "job": f"call-{number}", "reserved_seconds": timeout,
                       "prompt_sha256": hashlib.sha256(prompt.encode()).hexdigest(), "token": None}
            state["pending"] = pending
            state["active_seconds"] += timeout
            save(run, state)  # Reservation precedes all external actions.
            fleet(run, config, "submit", id=pending["job"], brief=pending["prompt_sha256"])
            ttl = int(config["call_timeout"]) + 60
            job = fleet(run, config, "claim", id=pending["job"], worker=pending["worker"], key=pending["job"], ttl=ttl)
            pending["token"] = job["attempts"][-1]["token"]
            save(run, state)
            result = complete_fn(prompt, model=model, schema=SCHEMA,
                                 cwd=run / "versions" / state["version"], output_dir=output,
                                 timeout=timeout, stop_file=run / "STOP")
            atomic(output / "result.json", result)
            finish_pending(run, state, config, result, check_fn)
        if state["status"] == "ready" and (len(state["calls"]) >= config["max_calls"] or state["active_seconds"] >= config["wall_seconds"]):
            state["status"] = "budget"
            save(run, state)
        return load(run)


def mutate(run, command, message=""):
    with locked(run):
        state = load(run)
        if command == "abandon":
            # The status guard leads: account() mutates the in-memory state, and a
            # guard that fires after it leaves the caller reasoning about a state
            # that was edited and then thrown away.
            if state["status"] not in ("interrupted", "ready"):
                raise ValueError("only an interrupted trial can be abandoned/resumed")
            if state["pending"]:
                account(state, state["pending"], {"error": "interrupted outcome unknown", "usage": None})
                state["pending"] = None
            state["status"] = "ready"
            state["human_interventions"] += 1
            state["history"].append({"role": "human", "message": "Explicitly continue after interruption: " + message})
        if command == "answer":
            if state["status"] != "needs_input":
                raise ValueError("no human question is pending")
            state["history"].append({"role": "human", "message": message})
            state["human_interventions"] += 1
            state["status"] = "ready"
        if command == "change":
            if state["pending"]:
                raise ValueError("finish or abandon the pending call first")
            state["phase"] = 2
            state["changes"].append(message or "Implement phase 2 replay while preserving phase 1 behavior.")
            state["checks"] = None
            state["status"] = "ready"
        save(run, state)
        return state


def summary(run):
    state = load(run)
    config = json.loads((run / "config.json").read_text())
    current_source = digest(source_files(run / "versions" / state["version"]))
    return {"mode": config["mode"], "status": state["status"], "phase": state["phase"],
            "model": config["model"], "calls": len(state["calls"]),
            "reserved_calls": len(state["calls"]) + int(state["pending"] is not None),
            "tokens_known": state["tokens_known"], "unknown_usage_calls": state["unknown_usage_calls"],
            "cost_usd": None, "charged_model_seconds": state["active_seconds"],
            "human_interventions": state["human_interventions"], "checks": state["checks"],
            "candidate_sha256": current_source,
            "checked_source_matches": bool(state["checks"] and state["checks"].get("source_sha256") == current_source),
            "stop_requested": (run / "STOP").exists(),
            "candidate": str(run / "versions" / state["version"]),
            "question": state.get("question") if state["status"] == "needs_input" else None}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    init = commands.add_parser("init")
    init.add_argument("run", type=Path)
    init.add_argument("--fleet", required=True)
    init.add_argument("--source", type=Path)
    init.add_argument("--context", type=Path, help="existing team handoff/messages as a JSON array")
    init.add_argument("--mode", choices=("solo", "lead", "pair"), default="lead")
    init.add_argument("--model", default="gpt-5.6-luna")
    init.add_argument("--lead-model")
    init.add_argument("--critic-model")
    init.add_argument("--max-calls", type=int, default=10)
    init.add_argument("--call-timeout", type=float, default=180)
    init.add_argument("--wall-seconds", type=float, default=1200)
    for name in ("run", "status", "stop", "answer", "change", "abandon"):
        command = commands.add_parser(name)
        command.add_argument("run", type=Path)
        if name == "run":
            command.add_argument("--steps", type=int)
        if name in ("answer", "change", "abandon"):
            command.add_argument("message", nargs="?", default="")
    args = parser.parse_args()
    run = args.run.resolve()
    if args.command == "init":
        config = {key: value for key, value in vars(args).items() if key not in ("command", "run", "source", "context")}
        config["lead_model"] = config["lead_model"] or config["model"]
        config["critic_model"] = config["critic_model"] or config["model"]
        initialize(run, config, args.source, args.context)
    if args.command == "run":
        run_steps(run, args.steps)
    if args.command == "stop":
        (run / "STOP").touch()
    if args.command in ("answer", "change", "abandon"):
        mutate(run, args.command, args.message)
    print(json.dumps(summary(run), indent=2))


if __name__ == "__main__":
    try:
        main()
    except (ValueError, RuntimeError, OSError, subprocess.SubprocessError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
