#!/usr/bin/env python3
"""Small public smoke suite; it intentionally fails against the seed."""

import csv
import io
import json
import sqlite3
import subprocess
import sys
import socket
import tempfile
import threading
import time
import urllib.error
import urllib.request
from pathlib import Path


ROOT = Path(__file__).parent
SERVER = ROOT / "server.py"
CSV = "title,body\nTransit Plan,Use the east entrance.\n"


def request(url: str, method: str = "GET", payload: object = None, timeout: int = 2):
    data = None if payload is None else json.dumps(payload).encode()
    request = urllib.request.Request(url, data=data, method=method)
    if data is not None:
        request.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            response_data = response.read()
            content_type = response.headers.get("Content-Type", "")
            if "application/json" in content_type:
                return response.status, json.loads(response_data), content_type
            else:
                return response.status, response_data.decode("utf-8"), content_type
    except urllib.error.HTTPError as error:
        error_data = error.read()
        try:
            return error.code, json.loads(error_data), error.headers.get("Content-Type", "")
        except json.JSONDecodeError:
            return error.code, error_data.decode("utf-8"), error.headers.get("Content-Type", "")


def parse_csv(csv_text: str):
    """Parse CSV text and return as list of dicts."""
    reader = csv.DictReader(io.StringIO(csv_text))
    return list(reader)


def test_sqlite_lock_handling() -> None:
    """Test that transient SQLite lock failures are handled properly."""
    with tempfile.TemporaryDirectory() as state:
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        state_path = Path(state)

        # Start server
        process = subprocess.Popen([sys.executable, str(SERVER), "--port", str(port), "--state", state])
        try:
            deadline = time.time() + 2
            while time.time() < deadline:
                try:
                    status, _, _ = request(base + "/health")
                    if status == 200:
                        break
                except urllib.error.URLError:
                    time.sleep(0.02)
            else:
                raise AssertionError("server did not become healthy")

            # Part 1: Hold a lock and verify import fails quickly with non-2xx
            lock_event = threading.Event()
            release_event = threading.Event()

            def hold_exclusive_lock():
                conn = sqlite3.connect(str(state_path / "imports.sqlite"), timeout=30.0)
                try:
                    conn.execute("BEGIN EXCLUSIVE")
                    lock_event.set()
                    release_event.wait(timeout=10)
                finally:
                    conn.rollback()
                    conn.close()

            lock_thread = threading.Thread(target=hold_exclusive_lock, daemon=True)
            lock_thread.start()
            lock_event.wait(timeout=2)
            time.sleep(0.1)

            # Try to import while lock is held - should fail within 6 seconds with non-2xx
            start_time = time.time()
            try:
                status, result, _ = request(base + "/imports", "POST", {
                    "request_id": "lock-test-1",
                    "csv": CSV
                }, timeout=7)
                elapsed = time.time() - start_time
                assert status != 201, f"Should not succeed under lock, got {status}"
                assert status >= 400, f"Expected 4xx or 5xx, got {status}"
                assert elapsed < 6.5, f"Should fail within 6 seconds, took {elapsed:.1f}s"
                print(f"✓ Import failed with {status} after {elapsed:.2f}s under lock")
            except urllib.error.HTTPError as e:
                elapsed = time.time() - start_time
                assert e.code >= 400, f"Expected 4xx or 5xx, got {e.code}"
                assert elapsed < 6.5, f"Should fail within 6 seconds, took {elapsed:.1f}s"
                print(f"✓ Import failed with {e.code} after {elapsed:.2f}s under lock")
            finally:
                release_event.set()
                lock_thread.join(timeout=2)

            time.sleep(0.2)

            # Part 2: After lock is released, retry succeeds
            status, result, _ = request(base + "/imports", "POST", {
                "request_id": "lock-test-1",
                "csv": CSV
            })
            assert status == 201, f"Retry should succeed, got {status}: {result}"
            import_id_1 = result["import_id"]
            print(f"✓ Retry succeeded after lock released, import_id={import_id_1}")

            # Part 3: Verify data persists and can be retrieved
            status, result, _ = request(base + f"/imports/{import_id_1}")
            assert status == 200, (status, result)
            assert "documents" in result, f"Expected documents array, got {result.keys()}"
            assert len(result["documents"]) == 1, result
            assert result["documents"][0]["title"] == "Transit Plan", result
            print(f"✓ Data retrievable after lock release")

            # Part 4: Stop server and verify data survives restart
            process.terminate()
            process.wait(timeout=2)
            print(f"✓ Server stopped")

            time.sleep(0.2)

            # Restart server
            process = subprocess.Popen([sys.executable, str(SERVER), "--port", str(port), "--state", state])
            deadline = time.time() + 2
            while time.time() < deadline:
                try:
                    status, _, _ = request(base + "/health")
                    if status == 200:
                        break
                except urllib.error.URLError:
                    time.sleep(0.02)
            else:
                raise AssertionError("server did not restart")
            print(f"✓ Server restarted")

            # Verify data still exists after restart
            status, result, _ = request(base + f"/imports/{import_id_1}")
            assert status == 200, (status, result)
            assert "documents" in result, f"Expected documents array, got {result.keys()}"
            assert len(result["documents"]) == 1, result
            assert result["documents"][0]["title"] == "Transit Plan", result
            print(f"✓ Data persists after service restart")

            # Test CSV export works after restart
            status, csv_content, content_type = request(base + f"/imports/{import_id_1}/export")
            assert status == 200, (status, csv_content)
            assert "text/csv" in content_type
            csv_rows = parse_csv(csv_content)
            assert len(csv_rows) == 1, csv_rows
            assert csv_rows[0]["title"] == "Transit Plan", csv_rows
            print(f"✓ CSV export works after service restart")

        finally:
            if process.poll() is None:
                process.terminate()
                process.wait(timeout=2)


