package e2e_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapter"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/evidence"
)

const (
	v1Input  = "hello workbench\nsecond line stays\n"
	v1Output = "HELLO WORKBENCH\nSECOND LINE STAYS\n"
	v1Notes  = "owner: demo\n"
	v2Input  = "hello bakeoff\nsecond line stays\n"
	v2Output = "HELLO BAKEOFF\nSECOND LINE STAYS\n"
	v3Output = "HELLO REVIEWER\nSECOND LINE STAYS\n"
	v4Input  = "hello retry\nsecond line stays\n"
	v4Output = "HELLO RETRY\nSECOND LINE STAYS\n"
	v4Notes  = "owner: demo\nreviewed: yes\n"
)

var artifacts = []string{"input.txt", "output.txt", "notes.txt"}

// Case 1: source -> normalized intent -> proposed changes -> apply ->
// observed outcome, in a freshly created disposable directory.
func TestCase1InitialPlanAndApply(t *testing.T) {
	w := newWorkspace(t)
	w.use("v1.wb.hcl")

	var in struct {
		Nodes []struct {
			Address string
			Kind    string
			Deps    []string
			Owns    []string
			Reads   []string
		}
	}
	if err := json.Unmarshal([]byte(w.mustWB("compile").stdout), &in); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, n := range in.Nodes {
		order = append(order, n.Address)
	}
	equal(t, "node order", reflect.DeepEqual(order, []string{"file.input", "exec.shout", "file.notes"}), true)
	task := in.Nodes[1]
	equal(t, "exec.shout kind", task.Kind, "task")
	equal(t, "exec.shout deps", reflect.DeepEqual(task.Deps, []string{"file.input"}), true)
	equal(t, "exec.shout reads", reflect.DeepEqual(task.Reads, []string{"input.txt"}), true)
	equal(t, "exec.shout owns", reflect.DeepEqual(task.Owns, []string{"output.txt"}), true)
	equal(t, "file.input kind", in.Nodes[0].Kind, "resource")

	p := w.mustWB("plan")
	contains(t, p.stdout,
		"+ file.input  create     input.txt: 34 bytes",
		"      + hello workbench",
		"> exec.shout  run        never run",
		"+ file.notes  create     notes.txt: 12 bytes",
		"Plan: 2 to create, 0 to update, 1 to run, 0 unchanged.")
	equal(t, "files after plan", reflect.DeepEqual(w.listing(), []string{"main.wb.hcl"}), true)

	a := w.mustWB("apply")
	contains(t, a.stdout,
		"Apply complete: 2 created, 0 updated, 1 ran, 0 failed, 0 skipped.",
		"Observed after apply: converged")
	equal(t, "input.txt", w.read("input.txt"), v1Input)
	equal(t, "output.txt", w.read("output.txt"), v1Output)
	equal(t, "notes.txt", w.read("notes.txt"), v1Notes)
	equal(t, "tr runs", w.runs(), 1)

	receipt := w.latest("exec.shout")
	equal(t, "receipt event", receipt.Event, evidence.Succeeded)
	equal(t, "receipt input digest", receipt.Inputs["input.txt"], adapter.DigestBytes([]byte(v1Input)))
	equal(t, "receipt output digest", receipt.Outputs["output.txt"], adapter.DigestBytes([]byte(v1Output)))
}

// Case 2: an unchanged re-apply performs no effects at all.
func TestCase2UnchangedReapply(t *testing.T) {
	w := applied(t, "v1.wb.hcl")
	before := w.identities(artifacts...)
	entries := len(w.journal())

	contains(t, w.mustWB("plan").stdout, "No changes. 3 unchanged.")
	contains(t, w.mustWB("apply").stdout, "Nothing to apply.")

	sameMap(t, "identities", w.identities(artifacts...), before)
	equal(t, "journal entries", len(w.journal()), entries)
	equal(t, "tr runs", w.runs(), 1)
}

// Case 3: an input change plans the update and the downstream re-run, and
// leaves the unrelated resource alone.
func TestCase3InputChangeDownstreamPlan(t *testing.T) {
	w := applied(t, "v1.wb.hcl")
	before := w.identities(artifacts...)
	w.use("v2.wb.hcl")

	p := w.mustWB("plan")
	contains(t, p.stdout,
		"~ file.input  update     input.txt: 34 bytes",
		"      - hello workbench\n      + hello bakeoff\n        second line stays",
		"> exec.shout  run        upstream file.input changes",
		"= file.notes  unchanged",
		"Plan: 0 to create, 1 to update, 1 to run, 1 unchanged.")
	sameMap(t, "identities after plan", w.identities(artifacts...), before)

	w.mustWB("apply")
	equal(t, "output.txt", w.read("output.txt"), v2Output)
	equal(t, "tr runs", w.runs(), 2)
	equal(t, "notes identity", w.identity("notes.txt"), before["notes.txt"])
}

