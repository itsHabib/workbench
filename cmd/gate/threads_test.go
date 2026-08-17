package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/gate/internal/evidence"
)

// The rendered sweep must separate the two states in words a reader cannot
// misread, and must never print a resolve command for a thread the sweep could
// not back with a commit and a test.
func TestWriteDispositions(t *testing.T) {
	pr := evidence.PRRef{Repo: "itsHabib/workbench", Number: 231}
	ds := []evidence.Disposition{
		{
			Thread:         evidence.Thread{Path: "a.go", Line: 12, Author: "codex"},
			FixCommit:      "abc123",
			Tests:          []string{"a_test.go"},
			Why:            "abc123 changes a.go and its regression test in one commit",
			Comment:        "Fixed in `abc123`.\n\nEvidence:\n- fix: `abc123` changes `a.go`",
			ResolveCommand: "gh api graphql -f query='mutation{...}'",
		},
		{
			Thread:     evidence.Thread{Path: "b.go", Line: 7, Author: "claude"},
			Actionable: true,
			Why:        "no commit after c1 touches b.go",
		},
	}
	var buf bytes.Buffer
	if err := writeDispositions(&buf, pr, "head1", ds); err != nil {
		t.Fatalf("writeDispositions: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"itsHabib/workbench#231 @ head1", "2 unresolved", "dispositioned a.go:12", "actionable b.go:7", "| Fixed in `abc123`.", "resolve: gh api graphql"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "resolve: ") != 1 {
		t.Errorf("a resolve command was printed for an actionable thread:\n%s", out)
	}
}

func TestCmdThreadsRequiresSubject(t *testing.T) {
	if err := cmdThreads([]string{"-pr", "1"}); err == nil {
		t.Fatal("cmdThreads with no -repo: want error")
	}
	if err := cmdThreads([]string{"-repo", "o/r"}); err == nil {
		t.Fatal("cmdThreads with no -pr: want error")
	}
}

// A thread with no line (or no file) must not print as line zero.
func TestThreadLocus(t *testing.T) {
	cases := []struct {
		th   evidence.Thread
		want string
	}{
		{evidence.Thread{Path: "a.go", Line: 12}, "a.go:12"},
		{evidence.Thread{Path: "a.go"}, "a.go"},
		{evidence.Thread{}, "(no file anchor)"},
	}
	for _, c := range cases {
		if got := threadLocus(c.th); got != c.want {
			t.Errorf("threadLocus(%+v) = %q, want %q", c.th, got, c.want)
		}
	}
}