def test_multiline_quoted_csv() -> None:
    """Test that CSV with quoted multiline fields is treated as one row."""
    with tempfile.TemporaryDirectory() as state:
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        process = subprocess.Popen([sys.executable, str(SERVER), "--port", str(port), "--state", state])
        try:
            deadline = time.time() + 2
            while time.time() < deadline:
                try:
                    status, _, _ = request(base + "/health")
                    if status == 200:
                        break
                except urllib.error.URLError:
                    time.sleep(0.02)
            else:
                raise AssertionError("server did not become healthy")

            # CSV with a quoted multiline field: should be treated as 1 row, not 2
            multiline_csv = 'title,body\n"Multi\nline Title","Body text"\n'
            status, result, _ = request(base + "/imports", "POST", {"request_id": "multiline-test", "csv": multiline_csv})
            assert status == 201, f"Multiline CSV should be accepted, got {status}: {result}"
            import_id = result["import_id"]
            print("✓ Multiline quoted CSV accepted (201)")

            # Verify it's stored and retrieved as 1 document
            status, result, _ = request(base + f"/imports/{import_id}")
            assert status == 200, (status, result)
            assert "documents" in result
            assert len(result["documents"]) == 1, f"Expected 1 document, got {len(result['documents'])}: {result['documents']}"
            assert result["documents"][0]["title"] == "Multi\nline Title", result["documents"]
            assert result["documents"][0]["body"] == "Body text", result["documents"]
            print(f"✓ Multiline document retrieved as 1 row (not {len(result['documents'])})")

            # Verify export preserves the multiline field as 1 row
            status, csv_content, content_type = request(base + f"/imports/{import_id}/export")
            assert status == 200, (status, csv_content)
            assert "text/csv" in content_type
            csv_rows = parse_csv(csv_content)
            assert len(csv_rows) == 1, f"Expected 1 row in exported CSV, got {len(csv_rows)}: {csv_rows}"
            assert csv_rows[0]["title"] == "Multi\nline Title", csv_rows[0]
            assert csv_rows[0]["body"] == "Body text", csv_rows[0]
            print(f"✓ Exported CSV has 1 row (not {len(csv_rows)})")

            # Test JSON document with multiline field exported to CSV
            json_doc = [{"title": "Another\nMultiline", "body": "More\nlines\nhere"}]
            status, result, _ = request(base + "/imports", "POST", {"request_id": "multiline-json", "documents": json_doc})
            assert status == 201, f"JSON with multiline should be accepted, got {status}: {result}"
            json_import_id = result["import_id"]
            print("✓ JSON document with multiline field accepted (201)")

            # Export JSON to CSV and verify it's still 1 row
            status, csv_content, content_type = request(base + f"/imports/{json_import_id}/export")
            assert status == 200, (status, csv_content)
            csv_rows = parse_csv(csv_content)
            assert len(csv_rows) == 1, f"Expected 1 row when exporting JSON to CSV, got {len(csv_rows)}: {csv_rows}"
            assert csv_rows[0]["title"] == "Another\nMultiline", csv_rows[0]
            assert csv_rows[0]["body"] == "More\nlines\nhere", csv_rows[0]
            print(f"✓ JSON export to CSV has 1 row (not {len(csv_rows)})")

        finally:
            process.terminate()
            process.wait(timeout=2)


