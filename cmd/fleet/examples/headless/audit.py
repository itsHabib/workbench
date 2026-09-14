"""Read existing run evidence; never equate an assessment file with success."""
import hashlib
import json
import os
from pathlib import Path
import subprocess


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


def draft_continuity(root, draft, interruption, author_session):
    """Observe ownership through recovery; the author may update its plan later."""
    changes = []
    for trace in (root / "state/watch/delivery").glob("*.trace.jsonl"):
        for line in trace.read_text().splitlines():
            event = json.loads(line)
            params = event.get("params", {})
            item = params.get("item", {})
            if event.get("method") != "item/completed" or item.get("type") != "fileChange":
                continue
            for change in item.get("changes", []):
                if change.get("path") == str(draft):
                    changes.append((event["emittedAtMs"], params.get("threadId"), change))
    changes.sort(key=lambda row: row[0])
    if not changes:
        return False
    first_at, first_session, first = changes[0]
    created = hashlib.sha256(first.get("diff", "").encode()).hexdigest()
    return (first.get("kind", {}).get("type") == "add" and created == interruption["draft_sha256"]
            and first_session == author_session and first_at <= interruption["requested_at"] * 1000
            and all(at > interruption["resumed_at"] * 1000 and session == author_session
                    and change.get("kind", {}).get("type") == "update" for at, session, change in changes[1:]))


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
        checks["preserved_" + name] = (task / name).read_bytes() == git(author, "show", info["seed_head"] + ":" + info["task_path"] + "/" + name)
    interruption = read(root / "control/interruption.json")
    draft = task / "PLAN.md"
    # Final PLAN.md may legitimately document the answer and completed tests.
    # The trace distinguishes the original author's continuation from replacement.
    checks["assignment_retained"] = all(digest(root / "state/assign" / name) == value["sha256"]
                                         for name, value in interruption["assignments"].items())
    receipts = [read(p) for p in (root / "state/receipts").glob("*.json")]
    evidence = {}
    for kind, cwd in (("implementation", author), ("verify", verifier)):
        matching = [r for r in receipts if r.get("head") == head and r.get("kind") == kind]
        latest = max(matching, key=lambda r: r["at"], default={})
        evidence[kind] = latest
        checks[kind + "_receipt"] = (latest.get("verdict") == "pass" and latest.get("dirty") is False
                                         and latest.get("cwd") == str(cwd) and latest.get("worktree") == str(cwd))
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
    return {"status": "pass" if all(checks.values()) else "fail", "head": head,
            "patch_sha256": digest(patch), "checks": checks, "receipts": evidence,
            "supervisor_sessions": supervisor_sessions,
            "scope": "artifact audit only; no worker code is executed; test and semantic judgment belong to the independent verifier; no merge or isolation authority"}
