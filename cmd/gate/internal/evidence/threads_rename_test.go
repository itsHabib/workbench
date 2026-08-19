package evidence

import (
	"encoding/json"
	"testing"
)

// GitHub reports a rename as the NEW filename with status "renamed" and the old
// name in previous_filename. Dropping the old path made a thread anchored to it
// read "no commit after X touches a.go" while the commit renamed a.go away —
// a completeness claim the history contradicts. A rename touches both paths:
// the new one as an ordinary change, the old one as removed.
func TestParseCommitFilesDecodesARename(t *testing.T) {
	raw := json.RawMessage(`{"files":[
		{"filename":"b.go","status":"renamed","previous_filename":"a.go"},
		{"filename":"c.go","status":"modified"}
	]}`)
	files, n, err := parseCommitFiles(raw)
	if err != nil {
		t.Fatal(err)
	}
	// The raw count is what GitHub sent — pagination must not read the extra
	// synthesized entry as a fuller page.
	if n != 2 {
		t.Fatalf("raw page count must be 2, got %d", n)
	}
	want := []File{{Path: "b.go"}, {Path: "a.go", Removed: true}, {Path: "c.go"}}
	if len(files) != len(want) {
		t.Fatalf("got %+v, want %+v", files, want)
	}
	for i, f := range files {
		if f != want[i] {
			t.Fatalf("file %d: got %+v, want %+v", i, f, want[i])
		}
	}
}

// A thread on the old path of a renamed file gets the renaming commit as a
// candidate — marked as a removal, because the path it reviewed is gone — and a
// test renamed AWAY in the same commit is not standing coverage.
func TestDispositionsSeeARenameAsTouchingTheOldPath(t *testing.T) {
	commits := []Commit{
		{SHA: "anchor"},
		{SHA: "rename", Subject: "move a.go to b.go", Files: []File{
			{Path: "b.go"},
			{Path: "a.go", Removed: true},
			{Path: "b_test.go"},
			{Path: "a_test.go", Removed: true},
		}},
	}
	d := Dispositions([]Thread{{ID: "t1", Path: "a.go", AnchorSHA: "anchor"}}, commits)[0]
	if len(d.Candidates) != 1 {
		t.Fatalf("the renaming commit must surface as a candidate, got %+v", d.Candidates)
	}
	if !d.Candidates[0].Deleted {
		t.Error("the old path must be marked removed — the reviewed file is gone from the tree")
	}
	if len(d.Candidates[0].Tests) != 0 {
		t.Errorf("a renamed-away test is not standing coverage, got %v", d.Candidates[0].Tests)
	}
}

// The commits endpoint stops at GitHub's 250-commit ceiling, and its short
// final page reads exactly like normal exhaustion. Every disposition note is a
// completeness claim, so a history that does not end at the swept head is
// refused, never trimmed to.
func TestTruncatedHistoryIsRefused(t *testing.T) {
	full := []Commit{{SHA: "a"}, {SHA: "b"}, {SHA: "head"}}
	if truncated(full, "head") {
		t.Error("a history ending at the head is complete")
	}
	if !truncated(full[:2], "head") {
		t.Error("a history stopping short of the head must be refused")
	}
	if !truncated(nil, "head") {
		t.Error("an empty history must be refused — a PR always has at least one commit")
	}
}
