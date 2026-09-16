"""A disposable operator example. Fleet owns all agent launches and mail wakes."""
import argparse
import difflib
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import subprocess
import tempfile
import time
import tomllib
from audit import audit

HERE = Path(__file__).resolve().parent
REPO = HERE.parents[3]
BASE = "92a706a7982a527ade967e43b68afd4bc1e5d667"
TASK = "cmd/fleet/examples/headless/task"
ROLES = ("supervisor", "author", "verifier")
PROVIDERS = ("codex", "claude")
# Claude runs without an OS sandbox here. File tools are confined to the lab by
# path rules under dontAsk; Bash is unconfined and its denies are prefix rules
# only, not a boundary. The README says so.
CLAUDE_TOOLS = "Bash,Read,Write,Edit,Glob,Grep"
CLAUDE_DENY = ["Bash(git push:*)", "Bash(gh:*)", "Bash(curl:*)", "Bash(wget:*)", "Bash(limactl:*)", "WebFetch", "WebSearch"]
# A launching agent's own session must not reach the lab; its account and
# provider routing must: every CLAUDE_CODE_USE_*, SKIP_*_AUTH and CLIENT_*.
CLAUDE_KEEP = re.compile(r"CLAUDE_CONFIG_DIR|CLAUDE_CODE_(USE_(BEDROCK|VERTEX|FOUNDRY|MANTLE|GATEWAY|ANTHROPIC_AWS|"
                         r"ANTHROPIC_GOOGLE_CLOUD)|SKIP_\w+_AUTH|SKIP_AWS_CRED_CACHE|CLIENT_\w+|OAUTH_TOKEN|"
                         r"OAUTH_REFRESH_TOKEN|OAUTH_CLIENT_ID|API_KEY_HELPER_TTL_MS|CUSTOM_OAUTH_URL|CERT_STORE)")


def run(args, cwd, env=None, check=True, timeout=60):
    p = subprocess.run([str(a) for a in args], cwd=cwd, env=env, text=True, capture_output=True, timeout=timeout)
    if check and p.returncode:
        raise RuntimeError(f"{args[0:3]}: {p.returncode}: {p.stderr or p.stdout}")
    return p


def git(cwd, *args):
    return run(["git", *args], cwd).stdout.strip()


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def parent_session(name):
    """A launching agent's own session variables never reach the lab's agents."""
    return name == "CLAUDECODE" or (name.startswith("CLAUDE_") and not CLAUDE_KEEP.fullmatch(name))

def claude_allow(root):
    """Bash plus file tools whose paths stay inside the lab (Read rules cover Glob/Grep)."""
    return ["Bash", f"Edit(/{root}/**)", f"Read(/{root}/**)"]


def env_for(root):
    inherited = {k: v for k, v in os.environ.items() if not parent_session(k)}
    env = {**inherited, "PATH": str(root / "bin") + os.pathsep + os.environ["PATH"],
           "FLEET_STATE": str(root / "state"), "ORG_STATE": str(root / "org"),
           "FLEET_LANES": str(root / "lanes"), "ORG_TENANT": "headless-lab",
           "FLEET_WATCH": "off", "FLEET_GITHUB": "off", "FLEET_NOTIFY": "",
           "PYTHONDONTWRITEBYTECODE": "1"}
    resolved = root / "resolved.json"
    runtime = json.loads(resolved.read_text()).get("provider", {}).get("runtime_home") if resolved.exists() else None
    if runtime:
        env["FLEET_RUNTIME_HOME"] = runtime
    return env


def fleet(root, *args, cwd=None, check=True):
    return run([root / "bin/fleet", *args], cwd or root / "workbench-lead", env_for(root), check)


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n")


