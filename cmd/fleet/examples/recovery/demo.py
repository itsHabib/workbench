#!/usr/bin/env python3
"""Disposable, keyless recovery probe. Requires Python 3, Git and a Fleet binary.

The child processes are deterministic harness fixtures, not model/provider runs.
Only their PID provenance is seeded; hooks, work, handoffs and receipts use Fleet.
"""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile


SOURCE = "one useful foundation\n"
NEXT = "Wait for worker:transform; then verify output bytes and create summary.txt if absent. No merge grant was supplied."


def run(args, cwd, env, stdin=None):
    result = subprocess.run(args, cwd=cwd, env=env, input=stdin, text=True,
                            capture_output=True, timeout=15)
    if result.returncode:
        raise RuntimeError(f"{args[:3]}: {result.stderr or result.stdout}")
    return result.stdout


def git(repo, env, *args):
    return run(["git", "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false",
                "-c", "user.name=Recovery demo", "-c", "user.email=demo@example.invalid",
                *args], repo, env).strip()


def call(binary, repo, env, *args):
    return run([binary, *args], repo, env)


def hook(binary, repo, env, sid, event, **fields):
    return run([binary, "hook", "claude"], repo, env, json.dumps({
        "session_id": sid, "cwd": str(repo), "hook_event_name": event, **fields}))


def register(binary, repo, env, sid):
    hook(binary, repo, env, sid, "SessionStart")
    # These local Python children stand in for a harness. Avoid attributing them
    # to a real Codex ancestor when the demo is launched from a desktop task.
    path = Path(env["FLEET_STATE"]) / "sessions" / f"{sid}.json"
    record = json.loads(path.read_text())
    record.update(pid=os.getpid(), pid_kind="harness")
    path.write_text(json.dumps(record))


def receipt(binary, repo, env, sid, kind, observable):
    head = git(repo, env, "rev-parse", "HEAD")
    call(binary, repo, env, "receipt", head, kind, "pass", observable, "--session", sid)


def checkpoint():
    # A small operational checkpoint, without padding; accepted bodies may be
    # 16 KiB, while the existing startup hint is intentionally only 1 KiB.
    return "\n".join([
        "Batch: preserve the completed input and active transformation, then produce a local summary.",
        "Input: input/source.txt is committed on branch input. The author emitted implementation/pass. "
        "A separate verifier process checked the committed bytes and emitted verify/pass at that exact head. "
        "Read current receipts and compare provenance before treating it as independently verified.",
        "Active transformation: worker:transform has a branch lease on transform and an uncommitted output.draft. "
        "The worker is deliberately waiting for this fixture's continuation signal. A killed supervisor does not "
        "end that worker. Keep the process and every dirty byte; no takeover or redispatch is required.",
        "Remaining: once the worker completes, compare output.txt to the current source.txt uppercased. "
        "Do not infer transformation success from activity, a live PID or a provider exit. The final summary is "
        "a local artifact; an existing matching summary should be kept without rewriting it.",
        "Retrieval: fleet work --for supervisor:batch --json gives rows; inspect worker:transform gives activity; "
        "receipts supplies exact-head evidence. Git provides current heads and dirty bytes. Observations are "
        "separate reads, not an atomic snapshot of the batch. Recheck before an effect if a writer may change it.",
        "Authority: only this disposable local transformation and summary are authorized. The checkpoint is "
        "authored context, not a grant. There is no authority here to publish, merge, replace a live writer, "
        "alter configuration or mint grants. Gate remains a separate source of merge authorization.",
    ])


