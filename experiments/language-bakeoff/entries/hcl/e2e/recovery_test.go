package e2e_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/evidence"
)

// A crash after a task's effect but before its receipt: the journal tells a
// fresh caller the outcome is unknown, and the task re-runs. That is
// at-least-once, not exactly-once, and the test pins it.
func TestInterruptedTaskReRunsFromEvidence(t *testing.T) {
	w := applied(t, "v1.wb.hcl")
	w.use("v2.wb.hcl")

	r := w.wbEnv([]string{"WB_FAULT=exec.shout:crash-after"}, "apply")
	equal(t, "exit code", r.code, 70)
	equal(t, "output.txt (effect happened)", w.read("output.txt"), v2Output)
	equal(t, "receipt event", w.latest("exec.shout").Event, evidence.Started)
	equal(t, "tr runs", w.runs(), 2)

	contains(t, w.mustWB("log").stdout, "exec.shout  run     INTERRUPTED  started, no result recorded")
	contains(t, w.mustWB("plan").stdout,
		"> exec.shout  run        run 2 was interrupted (started, no result recorded); outcome unknown")
	w.mustWB("apply")
	equal(t, "tr runs (repeated once)", w.runs(), 3)
	contains(t, w.mustWB("plan").stdout, "No changes.")
}

// A crash after a resource's effect needs no evidence to recover: the next
// plan observes the file directly and sees it converged.
func TestInterruptedResourceRecoversByObservation(t *testing.T) {
	w := applied(t, "v1.wb.hcl")
	w.use("v2.wb.hcl")

	r := w.wbEnv([]string{"WB_FAULT=file.input:crash-after"}, "apply")
	equal(t, "exit code", r.code, 70)
	equal(t, "input.txt (effect happened)", w.read("input.txt"), v2Input)
	equal(t, "file.input last event", w.latest("file.input").Event, evidence.Started)

	contains(t, w.mustWB("plan").stdout,
		"= file.input  unchanged",
		"> exec.shout  run        upstream file.input changed since last run",
		"input input.txt changed since last run")
	w.mustWB("apply")
	equal(t, "output.txt", w.read("output.txt"), v2Output)
}

// Errors reach the terminal and the journal; they name workspace-relative
// paths, never the workspace's absolute location.
func TestErrorsNameRelativePaths(t *testing.T) {
	w := newWorkspace(t)
	w.write("main.wb.hcl", `task "exec" "shout" {
  command = ["tr", "a-z", "A-Z"]
  stdin   = "missing.txt"
  stdout  = "out.txt"
}
`)
	r := w.wb("apply")
	equal(t, "exit code", r.code, 1)
	contains(t, r.stdout, "exec.shout  FAILED     open missing.txt: no such file or directory")
	journal := w.read(filepath.Join(".wb", "journal.jsonl"))
	resolved, err := filepath.EvalSymlinks(w.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, abs := range []string{w.dir, resolved} {
		for what, text := range map[string]string{"stdout": r.stdout, "stderr": r.stderr, "journal": journal} {
			if strings.Contains(text, abs) {
				t.Errorf("%s leaks the absolute workspace path:\n%s", what, text)
			}
		}
	}
}

// Source that does not compile is reported with file:line and a snippet by
// HCL's diagnostic writer, and nothing runs.
func TestCompileErrorPointsAtSource(t *testing.T) {
	w := newWorkspace(t)
	w.write("main.wb.hcl", `resource "file" "input" {
  path    = "input.txt"
  content = upper("hi")
}
`)
	r := w.wb("apply")
	equal(t, "exit code", r.code, 2)
	contains(t, r.stderr, "Error: Function calls are not supported", "on main.wb.hcl line 3", `content = upper("hi")`)
	equal(t, "files", len(w.listing()), 1)
}