def codex_wrapper(root, directories):
    real = shutil.which("codex")
    if not real:
        raise RuntimeError("authenticated Codex CLI is required; no installer is run")
    config = Path(os.environ.get("CODEX_HOME", str(Path.home() / ".codex"))) / "config.toml"
    current = tomllib.loads(config.read_text()) if config.exists() else {}
    options = {"sandbox_mode": "workspace-write", "approval_policy": "never", "notify": [],
               "sandbox_workspace_write.writable_roots": [str(root)],
               "sandbox_workspace_write.network_access": False,
               "web_search": "disabled"}
    for name in ("apps", "plugins", "memories", "multi_agent", "browser_use", "computer_use"):
        options["features." + name] = False
    options["features.skip_host_skill_discovery"] = True
    for name in current.get("mcp_servers", {}):
        if "." in name:
            raise RuntimeError("this example needs explicit handling for a dotted MCP server name")
        options['mcp_servers.' + name + '.enabled'] = False
    options["projects"] = {str(directory): {"trust_level": "trusted"} for directory in directories}
    args = []
    for key, value in options.items():
        args.extend(["-c", key + "=" + toml_value(value)])
    wrapper = root / "bin/codex"
    wrapper.write_text("#!/usr/bin/env python3\nimport os,sys\n" +
                       "os.execv(" + repr(real) + ", " + repr([real, *args]) + " + sys.argv[1:])\n")
    wrapper.chmod(0o700)
    return {"name": "codex", "real_cli": real, "overrides": options, "wrapper_sha256": sha(wrapper),
            "scope": "process-local CLI settings; normal auth and trusted hooks retained; no global config edit"}


def claude_wrapper(root, directories, runtime_home):
    real = shutil.which("claude")
    if not real:
        raise RuntimeError("an authenticated Claude Code CLI is required; no installer is run")
    runtime = Path(runtime_home or os.environ.get("FLEET_RUNTIME_HOME") or Path.home() / ".local/share/fleet/runtime").resolve()
    if not (runtime / "node_modules/@anthropic-ai/claude-agent-sdk/package.json").exists():
        raise RuntimeError(f"the Claude Agent SDK is not installed under {runtime}; pass --runtime-home")
    write_json(root / "mcp.json", {"mcpServers": {}})
    # Appended after the SDK's own arguments, so these settings sources win.
    args = ["--setting-sources", "project,local", "--strict-mcp-config", "--mcp-config", str(root / "mcp.json"), "--tools", CLAUDE_TOOLS]
    # Fleet's CLAUDE.local.md imports the card from outside the checkout, which
    # Claude skips without a per-project approval. Each launch appends the
    # checkout's current card instead, so an edited card reaches the next launch.
    cards = {physical(d): str(root / "lanes" / kind / "card.md") for d, kind in zip(directories, ROLES)}
    wrapper = root / "bin/claude"
    wrapper.write_text("#!/usr/bin/env python3\nimport os,sys\nos.environ['CLAUDE_CODE_DISABLE_AUTO_MEMORY'] = '1'\n" +
                       "card = " + repr(cards) + ".get(os.getcwd())\n" +
                       "if not card and '--version' not in sys.argv:\n    sys.exit('no role card for ' + os.getcwd())\n" +
                       "extra = ['--append-system-prompt-file', card] if card else []\n" +
                       "os.execv(" + repr(real) + ", [" + repr(real) + ", *sys.argv[1:], *" + repr(args) + ", *extra])\n")
    wrapper.chmod(0o700)
    for directory in directories:
        claude_settings(directory, root)
    return {"name": "claude", "real_cli": real, "runtime_home": str(runtime), "arguments": args, "cards": cards,
            "wrapper_sha256": sha(wrapper), "allow": claude_allow(root), "deny": CLAUDE_DENY, "file_tool_root": str(root),
            "auto_memory": "disabled", "os_sandbox": "none; Bash is not confined to the lab and its denies are prefix rules",
            "scope": "process-local CLI arguments and checkout-local settings; user settings, MCP and memory excluded; normal auth retained"}


def physical(directory):
    """The path getcwd reports inside directory: resolve() keeps a caller's letter case."""
    before = os.getcwd()
    os.chdir(directory)
    try:
        return os.getcwd()
    finally:
        os.chdir(before)


