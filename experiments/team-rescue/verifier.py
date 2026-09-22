#!/usr/bin/env python3
"""Independent black-box verifier for the team-rescue webhook workload."""

from __future__ import annotations

import argparse
from contextlib import ExitStack
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import tempfile
import threading
import time
from typing import Any, Callable
import urllib.error
import urllib.request


HTTP_TIMEOUT = 3
START_TIMEOUT = 4
STOP_TIMEOUT = 4
HERE = Path(__file__).resolve().parent


class VerificationError(AssertionError):
    pass


def require(condition: bool, detail: str) -> None:
    if not condition:
        raise VerificationError(detail)


def unused_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return int(listener.getsockname()[1])


class RecipientHandler(BaseHTTPRequestHandler):
    def log_message(self, _format: str, *_args: object) -> None:
        return

    def do_POST(self) -> None:
        length = int(self.headers.get("Content-Length", "0"))
        raw = self.rfile.read(length)
        try:
            body = json.loads(raw)
        except (UnicodeDecodeError, json.JSONDecodeError):
            body = {"invalid_json": raw.decode(errors="replace")}
        recorder = self.server.recorder  # type: ignore[attr-defined]
        with recorder.lock:
            recorder.receipts.append(body)
            status = recorder.status
        self.send_response(status)
        self.send_header("Content-Length", "0")
        self.end_headers()


class Recipient:
    def __init__(self, status: int = 204):
        self.status = status
        self.receipts: list[Any] = []
        self.lock = threading.Lock()
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), RecipientHandler)
        self.server.recorder = self  # type: ignore[attr-defined]
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    @property
    def url(self) -> str:
        return f"http://127.0.0.1:{self.server.server_port}/hook"

    def __enter__(self) -> "Recipient":
        self.thread.start()
        return self

    def __exit__(self, *_args: object) -> None:
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)


class DisconnectedRecipient:
    """Reserve a loopback port without listening so connects fail deterministically."""

    def __init__(self):
        self.socket = socket.socket()
        self.socket.bind(("127.0.0.1", 0))

    @property
    def url(self) -> str:
        return f"http://127.0.0.1:{self.socket.getsockname()[1]}/hook"

    def __enter__(self) -> "DisconnectedRecipient":
        return self

    def __exit__(self, *_args: object) -> None:
        self.socket.close()


