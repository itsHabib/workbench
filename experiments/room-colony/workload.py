"""Fixed route-planning oracle. Candidate code returns an ordering, never a score."""
import hashlib
import json
from pathlib import Path
import random
import subprocess
import sys
import tempfile

BASELINE = "def plan(points):\n    return list(range(len(points)))\n"
RUNNER = """import importlib.util, json, sys
spec = importlib.util.spec_from_file_location('candidate', sys.argv[1])
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
print(json.dumps(module.plan(json.load(sys.stdin))))
"""


def digest(text):
    return hashlib.sha256(text.encode()).hexdigest()


def maps(split):
    seeds = range(10, 18) if split == "development" else range(1001, 1025)
    result = [[], [[0, 0]], [[3, 3], [3, 3], [0, 0]]]
    for seed in seeds:
        rng = random.Random(seed)
        result.append([[rng.randrange(-50, 51), rng.randrange(-50, 51)] for _ in range(12 + seed % 24)])
    return result


def distance(points, order):
    if not isinstance(order, list) or any(type(i) is not int for i in order):
        raise ValueError("route must be a list of integer indices")
    if sorted(order) != list(range(len(points))):
        raise ValueError("route must visit every index exactly once")
    route = [[0, 0], *[points[i] for i in order], [0, 0]]
    return sum(abs(a[0] - b[0]) + abs(a[1] - b[1]) for a, b in zip(route, route[1:]))


def assess(source, split):
    """Compile, execute with a deadline, and score outside the candidate process.

    This is a cooperative-code experiment, not a hostile Python sandbox.
    The enclosing Room is disposable and has no model credentials.
    """
    rows = []
    with tempfile.TemporaryDirectory() as tmp:
        candidate = Path(tmp) / "candidate.py"
        candidate.write_text(source)
        try:
            compile(source, "candidate.py", "exec")
            for points in maps(split):
                with tempfile.TemporaryFile() as stdout, tempfile.TemporaryFile() as stderr:
                    run = subprocess.run([sys.executable, "-I", "-c", RUNNER, str(candidate)],
                                         input=json.dumps(points).encode(), stdout=stdout, stderr=stderr,
                                         timeout=3, cwd=tmp)
                    stdout.seek(0)
                    raw = stdout.read(65537)
                if run.returncode or len(raw) > 65536:
                    raise ValueError("candidate failed or returned excessive output")
                cost = distance(points, json.loads(raw))
                rows.append({"points": len(points), "baseline": distance(points, list(range(len(points)))),
                             "candidate": cost})
        except (ValueError, SyntaxError, subprocess.TimeoutExpired, TypeError, IndexError) as error:
            return {"sha256": digest(source), "split": split, "valid": False, "error": str(error), "cases": rows}
    base = sum(row["baseline"] for row in rows)
    cost = sum(row["candidate"] for row in rows)
    return {"sha256": digest(source), "split": split, "valid": True, "cases": rows,
            "baseline_distance": base, "candidate_distance": cost, "ratio": cost / base,
            "qualified": cost <= base * 0.85}
