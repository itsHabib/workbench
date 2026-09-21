#!/usr/bin/env python3
import os
from pathlib import Path
import sys
import tempfile
import unittest

sys.path.insert(0, str(Path(__file__).parent))
import pilot


class PilotMechanicalTests(unittest.TestCase):
    def test_fixture_files_are_local_to_runner(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "team.py").write_text("# team")
            (root / "oracle.py").write_text("# oracle")
            original = pilot.HERE
            pilot.HERE = root
            try:
                self.assertEqual(pilot.fixture_file("team.py"), (root / "team.py").resolve())
                self.assertEqual(pilot.fixture_file("oracle.py"), (root / "oracle.py").resolve())
            finally:
                pilot.HERE = original

    def test_environment_inherits_runtime_home(self):
        original = os.environ.get("FLEET_RUNTIME_HOME")
        os.environ["FLEET_RUNTIME_HOME"] = "caller-runtime"
        try:
            env = pilot.pilot_environment(Path("/tmp/fleet"), Path("/tmp/org"))
            self.assertEqual(env["FLEET_RUNTIME_HOME"], "caller-runtime")
        finally:
            if original is None:
                os.environ.pop("FLEET_RUNTIME_HOME", None)
            else:
                os.environ["FLEET_RUNTIME_HOME"] = original

    def test_source_filter_ignores_only_known_noise(self):
        class Result:
            returncode = 0
            stdout = (
                " M .claude/temp/ignored\n"
                " M __pycache__/ignored.pyc\n"
                " M .claude/keep-this.md\n"
                " M src/server.py\n"
            )

        original = pilot.subprocess.run
        pilot.subprocess.run = lambda *args, **kwargs: Result()
        try:
            changed = pilot.source_edit(Path("/tmp/source"))
        finally:
            pilot.subprocess.run = original
        self.assertIn(".claude/keep-this.md", changed)
        self.assertIn("src/server.py", changed)
        self.assertNotIn("ignored", changed)


if __name__ == "__main__":
    unittest.main()
