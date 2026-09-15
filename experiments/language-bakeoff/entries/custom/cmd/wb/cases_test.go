package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The six common cases. Each starts from a freshly created disposable
// directory and asserts effects directly: file contents and modification
// times, the journal, and how many times each child process really ran.

func TestCase1InitialPlanAndApply(t *testing.T) {
	f := newFixture(t)
	f.pipeline("the quick brown fox\n")

	r := f.plan()
	r.want(t, 0)
	contains(t, r.stdout,
		"+ keep file    notes  create notes.txt (20 bytes)",
		"> run  command shout  run, writes notes.upper.txt",
		"> run  command count  run, writes notes.count.txt",
		"* it never completed",
		"plan: 1 to create, 2 to run, 0 unchanged")
	entries, err := os.ReadDir(f.ws)
	f.must(err)
	equal(t, "workspace entries after plan", len(entries), 0) // planning never writes

	r = f.mustApply()
	contains(t, r.stdout, "applied: 1 created, 2 ran")
	equal(t, "notes.txt", f.read("notes.txt"), "the quick brown fox\n")
	equal(t, "notes.upper.txt", f.read("notes.upper.txt"), "THE QUICK BROWN FOX\n")
	equal(t, "notes.count.txt", f.read("notes.count.txt"), "4\n")
	equal(t, "calls", f.calledTimes(), map[string]int{"shout": 1, "count": 1})
	equal(t, "journal", f.ops(), []string{"converge notes", "start shout", "finish shout", "start count", "finish count"})
}

func TestCase2UnchangedReapplyRepeatsNothing(t *testing.T) {
	f := newFixture(t)
	f.pipeline("the quick brown fox\n")
	f.mustApply()
	before := f.snapshot()

	r := f.plan()
	r.want(t, 0)
	contains(t, r.stdout,
		"= keep file    notes  notes.txt matches (20 bytes)",
		"= run  command shout  current, keeps notes.upper.txt",
		"* definition, inputs and output match receipt #3",
		"plan: no changes: 3 unchanged")

	r = f.mustApply()
	contains(t, r.stdout, "applied: 1 unchanged, 2 skipped")
	f.unchangedSince(before) // same bytes, same mtimes, no journal records, no child processes
}

func TestCase3InputChangePlansDownstreamWork(t *testing.T) {
	f := newFixture(t)
	f.pipeline("the quick brown fox\n")
	f.mustApply()
	f.pipeline("the quick red fox\n")

	r := f.plan()
	r.want(t, 0)
	contains(t, r.stdout,
		"~ keep file    notes  update notes.txt (20 -> 18 bytes)",
		"* the source changed since wb wrote notes.txt at #1",
		"| -the quick brown fox",
		"| +the quick red fox",
		"> run  command shout  run, writes notes.upper.txt",
		"> run  command count  run, writes notes.count.txt",
		"* input notes changed (",
		"plan: 1 to update, 2 to run, 0 unchanged")

	f.mustApply()
	equal(t, "notes.upper.txt", f.read("notes.upper.txt"), "THE QUICK RED FOX\n")
	equal(t, "calls", f.calledTimes(), map[string]int{"shout": 2, "count": 2})
}

func TestCase4StalePlanIsRefusedWithoutEffects(t *testing.T) {
	f := newFixture(t)
	f.pipeline("the quick brown fox\n")
	f.mustApply()
	f.pipeline("the quick red fox\n")
	f.plan("-out", "plan.json").want(t, 0)

	// Someone edits the input between plan and apply.
	f.write("notes.txt", "edited by hand\n")
	before := f.snapshot()

	r := f.wb("apply", "-dir", "ws", "-plan", "plan.json")
	r.want(t, 3)
	contains(t, r.stderr,
		"refused: the plan is stale, so nothing was changed",
		"notes: notes.txt was ", " at plan time, now ",
		"run wb plan again")
	f.unchangedSince(before) // the hand edit survives; nothing ran

	// Re-planning shows the edit and that applying overwrites it.
	r = f.plan()
	contains(t, r.stdout,
		"* notes.txt changed outside wb after #1; applying overwrites that edit",
		"| -edited by hand",
		"| +the quick red fox")
	f.mustApply()
	equal(t, "notes.txt", f.read("notes.txt"), "the quick red fox\n")
}

func TestCase4StalePlanOnOwnedOutput(t *testing.T) {
	f := newFixture(t)
	f.pipeline("the quick brown fox\n")
	f.mustApply()
	f.plan("-out", "plan.json").want(t, 0) // a no-op plan is still bound to what it saw

	f.write("notes.upper.txt", "tampered\n")
	r := f.wb("apply", "-dir", "ws", "-plan", "plan.json")
	r.want(t, 3)
	contains(t, r.stderr, "shout: notes.upper.txt was ")

	r = f.plan()
	contains(t, r.stdout, "* output notes.upper.txt does not match receipt #3")
	f.mustApply()
	equal(t, "restored output", f.read("notes.upper.txt"), "THE QUICK BROWN FOX\n")
	equal(t, "calls", f.calledTimes(), map[string]int{"shout": 2, "count": 1})
}

