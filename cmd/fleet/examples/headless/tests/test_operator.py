"""Offline operator regressions. No provider, watcher, network or installed config."""
import contextlib
import io
import json
from pathlib import Path
import shutil
import sys
import tempfile
import tomllib
import unittest
from unittest.mock import patch

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

    def test_uncertain_watcher_does_not_project(self):
        lab.write_json(self.root / "resolved.json", {})
        result = type("Result", (), {"stdout": '{"watcher":"stale"}'})()
        with patch.object(lab, "fleet", return_value=result), self.assertRaises(RuntimeError):
            lab.card_projection(self.root, apply=True)

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