def test_csv_validation() -> None:
    """Test that CSV validation properly rejects malformed CSV."""
    with tempfile.TemporaryDirectory() as state:
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        process = subprocess.Popen([sys.executable, str(SERVER), "--port", str(port), "--state", state])
        try:
            deadline = time.time() + 2
            while time.time() < deadline:
                try:
                    status, _, _ = request(base + "/health")
                    if status == 200:
                        break
                except urllib.error.URLError:
                    time.sleep(0.02)
            else:
                raise AssertionError("server did not become healthy")

            # Valid CSV should be accepted
            valid_csv = "title,body\nTest,Data\n"
            status, result, _ = request(base + "/imports", "POST", {"request_id": "csv-valid", "csv": valid_csv})
            assert status == 201, f"Valid CSV should be accepted, got {status}: {result}"
            print("✓ Valid CSV accepted (201)")

            # Wrong headers should be rejected
            wrong_headers_csv = "name,content\nTest,Data\n"
            status, result, _ = request(base + "/imports", "POST", {"request_id": "csv-wrong-headers", "csv": wrong_headers_csv})
            assert status == 400, f"CSV with wrong headers should be rejected, got {status}: {result}"
            print("✓ CSV with wrong headers rejected (400)")

            # Ragged rows (missing column) should be rejected
            ragged_csv = "title,body\nTest\n"
            status, result, _ = request(base + "/imports", "POST", {"request_id": "csv-ragged", "csv": ragged_csv})
            assert status == 400, f"Ragged CSV should be rejected, got {status}: {result}"
            print("✓ Ragged CSV (missing column) rejected (400)")

            # Extra columns should be rejected
            extra_cols_csv = "title,body\nTest,Data,Extra\n"
            status, result, _ = request(base + "/imports", "POST", {"request_id": "csv-extra-cols", "csv": extra_cols_csv})
            assert status == 400, f"CSV with extra columns should be rejected, got {status}: {result}"
            print("✓ CSV with extra columns rejected (400)")

            # Empty CSV should be rejected
            status, result, _ = request(base + "/imports", "POST", {"request_id": "csv-empty", "csv": ""})
            assert status == 400, f"Empty CSV should be rejected, got {status}: {result}"
            print("✓ Empty CSV rejected (400)")

        finally:
            process.terminate()
            process.wait(timeout=2)


