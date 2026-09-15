package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regressions for the independent review's findings, driven through the
// real binary. Each names the finding it pins down.

// Finding 2: a symlink planted at the temporary name must not redirect the
// write into an unowned file.
func TestPlantedTemporaryLinkIsNotFollowed(t *testing.T) {
	f := newFixture(t)
	f.pipeline("the quick brown fox\n")
	f.write("diary.txt", "private\n")
	f.must(os.Symlink("diary.txt", filepath.Join(f.ws, "notes.txt.wb-partial")))

	f.mustApply()
	equal(t, "diary.txt", f.read("diary.txt"), "private\n")
	equal(t, "notes.txt", f.read("notes.txt"), "the quick brown fox\n")
	contains(t, f.plan().stdout, "plan: no changes: 3 unchanged")
}

// Finding 3: a literal input reached through a symlink is tracked by the
// content it links to.
func TestSymlinkedInputIsTracked(t *testing.T) {
	f := newFixture(t)
	f.must(os.Mkdir(filepath.Join(f.ws, "real"), 0o755))
	f.write("real/data.txt", "one\n")
	f.must(os.Symlink("real/data.txt", filepath.Join(f.ws, "data.txt")))
	f.source(`run command up {
  stdin  "data.txt"
  argv   "tr" "a-z" "A-Z"
  stdout "upper.txt"
}
`)
	f.mustApply()
	f.write("real/data.txt", "two\n")
	r := f.plan()
	contains(t, r.stdout, "> run command up  run, writes upper.txt", "* input path:data.txt changed (")
	f.mustApply()
	equal(t, "upper.txt", f.read("upper.txt"), "TWO\n")
}

// Finding 5: a literal input path named like the journal check does not
// collide with it.
func TestInputNamedJournalDoesNotCollide(t *testing.T) {
	f := newFixture(t)
	f.write("journal", "entries\n")
	f.source(`run command up {
  stdin  "journal"
  argv   "tr" "a-z" "A-Z"
  stdout "upper.txt"
}
`)
	f.mustApply()
	f.plan("-out", "plan.json").want(t, 0)
	r := f.wb("apply", "-dir", "ws", "-plan", "plan.json")
	r.want(t, 0)
	equal(t, "upper.txt", f.read("upper.txt"), "ENTRIES\n")
}

// Finding 6: work whose output path holds a directory or a symlink is a
// conflict found by the plan, before the command runs.
func TestWorkOutputThatIsNotAFileIsAConflict(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		plant      func(f *fixture)
	}{
		{"directory", "notes.upper.txt is a directory, not a regular file; wb will not replace it", func(f *fixture) {
			f.must(os.MkdirAll(filepath.Join(f.ws, "notes.upper.txt", "keep"), 0o755))
		}},
		{"symlink", "notes.upper.txt is a symlink or special file, not a regular file; wb will not replace it", func(f *fixture) {
			f.write("elsewhere.txt", "not wb's\n")
			f.must(os.Symlink("elsewhere.txt", filepath.Join(f.ws, "notes.upper.txt")))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.pipeline("the quick brown fox\n")
			tc.plant(f)
			r := f.plan()
			contains(t, r.stdout, "! run  command shout  cannot write notes.upper.txt", "* "+tc.want, "1 in conflict (apply refuses)")
			f.apply().want(t, 3)
			equal(t, "calls", f.calledTimes(), map[string]int{})
			equal(t, "journal", len(f.journal()), 0)
		})
	}
}

// Finding 7: work may write into a directory nothing else has created yet.
func TestWorkOutputDirectoryIsCreated(t *testing.T) {
	f := newFixture(t)
	f.source(`run command gen {
  argv   "sh" "-c" "echo generated"
  stdout "out/gen.txt"
}

keep file readme {
  path "out/README"
  text "generated files live here\n"
}
`)
	f.mustApply()
	equal(t, "out/gen.txt", f.read("out/gen.txt"), "generated\n")
	equal(t, "out/README", f.read("out/README"), "generated files live here\n")
}

