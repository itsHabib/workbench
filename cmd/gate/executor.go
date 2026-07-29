package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/authorization"
	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	gateexecutor "github.com/itsHabib/workbench/cmd/gate/internal/executor"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/contracts/gateauthorization"
)

// executorRefusal marks an expected policy/contract refusal. The top-level
// command maps it to Gate's stable exit 3; transport, storage, credential, and
// process failures remain hard errors (exit 4).
type executorRefusal struct {
	err error
}

func (e executorRefusal) Error() string { return e.err.Error() }
func (e executorRefusal) Unwrap() error { return e.err }

func refuseExecutor(err error) error {
	if err == nil {
		return nil
	}
	var refusal executorRefusal
	if errors.As(err, &refusal) {
		return err
	}
	return executorRefusal{err: err}
}

func cmdExecutor(args []string) error {
	if len(args) == 0 {
		return errors.New("executor: request, claim, verify, or execute required")
	}
	switch args[0] {
	case "request":
		return cmdExecutorRequest(args[1:])
	case "claim":
		return cmdExecutorClaim(args[1:])
	case "verify":
		return cmdExecutorVerify(args[1:])
	case "execute":
		return cmdExecutorExecute(args[1:])
	default:
		return fmt.Errorf("executor: unknown verb %q", args[0])
	}
}