class Candidate:
    def __init__(self, workspace: Path, database: Path):
        self.workspace = workspace
        self.database = database
        self.port = unused_port()
        self.process: subprocess.Popen[bytes] | None = None
        self.log = tempfile.TemporaryFile()
        self.pid_file = database.with_name(f"{database.name}.service-{self.port}.pid")

    @property
    def base_url(self) -> str:
        return f"http://127.0.0.1:{self.port}"

    @property
    def service_pid(self) -> int | None:
        try:
            return int(self.pid_file.read_text())
        except (FileNotFoundError, ValueError):
            return None

    def start(self) -> "Candidate":
        environment = dict(os.environ)
        environment["PYTHONDONTWRITEBYTECODE"] = "1"
        self.process = subprocess.Popen(
            [
                sys.executable,
                str(HERE / "service_worker.py"),
                "--parent-pid",
                str(os.getpid()),
                "--pid-file",
                str(self.pid_file),
                "--",
                sys.executable,
                str(self.workspace / "service.py"),
                "--port",
                str(self.port),
                "--db",
                str(self.database),
            ],
            cwd=self.workspace,
            env=environment,
            stdin=subprocess.DEVNULL,
            stdout=self.log,
            stderr=subprocess.STDOUT,
        )
        deadline = time.monotonic() + START_TIMEOUT
        last_error = "service did not answer"
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                break
            try:
                self.request("GET", "/metrics")
                return self
            except Exception as error:  # startup may race the listener
                last_error = str(error)
                time.sleep(0.04)
        self.stop()
        self.log.seek(0)
        output = self.log.read().decode(errors="replace")[-2000:]
        raise VerificationError(f"service failed to start: {last_error}; output={output!r}")

    def stop(self) -> None:
        if self.process is None:
            return
        if self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=STOP_TIMEOUT)
            except subprocess.TimeoutExpired:
                service_pid = self.service_pid
                if service_pid is not None:
                    try:
                        os.killpg(service_pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                self.process.kill()
                self.process.wait(timeout=2)
        self.process = None

    def close(self) -> None:
        self.stop()
        self.log.close()
        try:
            self.pid_file.unlink()
        except FileNotFoundError:
            pass

    def request(self, method: str, path: str, body: Any = None) -> tuple[int, Any]:
        data = None
        headers: dict[str, str] = {}
        if body is not None:
            data = json.dumps(body, separators=(",", ":")).encode()
            headers["Content-Type"] = "application/json"
        return self.request_bytes(method, path, data, headers)

    def request_bytes(
        self, method: str, path: str, data: bytes | None, headers: dict[str, str]
    ) -> tuple[int, Any]:
        request = urllib.request.Request(self.base_url + path, data, headers, method=method)
        try:
            with urllib.request.urlopen(request, timeout=HTTP_TIMEOUT) as response:
                raw = response.read()
                return response.status, json.loads(raw)
        except urllib.error.HTTPError as error:
            raw = error.read()
            error.close()
            try:
                decoded = json.loads(raw)
            except (UnicodeDecodeError, json.JSONDecodeError):
                decoded = {"raw": raw.decode(errors="replace")}
            return error.code, decoded
        except (OSError, TimeoutError) as error:
            raise VerificationError(f"{method} {path} failed: {error}") from error

    def ok(self, method: str, path: str, body: Any = None) -> Any:
        status, value = self.request(method, path, body)
        require(200 <= status < 300, f"{method} {path} returned HTTP {status}: {value!r}")
        require(isinstance(value, dict), f"{method} {path} did not return a JSON object")
        return value


def delivery_rows(candidate: Candidate) -> list[dict[str, Any]]:
    value = candidate.ok("GET", "/deliveries")
    rows = value.get("deliveries")
    require(isinstance(rows, list), "GET /deliveries must contain a deliveries array")
    require(all(isinstance(item, dict) for item in rows), "every delivery must be an object")
    return rows


def subscribe(candidate: Candidate, recipient: Recipient) -> str:
    value = candidate.ok("POST", "/subscriptions", {"url": recipient.url})
    subscription_id = value.get("id")
    require(isinstance(subscription_id, str) and subscription_id, "subscription id must be a non-empty string")
    return subscription_id


def event(candidate: Candidate, event_id: str, payload: Any) -> Any:
    return candidate.ok("POST", "/events", {"id": event_id, "payload": payload})


def tick(candidate: Candidate, now: int) -> int:
    value = candidate.ok("POST", "/tick", {"now": now})
    attempted = value.get("attempted")
    require(isinstance(attempted, int) and not isinstance(attempted, bool), "tick attempted must be an integer")
    return attempted


def new_candidate(workspace: Path, root: Path, name: str) -> Candidate:
    return Candidate(workspace, root / f"{name}.sqlite").start()


def check_api_and_idempotency(workspace: Path) -> str:
    with tempfile.TemporaryDirectory(prefix="team-rescue-api-") as raw_root, ExitStack() as stack:
        root = Path(raw_root)
        first = stack.enter_context(Recipient())
        second = stack.enter_context(Recipient())
        late = stack.enter_context(Recipient())
        candidate = new_candidate(workspace, root, "state")
        stack.callback(candidate.close)
        first_id = subscribe(candidate, first)
        second_id = subscribe(candidate, second)
        require(first_id != second_id, "subscriptions received duplicate IDs")
        payload = {"invoice": 1001, "tags": ["public", "synthetic"]}
        created = event(candidate, "invoice.paid:1001", payload)
        require(created.get("id") == "invoice.paid:1001" and created.get("created") is True,
                f"first event response was {created!r}")
        duplicate = event(candidate, "invoice.paid:1001", {"invoice": "replacement"})
        require(duplicate.get("id") == "invoice.paid:1001" and duplicate.get("created") is False,
                f"duplicate event response was {duplicate!r}")
        # A subscription applies only to events accepted after it (TASK.md). Subscribing
        # here, after the event exists, is the only arrangement that can catch an
        # implementation which backfills the whole event log to every new subscriber.
        late_id = subscribe(candidate, late)
        require(late_id not in {first_id, second_id}, "late subscription reused an ID")
        require(tick(candidate, 100) == 2, "first tick must attempt exactly two fanout deliveries")
        require(len(late.receipts) == 0,
                f"a subscription created after the event was delivered {len(late.receipts)} times")
        require(len(first.receipts) == 1 and len(second.receipts) == 1,
                f"fanout receipts were {len(first.receipts)} and {len(second.receipts)}")
        rows = delivery_rows(candidate)
        require(len(rows) == 2, f"duplicate event created deliveries; found {len(rows)}")
        required = {"id", "event_id", "subscription_id", "url", "payload", "status",
                    "attempts", "next_attempt_at", "history"}
        for row in rows:
            require(required <= set(row), f"delivery lacks fields: {sorted(required - set(row))}")
            require(row["event_id"] == "invoice.paid:1001", f"wrong event id in {row!r}")
            require(row["payload"] == payload, "duplicate event replaced the original payload")
            require(row["status"] == "succeeded" and row["attempts"] == 1,
                    f"successful delivery state was {row!r}")
            require(row["next_attempt_at"] is None, "successful delivery remained scheduled")
            require(len(row["history"]) == 1, "successful delivery history must contain one attempt")
            history = row["history"][0]
            require(history.get("attempt") == 1 and history.get("at") == 100,
                    f"bad success history {history!r}")
            require(isinstance(history.get("status_code"), int) and 200 <= history["status_code"] < 300,
                    f"success status was not recorded: {history!r}")
        by_url = {row["url"]: row for row in rows}
        require(set(by_url) == {first.url, second.url}, "delivery URLs do not match subscriptions")
        require({row["subscription_id"] for row in rows} == {first_id, second_id},
                "deliveries do not retain subscription IDs")
        require(late.url not in by_url,
                "the event was backfilled to a subscription created after it")
        for recipient in (first, second):
            receipt = recipient.receipts[0]
            row = by_url[recipient.url]
            require(receipt == {
                "event_id": "invoice.paid:1001", "delivery_id": row["id"], "payload": payload
            }, f"recipient body was {receipt!r}")
        require(tick(candidate, 1000) == 0, "successful deliveries were attempted again")
        require(len(first.receipts) == 1 and len(second.receipts) == 1,
                "successful recipients received a redelivery")
        require(len(late.receipts) == 0, "a later tick backfilled the late subscription")
    return ("fanout, response bodies, original payload, idempotency, late-subscription "
            "exclusion, and success terminality hold")


def canonical_json(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def check_payload_roundtrip(workspace: Path) -> str:
    payloads = [
        None,
        True,
        17,
        "synthetic payload",
        [1, "two", False, None],
        {"nested": {"count": 3}, "items": ["a", "b"]},
    ]
    with tempfile.TemporaryDirectory(prefix="team-rescue-payload-") as raw_root, Recipient(204) as recipient:
        root = Path(raw_root)
        database = root / "state.sqlite"
        first = Candidate(workspace, database).start()
        try:
            subscribe(first, recipient)
            for index, payload in enumerate(payloads):
                created = event(first, f"payload.kind:{index}", payload)
                require(created.get("created") is True,
                        f"payload event {index} was not created: {created!r}")
        finally:
            first.close()
        second = Candidate(workspace, database).start()
        try:
            require(tick(second, 100) == len(payloads),
                    "JSON payload deliveries were lost across restart")
            require(len(recipient.receipts) == len(payloads),
                    f"recipient got {len(recipient.receipts)} payloads, expected {len(payloads)}")
            receipts = {item.get("event_id"): item.get("payload") for item in recipient.receipts}
            rows = {item.get("event_id"): item.get("payload") for item in delivery_rows(second)}
            for index, payload in enumerate(payloads):
                event_id = f"payload.kind:{index}"
                expected = canonical_json(payload)
                require(event_id in receipts and canonical_json(receipts[event_id]) == expected,
                        f"recipient payload changed type or value for {event_id}: {receipts.get(event_id)!r}")
                require(event_id in rows and canonical_json(rows[event_id]) == expected,
                        f"delivery payload changed type or value for {event_id}: {rows.get(event_id)!r}")
        finally:
            second.close()
    return "null, boolean, number, string, list, and object payloads survive restart and delivery"


def check_network_error_isolation(workspace: Path) -> str:
    with tempfile.TemporaryDirectory(prefix="team-rescue-network-") as raw_root, ExitStack() as stack:
        root = Path(raw_root)
        disconnected = stack.enter_context(DisconnectedRecipient())
        healthy = stack.enter_context(Recipient(204))
        candidate = new_candidate(workspace, root, "state")
        stack.callback(candidate.close)
        subscribe(candidate, disconnected)
        subscribe(candidate, healthy)
        event(candidate, "export.ready:1501", {"export": 1501})
        require(tick(candidate, 100) == 2,
                "disconnected first recipient prevented complete fanout")
        require(len(healthy.receipts) == 1,
                "healthy recipient was not reached after disconnected recipient")
        rows = delivery_rows(candidate)
        require(len(rows) == 2, f"expected two isolated deliveries, got {len(rows)}")
        broken = next((row for row in rows if row["url"] == disconnected.url), None)
        good = next((row for row in rows if row["url"] == healthy.url), None)
        require(broken is not None and good is not None, "network failure lost a destination")
        require(good["status"] == "succeeded" and good["attempts"] == 1,
                f"healthy delivery state was {good!r}")
        require(broken["status"] == "retrying" and broken["attempts"] == 1,
                f"network failure was not recorded as retrying: {broken!r}")
        require(broken["next_attempt_at"] == 110,
                f"network failure retry was due at {broken['next_attempt_at']!r}, expected 110")
        require(broken["history"] == [{"attempt": 1, "at": 100, "status_code": None}],
                f"network failure history was {broken['history']!r}")
        require(tick(candidate, 109) == 0, "network failure retried before its due time")
        require(tick(candidate, 110) == 1, "network failure did not retry when due")
        require(len(healthy.receipts) == 1, "healthy delivery was repeated during network retry")
        broken = next(row for row in delivery_rows(candidate) if row["url"] == disconnected.url)
        require(broken["status"] == "retrying" and broken["attempts"] == 2,
                f"second network failure state was {broken!r}")
        require(broken["next_attempt_at"] == 130,
                "second network failure did not use the 20-second delay")
        require([item.get("status_code") for item in broken["history"]] == [None, None],
                f"network history did not retain null status codes: {broken['history']!r}")
    return "connection refusal is isolated, recorded with null status, and retried on schedule"


def check_retry_isolation_and_cap(workspace: Path) -> str:
    with tempfile.TemporaryDirectory(prefix="team-rescue-retry-") as raw_root, ExitStack() as stack:
        root = Path(raw_root)
        good = stack.enter_context(Recipient(204))
        bad = stack.enter_context(Recipient(503))
        candidate = new_candidate(workspace, root, "state")
        stack.callback(candidate.close)
        subscribe(candidate, bad)
        subscribe(candidate, good)
        event(candidate, "shipment.ready:2002", {"shipment": 2002})
        require(tick(candidate, 100) == 2, "a failing subscription prevented complete first fanout")
        require(len(good.receipts) == 1 and len(bad.receipts) == 1,
                "both subscriptions were not independently attempted")
        require(tick(candidate, 109) == 0, "failed delivery retried before the 10-second delay")
        require(tick(candidate, 110) == 1, "failed delivery was not retried at now+10")
        require(tick(candidate, 129) == 0, "failed delivery retried before the 20-second delay")
        require(tick(candidate, 130) == 1, "failed delivery was not retried at now+20")
        require(tick(candidate, 999) == 0, "terminal delivery exceeded the three-attempt cap")
        require(len(good.receipts) == 1, "successful delivery was sent again")
        require(len(bad.receipts) == 3, f"failed recipient got {len(bad.receipts)} attempts, expected 3")
        rows = delivery_rows(candidate)
        require(len(rows) == 2, f"expected two deliveries, got {len(rows)}")
        good_row = next((row for row in rows if row["url"] == good.url), None)
        bad_row = next((row for row in rows if row["url"] == bad.url), None)
        require(good_row is not None and bad_row is not None, "delivery isolation lost a destination")
        require(good_row["status"] == "succeeded" and good_row["attempts"] == 1,
                f"good delivery state was {good_row!r}")
        require(bad_row["status"] == "failed" and bad_row["attempts"] == 3,
                f"failed delivery state was {bad_row!r}")
        require(bad_row["next_attempt_at"] is None, "capped delivery remained scheduled")
        history = bad_row["history"]
        require([item.get("attempt") for item in history] == [1, 2, 3],
                f"attempt numbering was {history!r}")
        require([item.get("at") for item in history] == [100, 110, 130],
                f"retry timing was {history!r}")
        require([item.get("status_code") for item in history] == [503, 503, 503],
                f"failure statuses were {history!r}")
    return "failure isolation, deterministic 10/20 backoff, cap, and no success redelivery hold"


def check_metrics(workspace: Path) -> str:
    with tempfile.TemporaryDirectory(prefix="team-rescue-metrics-") as raw_root, ExitStack() as stack:
        root = Path(raw_root)
        good = stack.enter_context(Recipient(204))
        bad = stack.enter_context(Recipient(503))
        candidate = new_candidate(workspace, root, "state")
        stack.callback(candidate.close)
        subscribe(candidate, good)
        subscribe(candidate, bad)
        event(candidate, "account.opened:3003", {"account": 3003})
        tick(candidate, 50)
        tick(candidate, 60)
        metrics = candidate.ok("GET", "/metrics")
        expected = {
            "subscriptions": 2, "events": 1, "deliveries": 2, "pending": 0,
            "retrying": 1, "succeeded": 1, "failed": 0, "attempts": 3,
        }
        wrong_types = {key: type(metrics.get(key)).__name__ for key in expected
                       if type(metrics.get(key)) is not int}
        require(not wrong_types, f"metrics must be integers, got: {wrong_types!r}")
        mismatches = {key: (metrics.get(key), value) for key, value in expected.items()
                      if metrics.get(key) != value}
        require(not mismatches, f"metric mismatches (actual, expected): {mismatches!r}")
        require(sum(metrics[name] for name in ("pending", "retrying", "succeeded", "failed")) == 2,
                "status metrics do not partition deliveries")
    return "all required counters match mixed successful and retrying state"


def check_restart_persistence(workspace: Path) -> str:
    with tempfile.TemporaryDirectory(prefix="team-rescue-restart-") as raw_root, Recipient(503) as recipient:
        root = Path(raw_root)
        database = root / "state.sqlite"
        first = Candidate(workspace, database).start()
        try:
            subscription_id = subscribe(first, recipient)
            event(first, "member.joined:4004", {"member": 4004})
            require(tick(first, 50) == 1, "initial failed attempt did not run")
        finally:
            first.close()
        second = Candidate(workspace, database).start()
        try:
            require(tick(second, 59) == 0, "retry schedule moved earlier across restart")
            recipient.status = 204
            require(tick(second, 60) == 1, "retry schedule was lost across restart")
            rows = delivery_rows(second)
            require(len(rows) == 1, f"restart restored {len(rows)} deliveries, expected one")
            row = rows[0]
            require(row["subscription_id"] == subscription_id, "subscription identity changed on restart")
            require(row["status"] == "succeeded" and row["attempts"] == 2,
                    f"restored delivery did not succeed: {row!r}")
            require([item.get("at") for item in row["history"]] == [50, 60],
                    f"attempt history was lost across restart: {row['history']!r}")
        finally:
            second.close()
        third = Candidate(workspace, database).start()
        try:
            require(tick(third, 1000) == 0, "successful state was lost and redelivered after restart")
            require(len(recipient.receipts) == 2, "recipient got duplicate delivery after restart")
            metrics = third.ok("GET", "/metrics")
            require(metrics.get("subscriptions") == 1 and metrics.get("events") == 1,
                    f"durable object metrics were lost: {metrics!r}")
            require(metrics.get("succeeded") == 1 and metrics.get("attempts") == 2,
                    f"durable delivery metrics were lost: {metrics!r}")
        finally:
            third.close()
    return "subscription, event, schedule, terminal state, history, and metrics survive restarts"


def check_invalid_input(workspace: Path) -> str:
    with tempfile.TemporaryDirectory(prefix="team-rescue-input-") as raw_root:
        candidate = new_candidate(workspace, Path(raw_root), "state")
        try:
            status, value = candidate.request_bytes(
                "POST", "/events", b'{"id":', {"Content-Type": "application/json"}
            )
            require(400 <= status < 500 and isinstance(value, dict),
                    f"malformed JSON returned HTTP {status}: {value!r}")
            status, value = candidate.request("POST", "/subscriptions", {})
            require(400 <= status < 500 and isinstance(value, dict),
                    f"missing subscription URL returned HTTP {status}: {value!r}")
            status, value = candidate.request("POST", "/subscriptions", {"url": 17})
            require(400 <= status < 500 and isinstance(value, dict),
                    f"non-string subscription URL returned HTTP {status}: {value!r}")
            status, value = candidate.request("POST", "/events", {"payload": {}})
            require(400 <= status < 500 and isinstance(value, dict),
                    f"missing event id returned HTTP {status}: {value!r}")
            status, value = candidate.request("POST", "/events", {"id": 17, "payload": {}})
            require(400 <= status < 500 and isinstance(value, dict),
                    f"non-string event id returned HTTP {status}: {value!r}")
            status, value = candidate.request("POST", "/tick", {"now": "100"})
            require(400 <= status < 500 and isinstance(value, dict),
                    f"non-integer tick time returned HTTP {status}: {value!r}")
            status, value = candidate.request("POST", "/not-an-endpoint", {})
            require(400 <= status < 500 and isinstance(value, dict),
                    f"unknown path returned HTTP {status}: {value!r}")
            status, value = candidate.request("PUT", "/not-an-endpoint", {})
            require(400 <= status < 500 and isinstance(value, dict),
                    f"unsupported method returned HTTP {status}: {value!r}")
            candidate.ok("GET", "/metrics")
        finally:
            candidate.close()
    return "malformed JSON, bad field types, unknown routes, and unsupported methods return JSON 4xx"


def check_replay(workspace: Path) -> str:
    with tempfile.TemporaryDirectory(prefix="team-rescue-replay-") as raw_root, Recipient(503) as recipient:
        root = Path(raw_root)
        database = root / "state.sqlite"
        first = Candidate(workspace, database).start()
        try:
            subscribe(first, recipient)
            event(first, "report.ready:5005", {"report": 5005})
            tick(first, 10)
            tick(first, 20)
            tick(first, 40)
            before = delivery_rows(first)[0]
            require(before["status"] == "failed" and before["attempts"] == 3,
                    f"replay setup did not reach terminal failure: {before!r}")
            delivery_id = before["id"]
            old_history = json.loads(json.dumps(before["history"]))
        finally:
            first.close()
        second = Candidate(workspace, database).start()
        try:
            replayed = second.ok("POST", f"/deliveries/{delivery_id}/replay", {"now": 500})
            require(replayed.get("id") == delivery_id and replayed.get("replayed") is True,
                    f"replay response was {replayed!r}")
            pending = delivery_rows(second)[0]
            require(pending["id"] == delivery_id and pending["status"] == "pending",
                    f"replay did not re-enable the same delivery: {pending!r}")
            require(pending["attempts"] == 3 and pending["history"] == old_history,
                    "replay discarded prior attempts or history")
            require(pending["next_attempt_at"] == 500, "replay did not use explicit now as due time")
            require(tick(second, 499) == 0, "replay ran before its explicit due time")
            status, _ = second.request("POST", f"/deliveries/{delivery_id}/replay", {"now": 499})
            require(400 <= status < 500, "replaying a non-terminal delivery did not return 4xx")
            require(tick(second, 500) == 1, "replayed delivery was not attempted when due")
            require(tick(second, 509) == 0, "replayed delivery ignored first retry delay")
            require(tick(second, 510) == 1, "replayed delivery did not get a second activation attempt")
            require(tick(second, 529) == 0, "replayed delivery ignored second retry delay")
            require(tick(second, 530) == 1, "replayed delivery did not get a third activation attempt")
            after = delivery_rows(second)[0]
            require(after["status"] == "failed" and after["attempts"] == 6,
                    f"replayed delivery exceeded its fresh three-attempt cap: {after!r}")
            require(after["next_attempt_at"] is None,
                    "failed replay activation remained scheduled after three attempts")
            require(after["history"][:3] == old_history and len(after["history"]) == 6,
                    "replay did not append to retained history")
            require(after["history"][3].get("attempt") == 4 and after["history"][3].get("at") == 500,
                    f"replay history entry was {after['history'][3]!r}")
            require([item.get("at") for item in after["history"][3:]] == [500, 510, 530],
                    "fresh replay activation did not use the documented retry schedule")
            require(tick(second, 599) == 0,
                    "fresh replay activation was attempted beyond its three-attempt cap")
            recipient.status = 204
            replayed_again = second.ok("POST", f"/deliveries/{delivery_id}/replay", {"now": 600})
            require(replayed_again.get("replayed") is True, "successful delivery could not be explicitly replayed")
            require(tick(second, 600) == 1, "explicit replay of successful delivery was not attempted")
            final = delivery_rows(second)[0]
            require(final["status"] == "succeeded" and final["attempts"] == 7,
                    f"successful replay state was {final!r}")
            require(len(final["history"]) == 7 and final["history"][:6] == after["history"],
                    "successful replay did not retain history")
            require(len(recipient.receipts) == 7, "replay attempt count did not match real network effects")
            second.ok("POST", f"/deliveries/{delivery_id}/replay", {"now": 700})
            require(tick(second, 700) == 1, "a successful delivery could not be explicitly replayed")
            successful_replay = delivery_rows(second)[0]
            require(successful_replay["attempts"] == 8 and successful_replay["history"][:7] == final["history"],
                    "successful replay did not retain prior history")
            require(len(recipient.receipts) == 8 and tick(second, 800) == 0,
                    "successful replay did not become terminal again")
        finally:
            second.close()
    return "terminal replay survives restart, retains history, and opens a fresh activation"


Check = tuple[str, Callable[[Path], str]]


def sha256_bytes(value: bytes) -> str:
    return hashlib.sha256(value).hexdigest()


def snapshot_candidate(workspace: Path, destination: Path) -> dict[str, str]:
    destination.mkdir()
    hashes: dict[str, str] = {}
    for name in ("service.py", "README.md"):
        source = workspace / name
        if name == "README.md" and not source.exists():
            continue
        require(source.is_file() and not source.is_symlink(), f"{name} must be a regular file")
        content = source.read_bytes()
        hashes[name] = sha256_bytes(content)
        (destination / name).write_bytes(content)
    return hashes


def check(workspace: Path, phase: int = 1) -> dict[str, Any]:
    """Check a candidate workspace and return a stable JSON-serializable receipt."""
    workspace = Path(workspace).resolve()
    checks: list[Check] = [
        ("api_fanout_and_idempotency", check_api_and_idempotency),
        ("payload_roundtrip", check_payload_roundtrip),
        ("network_error_isolation", check_network_error_isolation),
        ("retry_isolation_and_cap", check_retry_isolation_and_cap),
        ("metrics_consistency", check_metrics),
        ("restart_persistence", check_restart_persistence),
        ("invalid_input", check_invalid_input),
    ]
    if phase >= 2:
        checks.append(("replay", check_replay))
    results: list[dict[str, Any]] = []
    service = workspace / "service.py"
    if phase not in (1, 2):
        return {"passed": False, "phase": phase, "workspace": str(workspace), "checks": [
            {"name": "configuration", "passed": False, "detail": "phase must be 1 or 2"}
        ]}
    if not service.is_file() or service.is_symlink():
        return {"passed": False, "phase": phase, "workspace": str(workspace), "checks": [
            {"name": "candidate", "passed": False, "detail": "workspace must contain a regular service.py"}
        ]}
    started = time.monotonic()
    with tempfile.TemporaryDirectory(prefix="team-rescue-candidate-") as raw_root:
        candidate_workspace = Path(raw_root) / "workspace"
        input_hashes = snapshot_candidate(workspace, candidate_workspace)
        for name, operation in checks:
            try:
                detail = operation(candidate_workspace)
                results.append({"name": name, "passed": True, "detail": detail})
            except Exception as error:
                results.append({"name": name, "passed": False,
                                "detail": f"{type(error).__name__}: {error}"})
        changed: list[str] = []
        for name, before in input_hashes.items():
            try:
                after = sha256_bytes((candidate_workspace / name).read_bytes())
            except OSError:
                after = "missing"
            if after != before:
                changed.append(name)
        results.append({
            "name": "source_integrity",
            "passed": not changed,
            "detail": "candidate source remained unchanged" if not changed
            else f"candidate modified its source snapshot: {', '.join(changed)}",
        })
    return {
        "passed": all(item["passed"] for item in results),
        "phase": phase,
        "workspace": str(workspace),
        "input_hashes": input_hashes,
        "checks": results,
        "elapsed_ms": round((time.monotonic() - started) * 1000),
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("workspace", type=Path)
    parser.add_argument("--phase", type=int, default=1)
    args = parser.parse_args()
    result = check(args.workspace, args.phase)
    print(json.dumps(result, sort_keys=True, separators=(",", ":")))
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    raise SystemExit(main())
