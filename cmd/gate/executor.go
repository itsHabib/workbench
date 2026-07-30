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
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
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
		return errors.New("executor: prepare-request, prepare, request, bootstrap, run, or reconcile required")
	}
	switch args[0] {
	case "prepare-request":
		return cmdExecutorPrepareRequest(args[1:])
	case "prepare":
		return cmdExecutorPrepare(args[1:])
	case "request":
		return cmdExecutorRequest(args[1:])
	case "bootstrap":
		return cmdExecutorBootstrap(args[1:])
	case "run":
		return cmdExecutorRun(args[1:])
	case "reconcile":
		return cmdExecutorReconcile(args[1:])
	default:
		return fmt.Errorf("executor: unknown verb %q", args[0])
	}
}

func cmdExecutorPrepareRequest(args []string) error {
	fs := flag.NewFlagSet("executor prepare-request", flag.ContinueOnError)
	repo := fs.String("repo", "", "owner/repo")
	pr := fs.Int("pr", 0, "pull request number")
	head := fs.String("head", "", "expected exact head SHA")
	grantID := fs.String("grant", "", "operator-minted hosted grant")
	decision := fs.String("decision", "", "pass or block")
	why := fs.String("why", "", "operator judgment reasoning")
	replayID := fs.String("replay", "", "evt_<32 lowercase hex> delivery identity")
	out := fs.String("out", "", "GatePreparationRequestV1 path")
	help, err := parseFlags(fs, args)
	if err != nil || help {
		return err
	}
	if *repo == "" || *pr < 1 || *head == "" || *grantID == "" ||
		*decision == "" || *why == "" || *replayID == "" || *out == "" {
		return errors.New("executor prepare-request: -repo -pr -head -grant -decision -why -replay -out required")
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
	now := time.Now().UTC()
	request := gateauthorization.PreparationRequest{
		Subject: subject, GrantID: *grantID, Decision: *decision, Why: *why,
		IssuedAt: now, ExpiresAt: now.Add(20 * time.Minute), ReplayID: *replayID,
		TrustRoot: gateauthorization.TrustRoot{
			Kind:        gateauthorization.TrustGitHubEnvironment,
			Environment: gateauthorization.DefaultEnvironment,
		},
	}
	id, err := gateauthorization.PreparationID(request)
	if err != nil {
		return refuseExecutor(err)
	}
	artifact := gateauthorization.PreparationRequestArtifact{
		SchemaVersion: gateauthorization.SchemaVersion,
		PreparationID: id, Request: request,
	}
	if err := writeExecutorArtifact(*out, artifact); err != nil {
		return err
	}
	printJSON(map[string]any{
		"preparation_id":   id,
		"approval_comment": gateauthorization.ExpectedPreparationApprovalComment(id, request),
		"path":             *out,
	})
	return nil
}

type executorPrepareFlags struct {
	stateDir        string
	floorBin        string
	keyDir          string
	requestPath     string
	stateTip        string
	workflowRunID   int64
	workflowActorID int64
	triggeringActor string
	appID           int64
	installationID  int64
	apiURL          string
}

type preparedGateState struct {
	result   gateResult
	actionID string
	files    gateexecutor.StateFiles
}

func cmdExecutorPrepare(args []string) error {
	flags, help, err := parseExecutorPrepareFlags(args)
	if err != nil || help {
		return err
	}
	document, approval, e, err := preflightExecutorPreparation(flags)
	if err != nil {
		return err
	}
	prepared, err := evaluateHostedPreparation(e, document, approval)
	if err != nil {
		return err
	}
	snapshot, err := publishHostedPreparation(flags, document, prepared.files)
	if err != nil {
		return err
	}
	printJSON(map[string]any{
		"preparation_id": document.PreparationID,
		"run":            prepared.result.Run,
		"outcome":        prepared.result.Outcome,
		"action_id":      prepared.actionID,
		"head":           prepared.result.HeadSHA,
		"tip":            snapshot.Tip,
	})
	return nil
}

func parseExecutorPrepareFlags(args []string) (executorPrepareFlags, bool, error) {
	fs := flag.NewFlagSet("executor prepare", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	requestPath := fs.String("request", "", "GatePreparationRequestV1 path")
	stateTip := fs.String("state-tip", "", "exact gate-state checkout commit")
	workflowRunID := fs.Int64("workflow-run-id", 0, "current protected workflow run ID")
	workflowActorID := fs.Int64("workflow-actor-id", 0, "dispatching actor ID")
	triggeringActor := fs.String("workflow-triggering-actor", "", "GitHub login that triggered this attempt")
	appID := fs.Int64("app-id", 0, "dedicated Gate App ID")
	installationID := fs.Int64("installation-id", 0, "repository installation ID")
	apiURL := fs.String("api-url", "", "GitHub API URL")
	help, err := parseFlags(fs, args)
	if err != nil || help {
		return executorPrepareFlags{}, help, err
	}
	if *requestPath == "" || !validExecutorSHA(*stateTip) ||
		*workflowRunID < 1 || *workflowActorID < 1 ||
		*triggeringActor == "" || *appID < 1 || *installationID < 1 ||
		*keyDir == "" {
		return executorPrepareFlags{}, false, errors.New("executor prepare: valid -request -state-tip -workflow-run-id -workflow-actor-id -workflow-triggering-actor -app-id -installation-id -key required")
	}
	return executorPrepareFlags{
		stateDir: *stateDir, floorBin: *floorBin, keyDir: *keyDir,
		requestPath: *requestPath, stateTip: *stateTip,
		workflowRunID: *workflowRunID, workflowActorID: *workflowActorID,
		triggeringActor: *triggeringActor, appID: *appID,
		installationID: *installationID, apiURL: *apiURL,
	}, false, nil
}

func preflightExecutorPreparation(
	flags executorPrepareFlags,
) (
	gateauthorization.PreparationRequestArtifact,
	gateauthorization.PreparationApproval,
	env,
	error,
) {
	data, err := os.ReadFile(flags.requestPath)
	if err != nil {
		return gateauthorization.PreparationRequestArtifact{},
			gateauthorization.PreparationApproval{}, env{},
			fmt.Errorf("executor prepare: read request: %w", err)
	}
	document, err := gateauthorization.DecodePreparationRequest(data)
	if err != nil {
		return document, gateauthorization.PreparationApproval{}, env{},
			refuseExecutor(err)
	}
	reviews, err := readRunApprovals(
		document.Request.Subject.Repo, flags.workflowRunID,
	)
	if err != nil {
		return document, gateauthorization.PreparationApproval{}, env{}, err
	}
	actorID, triggeringActorID, err := readWorkflowActors(
		document.Request.Subject.Repo, flags.workflowRunID,
		flags.workflowActorID, flags.triggeringActor,
	)
	if err != nil {
		return document, gateauthorization.PreparationApproval{}, env{}, err
	}
	approval, err := authorization.AuthorizePreparation(
		document.Request,
		authorization.ApprovalFacts{
			WorkflowRunID: flags.workflowRunID, WorkflowActorID: actorID,
			TriggeringActorID: triggeringActorID, ObservedAt: time.Now().UTC(),
			Reviews: reviews,
		},
	)
	if err != nil {
		return document, approval, env{}, refuseExecutor(err)
	}
	live, err := readExecutorPull(
		document.Request.Subject.Repo, document.Request.Subject.Number,
		document.Request.Subject.HeadSHA,
	)
	if err != nil {
		return document, approval, env{}, classifyExecutorLiveRead(err)
	}
	if err := authorization.ValidateLive(document.Request.Subject, live); err != nil {
		return document, approval, env{}, refuseExecutor(err)
	}
	e, err := newEnv(flags.stateDir, flags.floorBin, flags.keyDir)
	if err != nil {
		return document, approval, env{}, err
	}
	audit, err := e.st.Audit()
	if err != nil {
		return document, approval, env{}, err
	}
	if err := authorization.ValidateRepositoryScope(
		audit, document.Request.Subject.Repo,
	); err != nil {
		return document, approval, env{}, refuseExecutor(err)
	}
	if preparationConsumed(audit, document.PreparationID) {
		return document, approval, env{},
			refuseExecutor(errors.New("executor prepare: preparation already consumed"))
	}
	remoteTip, err := readExecutorStateTip(document.Request.Subject.Repo)
	if err != nil {
		return document, approval, env{}, err
	}
	if remoteTip != flags.stateTip {
		return document, approval, env{}, refuseExecutor(gateexecutor.ErrStateCAS)
	}
	return document, approval, e, nil
}

func evaluateHostedPreparation(
	e env,
	document gateauthorization.PreparationRequestArtifact,
	approval gateauthorization.PreparationApproval,
) (preparedGateState, error) {
	preparationRun := state.NewRunID()
	if _, err := e.st.Append(
		state.KindGatePreparation, preparationRun,
		[]string{document.Request.GrantID}, approval,
	); err != nil {
		return preparedGateState{}, err
	}
	result, code, err := runGateWithSynthesis(
		e, document.Request.Subject.Repo, document.Request.Subject.Number,
		document.Request.GrantID, false, "local", true, false,
	)
	if err != nil {
		return preparedGateState{}, err
	}
	if code == codeParked {
		result, code, err = applyPreparationJudgment(e, document, result)
	}
	if err != nil {
		return preparedGateState{}, err
	}
	if err := validatePreparedOutcome(document.Request.Decision, code, result.Outcome); err != nil {
		return preparedGateState{}, refuseExecutor(err)
	}
	if result.HeadSHA != document.Request.Subject.HeadSHA {
		return preparedGateState{}, refuseExecutor(authorization.ErrLiveMismatch)
	}
	candidateAudit, err := e.st.Audit()
	if err != nil {
		return preparedGateState{}, err
	}
	actionID := ""
	if code == codeMerge {
		actionID, err = preparedActionID(
			candidateAudit, result.Run, result.Hash, document.Request,
		)
		if err != nil {
			return preparedGateState{}, refuseExecutor(err)
		}
	}
	files, err := readExecutorStateFiles(e)
	if err != nil {
		return preparedGateState{}, err
	}
	return preparedGateState{
		result: result, actionID: actionID, files: files,
	}, nil
}

func validatePreparedOutcome(decision string, code int, outcome string) error {
	if decision == verify.DecisionBlock && code == codeMerge {
		return errors.New("executor prepare: approved block cannot publish a merge action")
	}
	if code != codeMerge && code != codeBlocked {
		return fmt.Errorf("executor prepare: gate outcome %s", outcome)
	}
	return nil
}

func applyPreparationJudgment(
	e env,
	document gateauthorization.PreparationRequestArtifact,
	result gateResult,
) (gateResult, int, error) {
	arts, err := e.st.Run(result.Run)
	if err != nil {
		return gateResult{}, 0, err
	}
	_, escalationID, _, err := runVerdicts(arts)
	if err != nil {
		return gateResult{}, 0, err
	}
	judged, code, _, err := applyJudgment(
		e, result.Run, escalationID, document.Request.GrantID,
		judgmentOptions{
			Decision: document.Request.Decision,
			Why:      document.Request.Why,
		},
	)
	return judged, code, err
}

func publishHostedPreparation(
	flags executorPrepareFlags,
	document gateauthorization.PreparationRequestArtifact,
	files gateexecutor.StateFiles,
) (gateexecutor.StateSnapshot, error) {
	config, err := readExecutorAppConfig(
		flags.appID, flags.installationID, flags.apiURL,
		document.Request.Subject.Repo,
	)
	if err != nil {
		return gateexecutor.StateSnapshot{}, err
	}
	defer wipeBytes(config.PrivateKeyPEM)
	var snapshot gateexecutor.StateSnapshot
	err = gateexecutor.WithSession(
		context.Background(), config,
		func(session *gateexecutor.Session) error {
			remote, fetchErr := session.FetchGateState(
				context.Background(), flags.stateTip,
			)
			if fetchErr != nil {
				return fetchErr
			}
			if err := validateAppendOnlyPromotion(files, remote.Files); err != nil {
				return refuseExecutor(fmt.Errorf("executor prepare: %w", err))
			}
			snapshot, fetchErr = session.PublishGateState(
				context.Background(), remote.Tip,
				"gate: prepare "+document.PreparationID, files,
			)
			return fetchErr
		},
	)
	if err != nil {
		return gateexecutor.StateSnapshot{}, err
	}
	return snapshot, nil
}

func preparationConsumed(audit state.AuditResult, preparationID string) bool {
	for _, artifact := range audit.All {
		if artifact.Kind != state.KindGatePreparation {
			continue
		}
		var approval gateauthorization.PreparationApproval
		if json.Unmarshal(artifact.Body, &approval) == nil &&
			approval.PreparationID == preparationID {
			return true
		}
	}
	return false
}

func preparedActionID(
	audit state.AuditResult,
	run, hash string,
	request gateauthorization.PreparationRequest,
) (string, error) {
	for _, artifact := range audit.All {
		if artifact.Kind != state.KindAction || artifact.Run != run ||
			artifact.Hash != hash {
			continue
		}
		_, err := authorization.BuildRequest(
			audit,
			authorization.RequestInput{
				ActionID: artifact.ID, Subject: request.Subject,
				JudgmentQuestion: request.Why, ReplayID: request.ReplayID,
				IssuedAt: request.IssuedAt, ExpiresAt: request.ExpiresAt,
			},
		)
		if err != nil {
			return "", err
		}
		return artifact.ID, nil
	}
	return "", errors.New("executor prepare: passing action not found")
}

type executorBootstrapFlags struct {
	explicitState  bool
	explicitKey    bool
	stateTip       string
	actionID       string
	repo           string
	pr             int
	head           string
	appID          int64
	installationID int64
	keyDir         string
}

type executorBootstrapPlan struct {
	expectedTip string
	files       gateexecutor.StateFiles
	argv        []string
	subject     gateauthorization.Subject
	pr          int
	entries     int
	auditHead   string
}

func (flags executorBootstrapFlags) valid() bool {
	return flags.explicitState && flags.explicitKey &&
		validExecutorSHA(flags.stateTip) && flags.actionID != "" &&
		gateexecutor.ValidRepository(flags.repo) && flags.pr > 0 &&
		validExecutorSHA(flags.head) && flags.appID > 0 &&
		flags.installationID > 0 && flags.keyDir != ""
}

func cmdExecutorBootstrap(args []string) error {
	explicitState := executorFlagPresent(args, "-state")
	explicitKey := executorFlagPresent(args, "-key")
	fs := flag.NewFlagSet("executor bootstrap", flag.ContinueOnError)
	stateDir, floorBin, keyDir := commonFlags(fs)
	stateTip := fs.String("state-tip", "", "exact current gate-state commit")
	actionID := fs.String("action", "", "exact would_merge bootstrap action")
	repo := fs.String("repo", "", "owner/repo")
	pr := fs.Int("pr", 0, "pull request number")
	head := fs.String("head", "", "expected exact head SHA")
	appID := fs.Int64("app-id", 0, "dedicated Gate App ID")
	installationID := fs.Int64("installation-id", 0, "repository installation ID")
	apiURL := fs.String("api-url", "", "GitHub API URL")
	help, err := parseFlags(fs, args)
	if err != nil || help {
		return err
	}
	flags := executorBootstrapFlags{
		explicitState: explicitState, explicitKey: explicitKey,
		stateTip: *stateTip, actionID: *actionID, repo: *repo, pr: *pr,
		head: *head, appID: *appID, installationID: *installationID,
		keyDir: *keyDir,
	}
	if !flags.valid() {
		return refuseExecutor(errors.New(
			"executor bootstrap: explicit -state -key and valid -state-tip -action -repo -pr -head -app-id -installation-id required",
		))
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
	e, err := newEnv(*stateDir, *floorBin, *keyDir)
	if err != nil {
		return err
	}
	audit, err := e.st.Audit()
	if err != nil {
		return err
	}
	if !audit.OK || len(audit.All) == 0 {
		return refuseExecutor(errors.New("executor bootstrap: audited non-empty state required"))
	}
	if err := authorization.ValidateRepositoryScope(audit, *repo); err != nil {
		return classifyExecutorAuthorizationError(err)
	}
	argv, err := authorization.BootstrapMergeArgv(audit, *actionID, subject)
	if err != nil {
		return classifyExecutorAuthorizationError(err)
	}
	files, err := readExecutorStateFiles(e)
	if err != nil {
		return err
	}
	if err := gateexecutor.ValidateStateFiles(files); err != nil {
		return refuseExecutor(fmt.Errorf("executor bootstrap: state payload invalid: %w", err))
	}
	remoteTip, err := readExecutorStateTip(*repo)
	if err != nil {
		return err
	}
	if remoteTip != *stateTip {
		return refuseExecutor(gateexecutor.ErrStateCAS)
	}
	config, err := readExecutorAppConfig(
		*appID, *installationID, *apiURL, *repo,
	)
	if err != nil {
		return err
	}
	defer wipeBytes(config.PrivateKeyPEM)
	return gateexecutor.WithSession(
		context.Background(), config, func(session *gateexecutor.Session) error {
			plan := executorBootstrapPlan{
				expectedTip: *stateTip, files: files, argv: argv,
				subject: subject, pr: *pr, entries: len(audit.All),
				auditHead: audit.All[len(audit.All)-1].Hash,
			}
			return executeExecutorBootstrap(context.Background(), session, plan)
		},
	)
}

func executeExecutorBootstrap(
	ctx context.Context,
	session *gateexecutor.Session,
	plan executorBootstrapPlan,
) error {
	remote, err := session.FetchGateState(ctx, plan.expectedTip)
	if err != nil {
		return err
	}
	if err := validateAppendOnlyPromotion(plan.files, remote.Files); err != nil {
		return refuseExecutor(fmt.Errorf("executor bootstrap: %w", err))
	}
	snapshot, err := session.PublishGateState(
		ctx, plan.expectedTip, "gate: bootstrap dedicated hosted state", plan.files,
	)
	if err != nil {
		return err
	}
	command, mergeErr := session.Merge(ctx, plan.argv)
	pull, err := session.ReadPullState(ctx, plan.pr)
	if err != nil {
		return err
	}
	if pull.HeadSHA != plan.subject.HeadSHA || pull.BaseRef != plan.subject.BaseRef {
		return refuseExecutor(authorization.ErrLiveMismatch)
	}
	if !pull.Merged {
		if mergeErr != nil {
			return mergeErr
		}
		if command.ExitCode == 0 {
			return errMergeConfirmation
		}
		return errors.New("executor bootstrap merge failed without an error")
	}
	printJSON(map[string]any{
		"argv":         plan.argv,
		"entries":      plan.entries,
		"head":         plan.auditHead,
		"merge_commit": pull.MergeCommit,
		"tip":          snapshot.Tip,
	})
	return nil
}

func validateAppendOnlyPromotion(
	candidate gateexecutor.StateFiles,
	remote gateexecutor.StateFiles,
) error {
	if !bytes.HasPrefix(candidate.Log, remote.Log) ||
		len(candidate.Log) == len(remote.Log) {
		return errors.New("candidate state is not an append-only promotion")
	}
	return nil
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

func executorFlagPresent(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
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
		authorization.ErrRepositoryScope,
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
	path := ".github/workflows/gate-executor.yml"
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
