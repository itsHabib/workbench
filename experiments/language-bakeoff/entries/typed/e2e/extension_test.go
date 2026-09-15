package e2e

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/cli"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/engine"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/wb"
)

const sp = "scratchpipe"

const report0 = "JUMPS OVER THE LAZY DOG\nTHE QUICK BROWN FOX\n"

// Case 6: the third adapter, a disposable directory, through the same
// unchanged planner and CLI.
func TestCase6DisposableDirectoryAdapter(t *testing.T) {
	s := newSandbox(t)
	r := s.must(cli.ExitOK, nil, sp, "plan", "-dir", "ws", "-out", "plans/6.json")
	golden(t, "6-plan.txt", r.stdout)
	assertOps(t, s.plan("plans/6.json"), map[wb.Address]engine.Op{
		"file.input": engine.Create, "transform.upper": engine.Run, "transform.wordcount": engine.Run,
		"scratchdir.work": engine.Create, "transform.report": engine.Run,
	})
	s.must(cli.ExitOK, nil, sp, "apply", "-dir", "ws", "-plan", "plans/6.json")
	if got := s.read("ws/work/report.txt"); got != report0 {
		t.Errorf("report.txt = %q", got)
	}
	if got := s.read("ws/work/.wb-scratchdir"); got != "1" {
		t.Errorf("marker = %q", got)
	}

	// Bumping the generation replaces the directory: its contents, including
	// a stray file, are deleted, and only the task living inside it re-runs.
	s.write("ws/work/stray.txt", "not declared anywhere\n")
	kept := s.identities(textpipeOutputs...)
	r = s.must(cli.ExitOK, nil, sp, "plan", "-dir", "ws", "-var", "generation=2", "-out", "plans/6b.json")
	golden(t, "6-plan-replace.txt", r.stdout)
	p := s.plan("plans/6b.json")
	assertOps(t, p, map[wb.Address]engine.Op{
		"file.input": engine.None, "transform.upper": engine.None, "transform.wordcount": engine.None,
		"scratchdir.work": engine.Replace, "transform.report": engine.Run,
	})
	if !mentions(stepOf(p, "transform.report").Reasons, "scratchdir.work will be replaced") {
		t.Errorf("report reasons %q do not name the replaced directory", stepOf(p, "transform.report").Reasons)
	}
	s.must(cli.ExitOK, nil, sp, "apply", "-dir", "ws", "-plan", "plans/6b.json")
	if _, err := os.Stat(s.path("ws/work/stray.txt")); !os.IsNotExist(err) {
		t.Errorf("stray file survived the replace: %v", err)
	}
	if got := s.read("ws/work/report.txt"); got != report0 {
		t.Errorf("report.txt after replace = %q", got)
	}
	s.assertNotRewritten(kept)
	if n := s.starts("transform.report"); n != 2 {
		t.Errorf("transform.report started %d times, want 2", n)
	}
}

