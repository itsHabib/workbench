"""xlib — the suite's facade over the fleet binary, with hook.py's function names.

The reference suite's concurrency scenarios were written against the Python module (`import hook`;
`hook.acquire_lease(...)`). A Go binary cannot be imported, so this module keeps every name those
scenarios call and implements each over one `fleet x-*` verb, which is the same primitive the hook
or a verb would run. A scenario changes exactly one line — `import hook` becomes `import xlib as
hook` — and its logic, its forced interleavings (FLEET_TEST_PAUSE_* passes through the environment)
and its assertions are untouched. Pure file helpers (path, now, read/write_json, _unlink) are plain
Python: they never went through the substrate's locks in the reference either.
"""
import contextlib
import json
import os
import subprocess
import sys
import time

BIN = os.environ.get("FLEET_BIN") or os.path.join(os.path.dirname(os.path.abspath(__file__)), "fleet.bin")


def _state():
    return os.environ.get("FLEET_STATE") or os.path.expanduser("~/.fleet")


def path(*parts):
    return os.path.join(_state(), *parts)


def now():
    return time.time()


def read_json(p, default=None):
    try:
        with open(p, encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return default


def write_json(p, obj):
    os.makedirs(os.path.dirname(p), exist_ok=True)
    tmp = f"{p}.{os.getpid()}.tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        json.dump(obj, f)
    os.replace(tmp, p)


def append_jsonl(p, obj):
    os.makedirs(os.path.dirname(p), exist_ok=True)
    with open(p, "a", encoding="utf-8") as f:
        f.write(json.dumps(obj) + "\n")


def _unlink(p):
    try:
        os.remove(p)
    except OSError:
        pass


def _run(*args, env=None):
    return subprocess.run([BIN, *args], capture_output=True, text=True, env=env or os.environ)


def _out(*args):
    return _run(*args).stdout.strip()


def safe(name):
    return _out("x-safe", name)


def key_file(sub, key):
    return path(sub, safe(key) + ".json")


def repo_id(start):
    return _out("x-id", start) or None


def scope(start, branch):
    return _out("x-key", start, branch) or None


def key_parts(key):
    if key.startswith("slot:"):
        return {"kind": "resource", "name": key[len("slot:"):]}
    if key.startswith("repo:"):
        repo, _, branch = key[len("repo:"):].partition(":")
        return {"kind": "branch", "repo": repo, "branch": branch}
    return {}


def map_rows_for(cwd):
    r = json.loads(_out("x-role", cwd) or "{}")
    return r.get("role"), r.get("tenant"), r.get("slot")


def role_of(cwd):
    return map_rows_for(cwd)[0]


def lease(key):
    out = _out("x-lease", key)
    return json.loads(out) if out and out != "null" else None


def lease_record(key, sid, role, cwd, note=None):
    parts = key_parts(key)
    return {"key": key, "kind": parts.get("kind", "branch"), "repo": parts.get("repo"),
            "branch": parts.get("branch"), "name": parts.get("name"), "session": sid, "role": role,
            "cwd": cwd, "since": now(), "note": note}


def acquire_lease(key, rec):
    r = _run("x-acquire", key, rec["session"], rec.get("role") or "", rec.get("cwd") or "", rec.get("note") or "")
    return r.returncode == 0


def take_lease(key, branch, sid, role, cwd, note=None):
    out = _out("x-take", key, branch or "", sid, role or "", cwd or "", note or "")
    return json.loads(out) if out and out != "null" else None


def drop_lease(key, sid):
    return _run("x-drop", key, sid).returncode == 0


def check_lease(key, branch, sid, role, cwd, env=None):
    r = _run("x-check-lease", key, branch, sid, role or "", cwd or "", env=env)
    return None if r.returncode == 0 else (r.stdout.strip() or "denied")


def switch_targets(cmd, start):
    return json.loads(_out("x-switch", start, cmd) or "[]")


def switch_target(cmd, start):
    t = switch_targets(cmd, start)
    return t[0] if t else None


@contextlib.contextmanager
def keylock(key, seconds=30):
    """Hold the key's lock for the duration of the block, through a child that takes it and
    sleeps. The child prints `held` once the lock is taken; the block runs after that."""
    child = subprocess.Popen([BIN, "x-hold", key, str(seconds)], stdout=subprocess.PIPE, text=True)
    line = child.stdout.readline()
    if line.strip() != "held":
        child.kill()
        raise RuntimeError(f"x-hold did not take {key}: {line!r}")
    try:
        yield
    finally:
        child.kill()
        child.wait()


def board_rows():
    return json.loads(_out("board", "--json") or "[]")


def stop_flag(key):
    out = _out("x-stop-flag", key)
    return json.loads(out) if out and out != "null" else None


def migrate_legacy_keys():
    _run("x-migrate")


def _remove_owned(sub, sid, field="session", keep=None):
    _run("x-remove-owned", sub, sid, field)


def check_requires(rec, sid, tool, inp):
    """`rec` is the session record on disk; the binary reads the same file."""
    r = _run("x-check-requires", sid, tool, (inp or {}).get("command", ""))
    return None if r.returncode == 0 else (r.stdout.strip() or "denied")


def session_alive(rec):
    """Mirror of the reference rule, used by scenarios that classify records they wrote."""
    if not rec or rec.get("ended"):
        return False
    if rec.get("pid_kind") == "harness":
        try:
            os.kill(rec["pid"], 0)
            return True
        except OSError:
            return False
    return (now() - rec.get("last_event_at", 0)) < int(os.environ.get("FLEET_STALE_S", "7200"))
