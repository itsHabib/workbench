"""Acceptance for a frozen check readout; the author implements report.py."""
import copy
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

HERE = Path(__file__).resolve().parent


class ReadoutTest(unittest.TestCase):
    def run_report(self, value):
        with tempfile.TemporaryDirectory() as tmp:
            source, output = Path(tmp) / "input.json", Path(tmp) / "report.json"
            source.write_text(json.dumps(value))
            result = subprocess.run([sys.executable, str(HERE / "report.py"), str(source), str(output)], capture_output=True, text=True)
            return result, json.loads(output.read_text()) if output.exists() else None

    def fixture(self):
        return {"observed_at": "2026-09-14T00:00:00Z", "pull_requests": [
            {"number": 7, "headRefOid": "a" * 40, "statusCheckRollup": [
                {"name": "test", "status": "COMPLETED", "conclusion": "SUCCESS"},
                {"name": "review", "status": "COMPLETED", "conclusion": "SKIPPED"},
                {"name": "scan", "status": "IN_PROGRESS", "conclusion": ""},
                {"name": "other", "status": "COMPLETED", "conclusion": "NEUTRAL"}]},
            {"number": 8, "headRefOid": "b" * 40, "statusCheckRollup": []}]}

    def test_keeps_observed_states_and_unknowns(self):
        result, report = self.run_report(self.fixture())
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report, {"observed_at": "2026-09-14T00:00:00Z", "pull_requests": [
            {"number": 7, "head": "a" * 40, "checks": [
                {"name": "test", "outcome": "SUCCESS"}, {"name": "review", "outcome": "SKIPPED"},
                {"name": "scan", "outcome": "IN_PROGRESS"}, {"name": "other", "outcome": "NEUTRAL"}],
             "all_checks_successful": False},
            {"number": 8, "head": "b" * 40, "checks": [], "all_checks_successful": None}]})

    def test_success_is_only_about_observed_checks(self):
        value = self.fixture()
        value["pull_requests"][0]["statusCheckRollup"] = [{"name": "test", "conclusion": "SUCCESS", "status": "COMPLETED"}]
        result, report = self.run_report(value)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(report["pull_requests"][0]["all_checks_successful"])
        self.assertNotIn("merge_ready", json.dumps(report))

    def test_absent_conclusion_is_not_success(self):
        value = self.fixture()
        value["pull_requests"][0]["statusCheckRollup"] = [{"name": "missing"}]
        result, report = self.run_report(value)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report["pull_requests"][0]["checks"], [{"name": "missing", "outcome": "UNKNOWN"}])
        self.assertFalse(report["pull_requests"][0]["all_checks_successful"])

    def test_native_github_status_context_is_preserved(self):
        value = self.fixture()
        value["pull_requests"][0]["statusCheckRollup"] = [
            {"__typename": "StatusContext", "context": "gate/authorized", "state": "SUCCESS"},
            {"__typename": "StatusContext", "context": "external-review", "state": "PENDING"}]
        result, report = self.run_report(value)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report["pull_requests"][0]["checks"], [
            {"name": "gate/authorized", "outcome": "SUCCESS"},
            {"name": "external-review", "outcome": "PENDING"}])
        self.assertFalse(report["pull_requests"][0]["all_checks_successful"])

    def test_duplicate_pr_rejected_without_output(self):
        value = self.fixture()
        value["pull_requests"].append(copy.deepcopy(value["pull_requests"][0]))
        result, report = self.run_report(value)
        self.assertNotEqual(result.returncode, 0)
        self.assertIsNone(report)

    def test_invalid_head_rejected_without_output(self):
        value = self.fixture()
        value["pull_requests"][0]["headRefOid"] = "not-a-revision"
        result, report = self.run_report(value)
        self.assertNotEqual(result.returncode, 0)
        self.assertIsNone(report)

    def test_repeating_same_input_preserves_output(self):
        with tempfile.TemporaryDirectory() as tmp:
            source, output = Path(tmp) / "input.json", Path(tmp) / "report.json"
            source.write_text(json.dumps(self.fixture()))
            args = [sys.executable, str(HERE / "report.py"), str(source), str(output)]
            subprocess.run(args, check=True, capture_output=True)
            before = (output.read_bytes(), output.stat().st_mtime_ns)
            subprocess.run(args, check=True, capture_output=True)
            self.assertEqual((output.read_bytes(), output.stat().st_mtime_ns), before)

    def test_conflicting_output_is_preserved(self):
        with tempfile.TemporaryDirectory() as tmp:
            source, output = Path(tmp) / "input.json", Path(tmp) / "report.json"
            source.write_text(json.dumps(self.fixture()))
            output.write_text("operator-owned output\n")
            before = (output.read_bytes(), output.stat().st_mtime_ns)
            result = subprocess.run([sys.executable, str(HERE / "report.py"), str(source), str(output)], capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual((output.read_bytes(), output.stat().st_mtime_ns), before)


if __name__ == "__main__":
    unittest.main()
