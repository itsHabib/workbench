package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	gateexecutor "github.com/itsHabib/workbench/cmd/gate/internal/executor"
	"github.com/itsHabib/workbench/contracts/gateauthorization"
)

func TestWriteExecutorArtifactIsIdempotentButRefusesReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact.json")
	value := map[string]string{"version": "one"}
	if err := writeExecutorArtifact(path, value); err != nil {
		t.Fatal(err)
	}
	if err := writeExecutorArtifact(path, value); err != nil {
		t.Fatal(err)
	}
	if err := writeExecutorArtifact(path, map[string]string{"version": "two"}); err == nil {
		t.Fatal("expected replacement refusal")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("artifact permissions too broad: %o", info.Mode().Perm())
	}
}

func TestExecutorResultRecordsObservedMergeDespiteCommandError(t *testing.T) {
	claim := gateauthorization.ExecutionClaim{
		ClaimID: "claim", ExecutionID: "execution", MergeArgv: []string{"gh"},
	}
	result, err := executorResult(
		claim, gateexecutor.CommandResult{ExitCode: 1}, errors.New("branch cleanup failed"),
		"0123456789012345678901234567890123456789", nil, time.Unix(1000, 0),
	)
	if err != nil || result.Outcome != gateauthorization.ExecutionMerged ||
		result.MergeCommit == "" || result.ErrorCode != "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestExecutorResultRefusesUnconfirmedSuccess(t *testing.T) {
	claim := gateauthorization.ExecutionClaim{
		ClaimID: "claim", ExecutionID: "execution", MergeArgv: []string{"gh"},
	}
	confirmErr := errors.New("not merged")
	result, err := executorResult(
		claim, gateexecutor.CommandResult{ExitCode: 0}, nil, "", confirmErr,
		time.Unix(1000, 0),
	)
	if !errors.Is(err, confirmErr) ||
		result.Outcome != gateauthorization.ExecutionFailed ||
		result.ErrorCode != "merge_confirmation_failed" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestValidateWorkflowRunBindsTrustedWorkflowAndImmutableActors(t *testing.T) {
	var run workflowRunFacts
	run.ID = 42
	run.RunAttempt = 1
	run.Event = "workflow_dispatch"
	run.Path = ".github/workflows/gate-executor.yml@main"
	run.Repository.FullName = "o/r"
	run.Actor.ID = 7
	run.Actor.Login = "dispatcher"
	run.TriggeringActor.ID = 8
	run.TriggeringActor.Login = "rerunner"
	if err := validateWorkflowRun(run, "o/r", 42, 7, "rerunner"); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*workflowRunFacts){
		"other workflow": func(value *workflowRunFacts) {
			value.Path = ".github/workflows/other.yml@main"
		},
		"rerun attempt": func(value *workflowRunFacts) {
			value.RunAttempt = 2
		},
		"other repository": func(value *workflowRunFacts) {
			value.Repository.FullName = "other/r"
		},
		"other initial actor": func(value *workflowRunFacts) {
			value.Actor.ID = 9
		},
		"renamed or replaced triggering actor": func(value *workflowRunFacts) {
			value.TriggeringActor.Login = "other"
		},
		"missing immutable triggering actor": func(value *workflowRunFacts) {
			value.TriggeringActor.ID = 0
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidate := run
			mutate(&candidate)
			if err := validateWorkflowRun(candidate, "o/r", 42, 7, "rerunner"); err == nil {
				t.Fatal("expected workflow run refusal")
			}
		})
	}
}

func TestExecutorMalformedArtifactExitsRefused(t *testing.T) {
	if os.Getenv("GATE_EXECUTOR_ARGV_HELPER") == "1" {
		os.Args = []string{
			"gate", "executor", "claim",
			"-request", os.Getenv("GATE_EXECUTOR_BAD_REQUEST"),
			"-workflow-run-id", "42",
			"-workflow-actor-id", "7",
			"-workflow-triggering-actor", "dispatcher",
			"-out", filepath.Join(os.TempDir(), "unused-claim.json"),
		}
		main()
		return
	}
	request := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(request, []byte(`{"schema_version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=TestExecutorMalformedArtifactExitsRefused")
	command.Env = append(
		os.Environ(),
		"GATE_EXECUTOR_ARGV_HELPER=1",
		"GATE_EXECUTOR_BAD_REQUEST="+request,
	)
	err := command.Run()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		t.Fatalf("want executor refusal exit, got %v", err)
	}
	if exitError.ExitCode() != codeRefused {
		t.Fatalf("malformed executor artifact exited %d, want %d", exitError.ExitCode(), codeRefused)
	}
}

func TestExecutorExitClassificationKeepsOperationalFailuresHard(t *testing.T) {
	if got := commandErrorCode("executor", refuseExecutor(errors.New("stale authorization"))); got != codeRefused {
		t.Fatalf("executor refusal exit = %d, want %d", got, codeRefused)
	}
	if got := commandErrorCode("executor", errors.New("github transport failed")); got != codeError {
		t.Fatalf("executor transport exit = %d, want %d", got, codeError)
	}
	if got := commandErrorCode("gate", refuseExecutor(errors.New("scoped marker"))); got != codeError {
		t.Fatalf("non-executor error exit = %d, want %d", got, codeError)
	}
}
