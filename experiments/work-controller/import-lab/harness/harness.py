#!/usr/bin/env python3
"""Independent black-box harness for the import-desk experiment."""

from __future__ import annotations

import argparse
import hashlib
import http.client
import json
import os
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time
from dataclasses import asdict, dataclass
from html.parser import HTMLParser
from pathlib import Path
from typing import Any, Callable
from urllib.parse import quote


TERMINAL = {"completed", "failed"}
KNOWN_STATUSES = {"queued", "running", *TERMINAL}
MIB = 1024 * 1024


@dataclass
class Response:
    status: int
    headers: dict[str, str]
    body: bytes
    elapsed_ms: int
    request_file: str
    response_file: str

    def json(self) -> Any:
        return json.loads(self.body)


@dataclass
class Check:
    name: str
    status: str
    detail: str
    evidence: dict[str, Any]


class UISurfaceParser(HTMLParser):
    def __init__(self) -> None:
        super().__init__()
        self.has_form = False
        self.has_csv_input = False
        self.has_submit = False
        self.external_assets: list[str] = []

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        values = {key.lower(): value or "" for key, value in attrs}
        if tag == "form":
            self.has_form = True
        if tag == "textarea":
            self.has_csv_input = True
        if tag == "input" and values.get("type", "text").lower() == "file":
            self.has_csv_input = True
        if tag == "button" or (tag == "input" and values.get("type", "").lower() == "submit"):
            self.has_submit = True
        if tag in {"script", "link", "img"}:
            target = values.get("src") or values.get("href") or ""
            if target.startswith(("http://", "https://")):
                self.external_assets.append(target)