def claude_settings(checkout, root):
    """Add the lab's Claude tool policy beside the hooks and denies Fleet projected."""
    path = checkout / ".claude/settings.local.json"
    settings = json.loads(path.read_text())
    permissions = settings.setdefault("permissions", {})
    permissions["allow"] = sorted(set(permissions.get("allow") or []) | set(claude_allow(root)))
    permissions["deny"] = sorted(set(permissions.get("deny") or []) | set(CLAUDE_DENY))
    permissions["additionalDirectories"] = [str(root)]
    permissions["defaultMode"] = "dontAsk"  # deliver.json passes the same mode to the SDK
    settings["autoMemoryEnabled"] = False
    write_json(path, settings)


def rooms_backend(root, host, binary, image, toolstore):
    """Freeze a one-command Rooms entry point: rooms-run PATCH OUT. No VM is started."""
    if not shutil.which("limactl"):
        raise RuntimeError("limactl is required for the local Lima Rooms backend; no installer is run")
    adapter = root / "bin/rooms-check.py"
    shutil.copy2(HERE / "rooms-check.py", adapter)
    fixed = ["--lima", host, "--rooms", binary, "--image", image, "--toolstore", toolstore]
    cli = root / "bin/rooms-run"
    cli.write_text("#!/usr/bin/env python3\n\"\"\"Run PATCH once in a cold Rooms room, collecting into OUT: rooms-run PATCH OUT\"\"\"\n"
                   "import os,sys\nif len(sys.argv) != 3:\n    sys.exit('usage: rooms-run PATCH OUT')\n"
                   "os.execv(sys.executable, [sys.executable, " + repr(str(adapter)) + ", *" + repr(fixed) +
                   ", '--patch', sys.argv[1], '--out', sys.argv[2]])\n")
    cli.chmod(0o700)
    return {"cli": str(cli), "out": str(root / "result/rooms"), "host": host, "binary": binary, "image": image,
            "toolstore": toolstore, "adapter_sha256": sha(adapter), "cli_sha256": sha(cli),
            "scope": "executes only the frozen patch and supplied tests in a cold room; agents stay on the host"}


def provider_preflight(root, provider, directories):
    """Inspect configuration without a model turn."""
    if provider == "codex":
        return [fleet(root, "check", "supervisor:headless", check=False)]
    checks = [run([root / "bin/claude", "--version"], root, env_for(root), check=False)]
    for directory in directories:
        checks.append(fleet(root, "inspect-hooks", "--config", directory / ".claude/settings.local.json", check=False))
    return checks


def toml_value(value):
    if isinstance(value, dict):
        return "{" + ",".join(json.dumps(k) + "=" + toml_value(v) for k, v in value.items()) + "}"
    return json.dumps(value)


