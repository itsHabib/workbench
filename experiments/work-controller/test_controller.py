"""Behavioral tests for the local lifecycle, not agent-routing quality."""
import concurrent.futures
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import unittest

import controller


class ControllerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.db = str(Path(self.temp.name) / 'jobs.sqlite')
        self.call('init')
        self.call('register', 'alice', 'implementation')
        self.call('register', 'bob', 'integration')
        self.call('submit', 'one', 'implement feature')

    def call(self, *args):
        return controller.execute(controller.parser().parse_args(['--db', self.db, *map(str, args)]))

    def assign(self, job='one', worker='alice'):
        return self.call('decide', job, '--worker', worker, '--reason', 'owns context')

    def claim(self, job='one', worker='alice'):
        return self.call('claim', job, '--worker', worker)

    def complete(self, token, result='artifact at revision abc'):
        return self.call('complete', 'one', '--worker', 'alice', '--token', token, '--result', result)

    def test_replay_conflicts_and_restart(self):
        self.assertEqual(self.call('submit', 'one', 'implement feature')['state'], 'queued')
        self.call('register', 'alice', 'implementation')
        with self.assertRaises(controller.Rejected):
            self.call('submit', 'one', 'different work')
        with self.assertRaises(controller.Rejected):
            self.call('register', 'alice', 'different context')
        self.assign()
        token = self.claim()['token']
        output = subprocess.check_output([sys.executable, str(Path(controller.__file__)), '--db', self.db, 'snapshot'])
        self.assertEqual(json.loads(output)['jobs'][0]['token'], token)
        self.assertEqual(len(self.call('snapshot')['jobs']), 1)

    def test_current_result_acceptance_and_replay(self):
        self.assign()
        token = self.claim()['token']
        with self.assertRaises(controller.Rejected):
            self.call('accept', 'one', '--token', token, '--evidence', 'looks fine')
        self.complete(token)
        self.complete(token)
        self.assertEqual(len(self.call('outbox')), 2)
        with self.assertRaises(controller.Rejected):
            self.complete(token, 'different result')
        args = ('accept', 'one', '--token', token, '--evidence', 'integration test at revision abc')
        self.assertEqual(self.call(*args)['state'], 'accepted')
        self.call(*args)
        self.complete(token)
        with self.assertRaises(controller.Rejected):
            self.call('reassign', 'one', '--worker', 'bob', '--reason', 'too late')

    def test_stale_completion_after_reassignment(self):
        self.assign()
        old = self.claim()['token']
        self.call('reassign', 'one', '--worker', 'bob', '--reason', 'operator replaced worker')
        with self.assertRaises(controller.Rejected):
            self.complete(old)
        new = self.claim(worker='bob')['token']
        self.assertGreater(new, old)
        with self.assertRaises(controller.Rejected):
            self.call('accept', 'one', '--token', old, '--evidence', 'obsolete')
        self.assertEqual(self.call('snapshot')['jobs'][0]['token'], new)

    def test_reported_result_invalidated(self):
        self.assign()
        old = self.claim()['token']
        self.complete(old)
        job = self.call('reassign', 'one', '--worker', 'alice', '--reason', 'result incomplete')
        self.assertIsNone(job['result'])
        with self.assertRaises(controller.Rejected):
            self.call('accept', 'one', '--token', old, '--evidence', 'obsolete')
        self.assertGreater(self.claim()['token'], old)

    def test_busy_worker_can_queue_but_not_run_second_job(self):
        self.assign()
        self.claim()
        self.call('submit', 'two', 'follow-up')
        self.assign('two')
        before = self.call('snapshot')
        with self.assertRaises(controller.Rejected):
            self.claim('two')
        self.assertEqual(before, self.call('snapshot'))
        self.complete(before['jobs'][0]['token'])
        self.assertEqual(self.claim('two')['state'], 'running')

    def race(self, calls):
        barrier = threading.Barrier(len(calls))
        def run(args):
            barrier.wait()
            try:
                return self.call(*args)
            except controller.Rejected:
                return None
        with concurrent.futures.ThreadPoolExecutor(max_workers=len(calls)) as pool:
            return list(pool.map(run, calls))

    def test_concurrent_duplicate_claim_one_winner(self):
        self.assign()
        results = self.race([('claim', 'one', '--worker', 'alice')] * 6)
        self.assertEqual(sum(result is not None for result in results), 1)

    def test_concurrent_different_workers_cannot_steal(self):
        self.assign()
        results = self.race([('claim', 'one', '--worker', 'alice'), ('claim', 'one', '--worker', 'bob')])
        self.assertEqual(sum(result is not None for result in results), 1)
        self.assertEqual(self.call('snapshot')['jobs'][0]['worker'], 'alice')

    def test_concurrent_jobs_one_worker_one_winner(self):
        self.assign()
        self.call('submit', 'two', 'another job')
        self.assign('two')
        results = self.race([('claim', job, '--worker', 'alice') for job in ('one', 'two')])
        self.assertEqual(sum(result is not None for result in results), 1)
        self.assertEqual(sorted(job['state'] for job in self.call('snapshot')['jobs']), ['assigned', 'running'])

    def test_spawn_outbox_durable_and_ack_idempotent(self):
        self.call('decide', 'one', '--worker', 'carol', '--reason', 'independent work', '--spawn')
        pending = self.call('outbox')
        self.assertEqual([item['kind'] for item in pending], ['spawn', 'assignment'])
        self.assertEqual(pending[0]['payload']['context'], 'implement feature')
        self.assertEqual(self.call('outbox'), pending)
        self.call('delivered', pending[0]['id'])
        self.call('delivered', pending[0]['id'])
        self.assertEqual(len(self.call('outbox')), 1)
        with self.assertRaises(controller.Rejected):
            self.call('delivered', 999)

    def test_failed_routing_has_no_events_or_decisions(self):
        before = self.call('snapshot')
        with self.assertRaises(controller.Rejected):
            self.call('decide', 'one', '--worker', 'alice', '--reason', 'duplicate spawn', '--spawn')
        self.assertEqual(self.call('snapshot'), before)
        self.assertEqual(self.call('outbox'), [])


if __name__ == '__main__':
    unittest.main()
