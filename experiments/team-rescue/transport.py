"""Minimal native Codex transport for the team-rescue experiment."""

from __future__ import annotations

import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile
import time
from typing import Any


_POLL_SECONDS = 0.05
_TERMINATE_GRACE_SECONDS = 0.5


def complete(
    prompt: str,
    *,
    model: str,
    schema: dict[str, Any],
    cwd: Path,
    output_dir: Path,
    timeout: float,
    stop_file: Path,
    max_output_tokens: int = 6000,
    executable: str | Path = "codex",
    owner_pid: int | None = None,
) -> dict[str, Any]:
    """Run one bounded, structured Codex turn and return its local evidence."""
    started = time.monotonic()
    output_dir = Path(output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    schema_file = output_dir / "schema.json"
    response_file = output_dir / "response.json"
    events_file = output_dir / "events.jsonl"
    stderr_file = output_dir / "stderr.txt"
    schema_file.write_text(json.dumps(schema, indent=2) + "\n", encoding="utf-8")

    command = [
        str(executable),
        "exec",
        "--json",
        "--ephemeral",
        "--skip-git-repo-check",
        "--sandbox",
        "read-only",
        "--model",
        model,
        "-c",
        'model_reasoning_effort="low"',
        "--cd",
        str(cwd),
        "--output-schema",
        str(schema_file),
        "-o",
        str(response_file),
        "-",
    ]
    result: dict[str, Any] = {
        "response": None,
        "usage": None,
        "error": None,
        "elapsed_seconds": 0.0,
        "returncode": None,
        "command": command,
        "events_file": str(events_file),
        "stderr_file": str(stderr_file),
        "response_file": str(response_file),
        "schema_file": str(schema_file),
        "max_output_tokens": max_output_tokens,
        "enforcement": {"max_output_tokens": "unsupported_by_codex_cli"},
        "stopped": False,
        "timed_out": False,
        "owner_lost": False,
        "tool_execution_count": 0,
    }
    events_file.write_bytes(b"")
    stderr_file.write_bytes(b"")
    response_file.unlink(missing_ok=True)
    if owner_pid is not None and os.getppid() != owner_pid:
        result["owner_lost"] = True
        result["error"] = "owner_lost"
        result["elapsed_seconds"] = time.monotonic() - started
        return result
    if stop_file.exists():
        result["stopped"] = True
        result["error"] = "stopped"
        result["elapsed_seconds"] = time.monotonic() - started
        return result

    worker_prompt = (
        "Return only the requested response as JSON matching the provided schema. "
        "Do not run tools or commands.\n\n" + prompt
    )

    with (
        events_file.open("wb") as events,
        stderr_file.open("wb") as stderr,
        tempfile.TemporaryFile() as prompt_input,
    ):
        prompt_input.write(worker_prompt.encode("utf-8"))
        prompt_input.seek(0)
        try:
            process = subprocess.Popen(
                command,
                cwd=cwd,
                stdin=prompt_input,
                stdout=events,
                stderr=stderr,
                start_new_session=True,
            )
        except (OSError, ValueError) as exc:
            result["error"] = f"failed to start codex: {exc}"
            result["elapsed_seconds"] = time.monotonic() - started
            return result

        while process.poll() is None:
            if owner_pid is not None and os.getppid() != owner_pid:
                result["owner_lost"] = True
                result["error"] = "owner_lost"
                _terminate_process_group(process)
                break
            if stop_file.exists():
                result["stopped"] = True
                result["error"] = "stopped"
                _terminate_process_group(process)
                break
            if time.monotonic() - started >= timeout:
                result["timed_out"] = True
                result["error"] = "timeout"
                _terminate_process_group(process)
                break
            time.sleep(_POLL_SECONDS)

        result["returncode"] = process.wait()

    result["elapsed_seconds"] = time.monotonic() - started
    result["usage"], result["tool_execution_count"] = _read_events(events_file)

    if result["error"] is not None:
        return result
    if result["returncode"] != 0:
        result["error"] = f"codex exited with status {result['returncode']}"
        return result

    try:
        response = json.loads(response_file.read_text(encoding="utf-8"))
    except FileNotFoundError:
        result["error"] = "codex did not write a response"
        return result
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        result["error"] = f"invalid codex response: {exc}"
        return result

    if not isinstance(response, dict):
        result["error"] = "invalid codex response: expected a JSON object"
        return result
    result["response"] = response
    return result


def _terminate_process_group(process: subprocess.Popen[bytes]) -> None:
    """Terminate only the process group created for this invocation."""
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        process.wait()
        return

    deadline = time.monotonic() + _TERMINATE_GRACE_SECONDS
    while time.monotonic() < deadline:
        process.poll()
        if not _process_group_exists(process.pid):
            process.wait()
            return
        time.sleep(_POLL_SECONDS)

    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    process.wait()


def _process_group_exists(process_group: int) -> bool:
    try:
        os.killpg(process_group, 0)
    except ProcessLookupError:
        return False
    return True


def _read_events(events_file: Path) -> tuple[dict[str, Any] | None, int]:
    """Extract usage and observed tool runs from native Codex events."""
    try:
        lines = events_file.read_text(encoding="utf-8").splitlines()
    except (FileNotFoundError, OSError, UnicodeError):
        return None, 0

    usage = None
    tool_item_ids: set[str] = set()
    anonymous_tool_starts = 0
    anonymous_tool_completions = 0
    tool_item_types = {"command_execution", "mcp_tool_call", "web_search"}
    for line in lines:
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        if not isinstance(event, dict):
            continue
        if event.get("type") == "turn.completed":
            candidate = event.get("usage")
            if isinstance(candidate, dict):
                usage = candidate
        if event.get("type") not in {"item.started", "item.completed"}:
            continue
        item = event.get("item")
        if not isinstance(item, dict) or item.get("type") not in tool_item_types:
            continue
        item_id = item.get("id")
        if isinstance(item_id, str):
            tool_item_ids.add(item_id)
            continue
        if event["type"] == "item.started":
            anonymous_tool_starts += 1
            continue
        anonymous_tool_completions += 1
    anonymous_tool_runs = max(anonymous_tool_starts, anonymous_tool_completions)
    return usage, len(tool_item_ids) + anonymous_tool_runs
