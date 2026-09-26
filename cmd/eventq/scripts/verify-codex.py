#!/usr/bin/env python3
"""Independent correctness/end-to-end timing check against the indexed source prefixes.

Usage: python3 verify-codex.py /absolute/eventq /absolute/commands.eq
Reads private source data locally, emits only aggregate measurements, never payloads.
"""
import collections
import hashlib
import json
import pathlib
import statistics
import struct
import subprocess
import sys
import time


def source_rows(header):
    rows = []
    for source in header["sources"]:
        digest = hashlib.sha256()
        remaining = source["bytes"]
        with open(source["path"], "rb") as stream:
            while remaining:
                raw = stream.readline(remaining)
                if not raw:
                    raise ValueError("source prefix was truncated")
                remaining -= len(raw)
                digest.update(raw)
                if not raw.strip():
                    continue
                event = json.loads(raw)
                payload = event.get("payload") or {}
                item = payload.get("item") or {}
                if (event.get("type"), payload.get("type"), item.get("type")) != (
                    "event_msg", "item_completed", "CommandExecution"
                ):
                    continue
                duration = item.get("duration")
                ms = None
                if duration is not None:
                    ms = duration["secs"] * 1000 + duration["nanos"] // 1_000_000
                rows.append((item.get("cwd", ""), item.get("exit_code"), ms))
        if digest.hexdigest() != source["sha256"]:
            raise ValueError("source prefix changed since indexing")
    return rows


def main():
    binary, index = sys.argv[1:]
    data = pathlib.Path(index).read_bytes()
    if data[:8] != b"EVENTQ01" or hashlib.sha256(data[:-32]).digest() != data[-32:]:
        raise ValueError("invalid index")
    header_size, = struct.unpack_from("<I", data, 8)
    header = json.loads(data[12:12 + header_size])
    if header["ignored_tails"]:
        raise ValueError("this independent check requires complete source prefixes")
    start = time.perf_counter()
    rows = source_rows(header)
    raw_ms = (time.perf_counter() - start) * 1000
    failures = [row for row in rows if row[1] is not None and row[1] != 0]
    slow = [row for row in rows if row[2] is not None and row[2] >= 30_000]
    both = [row for row in slow if row[1] is not None and row[1] != 0]
    cases = [
        ([], len(rows)),
        (["--where", "exit_code != 0"], len(failures)),
        (["--where", "duration_ms >= 30000"], len(slow)),
        (["--where", "duration_ms >= 30000 AND exit_code != 0"], len(both)),
    ]
    timings = []
    for flags, expected in cases:
        start = time.perf_counter()
        result = json.loads(subprocess.check_output([binary, "query", "--json", "--count", *flags, index]))
        timings.append((time.perf_counter() - start) * 1000)
        assert result["matches"] == expected, (flags, expected, result["matches"])
    result = json.loads(subprocess.check_output([binary, "query", "--json", "--where", "exit_code != 0", "--group", "cwd", "--limit", "1000000", index]))
    actual = {group["key"]: group["count"] for group in result["groups"]}
    assert actual == dict(collections.Counter(row[0] for row in failures))
    result = json.loads(subprocess.check_output([binary, "query", "--json", "--count", "--sum", "duration_ms", index]))
    assert result["sum"] == sum(row[2] for row in rows if row[2] is not None)
    result = json.loads(subprocess.check_output([binary, "query", "--json", "--top", "duration_ms", "--limit", "5", index]))
    assert [row["duration_ms"] for row in result["rows"]] == sorted((row[2] for row in rows if row[2] is not None), reverse=True)[:5]
    print(json.dumps({
        "source_bytes": sum(source["bytes"] for source in header["sources"]),
        "source_files": len(header["sources"]), "index_bytes": len(data),
        "command_rows": len(rows), "nonzero_exits": len(failures), "slow_30s": len(slow),
        "slow_nonzero": len(both), "source_hashes_verified": True,
        "independent_json_parse_ms": round(raw_ms, 3),
        "query_process_ms": [round(v, 3) for v in timings],
        "median_query_process_ms": round(statistics.median(timings), 3),
        "checks": ["counts", "grouped_counts", "sum", "top5", "source_prefix_hashes"],
    }, indent=2))


if __name__ == "__main__":
    main()
