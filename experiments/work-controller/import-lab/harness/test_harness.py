import json
import unittest

import harness


class HarnessUnitTests(unittest.TestCase):
    def test_ui_parser_recognizes_local_form(self) -> None:
        parser = harness.UISurfaceParser()
        parser.feed('<form><input type="file"><button>Import</button><script src="/app.js"></script></form>')
        self.assertTrue(parser.has_form)
        self.assertTrue(parser.has_csv_input)
        self.assertTrue(parser.has_submit)
        self.assertEqual(parser.external_assets, [])

    def test_ui_parser_reports_external_asset(self) -> None:
        parser = harness.UISurfaceParser()
        parser.feed('<link rel="stylesheet" href="https://example.com/app.css">')
        self.assertEqual(parser.external_assets, ["https://example.com/app.css"])

    def test_fifty_thousand_row_fixture_is_below_cap(self) -> None:
        csv_text, count, first, last = harness.make_csv(50_000)
        body = json.dumps({"csv": csv_text}, separators=(",", ":")).encode()
        self.assertEqual(count, 50_000)
        self.assertLess(len(body), 10 * harness.MIB)
        self.assertIn(",".join(first.values()), csv_text)
        self.assertIn(",".join(last.values()), csv_text)

    def test_recovery_fixture_is_near_but_below_cap(self) -> None:
        csv_text, count, first, last = harness.make_near_limit_csv()
        body = json.dumps(
            {"csv": csv_text, "request_id": "recovery-request"},
            separators=(",", ":"),
        ).encode()
        self.assertGreater(count, 50_000)
        self.assertGreater(len(body), 8 * harness.MIB)
        self.assertLess(len(body), 10 * harness.MIB)
        self.assertTrue(csv_text.startswith(",".join(first.keys())))
        self.assertIn(",".join(first.values()), csv_text)
        self.assertIn(",".join(last.values()), csv_text)

    def test_digest_ignores_summary_extras(self) -> None:
        base = {"id": "x", "status": "completed", "records": [], "errors": []}
        self.assertEqual(harness.digest_result(base), harness.digest_result({**base, "created_at": "later"}))


if __name__ == "__main__":
    unittest.main()
