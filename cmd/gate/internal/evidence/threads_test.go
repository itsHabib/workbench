package evidence

import (
	"strings"
	"testing"
)

// commits is the PR history the disposition cases share: a thread anchored at
// c1, a later commit that fixes the reviewed file with a test, and one that
// touches it without.
func threadCommits() []Commit {
	return []Commit{
		{SHA: "c1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Subject: "first", Files: []string{"cmd/gate/internal/evidence/panel.go"}},
		{SHA: "c2bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Subject: "fix the panel split", Files: []string{"cmd/gate/internal/evidence/panel.go", "cmd/gate/internal/evidence/panel_test.go"}},
		{SHA: "c3cccccccccccccccccccccccccccccccccccccc", Subject: "docs only", Files: []string{"README.md"}},
	}
}

func TestDispositionsFixedWithTest(t *testing.T) {
	threads := []Thread{{
		ID:        "PRRT_kw1",
		Path:      "cmd/gate/internal/evidence/panel.go",
		Line:      42,
		Author:    "chatgpt-codex-connector",
		AnchorSHA: "c1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}}
	got := Dispositions(threads, threadCommits(), "c3cccccccccccccccccccccccccccccccccccccc")
	if len(got) != 1 {
		t.Fatalf("dispositions = %d, want 1", len(got))
	}
	d := got[0]
	if d.Actionable {
		t.Fatalf("thread reported actionable: %s", d.Why)
	}
	if d.FixCommit != "c2bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Errorf("fix commit = %q", d.FixCommit)
	}
	if len(d.Tests) != 1 || d.Tests[0] != "cmd/gate/internal/evidence/panel_test.go" {
		t.Errorf("tests = %v", d.Tests)
	}
	for _, want := range []string{"c2bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "panel_test.go", "fix the panel split", "c3cccccccccccccccccccccccccccccccccccccc"} {
		if !strings.Contains(d.Comment, want) {
			t.Errorf("comment missing %q:\n%s", want, d.Comment)
		}
	}
	if !strings.Contains(d.ResolveCommand, "PRRT_kw1") || !strings.Contains(d.ResolveCommand, "resolveReviewThread") {
		t.Errorf("resolve command = %q", d.ResolveCommand)
	}
}

// A commit that changes the reviewed file but ships no test is exactly the case
// that must NOT be auto-dispositioned: the finding may well be fixed, but
// nothing on the record keeps it fixed.
func TestDispositionsFixWithoutTestStaysActionable(t *testing.T) {
	commits := threadCommits()
	commits[1].Files = []string{"cmd/gate/internal/evidence/panel.go"}
	threads := []Thread{{
		Path:      "cmd/gate/internal/evidence/panel.go",
		AnchorSHA: "c1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}}
	d := Dispositions(threads, commits, "head")[0]
	if !d.Actionable {
		t.Fatalf("thread dispositioned without a test: %+v", d)
	}
	if d.Comment != "" || d.ResolveCommand != "" {
		t.Errorf("actionable thread carries a prepared resolve: %+v", d)
	}
	if !strings.Contains(d.Why, "no test change") {
		t.Errorf("why = %q", d.Why)
	}
}

func TestDispositionsNoLaterCommitTouchesFile(t *testing.T) {
	threads := []Thread{{
		Path:      "cmd/gate/internal/verify/reviews.go",
		AnchorSHA: "c1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}}
	d := Dispositions(threads, threadCommits(), "head")[0]
	if !d.Actionable || !strings.Contains(d.Why, "no commit after") {
		t.Fatalf("want still-actionable, got %+v", d)
	}
}

// A fix that landed BEFORE the thread was posted is not a fix for it. Ordering
// is the whole reason the anchor commit is carried.
func TestDispositionsIgnoresCommitsBeforeAnchor(t *testing.T) {
	threads := []Thread{{
		Path:      "cmd/gate/internal/evidence/panel.go",
		AnchorSHA: "c2bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}}
	d := Dispositions(threads, threadCommits(), "head")[0]
	if !d.Actionable {
		t.Fatalf("credited an earlier commit as the fix: %+v", d)
	}
}

func TestDispositionsUnknownAnchorStaysActionable(t *testing.T) {
	threads := []Thread{{
		Path:      "cmd/gate/internal/evidence/panel.go",
		AnchorSHA: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
	}}
	d := Dispositions(threads, threadCommits(), "head")[0]
	if !d.Actionable || !strings.Contains(d.Why, "not in this pull request's history") {
		t.Fatalf("want still-actionable on an unknown anchor, got %+v", d)
	}
}

func TestDispositionsSkipsResolvedThreads(t *testing.T) {
	threads := []Thread{
		{Path: "cmd/gate/internal/evidence/panel.go", AnchorSHA: "c1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Resolved: true},
		{Path: "cmd/gate/internal/evidence/panel.go", AnchorSHA: "c1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}
	got := Dispositions(threads, threadCommits(), "head")
	if len(got) != 1 {
		t.Fatalf("dispositions = %d, want the unresolved one only", len(got))
	}
}

func TestDispositionsThreadWithoutPath(t *testing.T) {
	d := Dispositions([]Thread{{AnchorSHA: "c1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, threadCommits(), "head")[0]
	if !d.Actionable || !strings.Contains(d.Why, "no file anchor") {
		t.Fatalf("want still-actionable, got %+v", d)
	}
}

// A thread ON a test file must not credit that same file as its own coverage.
func TestDispositionsTestFileThreadNeedsOtherCoverage(t *testing.T) {
	commits := []Commit{
		{SHA: "c1", Files: []string{"a_test.go"}},
		{SHA: "c2", Files: []string{"a_test.go"}},
	}
	d := Dispositions([]Thread{{Path: "a_test.go", AnchorSHA: "c1"}}, commits, "head")[0]
	if !d.Actionable {
		t.Fatalf("credited the reviewed test file as its own coverage: %+v", d)
	}
}

// The earliest commit pairing the file with a test wins — a later one is not
// more authoritative, and naming the first keeps the comment re-derivable.
func TestFixingCommitPicksEarliestPairing(t *testing.T) {
	commits := []Commit{
		{SHA: "x1", Files: []string{"a.go"}},
		{SHA: "x2", Files: []string{"a.go", "a_test.go"}},
		{SHA: "x3", Files: []string{"a.go", "a_test.go"}},
	}
	fix, tests, touched := fixingCommit(commits, "a.go")
	if fix.SHA != "x2" || touched != "x1" || len(tests) != 1 {
		t.Fatalf("fix=%q touched=%q tests=%v", fix.SHA, touched, tests)
	}
}

func TestIsTestFile(t *testing.T) {
	yes := []string{"a_test.go", "src/a_test.rs", "test/helper.exs", "tests/x.py",
		"web/__tests__/a.js", "web/a.spec.ts", "web/a.test.tsx", "py/test_thing.py"}
	no := []string{"a.go", "cmd/gate/main.go", "docs/testing.md", "internal/latest.go"}
	for _, f := range yes {
		if !isTestFile(f) {
			t.Errorf("isTestFile(%q) = false", f)
		}
	}
	for _, f := range no {
		if isTestFile(f) {
			t.Errorf("isTestFile(%q) = true", f)
		}
	}
}

// firstRelevant keeps the per-commit file fetch off commits no thread can be
// dispositioned by.
func TestFirstRelevant(t *testing.T) {
	commits := threadCommits()
	threads := []Thread{
		{AnchorSHA: "c2bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		{AnchorSHA: "c1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Resolved: true},
	}
	if got := firstRelevant(commits, threads); got != 2 {
		t.Errorf("firstRelevant = %d, want 2", got)
	}
	if got := firstRelevant(commits, nil); got != len(commits) {
		t.Errorf("firstRelevant with no threads = %d, want %d", got, len(commits))
	}
}

func TestShellSingleQuote(t *testing.T) {
	if got := shellSingleQuote("a'b"); got != `'a'\''b'` {
		t.Errorf("shellSingleQuote = %q", got)
	}
}