// Findings 9 and 10: a saved plan is checked like source. Its embedded
// source must still compile to exactly the declarations it would apply;
// anything else is invalid input (exit 2) and nothing runs.
func TestEditedOrMalformedSavedPlanIsRefused(t *testing.T) {
	f := newFixture(t)
	f.pipeline("the quick brown fox\n")
	f.plan("-out", "plan.json").want(t, 0)
	b, err := os.ReadFile(filepath.Join(f.root, "plan.json"))
	f.must(err)

	var p map[string]any
	f.must(json.Unmarshal(b, &p))
	p["decls"].([]any)[1].(map[string]any)["attrs"].(map[string]any)["argv"] = []string{}
	edited, err := json.Marshal(p)
	f.must(err)
	f.must(os.WriteFile(filepath.Join(f.root, "edited.json"), edited, 0o644))
	f.must(os.WriteFile(filepath.Join(f.root, "empty.json"), []byte("{}"), 0o644))
	f.must(os.WriteFile(filepath.Join(f.root, "broken.json"), []byte(strings.Replace(string(b), `keep file notes`, `keep file notes notes`, 1)), 0o644))

	for plan, want := range map[string]string{
		"edited.json": "edited.json was edited: its declarations do not match its source",
		"empty.json":  "empty.json is not a format-1 plan",
		"broken.json": "broken.json: its embedded source does not check",
	} {
		r := f.wb("apply", "-dir", "ws", "-plan", plan)
		r.want(t, 2)
		contains(t, r.stderr, want)
	}
	equal(t, "journal", len(f.journal()), 0)
	equal(t, "calls", f.calledTimes(), map[string]int{})
	f.wb("apply", "-dir", "ws", "-plan", "plan.json").want(t, 0) // the untouched plan still applies
}

// Round 2, finding 2: a symlinked parent directory cannot carry a write
// into .wb (or anywhere else); the plan refuses before any effect.
func TestSymlinkedParentCannotReachTheJournal(t *testing.T) {
	f := newFixture(t)
	f.pipeline("the quick brown fox\n")
	f.mustApply()
	before := len(f.journal())
	f.must(os.Symlink(".wb", filepath.Join(f.ws, "state")))
	f.source(fmt.Sprintf(pipeline, "the quick brown fox\n", f.calls) + `
keep file clobber {
  path "state/journal.jsonl"
  text "gone\n"
}
`)
	r := f.apply()
	r.want(t, 1)
	contains(t, r.stderr, "state/journal.jsonl: parent state is a symlink; wb writes only through real directories")
	equal(t, "journal records", len(f.journal()), before)
}

// Round 2, finding 4: evidence is never read from or appended through a
// link planted at .wb or at the journal.
func TestJournalMustBeARealFile(t *testing.T) {
	f := newFixture(t)
	f.pipeline("the quick brown fox\n")
	f.write("diary.txt", "private\n")
	f.must(os.Mkdir(filepath.Join(f.ws, ".wb"), 0o755))
	f.must(os.Symlink("../diary.txt", filepath.Join(f.ws, ".wb", "journal.jsonl")))
	r := f.apply()
	r.want(t, 1)
	contains(t, r.stderr, ".wb/journal.jsonl is not a regular file; wb will not read or append evidence there")
	equal(t, "diary.txt", f.read("diary.txt"), "private\n")
	equal(t, "calls", f.calledTimes(), map[string]int{})
}

// Round 2, finding 3: list values that contain quotes and commas survive
// the saved plan exactly, so an untouched plan applies.
func TestSavedPlanKeepsQuotedArguments(t *testing.T) {
	f := newFixture(t)
	f.write("in.csv", "a\",b\n")
	f.source(`run command field {
  stdin  "in.csv"
  argv   "awk" "-F\"," "{ print $2 }"
  stdout "field.txt"
}
`)
	contains(t, f.wb("intent", "pipeline.wb").stdout, `"argv": ["awk", "-F\",", "{ print $2 }"]`)
	f.plan("-out", "plan.json").want(t, 0)
	f.wb("apply", "-dir", "ws", "-plan", "plan.json").want(t, 0)
	equal(t, "field.txt", f.read("field.txt"), "b\n")
}

// Round 2, finding 5: a saved plan cannot drop its own checks to get past
// the stale-plan refusal.
func TestSavedPlanCannotDropItsChecks(t *testing.T) {
	f := newFixture(t)
	f.pipeline("the quick brown fox\n")
	f.plan("-out", "plan.json").want(t, 0)
	b, err := os.ReadFile(filepath.Join(f.root, "plan.json"))
	f.must(err)
	var p map[string]any
	f.must(json.Unmarshal(b, &p))
	p["checks"] = []any{}
	stripped, err := json.Marshal(p)
	f.must(err)
	f.must(os.WriteFile(filepath.Join(f.root, "stripped.json"), stripped, 0o644))

	r := f.wb("apply", "-dir", "ws", "-plan", "stripped.json")
	r.want(t, 3)
	contains(t, r.stderr, "notes: the plan never observed notes.txt")
	equal(t, "journal", len(f.journal()), 0)
}