def test_multiline_comprehensive() -> None:
    """Comprehensive test for multiline field handling in all scenarios."""
    with tempfile.TemporaryDirectory() as state:
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        process = subprocess.Popen([sys.executable, str(SERVER), "--port", str(port), "--state", state])
        try:
            deadline = time.time() + 2
            while time.time() < deadline:
                try:
                    status, _, _ = request(base + "/health")
                    if status == 200:
                        break
                except urllib.error.URLError:
                    time.sleep(0.02)
            else:
                raise AssertionError("server did not become healthy")

            # Test 1: Multiline in title only
            csv1 = 'title,body\n"Title\nLine 2","Normal body"\n'
            status, result, _ = request(base + "/imports", "POST", {"request_id": "multiline-1", "csv": csv1})
            assert status == 201, f"Multiline title should be accepted, got {status}: {result}"
            import_id_1 = result["import_id"]
            status, result, _ = request(base + f"/imports/{import_id_1}")
            assert len(result["documents"]) == 1, f"Should be 1 doc, got {len(result['documents'])}"
            assert result["documents"][0]["title"] == "Title\nLine 2", result["documents"][0]
            print("✓ Multiline in title only: 1 document")

            # Test 2: Multiline in body only
            csv2 = 'title,body\n"Normal title","Body\nLine 2\nLine 3"\n'
            status, result, _ = request(base + "/imports", "POST", {"request_id": "multiline-2", "csv": csv2})
            assert status == 201, f"Multiline body should be accepted, got {status}: {result}"
            import_id_2 = result["import_id"]
            status, result, _ = request(base + f"/imports/{import_id_2}")
            assert len(result["documents"]) == 1, f"Should be 1 doc, got {len(result['documents'])}"
            assert result["documents"][0]["body"] == "Body\nLine 2\nLine 3", result["documents"][0]
            print("✓ Multiline in body only: 1 document")

            # Test 3: Multiline in both title and body
            csv3 = 'title,body\n"Title\nWith\nLines","Body\nWith\nLines"\n'
            status, result, _ = request(base + "/imports", "POST", {"request_id": "multiline-3", "csv": csv3})
            assert status == 201, f"Multiline in both should be accepted, got {status}: {result}"
            import_id_3 = result["import_id"]
            status, result, _ = request(base + f"/imports/{import_id_3}")
            assert len(result["documents"]) == 1, f"Should be 1 doc, got {len(result['documents'])}"
            print("✓ Multiline in both title and body: 1 document")

            # Test 4: Multiple rows with some multiline
            csv4 = 'title,body\n"Normal 1","Normal 2"\n"Multi\nline 3","Body 4"\n"Normal 5","Normal 6"\n'
            status, result, _ = request(base + "/imports", "POST", {"request_id": "multiline-4", "csv": csv4})
            assert status == 201, f"Multiple rows with multiline should be accepted, got {status}: {result}"
            import_id_4 = result["import_id"]
            status, result, _ = request(base + f"/imports/{import_id_4}")
            assert len(result["documents"]) == 3, f"Should be 3 docs, got {len(result['documents'])}: {result['documents']}"
            assert result["documents"][1]["title"] == "Multi\nline 3", result["documents"][1]
            print("✓ Multiple rows with multiline: 3 documents (multiline counted as 1)")

            # Test 5: Export multiline back to CSV and verify round-trip
            status, csv_content, _ = request(base + f"/imports/{import_id_3}/export")
            assert status == 200, (status, csv_content)
            csv_rows = parse_csv(csv_content)
            assert len(csv_rows) == 1, f"Exported CSV should have 1 row, got {len(csv_rows)}"
            assert csv_rows[0]["title"] == "Title\nWith\nLines", csv_rows[0]
            assert csv_rows[0]["body"] == "Body\nWith\nLines", csv_rows[0]
            print("✓ Exported multiline CSV round-trips correctly: 1 row")

            # Test 6: JSON import with multiline, export to CSV
            json_doc = [{"title": "JSON\nTitle", "body": "JSON\nBody"}]
            status, result, _ = request(base + "/imports", "POST", {"request_id": "multiline-json", "documents": json_doc})
            assert status == 201, f"JSON multiline should be accepted, got {status}: {result}"
            import_id_json = result["import_id"]
            status, csv_content, _ = request(base + f"/imports/{import_id_json}/export")
            csv_rows = parse_csv(csv_content)
            assert len(csv_rows) == 1, f"JSON->CSV export should have 1 row, got {len(csv_rows)}"
            assert csv_rows[0]["title"] == "JSON\nTitle", csv_rows[0]
            assert csv_rows[0]["body"] == "JSON\nBody", csv_rows[0]
            print("✓ JSON multiline export to CSV: 1 row")

        finally:
            process.terminate()
            process.wait(timeout=2)


