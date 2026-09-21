"""Regression checks for lifecycle ownership and preservation, without model calls."""
import fcntl
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import team


class LifecycleTests(unittest.TestCase):
    def test_existing_permissions_and_hooks_survive_preparation(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'fleet').mkdir()
            (root / 'org').mkdir()
            original = {'permissions': {'deny': ['Bash(python3 *)']},
                        'hooks': {'PreToolUse': [{'matcher': 'Bash', 'hooks': []}]},
                        'custom': {'preserve': True}}
            for name in ('coordinator', 'worker-1', 'worker-2'):
                settings = root / name / '.claude/settings.local.json'
                settings.parent.mkdir(parents=True)
                settings.write_text(json.dumps(original))
            team.configure(root, {'model': 'haiku', 'turn_budget_usd': 1, 'fleet': '/tmp/fleet'})
            for name in ('coordinator', 'worker-1', 'worker-2'):
                actual = json.loads((root / name / '.claude/settings.local.json').read_text())
                self.assertIn('Bash(python3 *)', actual['permissions']['deny'])
                self.assertEqual(actual['hooks'], original['hooks'])
                self.assertEqual(actual['custom'], original['custom'])

    def test_second_launcher_cannot_start_or_stop_anything(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with (root / 'run.lock').open('a') as incumbent:
                fcntl.flock(incumbent, fcntl.LOCK_EX | fcntl.LOCK_NB)
                with patch.object(team, 'fleet') as fleet:
                    with self.assertRaisesRegex(RuntimeError, 'another launcher'):
                        team.run(root)
                    fleet.assert_not_called()

    def test_orphaned_watcher_is_not_cancelled_by_failed_resume(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with patch.object(team, 'fleet', return_value='{"watcher":"running"}') as fleet:
                with self.assertRaisesRegex(RuntimeError, 'live watcher'):
                    team.run(root)
                self.assertEqual(fleet.call_count, 1)
                self.assertEqual(fleet.call_args.args[1:], ('watch', 'status', '--json'))

    def test_failed_bridge_with_unknown_children_is_not_settled(self):
        self.assertTrue(team.cleanup_pending({'workers': [
            {'state': 'failed', 'provider_cleanup_pending': True, 'provider_terminal': False}]}))
        self.assertFalse(team.cleanup_pending({'workers': [
            {'state': 'exited', 'provider_terminal': True}]}))


if __name__ == '__main__':
    unittest.main()
