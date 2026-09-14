"""Build a frozen observation readout; observed success grants no authority."""

import argparse
import json
from pathlib import Path
import re
import sys


def unique_object(pairs):
    """Reject ambiguous JSON instead of silently discarding repeated fields."""
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError(f"duplicate JSON field: {key}")
        result[key] = value
    return result


def reject_constant(value):
    raise ValueError(f"invalid JSON constant: {value}")


def summarize_check(check, location):
    if not isinstance(check, dict):
        raise ValueError(f"{location} must be an object")
    for field in ("name", "context", "conclusion", "state", "status"):
        value = check.get(field)
        if value is not None and not isinstance(value, str):
            raise ValueError(f"{location}.{field} must be a string or null")

    name = check.get("name") or check.get("context")
    if not name:
        raise ValueError(f"{location} needs a nonempty name or context")
    outcome = (
        check.get("conclusion")
        or check.get("state")
        or check.get("status")
        or "UNKNOWN"
    )
    return {"name": name, "outcome": outcome}


def summarize_pr(pr, location):
    if not isinstance(pr, dict):
        raise ValueError(f"{location} must be an object")
    number = pr.get("number")
    if type(number) is not int or number <= 0:
        raise ValueError(f"{location}.number must be a positive integer")
    head = pr.get("headRefOid")
    if not isinstance(head, str) or re.fullmatch(r"[0-9a-fA-F]{40}", head) is None:
        raise ValueError(f"{location}.headRefOid must be a 40-digit hex revision")
    rollup = pr.get("statusCheckRollup")
    if not isinstance(rollup, list):
        raise ValueError(f"{location}.statusCheckRollup must be an array")

    checks = [
        summarize_check(check, f"{location}.statusCheckRollup[{index}]")
        for index, check in enumerate(rollup)
    ]
    successful = None
    if checks:
        successful = all(check["outcome"] == "SUCCESS" for check in checks)
    return {
        "number": number,
        "head": head,
        "checks": checks,
        "all_checks_successful": successful,
    }


def build_report(value):
    """Validate the entire input and return its compact, ordered readout."""
    if not isinstance(value, dict):
        raise ValueError("input must be an object")
    observed_at = value.get("observed_at")
    if not isinstance(observed_at, str) or not observed_at.strip():
        raise ValueError("observed_at must be a nonempty string")
    pull_requests = value.get("pull_requests")
    if not isinstance(pull_requests, list):
        raise ValueError("pull_requests must be an array")

    summaries = []
    numbers = set()
    for index, pr in enumerate(pull_requests):
        summary = summarize_pr(pr, f"pull_requests[{index}]")
        number = summary["number"]
        if number in numbers:
            raise ValueError(f"duplicate pull request number: {number}")
        numbers.add(number)
        summaries.append(summary)
    return {"observed_at": observed_at, "pull_requests": summaries}


def write_report(output, payload):
    """Create output exclusively, or leave identical existing bytes untouched."""
    try:
        with output.open("xb") as destination:
            destination.write(payload)
    except FileExistsError:
        if output.read_bytes() != payload:
            raise ValueError(f"refusing to overwrite conflicting output: {output}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("input", type=Path, help="frozen observation JSON")
    parser.add_argument("output", type=Path, help="destination report JSON")
    args = parser.parse_args()
    try:
        value = json.loads(
            args.input.read_text(encoding="utf-8"),
            object_pairs_hook=unique_object,
            parse_constant=reject_constant,
        )
        report = build_report(value)
        payload = (json.dumps(report, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
        write_report(args.output, payload)
    except (OSError, ValueError) as error:
        print(f"report: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
