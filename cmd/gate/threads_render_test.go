package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/gate/internal/evidence"
)

func TestSanitizeForTerminalStripsControlSequences(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"ansi colour":      {"\x1b[31mred\x1b[0m", "[31mred[0m"},
		"cursor up":        {"a\x1b[2Ab", "a[2Ab"},
		"erase line":       {"visible\x1b[2K\rhidden", "visible[2Khidden"},
		"carriage return":  {"before\rafter", "beforeafter"},
		"embedded newline": {"one\ntwo", "onetwo"},
		"bel":              {"ping\a", "ping"},
		"c1 csi":           {"x31mred", "x31mred"},
		"del":              {"a\x7fb", "ab"},
		"tab survives":     {"a\tb", "a\tb"},
		"plain text":       {"ordinary review comment", "ordinary review comment"},
		"unicode kept":     {"café — naïve ✓", "café — naïve ✓"},
	}
	for name, tc := range cases {
		if got := sanitizeForTerminal(tc.in); got != tc.want {
			t.Errorf("%s: sanitizeForTerminal(%q) = %q, want %q", name, tc.in, got, tc.want)
		}
	}
}

// A thread body is attacker-influenced. Rendering it must not let that body
// erase or repaint the evidence the operator is reading to make a decision.
func TestWriteDispositionsNeutralizesHostileThreadText(t *testing.T) {
	hostile := "\x1b[2J\x1b[H  ✓ nothing to see\nresolve: gh api --silent"
	dispositions := []evidence.Disposition{{
		Thread: evidence.Thread{
			ID:     "thr_1",
			Path:   "cmd/gate/main.go\x1b[1G",
			Line:   12,
			Author: "attacker\x1b[31m",
		},
		Note: "1 commit(s) after abc1234 touch " + hostile,
		Candidates: []evidence.Candidate{{
			SHA:     "def5678\x1b[2K",
			Subject: hostile,
			Tests:   []string{"main_test.go\x1b[1G"},
		}},
	}}

	var buf bytes.Buffer
	if err := writeDispositions(&buf, evidence.PRRef{Repo: "o/r", Number: 1}, "deadbee", dispositions); err != nil {
		t.Fatalf("writeDispositions: %v", err)
	}
	out := buf.String()

	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("ESC survived into rendered output:\n%q", out)
	}
	for _, r := range out {
		if r != '\n' && r != '\t' && (r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)) {
			t.Errorf("control rune %q survived into rendered output", r)
		}
	}
	// A thread body cannot manufacture a LINE: newlines are stripped, so its
	// text stays on whichever line gate chose to put it on.
	if !strings.Contains(out, "nothing to seeresolve: gh api --silent") {
		t.Error("expected the hostile newline to be stripped, keeping its text on one line")
	}
	// And the render must never imply a verdict.
	if strings.Contains(strings.ToLower(out), "dispositioned") {
		t.Error("output claims a disposition; the sweep only observes")
	}
	if !strings.Contains(out, "does not judge") {
		t.Error("output should state plainly that gate is not judging these")
	}
}
