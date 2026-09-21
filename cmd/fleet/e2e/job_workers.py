#!/usr/bin/env python3
"""Black-box Fleet watcher/job binding drill.

The SDK and artifact are deterministic synthetic fixtures. This proves watcher
and lease mechanics only; it makes no LLM productivity claim.
"""

import argparse
from datetime import datetime
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time


FAKE_SDK = r'''import fs from 'node:fs';
export function query({options}) {
  const artifact = process.env.FLEET_FAKE_ARTIFACT;
  const child = options.spawnClaudeCodeProcess({
    command: process.execPath, args: ['-e', 'setTimeout(()=>{}, 5000)'],
    cwd: options.cwd, env: process.env
  });
  let interrupted = false;
  return {
    interrupt: async () => { interrupted = true; },
    close() { child.kill('SIGTERM'); },
    async *[Symbol.asyncIterator]() {
      yield {type: 'system', subtype: 'init', session_id: 'synthetic-session'};
      fs.writeFileSync(artifact, 'partial synthetic artifact\n');
      for (let i = 0; i < 100 && !interrupted; i++)
        await new Promise(resolve => setTimeout(resolve, 50));
      if (interrupted || process.env.FLEET_FAKE_MODE === 'cancel') {
        yield {type: 'result', subtype: 'interrupted', is_error: true,
          session_id: 'synthetic-session'};
        return;
      }
      fs.writeFileSync(artifact, 'complete synthetic artifact\n');
      yield {type: 'result', subtype: 'success', is_error: false,
        session_id: 'synthetic-session'};
    }
  };
}'''


def run(binary, *args, env, check=True):
    result = subprocess.run([binary, *args], env=env, capture_output=True, text=True)
    if check and result.returncode:
        raise RuntimeError(f"fleet command failed ({result.returncode})")
    return result


def job(binary, state, operation, env, check=True, **fields):
    args = ["job", operation, "--state", str(state)]
    for key, value in fields.items():
        args.extend([f"--{key.replace('_', '-')}", str(value)])
    result = run(binary, *args, env=env, check=check)
    if not result.stdout:
        return None
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError(f"fleet job output was not JSON for {operation}") from error


def wait_for(predicate, timeout=10):
    deadline = time.time() + timeout
    while time.time() < deadline:
        value = predicate()
        if value:
            return value
        time.sleep(0.05)
    raise RuntimeError("timed out waiting for worker evidence")


def fake_runtime(home):
    package = home / "node_modules" / "@anthropic-ai" / "claude-agent-sdk"
    package.mkdir(parents=True)
    (home / "package.json").write_text('{"type":"module"}\n')
    (package / "package.json").write_text('{"type":"module","exports":"./index.mjs"}\n')
    (package / "index.mjs").write_text(FAKE_SDK)


def configure(env, fleet_state, org_state, workspace, jobs_state, job_id):
    fleet_state.mkdir(parents=True, exist_ok=True)
    org_state.mkdir(parents=True, exist_ok=True)
    (org_state / "roles.map").write_text(f"{workspace} t1 hub:lead\n")
    entry = {"cwd": str(workspace), "provider": "claude",
             "prompt": "Run the synthetic worker.",
             "jobs": {"state": str(jobs_state.resolve()), "id": job_id, "ttl_seconds": 2}}
    (fleet_state / "deliver.json").write_text(json.dumps({"hub:lead": entry}))
    env.update({"FLEET_STATE": str(fleet_state.resolve()), "ORG_STATE": str(org_state.resolve()),
                "FLEET_GITHUB": "off", "FLEET_MAIL_GRACE": "0"})