// Case 4: a reviewed plan goes stale when the workspace moves; apply
// refuses it before any effect and names what moved.
func TestCase4ExternalModificationRefusesStalePlan(t *testing.T) {
	w := applied(t, "v1.wb.hcl")
	w.use("v3.wb.hcl")
	w.mustWB("plan", "-out", "plan.json")

	w.appendTo("input.txt", "edited by hand\n")
	edited := w.read("input.txt")
	output := w.identity("output.txt")
	entries := len(w.journal())

	r := w.wb("apply", "-plan", "plan.json")
	equal(t, "exit code", r.code, 3)
	contains(t, r.stderr,
		"is stale; nothing was applied.",
		"file.input: state was sha256:",
		"exec.shout: input input.txt was sha256:")
	equal(t, "input.txt keeps the hand edit", w.read("input.txt"), edited)
	equal(t, "output identity", w.identity("output.txt"), output)
	equal(t, "journal entries", len(w.journal()), entries)
	equal(t, "tr runs", w.runs(), 1)

	// Re-planning makes the overwrite of the hand edit visible before it
	// happens; that reviewed plan then applies.
	contains(t, w.mustWB("plan", "-out", "plan.json").stdout, "      - edited by hand", "      + hello reviewer")
	contains(t, w.mustWB("apply", "-plan", "plan.json").stdout, "still matches the workspace")
	equal(t, "output.txt", w.read("output.txt"), v3Output)
	equal(t, "tr runs", w.runs(), 2)
}

// Case 4, source edition: editing the source after planning also stales it.
func TestCase4SourceEditAfterPlanRefusesStalePlan(t *testing.T) {
	w := applied(t, "v1.wb.hcl")
	w.use("v2.wb.hcl")
	w.mustWB("plan", "-out", "plan.json")
	w.use("v3.wb.hcl")

	r := w.wb("apply", "-plan", "plan.json")
	equal(t, "exit code", r.code, 3)
	contains(t, r.stderr, "source changed since plan")
	equal(t, "input.txt", w.read("input.txt"), v1Input)
}

// Case 5: an injected failure keeps completed effects (including the
// unrelated one declared after the failure), and the retry repeats only
// what did not complete.
func TestCase5PartialFailureThenRetry(t *testing.T) {
	w := applied(t, "v1.wb.hcl")
	w.use("v4.wb.hcl")
	output := w.identity("output.txt")

	r := w.wbEnv([]string{"WB_FAULT=exec.shout"}, "apply")
	equal(t, "exit code", r.code, 1)
	contains(t, r.stdout,
		"exec.shout  FAILED     injected fault before effect",
		"file.notes  updated",
		"Apply incomplete: 0 created, 2 updated, 0 ran, 1 failed, 0 skipped.")
	equal(t, "input.txt", w.read("input.txt"), v4Input)
	equal(t, "notes.txt", w.read("notes.txt"), v4Notes)
	equal(t, "output identity", w.identity("output.txt"), output)
	equal(t, "tr runs", w.runs(), 1)

	done := w.identities("input.txt", "notes.txt")
	fileEntries := w.entriesFor("file.input", "file.notes")
	contains(t, w.mustWB("plan").stdout,
		"= file.input  unchanged",
		"> exec.shout  run        last attempt failed (run 2): injected fault before effect",
		"= file.notes  unchanged",
		"Plan: 0 to create, 0 to update, 1 to run, 2 unchanged.")

	w.mustWB("apply")
	equal(t, "output.txt", w.read("output.txt"), v4Output)
	equal(t, "tr runs", w.runs(), 2)
	sameMap(t, "completed effects not repeated", w.identities("input.txt", "notes.txt"), done)
	equal(t, "file journal entries", w.entriesFor("file.input", "file.notes"), fileEntries)
}

// Case 5, upstream edition: a failed resource blocks the task that depends
// on it (the task must not run against the old input), while the unrelated
// resource still applies. The retry then runs both halves exactly once.
func TestCase5FailedResourceBlocksItsDependents(t *testing.T) {
	w := applied(t, "v1.wb.hcl")
	w.use("v4.wb.hcl")
	output := w.identity("output.txt")

	r := w.wbEnv([]string{"WB_FAULT=file.input"}, "apply")
	equal(t, "exit code", r.code, 1)
	contains(t, r.stdout,
		"file.input  FAILED     injected fault before effect",
		"exec.shout  skipped    upstream file.input did not complete",
		"file.notes  updated",
		"Apply incomplete: 0 created, 1 updated, 0 ran, 1 failed, 1 skipped.")
	equal(t, "input.txt", w.read("input.txt"), v1Input)
	equal(t, "output identity", w.identity("output.txt"), output)
	equal(t, "tr runs", w.runs(), 1)

	notes := w.identity("notes.txt")
	contains(t, w.mustWB("plan").stdout, "~ file.input  update", "> exec.shout  run        upstream file.input changes", "= file.notes  unchanged")
	w.mustWB("apply")
	equal(t, "output.txt", w.read("output.txt"), v4Output)
	equal(t, "tr runs", w.runs(), 2)
	equal(t, "notes identity", w.identity("notes.txt"), notes)
}

// Case 5 with a real child-process failure instead of an injected one: the
// failed run leaves no torn output and no temp file, and the retry works.
func TestCase5ChildProcessFailureThenRetry(t *testing.T) {
	w := applied(t, "v1.wb.hcl")
	w.use("v4.wb.hcl")
	output := w.identity("output.txt")
	w.failTr(true)

	r := w.wb("apply")
	equal(t, "exit code", r.code, 1)
	contains(t, r.stdout, "exec.shout  FAILED     tr: exit status 3: tr: injected failure")
	equal(t, "output identity", w.identity("output.txt"), output)
	equal(t, "workspace files", reflect.DeepEqual(w.listing(), []string{".wb", "input.txt", "main.wb.hcl", "notes.txt", "output.txt"}), true)

	w.failTr(false)
	w.mustWB("apply")
	equal(t, "output.txt", w.read("output.txt"), v4Output)
	equal(t, "tr runs", w.runs(), 2)
}
