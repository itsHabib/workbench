"""Additional CLI coverage for validation and lossless observation handling."""

import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().with_name("report.py")


def fixture():
    return {
        "observed_at": "2026-09-14T00:00:00Z",
        "pull_requests": [{
            "number": 1,
            "headRefOid": "Ab" * 20,
            "statusCheckRollup": [{"name": "test", "conclusion": "SUCCESS"}],
        }],
    }


def run_cli(source, output):
    return subprocess.run(
        [sys.executable, str(SCRIPT), str(source), str(output)],
        capture_output=True,
        text=True,
    )


class AdditionalReadoutTest(unittest.TestCase):
    def run_bytes(self, raw):
        with tempfile.TemporaryDirectory() as tmp:
            source = Path(tmp) / "input.json"
            output = Path(tmp) / "report.json"
            source.write_bytes(raw)
            result = run_cli(source, output)
            report = None
            if output.exists():
                report = json.loads(output.read_text(encoding="utf-8"))
            return result, report

    def run_value(self, value):
        return self.run_bytes(json.dumps(value).encode("utf-8"))

    def test_structure_and_field_types_are_validated(self):
        invalid = [None, [], {}, {"observed_at": "now"}]
        for field, values in (
            ("observed_at", [None, 123, "", "  "]),
            ("pull_requests", [None, {}, "prs", [None], [7]]),
        ):
            for value in values:
                candidate = fixture()
                candidate[field] = value
                invalid.append(candidate)
        for field, values in (
            ("number", [None, True, 0, -1, 1.5, "1"]),
            ("headRefOid", [None, 123, "a" * 39, "g" * 40, "a" * 40 + "\n"]),
            ("statusCheckRollup", [None, {}, "checks", [None], [[]], [{}]]),
        ):
            for value in values:
                candidate = fixture()
                candidate["pull_requests"][0][field] = value
                invalid.append(candidate)
        for field in ("name", "context", "conclusion", "state", "status"):
            candidate = fixture()
            candidate["pull_requests"][0]["statusCheckRollup"][0][field] = False
            invalid.append(candidate)

        for candidate in invalid:
            with self.subTest(value=candidate):
                result, report = self.run_value(candidate)
                self.assertNotEqual(result.returncode, 0)
                self.assertIsNone(report)
                self.assertIn("report:", result.stderr)
                self.assertNotIn("Traceback", result.stderr)

    def test_invalid_json_is_rejected_without_output(self):
        for raw in (
            b"{",
            b'{"observed_at":"one","observed_at":"two","pull_requests":[]}',
            b'{"observed_at":"now","pull_requests":[],"extra":NaN}',
            b'{"observed_at":"now","pull_requests":[],"extra":Infinity}',
            b"\xff",
        ):
            with self.subTest(raw=raw):
                result, report = self.run_bytes(raw)
                self.assertNotEqual(result.returncode, 0)
                self.assertIsNone(report)
                self.assertNotIn("Traceback", result.stderr)

    def test_fallback_order_unicode_and_repeated_names_are_preserved(self):
        value = fixture()
        value["pull_requests"][0]["statusCheckRollup"] = [
            {"name": "same", "context": "unused", "conclusion": "NEUTRAL",
             "state": "SUCCESS", "status": "COMPLETED"},
            {"name": "same", "conclusion": "SUCCESS"},
            {"name": None, "context": "external/é", "conclusion": "",
             "state": "PENDING", "status": "IN_PROGRESS"},
            {"name": "", "context": "queued", "conclusion": None,
             "state": "", "status": "QUEUED"},
            {"context": "absent", "conclusion": "", "state": None, "status": ""},
            {"name": " custom label ", "conclusion": " FUTURE_RESULT "},
        ]
        result, report = self.run_value(value)
        self.assertEqual(result.returncode, 0, result.stderr)
        pr = report["pull_requests"][0]
        self.assertEqual(pr["head"], "Ab" * 20)
        self.assertEqual(pr["checks"], [
            {"name": "same", "outcome": "NEUTRAL"},
            {"name": "same", "outcome": "SUCCESS"},
            {"name": "external/é", "outcome": "PENDING"},
            {"name": "queued", "outcome": "QUEUED"},
            {"name": "absent", "outcome": "UNKNOWN"},
            {"name": " custom label ", "outcome": " FUTURE_RESULT "},
        ])
        self.assertIs(pr["all_checks_successful"], False)

    def test_non_success_labels_never_count_as_success(self):
        for outcome in (
            "SKIPPED", "NEUTRAL", "PENDING", "IN_PROGRESS", "FAILURE",
            "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED", "success", "FUTURE_RESULT",
        ):
            with self.subTest(outcome=outcome):
                value = fixture()
                value["pull_requests"][0]["statusCheckRollup"][0]["conclusion"] = outcome
                result, report = self.run_value(value)
                self.assertEqual(result.returncode, 0, result.stderr)
                pr = report["pull_requests"][0]
                self.assertEqual(pr["checks"][0]["outcome"], outcome)
                self.assertIs(pr["all_checks_successful"], False)

    def test_empty_pull_request_list_is_preserved(self):
        value = {"observed_at": "2026-09-14T00:00:00Z", "pull_requests": []}
        result, report = self.run_value(value)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(report, value)

    def test_late_invalid_input_leaves_existing_output_untouched(self):
        value = fixture()
        value["pull_requests"].append({"number": 2, "headRefOid": "invalid"})
        with tempfile.TemporaryDirectory() as tmp:
            source = Path(tmp) / "input.json"
            output = Path(tmp) / "report.json"
            source.write_text(json.dumps(value), encoding="utf-8")
            output.write_bytes(b"owned report\n")
            before = (output.read_bytes(), output.stat().st_mtime_ns)
            result = run_cli(source, output)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("headRefOid", result.stderr)
            self.assertEqual((output.read_bytes(), output.stat().st_mtime_ns), before)

    def test_input_cannot_be_overwritten_by_using_same_output_path(self):
        with tempfile.TemporaryDirectory() as tmp:
            source = Path(tmp) / "input.json"
            source.write_text(json.dumps(fixture()), encoding="utf-8")
            before = (source.read_bytes(), source.stat().st_mtime_ns)
            result = run_cli(source, source)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("conflicting output", result.stderr)
            self.assertEqual((source.read_bytes(), source.stat().st_mtime_ns), before)

    def test_missing_input_reports_error_without_creating_output(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp) / "report.json"
            result = run_cli(Path(tmp) / "missing.json", output)
            self.assertNotEqual(result.returncode, 0)
            self.assertFalse(output.exists())
            self.assertIn("report:", result.stderr)
            self.assertNotIn("Traceback", result.stderr)


if __name__ == "__main__":
    unittest.main()