def prepare(destination, cards, fleet_source, provider="codex", model=None, runtime_home=None, rooms=None):
    root = Path(destination).resolve() if destination else Path(tempfile.mkdtemp(prefix="headless-workbench-")).resolve()
    if any(c in str(root) for c in "*?[]{}()\\"):
        raise RuntimeError("the lab root is used in permission globs; choose a path without *?[]{}()\\")
    if destination:
        root.mkdir()
    root = Path(physical(root))  # the letter case Fleet will record receipts under
    print(root, flush=True)
    write_json(root / "lab.json", {"example": "headless-workbench-v1", "root": str(root)})
    for name in ("bin", "state", "org", "lanes", "projection-home", "control", "result", "tmp"):
        (root / name).mkdir()
    build_source = Path(fleet_source).resolve() if fleet_source else REPO
    run(["go", "build", "-o", root / "bin/fleet", "./cmd/fleet"], build_source, timeout=900)  # a cold module cache is slow
    repo, lead, verifier = (root / n for n in ("workbench", "workbench-lead", "workbench-verifier"))
    # Only BASE's history enters the lab: the source repository's other refs
    # include this example's committed reference result.
    run(["git", "init", "--quiet", repo], root)
    run(["git", "fetch", "--quiet", "--no-tags", REPO, BASE], repo, timeout=600)
    git(repo, "checkout", "--quiet", "--detach", BASE)
    git(repo, "config", "user.name", "Headless Workbench lab")
    git(repo, "config", "user.email", "lab@example.invalid")
    # No publication endpoint. Rooms later receives a frozen base-relative patch.
    git(repo, "remote", "add", "origin", str(repo))
    task = repo / TASK
    task.mkdir(parents=True)
    shutil.copy2(HERE / "task/test_report.py", task / "test_report.py")
    shutil.copy2(HERE / "task/input.json", task / "input.json")
    shutil.copy2(HERE / "task/INPUT.md", task / "INPUT.md")
    (task / "README.md").write_text("# Frozen component check readout\n\nImplement report.py to satisfy test_report.py. Input contains actual frozen GitHub observations for Workbench component PRs; see INPUT.md. Preserve every check name (name, else context), outcome (conclusion, else state, else status, else UNKNOWN) and exact revision. Never infer merge authority. The script takes input and output file paths. Do not change supplied tests/input.\n")
    git(repo, "add", TASK)
    git(repo, "commit", "-m", "Specify frozen observation readout acceptance")
    seed = git(repo, "rev-parse", "HEAD")
    git(repo, "branch", "task/readout")
    git(repo, "worktree", "add", "--detach", str(lead), seed)
    run(["git", "clone", "--no-hardlinks", repo, verifier], root)
    git(verifier, "remote", "set-url", "origin", str(repo))
    projection = {**env_for(root), "CODEX_HOME": str(root / "projection-home")}
    sources = Path(cards).resolve() if cards else HERE / "cards"
    for kind in ROLES:
        card = sources / (kind + ".md")
        lane = root / "lanes" / kind
        lane.mkdir()
        shutil.copy2(card, lane / "card.md")
        write_json(lane / "manifest.json", {"kind": kind, "card": "card.md", "denies": [], "requires": [], "produces": None, "slots": 0})
    run([root / "bin/fleet", "pool", repo, "author", "1", "--tenant", "headless-lab"], root, projection)
    author = root / "workbench-author-1"
    for path, role in ((lead, "supervisor:headless"), (verifier, "verifier:headless")):
        run([root / "bin/fleet", "role", path, role, "--tenant", "headless-lab"], root, projection)
    for path in (repo, verifier):
        with (path / ".git/info/exclude").open("a") as f:
            f.write("\n/RUN.md\n/resolved.json\n")
    for path in (lead, author, verifier):
        (path / "RUN.md").symlink_to(root / "RUN.md")
        (path / "resolved.json").symlink_to(root / "resolved.json")
    if provider == "claude":
        provider_info = claude_wrapper(root, (lead, author, verifier), runtime_home)
    else:
        provider_info = codex_wrapper(root, (repo, lead, author, verifier))
    provider_info["model"] = model
    backend = rooms_backend(root, *rooms) if rooms else None
    resolved = {"root": str(root), "base": BASE, "seed_head": seed,
                "fleet_cli": str(root / "bin/fleet"), "fleet_source": str(build_source),
                "fleet_source_head": git(build_source, "rev-parse", "HEAD"), "fleet_binary_sha256": sha(root / "bin/fleet"),
                "fleet_source_status": git(build_source, "status", "--short"),
                "card_source": str(sources),
                "initial_card_hashes": {kind: sha(sources / (kind + ".md")) for kind in ROLES},
                "task_branch": "task/readout", "task_path": TASK,
                "test_command": "python3 -m unittest discover -s " + TASK + " -p 'test_*.py'",
                "supervisor": {"address": "supervisor:headless", "cwd": str(lead), "card": str(root / "lanes/supervisor/card.md")},
                "author": {"address": "workbench-author-1", "cwd": str(author), "card": str(root / "lanes/author/card.md")},
                "verifier": {"address": "verifier:headless", "cwd": str(verifier), "card": str(root / "lanes/verifier/card.md")},
                "provider": provider_info, "rooms": backend}
    write_json(root / "resolved.json", resolved)
    (root / "RUN.md").write_text(run_brief(root, provider, backend))
    delivery = {}
    for key in ROLES:
        target = {"cwd": resolved[key]["cwd"], "provider": provider}
        if provider == "claude":
            target.update(permission_mode="dontAsk")
        if model:
            target["model"] = model
        delivery[resolved[key]["address"]] = target
    delivery["supervisor:headless"].update(fresh=True, every="1h", prompt=f"Complete the local headless Workbench run in {root}/RUN.md. Read your role card and resolved.json, inspect existing Fleet work/mail/handoff and advance the authorized outcome. On the initial turn follow the deliberate interruption fixture; on later wakes recover existing work. Stop the addresses at completion.")
    write_json(root / "state/deliver.json", delivery)
    checks = provider_preflight(root, provider, (lead, author, verifier))
    (root / "check.stdout").write_text("".join(c.stdout for c in checks))
    (root / "check.stderr").write_text("".join(c.stderr for c in checks))
    print((root / "check.stdout").read_text())
    if any(c.returncode for c in checks):
        raise RuntimeError(f"provider preflight failed; evidence retained in {root}")