func TestCase5PartialFailureRetrySkipsCompletedWork(t *testing.T) {
	f := newFixture(t)
	f.pipeline("the quick brown fox\n")

	// Inject a failure by shadowing awk on PATH; count uses awk, shout does not.
	shim := filepath.Join(f.root, "shim")
	f.must(os.Mkdir(shim, 0o755))
	f.must(os.WriteFile(filepath.Join(shim, "awk"), []byte("#!/bin/sh\necho 'awk: injected failure' >&2\nexit 1\n"), 0o755))

	r := f.wbEnv([]string{"PATH=" + shim + ":" + os.Getenv("PATH")}, "apply", "-dir", "ws", "pipeline.wb")
	r.want(t, 1)
	contains(t, r.stdout,
		"count  failed   sh: exit status 1: awk: injected failure",
		"applied: 1 created, 1 ran, 1 failed")
	contains(t, r.stderr, "apply stopped: count:", "run wb apply again to retry")
	equal(t, "notes.count.txt after failure", f.read("notes.count.txt"), "<absent>")
	equal(t, "partial output left behind", f.read("notes.count.txt.wb-partial"), "<absent>")
	equal(t, "journal", f.ops(), []string{"converge notes", "start shout", "finish shout", "start count", "fail count"})
	notes, upper := f.snapshot().mtimes["notes.txt"], f.snapshot().mtimes["notes.upper.txt"]

	r = f.plan()
	contains(t, r.stdout,
		"= keep file    notes  notes.txt matches",
		"= run  command shout  current, keeps notes.upper.txt",
		"> run  command count  run, writes notes.count.txt",
		"* attempt #4 failed: sh: exit status 1: awk: injected failure")

	f.mustApply()
	equal(t, "notes.count.txt", f.read("notes.count.txt"), "4\n")
	equal(t, "calls", f.calledTimes(), map[string]int{"shout": 1, "count": 2}) // shout never repeated
	after := f.snapshot()
	equal(t, "notes.txt mtime", after.mtimes["notes.txt"], notes)
	equal(t, "notes.upper.txt mtime", after.mtimes["notes.upper.txt"], upper)
}

// Downstream work behind other work cannot be decided at plan time, because
// its input is an output that does not exist yet. The plan says so, and
// apply decides once the upstream output is known.
func TestDownstreamWorkIsDecidedDuringApply(t *testing.T) {
	f := newFixture(t)
	src := func(text string) {
		f.source(`keep file notes {
  path "notes.txt"
  text "` + text + `"
}

run command shout {
  stdin  notes.path
  argv   "tr" "a-z" "A-Z"
  stdout "notes.upper.txt"
}

run command tally {
  stdin  shout.stdout
  argv   "sh" "-c" "echo tally >> '` + f.calls + `'; wc -c"
  stdout "tally.txt"
}
`)
	}
	src(`the quick brown fox\n`)
	f.mustApply()

	src(`the Quick brown fox\n`) // shout's output is unchanged by this edit
	r := f.plan()
	contains(t, r.stdout,
		"> run  command shout  run, writes notes.upper.txt",
		"? run  command tally  decided during apply, writes tally.txt",
		"* waits for shout; runs only if that changes its input",
		"1 decided during apply")
	r = f.mustApply()
	contains(t, r.stdout, "tally  skipped")
	equal(t, "calls", f.calledTimes(), map[string]int{"tally": 1})

	src(`the quick red fox\n`)
	f.mustApply()
	equal(t, "calls", f.calledTimes(), map[string]int{"tally": 2})
}

func TestConflictIsRefusedBeforeAnyEffect(t *testing.T) {
	f := newFixture(t)
	f.pipeline("the quick brown fox\n")
	f.must(os.Mkdir(filepath.Join(f.ws, "notes.txt"), 0o755))

	r := f.plan()
	contains(t, r.stdout,
		"! keep file    notes  notes.txt exists and is not a regular file; wb will not replace it",
		"1 in conflict (apply refuses)")
	r = f.apply()
	r.want(t, 3)
	contains(t, r.stderr, "refused, nothing was changed")
	equal(t, "journal", len(f.journal()), 0)
	equal(t, "calls", f.calledTimes(), map[string]int{})
}

func TestEffectsStayInsideTheWorkspace(t *testing.T) {
	f := newFixture(t)
	outside := filepath.Join(f.root, "outside")
	f.must(os.Mkdir(outside, 0o755))
	f.must(os.Symlink(outside, filepath.Join(f.ws, "sub")))
	f.must(os.Symlink(filepath.Join(outside, "target.txt"), filepath.Join(f.ws, "link.txt")))
	f.source(`keep file deep {
  path "sub/deep.txt"
  text "x"
}
`)
	r := f.apply()
	if r.code == 0 {
		t.Fatalf("apply through a symlink that leaves the workspace must fail\n%s", r.stdout)
	}
	contains(t, r.stderr, "sub/deep.txt: parent sub is a symlink; wb writes only through real directories")

	f.source(`keep file link {
  path "link.txt"
  text "x"
}
`)
	r = f.apply()
	r.want(t, 3)
	contains(t, r.stdout, "link.txt exists and is not a regular file")
	entries, err := os.ReadDir(outside)
	f.must(err)
	equal(t, "files written outside the workspace", len(entries), 0)
}

func TestCheckReportsPositionedErrors(t *testing.T) {
	f := newFixture(t)
	f.source(`keep file notes {
  path "../notes.txt"
  txt  "hello"
}

run command shout {
  stdin  nots.path
  argv   "tr" "a-z" "A-Z"
  stdout "notes.txt"
}
`)
	r := f.wb("check", "pipeline.wb")
	r.want(t, 2)
	contains(t, r.stderr,
		"pipeline.wb:1:11: file notes is missing required attribute text",
		`pipeline.wb:2:8: path "../notes.txt" must stay inside the workspace: relative, without ..`,
		`pipeline.wb:3:3: file has no attribute "txt" (attributes: path, text)`,
		"pipeline.wb:7:10: shout refers to nots.path, but nothing is named nots",
		"wb: pipeline.wb: 4 error(s)")
}
