package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
		"0123456789012345678901234567890123456789", true, time.Unix(1000, 0),
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
	result, err := executorResult(
		claim, gateexecutor.CommandResult{ExitCode: 0}, nil, "", false,
		time.Unix(1000, 0),
	)
	if err == nil ||
		result.Outcome != gateauthorization.ExecutionFailed ||
		result.ErrorCode != "executor_command_failed" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestExpiredClaimResultRecordsObservedExactHeadMerge(t *testing.T) {
	head := strings.Repeat("a", 40)
	merge := strings.Repeat("b", 40)
	claim := gateauthorization.ExecutionClaim{
		ClaimID: "claim", ExecutionID: "execution", MergeArgv: []string{"stored"},
		Subject: gateauthorization.Subject{HeadSHA: head, BaseRef: "main"},
	}
	result := expiredClaimResult(
		claim,
		gateexecutor.PullState{
			HeadSHA: head, BaseRef: "main", Merged: true, MergeCommit: merge,
		},
		time.Unix(1000, 0),
	)
	if result.Outcome != gateauthorization.ExecutionMerged ||
		result.MergeCommit != merge || result.ErrorCode != "" ||
		len(result.MergeArgv) != 1 || result.MergeArgv[0] != "stored" {
		t.Fatalf("result = %+v", result)
	}
}

func TestExpiredClaimResultClosesUnmergedAndChangedSubjects(t *testing.T) {
	head := strings.Repeat("a", 40)
	claim := gateauthorization.ExecutionClaim{
		ClaimID: "claim", ExecutionID: "execution", MergeArgv: []string{"stored"},
		Subject: gateauthorization.Subject{HeadSHA: head, BaseRef: "main"},
	}
	unmerged := expiredClaimResult(
		claim, gateexecutor.PullState{HeadSHA: head, BaseRef: "main"},
		time.Unix(1000, 0),
	)
	if unmerged.Outcome != gateauthorization.ExecutionFailed ||
		unmerged.ErrorCode != "executor_claim_expired_unmerged" {
		t.Fatalf("unmerged result = %+v", unmerged)
	}
	changed := expiredClaimResult(
		claim,
		gateexecutor.PullState{
			HeadSHA: strings.Repeat("b", 40), BaseRef: "main", Merged: true,
			MergeCommit: strings.Repeat("c", 40),
		},
		time.Unix(1000, 0),
	)
	if changed.Outcome != gateauthorization.ExecutionFailed ||
		changed.MergeCommit != "" ||
		changed.ErrorCode != "executor_claim_subject_changed" {
		t.Fatalf("changed result = %+v", changed)
	}
}

func TestValidExecutorClaimID(t *testing.T) {
	valid := "gxc_" + strings.Repeat("a", 64)
	if !validExecutorClaimID(valid) {
		t.Fatal("valid claim ID refused")
	}
	for _, invalid := range []string{
		"", strings.Repeat("a", 64), "gxc_" + strings.Repeat("A", 64),
		"gxc_" + strings.Repeat("g", 64), "gxc_" + strings.Repeat("a", 63),
	} {
		if validExecutorClaimID(invalid) {
			t.Fatalf("invalid claim ID accepted: %q", invalid)
		}
	}
}

func TestExecutorReconcileRefusesMalformedIdentityBeforeCredentials(t *testing.T) {
	t.Setenv("INPUT_APP_PRIVATE_KEY", "must-not-be-read")
	err := cmdExecutor([]string{
		"reconcile",
		"-claim", "not-a-claim",
		"-state-tip", strings.Repeat("a", 40),
		"-app-id", "1",
		"-installation-id", "2",
		"-key", t.TempDir(),
	})
	var refusal executorRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("reconcile = %v, want refusal", err)
	}
	if os.Getenv("INPUT_APP_PRIVATE_KEY") != "must-not-be-read" {
		t.Fatal("malformed claim reached App credential custody")
	}
}

func TestValidateWorkflowRunBindsTrustedWorkflowAndImmutableActors(t *testing.T) {
	var run workflowRunFacts
	run.ID = 42
	run.RunAttempt = 1
	run.Event = "workflow_dispatch"
	run.Path = ".github/workflows/gate-executor.yml@main"
	run.HeadBranch = "main"
	run.HeadSHA = strings.Repeat("a", 40)
	run.Repository.FullName = "o/r"
	run.Actor.ID = 7
	run.Actor.Login = "dispatcher"
	run.TriggeringActor.ID = 8
	run.TriggeringActor.Login = "rerunner"
	if err := validateWorkflowRun(
		run, "o/r", 42, 7, "rerunner", strings.Repeat("a", 40),
	); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*workflowRunFacts){
		"other workflow": func(value *workflowRunFacts) {
			value.Path = ".github/workflows/other.yml@main"
		},
		"other workflow ref": func(value *workflowRunFacts) {
			value.HeadBranch = "feature"
		},
		"tag dispatch": func(value *workflowRunFacts) {
			value.HeadBranch = "v1.0.0"
		},
		"stale default head": func(value *workflowRunFacts) {
			value.HeadSHA = strings.Repeat("b", 40)
		},
		"noncanonical workflow path": func(value *workflowRunFacts) {
			value.Path = ".github/workflows/gate-executor.yml@refs/heads/main"
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
			if err := validateWorkflowRun(
				candidate, "o/r", 42, 7, "rerunner", strings.Repeat("a", 40),
			); err == nil {
				t.Fatal("expected workflow run refusal")
			}
		})
	}
}

func TestExecutorMalformedArtifactExitsRefused(t *testing.T) {
	if os.Getenv("GATE_EXECUTOR_ARGV_HELPER") == "1" {
		os.Args = []string{
			"gate", "executor", "run",
			"-request", os.Getenv("GATE_EXECUTOR_BAD_REQUEST"),
			"-state-tip", "0123456789012345678901234567890123456789",
			"-workflow-run-id", "42",
			"-workflow-actor-id", "7",
			"-workflow-triggering-actor", "dispatcher",
			"-app-id", "1",
			"-installation-id", "2",
			"-key", filepath.Join(os.TempDir(), "unused-keys"),
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

func TestExecutorReadEnvironmentExcludesAppCustody(t *testing.T) {
	t.Setenv("GH_TOKEN", "read-token")
	t.Setenv("INPUT_APP_PRIVATE_KEY", "private-key")
	got := executorReadEnvironment()
	joined := strings.Join(got, "\n")
	if !strings.Contains(joined, "GH_TOKEN=read-token") {
		t.Fatal("read token missing from gh environment")
	}
	if strings.Contains(joined, "private-key") ||
		strings.Contains(joined, "INPUT_APP_PRIVATE_KEY") {
		t.Fatal("App private key crossed into read-only gh child")
	}
}