def child(kind, root, binary, env):
    # Completion callers also run in fresh processes with no predecessor memory.
    if kind.startswith("complete-"):
        print(json.dumps({"effects": finish(root, kind.removeprefix("complete-"))}))
        return
    repo = root / ("input" if kind in ("author", "verifier") else kind)
    sid = f"demo-{kind}"
    if kind == "observe":
        inspect = json.loads(call(binary, root / "supervisor", env, "inspect", "supervisor:batch", "--json"))
        work = json.loads(call(binary, root / "supervisor", env, "work", "--for", "supervisor:batch", "--json"))
        records = json.loads(call(binary, root / "input", env, "receipts", "--json"))
        worker = json.loads(call(binary, root / "supervisor", env, "inspect", "worker:transform", "--json"))
        dirty = git(Path(worker["agent"]["cwd"]), env, "status", "--porcelain")
        print(json.dumps({"inspect": inspect, "work": work, "receipts": records, "worker": worker, "dirty": dirty}))
        return
    register(binary, repo, env, sid)
    if kind == "author":
        hook(binary, repo, env, sid, "PreToolUse", tool_name="Write", tool_input={"file_path": str(repo / "source.txt")})
        (repo / "source.txt").write_text(SOURCE)
        git(repo, env, "add", "source.txt")
        git(repo, env, "commit", "-m", "record input")
        receipt(binary, repo, env, sid, "implementation", "source.txt committed")
    if kind == "verifier":
        assert (repo / "source.txt").read_text() == SOURCE
        assert git(repo, env, "show", "HEAD:source.txt") == SOURCE.rstrip()
        receipt(binary, repo, env, sid, "verify", "independent process compared source bytes at HEAD")
    if kind == "transform":
        hook(binary, repo, env, sid, "PreToolUse", tool_name="Write", tool_input={"file_path": str(repo / "output.draft")})
        (repo / "output.draft").write_text("retained unfinished draft\n")
        print("ready", flush=True)
        assert sys.stdin.readline().strip() == "finish"
        (repo / "output.draft").write_text((root / "input" / "source.txt").read_text().upper())
        (repo / "output.draft").rename(repo / "output.txt")
        git(repo, env, "add", "output.txt")
        git(repo, env, "commit", "-m", "complete transformation")
        receipt(binary, repo, env, sid, "implementation", "upper-case output committed")
    if kind == "supervisor":
        call(binary, repo, env, "handoff", "--role", checkpoint(), NEXT, "--session", sid)
        print("ready", flush=True)
        sys.stdin.readline()  # The driver kills only this known local child.
    hook(binary, repo, env, sid, "SessionEnd")


def file_facts(root):
    return {str(p.relative_to(root)): (hashlib.sha256(p.read_bytes()).hexdigest(), p.stat().st_mtime_ns)
            for p in root.rglob("*") if p.is_file() and ".git" not in p.parts}


def ensure_file(path, contents):
    if path.exists():
        if path.read_text() != contents:
            raise RuntimeError(f"external content conflicts at {path.name}; preserved")
        return "keep"
    with path.open("x") as output:
        output.write(contents)
    return "create"


def finish(root, mode):
    source = (root / "input" / "source.txt").read_text()
    output = (root / "transform" / "output.txt").read_text()
    assert output == source.upper()
    contents = f"Verified locally: {output}"
    if mode == "direct":
        return [ensure_file(root / "summary.txt", contents)]
    # An optional desired-data client over the same operation, not a language
    # entry or second execution ledger. It adds an inspectable proposal only.
    desired = {"summary.txt": contents}
    proposed = [{"path": name, "contents": value, "change": file_change(root / name, value)}
                for name, value in desired.items()]
    print(json.dumps({"proposed": proposed}))
    return [ensure_file(root / item["path"], item["contents"]) for item in proposed]


def file_change(path, value):
    if not path.exists():
        return "create"
    return "keep" if path.read_text() == value else "conflict"


def start(kind, root, binary, env):
    p = subprocess.Popen([sys.executable, __file__, "--child", kind, str(root), binary],
                         env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                         stderr=subprocess.PIPE, text=True)
    return p


def ready(p):
    # Readiness has a bounded wait and propagates child errors. Poll only a local
    # pipe: the Fleet watcher is never involved in this deterministic fixture.
    import selectors
    with selectors.DefaultSelector() as selector:
        selector.register(p.stdout, selectors.EVENT_READ)
        if not selector.select(timeout=15):
            raise RuntimeError("fixture did not become ready")
        if p.stdout.readline().strip() != "ready":
            raise RuntimeError(p.stderr.read() if p.poll() is not None else "fixture failed")


