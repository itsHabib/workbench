#!/usr/bin/env python3
"""Black-box Fleet watcher/job worker drill.

This uses a fake Claude Agent SDK and deterministic local artifacts.  It proves
lease and watcher mechanics only; it makes no claim about LLM productivity.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time


FAKE_SDK = r'''import fs from 'node:fs';
import {spawn} from 'node:child_process';
export function query({options}) {
  const artifact = process.env.FLEET_FAKE_ARTIFACT;
  let interrupted = false;
  const child = spawn(process.execPath, ['-e', 'setTimeout(()=>{}, 5000)'],
    {stdio: 'ignore'});
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


def job(binary, state, operation, env, **fields):
    args = ["job", operation, "--state", str(state)]
    for key, value in fields.items():
        args.extend([f"--{key.replace('_', '-')}", str(value)])
    result = run(binary, *args, env=env)
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise RuntimeError(f"fleet job output was not JSON for {operation}") from error


def wait_for(predicate, timeout=8):
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


def configure(env, fleet_state, org_state, workspace):
    fleet_state.mkdir(parents=True)
    org_state.mkdir(parents=True)
    (org_state / "roles.map").write_text(f"{workspace} t1 hub:lead\n")
    (fleet_state / "deliver.json").write_text(json.dumps({"hub:lead": {
        "cwd": str(workspace), "provider": "claude", "prompt": "Run the synthetic worker."}}))
    env.update({"FLEET_STATE": str(fleet_state), "ORG_STATE": str(org_state),
                "FLEET_GITHUB": "off", "FLEET_MAIL_GRACE": "0"})


def mail(binary, fleet_state, env, identifier):
    run(binary, "send", "hub:lead", "--id", identifier, "--kind", "question",
        "--subject", "synthetic", "--body", "work", env=env)


def launch(binary, env):
    return subprocess.Popen([binary, "watch", "--interval", "100ms"], env=env,
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


def worker_state(fleet_state):
    records = list((fleet_state / "watch" / "delivery").glob("*.json"))
    for path in records:
        try:
            record = json.loads(path.read_text())
        except (OSError, json.JSONDecodeError):
            continue
        state_file = record.get("state_file")
        if state_file and Path(state_file).exists():
            try:
                return record, json.loads(Path(state_file).read_text())
            except (OSError, json.JSONDecodeError):
                pass
    return None, None


def provider_started(fleet_state):
    _, state = worker_state(fleet_state)
    return state if state and state.get("provider_started") else None


def provider_terminal(fleet_state):
    _, state = worker_state(fleet_state)
    return state if state and state.get("provider_terminal") else None


def independent_accept(binary, state, env, job_id, token, artifact):
    data = artifact.read_bytes()
    digest = hashlib.sha256(data).hexdigest()
    result = job(binary, state, "complete", env, id=job_id, worker="fixture",
                 token=token, result=digest)
    accepted = job(binary, state, "accept", env, id=job_id, token=token,
                   evidence=f"independent sha256 {digest}")
    if accepted.get("state") != "accepted":
        raise RuntimeError("independent acceptance did not accept artifact")
    return result, accepted


def run_drill(binary):
    checks = []
    with tempfile.TemporaryDirectory(prefix="fleet-worker-drill-") as root:
        root = Path(root)
        fleet_state, org_state, runtime = root / "fleet", root / "org", root / "runtime"
        workspace, artifact = root / "workspace", root / "artifact.txt"
        workspace.mkdir()
        fake_runtime(runtime)
        env = os.environ.copy()
        env["FLEET_RUNTIME_HOME"] = str(runtime)
        env["FLEET_FAKE_ARTIFACT"] = str(artifact)
        configure(env, fleet_state, org_state, workspace)
        mail(binary, fleet_state, env, "synthetic-cancel")
        submitted = job(binary, fleet_state / "jobs", "submit", env,
                        id="cancel-job", brief="cancel synthetic worker")
        claimed = job(binary, fleet_state / "jobs", "claim", env, id="cancel-job",
                      worker="fixture", key="cancel-1", ttl_seconds=2)
        watcher = launch(binary, env)
        try:
            wait_for(lambda: provider_started(fleet_state))
            run(binary, "watch", "cancel", "hub:lead", env=env)
            wait_for(lambda: provider_terminal(fleet_state))
            if not artifact.exists() or b"partial" not in artifact.read_bytes():
                raise RuntimeError("cancelled worker did not preserve partial artifact")
            checks.append("cancelled worker reached terminal state and preserved partial artifact")
        finally:
            watcher.kill()
            watcher.wait(timeout=5)

        time.sleep(2.2)
        late = run(binary, "job", "complete", "--state", str(fleet_state / "jobs"),
                   "--id", "cancel-job", "--worker", "fixture", "--token",
                   claimed["attempts"][-1]["token"], "--result", "late", env=env, check=False)
        if late.returncode == 0:
            raise RuntimeError("expired old token completed a job")
        reclaimed = job(binary, fleet_state / "jobs", "claim", env, id="cancel-job",
                        worker="fixture-2", key="cancel-2", ttl_seconds=2)
        if len(reclaimed.get("attempts", [])) != 2:
            raise RuntimeError("expired reclaim did not create exactly one second attempt")
        checks.append("expired lease rejected old completion and allowed one reclaim")

        second = job(binary, fleet_state / "jobs", "submit", env,
                     id="restart-job", brief="restart synthetic worker")
        second_claim = job(binary, fleet_state / "jobs", "claim", env, id="restart-job",
                           worker="fixture-3", key="restart-1", ttl_seconds=2)
        env["FLEET_FAKE_MODE"] = "success"
        mail(binary, fleet_state, env, "synthetic-restart")
        watcher = launch(binary, env)
        try:
            wait_for(lambda: provider_started(fleet_state))
            watcher.kill()
            watcher.wait(timeout=5)
            deadline = time.time() + 1.0
            while time.time() < deadline:
                job(binary, fleet_state / "jobs", "renew", env, id="restart-job",
                    worker="fixture-3", token=second_claim["attempts"][-1]["token"], ttl_seconds=2)
                time.sleep(0.15)
            watcher = launch(binary, env)
            wait_for(lambda: artifact.exists() and b"complete" in artifact.read_bytes(), timeout=8)
        finally:
            watcher.kill()
            watcher.wait(timeout=5)
        artifact_result, accepted = independent_accept(binary, fleet_state / "jobs", env,
                                                       "restart-job", second_claim["attempts"][-1]["token"], artifact)
        if artifact_result.get("state") != "reported" or accepted.get("state") != "accepted":
            raise RuntimeError("restart job did not report then independently accept")
        checks.append("watcher restart preserved one renewed attempt; artifact was independently accepted")
        return {"checks": checks, "jobs": 2, "attempts": 3,
                "llm_productivity_claim": False,
                "paths_redacted": True}


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