def run_brief(root, provider, backend):
    rooms = ""
    if backend:
        rooms = (f" After its local tests pass, the verifier runs the same exported patch once through `rooms_cli PATCH {backend['out']}`"
                 " (rooms.cli in resolved.json), then records a separate rooms/pass or rooms/fail receipt at the same head from"
                 " result.json, the returned patch hash and lifecycle collection/cleanup. The supervisor waits for both receipts.")
    return f"""# Useful supervision without the desktop

Lab root: {root}. Read resolved.json and your projected card. This is one authorized disposable local task: produce and independently verify a Python frozen-check readout and a patch against {BASE}. No network publication, installs, credential reads, grants, merges, or effects outside this lab; temporary files go under {root}/tmp. The {provider} backend uses existing login; MCP, desktop tools, plugins and model subagents are disabled for these processes.

Role cards are ordinary prose. The author seat is workbench-author-1; the supervisor is supervisor:headless; verifier:headless is a separate process/checkout. Fleet watches and launches all three. Use the absolute fleet_cli from resolved.json for every Fleet command; a login shell can replace PATH. The outer fixture only prepared the lab, starts/stops the watcher, deliberately interrupts the supervisor after checkpoint and dirty author work, and resumes its address. Supervisor fresh=true starts a new provider conversation on each wake, so recovery relies on Fleet records and files. It supplies no answer or restated task after interruption.

Supervisor dispatch: fleet dispatch task/readout --as implementation --for supervisor:headless --slot workbench-author-1 --brief 'Read RUN.md, resolved.json and the author card; implement the frozen observation readout through tests, a clean commit and implementation receipt.' Inspect existing rows before dispatch; recovery keeps the existing assignment. Initial supervisor writes `fleet handoff --role CONCLUSION NEXT` and control/supervisor-ready, then runs `python3 -c 'import time; time.sleep(300)'` as one foreground command with a tool timeout above five minutes, never in the background; that is the interruption fixture. Claude Code refuses a standalone shell sleep, which is why the wait is a Python process. Do not use sleep for normal peer coordination.

Author first writes task/PLAN.md with its file-writing tool and asks the supervisor about status-policy by mail; wait by ending the turn. The supervisor knows to preserve distinct observed labels, with empty checks unknown. Resume through Fleet mail; use stable IDs for retries. Every agent waits for a peer by ending its turn: Fleet wakes it when mail arrives. When the author reports a head, the supervisor exports `git -C AUTHOR diff {BASE} HEAD` to {root}/result/worker.patch and sends the verifier the head, the author's checkout and the patch path and SHA-256. The verifier fetches the author's full commit from the author's local checkout, checks out detached in its own directory, confirms the patch equals its own `git diff {BASE} HEAD`, tests and independently compares input/report, and emits verify/pass or fail at the exact clean head. Keep outputs/caches outside its checkout.{rooms}

Completion: supervisor checks real implementation and independent receipts at the exact head, writes result/ASSESSMENT.md with that head, the patch hash, the evidence and what remains unproven, checkpoints, then stops all three addresses. Keep report/input/test source and finished commit on retries. No result is approval of any PR. Rooms executes only the frozen patch and tests; it is never proof that agents ran in a VM.
"""


