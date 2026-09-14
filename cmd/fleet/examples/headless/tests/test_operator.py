"""Offline operator regressions. No provider, watcher, network or installed config."""
import contextlib
import io
import json
from pathlib import Path
import shutil
import signal
import sys
import tempfile
import tomllib
import unittest
from unittest.mock import Mock, patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import lab


class OperatorTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.build = tempfile.TemporaryDirectory(prefix="headless-cli-")
        cls.binary = Path(cls.build.name) / "fleet"
        lab.run(["go", "build", "-o", cls.binary, "./cmd/fleet"], lab.REPO)

    @classmethod
    def tearDownClass(cls):
        cls.build.cleanup()

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="headless-operator-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        for name in ("state/watch/delivery", "org", "lanes", "projection-home", "bin"):
            (self.root / name).mkdir(parents=True)
        shutil.copy2(self.binary, self.root / "bin/fleet")
        lab.write_json(self.root / "lab.json", {"example": "headless-workbench-v1", "root": str(self.root)})

    def test_foreign_root_is_refused(self):
        self.assertEqual(lab.lab_root(self.root), self.root)
        lab.write_json(self.root / "lab.json", {"example": "headless-workbench-v1", "root": "/another-run"})
        with self.assertRaises(RuntimeError):
            lab.lab_root(self.root)

    def test_exit_requires_collected_exit_code(self):
        path = self.root / "state/watch/delivery/attempt.meta.json"
        path.write_text("{}")
        self.assertFalse(lab.exits_collected(self.root))
        path = path.with_name("attempt.exit.json")
        path.write_text('{}')
        self.assertFalse(lab.exits_collected(self.root))
        lab.write_json(path, {"what": "delivery-exited", "exit_code": 130})
        self.assertTrue(lab.exits_collected(self.root))

    def test_missing_or_mismatched_provider_state_is_unknown(self):
        attempt = str(self.root / "state/watch/delivery/attempt")
        lab.write_json(Path(attempt + ".meta.json"), {"attempt": attempt, "provider": "codex", "state_file": attempt + ".state.json"})
        with self.assertRaises(FileNotFoundError):
            lab.states(self.root)
        lab.write_json(Path(attempt + ".state.json"), {"attempt": "another", "provider": "codex", "provider_terminal": True})
        with self.assertRaises(RuntimeError):
            lab.states(self.root)

    def test_uncertain_watcher_does_not_project(self):
        lab.write_json(self.root / "resolved.json", {})
        result = type("Result", (), {"stdout": '{"watcher":"stale"}'})()
        with patch.object(lab, "fleet", return_value=result), self.assertRaises(RuntimeError):
            lab.card_projection(self.root, apply=True)

    def test_collection_error_still_stops_watcher_and_closes_log(self):
        (self.root / "control").mkdir()
        (self.root / "control/stop-requested").touch()
        (self.root / "card.md").write_text("fixture")
        (self.root / "RUN.md").write_text("fixture")
        lab.write_json(self.root / "state/deliver.json", {})
        lab.write_json(self.root / "resolved.json", {kind: {"card": str(self.root / "card.md")} for kind in lab.ROLES})
        watcher = Mock(pid=123, poll=Mock(return_value=None))
        with patch.object(lab.subprocess, "Popen", return_value=watcher) as start, patch.object(lab, "stop", side_effect=RuntimeError("status unavailable")), contextlib.redirect_stdout(io.StringIO()), self.assertRaisesRegex(RuntimeError, "status unavailable"):
            lab.operate(self.root)
        watcher.send_signal.assert_called_once_with(signal.SIGINT)
        watcher.wait.assert_called_once_with(timeout=15)
        self.assertTrue(start.call_args.kwargs["stdout"].closed)
        self.assertEqual(json.loads((self.root / "control/collection-error.json").read_text())["error"], "status unavailable")

    def test_edit_once_inspect_then_project_using_real_fleet(self):
        info = {}
        for kind in lab.ROLES:
            checkout = self.root / ("workbench-lead" if kind == "supervisor" else "workbench-" + kind)
            lab.run(["git", "init", checkout], self.root)
            lane = self.root / "lanes" / kind
            lane.mkdir()
            card = lane / "card.md"
            card.write_text("Initial role purpose.\n")
            lab.write_json(lane / "manifest.json", {"kind": kind, "card": "card.md", "denies": [], "requires": [], "produces": None, "slots": 0})
            projection = {**lab.env_for(self.root), "CODEX_HOME": str(self.root / "projection-home")}
            lab.run([self.root / "bin/fleet", "role", checkout, kind + ":test", "--tenant", "headless-lab"], self.root, projection)
            info[kind] = {"cwd": str(checkout), "card": str(card), "address": kind + ":test"}
        lab.write_json(self.root / "resolved.json", info)
        target = Path(info["author"]["cwd"]) / ".codex/config.toml"
        before = target.read_bytes(), target.stat().st_mtime_ns
        Path(info["author"]["card"]).write_text("Changed purpose from the one source.\n")
        with contextlib.redirect_stdout(io.StringIO()) as output:
            lab.card_projection(self.root)
        self.assertIn('"projection_matches": false', output.getvalue())
        self.assertEqual((target.read_bytes(), target.stat().st_mtime_ns), before)
        with contextlib.redirect_stdout(io.StringIO()):
            lab.card_projection(self.root, apply=True)
        self.assertIn("Changed purpose", tomllib.loads(target.read_text())["developer_instructions"])
        projected = target.read_bytes(), target.stat().st_mtime_ns
        with contextlib.redirect_stdout(io.StringIO()):
            lab.card_projection(self.root, apply=True)
        self.assertEqual((target.read_bytes(), target.stat().st_mtime_ns), projected)


if __name__ == "__main__":
    unittest.main()
