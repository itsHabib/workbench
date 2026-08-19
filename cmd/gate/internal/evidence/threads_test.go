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
func TestDispositionsNeverClaimAThreadIsFixed(t *testing.T) {
	// Every shape that previously produced a "dispositioned" verdict — and
	// every shape that produced a WRONG one — now produces observations only.
	// There is no field left that can say "fixed", which is the whole point.
	cases := map[string][]Commit{
		"fix with its named test": {
			{SHA: "c1", Subject: "fix", Files: []File{{Path: "a.go"}, {Path: "a_test.go"}}},
		},
		"test deleted in the same commit": {
			{SHA: "c1", Files: []File{{Path: "a.go"}, {Path: "a_test.go", Removed: true}}},
		},
		"unrelated test alongside": {
			{SHA: "c1", Files: []File{{Path: "a.go"}, {Path: "other_test.go"}}},
		},
		"test added then removed later": {
			{SHA: "c1", Files: []File{{Path: "a.go"}, {Path: "a_test.go"}}},
			{SHA: "c2", Files: []File{{Path: "a_test.go", Removed: true}}},
		},
	}
	for name, commits := range cases {
		all := append([]Commit{{SHA: "anchor"}}, commits...)
		got := Dispositions([]Thread{{ID: "t1", Path: "a.go", AnchorSHA: "anchor"}}, all, "head")
		if len(got) != 1 {
			t.Fatalf("%s: expected one disposition, got %d", name, len(got))
		}
		d := got[0]
		if strings.Contains(strings.ToLower(d.Note), "fixed") {
			t.Errorf("%s: note claims a fix: %q", name, d.Note)
		}
		if len(d.Candidates) == 0 {
			t.Errorf("%s: expected the touching commit to be reported as a candidate", name)
		}
	}
}

func TestDispositionsReportEveryTouchingCommit(t *testing.T) {
	// Not just the first. Picking one was the judgement that kept being wrong,
	// and a later commit can undo an earlier one.
	commits := []Commit{
		{SHA: "anchor"},
		{SHA: "c1", Subject: "first", Files: []File{{Path: "a.go"}, {Path: "a_test.go"}}},
		{SHA: "c2", Subject: "second", Files: []File{{Path: "a.go"}}},
		{SHA: "c3", Subject: "elsewhere", Files: []File{{Path: "b.go"}}},
	}
	d := Dispositions([]Thread{{ID: "t1", Path: "a.go", AnchorSHA: "anchor"}}, commits, "head")[0]
	if len(d.Candidates) != 2 {
		t.Fatalf("expected both commits touching a.go, got %d: %+v", len(d.Candidates), d.Candidates)
	}
	if d.Candidates[0].SHA != "c1" || d.Candidates[1].SHA != "c2" {
		t.Errorf("candidates should be oldest-first: %+v", d.Candidates)
	}
	if len(d.Candidates[0].Tests) != 1 || len(d.Candidates[1].Tests) != 0 {
		t.Errorf("tests should attach to the commit that carried them: %+v", d.Candidates)
	}
}

func TestDispositionsSkipResolvedThreads(t *testing.T) {
	got := Dispositions([]Thread{{ID: "t1", Path: "a.go", AnchorSHA: "anchor", Resolved: true}},
		[]Commit{{SHA: "anchor"}}, "head")
	if len(got) != 0 {
		t.Errorf("a resolved thread needs no disposition, got %d", len(got))
	}
}

func TestDispositionsSayWhatCouldNotBeEstablished(t *testing.T) {
	for name, th := range map[string]Thread{
		"no file anchor":   {ID: "t1", AnchorSHA: "anchor"},
		"no anchor commit": {ID: "t2", Path: "a.go"},
		"anchor not in PR": {ID: "t3", Path: "a.go", AnchorSHA: "elsewhere"},
	} {
		d := Dispositions([]Thread{th}, []Commit{{SHA: "anchor"}}, "head")[0]
		if d.Note == "" {
			t.Errorf("%s: expected a note explaining what could not be established", name)
		}
		if len(d.Candidates) != 0 {
			t.Errorf("%s: nothing can be a candidate here", name)
		}
	}
}