def lab_root(value):
    root = Path(value).resolve()
    marker = json.loads((root / "lab.json").read_text())
    if marker != {"example": "headless-workbench-v1", "root": str(root)}:
        raise RuntimeError("root does not name this example's original disposable lab")
    for name in ("state", "org", "lanes", "projection-home", "bin", "control", "result", "workbench-lead"):
        if not (root / name).resolve().is_relative_to(root):
            raise RuntimeError("lab path resolves outside its disposable root")
    return root


def card_projection(root, apply=False):
    info = json.loads((root / "resolved.json").read_text())
    status = json.loads(fleet(root, "watch", "status", "--json").stdout)
    if apply and (status["watcher"] not in ("stopped", "never_seen") or not exits_collected(root)):
        raise RuntimeError("stop the lab and resolve outstanding attempts before projecting cards")
    bindings = {row[0]: row for line in (root / "org/roles.map").read_text().splitlines()
                if (row := line.split()) and not row[0].startswith("#")}
    for kind in ROLES:
        target = info[kind]
        source = Path(target["card"])
        checkout, tenant, role, *slot = bindings[target["cwd"]]
        if not source.resolve().is_relative_to(root) or not Path(checkout).resolve().is_relative_to(root):
            raise RuntimeError("card source or checkout resolves outside the disposable lab")
        if not (Path(checkout) / ".codex").resolve().is_relative_to(root):
            raise RuntimeError("card projection resolves outside the disposable lab")
        config = Path(checkout) / ".codex/config.toml"
        for local in (".codex/config.toml", ".codex/rules/fleet-role.rules", ".claude/settings.local.json", "CLAUDE.local.md"):
            if not (Path(checkout) / local).resolve().is_relative_to(root):
                raise RuntimeError("a Fleet projection file resolves outside the disposable lab")
        before = tomllib.loads(config.read_text()).get("developer_instructions", "")
        expected = f"# Session role: {role}\n\n{source.read_text().strip()}\n"
        print(json.dumps({"kind": kind, "source": str(source), "source_sha256": sha(source),
                          "imported_from": info.get("card_source"), "initial_sha256": info.get("initial_card_hashes", {}).get(kind),
                          "role": role, "slot": slot or None, "address": target["address"],
                          "cwd": checkout, "provider": info.get("provider", {}).get("name", "codex"), "projection_matches": before == expected,
                          "claude_launch": "appends this card at each launch" if info.get("provider", {}).get("cards") else None,
                          "running_session_uptake": "not established; no restart or new turn requested"}))
        print("".join(difflib.unified_diff(before.splitlines(True), expected.splitlines(True),
                                         fromfile=str(config), tofile=str(source))), end="")
        if apply and before != expected:
            projection = {**env_for(root), "CODEX_HOME": str(root / "projection-home")}
            run([root / "bin/fleet", "role", checkout, role, "--tenant", tenant], root, projection)
            after = tomllib.loads(config.read_text())["developer_instructions"]
            if after != expected:
                raise RuntimeError("Fleet returned but the expected card projection is absent")
            print("projected; conversation uptake remains unverified")


def states(root):
    records = []
    for path in (root / "state/watch/delivery").glob("*.meta.json"):
        meta = json.loads(path.read_text())
        attempt = str(path).removesuffix(".meta.json")
        if meta.get("attempt") != attempt or meta.get("state_file") != attempt + ".state.json":
            raise RuntimeError("attempt metadata identity is unavailable or mismatched")
        state = json.loads(Path(attempt + ".state.json").read_text())
        if state.get("attempt") != attempt or state.get("provider") != meta.get("provider"):
            raise RuntimeError("provider state belongs to a different attempt")
        records.append(state)
    return records


def exits_collected(root):
    attempts = list((root / "state/watch/delivery").glob("*.meta.json"))
    for path in attempts:
        exit_path = path.with_name(path.name.replace(".meta.json", ".exit.json"))
        if not exit_path.exists():
            return False
        record = json.loads(exit_path.read_text())
        if record.get("what") != "delivery-exited" or type(record.get("exit_code")) is not int:
            return False
    return True


