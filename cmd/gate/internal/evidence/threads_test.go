package evidence

import (
	"strings"
	"testing"
)

// changed builds a commit's file list, all modifications.
func changed(paths ...string) []File {
	out := make([]File, 0, len(paths))
	for _, p := range paths {
		out = append(out, File{Path: p})
	}
	return out
}

// commits is the PR history the disposition cases share: a thread anchored at
// c1, a later commit that fixes the reviewed file with a test, and one that
// touches it without.
func threadCommits() []Commit {
	return []Commit{
		{SHA: "c1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Subject: "first", Files: changed("cmd/gate/internal/evidence/panel.go")},
		{SHA: "c2bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Subject: "fix the panel split", Files: changed("cmd/gate/internal/evidence/panel.go", "cmd/gate/internal/evidence/panel_test.go")},
		{SHA: "c3cccccccccccccccccccccccccccccccccccccc", Subject: "docs only", Files: changed("README.md")},
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
	commits[1].Files = changed("cmd/gate/internal/evidence/panel.go")
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
	if !strings.Contains(d.Why, "no test change in it names") {
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
		{SHA: "c1", Files: changed("a_test.go")},
		{SHA: "c2", Files: changed("a_test.go")},
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
		{SHA: "x1", Files: changed("a.go")},
		{SHA: "x2", Files: changed("a.go", "a_test.go")},
		{SHA: "x3", Files: changed("a.go", "a_test.go")},
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

// The false-fixed case the design exists to refuse: a commit that changes the
// reviewed file while DELETING a test-looking file removes coverage; it must
// never read as covering evidence.
func TestDispositionsRemovedTestIsNotCoverage(t *testing.T) {
	commits := []Commit{
		{SHA: "c1", Files: changed("a.go")},
		{SHA: "c2", Files: []File{{Path: "a.go"}, {Path: "a_test.go", Removed: true}}},
	}
	d := Dispositions([]Thread{{Path: "a.go", AnchorSHA: "c1"}}, commits, "head")[0]
	if !d.Actionable {
		t.Fatalf("a deleted test file was credited as regression coverage: %+v", d)
	}
	if !strings.Contains(d.Why, "no test change in it names") {
		t.Errorf("why = %q", d.Why)
	}
}

// A later commit that restores real coverage still disposes the thread — the
// removal exclusion must not poison the whole file list.
func TestDispositionsRemovedTestDoesNotBlockLaterCoverage(t *testing.T) {
	commits := []Commit{
		{SHA: "c1", Files: changed("a.go")},
		{SHA: "c2", Files: []File{{Path: "a.go"}, {Path: "a_test.go", Removed: true}}},
		{SHA: "c3", Files: changed("a.go", "a_test.go")},
	}
	d := Dispositions([]Thread{{Path: "a.go", AnchorSHA: "c1"}}, commits, "head")[0]
	if d.Actionable || d.FixCommit != "c3" {
		t.Fatalf("want c3 credited as the fix, got %+v", d)
	}
}

func TestDispositionsEmptyAnchor(t *testing.T) {
	d := Dispositions([]Thread{{Path: "cmd/gate/internal/evidence/panel.go"}}, threadCommits(), "head")[0]
	if !d.Actionable || !strings.Contains(d.Why, "no anchor commit") {
		t.Fatalf("want still-actionable with an anchor-less why, got %+v", d)
	}
	if strings.Contains(d.Why, "  ") {
		t.Errorf("why has a double space: %q", d.Why)
	}
}

// A GraphQL error arrives with HTTP 200 next to an empty data block. Consuming
// it as an empty thread set would report a truncated PR as fully dispositioned.
func TestParseThreadsPageRejectsGraphQLErrors(t *testing.T) {
	raw := []byte(`{"data":{"repository":null},"errors":[{"message":"Could not resolve to a Repository"}]}`)
	if _, err := parseThreadsPage(raw); err == nil {
		t.Fatal("parseThreadsPage on an errors payload: want error")
	} else if !strings.Contains(err.Error(), "Could not resolve") {
		t.Errorf("error = %v", err)
	}
}

func TestParseThreadsPageReadsNodes(t *testing.T) {
	raw := []byte(`{"data":{"repository":{"pullRequest":{"reviewThreads":{
	  "pageInfo":{"hasNextPage":false,"endCursor":""},
	  "nodes":[{"id":"T1","isResolved":false,"path":"a.go","line":9,
	    "comments":{"nodes":[{"body":"b","author":{"login":"codex"},"originalCommit":{"oid":"abc"}}]}}]}}}}}`)
	page, err := parseThreadsPage(raw)
	if err != nil {
		t.Fatalf("parseThreadsPage: %v", err)
	}
	if len(page.Nodes) != 1 || page.Nodes[0].ID != "T1" ||
		page.Nodes[0].Comments.Nodes[0].OriginalCommit.OID != "abc" {
		t.Fatalf("page = %+v", page)
	}
}

// An unrelated test riding in the same commit is not coverage of the reviewed
// change — claiming it is would invite a human to resolve a live finding.
func TestDispositionsUnrelatedTestIsNotCoverage(t *testing.T) {
	commits := []Commit{
		{SHA: "c1", Files: changed("a.go")},
		{SHA: "c2", Files: changed("a.go", "other_test.go")},
	}
	d := Dispositions([]Thread{{Path: "a.go", AnchorSHA: "c1"}}, commits, "head")[0]
	if !d.Actionable {
		t.Fatalf("an unrelated test was credited as coverage: %+v", d)
	}
	if len(d.Tests) != 0 || d.Comment != "" {
		t.Errorf("actionable thread carries evidence: %+v", d)
	}
}

// ...and the commit that DOES name the reviewed file as its subject disposes it.
func TestDispositionsPrefersTheNamedCounterpart(t *testing.T) {
	commits := []Commit{
		{SHA: "c1", Files: changed("pkg/a.go")},
		{SHA: "c2", Files: changed("pkg/a.go", "pkg/other_test.go")},
		{SHA: "c3", Files: changed("pkg/a.go", "pkg/a_test.go")},
	}
	d := Dispositions([]Thread{{Path: "pkg/a.go", AnchorSHA: "c1"}}, commits, "head")[0]
	if d.Actionable || d.FixCommit != "c3" {
		t.Fatalf("want c3 credited, got %+v", d)
	}
	if len(d.Tests) != 1 || d.Tests[0] != "pkg/a_test.go" {
		t.Errorf("tests = %v", d.Tests)
	}
}

func TestCovers(t *testing.T) {
	yes := [][2]string{
		{"pkg/a_test.go", "pkg/a.go"},
		{"src/foo.test.ts", "src/foo.ts"},
		{"web/__tests__/foo.spec.js", "web/foo.js"},
		{"tests/test_thing.py", "src/thing.py"},
		{"native/src/parse_test.rs", "native/src/parse.rs"},
	}
	no := [][2]string{
		{"pkg/other_test.go", "pkg/a.go"},
		{"tests/test_other.py", "src/thing.py"},
		{"web/bar.test.ts", "web/foo.ts"},
	}
	for _, c := range yes {
		if !covers(c[0], c[1]) {
			t.Errorf("covers(%q, %q) = false", c[0], c[1])
		}
	}
	for _, c := range no {
		if covers(c[0], c[1]) {
			t.Errorf("covers(%q, %q) = true", c[0], c[1])
		}
	}
}

// A test credited as coverage must still exist at the head being stamped.
func TestCoverageDeletedByALaterCommitLeavesTheThreadActionable(t *testing.T) {
	commits := []Commit{
		{SHA: "c1", Files: []File{{Path: "unrelated.go"}}},
		{SHA: "c2", Files: []File{{Path: "a.go"}, {Path: "a_test.go"}}},
		{SHA: "c3", Files: []File{{Path: "a_test.go", Removed: true}}},
	}
	got, tests, touched := fixingCommit(commits, "a.go")
	if got.SHA != "" || len(tests) != 0 {
		t.Errorf("credited a test deleted before the head: commit=%q tests=%v", got.SHA, tests)
	}
	if touched != "c2" {
		t.Errorf("touched = %q, want c2 — the file WAS changed, only the coverage is gone", touched)
	}
}

func TestCoverageReAddedAfterRemovalStillCounts(t *testing.T) {
	commits := []Commit{
		{SHA: "c1", Files: []File{{Path: "a.go"}, {Path: "a_test.go"}}},
		{SHA: "c2", Files: []File{{Path: "a_test.go", Removed: true}}},
		{SHA: "c3", Files: []File{{Path: "a_test.go"}}},
	}
	got, tests, _ := fixingCommit(commits, "a.go")
	if got.SHA != "c1" || len(tests) != 1 {
		t.Errorf("a test present at head should count: commit=%q tests=%v", got.SHA, tests)
	}
}

func TestSurvivingCoverageStillDispositions(t *testing.T) {
	commits := []Commit{
		{SHA: "c1", Files: []File{{Path: "a.go"}, {Path: "a_test.go"}}},
		{SHA: "c2", Files: []File{{Path: "other.go"}}},
	}
	got, tests, _ := fixingCommit(commits, "a.go")
	if got.SHA != "c1" || len(tests) != 1 || tests[0] != "a_test.go" {
		t.Errorf("expected c1/a_test.go, got commit=%q tests=%v", got.SHA, tests)
	}
}