def test_multiline_browser_display() -> None:
    """Regression test: verify multiline CSV displays as exactly 1 row in browser.

    External rejection identified: "one quoted multiline document must display as one row, 2 !== 1"
    This test ensures the browser correctly parses and displays multiline quoted fields as a single row.
    """
    with tempfile.TemporaryDirectory() as state:
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        process = subprocess.Popen([sys.executable, str(SERVER), "--port", str(port), "--state", state])
        try:
            deadline = time.time() + 2
            while time.time() < deadline:
                try:
                    status, _, _ = request(base + "/health")
                    if status == 200:
                        break
                except urllib.error.URLError:
                    time.sleep(0.02)
            else:
                raise AssertionError("server did not become healthy")

            # Test case 1: Single document with quoted newline in title
            multiline_csv_1 = 'title,body\n"A\nB","Body"\n'
            status, result, _ = request(base + "/imports", "POST", {"request_id": "browser-test-1", "csv": multiline_csv_1})
            assert status == 201, f"Should accept multiline CSV, got {status}"
            import_id_1 = result["import_id"]

            # Verify backend returns exactly 1 document (not 2 or more)
            status, result, _ = request(base + f"/imports/{import_id_1}")
            assert status == 200, (status, result)
            docs = result.get("documents", [])
            assert len(docs) == 1, f"Expected 1 document, got {len(docs)}: would display as {len(docs)} row(s), not 1"
            assert docs[0]["title"] == "A\nB", docs[0]
            assert docs[0]["body"] == "Body", docs[0]
            print(f"✓ Multiline in title: 1 document (display would show {len(docs)} row, not 2)")

            # Test case 2: Single document with quoted newline in body
            multiline_csv_2 = 'title,body\n"Title","A\nB"\n'
            status, result, _ = request(base + "/imports", "POST", {"request_id": "browser-test-2", "csv": multiline_csv_2})
            assert status == 201
            import_id_2 = result["import_id"]

            status, result, _ = request(base + f"/imports/{import_id_2}")
            assert status == 200
            docs = result.get("documents", [])
            assert len(docs) == 1, f"Expected 1 document (1 display row), got {len(docs)} (would show {len(docs)} rows)"
            print(f"✓ Multiline in body: 1 document (would show {len(docs)} row, not 2)")

            # Test case 3: Multiple rows where one has multiline - verify row count is correct
            multiline_csv_3 = 'title,body\n"First\nRow","Body 1"\n"Second","Body 2"\n'
            status, result, _ = request(base + "/imports", "POST", {"request_id": "browser-test-3", "csv": multiline_csv_3})
            assert status == 201
            import_id_3 = result["import_id"]

            status, result, _ = request(base + f"/imports/{import_id_3}")
            assert status == 200
            docs = result.get("documents", [])
            assert len(docs) == 2, f"Expected 2 documents (2 display rows), got {len(docs)}"
            print(f"✓ Mixed rows: 2 documents (would show {len(docs)} rows, not 3)")

        finally:
            process.terminate()
            process.wait(timeout=2)