def operate(root, resume=False):
    info = json.loads((root / "resolved.json").read_text())
    if resume:
        status = json.loads(fleet(root, "watch", "status", "--json").stdout)
        if status["watcher"] not in ("stopped", "never_seen") or not exits_collected(root) or any(not s.get("provider_terminal") for s in states(root)):
            raise RuntimeError("resume requires a stopped watcher and collected terminal attempts")
        archive = root / "control/before-resume"
        archive.mkdir()  # This bounded example permits one continuation; retain the first run.
        for path in (root / "status.json", root / "control/cleanup.json", root / "control/run-error.json"):
            if path.exists():
                shutil.copy2(path, archive / path.name)
        for kind in ROLES:
            fleet(root, "resume", "address:" + info[kind]["address"])
        (root / "control/stop-requested").unlink(missing_ok=True)
    log = (root / ("watcher-resume.log" if resume else "watcher.log")).open("x")
    prefix = "resume" if resume else "run"
    write_json(root / ("control/" + prefix + "-inputs.json"), {"at": time.time(), "runner_sha256": sha(Path(__file__)),
               "cards": {kind: sha(Path(info[kind]["card"])) for kind in ROLES},
               "delivery_sha256": sha(root / "state/deliver.json"), "brief_sha256": sha(root / "RUN.md")})
    watcher = subprocess.Popen([root / "bin/fleet", "watch", "--interval", "1s"], cwd=root / "workbench-lead", env=env_for(root), stdout=log, stderr=subprocess.STDOUT)
    write_json(root / "control/watcher.json", {"pid": watcher.pid, "started_at": time.time()})
    phase, started, interruption = ("resumed" if resume else "inflight"), time.time(), None
    try:
        while time.time() - started < 1200:
            if (root / "control/stop-requested").exists():
                print("Operator stop requested; collecting exits.", flush=True)
                break
            if watcher.poll() is not None:
                raise RuntimeError("watcher stopped unexpectedly")
            draft = root / "workbench-author-1" / TASK / "PLAN.md"
            if phase == "inflight" and (root / "control/supervisor-ready").exists() and not draft.exists():
                fixture_alive(root)
            if phase == "inflight" and (root / "control/supervisor-ready").exists() and draft.exists():
                observed = json.loads(fleet(root, "watch", "status", "--json").stdout)
                supervisor = next(w for w in observed["workers"] if w["address"] == "supervisor:headless")
                interruption = {"requested_at": time.time(), "draft_sha256": sha(draft), "draft_mtime_ns": draft.stat().st_mtime_ns,
                                "attempt": supervisor["attempt"], "provider_session": supervisor["provider_session"],
                                "assignments": {p.name: {"sha256": sha(p), "mtime_ns": p.stat().st_mtime_ns} for p in (root / "state/assign").glob("*.json")}}
                fleet(root, "stop", "address:supervisor:headless", "controlled interruption of this disposable supervisor")
                fleet(root, "watch", "cancel", "supervisor:headless")
                write_json(root / "control/interruption.json", interruption)
                phase = "cancelled"
                print("Requested actual provider interruption; original author draft retained.", flush=True)
            if phase == "cancelled":
                # Exit first: the bridge's last state write precedes its exit, so a
                # state read after a collected exit is final.
                exited = Path(interruption["attempt"] + ".exit.json").exists()
                current = states(root)
                interrupted = [s for s in current if s.get("attempt") == interruption["attempt"] and s.get("provider_state") == "interrupted" and s.get("provider_terminal")]
                if exited:
                    ended_otherwise(current, interruption["attempt"])
                if interrupted and exited:
                    interruption["terminal"] = interrupted
                    interruption["resumed_at"] = time.time()
                    write_json(root / "control/interruption.json", interruption)
                    fleet(root, "resume", "address:supervisor:headless")
                    phase = "resumed"
                    print("Resumed the address without supplying task facts; Fleet owns continuation.", flush=True)
            if (root / "result/ASSESSMENT.md").exists() and exits_collected(root):
                result = audit(root)
                write_json(root / "result/audit.json", result)
                if result["status"] != "pass":
                    raise RuntimeError("artifact audit failed; inspect result/audit.json")
                print("Artifact audit passed; independent receipts and actual results retained.", flush=True)
                break
            time.sleep(1)
        else:
            raise RuntimeError("bounded run timed out; inspect retained evidence")
    except Exception as error:
        write_json(root / ("control/" + prefix + "-error.json"), {"at": time.time(), "error": str(error)})
        raise
    finally:
        try:
            stop(root)
        except Exception as error:
            write_json(root / "control/collection-error.json", {"at": time.time(), "error": str(error)})
            raise
        finally:
            try:
                if watcher.poll() is None:
                    watcher.send_signal(signal.SIGINT)
                try:
                    watcher.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    watcher.terminate()
                    watcher.wait(timeout=5)
            finally:
                log.close()
        print(root, flush=True)


