package e2e_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Regression tests for the independent review's findings, one per finding.

// F1: a task that reaches its upstream only through argv (no digested read)
// still re-runs when the upstream changes, including after a crash that
// separates the upstream's effect from the task's run. The receipt records
// the upstream state the task ran against.
func TestF1UpstreamThroughArgvReRuns(t *testing.T) {
	src := func(content string) string {
		return fmt.Sprintf(`resource "file" "input" {
  path    = "input.txt"
  content = %q
}

task "exec" "sorted" {
  command = ["sort", file.input.path]
  stdout  = "sorted.txt"
}
`, content)
	}
	w := newWorkspace(t)
	w.write("main.wb.hcl", src("b\na\n"))
	w.mustWB("apply")
	equal(t, "sorted.txt", w.read("sorted.txt"), "a\nb\n")

	w.write("main.wb.hcl", src("d\nc\n"))
	w.mustWB("plan", "-out", "plan.json")
	contains(t, w.mustWB("apply", "-plan", "plan.json").stdout, "exec.sorted  ran")
	equal(t, "sorted.txt", w.read("sorted.txt"), "c\nd\n")

	w.write("main.wb.hcl", src("f\ne\n"))
	equal(t, "crash exit", w.wbEnv([]string{"WB_FAULT=file.input:crash-after"}, "apply").code, 70)
	contains(t, w.mustWB("plan").stdout,
		"= file.input",
		"> exec.sorted  run        upstream file.input changed since last run")
	w.mustWB("apply")
	equal(t, "sorted.txt", w.read("sorted.txt"), "e\nf\n")
}

// F5: a saved plan that can no longer be checked (the source stopped
// compiling) is refused as stale, with nothing applied.
func TestF5UnverifiableSavedPlanIsStale(t *testing.T) {
	w := applied(t, "v1.wb.hcl")
	w.use("v2.wb.hcl")
	w.mustWB("plan", "-out", "plan.json")
	w.write("main.wb.hcl", "resource \"file\" {\n")

	r := w.wb("apply", "-plan", "plan.json")
	equal(t, "exit code", r.code, 3)
	contains(t, r.stderr, "the saved plan cannot be verified against the workspace; nothing was applied.")
	equal(t, "input.txt", w.read("input.txt"), v1Input)
}

// F6: erasing content to "" still shows the removed lines.
func TestF6PlanShowsErasedContent(t *testing.T) {
	w := newWorkspace(t)
	src := "resource \"file\" \"a\" {\n  path    = \"a.txt\"\n  content = %q\n}\n"
	w.write("main.wb.hcl", fmt.Sprintf(src, "one\ntwo\n"))
	w.mustWB("apply")
	w.write("main.wb.hcl", fmt.Sprintf(src, ""))
	contains(t, w.mustWB("plan").stdout, "a.txt: 8 bytes", "-> 0 bytes", "      - one\n      - two")
}

// F7: a task's first run over a file wb never wrote says so in the plan.
func TestF7PlanNotesReplacingAFileWbDidNotWrite(t *testing.T) {
	w := newWorkspace(t)
	w.write("report.txt", "hand-written\n")
	w.write("data.txt", "x\n")
	w.write("main.wb.hcl", `task "exec" "report" {
  command = ["tr", "a-z", "A-Z"]
  stdin   = "data.txt"
  stdout  = "report.txt"
}
`)
	contains(t, w.mustWB("plan").stdout, "note: replaces existing report.txt, which no earlier successful run of this task wrote")
	w.mustWB("apply")
	contains(t, w.mustWB("plan").stdout, "No changes.")
}

// F8: replacing a file keeps its permission bits.
func TestF8ReplaceKeepsPermissions(t *testing.T) {
	w := applied(t, "v1.wb.hcl")
	for rel, mode := range map[string]os.FileMode{"input.txt": 0o600, "output.txt": 0o640} {
		if err := os.Chmod(filepath.Join(w.dir, rel), mode); err != nil {
			t.Fatal(err)
		}
	}
	w.use("v2.wb.hcl")
	w.mustWB("apply")
	equal(t, "output.txt rewritten", w.read("output.txt"), v2Output)
	for rel, mode := range map[string]os.FileMode{"input.txt": 0o600, "output.txt": 0o640} {
		info, err := os.Stat(filepath.Join(w.dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		equal(t, rel+" mode", info.Mode().Perm(), mode)
	}
}
