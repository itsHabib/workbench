"""Read existing run evidence; never equate an assessment file with success."""
import hashlib
import json
import os
from pathlib import Path
import subprocess

EDIT_TOOLS = ("Write", "Edit", "MultiEdit")


def read(path):
    return json.loads(path.read_text())


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def git(path, *args):
    if args[0] == "diff":
        args = ("diff", "--no-ext-diff", "--no-textconv", *args[1:])
    return subprocess.check_output(["git", "--no-pager", "-c", "core.fsmonitor=false",
                                    "-c", "core.hooksPath=/dev/null", "-C", str(path), *args],
                                   timeout=30, env={**os.environ, "GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.devnull})


def traces(root):
    """Each observed native trace with its attempt's launch time in milliseconds."""
    for trace in (root / "state/watch/delivery").glob("*.trace.jsonl"):
        meta = Path(str(trace).removesuffix(".trace.jsonl") + ".meta.json")
        launched = read(meta)["at"] * 1000 if meta.exists() else None
        yield launched, [json.loads(line) for line in trace.read_text().splitlines()]


def file_changes(event, draft, launched):
    """(at_ms, session, kind, content_sha256) for a traced edit of draft, Codex or Claude."""
    params = event.get("params", {})
    item = params.get("item", {})
    if event.get("method") == "item/completed" and item.get("type") == "fileChange":
        for change in item.get("changes", []):
            if change.get("path") == str(draft):
                kind = change.get("kind", {}).get("type")
                yield event["emittedAtMs"], params.get("threadId"), kind, hashlib.sha256(change.get("diff", "").encode()).hexdigest()
    if event.get("type") != "assistant" or launched is None:
        return
    for block in event.get("message", {}).get("content", []):
        tool = block.get("input", {}) if block.get("type") == "tool_use" and block.get("name") in EDIT_TOOLS else {}
        if tool.get("file_path") == str(draft):
            kind = "add" if block["name"] == "Write" else "update"
            yield launched, event.get("session_id"), kind, hashlib.sha256(tool.get("content", "").encode()).hexdigest()


def draft_continuity(root, draft, interruption, author_session):
    """Observe ownership through recovery; the author may update its plan later.

    Codex events carry emission times. Claude SDK messages do not, so a Claude
    edit is placed at its attempt's launch: the draft's creating write belongs to
    an attempt launched before the interruption, and every later edit to one
    launched after the resume.
    """
    changes = sorted((row for launched, events in traces(root) for event in events
                      for row in file_changes(event, draft, launched)), key=lambda row: row[0])
    if not changes:
        return False
    first_at, first_session, first_kind, created = changes[0]
    return (first_kind == "add" and created == interruption["draft_sha256"]
            and first_session == author_session and first_at <= interruption["requested_at"] * 1000
            and all(at > interruption["resumed_at"] * 1000 and session == author_session
                    and kind in ("add", "update") for at, session, kind, _ in changes[1:]))


def tool_inputs(event):
    """The text of each observed tool invocation, Codex or Claude."""
    item = event.get("params", {}).get("item", {})
    if event.get("method") == "item/started" and item.get("type") in ("commandExecution", "fileChange"):
        yield json.dumps(item.get("command") or item.get("changes"))
    if event.get("type") == "assistant":
        for block in event.get("message", {}).get("content", []):
            if block.get("type") == "tool_use":
                yield json.dumps(block.get("input"))


def reference_reads(root, info):
    """Observed tool inputs naming the source checkout's committed example task."""
    sources = {str(Path(info["fleet_source"]) / info["task_path"]), str(Path(__file__).resolve().parent / "task")}
    return sorted({text[:300] for _, events in traces(root) for event in events
                   for text in tool_inputs(event) if any(source in text for source in sources)})


def preserved(checkout, head, seed, path):
    """The committed result, which the verifier and Rooms test, keeps the seed's bytes."""
    try:
        return git(checkout, "show", head + ":" + path) == git(checkout, "show", seed + ":" + path)
    except subprocess.CalledProcessError:
        return False  # deleted or never seeded


def own_checkout(receipt, checkout):
    """Fleet recorded the receipt in this checkout: its worktree, from anywhere inside it."""
    cwd = receipt.get("cwd")
    return receipt.get("worktree") == str(checkout) and bool(cwd) and Path(cwd).is_relative_to(checkout)


def rooms_evidence(root, info, patch, receipts, head):
    """Rooms applied the identical patch, succeeded, collected and cleaned up."""
    out = Path(info["rooms"]["out"])
    summary = read(out / "summary.json") if (out / "summary.json").exists() else {}
    lifecycle = out / "lifecycle.ndjson"
    events = [json.loads(line).get("event") for line in lifecycle.read_text().splitlines()] if lifecycle.exists() else []
    matching = [r for r in receipts if r.get("head") == head and r.get("kind") == "rooms"]
    latest = max(matching, key=lambda r: r["at"], default={})
    checks = {"rooms_receipt": (latest.get("verdict") == "pass" and latest.get("dirty") is False
                                and own_checkout(latest, info["verifier"]["cwd"])),
              "rooms_identical_patch": (summary.get("input_patch_sha256") == summary.get("returned_patch_sha256") == digest(patch)),
              "rooms_succeeded": summary.get("cli_exit") == 0 and summary.get("command_status") == "succeeded" and summary.get("command_exit") == 0,
              "rooms_collected": "collection_done" in events and "cleanup_done" in events,
              # Labs prepared before the entry point's hash was recorded check the adapter only.
              "rooms_adapter_unchanged": (digest(root / "bin/rooms-check.py") == info["rooms"]["adapter_sha256"]
                                          and info["rooms"].get("cli_sha256", digest(Path(info["rooms"]["cli"])))
                                          == digest(Path(info["rooms"]["cli"])))}
    return checks, {"receipt": latest, "summary": summary, "events": events}


def audit(root):
    info = read(root / "resolved.json")
    author = Path(info["author"]["cwd"])
    verifier = Path(info["verifier"]["cwd"])
    head = git(author, "rev-parse", "HEAD").decode().strip()
    checks = {}
    checks["new_result"] = head != info["seed_head"]
    # git status may run a repository's clean filters. Cleanliness is the
    # independent receipt's observation; this outer reader does not recompute it.
    checks["verifier_same_head"] = git(verifier, "rev-parse", "HEAD").decode().strip() == head
    patch = root / "result/worker.patch"
    checks["patch_matches_result"] = patch.read_bytes() == git(author, "diff", info["base"], head)
    task = author / info["task_path"]
    for name in ("input.json", "test_report.py", "INPUT.md"):
        checks["preserved_" + name] = preserved(author, head, info["seed_head"], info["task_path"] + "/" + name)
    interruption = read(root / "control/interruption.json")
    draft = task / "PLAN.md"
    # Final PLAN.md may legitimately document the answer and completed tests.
    # The trace distinguishes the original author's continuation from replacement.
    checks["assignment_retained"] = bool(interruption["assignments"]) and all(
        digest(root / "state/assign" / name) == value["sha256"] for name, value in interruption["assignments"].items())
    receipts = [read(p) for p in (root / "state/receipts").glob("*.json")]
    evidence = {}
    for kind, cwd in (("implementation", author), ("verify", verifier)):
        matching = [r for r in receipts if r.get("head") == head and r.get("kind") == kind]
        latest = max(matching, key=lambda r: r["at"], default={})
        evidence[kind] = latest
        checks[kind + "_receipt"] = (latest.get("verdict") == "pass" and latest.get("dirty") is False
                                         and own_checkout(latest, cwd))
    checks["independent_sessions"] = bool(evidence["verify"].get("session")) and evidence["verify"].get("session") != evidence["implementation"].get("session")
    checks["observed_draft_continuity"] = draft_continuity(root, draft, interruption, evidence["implementation"].get("session"))
    attempts = [read(p) for p in (root / "state/watch/delivery").glob("*.meta.json")]
    supervisor_sessions = []
    for meta in attempts:
        state = read(Path(meta["state_file"]))
        if meta["address"] == info["supervisor"]["address"]:
            supervisor_sessions.append(state.get("provider_session"))
        if meta["address"] in (info["author"]["address"], info["verifier"]["address"]):
            kind = "implementation" if meta["address"] == info["author"]["address"] else "verify"
            if state.get("provider_session") == evidence[kind].get("session"):
                checks[kind + "_provider_observed"] = True
        checks["terminal_" + Path(meta["attempt"]).name] = (state.get("provider_terminal") is True
                          and Path(meta["attempt"] + ".exit.json").exists())
    checks["fresh_supervisor"] = (interruption["provider_session"] in supervisor_sessions
                                   and len(set(supervisor_sessions) - {None, interruption["provider_session"]}) > 0)
    for kind in ("implementation", "verify"):
        checks.setdefault(kind + "_provider_observed", False)
    observed_reads = reference_reads(root, info)
    checks["no_observed_reference_reads"] = not observed_reads
    rooms = None
    if info.get("rooms"):
        rooms_checks, rooms = rooms_evidence(root, info, patch, receipts, head)
        checks.update(rooms_checks)
    return {"status": "pass" if all(checks.values()) else "fail", "head": head,
            "patch_sha256": digest(patch), "checks": checks, "receipts": evidence, "rooms": rooms,
            "supervisor_sessions": supervisor_sessions, "observed_reference_reads": observed_reads,
            "scope": "artifact audit only; no worker code is executed; test and semantic judgment belong to the independent verifier; no merge or isolation authority"}