def ended_otherwise(current, attempt):
    """Judge a cancelled supervisor whose bridge has exited: only an interruption counts.

    The bridge can exit after a runtime error without marking the turn terminal,
    so any final state other than an interrupted terminal one fails.
    """
    for state in current:
        if state.get("attempt") != attempt:
            continue
        if state.get("provider_terminal") and state.get("provider_state") == "interrupted":
            return
        raise RuntimeError(f"the cancelled supervisor's bridge exited with state {state.get('provider_state')!r}, not interrupted")


def fixture_alive(root):
    """An interruption fixture that already ended cannot demonstrate recovery."""
    observed = json.loads(fleet(root, "watch", "status", "--json").stdout)
    supervisor = next((w for w in observed["workers"] if w["address"] == "supervisor:headless"), {})
    if supervisor.get("provider_terminal"):
        raise RuntimeError("the supervisor's interruption fixture ended before the author draft existed; nothing was interrupted")


def stop(root, requested=False):
    if requested:
        (root / "control/stop-requested").touch(exist_ok=True)
    info = json.loads((root / "resolved.json").read_text())
    commands = []
    for key in ROLES:
        address = info[key]["address"]
        for args in (("stop", "address:" + address, "bounded local run ended"), ("watch", "cancel", address)):
            result = fleet(root, *args, check=False)
            commands.append({"args": args, "exit": result.returncode, "stdout": result.stdout, "stderr": result.stderr})
    deadline = time.time() + 15
    while time.time() < deadline and not exits_collected(root):
        time.sleep(0.5)
    (root / "status.json").write_text(fleet(root, "watch", "status", "--json").stdout)
    write_json(root / "control/cleanup.json", {"at": time.time(), "commands": commands,
               "bridge_exits_collected": exits_collected(root),
               "provider_terminal": all(s.get("provider_terminal") for s in states(root)),
               "descendant_quiescence": "not proven"})
    if not exits_collected(root):
        print("Some bridge exits remain unknown; retain the lab and inspect status.", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("prepare", "run", "resume", "status", "stop", "cards", "update"))
    parser.add_argument("root", nargs="?")
    parser.add_argument("--cards", help="source directory containing supervisor.md, author.md and verifier.md")
    parser.add_argument("--fleet-source", help="local Fleet source checkout to build; defaults to this repository")
    parser.add_argument("--provider", choices=PROVIDERS, default="codex", help="provider for all three agents")
    parser.add_argument("--model", help="provider model for all three agents; the provider default otherwise")
    parser.add_argument("--runtime-home", help="directory whose node_modules holds the Claude Agent SDK (FLEET_RUNTIME_HOME)")
    parser.add_argument("--rooms", nargs=4, metavar=("LIMA_HOST", "ROOMS", "IMAGE", "TOOLSTORE"),
                        help="let the verifier run the patch in a cold room on this Lima host; paths are inside the host")
    args = parser.parse_args()
    if args.action == "prepare":
        prepare(args.root, args.cards, args.fleet_source, args.provider, args.model, args.runtime_home, args.rooms)
        return
    if not args.root:
        parser.error("root is required")
    root = lab_root(args.root)
    if args.action in ("cards", "update"):
        card_projection(root, apply=args.action == "update")
        return
    if args.action in ("run", "resume"):
        operate(root, resume=args.action == "resume")
        return
    if args.action == "stop":
        stop(root, requested=True)
        return
    print(fleet(root, "status", "--all", "--json").stdout)


if __name__ == "__main__":
    main()
