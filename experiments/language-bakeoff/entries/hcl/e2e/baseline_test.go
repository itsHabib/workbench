package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The direct-tools baseline, driven through the same scenarios with the
// same witness. It passes cases 1-3 and 5 on its own terms; the assertions
// for case 4 record what it actually does (overwrite the hand edit without
// showing it) rather than failing it for lacking a plan.
func TestBaselineOnTheCommonWorkload(t *testing.T) {
	w := newWorkspace(t)
	desired := t.TempDir()
	put := func(input, notes string) {
		writeFile(t, filepath.Join(desired, "input.txt"), input)
		writeFile(t, filepath.Join(desired, "notes.txt"), notes)
	}
	run := func() int {
		cmd := exec.Command("sh", filepath.Join("..", "baseline", "run.sh"), desired, w.dir)
		cmd.Env = []string{"PATH=" + w.path}
		out, err := cmd.CombinedOutput()
		t.Logf("baseline:\n%s", out)
		if err != nil && cmd.ProcessState == nil {
			t.Fatal(err)
		}
		return cmd.ProcessState.ExitCode()
	}

	// 1. Initial run.
	put(v1Input, v1Notes)
	equal(t, "exit", run(), 0)
	equal(t, "output.txt", w.read("output.txt"), v1Output)
	equal(t, "tr runs", w.runs(), 1)

	// 2. Unchanged re-run: no effects.
	before := w.identities(artifacts...)
	equal(t, "exit", run(), 0)
	sameMap(t, "identities", w.identities(artifacts...), before)
	equal(t, "tr runs", w.runs(), 1)

	// 3. Input change: the transform re-runs, notes untouched.
	put(v2Input, v1Notes)
	equal(t, "exit", run(), 0)
	equal(t, "output.txt", w.read("output.txt"), v2Output)
	equal(t, "tr runs", w.runs(), 2)
	equal(t, "notes identity", w.identity("notes.txt"), before["notes.txt"])

	// 4. External modification: there is no reviewed plan to go stale. The
	// next run silently restores the desired input; the restored content
	// matches the stamp, so the transform correctly does not re-run.
	w.appendTo("input.txt", "edited by hand\n")
	equal(t, "exit", run(), 0)
	equal(t, "input.txt (hand edit gone)", w.read("input.txt"), v2Input)
	equal(t, "tr runs", w.runs(), 2)

	// 5. Failure then retry: set -e stops at the failed transform, so the
	// unrelated notes wait for the retry; the input sync is not repeated.
	put(v4Input, v4Notes)
	w.failTr(true)
	if run() == 0 {
		t.Fatal("baseline succeeded with a failing tr")
	}
	equal(t, "input.txt", w.read("input.txt"), v4Input)
	equal(t, "notes.txt (not reached)", w.read("notes.txt"), v1Notes)
	input := w.identity("input.txt")
	w.failTr(false)
	equal(t, "exit", run(), 0)
	equal(t, "output.txt", w.read("output.txt"), v4Output)
	equal(t, "notes.txt", w.read("notes.txt"), v4Notes)
	equal(t, "input identity", w.identity("input.txt"), input)
	equal(t, "tr runs", w.runs(), 3)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
