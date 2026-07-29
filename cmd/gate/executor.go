package main

import (
	"bytes"
	"context"
	"encoding/hex"
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

var errMergeConfirmation = errors.New("executor merge confirmation missing")

// executorRefusal maps expected policy failures to Gate's stable exit 3.
// Transport, storage, credential, and process failures remain exit 4.
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
		return errors.New("executor: request, run, or reconcile required")
	}
	switch args[0] {
	case "request":
		return cmdExecutorRequest(args[1:])
	case "run":
		return cmdExecutorRun(args[1:])
	case "reconcile":
		return cmdExecutorReconcile(args[1:])
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
		JudgmentQuestion: *question,
		ReplayID:         *replayID,
		IssuedAt:         now,
		ExpiresAt:        now.Add(20 * time.Minute),
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
		AuthorizationID: id,
		Request:         request,
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

func cmdExecutorRun(args []string) error {
	fs := flag.NewFlagSet("executor run", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	requestPath := fs.String("request", "", "GateAuthorizationRequestV1 path")
	stateTip := fs.String("state-tip", "", "exact gate-state checkout commit")
	workflowRunID := fs.Int64("workflow-run-id", 0, "current protected workflow run ID")
	workflowActorID := fs.Int64("workflow-actor-id", 0, "dispatching actor ID")
	triggeringActor := fs.String("workflow-triggering-actor", "", "GitHub login that triggered this attempt")
	appID := fs.Int64("app-id", 0, "dedicated Gate App ID")
	installationID := fs.Int64("installation-id", 0, "repository installation ID")
	apiURL := fs.String("api-url", "", "GitHub API URL")
	help, err := parseFlags(fs, args)
	if err != nil || help {
		return err
	}
	if *requestPath == "" || *stateTip == "" || *workflowRunID < 1 ||
		*workflowActorID < 1 || *triggeringActor == "" || *appID < 1 ||
		*installationID < 1 || *keyDir == "" {
		return errors.New("executor run: -request -state-tip -workflow-run-id -workflow-actor-id -workflow-triggering-actor -app-id -installation-id -key required")
	}
	if !validExecutorSHA(*stateTip) {
		return refuseExecutor(errors.New("executor run: malformed state tip"))
	}
	request, err := readExecutorRequest(*requestPath)
	if err != nil {
		return err
	}
	approved, live, e, err := executorPreflight(
		request, *workflowRunID, *workflowActorID, *triggeringActor,
		*stateTip, *stateDir, *floorBin, *keyDir,
	)
	if err != nil {
		return err
	}
	config, err := readExecutorAppConfig(
		*appID, *installationID, *apiURL, approved.Request.Subject.Repo,
	)
	if err != nil {
		return err
	}
	defer wipeBytes(config.PrivateKeyPEM)
	return runCustodiedExecution(
		context.Background(), e, *keyDir, approved, live, *stateTip, config,
	)
}

func cmdExecutorReconcile(args []string) error {
	fs := flag.NewFlagSet("executor reconcile", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	claimID := fs.String("claim", "", "expired durable claim ID")
	stateTip := fs.String("state-tip", "", "exact gate-state checkout commit")
	appID := fs.Int64("app-id", 0, "dedicated Gate App ID")
	installationID := fs.Int64("installation-id", 0, "repository installation ID")
	apiURL := fs.String("api-url", "", "GitHub API URL")
	help, err := parseFlags(fs, args)
	if err != nil || help {
		return err
	}
	if !validExecutorClaimID(*claimID) || !validExecutorSHA(*stateTip) ||
		*appID < 1 || *installationID < 1 || *keyDir == "" {
		return refuseExecutor(errors.New("executor reconcile: valid -claim -state-tip -app-id -installation-id -key required"))
	}
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	audit, err := e.st.Audit()
	if err != nil {
		return err
	}
	_, claim, err := authorization.VerifyExpiredClaim(
		audit, *claimID, time.Now().UTC(),
	)
	if err != nil {
		return classifyExecutorAuthorizationError(err)
	}
	config, err := readExecutorAppConfig(
		*appID, *installationID, *apiURL, claim.Subject.Repo,
	)
	if err != nil {
		return err
	}
	defer wipeBytes(config.PrivateKeyPEM)
	return reconcileExpiredClaim(
		context.Background(), *claimID, *stateTip, *keyDir, config,
	)
}

func reconcileExpiredClaim(
	ctx context.Context,
	claimID, stateTip, keyDir string,
	config gateexecutor.AppConfig,
) error {
	return gateexecutor.WithSession(ctx, config, func(session *gateexecutor.Session) error {
		snapshot, err := session.FetchGateState(ctx, stateTip)
		if err != nil {
			return err
		}
		durable, cleanup, err := openExecutorSnapshot(snapshot.Files, keyDir)
		if err != nil {
			return err
		}
		defer cleanup()
		audit, err := durable.st.Audit()
		if err != nil {
			return err
		}
		artifact, claim, err := authorization.VerifyExpiredClaim(
			audit, claimID, time.Now().UTC(),
		)
		if err != nil {
			return classifyExecutorAuthorizationError(err)
		}
		pull, err := session.ReadPullState(ctx, claim.Subject.Number)
		if err != nil {
			return err
		}
		result := expiredClaimResult(claim, pull, time.Now().UTC())
		if _, err := authorization.RecordResult(
			durable.st, artifact, claim, result,
		); err != nil {
			return classifyExecutorAuthorizationError(err)
		}
		files, err := readExecutorStateFiles(durable)
		if err != nil {
			return err
		}
		if _, err := session.PublishGateState(
			ctx, snapshot.Tip, "gate: reconcile "+claim.ExecutionID, files,
		); err != nil {
			return err
		}
		printJSON(map[string]any{
			"claim_id": claim.ClaimID,
			"outcome":  result.Outcome,
			"reason":   result.ErrorCode,
		})
		return nil
	})
}

func expiredClaimResult(
	claim gateauthorization.ExecutionClaim,
	pull gateexecutor.PullState,
	completedAt time.Time,
) gateauthorization.ExecutionResult {
	result := gateauthorization.ExecutionResult{
		SchemaVersion: gateauthorization.SchemaVersion,
		ExecutionID:   claim.ExecutionID,
		ClaimID:       claim.ClaimID,
		Outcome:       gateauthorization.ExecutionFailed,
		MergeArgv:     append([]string(nil), claim.MergeArgv...),
		CompletedAt:   completedAt,
		ErrorCode:     "executor_claim_expired_unmerged",
	}
	if pull.HeadSHA != claim.Subject.HeadSHA ||
		pull.BaseRef != claim.Subject.BaseRef {
		result.ErrorCode = "executor_claim_subject_changed"
		return result
	}
	if !pull.Merged {
		return result
	}
	result.Outcome = gateauthorization.ExecutionMerged
	result.MergeCommit = pull.MergeCommit
	result.ErrorCode = ""
	return result
}

func readExecutorRequest(path string) (gateauthorization.RequestArtifact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return gateauthorization.RequestArtifact{}, fmt.Errorf("executor run: read request: %w", err)
	}
	request, err := gateauthorization.DecodeRequest(data)
	if err != nil {
		return gateauthorization.RequestArtifact{}, refuseExecutor(err)
	}
	return request, nil
}

func executorPreflight(
	request gateauthorization.RequestArtifact,
	workflowRunID, workflowActorID int64,
	triggeringActor, stateTip, stateDir, floorBin, keyDir string,
) (gateauthorization.Artifact, authorization.LivePullRequest, env, error) {
	reviews, err := readRunApprovals(request.Request.Subject.Repo, workflowRunID)
	if err != nil {
		return gateauthorization.Artifact{}, authorization.LivePullRequest{}, env{}, err
	}
	actorID, triggeringActorID, err := readWorkflowActors(
		request.Request.Subject.Repo, workflowRunID, workflowActorID, triggeringActor,
	)
	if err != nil {
		return gateauthorization.Artifact{}, authorization.LivePullRequest{}, env{}, err
	}
	approved, err := authorization.Authorize(request.Request, authorization.ApprovalFacts{
		WorkflowRunID:     workflowRunID,
		WorkflowActorID:   actorID,
		TriggeringActorID: triggeringActorID,
		ObservedAt:        time.Now().UTC(),
		Reviews:           reviews,
	})
	if err != nil {
		return gateauthorization.Artifact{}, authorization.LivePullRequest{}, env{},
			refuseExecutor(err)
	}
	live, err := readExecutorPull(
		approved.Request.Subject.Repo,
		approved.Request.Subject.Number,
		approved.Request.Subject.HeadSHA,
	)
	if err != nil {
		return gateauthorization.Artifact{}, authorization.LivePullRequest{}, env{},
			classifyExecutorLiveRead(err)
	}
	e, err := newEnv(stateDir, floorBin, keyDir)
	if err != nil {
		return gateauthorization.Artifact{}, authorization.LivePullRequest{}, env{}, err
	}
	audit, err := e.st.Audit()
	if err != nil {
		return gateauthorization.Artifact{}, authorization.LivePullRequest{}, env{}, err
	}
	if err := authorization.PreflightClaim(audit, approved, live, time.Now().UTC()); err != nil {
		return gateauthorization.Artifact{}, authorization.LivePullRequest{}, env{},
			classifyExecutorAuthorizationError(err)
	}
	remoteTip, err := readExecutorStateTip(approved.Request.Subject.Repo)
	if err != nil {
		return gateauthorization.Artifact{}, authorization.LivePullRequest{}, env{}, err
	}
	if remoteTip != stateTip {
		return gateauthorization.Artifact{}, authorization.LivePullRequest{}, env{},
			refuseExecutor(gateexecutor.ErrStateCAS)
	}
	return approved, live, e, nil
}

func runCustodiedExecution(
	ctx context.Context,
	e env,
	keyDir string,
	approved gateauthorization.Artifact,
	live authorization.LivePullRequest,
	stateTip string,
	config gateexecutor.AppConfig,
) error {
	return gateexecutor.WithSession(ctx, config, func(session *gateexecutor.Session) error {
		claimed, err := publishDurableClaim(
			ctx, session, e, keyDir, approved, live, stateTip,
		)
		if err != nil {
			return err
		}
		defer claimed.cleanup()
		return finishCustodiedExecution(ctx, session, claimed)
	})
}

type durableClaim struct {
	e        env
	artifact state.Artifact
	claim    gateauthorization.ExecutionClaim
	tip      string
	cleanup  func()
}

func publishDurableClaim(
	ctx context.Context,
	session *gateexecutor.Session,
	e env,
	keyDir string,
	approved gateauthorization.Artifact,
	live authorization.LivePullRequest,
	stateTip string,
) (durableClaim, error) {
	now := time.Now().UTC()
	ttl := approved.Request.ExpiresAt.Sub(now)
	if ttl <= 0 {
		return durableClaim{}, refuseExecutor(authorization.ErrExpired)
	}
	grant, err := capability.MintBound(
		e.st, e.keyPath, approved.Request.Subject.Repo, "merge", "T3", 1,
		fmt.Sprintf(
			"gh:%s via environment:%d",
			approved.Receipt.ActorLogin,
			approved.Receipt.WorkflowRunID,
		),
		ttl, approved.Request.Subject.HeadSHA, approved.Request.Subject.Number,
		approved.AuthorizationID, time.Now,
	)
	if err != nil {
		return durableClaim{}, classifyExecutorAuthorizationError(err)
	}
	_, claim, err := authorization.Claim(
		e.st, e.keyPath, approved, live, grant.ID, now,
	)
	if err != nil {
		return durableClaim{}, classifyExecutorAuthorizationError(err)
	}
	files, err := readExecutorStateFiles(e)
	if err != nil {
		return durableClaim{}, err
	}
	snapshot, err := session.PublishGateState(
		ctx, stateTip, "gate: claim "+claim.ClaimID, files,
	)
	if err != nil {
		return durableClaim{}, err
	}
	durable, cleanup, err := openExecutorSnapshot(snapshot.Files, keyDir)
	if err != nil {
		return durableClaim{}, err
	}
	claimed, err := verifyDurableClaim(ctx, session, durable, claim, snapshot.Tip)
	if err != nil {
		cleanup()
		return durableClaim{}, err
	}
	claimed.cleanup = cleanup
	return claimed, nil
}

func verifyDurableClaim(
	ctx context.Context,
	session *gateexecutor.Session,
	durable env,
	claim gateauthorization.ExecutionClaim,
	tip string,
) (durableClaim, error) {
	audit, err := durable.st.Audit()
	if err != nil {
		return durableClaim{}, err
	}
	if _, err := session.FetchGateState(ctx, tip); err != nil {
		return durableClaim{}, err
	}
	live, err := readExecutorPull(
		claim.Subject.Repo, claim.Subject.Number, claim.Subject.HeadSHA,
	)
	if err != nil {
		return durableClaim{}, classifyExecutorLiveRead(err)
	}
	artifact, claim, err := authorization.VerifyDurableClaim(
		audit, claim.ClaimID, live, time.Now().UTC(),
	)
	if err != nil {
		return durableClaim{}, classifyExecutorAuthorizationError(err)
	}
	return durableClaim{e: durable, artifact: artifact, claim: claim, tip: tip}, nil
}

func finishCustodiedExecution(
	ctx context.Context,
	session *gateexecutor.Session,
	claimed durableClaim,
) error {
	command, executeErr := session.Merge(ctx, claimed.claim.MergeArgv)
	mergeCommit, merged, err := readMergedCommit(
		claimed.claim.Subject.Repo, claimed.claim.Subject.Number,
	)
	if err != nil {
		return err
	}
	record, terminalErr := executorResult(
		claimed.claim, command, executeErr, mergeCommit, merged, time.Now().UTC(),
	)
	if errors.Is(terminalErr, errMergeConfirmation) {
		return terminalErr
	}
	if _, err := authorization.RecordResult(
		claimed.e.st, claimed.artifact, claimed.claim, record,
	); err != nil {
		if errors.Is(err, authorization.ErrResultDuplicate) {
			return refuseExecutor(err)
		}
		return err
	}
	files, err := readExecutorStateFiles(claimed.e)
	if err != nil {
		return err
	}
	if _, err := session.PublishGateState(
		ctx, claimed.tip, "gate: result "+claimed.claim.ExecutionID, files,
	); err != nil {
		return err
	}
	if terminalErr != nil {
		return terminalErr
	}
	printExecutorResult(claimed.claim, record)
	return nil
}

func printExecutorResult(
	claim gateauthorization.ExecutionClaim,
	result gateauthorization.ExecutionResult,
) {
	printJSON(map[string]any{
		"claim_id":     claim.ClaimID,
		"execution_id": claim.ExecutionID,
		"outcome":      result.Outcome,
		"merge_commit": result.MergeCommit,
		"argv":         claim.MergeArgv,
	})
}

func readExecutorStateFiles(e env) (gateexecutor.StateFiles, error) {
	log, err := os.ReadFile(filepath.Join(e.stateDir, "log.jsonl"))
	if err != nil {
		return gateexecutor.StateFiles{}, fmt.Errorf("executor state: read log: %w", err)
	}
	anchor, err := os.ReadFile(e.anchor)
	if err != nil {
		return gateexecutor.StateFiles{}, fmt.Errorf("executor state: read anchor: %w", err)
	}
	return gateexecutor.StateFiles{Log: log, Anchor: anchor}, nil
}

func openExecutorSnapshot(files gateexecutor.StateFiles, keyDir string) (env, func(), error) {
	root, err := os.MkdirTemp("", "gate-executor-state-*")
	if err != nil {
		return env{}, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	stateDir := filepath.Join(root, "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		cleanup()
		return env{}, func() {}, err
	}
	if err := os.WriteFile(filepath.Join(stateDir, "log.jsonl"), files.Log, 0o600); err != nil {
		cleanup()
		return env{}, func() {}, err
	}
	anchor := filepath.Join(root, "anchor.json")
	if err := os.WriteFile(anchor, files.Anchor, 0o600); err != nil {
		cleanup()
		return env{}, func() {}, err
	}
	e, err := newEnvWithAnchor(stateDir, "", keyDir, anchor)
	if err != nil {
		cleanup()
		return env{}, func() {}, err
	}
	return e, cleanup, nil
}

func executorResult(
	claim gateauthorization.ExecutionClaim,
	command gateexecutor.CommandResult,
	executeErr error,
	mergeCommit string,
	merged bool,
	completedAt time.Time,
) (gateauthorization.ExecutionResult, error) {
	result := gateauthorization.ExecutionResult{
		SchemaVersion: gateauthorization.SchemaVersion,
		ExecutionID:   claim.ExecutionID,
		ClaimID:       claim.ClaimID,
		Outcome:       gateauthorization.ExecutionFailed,
		MergeArgv:     append([]string(nil), claim.MergeArgv...),
		CompletedAt:   completedAt,
		ErrorCode:     "executor_command_failed",
	}
	if merged {
		result.Outcome = gateauthorization.ExecutionMerged
		result.MergeCommit = mergeCommit
		result.ErrorCode = ""
		return result, nil
	}
	if executeErr != nil {
		return result, executeErr
	}
	if command.ExitCode == 0 {
		return result, errMergeConfirmation
	}
	return result, errors.New("executor command failed without an error")
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
		authorization.ErrClaimNotExpired,
		capability.ErrExpired,
		capability.ErrScope,
		capability.ErrSignature,
		capability.ErrBadHead,
		capability.ErrHeadMismatch,
		capability.ErrBadSubject,
		capability.ErrSubject,
		state.ErrAlreadyExists,
		state.ErrNotFound,
		gateexecutor.ErrStateCAS,
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

func readExecutorAppConfig(
	appID, installationID int64,
	apiURL, repository string,
) (gateexecutor.AppConfig, error) {
	if apiURL != "" && apiURL != "https://api.github.com" {
		return gateexecutor.AppConfig{}, errors.New("executor run: only https://api.github.com is supported")
	}
	privateKey := []byte(os.Getenv("INPUT_APP_PRIVATE_KEY"))
	if len(privateKey) == 0 {
		return gateexecutor.AppConfig{}, errors.New("executor run: INPUT_APP_PRIVATE_KEY is required")
	}
	_ = os.Unsetenv("INPUT_APP_PRIVATE_KEY")
	return gateexecutor.AppConfig{
		AppID:          appID,
		InstallationID: installationID,
		PrivateKeyPEM:  privateKey,
		APIURL:         apiURL,
		Repository:     repository,
	}, nil
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
			WorkflowRunID: runID,
			ActorLogin:    item.User.Login,
			ActorID:       item.User.ID,
			State:         strings.ToLower(item.State),
			Comment:       item.Comment,
			Environments:  environments,
		})
	}
	return out, nil
}

type workflowRunFacts struct {
	ID         int64  `json:"id"`
	RunAttempt int    `json:"run_attempt"`
	Event      string `json:"event"`
	Path       string `json:"path"`
	HeadBranch string `json:"head_branch"`
	HeadSHA    string `json:"head_sha"`
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

func readWorkflowActors(
	repo string,
	runID, expectedActorID int64,
	expectedTriggeringLogin string,
) (int64, int64, error) {
	var run workflowRunFacts
	if err := ghDecodeExecutor(
		&run, "api", fmt.Sprintf("repos/%s/actions/runs/%d", repo, runID),
	); err != nil {
		return 0, 0, err
	}
	var mainRef struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := ghDecodeExecutor(
		&mainRef, "api", fmt.Sprintf("repos/%s/git/ref/heads/main", repo),
	); err != nil {
		return 0, 0, err
	}
	if err := validateWorkflowRun(
		run, repo, runID, expectedActorID, expectedTriggeringLogin,
		mainRef.Object.SHA,
	); err != nil {
		return 0, 0, refuseExecutor(err)
	}
	return run.Actor.ID, run.TriggeringActor.ID, nil
}

func validateWorkflowRun(
	run workflowRunFacts,
	repo string,
	runID, expectedActorID int64,
	expectedTriggeringLogin string,
	expectedHead string,
) error {
	path := ".github/workflows/gate-executor.yml@main"
	if run.ID != runID || run.Repository.FullName != repo || run.RunAttempt != 1 ||
		run.Event != "workflow_dispatch" || run.HeadBranch != "main" ||
		run.Path != path || !validExecutorSHA(run.HeadSHA) ||
		run.HeadSHA != expectedHead {
		return errors.New("executor workflow run identity mismatch")
	}
	if run.Actor.ID < 1 || run.TriggeringActor.ID < 1 ||
		run.Actor.ID != expectedActorID ||
		run.TriggeringActor.Login != expectedTriggeringLogin {
		return errors.New("executor workflow actor identity mismatch")
	}
	return nil
}

func readExecutorPull(
	repo string,
	number int,
	expectedHead string,
) (authorization.LivePullRequest, error) {
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
	if err := ghDecodeExecutor(
		&pull, "api", fmt.Sprintf("repos/%s/pulls/%d", repo, number),
	); err != nil {
		return authorization.LivePullRequest{}, err
	}
	if repository.FullName != repo || repository.DefaultBranch != "main" ||
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
		Repository:     repo,
		Number:         pull.Number,
		State:          pull.State,
		HeadSHA:        pull.Head.SHA,
		BaseRef:        pull.Base.Ref,
		BaseSHA:        pull.Base.SHA,
		MergeBaseSHA:   comparison.MergeBase.SHA,
		OpenPRsForHead: open,
	}, nil
}

func readExecutorStateTip(repo string) (string, error) {
	var response struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err := ghDecodeExecutor(
		&response, "api", fmt.Sprintf("repos/%s/git/ref/heads/gate-state", repo),
	); err != nil {
		return "", err
	}
	if !validExecutorSHA(response.Object.SHA) {
		return "", errors.New("executor state tip malformed")
	}
	return strings.ToLower(response.Object.SHA), nil
}

func readMergedCommit(repo string, number int) (string, bool, error) {
	var pull struct {
		Merged         bool   `json:"merged"`
		MergeCommitSHA string `json:"merge_commit_sha"`
	}
	if err := ghDecodeExecutor(
		&pull, "api", fmt.Sprintf("repos/%s/pulls/%d", repo, number),
	); err != nil {
		return "", false, err
	}
	if !pull.Merged {
		return "", false, nil
	}
	if !validExecutorSHA(pull.MergeCommitSHA) {
		return "", false, errors.New("executor merge confirmation malformed")
	}
	return strings.ToLower(pull.MergeCommitSHA), true, nil
}

func validExecutorSHA(value string) bool {
	if len(value) != 40 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validExecutorClaimID(value string) bool {
	const prefix = "gxc_"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	digest := strings.TrimPrefix(value, prefix)
	if len(digest) != 64 || digest != strings.ToLower(digest) {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func ghDecodeExecutor(value any, args ...string) error {
	command := exec.Command("gh", args...)
	command.Env = executorReadEnvironment()
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf(
			"executor github read: %w: %s",
			err,
			strings.TrimSpace(stderr.String()),
		)
	}
	if err := json.Unmarshal(stdout.Bytes(), value); err != nil {
		return fmt.Errorf("executor github decode: %w", err)
	}
	return nil
}

func executorReadEnvironment() []string {
	keys := []string{
		"GH_TOKEN", "GITHUB_TOKEN", "GH_HOST", "GH_CONFIG_DIR",
		"HOME", "USERPROFILE", "PATH", "SystemRoot", "TMP", "TEMP",
		"SSL_CERT_FILE", "SSL_CERT_DIR", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY",
	}
	environment := []string{"GH_PROMPT_DISABLED=1"}
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			environment = append(environment, key+"="+value)
		}
	}
	return environment
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
