package e2e_test

import (
	"strings"
	"testing"
)

// Rule 3 is a plan-time projection, so the plan proposes the run. Apply
// re-checks with real observations: the upstream converged back to exactly
// what the receipt recorded, so the task is not re-run (early cutoff).
func TestUpstreamConvergingBackSkipsTheRun(t *testing.T) {
	w := applied(t, "v1.wb.hcl")
	w.appendTo("input.txt", "edited by hand\n")

	contains(t, w.mustWB("plan").stdout,
		"~ file.input  update",
		"      - edited by hand",
		"> exec.shout  run        upstream file.input changes")
	contains(t, w.mustWB("apply").stdout,
		"exec.shout  unchanged  fresh once upstream applied",
		"Apply complete: 0 created, 1 updated, 0 ran, 0 failed, 0 skipped.",
		"Observed after apply: converged")
	equal(t, "input.txt", w.read("input.txt"), v1Input)
	equal(t, "tr runs", w.runs(), 1)
}

// Each declared staleness rule, one at a time, against a converged
// workspace: exactly the named reason appears and the task runs once.
func TestTaskStalenessRules(t *testing.T) {
	const src = `
task "exec" "shout" {
  command = ["tr", "a-z", "A-Z"]
  stdin   = "data.txt"  # an unmanaged input: no block owns it
  stdout  = "out.txt"
}
`
	cases := []struct {
		name   string
		mutate func(w *workspace)
		reason string
	}{
		{"unmanaged input edited", func(w *workspace) { w.write("data.txt", "changed\n") },
			"input data.txt changed since last run"},
		{"output edited by hand", func(w *workspace) { w.write("out.txt", "HAND EDIT\n") },
			"output out.txt was modified after the last run"},
		{"output deleted", func(w *workspace) { w.remove("out.txt") },
			"output out.txt is missing"},
		{"configuration changed", func(w *workspace) { w.write("main.wb.hcl", strings.ReplaceAll(src, `"a-z", "A-Z"`, `"a-y", "A-Y"`)) },
			`config command: ["tr","a-z","A-Z"] -> ["tr","a-y","A-Y"]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorkspace(t)
			w.write("main.wb.hcl", src)
			w.write("data.txt", "data\n")
			w.mustWB("apply")
			contains(t, w.mustWB("plan").stdout, "No changes.")

			tc.mutate(w)
			contains(t, w.mustWB("plan").stdout, "> exec.shout  run        "+tc.reason)
			w.mustWB("apply")
			equal(t, "tr runs", w.runs(), 2)
			contains(t, w.mustWB("plan").stdout, "No changes.")
		})
	}
}