class Harness:
    def __init__(self, app_dir: Path, round_name: str, out_dir: Path) -> None:
        self.app_dir = app_dir.resolve()
        self.round_name = round_name
        self.out_dir = out_dir.resolve()
        self.out_dir.mkdir(parents=True, exist_ok=False)
        self.request_dir = self.out_dir / "requests"
        self.response_dir = self.out_dir / "responses"
        self.request_dir.mkdir()
        self.response_dir.mkdir()
        self.data_dir = Path(tempfile.mkdtemp(prefix="data-", dir=self.out_dir))
        self.exchange_path = self.out_dir / "exchanges.jsonl"
        self.process: subprocess.Popen[bytes] | None = None
        self.port: int | None = None
        self.start_number = 0
        self.exchange_number = 0
        self.exchange_lock = threading.Lock()
        self.checks: list[Check] = []
        self.started_at = time.monotonic()
        self.deadline = self.started_at + 30.0

    def remaining(self, cap: float = 5.0) -> float:
        return max(0.1, min(cap, self.deadline - time.monotonic()))

    def add(self, name: str, status: str, detail: str, **evidence: Any) -> None:
        self.checks.append(Check(name, status, detail, evidence))

    def run_check(self, name: str, action: Callable[[], None]) -> None:
        try:
            action()
        except AssertionError as exc:
            self.add(name, "fail", str(exc) or "contract assertion failed")
        except (OSError, TimeoutError, ValueError, json.JSONDecodeError) as exc:
            self.add(name, "error", f"{type(exc).__name__}: {exc}")
        except Exception as exc:  # Preserve evidence rather than lose the run.
            self.add(name, "error", f"unexpected {type(exc).__name__}: {exc}")

    def start(self) -> None:
        server = self.app_dir / "server.py"
        if not server.is_file():
            raise FileNotFoundError(f"missing launch file: {server}")
        self.port = unused_port()
        self.start_number += 1
        stdout = (self.out_dir / f"server-{self.start_number}.stdout.log").open("wb")
        stderr = (self.out_dir / f"server-{self.start_number}.stderr.log").open("wb")
        command = [
            sys.executable,
            "server.py",
            "--port",
            str(self.port),
            "--data-dir",
            str(self.data_dir),
        ]
        self.process = subprocess.Popen(
            command,
            cwd=self.app_dir,
            stdout=stdout,
            stderr=stderr,
        )
        end = min(self.deadline, time.monotonic() + 5.0)
        last_error = "server did not answer"
        while time.monotonic() < end:
            if self.process.poll() is not None:
                raise RuntimeError(f"server exited during startup with {self.process.returncode}")
            try:
                response = self.request("GET", "/health", timeout=0.5)
                if response.status == 200:
                    return
                last_error = f"health returned HTTP {response.status}"
            except (OSError, TimeoutError) as exc:
                last_error = str(exc)
            time.sleep(0.05)
        raise TimeoutError(f"startup exceeded 5 seconds: {last_error}")

    def stop(self, *, kill: bool = False) -> None:
        process = self.process
        self.process = None
        if process is None or process.poll() is not None:
            return
        if kill:
            os.kill(process.pid, signal.SIGKILL)
            process.wait(timeout=2)
            return
        process.terminate()
        try:
            process.wait(timeout=2)
        except subprocess.TimeoutExpired:
            os.kill(process.pid, signal.SIGKILL)
            process.wait(timeout=2)

    def request(
        self,
        method: str,
        path: str,
        body: bytes = b"",
        headers: dict[str, str] | None = None,
        timeout: float | None = None,
    ) -> Response:
        if self.port is None:
            raise RuntimeError("server has no port")
        with self.exchange_lock:
            self.exchange_number += 1
            number = self.exchange_number
        request_file = self.request_dir / f"{number:03d}.bin"
        response_file = self.response_dir / f"{number:03d}.bin"
        request_file.write_bytes(body)
        started = time.monotonic()
        connection = http.client.HTTPConnection("127.0.0.1", self.port, timeout=timeout or self.remaining())
        error = ""
        status = 0
        response_headers: dict[str, str] = {}
        response_body = b""
        try:
            connection.request(method, path, body=body, headers=headers or {})
            raw = connection.getresponse()
            status = raw.status
            response_headers = {key.lower(): value for key, value in raw.getheaders()}
            response_body = raw.read()
            response_file.write_bytes(response_body)
        except Exception as exc:
            error = f"{type(exc).__name__}: {exc}"
            response_file.write_bytes(b"")
            raise
        finally:
            connection.close()
            elapsed_ms = round((time.monotonic() - started) * 1000)
            event = {
                "number": number,
                "method": method,
                "path": path,
                "request_bytes": len(body),
                "request_sha256": hashlib.sha256(body).hexdigest(),
                "request_file": str(request_file.relative_to(self.out_dir)),
                "status": status,
                "response_bytes": len(response_body),
                "response_sha256": hashlib.sha256(response_body).hexdigest(),
                "response_file": str(response_file.relative_to(self.out_dir)),
                "elapsed_ms": elapsed_ms,
                "error": error,
            }
            with self.exchange_lock:
                with self.exchange_path.open("a", encoding="utf-8") as stream:
                    stream.write(json.dumps(event, sort_keys=True) + "\n")
        return Response(
            status,
            response_headers,
            response_body,
            elapsed_ms,
            str(request_file.relative_to(self.out_dir)),
            str(response_file.relative_to(self.out_dir)),
        )

    def json_request(self, method: str, path: str, payload: Any, timeout: float | None = None) -> Response:
        body = json.dumps(payload, separators=(",", ":")).encode()
        return self.request(method, path, body, {"Content-Type": "application/json"}, timeout)

    def submit(self, csv_text: str, request_id: str | None = None, timeout: float | None = None) -> tuple[Response, Any]:
        payload: dict[str, Any] = {"csv": csv_text}
        if request_id is not None:
            payload["request_id"] = request_id
        response = self.json_request("POST", "/imports", payload, timeout)
        return response, parse_json(response)

    def follow(self, initial: Any, timeout: float = 5.0) -> dict[str, Any]:
        assert isinstance(initial, dict), "import response must be a JSON object"
        import_id = initial.get("id")
        status = initial.get("status")
        assert isinstance(import_id, str) and import_id, "import response needs a nonempty string id"
        assert status in KNOWN_STATUSES, f"unknown import status: {status!r}"
        if status in TERMINAL and "records" in initial and "errors" in initial:
            return initial
        end = min(self.deadline, time.monotonic() + timeout)
        latest = initial
        while time.monotonic() < end:
            response = self.request("GET", f"/imports/{quote(import_id, safe='')}", timeout=self.remaining(1.0))
            assert response.status == 200, f"inspect returned HTTP {response.status}"
            latest = parse_json(response)
            assert isinstance(latest, dict), "inspect response must be a JSON object"
            status = latest.get("status")
            assert status in KNOWN_STATUSES, f"unknown import status: {status!r}"
            if status in TERMINAL:
                assert isinstance(latest.get("records"), list), "terminal import needs records array"
                assert isinstance(latest.get("errors"), list), "terminal import needs errors array"
                return latest
            time.sleep(0.05)
        raise TimeoutError(f"import {import_id} did not finish in {timeout:g}s; last status {latest.get('status')!r}")

    def run(self) -> None:
        try:
            self.start()
        except Exception as exc:
            self.add("startup", "error", f"{type(exc).__name__}: {exc}")
            return
        if self.round_name == "build":
            self.run_build()
        if self.round_name == "pressure":
            self.run_pressure()
        if self.round_name == "recovery":
            self.run_recovery()

    def run_build(self) -> None:
        self.run_check("startup_health", self.check_health)
        self.run_check("browser_surface", self.check_ui)
        state: dict[str, Any] = {}

        def mixed() -> None:
            csv_text = (
                "id,name,email\n"
                "1,Ada,ada@example.com\n"
                ",No ID,noid@example.com\n"
                "3,No Domain,nodomain@\n"
                "4,Space,has space@example.com\n"
                "5,Lin,lin@example.org\n"
            )
            response, initial = self.submit(csv_text)
            assert response.status in {200, 201, 202}, f"submit returned HTTP {response.status}"
            terminal = self.follow(initial)
            assert terminal.get("status") == "completed", f"mixed import ended {terminal.get('status')!r}"
            expected = [
                {"id": "1", "name": "Ada", "email": "ada@example.com"},
                {"id": "5", "name": "Lin", "email": "lin@example.org"},
            ]
            assert canonical_records(terminal["records"]) == canonical_records(expected), "valid record set differs"
            rows = {item.get("row") for item in terminal["errors"] if isinstance(item, dict) and item.get("message")}
            assert {3, 4, 5}.issubset(rows), f"expected useful errors at rows 3, 4, 5; got {sorted(rows, key=str)}"
            assert len(terminal["errors"]) == 3, f"expected 3 row errors, got {len(terminal['errors'])}"
            state["mixed"] = terminal
            self.add("mixed_csv_validation", "pass", "two valid records and three row errors observed", import_id=terminal["id"])

        self.run_check("mixed_csv_validation", mixed)

        def retry() -> None:
            corrected = (
                "id,name,email\n"
                "2,No ID,noid@example.com\n"
                "3,No Domain,nodomain@example.com\n"
                "4,Space,hasspace@example.com\n"
            )
            response, initial = self.submit(corrected)
            assert response.status in {200, 201, 202}, f"retry returned HTTP {response.status}"
            terminal = self.follow(initial)
            assert terminal.get("status") == "completed", f"retry ended {terminal.get('status')!r}"
            assert len(terminal["records"]) == 3, f"expected 3 corrected records, got {len(terminal['records'])}"
            assert terminal["errors"] == [], f"corrected retry has {len(terminal['errors'])} errors"
            state["retry"] = terminal
            self.add("corrected_retry", "pass", "corrected input completed without errors", import_id=terminal["id"])

        self.run_check("corrected_retry", retry)

        def listing() -> None:
            assert "mixed" in state and "retry" in state, "prerequisite imports did not pass"
            mixed_id = state["mixed"]["id"]
            inspect = self.request("GET", f"/imports/{quote(mixed_id, safe='')}")
            assert inspect.status == 200, f"inspect returned HTTP {inspect.status}"
            inspected = parse_json(inspect)
            assert digest_result(inspected) == digest_result(state["mixed"]), "inspect differs from terminal result"
            response = self.request("GET", "/imports")
            assert response.status == 200, f"listing returned HTTP {response.status}"
            payload = parse_json(response)
            imports = payload.get("imports") if isinstance(payload, dict) else None
            assert isinstance(imports, list), "listing needs imports array"
            by_id = {item.get("id"): item for item in imports if isinstance(item, dict)}
            expected_ids = {state["mixed"]["id"], state["retry"]["id"]}
            assert expected_ids.issubset(by_id), "listing omitted a submitted import"
            assert all(by_id[item_id].get("status") in TERMINAL for item_id in expected_ids), "listing status is not terminal"
            self.add("inspection_listing", "pass", "inspection matched and listing included both imports", ids=sorted(expected_ids))

        self.run_check("inspection_listing", listing)

    def check_health(self) -> None:
        response = self.request("GET", "/health")
        assert response.status == 200, f"health returned HTTP {response.status}"
        assert isinstance(parse_json(response), dict), "health JSON must be an object"
        self.add("startup_health", "pass", "health returned HTTP 200 JSON", elapsed_ms=response.elapsed_ms)

    def check_ui(self) -> None:
        response = self.request("GET", "/")
        assert response.status == 200, f"UI returned HTTP {response.status}"
        html = response.body.decode("utf-8")
        parser = UISurfaceParser()
        parser.feed(html)
        assert parser.has_form, "UI has no form"
        assert parser.has_csv_input, "UI has no file input or textarea"
        assert parser.has_submit, "UI has no submit control"
        assert not parser.external_assets, f"UI loads external assets: {parser.external_assets}"
        self.add("browser_surface", "pass", "self-contained HTML has form, CSV input, and submit control")

    def run_pressure(self) -> None:
        state: dict[str, Any] = {}

        def large_import() -> None:
            csv_text, expected_count, first, last = make_csv(50_000)
            request_body = json.dumps({"csv": csv_text}, separators=(",", ":")).encode()
            assert len(request_body) < 10 * MIB, "harness generated an oversized 50k request"
            response = self.request("POST", "/imports", request_body, {"Content-Type": "application/json"}, self.remaining(15))
            assert response.status in {200, 201, 202}, f"large submit returned HTTP {response.status}"
            terminal = self.follow(parse_json(response), timeout=self.remaining(15))
            assert terminal.get("status") == "completed", f"large import ended {terminal.get('status')!r}"
            assert len(terminal["records"]) == expected_count, f"expected {expected_count} records, got {len(terminal['records'])}"
            assert terminal["errors"] == [], f"large import has {len(terminal['errors'])} errors"
            records = canonical_records(terminal["records"])
            assert canonical_record(first) in records and canonical_record(last) in records, "large import lost a sentinel"
            state["large"] = terminal
            self.add("large_valid_import", "pass", "50,000 rows completed", import_id=terminal["id"], request_bytes=len(request_body))

        self.run_check("large_valid_import", large_import)

        def body_cap() -> None:
            padding = "x" * (10 * MIB + 256)
            response, payload = self.submit("id,name,email\n1," + padding + ",x@example.com\n", timeout=self.remaining(5))
            assert response.status in {400, 413}, f"over-limit request returned HTTP {response.status}"
            assert useful_error(payload), "over-limit rejection lacks useful JSON error/message"
            self.add("ten_mib_cap", "pass", "over-10-MiB request rejected", http_status=response.status)

        self.run_check("ten_mib_cap", body_cap)

        def malformed() -> None:
            response, payload = self.submit('id,name,email\n1,"unterminated,user@example.com\n')
            if response.status in {400, 422}:
                assert useful_error(payload), "malformed rejection lacks useful JSON error/message"
                self.add("malformed_csv", "pass", "malformed CSV rejected", http_status=response.status)
                return
            assert response.status in {200, 201, 202}, f"malformed submit returned HTTP {response.status}"
            terminal = self.follow(payload)
            assert terminal.get("status") == "failed", f"malformed CSV ended {terminal.get('status')!r}"
            assert terminal.get("records") == [], "malformed CSV produced records"
            assert useful_error(terminal) or any(useful_error(item) for item in terminal.get("errors", [])), "failed import lacks useful error"
            self.add("malformed_csv", "pass", "malformed CSV produced a useful failed import", import_id=terminal["id"])

        self.run_check("malformed_csv", malformed)
        self.run_check("concurrent_service", self.check_concurrency)

        def persistence() -> None:
            assert "large" in state, "large import prerequisite did not pass"
            before = state["large"]
            before_digest = digest_result(before)
            self.stop()
            self.start()
            response = self.request("GET", f"/imports/{quote(before['id'], safe='')}")
            assert response.status == 200, f"persisted inspect returned HTTP {response.status}"
            after = parse_json(response)
            assert digest_result(after) == before_digest, "import changed or disappeared after restart"
            self.add("restart_persistence", "pass", "completed import matched after same-data-dir restart", import_id=before["id"])

        self.run_check("restart_persistence", persistence)
        self.run_check("request_id_idempotency", self.check_idempotency)

    def check_concurrency(self) -> None:
        csv_text, _, _, _ = make_csv(50_000, prefix="concurrent")
        result: dict[str, Any] = {}
        begun = threading.Event()

        def large_request() -> None:
            begun.set()
            try:
                response, payload = self.submit(csv_text, request_id="concurrent-large", timeout=self.remaining(12))
                result.update(response=response, payload=payload)
            except Exception as exc:
                result["error"] = f"{type(exc).__name__}: {exc}"

        worker = threading.Thread(target=large_request, daemon=True)
        worker.start()
        begun.wait(1)
        overlap = worker.is_alive()
        health = self.request("GET", "/health", timeout=min(2.0, self.remaining()))
        small_csv = "id,name,email\nsmall,Small,small@example.com\n"
        small_response, small_initial = self.submit(small_csv, request_id="concurrent-small", timeout=self.remaining(5))
        small = self.follow(small_initial, timeout=self.remaining(5))
        worker.join(timeout=self.remaining(8))
        if not overlap:
            self.add("concurrent_service", "not_covered", "large request completed before concurrent probes began")
            return
        assert health.status == 200, f"concurrent health returned HTTP {health.status}"
        assert health.elapsed_ms <= 2000, f"concurrent health took {health.elapsed_ms}ms"
        assert isinstance(parse_json(health), dict), "concurrent health response is not a JSON object"
        assert small_response.status in {200, 201, 202}, f"concurrent small submit returned HTTP {small_response.status}"
        assert small.get("status") == "completed", f"concurrent small import ended {small.get('status')!r}"
        assert len(small.get("records", [])) == 1 and small.get("errors") == [], "concurrent small import result differs"
        assert not worker.is_alive(), "large concurrent request exceeded timeout"
        assert "error" not in result, f"large concurrent request failed: {result.get('error')}"
        self.add("concurrent_service", "pass", "health and small import completed during large request", health_ms=health.elapsed_ms)

    def check_idempotency(self) -> None:
        csv_text = "id,name,email\ndupe,Dupe,dupe@example.com\n"
        first_response, first_initial = self.submit(csv_text, request_id="stable-request-id")
        assert first_response.status in {200, 201, 202}, f"first idempotent submit returned HTTP {first_response.status}"
        first = self.follow(first_initial)
        second_response, second_initial = self.submit(csv_text, request_id="stable-request-id")
        assert second_response.status in {200, 201, 202}, f"second idempotent submit returned HTTP {second_response.status}"
        second = self.follow(second_initial)
        assert first["id"] == second["id"], "same request ID and content created a different import"
        assert digest_result(first) == digest_result(second), "same request ID changed the result"
        changed_response, _ = self.submit(
            "id,name,email\ndupe,Changed,changed@example.com\n",
            request_id="stable-request-id",
        )
        assert changed_response.status == 409, f"changed content with reused request ID returned HTTP {changed_response.status}"
        listing_response = self.request("GET", "/imports")
        assert listing_response.status == 200, f"listing returned HTTP {listing_response.status}"
        listing = parse_json(listing_response)
        summaries = listing.get("imports") if isinstance(listing, dict) else None
        assert isinstance(summaries, list), "listing needs imports array"
        occurrences = sum(1 for item in summaries if isinstance(item, dict) and item.get("id") == first["id"])
        assert occurrences == 1, f"idempotent import appears {occurrences} times in listing"
        after_response = self.request("GET", f"/imports/{quote(first['id'], safe='')}")
        assert after_response.status == 200, f"post-conflict inspect returned HTTP {after_response.status}"
        assert digest_result(parse_json(after_response)) == digest_result(first), "conflicting retry changed original import"
        self.add("request_id_idempotency", "pass", "same content reused one import; changed content returned 409", import_id=first["id"])

    def run_recovery(self) -> None:
        def recovery() -> None:
            csv_text, expected_count, first, last = make_near_limit_csv()
            body_size = len(json.dumps({"csv": csv_text, "request_id": "recovery-request"}, separators=(",", ":")).encode())
            response, initial = self.submit(csv_text, request_id="recovery-request", timeout=self.remaining(8))
            assert response.status in {200, 201, 202}, f"recovery submit returned HTTP {response.status}"
            assert isinstance(initial, dict), "recovery submit must return a JSON object"
            import_id = initial.get("id")
            status = initial.get("status")
            assert isinstance(import_id, str) and import_id, "recovery submit lacks import id"
            assert status in KNOWN_STATUSES, f"recovery submit has unknown status {status!r}"
            if status == "completed":
                self.add(
                    "mid_import_recovery",
                    "not_covered",
                    "import completed synchronously before kill",
                    import_id=import_id,
                    request_bytes=body_size,
                )
                return
            if status == "failed":
                raise AssertionError("import failed before interruption")
            self.stop(kill=True)
            self.start()
            recovered = self.follow({"id": import_id, "status": status}, timeout=min(20.0, self.remaining(20)))
            assert recovered.get("status") == "completed", f"recovered import ended {recovered.get('status')!r}"
            records = recovered.get("records")
            assert isinstance(records, list), "recovered import lacks records array"
            assert len(records) == expected_count, f"expected {expected_count} recovered records, got {len(records)}"
            keys = [canonical_record(record) for record in records]
            assert len(set(keys)) == expected_count, "recovered import contains duplicate records"
            assert canonical_record(first) in keys and canonical_record(last) in keys, "recovered import lost a sentinel"
            assert recovered.get("errors") == [], f"recovered import has {len(recovered.get('errors', []))} errors"
            listing_response = self.request("GET", "/imports")
            assert listing_response.status == 200, f"listing returned HTTP {listing_response.status}"
            listing = parse_json(listing_response)
            summaries = listing.get("imports") if isinstance(listing, dict) else None
            assert isinstance(summaries, list), "listing needs imports array"
            occurrences = sum(1 for item in summaries if isinstance(item, dict) and item.get("id") == import_id)
            assert occurrences == 1, f"recovered import appears {occurrences} times in listing"
            self.add(
                "mid_import_recovery",
                "pass",
                "accepted unfinished import completed exactly once after SIGKILL and restart",
                import_id=import_id,
                records=expected_count,
                request_bytes=body_size,
            )

        self.run_check("mid_import_recovery", recovery)

    def finish(self) -> int:
        self.stop()
        elapsed_ms = round((time.monotonic() - self.started_at) * 1000)
        result = {
            "schema": "import-lab-harness-v1",
            "round": self.round_name,
            "app_dir": str(self.app_dir),
            "data_dir": str(self.data_dir.relative_to(self.out_dir)),
            "elapsed_ms": elapsed_ms,
            "checks": [asdict(check) for check in self.checks],
            "counts": {
                status: sum(1 for check in self.checks if check.status == status)
                for status in ("pass", "fail", "not_covered", "error")
            },
        }
        (self.out_dir / "result.json").write_text(json.dumps(result, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        print(json.dumps(result, indent=2, sort_keys=True))
        if result["counts"]["fail"] or result["counts"]["error"]:
            return 1
        if result["counts"]["not_covered"]:
            return 2
        return 0


def parse_json(response: Response) -> Any:
    try:
        return response.json()
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ValueError(f"HTTP {response.status} response is not valid JSON ({response.response_file})") from exc


def canonical_record(record: Any) -> tuple[str, str, str]:
    if not isinstance(record, dict):
        raise AssertionError(f"record is not an object: {record!r}")
    return (record.get("id"), record.get("name"), record.get("email"))


def canonical_records(records: list[Any]) -> list[tuple[str, str, str]]:
    return sorted(canonical_record(record) for record in records)


def digest_result(result: Any) -> str:
    if not isinstance(result, dict):
        raise AssertionError("import result must be an object")
    relevant = {
        "id": result.get("id"),
        "status": result.get("status"),
        "records": result.get("records"),
        "errors": result.get("errors"),
    }
    encoded = json.dumps(relevant, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(encoded).hexdigest()


def useful_error(payload: Any) -> bool:
    if not isinstance(payload, dict):
        return False
    for key in ("error", "message", "detail"):
        if isinstance(payload.get(key), str) and payload[key].strip():
            return True
    return False


def make_csv(count: int, prefix: str = "row") -> tuple[str, int, dict[str, str], dict[str, str]]:
    rows = ["id,name,email"]
    for index in range(count):
        rows.append(f"{prefix}-{index},Name {index},{prefix}-{index}@example.com")
    first = {"id": f"{prefix}-0", "name": "Name 0", "email": f"{prefix}-0@example.com"}
    last_index = count - 1
    last = {
        "id": f"{prefix}-{last_index}",
        "name": f"Name {last_index}",
        "email": f"{prefix}-{last_index}@example.com",
    }
    return "\n".join(rows) + "\n", count, first, last


def make_near_limit_csv() -> tuple[str, int, dict[str, str], dict[str, str]]:
    rows = ["id,name,email"]
    index = 0
    size = len(rows[0]) + 1
    target = 8 * MIB
    while size < target:
        row = f"recovery-{index},Recovery Name {index:06d} xxxxxxxxxxxx,recovery-{index}@example.com"
        rows.append(row)
        size += len(row) + 1
        index += 1
    first = {"id": "recovery-0", "name": "Recovery Name 000000 xxxxxxxxxxxx", "email": "recovery-0@example.com"}
    last_index = index - 1
    last = {
        "id": f"recovery-{last_index}",
        "name": f"Recovery Name {last_index:06d} xxxxxxxxxxxx",
        "email": f"recovery-{last_index}@example.com",
    }
    return "\n".join(rows) + "\n", index, first, last


def unused_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--app-dir", type=Path, required=True)
    parser.add_argument("--round", choices=("build", "pressure", "recovery"), required=True)
    parser.add_argument("--out", type=Path, required=True)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    harness = Harness(args.app_dir, args.round, args.out)
    try:
        harness.run()
    except Exception as exc:
        harness.add("harness", "error", f"unexpected {type(exc).__name__}: {exc}")
    return harness.finish()


if __name__ == "__main__":
    raise SystemExit(main())