// The adapter never adopts a non-empty directory it did not create. An
// empty one (what an interrupted create leaves behind) is adopted.
func TestCase6OwnershipOfExistingDirectories(t *testing.T) {
	s := newSandbox(t)
	if err := os.Mkdir(s.path("ws/work"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.write("ws/work/precious.txt", "mine\n")
	r := s.must(cli.ExitFailed, nil, sp, "plan", "-dir", "ws")
	golden(t, "6-refuse-unmarked.txt", r.stderr)
	if !strings.Contains(r.stderr, "ownership marker") {
		t.Errorf("stderr = %q", r.stderr)
	}
	r = s.must(cli.ExitFailed, nil, sp, "apply", "-dir", "ws")
	if got := s.read("ws/work/precious.txt"); got != "mine\n" {
		t.Errorf("precious.txt = %q", got)
	}
	if _, err := os.Stat(s.path("ws/input.txt")); !os.IsNotExist(err) {
		t.Errorf("a refused plan still applied other nodes: %v", err)
	}

	if err := os.Remove(s.path("ws/work/precious.txt")); err != nil {
		t.Fatal(err)
	}
	s.must(cli.ExitOK, nil, sp, "plan", "-dir", "ws", "-out", "plans/adopt.json")
	if note := stepOf(s.plan("plans/adopt.json"), "scratchdir.work").Note; !strings.Contains(note, "adopts") {
		t.Errorf("note = %q, want adoption of the empty directory", note)
	}
	s.must(cli.ExitOK, nil, sp, "apply", "-dir", "ws", "-plan", "plans/adopt.json")
	if got := s.read("ws/work/report.txt"); got != report0 {
		t.Errorf("report.txt = %q", got)
	}
}

// A killed replace leaves one of the states below. The swap builds the new
// directory beside the old one and renames it in, so the path never holds
// an unmarked directory, and the next apply of the node clears leftovers.
func TestCase6RecoversFromAnInterruptedSwap(t *testing.T) {
	s := newSandbox(t)
	s.must(cli.ExitOK, nil, sp, "apply", "-dir", "ws")

	// Killed after moving the old directory aside, before moving the new one in.
	if err := os.Rename(s.path("ws/work"), s.path("ws/.work.wb-old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.path("ws/.work.wb-new"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.write("ws/.work.wb-new/.wb-scratchdir", "2")
	s.must(cli.ExitOK, nil, sp, "plan", "-dir", "ws", "-var", "generation=2", "-out", "plans/b.json")
	assertOps(t, s.plan("plans/b.json"), map[wb.Address]engine.Op{
		"file.input": engine.None, "transform.upper": engine.None, "transform.wordcount": engine.None,
		"scratchdir.work": engine.Create, "transform.report": engine.Run,
	})
	s.must(cli.ExitOK, nil, sp, "apply", "-dir", "ws", "-plan", "plans/b.json")
	for _, leftover := range []string{"ws/.work.wb-old", "ws/.work.wb-new"} {
		if _, err := os.Stat(s.path(leftover)); !os.IsNotExist(err) {
			t.Errorf("%s survived the recovering apply: %v", leftover, err)
		}
	}
	if got := s.read("ws/work/report.txt"); got != report0 {
		t.Errorf("report.txt = %q", got)
	}

	// Killed while deleting the old contents, after the new directory was
	// in place: the leftover blocks nothing and goes with the next change.
	if err := os.Mkdir(s.path("ws/.work.wb-old"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range 50 {
		s.write("ws/.work.wb-old/f"+strconv.Itoa(i), "x")
	}
	s.must(cli.ExitOK, nil, sp, "plan", "-dir", "ws", "-var", "generation=2", "-out", "plans/c.json")
	for _, st := range s.plan("plans/c.json").Steps {
		if st.Op != engine.None {
			t.Errorf("%s: %s; a leftover should not disturb a converged workspace", st.Address, st.Op)
		}
	}
	s.must(cli.ExitOK, nil, sp, "apply", "-dir", "ws", "-var", "generation=3")
	if _, err := os.Stat(s.path("ws/.work.wb-old")); !os.IsNotExist(err) {
		t.Errorf("leftover survived the next replace: %v", err)
	}
}

// The replace deletes old contents only after the new directory is in
// place. A subdirectory the replace cannot empty makes the cleanup fail,
// and the path must still hold the new, marked directory. Deleting in
// place first (the original design) would leave it half-deleted instead.
func TestCase6OldContentsGoOnlyAfterTheSwap(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	s := newSandbox(t)
	s.must(cli.ExitOK, nil, sp, "apply", "-dir", "ws")
	if err := os.Mkdir(s.path("ws/work/locked"), 0o755); err != nil {
		t.Fatal(err)
	}
	s.write("ws/work/locked/f", "x")
	if err := os.Chmod(s.path("ws/work/locked"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(s.path("ws/work/locked"), 0o755)
		_ = os.Chmod(s.path("ws/.work.wb-old/locked"), 0o755)
	})

	r := s.must(cli.ExitFailed, nil, sp, "apply", "-dir", "ws", "-var", "generation=2")
	if !strings.Contains(r.stdout, "permission denied") {
		t.Errorf("apply output does not report the failed cleanup:\n%s", r.stdout)
	}
	if got := s.read("ws/work/.wb-scratchdir"); got != "2" {
		t.Errorf("marker = %q; the new directory should be in place", got)
	}
	s.must(cli.ExitOK, nil, sp, "plan", "-dir", "ws", "-var", "generation=2", "-out", "plans/after.json")
	if op := stepOf(s.plan("plans/after.json"), "scratchdir.work").Op; op != engine.None {
		t.Errorf("scratchdir.work: %s, want converged despite the failed cleanup", op)
	}
}