def launch(binary, env):
    return subprocess.Popen([binary, "watch", "--interval", "100ms"], env=env,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def get_job(binary, state, env, job_id):
    return job(binary, state, "get", env, id=job_id)


def running_job(binary, state, env, job_id):
    def check():
        record = get_job(binary, state, env, job_id)
        if record and record.get("state") == "running":
            return record
        return None
    return wait_for(check)


def reported_job(binary, state, env, job_id):
    def check():
        record = get_job(binary, state, env, job_id)
        if record and record.get("state") == "reported":
            return record
        return None
    return wait_for(check, timeout=12)


def observed_provider(fleet_state, job_id, predicate):
    for path in (fleet_state / "watch/delivery").glob("*.state.json"):
        state = json.loads(path.read_text())
        if state.get("job_id") == job_id and predicate(state):
            return state
    return None


def independent_accept(binary, state, env, job_id, token, artifact):
    expected = b"complete synthetic artifact\n"
    actual = artifact.read_bytes()
    if actual != expected:
        raise RuntimeError("artifact bytes differ from the deterministic expected artifact")
    digest = hashlib.sha256(actual).hexdigest()
    accepted = job(binary, state, "accept", env, id=job_id, token=token,
                   evidence=f"independent sha256 {digest}")
    if accepted.get("state") != "accepted":
        raise RuntimeError("independent acceptance did not accept artifact")


def run_drill(binary):
    checks = []
    with tempfile.TemporaryDirectory(prefix="fleet-worker-drill-") as root_text:
        root = Path(root_text)
        fleet_state, org_state = root / "fleet", root / "org"
        jobs_state, runtime = root / "jobs", root / "runtime"
        workspace, artifact = root / "workspace", root / "artifact.txt"
        workspace.mkdir()
        fake_runtime(runtime)
        env = os.environ.copy()
        env.update({"FLEET_RUNTIME_HOME": str(runtime), "FLEET_FAKE_ARTIFACT": str(artifact)})

        restart_job = "restart-job"
        job(binary, jobs_state, "submit", env, id=restart_job,
            brief="restart synthetic worker")
        configure(env, fleet_state, org_state, workspace, jobs_state, restart_job)
        watcher = launch(binary, env)
        try:
            first = running_job(binary, jobs_state, env, restart_job)
            old_attempt = first["attempts"][-1]
            wait_for(lambda: observed_provider(fleet_state, restart_job, lambda s: s.get("provider_started") and s.get("provider_state") == "running"))
            wait_for(lambda: artifact.exists() and artifact.read_bytes() == b"partial synthetic artifact\n")
            watcher.kill()
            watcher.wait(timeout=5)
            time.sleep(2.2)
            during = get_job(binary, jobs_state, env, restart_job)
            if during.get("state") != "running" or len(during["attempts"]) != 1:
                raise RuntimeError("killed watcher lost the single running job attempt")
            renewed_attempt = during["attempts"][-1]
            expiry = datetime.fromisoformat(renewed_attempt["expires_at"].replace("Z", "+00:00")).timestamp()
            if expiry <= time.time():
                raise RuntimeError("bridge did not renew its lease while watcher was down")
            watcher = launch(binary, env)
            final = reported_job(binary, jobs_state, env, restart_job)
        finally:
            if watcher.poll() is None:
                watcher.kill()
                watcher.wait(timeout=5)
        if len(final["attempts"]) != 1:
            raise RuntimeError("watcher restart created a duplicate job attempt")
        independent_accept(binary, jobs_state, env, restart_job, old_attempt["token"], artifact)
        checks.append("watcher SIGKILL/restart retained one renewed attempt and exact artifact")

        cancel_job = "cancel-job"
        job(binary, jobs_state, "submit", env, id=cancel_job,
            brief="cancel and reclaim synthetic worker")
        configure(env, fleet_state, org_state, workspace, jobs_state, cancel_job)
        env["FLEET_FAKE_MODE"] = "cancel"
        watcher = launch(binary, env)
        try:
            cancelled = running_job(binary, jobs_state, env, cancel_job)
            old_token = cancelled["attempts"][-1]["token"]
            wait_for(lambda: observed_provider(fleet_state, cancel_job, lambda s: s.get("provider_started") and s.get("provider_state") == "running"))
            run(binary, "watch", "cancel", "hub:lead", env=env)
            wait_for(lambda: observed_provider(fleet_state, cancel_job, lambda s: s.get("provider_terminal") and s.get("job_status") == "unfinished"))
            if get_job(binary, jobs_state, env, cancel_job)["state"] != "running":
                raise RuntimeError("interruption must not report or accept work")
            if artifact.read_bytes() != b"partial synthetic artifact\n":
                raise RuntimeError("cancelled worker did not preserve partial artifact")
            watcher.kill()
            watcher.wait(timeout=5)
            time.sleep(2.2)
            env["FLEET_FAKE_MODE"] = "success"
            watcher = launch(binary, env)
            second = reported_job(binary, jobs_state, env, cancel_job)
        finally:
            if watcher.poll() is None:
                watcher.kill()
                watcher.wait(timeout=5)
        if len(second["attempts"]) != 2:
            raise RuntimeError("expired cancellation did not auto-reclaim exactly once")
        late = job(binary, jobs_state, "complete", env, check=False, id=cancel_job,
                   worker="hub:lead", token=old_token, result="old-token")
        if late is not None:
            raise RuntimeError("old cancellation token completed after reclaim")
        checks.append("cancelled job stayed unaccepted, preserved partial output, and auto-reclaimed")
        return {"checks": checks, "jobs": 2, "attempts": 3,
                "llm_productivity_claim": False, "paths_redacted": True}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary", type=Path)
    parser.add_argument("receipt", nargs="?", type=Path)
    args = parser.parse_args()
    try:
        receipt = run_drill(str(args.binary.resolve()))
        receipt["ok"] = True
        code = 0
    except Exception as error:
        receipt = {"ok": False, "error": str(error), "llm_productivity_claim": False,
                   "paths_redacted": True}
        code = 1
    output = json.dumps(receipt, indent=2, sort_keys=True) + "\n"
    if args.receipt:
        args.receipt.write_text(output)
    print(output, end="")
    return code


if __name__ == "__main__":
    raise SystemExit(main())