def demo(binary, expect_missing):
    root = Path(tempfile.mkdtemp(prefix="fleet-recovery-")).resolve()
    env = dict(os.environ, FLEET_STATE=str(root / "state"), ORG_STATE=str(root / "org"),
               FLEET_LANES=str(root / "lanes"), FLEET_GITHUB="off", FLEET_WATCH="off")
    (root / "org").mkdir()
    roles = []
    for name, role in (("input", "worker:input"), ("transform", "worker:transform"), ("supervisor", "supervisor:batch")):
        repo = root / name
        repo.mkdir()
        git(repo, env, "init", "-b", name)
        git(repo, env, "commit", "--allow-empty", "-m", "fixture")
        roles.append(f"{repo} demo {role}\n")
    (root / "org" / "roles.map").write_text("".join(roles))
    for name in ("input", "transform"):
        call(binary, root / name, env, "dispatch", name, "--as", "implementation", "--for", "supervisor:batch")
    for kind in ("author", "verifier"):
        run([sys.executable, __file__, "--child", kind, str(root), binary], root, env)
    worker = start("transform", root, binary, env)
    supervisor = None
    try:
        ready(worker)
        supervisor = start("supervisor", root, binary, env)
        ready(supervisor)
        supervisor.kill()
        supervisor.wait(timeout=5)
        before = file_facts(root)
        # A new OS process receives only the disposable root and binary path.
        raw = run([sys.executable, __file__, "--child", "observe", str(root), binary], root, env)
        observed = json.loads(raw)
        assert before == file_facts(root), "observation changed retained state or dirty work"
        (root / "observed.json").write_text(json.dumps(observed, indent=2))
        inspect = observed["inspect"]
        full = inspect.get("role_handoff_record")
        if expect_missing:
            assert full is None and NEXT not in inspect["role_handoff"]
            stored = json.loads(next((root / "state" / "role-handoff").glob("*.json")).read_text())
            assert stored["next"] == NEXT
            print(json.dumps({"reproduced": "documented reader loses next step; raw checkpoint is intact", "artifacts": str(root)}))
            return
        assert full["conclusion"] == checkpoint() and full["next"] == NEXT
        assert inspect["agent"]["occupancy"] == "dead"
        assert observed["dirty"] == "?? output.draft"
        rows = observed["work"]
        assert {r["change"]: r["state"] for r in rows} == {"input": "done", "transform": "working"}, rows
        assert next(r for r in rows if r["change"] == "transform")["hands"] == "demo-transform"
        receipts = observed["receipts"]
        source_head = git(root / "input", env, "rev-parse", "HEAD")
        proof = [r for r in receipts if r["head"] == source_head]
        assert {r["kind"]: r["session"] for r in proof} == {"implementation": "demo-author", "verify": "demo-verifier"}, proof
        assert worker.poll() is None and supervisor.returncode != 0
        input_before = file_facts(root / "input")
        worker.stdin.write("finish\n")
        worker.stdin.flush()
        _, errors = worker.communicate(timeout=15)
        assert worker.returncode == 0, errors
        assert file_facts(root / "input") == input_before, "completed input was rewritten"
        # Both clients get the same completed outputs, retained records and
        # compare-before-create operation. Run each twice in its own directory.
        comparison = {}
        for mode in ("direct", "declarative"):
            target = root / mode
            target.mkdir()
            (target / "input").symlink_to(root / "input", target_is_directory=True)
            (target / "transform").symlink_to(root / "transform", target_is_directory=True)
            args = [sys.executable, __file__, "--child", "complete-" + mode, str(target), binary]
            first = json.loads(run(args, root, env).splitlines()[-1])["effects"]
            facts = file_facts(target)
            second = json.loads(run(args, root, env).splitlines()[-1])["effects"]
            assert first == ["create"] and second == ["keep"] and facts == file_facts(target)
            (target / "summary.txt").write_text("external edit\n")
            try:
                run(args, root, env)
            except RuntimeError:
                pass
            else:
                raise AssertionError("external change overwritten")
            assert (target / "summary.txt").read_text() == "external edit\n"
            comparison[mode] = {"first": first, "retry": second, "external_edit": "preserved"}
        print(json.dumps({"supervisor_exit": supervisor.returncode, "full_checkpoint_bytes": len(full["conclusion"].encode()),
                          "recovered": ["completed input", "independent receipt", "live writer", "dirty draft", "next step", "authority context"],
                          "clients": comparison, "artifacts": str(root)}, indent=2))
    finally:
        for p in (supervisor, worker):
            if p is not None and p.poll() is None:
                p.kill()
                p.wait(timeout=5)


if __name__ == "__main__":
    if sys.argv[1:2] == ["--child"]:
        child(sys.argv[2], Path(sys.argv[3]), sys.argv[4], os.environ)
    else:
        if len(sys.argv) not in (2, 3) or (len(sys.argv) == 3 and sys.argv[2] != "--expect-missing"):
            raise SystemExit("usage: python3 demo.py /absolute/fleet [--expect-missing]")
        demo(str(Path(sys.argv[1]).resolve()), len(sys.argv) == 3)
