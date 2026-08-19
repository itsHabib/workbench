package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/gate/internal/evidence"
)

// The rendered sweep lists what it saw and says plainly that it is not
// judging. It must never print a resolve command, because it no longer decides
// anything a resolve would act on.
func TestWriteDispositions(t *testing.T) {
	pr := evidence.PRRef{Repo: "itsHabib/workbench", Number: 231}
	ds := []evidence.Disposition{
		{
			Thread: evidence.Thread{Path: "a.go", Line: 12, Author: "codex"},
			Note:   "2 commit(s) after c0 touch a.go, 1 carrying a test that names it — read the thread to decide",
			Candidates: []evidence.Candidate{
				{SHA: "abc12345", Subject: "fix the thing", Tests: []string{"a_test.go"}},
				{SHA: "def67890", Subject: "tidy up"},
			},
		},
		{
			Thread: evidence.Thread{Path: "b.go", Line: 7, Author: "claude"},
			Note:   "no commit after c1 touches b.go",
		},
	}
	var buf bytes.Buffer
	if err := writeDispositions(&buf, pr, "head1", ds); err != nil {
		t.Fatalf("writeDispositions: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"itsHabib/workbench#231 @ head1", "2 unresolved",
		"a.go:12", "b.go:7",
		"abc12345 fix the thing", "[test: a_test.go]",
		"def67890 tidy up",
		"gate does not judge whether these are fixed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// The two things the sweep must never emit, because it no longer concludes.
	if strings.Contains(out, "resolve:") {
		t.Errorf("a resolve command was printed:\n%s", out)
	}
	if strings.Contains(strings.ToLower(out), "dispositioned") {
		t.Errorf("output claims a verdict:\n%s", out)
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
