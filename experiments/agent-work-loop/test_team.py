"""Regression checks for lifecycle ownership and preservation, without model calls."""
import fcntl
import hashlib
import json
from pathlib import Path
import tempfile
import time
import unittest
from unittest.mock import Mock, patch
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
            with patch.object(team, 'fleet', return_value='{"watcher":"stale"}') as fleet:
                with self.assertRaisesRegex(RuntimeError, 'live watcher'):
                    team.run(root)
                self.assertEqual(fleet.call_count, 1)
                self.assertEqual(fleet.call_args.args[1:], ('watch', 'status', '--json'))

    def test_failed_bridge_with_unknown_children_is_not_settled(self):
        self.assertTrue(team.cleanup_pending({'workers': [
            {'state': 'failed', 'provider_cleanup_pending': True, 'provider_terminal': False}]}))
        self.assertTrue(team.cleanup_pending({'workers': [
            {'state': 'gone_exit_unknown', 'provider_started': True, 'provider_terminal': False}]}))
        self.assertFalse(team.cleanup_pending({'workers': [
            {'state': 'exited', 'provider_terminal': True}]}))

    def test_replacement_that_loses_watcher_race_cannot_cancel_incumbent(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'run.json').write_text(json.dumps({'fleet': '/tmp/fleet', 'minutes': 1}))
            incumbent = {'watcher': 'running', 'heartbeat': {'pid': 123, 'at': 1},
                         'workers': [{'state': 'running'}]}
            replacement = Mock(pid=456, returncode=1)
            replacement.poll.return_value = 1
            with patch.object(team.subprocess, 'Popen', return_value=replacement), \
                    patch.object(team.time, 'sleep'), \
                    patch.object(team, 'fleet', return_value=json.dumps(incumbent)), \
                    patch.object(team, 'write_report', return_value={'usage': {}}), \
                    patch.object(team, 'stop') as stop:
                self.assertEqual(team.monitor(root), 1)
                stop.assert_not_called()
                replacement.terminate.assert_called_once()

    def test_done_file_allows_current_turn_to_finish(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'run.json').write_text(json.dumps({'fleet': '/tmp/fleet', 'minutes': 1, 'budget_usd': 10}))
            (root / 'DONE.md').write_text('completion claim')
            running = {'heartbeat': {'pid': 456, 'at': 101}, 'workers': [{'state': 'running'}]}
            terminal = {'heartbeat': {'pid': 456, 'at': 101},
                        'workers': [{'state': 'exited', 'provider_terminal': True}]}
            process = Mock(pid=456)
            process.poll.return_value = None
            observations = [running, terminal, terminal, terminal]
            with patch.object(team.subprocess, 'Popen', return_value=process), \
                    patch.object(team.time, 'sleep'), patch.object(team.time, 'time', return_value=100), \
                    patch.object(team, 'fleet', side_effect=[json.dumps(x) for x in observations]) as fleet, \
                    patch.object(team, 'write_report', return_value={'usage': {}}), \
                    patch.object(team, 'pause_starts') as pause, patch.object(team, 'stop'):
                self.assertEqual(team.monitor(root), 0)
                pause.assert_called_once()
                self.assertEqual(fleet.call_count, 4)

    def test_external_rejection_returns_evidence_and_retains_failed_done(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'coordinator').mkdir()
            (root / 'fleet').mkdir()
            (root / 'DONE.md').write_text('wrong completion')
            (root / 'REQUESTS.md').write_text('original requirement')
            (root / 'fleet/deliver.json').write_text(json.dumps({'coordinator:team': {'prompt': 'old'}}))
            check = root / 'acceptance'
            check.write_text('#!/usr/bin/env python3\nfrom pathlib import Path\nprint("missing documents array")\nraise SystemExit(0 if Path("passes").exists() else 1)\n')
            check.chmod(0o700)
            config = {'check': str(check), 'check_sha256': hashlib.sha256(check.read_bytes()).hexdigest()}
            with patch.object(team, 'fleet') as fleet, patch.object(team, 'command', return_value='exact-head'):
                self.assertEqual(team.check_completion(root, config, time.monotonic() + 10), 'running')
                self.assertEqual(fleet.call_count, 3)
                self.assertFalse((root / 'DONE.md').exists())
                self.assertFalse((root / 'VERIFIED.md').exists())
                self.assertEqual((root / 'checks/1-DONE.md').read_text(), 'wrong completion')
                wake = json.loads((root / 'fleet/deliver.json').read_text())['coordinator:team']
                self.assertIn('missing documents array', wake['prompt'])
                self.assertTrue(wake['fresh'])
                (root / 'coordinator/passes').touch()
                (root / 'DONE.md').write_text('repaired completion')
                self.assertEqual(team.check_completion(root, config, time.monotonic() + 10), 'verified')
                self.assertIn('exact-head', (root / 'VERIFIED.md').read_text())
                self.assertEqual(json.loads((root / 'checks/2.json').read_text())['exit_code'], 0)

    def test_changed_external_check_is_not_a_model_repair_request(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            check = root / 'acceptance'
            check.write_text('changed')
            with self.assertRaisesRegex(RuntimeError, 'changed since preparation'):
                team.check_completion(root, {'check': str(check), 'check_sha256': 'original'}, time.monotonic() + 10)


if __name__ == '__main__':
    unittest.main()
