package main

import (
	"errors"
	"os"
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
