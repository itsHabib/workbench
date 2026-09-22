"""Guest-owned author and verifier loops. The host never selects a candidate."""
import argparse
import json
import os
from pathlib import Path
import signal
import time
import urllib.error
import urllib.request

from workload import BASELINE, assess, digest


class Mail:
    def __init__(self, url, token):
        self.url, self.token = url.rstrip("/"), token

    def request(self, method, key, value=None):
        body = None if value is None else json.dumps(value).encode()
        req = urllib.request.Request(self.url + "/" + key, data=body, method=method,
                                     headers={"Authorization": "Bearer " + self.token,
                                              "Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req, timeout=240) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            if method == "GET" and error.code == 404:
                error.close()
                return None
            raise

    def get(self, key):
        return self.request("GET", "artifacts/" + key)

    def put(self, key, value):
        return self.request("PUT", "artifacts/" + key, value)

    def wait(self, key):
        deadline = time.monotonic() + 480
        while time.monotonic() < deadline:
            result = self.get(key)
            if result is not None:
                return result
            time.sleep(1)
        raise TimeoutError(key)


def author(mail):
    best, previous = BASELINE, None
    best_ratio = 1.0
    for round_id in (1, 2):
        key = f"candidate-{round_id}"
        candidate = mail.get(key)
        if candidate is None:
            candidate = mail.request("POST", f"generate/{round_id}", {"source": best, "feedback": previous})
            if digest(candidate["source"]) != candidate["sha256"]:
                raise ValueError("inference source digest mismatch")
            mail.put(key, candidate)
            # Failure after durable handoff, before consuming the receipt.
            # The replacement process must not regenerate this candidate.
            if round_id == 1:
                mail.put("interruption", {"after": key, "signal": "SIGKILL"})
                os.kill(os.getpid(), signal.SIGKILL)
        previous = mail.wait(f"review-{round_id}")
        if previous["sha256"] != candidate["sha256"]:
            raise ValueError("review is for a different candidate")
        if previous["valid"] and previous["ratio"] < best_ratio:
            best, best_ratio = candidate["source"], previous["ratio"]
    mail.put("selected", {"source": best, "sha256": digest(best)})
    result = mail.wait("qualification")
    if result["sha256"] != digest(best):
        raise ValueError("qualification is for a different candidate")
    mail.put("promotion", {"sha256": digest(best), "promoted": result.get("qualified", False),
                            "qualification_sha256": digest(json.dumps(result, sort_keys=True))})
    mail.put("author-done", {"selected": digest(best)})


def verifier(mail):
    # A deliberately wrong implementation is an oracle negative control.
    if mail.get("negative-control") is None:
        negative = assess("def plan(points):\n    return []\n", "development")
        if negative["valid"]:
            raise ValueError("oracle accepted missing deliveries")
        mail.put("negative-control", negative)
    for round_id in (1, 2):
        if mail.get(f"review-{round_id}") is not None:
            continue
        candidate = mail.wait(f"candidate-{round_id}")
        if candidate["sha256"] != digest(candidate["source"]):
            raise ValueError("candidate digest mismatch")
        mail.put(f"review-{round_id}", assess(candidate["source"], "development"))
    if mail.get("qualification") is None:
        selected = mail.wait("selected")
        if selected["sha256"] != digest(selected["source"]):
            raise ValueError("selection digest mismatch")
        mail.put("qualification", assess(selected["source"], "heldout"))
    mail.put("verifier-done", {"qualification": True})


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("role", choices=["author", "verifier"])
    parser.add_argument("url")
    parser.add_argument("token")
    args = parser.parse_args()
    identity = {"role": args.role, "pid": os.getpid(), "hostname": os.uname().nodename,
                "boot_id": Path("/proc/sys/kernel/random/boot_id").read_text().strip()}
    print(json.dumps({"guest_identity": identity}), flush=True)
    mail = Mail(args.url, args.token)
    {"author": author, "verifier": verifier}[args.role](mail)
    print(json.dumps({"completed": args.role}), flush=True)


if __name__ == "__main__":
    main()
