package evidence

import "testing"

// A merge commit's file list is the diff against its FIRST parent, so merging
// main into the branch lists main's files as if the merge introduced them —
// proven on this PR's own head, where merge commit 37c38c7's file list was
// main's #233 changes — mixed with any conflict resolution the merge itself
// made, which IS a change to the reviewed file. Neither crediting nor dropping
// it is honest, so a touching merge surfaces marked and claims nothing: no
// test, no deletion.
func TestDispositionsMarkMergeCommitsAndCreditThemNothing(t *testing.T) {
	commits := []Commit{
		{SHA: "anchor"},
		{SHA: "merge", Subject: "Merge main", Parents: 2,
			Files: []File{{Path: "a.go", Removed: true}, {Path: "a_test.go"}}},
	}
	d := Dispositions([]Thread{{ID: "t1", Path: "a.go", AnchorSHA: "anchor"}}, commits)[0]
	if len(d.Candidates) != 1 || !d.Candidates[0].Merge {
		t.Fatalf("a touching merge must surface, marked as a merge, got %+v", d.Candidates)
	}
	if len(d.Candidates[0].Tests) != 0 || d.Candidates[0].Deleted {
		t.Fatalf("a merge candidate must claim no test and no deletion — both could be base noise, got %+v", d.Candidates[0])
	}
	// A merge whose first-parent diff does not touch the file stays out.
	commits[1].Files = []File{{Path: "other.go"}}
	d = Dispositions([]Thread{{ID: "t1", Path: "a.go", AnchorSHA: "anchor"}}, commits)[0]
	if len(d.Candidates) != 0 {
		t.Fatalf("a merge not touching the file must not surface, got %+v", d.Candidates)
	}
	// An ordinary (single-parent) commit touching the file is still a candidate,
	// unmarked.
	commits[1] = Commit{SHA: "real", Parents: 1, Files: []File{{Path: "a.go"}}}
	d = Dispositions([]Thread{{ID: "t1", Path: "a.go", AnchorSHA: "anchor"}}, commits)[0]
	if len(d.Candidates) != 1 || d.Candidates[0].Merge {
		t.Fatalf("a real commit touching the file must still be an unmarked candidate, got %+v", d.Candidates)
	}
}

// A same-stem test in an unrelated package is not coverage: main_test.go names
// its subject "main", and so does every main.go in the tree. The tie must be the
// naming convention's actual shape — a sibling test, or a mirror test directory
// — not the stem alone.
func TestCoversRequiresMoreThanAStem(t *testing.T) {
	if covers("cmd/console/main_test.go", "cmd/gate/main.go") {
		t.Error("a same-stem test in an unrelated package must not be credited as coverage")
	}
	if covers("serviceA/handler_test.go", "serviceB/handler.go") {
		t.Error("a same-stem sibling-less test in another package must not be credited")
	}
	// Both legitimate shapes still hold.
	for _, ok := range [][2]string{
		{"pkg/a_test.go", "pkg/a.go"},                       // sibling
		{"native/src/parse_test.rs", "native/src/parse.rs"}, // sibling
		{"web/__tests__/foo.spec.js", "web/foo.js"},         // mirror dir
		{"tests/test_thing.py", "src/thing.py"},             // mirror dir
	} {
		if !covers(ok[0], ok[1]) {
			t.Errorf("covers(%q, %q) must stay true", ok[0], ok[1])
		}
	}
}

// A commit that DELETES the reviewed file still surfaces — deleting the file may
// be how a thread was addressed — but it is marked, so a reader does not take a
// removal for a change carrying a fix and a covering test.
func TestDispositionsMarkADeletion(t *testing.T) {
	commits := []Commit{
		{SHA: "anchor"},
		{SHA: "del", Subject: "remove a.go", Files: []File{{Path: "a.go", Removed: true}}},
	}
	d := Dispositions([]Thread{{ID: "t1", Path: "a.go", AnchorSHA: "anchor"}}, commits)[0]
	if len(d.Candidates) != 1 || !d.Candidates[0].Deleted {
		t.Fatalf("a deleting commit must surface, marked as a deletion, got %+v", d.Candidates)
	}
	if len(d.Candidates[0].Tests) != 0 {
		t.Errorf("a deletion carries no covering test, got %v", d.Candidates[0].Tests)
	}
}
