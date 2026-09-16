import tempfile
from pathlib import Path
import unittest
from unittest.mock import patch

from broker import write
from host import ReplayStore, copy_key, events, observe, runtime_home, directory, private_file


class HostRecoveryTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        for name in ('artifacts', 'attempts', 'model'):
            (self.root / name).mkdir()

    def tearDown(self):
        self.tmp.cleanup()

    def attempt(self, boot, pid):
        path = self.root / 'attempts/author-1'
        path.mkdir()
        write(path / 'launch.json', {'boot': boot, 'pid': pid})

    def test_dead_wrapper_same_boot_does_not_prove_guest_dead(self):
        self.attempt('current', 42)
        with patch('host.alive', return_value=False):
            self.assertEqual(observe(self.root, 'current')[0]['action'], 'blocked')

    def test_previous_boot_pid_reuse_does_not_keep_old_worker(self):
        self.attempt('previous', 42)
        with patch('host.alive', return_value=True):
            action = observe(self.root, 'current')[0]
            self.assertEqual((action['action'], action['attempt']), ('start', 2))
        self.assertFalse((self.root / 'attempts/author-1/terminal.json').exists())

    def test_current_worker_is_kept(self):
        self.attempt('current', 42)
        with patch('host.alive', return_value=True):
            self.assertEqual(observe(self.root, 'current')[0]['action'], 'keep')

    def test_missing_launch_record_is_ambiguous_even_after_reboot(self):
        (self.root / 'attempts/author-1').mkdir()
        self.assertEqual(observe(self.root, 'current')[0]['action'], 'blocked')

    def test_replay_never_falls_back_to_inference(self):
        with patch('subprocess.run') as run:
            with self.assertRaisesRegex(ValueError, 'inference is disabled'):
                ReplayStore(self.root).generate('1', {})
            run.assert_not_called()

    def test_replay_refuses_changed_request(self):
        path = self.root / 'model/1'
        path.mkdir()
        write(path / 'request.json', {'source': 'old'})
        write(path / 'reply.json', {'source': 'generated', 'sha256': 'hash'})
        with self.assertRaisesRegex(ValueError, 'differs'):
            ReplayStore(self.root).generate('1', {'source': 'new'})

    def test_private_key_permissions_do_not_depend_on_umask(self):
        source, target = self.root / 'source', self.root / 'target'
        source.write_text('fixture key')
        target.write_text('old key')
        target.chmod(0o644)
        copy_key(source, target)
        self.assertEqual(target.stat().st_mode & 0o777, 0o600)

    def test_long_socket_path_rejected_before_launch(self):
        with self.assertRaisesRegex(ValueError, 'path too long'):
            runtime_home(Path('/very-long-state-directory') / ('x' * 100), '12345678-full-boot')

    def test_power_cut_partial_event_is_not_cleanup(self):
        path = self.root / 'attempts/author-1'
        path.mkdir()
        log = path / 'lifecycle.ndjson'
        contents = '{"event":"collection_done"}\n{"event":"cleanup_'
        log.write_text(contents)
        self.assertEqual(events(path), ['collection_done'])
        self.assertEqual(log.read_text(), contents)

    def test_done_before_collection_restarts_after_reboot(self):
        self.attempt('previous', 42)
        write(self.root / 'artifacts/author-done.json', {'selected': 'hash'})
        self.assertEqual(observe(self.root, 'current')[0]['action'], 'start')

    def test_clean_old_completion_does_not_need_relaunch(self):
        self.attempt('previous', 42)
        attempt = self.root / 'attempts/author-1'
        write(attempt / 'terminal.json', {'exit': 0})
        (attempt / 'lifecycle.ndjson').write_text('{"event":"collection_done"}\n{"event":"cleanup_done"}\n')
        write(self.root / 'artifacts/author-done.json', {'selected': 'hash'})
        self.assertEqual(observe(self.root, 'current')[0]['action'], 'complete')

    def test_cleanup_without_collection_is_not_complete(self):
        self.attempt('current', 42)
        attempt = self.root / 'attempts/author-1'
        write(attempt / 'terminal.json', {'exit': 0})
        (attempt / 'lifecycle.ndjson').write_text('{"event":"cleanup_done"}\n')
        write(self.root / 'artifacts/author-done.json', {'selected': 'hash'})
        with patch('host.alive', return_value=False):
            self.assertEqual(observe(self.root, 'current')[0]['action'], 'blocked')

    def test_nested_state_and_credentials_sync_directory_entries(self):
        with patch('host.sync_directory') as sync:
            directory(self.root / 'a/b')
            private_file(b'fixture', self.root / 'a/b/token')
            self.assertEqual([c.args[0] for c in sync.call_args_list],
                             [self.root, self.root / 'a', self.root / 'a/b'])
        self.assertEqual((self.root / 'a/b/token').read_bytes(), b'fixture')


if __name__ == '__main__':
    unittest.main()
