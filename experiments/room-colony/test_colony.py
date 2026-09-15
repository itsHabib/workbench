import json
import fcntl
from pathlib import Path
import tempfile
import subprocess
import sys
import threading
import unittest
from unittest.mock import patch

from broker import Handler, Store, ThreadingHTTPServer, write
from lab import audit, plan, require_collected, source_hash, stop, verify_backend
from workload import BASELINE, assess, digest, distance, maps
from worker import Mail, author, verifier


class OracleTests(unittest.TestCase):
    def test_baseline_is_valid_but_not_improved(self):
        result = assess(BASELINE, "heldout")
        self.assertTrue(result["valid"])
        self.assertEqual(result["ratio"], 1)
        self.assertFalse(result["qualified"])

    def test_bad_routes_and_boolean_indices_rejected(self):
        for route in ([0, 0], [False, True], [0], [0, 2]):
            with self.assertRaises(ValueError):
                distance([[0, 0], [1, 1]], route)

    def test_compile_failure_and_missing_deliveries(self):
        for source in ("def broken", "def plan(points): return []"):
            self.assertFalse(assess(source, "development")["valid"])

    def test_heldout_maps_are_separate(self):
        self.assertFalse(any(m in maps("development")[3:] for m in maps("heldout")[3:]))


class StateTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        for name in ("artifacts", "attempts", "model"):
            (self.root / name).mkdir()
        write(self.root / "desired.json", {"source_sha256": source_hash()})
        write(self.root / "broker.json", {"pid": 123})

    def tearDown(self):
        self.tmp.cleanup()

    def test_immutable_replay_and_conflict(self):
        store = Store(self.root)
        store.put("selected", {"sha256": "abc"})
        store.put("selected", {"sha256": "abc"})
        with self.assertRaises(ValueError):
            store.put("selected", {"sha256": "different"})

    def test_model_call_ambiguous_completion_is_not_repeated(self):
        path = self.root / "model/1"
        path.mkdir()
        write(path / "request.json", {"source": "same"})
        with self.assertRaisesRegex(ValueError, "incomplete"):
            Store(self.root).generate("1", {"source": "same"})

    def test_live_worker_kept_then_missing_worker_replaced(self):
        attempt = self.root / "attempts/author-1"
        attempt.mkdir()
        write(attempt / "process.json", {"pid": 456})
        with patch("lab.alive", return_value=True):
            self.assertEqual(plan(self.root)[0]["action"], "keep")
        with patch("lab.alive", side_effect=lambda pid, _: pid == 123):
            self.assertEqual(plan(self.root)[0]["action"], "blocked")
            write(attempt / "terminal.json", {"exit": 137, "collection_exit": 0})
            (attempt / "collected").mkdir()
            (attempt / "collected/lifecycle.ndjson").write_text('\n'.join(json.dumps({"event": e}) for e in ("collection_done", "cleanup_done")))
            item = plan(self.root)[0]
            self.assertEqual((item["action"], item["attempt"]), ("start", 2))

    def test_unrecorded_launch_blocks_replacement(self):
        (self.root / "attempts/author-1").mkdir()
        with patch("lab.alive", return_value=True):
            self.assertEqual(plan(self.root)[0]["action"], "blocked")

    def test_stop_waits_for_apply_lock(self):
        entered = threading.Event()
        with (self.root / "apply.lock").open("a") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            with patch("lab.stop_collected", side_effect=lambda _: entered.set()):
                thread = threading.Thread(target=stop, args=(self.root,))
                thread.start()
                try:
                    self.assertFalse(entered.wait(.1))
                finally:
                    fcntl.flock(lock, fcntl.LOCK_UN)
                    thread.join(timeout=3)
                self.assertTrue(entered.is_set())
                self.assertFalse(thread.is_alive())

    def test_cleanup_preserves_uncollected_attempt(self):
        (self.root / "attempts/author-1").mkdir()
        with self.assertRaisesRegex(ValueError, "preserve remote"):
            require_collected(self.root)

    def test_backend_drift_refused(self):
        write(self.root / "desired.json", {"lima": "test"})
        write(self.root / "backend.json", {"resources": ["binary"], "sha256": ["old"]})
        with patch("lab.subprocess.check_output", return_value="new  binary\n"):
            with self.assertRaisesRegex(ValueError, "backend changed"):
                verify_backend(self.root)

    def test_archived_audit_needs_no_live_broker(self):
        (self.root / "broker.json").unlink()
        write(self.root / "desired.json", {"source_sha256": "historical"})
        result = audit(self.root)
        self.assertFalse(result["passed"])
        self.assertFalse(result["current_source_matches"])

    def test_worse_candidates_never_replace_baseline(self):
        from unittest.mock import Mock
        mail = Mock()
        mail.get.side_effect = [{"source": "first", "sha256": "one"}, {"source": "second", "sha256": "two"}]
        mail.wait.side_effect = [{"valid": True, "ratio": 1.2, "sha256": "one"},
                                {"valid": True, "ratio": 1.1, "sha256": "two"},
                                {"sha256": digest(BASELINE), "qualified": False}]
        author(mail)
        self.assertEqual(mail.put.call_args_list[0].args,
                         ("selected", {"source": BASELINE, "sha256": digest(BASELINE)}))

    def test_completion_requires_process_receipt(self):
        write(self.root / "artifacts/author-done.json", {"selected": "abc"})
        with patch("lab.alive", return_value=True):
            self.assertEqual(plan(self.root)[0]["action"], "blocked")

    def test_changed_source_refused(self):
        write(self.root / "desired.json", {"source_sha256": "different"})
        with self.assertRaisesRegex(ValueError, "source changed"):
            plan(self.root)

    def test_real_process_interruption_and_http_handoff(self):
        source = """def plan(points):
    pending = set(range(len(points)))
    route, here = [], [0, 0]
    while pending:
        i = min(pending, key=lambda i: (abs(points[i][0]-here[0])+abs(points[i][1]-here[1]), i))
        pending.remove(i)
        route.append(i)
        here = points[i]
    return route
"""
        calls = []
        store = Store(self.root)
        def generate(number, value):
            calls.append(number)
            return {"source": source, "sha256": digest(source), "round": int(number)}
        store.generate = generate
        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        server.store, server.token = store, "test-only"
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        url = f"http://127.0.0.1:{server.server_port}"
        failures = []
        def verify():
            try:
                verifier(Mail(url, "test-only"))
            except Exception as error:
                failures.append(error)
        peer = threading.Thread(target=verify, daemon=True)
        peer.start()
        command = [sys.executable, "-c", "from worker import Mail,author; import sys; author(Mail(sys.argv[1], 'test-only'))", url]
        try:
            first = subprocess.run(command, cwd=Path(__file__).parent, timeout=20, capture_output=True)
            self.assertEqual(first.returncode, -9, first.stderr.decode())
            self.assertIsNotNone(store.get("candidate-1"))
            second = subprocess.run(command, cwd=Path(__file__).parent, timeout=20, capture_output=True)
            self.assertEqual(second.returncode, 0, second.stderr.decode())
            peer.join(timeout=5)
            self.assertFalse(peer.is_alive())
            self.assertFalse(failures)
            self.assertEqual(calls, ["1", "2"])
            self.assertTrue(store.get("promotion")["promoted"])
            self.assertEqual(store.get("qualification")["sha256"], digest(source))
        finally:
            server.shutdown()
            server.server_close()


if __name__ == "__main__":
    unittest.main()
