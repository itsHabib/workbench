import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch

import cloud
import fleet_demo


class CloudTests(unittest.TestCase):
    def test_token_is_environment_only(self):
        with patch.object(cloud, "initialize"), patch.object(cloud, "config", return_value={"operator_cidr": "192.0.2.1/32", "ssh_public_key": "public"}), patch.object(cloud.subprocess, "check_output", return_value="private-token\n"), patch.object(cloud.subprocess, "run") as process:
            cloud.terraform(["plan"])
            args, kwargs = process.call_args
            self.assertEqual(args[0], ["terraform", "plan"])
            self.assertEqual(kwargs["env"]["GOOGLE_OAUTH_ACCESS_TOKEN"], "private-token")
            self.assertNotIn("private-token", str(args))

    def test_instance_address_can_be_supplied_during_apply(self):
        with patch.dict(cloud.os.environ, {"ROOMS_HOST": "192.0.2.4"}):
            self.assertEqual(cloud.host(), "192.0.2.4")

    def test_transport_has_explicit_key_and_no_agent_forwarding(self):
        with patch.object(cloud, "host", return_value="192.0.2.4"):
            args = cloud.ssh_args()
            self.assertIn("IdentitiesOnly=yes", args)
            self.assertIn("BatchMode=yes", args)
            self.assertNotIn("-A", args)
            self.assertEqual(args[-1], "rooms@192.0.2.4")

    def test_preparation_satisfies_upstream_freeze_and_detects_change(self):
        fleet_demo.lab_module()  # Import the exact downstream audit contract.
        from audit import entry_unchanged
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve() / "lab"
            def seed(*args):
                (root / "bin").mkdir(parents=True)
                subprocess.run(["git", "init", "-q", str(root / "workbench-verifier")], check=True)
                (root / "resolved.json").write_text('{"provider": {}}')
                (root / "bin/codex").write_text("import os,sys\nos.execv('/bin/false', ['/bin/false'] + sys.argv[1:])\n")
            lab = Mock()
            lab.prepare.side_effect = seed
            lab.run_brief.return_value = "test brief"
            with patch.object(fleet_demo, "ROOT", root), patch.object(cloud, "host", return_value="192.0.2.4"), patch.object(cloud, "ssh_args", return_value=["ssh", "-o", "StrictHostKeyChecking=accept-new", "rooms@192.0.2.4"]):
                fleet_demo.prepare(lab)
            backend = json.loads((root / "resolved.json").read_text())["rooms"]
            self.assertTrue(entry_unchanged(root, backend))
            self.assertIn("guest_adapter_sha256", backend)
            self.assertIn("target_sha256", backend)
            (root / "bin/rooms-check.py").write_text("changed")
            self.assertFalse(entry_unchanged(root, backend))


if __name__ == "__main__":
    unittest.main()
