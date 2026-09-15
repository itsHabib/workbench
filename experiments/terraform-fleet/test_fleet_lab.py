import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

import fleet_lab as adapter


class ApplyTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name).resolve() / "lab"
        self.config = {
            "root": str(self.root), "fleet_source": "/source", "fleet_revision": "abc",
            "provider": "codex", "rooms": None,
            "cards": {role: role + " instructions" for role in adapter.ROLES},
        }
        self.lab = Mock()
        self.lab.lab_root.return_value = self.root
        self.lab.fleet.return_value.stdout = '{"watcher":"stopped"}'
        self.lab.exits_collected.return_value = True
        self.root_patch = patch.object(adapter, "ROOT", self.root)
        self.loader_patch = patch.object(adapter, "load_lab", return_value=self.lab)
        self.root_patch.start()
        self.loader_patch.start()
        self.addCleanup(self.root_patch.stop)
        self.addCleanup(self.loader_patch.stop)

    def existing(self):
        self.root.mkdir()
        (self.root / "terraform-input.json").write_text(json.dumps(self.config))
        for role in adapter.ROLES:
            lane = self.root / "lanes" / role
            lane.mkdir(parents=True)
            (lane / "card.md").write_text("original")

    def test_update_projects_cards_without_launch(self):
        self.existing()
        adapter.apply(self.config)
        self.assertEqual((self.root / "lanes/author/card.md").read_text(), "author instructions")
        self.lab.card_projection.assert_called_once_with(self.root, apply=True)
        self.lab.operate.assert_not_called()
        self.lab.prepare.assert_not_called()

    def test_live_watcher_refused_before_write(self):
        self.existing()
        self.lab.fleet.return_value.stdout = '{"watcher":"running"}'
        with self.assertRaisesRegex(RuntimeError, "stop Fleet"):
            adapter.apply(self.config)
        self.assertEqual((self.root / "lanes/author/card.md").read_text(), "original")

    def test_uncollected_exit_refused(self):
        self.existing()
        self.lab.exits_collected.return_value = False
        with self.assertRaisesRegex(RuntimeError, "collect"):
            adapter.apply(self.config)

    def test_runtime_change_refused(self):
        self.existing()
        changed = copy.deepcopy(self.config)
        changed["provider"] = "claude"
        with self.assertRaisesRegex(RuntimeError, "only card edits"):
            adapter.apply(changed)

    def test_partial_lab_not_adopted(self):
        self.root.mkdir()
        with self.assertRaises(FileNotFoundError):
            adapter.apply(self.config)
        self.lab.prepare.assert_not_called()

    def test_missing_role_refused(self):
        del self.config["cards"]["author"]
        with self.assertRaisesRegex(RuntimeError, "requires"):
            adapter.apply(self.config)

    def test_projection_failure_does_not_record_success(self):
        self.existing()
        prior = (self.root / "terraform-input.json").read_text()
        self.config["cards"]["author"] = "changed"
        self.lab.card_projection.side_effect = RuntimeError("projection failed")
        with self.assertRaisesRegex(RuntimeError, "projection failed"):
            adapter.apply(self.config)
        self.assertEqual((self.root / "terraform-input.json").read_text(), prior)

    def test_prepare_receives_planned_cards_and_retains_inputs(self):
        def prepare(root, cards, source, provider, rooms):
            self.root.mkdir()
            (self.root / "resolved.json").write_text("{}")
            self.assertEqual((Path(cards) / "author.md").read_text(), "author instructions")
            self.assertEqual(source, "/source")
            self.assertEqual(provider, "codex")
            self.assertIsNone(rooms)
        self.lab.prepare.side_effect = prepare
        adapter.apply(self.config)
        self.assertEqual(json.loads((self.root / "terraform-input.json").read_text()), self.config)
        self.assertEqual((self.root / "imported-cards/author.md").read_text(), "author instructions")
        self.lab.operate.assert_not_called()

    def test_redirected_card_refused_before_any_write(self):
        self.existing()
        card = self.root / "lanes/verifier/card.md"
        card.unlink()
        card.symlink_to(Path(self.tmp.name) / "outside.md")
        with self.assertRaisesRegex(RuntimeError, "redirected card"):
            adapter.apply(self.config)
        self.assertEqual((self.root / "lanes/supervisor/card.md").read_text(), "original")


if __name__ == "__main__":
    unittest.main()