func cmdExecutorRequest(args []string) error {
	fs := flag.NewFlagSet("executor request", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	actionID := fs.String("action", "", "exact would_merge action artifact")
	repo := fs.String("repo", "", "owner/repo")
	pr := fs.Int("pr", 0, "pull request number")
	head := fs.String("head", "", "expected exact head SHA")
	question := fs.String("question", "", "exact judgment question")
	replayID := fs.String("replay", "", "evt_<32 lowercase hex> delivery identity")
	out := fs.String("out", "", "GateAuthorizationRequestV1 path")
	help, err := parseFlags(fs, args)
	if err != nil || help {
		return err
	}
	if *actionID == "" || *repo == "" || *pr < 1 || *head == "" ||
		*question == "" || *replayID == "" || *out == "" {
		return errors.New("executor request: -action -repo -pr -head -question -replay -out required")
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	live, err := readExecutorPull(*repo, *pr, *head)
	if err != nil {
		return classifyExecutorLiveRead(err)
	}
	subject := gateauthorization.Subject{
		Repo: live.Repository, Number: live.Number, HeadSHA: live.HeadSHA,
		BaseRef: live.BaseRef, BaseSHA: live.BaseSHA,
		MergeBaseSHA: live.MergeBaseSHA,
	}
	if err := authorization.ValidateLive(subject, live); err != nil {
		return refuseExecutor(err)
	}
	audit, err := e.st.Audit()
	if err != nil {
		return err
	}
	if !audit.OK {
		return fmt.Errorf("executor state invalid: %s", audit.Reason)
	}
	now := time.Now().UTC()
	request, err := authorization.BuildRequest(audit, authorization.RequestInput{
		ActionID:         *actionID,
		Subject:          subject,
		JudgmentQuestion: *question, ReplayID: *replayID,
		IssuedAt: now, ExpiresAt: now.Add(20 * time.Minute),
	})
	if err != nil {
		return refuseExecutor(err)
	}
	id, err := gateauthorization.AuthorizationID(request)
	if err != nil {
		return refuseExecutor(err)
	}
	artifact := gateauthorization.RequestArtifact{
		SchemaVersion:   gateauthorization.SchemaVersion,
		AuthorizationID: id, Request: request,
	}
	if err := gateauthorization.ValidateRequestArtifact(artifact); err != nil {
		return refuseExecutor(err)
	}
	if err := writeExecutorArtifact(*out, artifact); err != nil {
		return err
	}
	printJSON(map[string]any{
		"authorization_id": id,
		"approval_comment": gateauthorization.ExpectedApprovalComment(id, request),
		"path":             *out,
	})
	return nil
}

func cmdExecutorClaim(args []string) error {
	fs := flag.NewFlagSet("executor claim", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	requestPath := fs.String("request", "", "GateAuthorizationRequestV1 path")
	workflowRunID := fs.Int64("workflow-run-id", 0, "current protected workflow run ID")
	workflowActorID := fs.Int64("workflow-actor-id", 0, "dispatching/re-running actor ID")
	triggeringActor := fs.String("workflow-triggering-actor", "", "GitHub login that triggered this attempt")
	out := fs.String("out", "", "GateExecutionClaimV1 path")
	help, err := parseFlags(fs, args)
	if err != nil || help {
		return err
	}
	if *requestPath == "" || *workflowRunID < 1 || *workflowActorID < 1 ||
		*triggeringActor == "" || *out == "" {
		return errors.New("executor claim: -request -workflow-run-id -workflow-actor-id -workflow-triggering-actor -out required")
	}
	requestData, err := os.ReadFile(*requestPath)
	if err != nil {
		return fmt.Errorf("executor claim: read request: %w", err)
	}
	requestArtifact, err := gateauthorization.DecodeRequest(requestData)
	if err != nil {
		return refuseExecutor(err)
	}
	reviews, err := readRunApprovals(requestArtifact.Request.Subject.Repo, *workflowRunID)
	if err != nil {
		return err
	}
	validatedWorkflowActorID, triggeringActorID, err := readWorkflowActors(
		requestArtifact.Request.Subject.Repo, *workflowRunID,
		*workflowActorID, *triggeringActor,
	)
	if err != nil {
		return err
	}
	approved, err := authorization.Authorize(requestArtifact.Request, authorization.ApprovalFacts{
		WorkflowRunID: *workflowRunID, WorkflowActorID: validatedWorkflowActorID,
		TriggeringActorID: triggeringActorID, ObservedAt: time.Now().UTC(),
		Reviews: reviews,
	})
	if err != nil {
		return refuseExecutor(err)
	}
	live, err := readExecutorPull(
		approved.Request.Subject.Repo, approved.Request.Subject.Number,
		approved.Request.Subject.HeadSHA,
	)
	if err != nil {
		return classifyExecutorLiveRead(err)
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	ttl := approved.Request.ExpiresAt.Sub(now)
	if ttl <= 0 {
		return refuseExecutor(authorization.ErrExpired)
	}
	grant, err := capability.MintBound(
		e.st, e.keyPath, approved.Request.Subject.Repo, "merge", "T3", 1,
		fmt.Sprintf("gh:%s via environment:%d", approved.Receipt.ActorLogin, approved.Receipt.WorkflowRunID),
		ttl, approved.Request.Subject.HeadSHA, approved.Request.Subject.Number,
		approved.AuthorizationID, time.Now,
	)
	if err != nil {
		return classifyExecutorAuthorizationError(err)
	}
	_, claim, err := authorization.Claim(
		e.st, e.keyPath, approved, live, grant.ID, now,
	)
	if err != nil {
		return classifyExecutorAuthorizationError(err)
	}
	if err := writeExecutorArtifact(*out, claim); err != nil {
		return err
	}
	printJSON(map[string]any{
		"claim_id": claim.ClaimID, "execution_id": claim.ExecutionID,
		"authorization_id": claim.AuthorizationID, "grant_id": claim.ExecutionGrantID,
		"path": *out,
	})
	return nil
}

func cmdExecutorVerify(args []string) error {
	fs := flag.NewFlagSet("executor verify", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	claimID := fs.String("claim", "", "gxc_<64 lowercase hex>")
	help, err := parseFlags(fs, args)
	if err != nil || help {
		return err
	}
	if *claimID == "" {
		return errors.New("executor verify: -claim required")
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	audit, err := e.st.Audit()
	if err != nil {
		return err
	}
	_, provisional, err := claimFromAudit(audit, *claimID)
	if err != nil {
		return refuseExecutor(err)
	}
	live, err := readExecutorPull(
		provisional.Subject.Repo, provisional.Subject.Number,
		provisional.Subject.HeadSHA,
	)
	if err != nil {
		return classifyExecutorLiveRead(err)
	}
	_, claim, err := authorization.VerifyDurableClaim(audit, *claimID, live, time.Now().UTC())
	if err != nil {
		return refuseExecutor(err)
	}
	printJSON(map[string]any{
		"claim_id": claim.ClaimID, "execution_id": claim.ExecutionID,
		"repo": claim.Subject.Repo, "pr": claim.Subject.Number,
		"head_sha": claim.Subject.HeadSHA, "durable": true,
	})
	return nil
}

func cmdExecutorExecute(args []string) error {
	fs := flag.NewFlagSet("executor execute", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	claimID := fs.String("claim", "", "durable GateExecutionClaimV1 ID")
	appID := fs.Int64("app-id", 0, "dedicated Gate App ID")
	installationID := fs.Int64("installation-id", 0, "repository installation ID")
	apiURL := fs.String("api-url", "", "GitHub API URL")
	help, err := parseFlags(fs, args)
	if err != nil || help {
		return err
	}
	if *claimID == "" || *appID < 1 || *installationID < 1 {
		return errors.New("executor execute: -claim -app-id -installation-id required")
	}
	readToken, err := readExecutorReadToken()
	if err != nil {
		return err
	}
	if err := os.Setenv("GH_TOKEN", readToken); err != nil {
		return err
	}
	defer os.Unsetenv("GH_TOKEN")
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	audit, err := e.st.Audit()
	if err != nil {
		return err
	}
	_, provisional, err := claimFromAudit(audit, *claimID)
	if err != nil {
		return refuseExecutor(err)
	}
	live, err := readExecutorPull(
		provisional.Subject.Repo, provisional.Subject.Number,
		provisional.Subject.HeadSHA,
	)
	if err != nil {
		return classifyExecutorLiveRead(err)
	}
	claimArtifact, claim, err := authorization.VerifyDurableClaim(
		audit, *claimID, live, time.Now().UTC(),
	)
	if err != nil {
		return refuseExecutor(err)
	}
	appConfig, err := readExecutorAppConfig(*appID, *installationID, *apiURL)
	if err != nil {
		return err
	}
	defer wipeBytes(appConfig.PrivateKeyPEM)
	result, executeErr := gateexecutor.Execute(context.Background(), appConfig, claim.MergeArgv)
	mergeCommit, confirmErr := readMergedCommit(claim.Subject.Repo, claim.Subject.Number)
	record, executeErr := executorResult(claim, result, executeErr, mergeCommit, confirmErr, time.Now().UTC())
	if _, err := authorization.RecordResult(e.st, claimArtifact, claim, record); err != nil {
		if errors.Is(err, authorization.ErrResultDuplicate) {
			return refuseExecutor(err)
		}
		return err
	}
	if executeErr != nil {
		return executeErr
	}
	printJSON(map[string]any{
		"claim_id": claim.ClaimID, "execution_id": claim.ExecutionID,
		"outcome": record.Outcome, "merge_commit": record.MergeCommit,
		"argv": claim.MergeArgv,
	})
	return nil
}

func classifyExecutorAuthorizationError(err error) error {
	refusals := []error{
		authorization.ErrApprovalMissing,
		authorization.ErrApprovalDependent,
		authorization.ErrApprovalMismatch,
		authorization.ErrExpired,
		authorization.ErrNotYetValid,
		authorization.ErrLiveMismatch,
		authorization.ErrHeadAmbiguous,
		authorization.ErrActionMissing,
		authorization.ErrActionMismatch,
		authorization.ErrSuperseded,
		authorization.ErrAlreadyClaimed,
		authorization.ErrSubjectClaimPending,
		authorization.ErrResultDuplicate,
		capability.ErrExpired,
		capability.ErrScope,
		capability.ErrSignature,
		capability.ErrBadHead,
		capability.ErrHeadMismatch,
		capability.ErrBadSubject,
		capability.ErrSubject,
		state.ErrAlreadyExists,
		state.ErrNotFound,
	}
	for _, refusal := range refusals {
		if errors.Is(err, refusal) {
			return refuseExecutor(err)
		}
	}
	return err
}

func classifyExecutorLiveRead(err error) error {
	if errors.Is(err, authorization.ErrLiveMismatch) ||
		errors.Is(err, authorization.ErrHeadAmbiguous) {
		return refuseExecutor(err)
	}
	return err
}

func readExecutorReadToken() (string, error) {
	readToken := os.Getenv("INPUT_GITHUB_READ_TOKEN")
	if readToken == "" {
		return "", errors.New("executor execute: INPUT_GITHUB_READ_TOKEN is required")
	}
	_ = os.Unsetenv("INPUT_GITHUB_READ_TOKEN")
	return readToken, nil
}

func readExecutorAppConfig(appID, installationID int64, apiURL string) (gateexecutor.AppConfig, error) {
	if apiURL != "" && apiURL != "https://api.github.com" {
		return gateexecutor.AppConfig{}, errors.New("executor execute: only https://api.github.com is supported")
	}
	privateKey := []byte(os.Getenv("INPUT_APP_PRIVATE_KEY"))
	if len(privateKey) == 0 {
		return gateexecutor.AppConfig{}, errors.New("executor execute: INPUT_APP_PRIVATE_KEY is required")
	}
	_ = os.Unsetenv("INPUT_APP_PRIVATE_KEY")
	return gateexecutor.AppConfig{
		AppID: appID, InstallationID: installationID,
		PrivateKeyPEM: privateKey, APIURL: apiURL,
	}, nil
}

func executorResult(claim gateauthorization.ExecutionClaim, command gateexecutor.CommandResult, executeErr error, mergeCommit string, confirmErr error, completedAt time.Time) (gateauthorization.ExecutionResult, error) {
	result := gateauthorization.ExecutionResult{
		SchemaVersion: gateauthorization.SchemaVersion,
		ExecutionID:   claim.ExecutionID, ClaimID: claim.ClaimID,
		Outcome:     gateauthorization.ExecutionFailed,
		MergeArgv:   append([]string(nil), claim.MergeArgv...),
		CompletedAt: completedAt, ErrorCode: "executor_command_failed",
	}
	if confirmErr == nil {
		result.Outcome = gateauthorization.ExecutionMerged
		result.MergeCommit = mergeCommit
		result.ErrorCode = ""
		return result, nil
	}
	if executeErr == nil && command.ExitCode == 0 {
		result.ErrorCode = "merge_confirmation_failed"
		return result, confirmErr
	}
	return result, executeErr
}

func claimFromAudit(audit state.AuditResult, claimID string) (state.Artifact, gateauthorization.ExecutionClaim, error) {
	if !audit.OK {
		return state.Artifact{}, gateauthorization.ExecutionClaim{},
			fmt.Errorf("executor state invalid: %s", audit.Reason)
	}
	for _, artifact := range audit.All {
		if artifact.Kind != state.KindExecutionClaim {
			continue
		}
		var claim gateauthorization.ExecutionClaim
		if err := json.Unmarshal(artifact.Body, &claim); err != nil {
			return state.Artifact{}, gateauthorization.ExecutionClaim{}, err
		}
		if claim.ClaimID == claimID {
			return artifact, claim, nil
		}
	}
	return state.Artifact{}, gateauthorization.ExecutionClaim{}, errors.New("executor claim not found")
}

func readRunApprovals(repo string, runID int64) ([]authorization.ApprovalReview, error) {
	var response []struct {
		Environments []struct {
			Name string `json:"name"`
		} `json:"environments"`
		User struct {
			Login string `json:"login"`
			ID    int64  `json:"id"`
		} `json:"user"`
		State   string `json:"state"`
		Comment string `json:"comment"`
	}
	if err := ghDecodeExecutor(
		&response, "api", fmt.Sprintf("repos/%s/actions/runs/%d/approvals", repo, runID),
	); err != nil {
		return nil, err
	}
	out := make([]authorization.ApprovalReview, 0, len(response))
	for _, item := range response {
		environments := make([]string, 0, len(item.Environments))
		for _, environment := range item.Environments {
			environments = append(environments, environment.Name)
		}
		out = append(out, authorization.ApprovalReview{
			WorkflowRunID: runID, ActorLogin: item.User.Login, ActorID: item.User.ID,
			State: strings.ToLower(item.State), Comment: item.Comment,
			Environments: environments,
		})
	}
	return out, nil
}

type workflowRunFacts struct {
	ID         int64  `json:"id"`
	RunAttempt int    `json:"run_attempt"`
	Event      string `json:"event"`
	Path       string `json:"path"`
	Actor      struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	} `json:"actor"`
	TriggeringActor struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	} `json:"triggering_actor"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func readWorkflowActors(repo string, runID, expectedActorID int64, expectedTriggeringLogin string) (int64, int64, error) {
	var run workflowRunFacts
	if err := ghDecodeExecutor(
		&run, "api", fmt.Sprintf("repos/%s/actions/runs/%d", repo, runID),
	); err != nil {
		return 0, 0, err
	}
	if err := validateWorkflowRun(run, repo, runID, expectedActorID, expectedTriggeringLogin); err != nil {
		return 0, 0, refuseExecutor(err)
	}
	return run.Actor.ID, run.TriggeringActor.ID, nil
}

func validateWorkflowRun(run workflowRunFacts, repo string, runID, expectedActorID int64, expectedTriggeringLogin string) error {
	path := ".github/workflows/gate-executor.yml"
	if run.ID != runID || run.Repository.FullName != repo || run.RunAttempt != 1 ||
		run.Event != "workflow_dispatch" ||
		(run.Path != path && !strings.HasPrefix(run.Path, path+"@")) {
		return errors.New("executor workflow run identity mismatch")
	}
	if run.Actor.ID < 1 || run.TriggeringActor.ID < 1 ||
		run.Actor.ID != expectedActorID ||
		run.TriggeringActor.Login != expectedTriggeringLogin {
		return errors.New("executor workflow actor identity mismatch")
	}
	return nil
}

func readExecutorPull(repo string, number int, expectedHead string) (authorization.LivePullRequest, error) {
	var repository struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	}
	if err := ghDecodeExecutor(&repository, "api", "repos/"+repo); err != nil {
		return authorization.LivePullRequest{}, err
	}
	var pull struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"base"`
	}
	if err := ghDecodeExecutor(&pull, "api", fmt.Sprintf("repos/%s/pulls/%d", repo, number)); err != nil {
		return authorization.LivePullRequest{}, err
	}
	if repository.FullName != repo || repository.DefaultBranch == "" ||
		pull.Base.Ref != repository.DefaultBranch {
		return authorization.LivePullRequest{}, authorization.ErrLiveMismatch
	}
	if expectedHead != "" && pull.Head.SHA != expectedHead {
		return authorization.LivePullRequest{}, authorization.ErrLiveMismatch
	}
	var comparison struct {
		MergeBase struct {
			SHA string `json:"sha"`
		} `json:"merge_base_commit"`
	}
	if err := ghDecodeExecutor(
		&comparison, "api",
		fmt.Sprintf("repos/%s/compare/%s...%s", repo, pull.Base.SHA, pull.Head.SHA),
	); err != nil {
		return authorization.LivePullRequest{}, err
	}
	var associated [][]struct {
		Number int    `json:"number"`
		State  string `json:"state"`
		Head   struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := ghDecodeExecutor(
		&associated, "api", "--paginate", "--slurp",
		fmt.Sprintf("repos/%s/commits/%s/pulls", repo, pull.Head.SHA),
	); err != nil {
		return authorization.LivePullRequest{}, err
	}
	var open []int
	for _, page := range associated {
		for _, candidate := range page {
			if candidate.State == "open" && candidate.Head.SHA == pull.Head.SHA {
				open = append(open, candidate.Number)
			}
		}
	}
	return authorization.LivePullRequest{
		Repository: repo, Number: pull.Number, State: pull.State,
		HeadSHA: pull.Head.SHA, BaseRef: pull.Base.Ref, BaseSHA: pull.Base.SHA,
		MergeBaseSHA: comparison.MergeBase.SHA, OpenPRsForHead: open,
	}, nil
}

func readMergedCommit(repo string, number int) (string, error) {
	var pull struct {
		Merged         bool   `json:"merged"`
		MergeCommitSHA string `json:"merge_commit_sha"`
	}
	if err := ghDecodeExecutor(&pull, "api", fmt.Sprintf("repos/%s/pulls/%d", repo, number)); err != nil {
		return "", err
	}
	if !pull.Merged || len(pull.MergeCommitSHA) != 40 {
		return "", errors.New("executor merge confirmation missing")
	}
	return strings.ToLower(pull.MergeCommitSHA), nil
}

func ghDecodeExecutor(value any, args ...string) error {
	command := exec.Command("gh", args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("executor github read: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if err := json.Unmarshal(stdout.Bytes(), value); err != nil {
		return fmt.Errorf("executor github decode: %w", err)
	}
	return nil
}

func writeExecutorArtifact(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	existing, err := os.ReadFile(path)
	if err == nil {
		if bytes.Equal(existing, data) {
			return nil
		}
		return errors.New("executor output exists with different content")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".gate-executor-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func wipeBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}
