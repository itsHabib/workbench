package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Case 6: the dir kind is one adapter package plus one registry entry. The
// source gains two declarations; the existing declarations are untouched
// and nothing already settled is redone.
func TestCase6DirAdapter(t *testing.T) {
	f := newFixture(t)
	f.pipeline("the quick brown fox\n")
	f.mustApply()
	extension, err := os.ReadFile("../../examples/scratch.wb")
	f.must(err)
	f.source(fmt.Sprintf(pipeline, "the quick brown fox\n", f.calls) + "\n" + string(extension))

	r := f.plan()
	r.want(t, 0)
	contains(t, r.stdout,
		"= keep file    notes    notes.txt matches",
		"= run  command shout    current, keeps notes.upper.txt",
		"+ keep dir     scratch  create directory scratch/",
		"> run  command words    run, writes words.txt",
		"plan: 1 to create, 1 to run, 3 unchanged")
	f.mustApply()
	equal(t, "words.txt", f.read("words.txt"), "brown\nfox\nquick\nthe\n")
	equal(t, "temporary file in the scratch directory", f.read("scratch/all.tmp"), "the\nquick\nbrown\nfox\n")
	equal(t, "calls", f.calledTimes(), map[string]int{"shout": 1, "count": 1})

	// Disposable: deleting it recreates the directory and redoes no work.
	f.must(os.RemoveAll(filepath.Join(f.ws, "scratch")))
	r = f.plan()
	contains(t, r.stdout,
		"+ keep dir     scratch  create directory scratch/",
		"= run  command words    current, keeps words.txt",
		"plan: 1 to create, 4 unchanged")
	f.mustApply()
	equal(t, "words starts", strings.Count(strings.Join(f.ops(), "\n"), "start words"), 1)

	// It never replaces something that is not a directory.
	f.must(os.RemoveAll(filepath.Join(f.ws, "scratch")))
	f.write("scratch", "not a directory\n")
	r = f.apply()
	r.want(t, 3)
	contains(t, r.stdout, "! keep dir     scratch  scratch exists and is not a directory; wb will not replace it")
	equal(t, "scratch", f.read("scratch"), "not a directory\n")

	r = f.wb("kinds")
	contains(t, r.stdout, "keep dir\n  attributes: path (owned path, required)\n  exports: path")
}
