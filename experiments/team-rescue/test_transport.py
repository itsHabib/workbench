from __future__ import annotations

import json
import os
from pathlib import Path
import tempfile
import textwrap
import threading
import time
import unittest
from unittest import mock

import transport


SCHEMA = {
    "type": "object",
    "properties": {"answer": {"type": "string"}},
    "required": ["answer"],
    "additionalProperties": False,
}


class TransportTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary_directory = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary_directory.name)
        self.fake_codex = self.root / "codex"
        self.fake_codex.write_text(
            textwrap.dedent(
                '''\
                #!/usr/bin/env python3
                import json
                from pathlib import Path
                import signal
                import subprocess
                import sys
                import time

                args = sys.argv[1:]
                response = Path(args[args.index("-o") + 1])
                prompt = sys.stdin.read()

                if "tool-event" in prompt:
                    item = {"id": "item-1", "type": "command_execution"}
                    print(json.dumps({"type": "item.started", "item": item}))
                    print(json.dumps({"type": "item.completed", "item": item}))
                    response.write_text(json.dumps({"answer": "observed"}))
                    raise SystemExit(0)

                if "success" in prompt:
                    print(json.dumps({"type": "thread.started", "thread_id": "fake"}))
                    print(json.dumps({"type": "turn.completed", "usage": {
                        "input_tokens": 12,
                        "cached_input_tokens": 3,
                        "output_tokens": 4,
                    }}))
                    response.write_text(json.dumps({"answer": "ok"}))
                    raise SystemExit(0)

                if "invalid" in prompt:
                    response.write_text("not json")
                    raise SystemExit(0)

                if "nonzero" in prompt:
                    response.write_text(json.dumps({"answer": "not accepted"}))
                    raise SystemExit(7)

                marker = response.parent / "child-terminated"
                ready = response.parent / "child-ready"
                child_code = (
                    "import pathlib, signal, sys, time\\n"
                    "marker = pathlib.Path(sys.argv[1])\\n"
                    "ready = pathlib.Path(sys.argv[2])\\n"
                    "mode = sys.argv[3]\\n"
                    "def stop(signum, frame):\\n"
                    "    marker.write_text(str(signum))\\n"
                    "    raise SystemExit(0)\\n"
                    "signal.signal(signal.SIGTERM, "
                    "signal.SIG_IGN if mode == 'stubborn' else stop)\\n"
                    "ready.write_text('ready')\\n"
                    "while True:\\n"
                    "    time.sleep(1)\\n"
                )
                child = subprocess.Popen(
                    [
                        sys.executable,
                        "-c",
                        child_code,
                        str(marker),
                        str(ready),
                        "stubborn" if "stubborn" in prompt else "graceful",
                    ]
                )
                def stop_parent(signum, frame):
                    child.wait(timeout=1)
                    raise SystemExit(0)
                signal.signal(signal.SIGTERM, stop_parent)
                while not ready.exists():
                    time.sleep(0.01)
                (response.parent / "child.pid").write_text(str(child.pid))
                while True:
                    time.sleep(1)
                '''
            ),
            encoding="utf-8",
        )
        self.fake_codex.chmod(0o755)

    def tearDown(self) -> None:
        self.temporary_directory.cleanup()

    def complete(self, prompt: str, output_name: str, **overrides):
        arguments = {
            "model": "test-model",
            "schema": SCHEMA,
            "cwd": self.root,
            "output_dir": self.root / output_name,
            "timeout": 2.0,
            "stop_file": self.root / "stop",
            "executable": self.fake_codex,
        }
        arguments.update(overrides)
        return transport.complete(prompt, **arguments)

    def test_successful_structured_response_and_usage(self) -> None:
        result = self.complete("success", "success-output")

        self.assertEqual(result["response"], {"answer": "ok"})
        self.assertEqual(
            result["usage"],
            {"input_tokens": 12, "cached_input_tokens": 3, "output_tokens": 4},
        )
        self.assertIsNone(result["error"])
        self.assertEqual(result["returncode"], 0)
        self.assertEqual(
            result["enforcement"], {"max_output_tokens": "unsupported_by_codex_cli"}
        )
        self.assertIn("--sandbox", result["command"])
        self.assertIn("read-only", result["command"])
        self.assertNotIn("--dangerously-bypass-approvals-and-sandbox", result["command"])
        self.assertTrue(Path(result["events_file"]).is_file())
        self.assertTrue(Path(result["stderr_file"]).is_file())
        self.assertEqual(result["tool_execution_count"], 0)

    def test_records_observed_tool_execution_once(self) -> None:
        result = self.complete("tool-event", "tool-output")

        self.assertEqual(result["response"], {"answer": "observed"})
        self.assertEqual(result["tool_execution_count"], 1)

    def test_preexisting_stop_file_does_not_launch_codex(self) -> None:
        stop_file = self.root / "preexisting-stop"
        stop_file.write_text("stop\n", encoding="utf-8")

        result = self.complete(
            "success",
            "preexisting-stop-output",
            stop_file=stop_file,
            executable=self.root / "does-not-exist",
        )

        self.assertTrue(result["stopped"])
        self.assertEqual(result["error"], "stopped")
        self.assertIsNone(result["returncode"])

    def test_owner_mismatch_does_not_launch_codex(self) -> None:
        result = self.complete(
            "success",
            "owner-lost-output",
            owner_pid=os.getppid() + 1,
            executable=self.root / "does-not-exist",
        )

        self.assertTrue(result["owner_lost"])
        self.assertEqual(result["error"], "owner_lost")
        self.assertIsNone(result["returncode"])

    def test_owner_loss_terminates_running_codex(self) -> None:
        with mock.patch.object(transport.os, "getppid", side_effect=[123, 456]):
            result = self.complete(
                "hang", "owner-lost-running-output", owner_pid=123
            )

        self.assertTrue(result["owner_lost"])
        self.assertEqual(result["error"], "owner_lost")
        self.assertIsNotNone(result["returncode"])

    def test_invalid_json(self) -> None:
        result = self.complete("invalid", "invalid-output")

        self.assertIsNone(result["response"])
        self.assertIsNone(result["usage"])
        self.assertEqual(result["returncode"], 0)
        self.assertIn("invalid codex response", result["error"])

    def test_nonzero_exit(self) -> None:
        result = self.complete("nonzero", "nonzero-output")

        self.assertIsNone(result["response"])
        self.assertEqual(result["returncode"], 7)
        self.assertEqual(result["error"], "codex exited with status 7")

    def test_timeout_terminates_child_process_group(self) -> None:
        output = self.root / "timeout-output"
        result = self.complete("hang", output.name, timeout=0.4)

        self.assertTrue(result["timed_out"])
        self.assertEqual(result["error"], "timeout")
        self.assertIsNotNone(result["returncode"])
        self.assert_child_received_sigterm(output)

    def test_stop_file_terminates_child_process_group(self) -> None:
        output = self.root / "stop-output"
        stop_file = self.root / "stop-later"
        timer = threading.Timer(0.4, stop_file.write_text, args=("stop\n",))
        timer.start()
        result = self.complete("stubborn hang", output.name, stop_file=stop_file)
        timer.join()

        self.assertTrue(result["stopped"])
        self.assertEqual(result["error"], "stopped")
        self.assertIsNotNone(result["returncode"])
        self.assertFalse((output / "child-terminated").exists())
        self.assert_child_dead(output)

    def assert_child_received_sigterm(self, output: Path) -> None:
        marker = output / "child-terminated"
        deadline = time.monotonic() + 1.0
        while time.monotonic() < deadline and not marker.exists():
            time.sleep(0.01)
        self.assertTrue(marker.exists(), "child did not receive the process-group signal")
        self.assertEqual(marker.read_text(encoding="utf-8"), str(signal_number("SIGTERM")))
        self.assert_child_dead(output)

    def assert_child_dead(self, output: Path) -> None:
        child_pid = int((output / "child.pid").read_text(encoding="utf-8"))
        with self.assertRaises(ProcessLookupError):
            os.kill(child_pid, 0)


def signal_number(name: str) -> int:
    import signal

    return int(getattr(signal, name))


if __name__ == "__main__":
    unittest.main()
