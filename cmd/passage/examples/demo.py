#!/usr/bin/env python3
"""Offline six-phase rehearsal. Synthetic judgments; actual Go test failure/repair."""
import json
import pathlib
import subprocess
import tempfile

SOURCE = pathlib.Path(__file__).resolve().parents[3]


def main():
    out = pathlib.Path(tempfile.mkdtemp(prefix="passage-demo-"))
    root = out / "subject"
    root.mkdir()
    binary, work, evidence = out / "passage", out / "work.json", out / "evidence.txt"
    commands = []

    def run(args, expected=0, cwd=root):
        result = subprocess.run([str(x) for x in args], cwd=cwd, text=True,
                                capture_output=True, timeout=120)
        commands.append(dict(argv=[str(x) for x in args], exit=result.returncode,
                             stdout=result.stdout, stderr=result.stderr))
        (out / "commands.json").write_text(json.dumps(commands, indent=2))
        assert result.returncode == expected, commands[-1]
        return result.stdout

    run(["go", "build", "-o", binary, "./cmd/passage"], cwd=SOURCE)
    for filename, body in {"discovery.md": "Double an integer; multiplication is sufficient.",
                           "design.md": "Use n * 2; no dependency.",
                           "plan.md": "Twice(3) must equal 6.",
                           "go.mod": "module example.invalid/demo\n\ngo 1.26\n",
                           "twice.go": "package demo\nfunc Twice(n int) int { return n + 1 }\n",
                           "twice_test.go": 'package demo\nimport "testing"\nfunc TestTwice(t *testing.T) { if Twice(3)!=6 { t.Fatal("wrong result") } }\n'}.items():
        (root / filename).write_text(body)
    for args in [["init", "-b", "demo"], ["config", "user.name", "Passage demo"],
                 ["config", "user.email", "demo@example.invalid"], ["add", "."],
                 ["commit", "-m", "initial example"]]:
        run(["git", *args])
    run([binary, "init", "--work", work, "--root", root,
         "--contract", SOURCE / "cmd/passage/examples/contract.json"])

    def mutation(action, *args, expected=0):
        revision = json.loads(work.read_text())["events"]
        return run([binary, action, "--work", work, "--expect", len(revision),
                    "--by", "scripted-demo", "--note", "Offline fixture, not independent review",
                    *args], expected=expected)

    def record(requirement, verdict="pass"):
        mutation("record", "--requirement", requirement, "--verdict", verdict,
                 "--evidence", evidence)

    run([binary, "check", "--work", work], expected=1)
    for requirement in ["sources-assessed", "design-accepted", "plan-accepted"]:
        evidence.write_text("Synthetic reviewer assessed the document for this demo.\n")
        record(requirement)
        mutation("advance")

    evidence.write_text(run(["go", "test", "./..."], expected=1))
    record("checks-passed", "fail")
    mutation("advance", expected=1)
    (root / "twice.go").write_text("package demo\nfunc Twice(n int) int { return n * 2 }\n")
    run(["git", "add", "."])
    run(["git", "commit", "-m", "fix doubling"])
    evidence.write_text(run(["go", "test", "./..."]))
    record("checks-passed")
    mutation("advance")
    record("result-accepted")
    mutation("advance")
    # No real release: the fixture explicitly records a simulated external result.
    evidence.write_text("SIMULATED RELEASE ONLY. No Gate invocation, merge or deployment.\n")
    record("release-observed")
    mutation("advance")
    complete = json.loads(run([binary, "status", "--work", work]))
    assert complete["phase"] == "complete" and complete["ready"]
    (root / "design.md").write_text("New requirement changes the design.\n")
    drift = json.loads(run([binary, "status", "--work", work]))
    assert not drift["ready"] and "reopen design" in drift["problems"][0]
    mutation("reopen", "--phase", "design")
    reopened = json.loads(run([binary, "status", "--work", work]))
    assert reopened["phase"] == "design" and not reopened["ready"]
    summary = dict(complete=complete, changed_input=drift, reopened=reopened,
                   limitations="Scripted judgments and release; no Rooms, agents, cloud or merge.")
    (out / "summary.json").write_text(json.dumps(summary, indent=2))
    print(json.dumps(summary, indent=2))
    print(f"Evidence retained: {out}")


if __name__ == "__main__":
    main()
