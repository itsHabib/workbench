"""Adapter contract tests; these do not simulate or claim a Rooms execution."""
import base64
import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("rooms_check", Path(__file__).resolve().parents[1] / "rooms-check.py")
adapter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(adapter)


class RoomsAdapterTest(unittest.TestCase):
    def test_fixed_command_literal_payload_and_unknown_result(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            for name in ("rooms", "image", "toolstore.sqfs", "meta.json", "worker.patch"):
                (root / name).write_text("literal $(touch SHOULD_NOT_EXIST) `command`\n")
            argv = ["rooms-check.py", "--rooms", str(root / "rooms"), "--image", str(root / "image"),
                    "--toolstore", str(root), "--patch", str(root / "worker.patch"), "--out", str(root / "result")]
            with patch.object(sys, "argv", argv), patch.object(adapter.subprocess, "run", return_value=subprocess.CompletedProcess([], 7)) as call, contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(adapter.main(), 7)
            sent = call.call_args.args[0]
            self.assertEqual(sent[sent.index("--base-sha") + 1], adapter.BASE)
            command = sent[sent.index("--command") + 1]
            payload = command.split("printf '%s' ", 1)[1].split(" | base64", 1)[0]
            self.assertEqual(base64.b64decode(payload), (root / "worker.patch").read_bytes())
            self.assertNotIn("$(touch", command)
            summary = json.loads((root / "result/summary.json").read_text())
            self.assertEqual(summary["cli_exit"], 7)
            self.assertEqual(summary["command_status"], "unknown")
            self.assertIsNone(summary["returned_patch_sha256"])
            with patch.object(sys, "argv", argv), patch.object(adapter.subprocess, "run") as call, self.assertRaises(FileExistsError):
                adapter.main()
            call.assert_not_called()


if __name__ == "__main__":
    unittest.main()
