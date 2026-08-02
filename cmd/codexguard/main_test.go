package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/contracts/automode"
)

// projectionTempDir is t.TempDir() with symlinks resolved, for fixtures that
// become a projection home. Darwin hands out per-test directories under
// /var/folders/..., and /var is itself a symlink to private/var — a linked
// ancestor the projection rightly refuses.
func projectionTempDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

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

func TestRunRefusesCallerSelectedGateState(t *testing.T) {
	var stdout, stderr bytes.Buffer
	input := `{"gate_state":"C:/forged","envelope":{"kind":"shell","command":"git status"}}`
	code := run([]string{"decide"}, strings.NewReader(input), &stdout, &stderr)
	if code != codeRefused {
		t.Fatalf("code = %d, want %d", code, codeRefused)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "unknown field") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunEmitsDecisionAndExitCode(t *testing.T) {
	input := `{"envelope":{"kind":"shell","shell":"direct","command":"go test ./..."}}`
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

func TestRunRefusesCallerSelectedHarness(t *testing.T) {
	var stdout, stderr bytes.Buffer
	input := `{"harness":"forged","envelope":{"kind":"shell","command":"git status"}}`
	code := run([]string{"decide"}, strings.NewReader(input), &stdout, &stderr)
	if code != codeRefused {
		t.Fatalf("code = %d, want %d", code, codeRefused)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "unknown field") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
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

func TestRunHookPersistsBeforeNativeDeny(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	t.Setenv("CODEXGUARD_AUDIT", auditPath)
	input, err := os.ReadFile(filepath.Join("testdata", "hooks", "pre-bash-block.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"hook"}, bytes.NewReader(input), &stdout, &stderr)
	if code != codePass {
		t.Fatalf("code = %d, stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"permissionDecision":"deny"`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
	audit, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	var event automode.AuditEvent
	if err := json.Unmarshal(audit, &event); err != nil {
		t.Fatal(err)
	}
	if event.Kind != automode.EventDecision || event.Decision.Outcome != automode.OutcomeBlock {
		t.Fatalf("event = %+v", event)
	}
}

func TestRunHookMalformedInputIsOperationalFailure(t *testing.T) {
	t.Setenv("CODEXGUARD_AUDIT", filepath.Join(t.TempDir(), "audit.jsonl"))
	var stdout, stderr bytes.Buffer
	code := run([]string{"hook"}, strings.NewReader(`{"hook_event_name":"PreToolUse"}`), &stdout, &stderr)
	if code != codeError || stdout.Len() != 0 || !strings.Contains(stderr.String(), "required field missing") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunProjectionStatusAndApply(t *testing.T) {
	home := filepath.Join(projectionTempDir(t), "codex")
	var statusOut, statusErr bytes.Buffer
	code := run([]string{"projection", "status", "-home", home}, strings.NewReader(""), &statusOut, &statusErr)
	if code != codePass {
		t.Fatalf("status code=%d stderr=%q", code, statusErr.String())
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("status mutated home: %v", err)
	}
	var plan struct {
		Digest string `json:"digest"`
	}
	if err := json.Unmarshal(statusOut.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	var applyOut, applyErr bytes.Buffer
	code = run(
		[]string{"projection", "apply", "-home", home, "-expect", plan.Digest},
		strings.NewReader(""),
		&applyOut,
		&applyErr,
	)
	if code != codePass {
		t.Fatalf("apply code=%d stderr=%q", code, applyErr.String())
	}
	projected, err := os.ReadFile(filepath.Join(home, "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	executable, err := trustedExecutablePath()
	if err != nil {
		t.Fatal(err)
	}
	var hooks struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command        string `json:"command"`
				CommandWindows string `json:"commandWindows"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(projected, &hooks); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"PreToolUse", "PermissionRequest", "PostToolUse"} {
		command := hooks.Hooks[event][0].Hooks[0]
		if !strings.Contains(command.Command, executable) ||
			!strings.Contains(command.CommandWindows, executable) {
			t.Fatalf("%s does not bind executable %q: %+v", event, executable, command)
		}
	}
}

func TestRenderedHooksRefuseNonAbsoluteExecutable(t *testing.T) {
	if _, err := renderedHooks("codexguard"); err == nil {
		t.Fatal("renderedHooks accepted a PATH-resolved executable")
	}
}

func TestRunProjectionRefusesChangedTarget(t *testing.T) {
	home := filepath.Join(projectionTempDir(t), "codex")
	var stdout, stderr bytes.Buffer
	code := run([]string{"projection", "status", "-home", home}, strings.NewReader(""), &stdout, &stderr)
	if code != codePass {
		t.Fatalf("status code=%d stderr=%q", code, stderr.String())
	}
	var plan struct {
		Digest string `json:"digest"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "hooks.json"), []byte("owned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	code = run(
		[]string{"projection", "apply", "-home", home, "-expect", plan.Digest},
		strings.NewReader(""),
		&stdout,
		&stderr,
	)
	if code != codeRefused || !strings.Contains(stderr.String(), "plan changed") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