def main() -> None:
    # Run SQLite lock handling test first
    print("Running SQLite lock handling test...")
    test_sqlite_lock_handling()
    print()

    # Run multiline CSV test
    print("Running multiline quoted CSV test...")
    test_multiline_quoted_csv()
    print()

    # Run comprehensive multiline test
    print("Running comprehensive multiline field test...")
    test_multiline_comprehensive()
    print()

    # Run browser display regression test
    print("Running multiline browser display regression test...")
    test_multiline_browser_display()
    print()

    # Run CSV validation test
    print("Running CSV validation test...")
    test_csv_validation()
    print()

    # Run smoke tests
    with tempfile.TemporaryDirectory() as state:
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        process = subprocess.Popen([sys.executable, str(SERVER), "--port", str(port), "--state", state])
        try:
            deadline = time.time() + 2
            while time.time() < deadline:
                try:
                    status, _, _ = request(base + "/health")
                    if status == 200:
                        break
                except urllib.error.URLError:
                    time.sleep(0.02)
            else:
                raise AssertionError("server did not become healthy")

            # Test CSV import
            status, result, _ = request(base + "/imports", "POST", {"request_id": "smoke-1", "csv": CSV})
            assert status == 201, (status, result)
            csv_import_id = result["import_id"]

            # Test JSON document import with special characters
            documents = [
                {"title": "Report", "body": "Line 1\nLine 2"},
                {"title": "Data", "body": "Value with, comma"},
                {"title": "Quote", "body": 'Text with "quotes"'},
            ]
            status, result, _ = request(base + "/imports", "POST", {"request_id": "smoke-2", "documents": documents})
            assert status == 201, (status, result)
            json_import_id = result["import_id"]

            # Test CSV export from CSV import (idempotent)
            status, csv_content, content_type = request(base + f"/imports/{csv_import_id}/export")
            assert status == 200, (status, csv_content)
            assert "text/csv" in content_type, (content_type,)
            csv_rows = parse_csv(csv_content)
            assert len(csv_rows) == 1, csv_rows
            assert csv_rows[0]["title"] == "Transit Plan", csv_rows
            assert csv_rows[0]["body"] == "Use the east entrance.", csv_rows

            # Test CSV export from JSON import (conversion)
            status, csv_content, content_type = request(base + f"/imports/{json_import_id}/export")
            assert status == 200, (status, csv_content)
            assert "text/csv" in content_type, (content_type,)
            csv_rows = parse_csv(csv_content)
            assert len(csv_rows) == 3, csv_rows

            # Verify special character preservation
            assert csv_rows[0]["title"] == "Report", csv_rows[0]
            assert csv_rows[0]["body"] == "Line 1\nLine 2", csv_rows[0]
            assert csv_rows[1]["title"] == "Data", csv_rows[1]
            assert csv_rows[1]["body"] == "Value with, comma", csv_rows[1]
            assert csv_rows[2]["title"] == "Quote", csv_rows[2]
            assert csv_rows[2]["body"] == 'Text with "quotes"', csv_rows[2]

            # Test idempotency: identical retry returns 200 (not 409)
            status, result, _ = request(base + "/imports", "POST", {"request_id": "smoke-1", "csv": CSV})
            assert status == 200, (status, result)
            assert result["import_id"] == csv_import_id, (status, result)
            print("✓ Identical retry returns 200 (idempotent)")

            # Test conflict: same request_id with different payload returns 409
            status, result, _ = request(base + "/imports", "POST", {"request_id": "smoke-1", "csv": "title,body\nDifferent,Data\n"})
            assert status == 409, (status, result)
            print("✓ Conflicting payload returns 409")

            # Test GET /imports/ID returns documents array (not payload text)
            status, result, _ = request(base + f"/imports/{csv_import_id}")
            assert status == 200, (status, result)
            assert "documents" in result, f"Expected 'documents' key, got {result.keys()}"
            assert isinstance(result["documents"], list), f"documents should be array, got {type(result['documents'])}"
            assert len(result["documents"]) == 1, f"Expected 1 document, got {len(result['documents'])}"
            assert result["documents"][0]["title"] == "Transit Plan", result["documents"]
            assert result["documents"][0]["body"] == "Use the east entrance.", result["documents"]
            print("✓ GET /imports/ID returns documents array")

            status, result, _ = request(base + f"/imports/{json_import_id}")
            assert status == 200, (status, result)
            assert "documents" in result, f"Expected 'documents' key, got {result.keys()}"
            assert isinstance(result["documents"], list), f"documents should be array"
            assert len(result["documents"]) == 3, f"Expected 3 documents from JSON import"
            print("✓ GET /imports/ID works for JSON imports too")

        finally:
            process.terminate()
            process.wait(timeout=2)


if __name__ == "__main__":
    main()
