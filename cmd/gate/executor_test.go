package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	gateexecutor "github.com/itsHabib/workbench/cmd/gate/internal/executor"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
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

func TestPreparationConsumed(t *testing.T) {
	approval := gateauthorization.PreparationApproval{
		PreparationID: "gpr_" + strings.Repeat("a", 64),
	}
	body, err := json.Marshal(approval)
	if err != nil {
		t.Fatal(err)
	}
	audit := state.AuditResult{
		OK: true,
		All: []state.Artifact{{
			Kind: state.KindGatePreparation, Body: body,
		}},
	}
	if !preparationConsumed(audit, approval.PreparationID) {
		t.Fatal("existing preparation was not consumed")
	}
	if preparationConsumed(audit, "gpr_"+strings.Repeat("b", 64)) {
		t.Fatal("different preparation reported consumed")
	}
}

// TestMergedRefusalStatePublishesBeforeRefusing pins the publish-then-refuse
// contract: an already_merged preparation carries the refusal error INSIDE the
// prepared state, alongside the snapshot files, so cmdExecutorPrepare pushes
// the superseding refusal terminal to the hosted ledger before propagating the
// refusal. Any other outcome declines without building a state.
func TestMergedRefusalStatePublishesBeforeRefusing(t *testing.T) {
	e := testEnv(t)
	// The snapshot has substance only once the store has at least one entry —
	// exactly the situation in production, where the refusal terminal was just
	// appended.
	if _, err := e.st.Append(state.KindEvidence, "run_x", nil, map[string]string{"note": "refusal"}); err != nil {
		t.Fatal(err)
	}
	refusedResult := gateResult{Outcome: outcomeAlreadyMerged, Run: "run_x"}

	prepared, refused, err := mergedRefusalState(e, codeRefused, refusedResult)
	if err != nil || !refused {
		t.Fatalf("refused=%v err=%v, want a refusal state", refused, err)
	}
	if prepared.refuse == nil || len(prepared.files.Log) == 0 || len(prepared.files.Anchor) == 0 {
		t.Fatalf("refusal state must carry both the refusal and the publishable snapshot, got %+v", prepared)
	}

	for name, tc := range map[string]struct {
		code   int
		result gateResult
	}{
		"merge outcome":            {codeMerge, gateResult{Outcome: "would_merge"}},
		"refused but not merged":   {codeRefused, gateResult{Outcome: "capability_refused"}},
		"parked is not a refusal":  {codeParked, gateResult{Outcome: "parked_for_judgment"}},
		"blocked is not a refusal": {codeBlocked, gateResult{Outcome: "blocked"}},
	} {
		if _, refused, err := mergedRefusalState(e, tc.code, tc.result); refused || err != nil {
			t.Fatalf("%s: refused=%v err=%v, want pass-through", name, refused, err)
		}
	}
}

func TestValidatePreparedOutcome(t *testing.T) {
	tests := []struct {
		name     string
		decision string
		code     int
		wantErr  bool
	}{
		{name: "pass action", decision: "pass", code: codeMerge},
		{name: "deterministic block wins", decision: "pass", code: codeBlocked},
		{name: "approved block", decision: "block", code: codeBlocked},
		{name: "block cannot emit action", decision: "block", code: codeMerge, wantErr: true},
		{name: "park cannot publish", decision: "pass", code: codeParked, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validatePreparedOutcome(test.decision, test.code, "outcome")
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestValidateAppendOnlyPromotion(t *testing.T) {
	remote := gateexecutor.StateFiles{Log: []byte("one\n"), Anchor: []byte("old")}
	tests := []struct {
		name      string
		candidate gateexecutor.StateFiles
		wantErr   bool
	}{
		{
			name: "strict extension",
			candidate: gateexecutor.StateFiles{
				Log: []byte("one\ntwo\n"), Anchor: []byte("new"),
			},
		},
		{
			name: "same log",
			candidate: gateexecutor.StateFiles{
				Log: []byte("one\n"), Anchor: []byte("new"),
			},
			wantErr: true,
		},
		{
			name: "replacement",
			candidate: gateexecutor.StateFiles{
				Log: []byte("two\n"), Anchor: []byte("new"),
			},
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAppendOnlyPromotion(test.candidate, remote)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
		})
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

func TestExecutorBootstrapRequiresExplicitDedicatedStateBeforeCredentials(t *testing.T) {
	t.Setenv("GATE_STATE", t.TempDir())
	t.Setenv("GATE_KEY", t.TempDir())
	t.Setenv("INPUT_APP_PRIVATE_KEY", "must-not-be-read")
	err := cmdExecutor([]string{
		"bootstrap",
		"-state-tip", strings.Repeat("a", 40),
		"-action", "act_exact",
		"-repo", "o/r",
		"-pr", "17",
		"-head", strings.Repeat("b", 40),
		"-app-id", "1",
		"-installation-id", "2",
	})
	var refusal executorRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("bootstrap = %v, want refusal", err)
	}
	if os.Getenv("INPUT_APP_PRIVATE_KEY") != "must-not-be-read" {
		t.Fatal("implicit state paths reached App credential custody")
	}
}

func TestExecutorFlagPresent(t *testing.T) {
	if !executorFlagPresent([]string{"-state", "x"}, "-state") {
		t.Fatal("separate flag not found")
	}
	if !executorFlagPresent([]string{"-state=x"}, "-state") {
		t.Fatal("equals flag not found")
	}
	if executorFlagPresent([]string{"-stateful", "x"}, "-state") {
		t.Fatal("prefix-only argument accepted")
	}
}

func TestValidateWorkflowRunBindsTrustedWorkflowAndImmutableActors(t *testing.T) {
	var run workflowRunFacts
	run.ID = 42
	run.RunAttempt = 1
	run.Event = "workflow_dispatch"
	// GitHub's Actions run API reports the workflow path without a ref.
	// HeadBranch and HeadSHA carry the independently verified main binding.
	run.Path = ".github/workflows/gate-executor.yml"
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
			value.Path = ".github/workflows/other.yml"
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
			value.Path = ".github/workflows/gate-executor.yml@main"
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
