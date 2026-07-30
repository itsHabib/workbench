package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/contracts/automode"
)

func TestRunRefusesMalformedInput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"decide"}, strings.NewReader(`{"unknown":true}`), &stdout, &stderr)
	if code != codeRefused {
		t.Fatalf("code = %d, want %d", code, codeRefused)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "decode request") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunRefusesTrailingJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	input := `{"envelope":{"kind":"shell","command":"git status"}} {"second":true}`
	code := run([]string{"decide"}, strings.NewReader(input), &stdout, &stderr)
	if code != codeRefused {
		t.Fatalf("code = %d, want %d", code, codeRefused)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "exactly one JSON object") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunEmitsDecisionAndExitCode(t *testing.T) {
	input := `{"harness":"codex","envelope":{"kind":"shell","shell":"direct","command":"go test ./..."}}`
	var stdout, stderr bytes.Buffer
	code := run([]string{"decide"}, strings.NewReader(input), &stdout, &stderr)
	if code != codePass {
		t.Fatalf("code = %d, stderr=%q", code, stderr.String())
	}
	var decision automode.Decision
	if err := json.Unmarshal(stdout.Bytes(), &decision); err != nil {
		t.Fatal(err)
	}
	if decision.Outcome != automode.OutcomePass || decision.RuleFired != "safe.read_or_test" {
		t.Fatalf("decision = %+v", decision)
	}
	if err := automode.ValidateDecision(decision); err != nil {
		t.Fatalf("invalid decision: %v", err)
	}
}

func TestOutcomeCodePinsPublicSeam(t *testing.T) {
	tests := map[string]int{
		automode.OutcomePass:   codePass,
		automode.OutcomeBlock:  codeBlocked,
		automode.OutcomePark:   codeParked,
		automode.OutcomeRefuse: codeRefused,
	}
	for outcome, want := range tests {
		if got := outcomeCode(outcome); got != want {
			t.Errorf("%s = %d, want %d", outcome, got, want)
		}
	}
}
