// Package authorization owns Gate's policy for protected approvals, exact
// action freshness, permanent execution claims, and terminal result records.
// GitHub transport and App credential custody live in neighboring mechanisms.
package authorization

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/contracts"
	"github.com/itsHabib/workbench/contracts/gateauthorization"
)

// Coded refusal classes are stable enough for workflows and tests to branch
// on without parsing explanatory prose.
var (
	ErrApprovalMissing     = errors.New("authorization_approval_missing")
	ErrApprovalDependent   = errors.New("authorization_approval_not_independent")
	ErrApprovalMismatch    = errors.New("authorization_approval_mismatch")
	ErrExpired             = errors.New("authorization_expired")
	ErrNotYetValid         = errors.New("authorization_not_yet_valid")
	ErrLiveMismatch        = errors.New("authorization_live_pr_mismatch")
	ErrHeadAmbiguous       = errors.New("authorization_head_ambiguous")
	ErrActionMissing       = errors.New("authorization_action_missing")
	ErrActionMismatch      = errors.New("authorization_action_mismatch")
	ErrSuperseded          = errors.New("authorization_superseded")
	ErrAlreadyClaimed      = errors.New("authorization_already_claimed")
	ErrSubjectClaimPending = errors.New("authorization_subject_claim_pending")
	ErrResultDuplicate     = errors.New("authorization_result_duplicate")
)

type actionBody struct {
	Outcome string   `json:"outcome"`
	Verdict string   `json:"verdict"`
	Grant   string   `json:"grant"`
	Command string   `json:"command"`
	Argv    []string `json:"argv"`
}

// LivePullRequest is the trusted live projection checked both before claim and
// immediately before execution.
type LivePullRequest struct {
	Repository     string
	Number         int
	State          string
	HeadSHA        string
	BaseRef        string
	BaseSHA        string
	MergeBaseSHA   string
	OpenPRsForHead []int
}

// RequestInput supplies the live base identity and human-readable judgment
// question that are not encoded in Gate's existing action body.
type RequestInput struct {
	ActionID         string
	Subject          gateauthorization.Subject
	JudgmentQuestion string
	ReplayID         string
	IssuedAt         time.Time
	ExpiresAt        time.Time
}

// ApprovalReview is the relevant projection of GitHub's workflow-run approval
// history. Environments may contain several names; Gate requires its static one.
type ApprovalReview struct {
	WorkflowRunID int64
	ActorLogin    string
	ActorID       int64
	State         string
	Comment       string
	Environments  []string
}

// ApprovalFacts bind verification to this run and reject self-approval by the
// identity that dispatched or re-ran it.
type ApprovalFacts struct {
	WorkflowRunID     int64
	WorkflowActorID   int64
	TriggeringActorID int64
	ObservedAt        time.Time
	Reviews           []ApprovalReview
}

// ValidateLive refuses a moved, closed, or shared-head pull request before an
// authorization request is presented for protected approval.
func ValidateLive(subject gateauthorization.Subject, live LivePullRequest) error {
	return validateLive(subject, live)
}

// BuildRequest derives a canonical authorization request from an audited Gate
// action. It refuses a stale action before anything is presented for approval.
func BuildRequest(audit state.AuditResult, input RequestInput) (gateauthorization.Request, error) {
	if !audit.OK {
		return gateauthorization.Request{}, fmt.Errorf("authorization_state_invalid: %s", audit.Reason)
	}
	index, err := buildIndex(audit.All)
	if err != nil {
		return gateauthorization.Request{}, err
	}
	target, ok := index.byID[input.ActionID]
	if !ok || target.artifact.Kind != state.KindAction {
		return gateauthorization.Request{}, fmt.Errorf("%w: %s", ErrActionMissing, input.ActionID)
	}
	if err := matchSubject(target.subject, input.Subject); err != nil {
		return gateauthorization.Request{}, err
	}
	if err := validateAction(target.artifact, input.Subject); err != nil {
		return gateauthorization.Request{}, err
	}
	if newest := index.newestTerminal(input.Subject.Repo, input.Subject.Number); newest.artifact.ID != target.artifact.ID {
		return gateauthorization.Request{}, ErrSuperseded
	}
	digest, err := EvidenceDigest(audit.All, target.artifact.Run, target.order)
	if err != nil {
		return gateauthorization.Request{}, err
	}
	var body actionBody
	if err := json.Unmarshal(target.artifact.Body, &body); err != nil {
		return gateauthorization.Request{}, fmt.Errorf("%w: malformed action: %v", ErrActionMismatch, err)
	}
	request := gateauthorization.Request{
		Subject: input.Subject,
		Gate: gateauthorization.Decision{
			RunID: target.artifact.Run, ActionID: target.artifact.ID,
			ActionHash: target.artifact.Hash,
		},
		Outcome: body.Outcome, MergeArgv: append([]string(nil), body.Argv...),
		EvidenceDigest: digest, JudgmentQuestion: input.JudgmentQuestion,
		IssuedAt: input.IssuedAt.UTC(), ExpiresAt: input.ExpiresAt.UTC(),
		ReplayID: input.ReplayID,
		TrustRoot: gateauthorization.TrustRoot{
			Kind:        gateauthorization.TrustGitHubEnvironment,
			Environment: gateauthorization.DefaultEnvironment,
		},
	}
	if err := gateauthorization.ValidateRequest(request); err != nil {
		return gateauthorization.Request{}, fmt.Errorf("authorization_request_invalid: %w", err)
	}
	return request, nil
}

// Authorize selects the newest approval decision for Gate's static
// environment and proves that it came from a different immutable actor ID.
func Authorize(request gateauthorization.Request, facts ApprovalFacts) (gateauthorization.Artifact, error) {
	if err := gateauthorization.ValidateRequest(request); err != nil {
		return gateauthorization.Artifact{}, fmt.Errorf("authorization_request_invalid: %w", err)
	}
	if facts.WorkflowRunID < 1 || facts.WorkflowActorID < 1 || facts.TriggeringActorID < 1 {
		return gateauthorization.Artifact{}, ErrApprovalMissing
	}
	id, err := gateauthorization.AuthorizationID(request)
	if err != nil {
		return gateauthorization.Artifact{}, err
	}
	reviews := matchingReviews(facts)
	if len(reviews) == 0 {
		return gateauthorization.Artifact{}, ErrApprovalMissing
	}
	// The API does not expose approval timestamps or run-attempt identity.
	// Require one unambiguous decision for this static environment; workflow
	// run validation separately refuses re-runs.
	if len(reviews) != 1 {
		return gateauthorization.Artifact{}, ErrApprovalMismatch
	}
	review := reviews[0]
	if review.ActorID == facts.WorkflowActorID ||
		review.ActorID == facts.TriggeringActorID {
		return gateauthorization.Artifact{}, ErrApprovalDependent
	}
	if review.State != gateauthorization.ApprovalStateApproved ||
		review.Comment != gateauthorization.ExpectedApprovalComment(id, request) {
		return gateauthorization.Artifact{}, ErrApprovalMismatch
	}
	receipt := gateauthorization.ApprovalReceipt{
		WorkflowRunID: facts.WorkflowRunID, ActorLogin: review.ActorLogin,
		ActorID: review.ActorID, State: review.State,
		ObservedAt: facts.ObservedAt.UTC(), Comment: review.Comment,
	}
	receipt.ReceiptDigest, err = gateauthorization.ReceiptDigest(id, receipt)
	if err != nil {
		return gateauthorization.Artifact{}, err
	}
	artifact := gateauthorization.Artifact{
		SchemaVersion:   gateauthorization.SchemaVersion,
		AuthorizationID: id, Request: request, Receipt: receipt,
	}
	if err := gateauthorization.Validate(artifact); err != nil {
		return gateauthorization.Artifact{}, fmt.Errorf("authorization_receipt_invalid: %w", err)
	}
	return artifact, nil
}

// Claim atomically re-audits freshness and permanently consumes one exact
// action. Live GitHub facts are checked before entering the store critical
// section and again by the caller immediately before this function.
func Claim(st *state.Store, keyPath string, authorization gateauthorization.Artifact, live LivePullRequest, grantID string, now time.Time) (state.Artifact, gateauthorization.ExecutionClaim, error) {
	if err := gateauthorization.Validate(authorization); err != nil {
		return state.Artifact{}, gateauthorization.ExecutionClaim{}, fmt.Errorf("authorization_malformed: %w", err)
	}
	if err := validateTime(authorization, now); err != nil {
		return state.Artifact{}, gateauthorization.ExecutionClaim{}, err
	}
	if err := validateLive(authorization.Request.Subject, live); err != nil {
		return state.Artifact{}, gateauthorization.ExecutionClaim{}, err
	}
	if _, err := capability.CheckBound(
		st, keyPath, grantID, authorization.Request.Subject.Repo, "merge",
		authorization.Request.Subject.HeadSHA, authorization.Request.Subject.Number,
		authorization.AuthorizationID, func() time.Time { return now },
	); err != nil {
		return state.Artifact{}, gateauthorization.ExecutionClaim{},
			fmt.Errorf("authorization_execution_grant_invalid: %w", err)
	}
	claimID, err := gateauthorization.ClaimID(authorization)
	if err != nil {
		return state.Artifact{}, gateauthorization.ExecutionClaim{}, err
	}
	claim := gateauthorization.ExecutionClaim{
		SchemaVersion: gateauthorization.SchemaVersion,
		ClaimID:       claimID, ExecutionID: gateauthorization.ExecutionID(authorization.AuthorizationID),
		AuthorizationID: authorization.AuthorizationID, Authorization: authorization,
		ExecutionGrantID: grantID,
		Subject:          authorization.Request.Subject, Gate: authorization.Request.Gate,
		MergeArgv:             append([]string(nil), authorization.Request.MergeArgv...),
		ApprovalReceiptDigest: authorization.Receipt.ReceiptDigest,
		ClaimedAt:             now.UTC(), ExpiresAt: authorization.Request.ExpiresAt,
	}
	if err := gateauthorization.ValidateClaim(claim, authorization); err != nil {
		return state.Artifact{}, gateauthorization.ExecutionClaim{}, err
	}
	artifact, err := st.AppendAfterAudit(
		state.KindExecutionClaim, authorization.Request.Gate.RunID,
		[]string{authorization.Request.Gate.ActionID, grantID}, claim,
		func(audit state.AuditResult) error {
			return checkClaimSnapshot(audit.All, authorization)
		},
	)
	if err != nil {
		return state.Artifact{}, gateauthorization.ExecutionClaim{}, err
	}
	return artifact, claim, nil
}

// VerifyDurableClaim replays a freshly fetched anchored state snapshot and
// returns the exact claim that may enter the credential boundary.
func VerifyDurableClaim(audit state.AuditResult, claimID string, live LivePullRequest, now time.Time) (state.Artifact, gateauthorization.ExecutionClaim, error) {
	if !audit.OK {
		return state.Artifact{}, gateauthorization.ExecutionClaim{},
			fmt.Errorf("authorization_state_invalid: %s", audit.Reason)
	}
	var target state.Artifact
	var claim gateauthorization.ExecutionClaim
	results := make(map[string]struct{})
	for _, artifact := range audit.All {
		if artifact.Kind == state.KindExecutionResult {
			for _, parent := range artifact.Parents {
				results[parent] = struct{}{}
			}
		}
		if artifact.Kind != state.KindExecutionClaim {
			continue
		}
		var candidate gateauthorization.ExecutionClaim
		if err := json.Unmarshal(artifact.Body, &candidate); err != nil {
			return state.Artifact{}, gateauthorization.ExecutionClaim{},
				fmt.Errorf("authorization_claim_malformed: %w", err)
		}
		if candidate.ClaimID == claimID {
			target, claim = artifact, candidate
		}
	}
	if target.ID == "" {
		return state.Artifact{}, gateauthorization.ExecutionClaim{}, ErrActionMissing
	}
	if _, consumed := results[target.ID]; consumed {
		return state.Artifact{}, gateauthorization.ExecutionClaim{}, ErrAlreadyClaimed
	}
	if err := gateauthorization.ValidateClaim(claim, claim.Authorization); err != nil {
		return state.Artifact{}, gateauthorization.ExecutionClaim{}, fmt.Errorf("authorization_claim_malformed: %w", err)
	}
	if !now.Before(claim.ExpiresAt) {
		return state.Artifact{}, gateauthorization.ExecutionClaim{}, ErrExpired
	}
	if err := validateLive(claim.Subject, live); err != nil {
		return state.Artifact{}, gateauthorization.ExecutionClaim{}, err
	}
	index, err := buildIndex(audit.All)
	if err != nil {
		return state.Artifact{}, gateauthorization.ExecutionClaim{}, err
	}
	newest := index.newestTerminal(claim.Subject.Repo, claim.Subject.Number)
	if newest.artifact.ID != claim.Gate.ActionID ||
		newest.artifact.Hash != claim.Gate.ActionHash {
		return state.Artifact{}, gateauthorization.ExecutionClaim{}, ErrSuperseded
	}
	return target, claim, nil
}

// RecordResult atomically writes the one terminal result for a claim.
func RecordResult(st *state.Store, claimArtifact state.Artifact, claim gateauthorization.ExecutionClaim, result gateauthorization.ExecutionResult) (state.Artifact, error) {
	if err := gateauthorization.ValidateResult(result, claim); err != nil {
		return state.Artifact{}, err
	}
	return st.AppendAfterAudit(
		state.KindExecutionResult, claim.Gate.RunID,
		[]string{claimArtifact.ID}, result,
		func(audit state.AuditResult) error {
			found := false
			for _, artifact := range audit.All {
				if artifact.ID == claimArtifact.ID && artifact.Kind == state.KindExecutionClaim {
					found = true
				}
				if artifact.Kind == state.KindExecutionResult && hasParent(artifact.Parents, claimArtifact.ID) {
					return ErrResultDuplicate
				}
			}
			if !found {
				return ErrActionMissing
			}
			return nil
		},
	)
}

// SubjectHasOpenClaim reports whether Gate must refuse new outcome writes for
// this PR until the executor records a terminal result.
func SubjectHasOpenClaim(audit state.AuditResult, subject contracts.Subject) (bool, error) {
	if !audit.OK {
		return false, fmt.Errorf("authorization_state_invalid: %s", audit.Reason)
	}
	claims := make(map[string]gateauthorization.ExecutionClaim)
	resolved := make(map[string]struct{})
	for _, artifact := range audit.All {
		switch artifact.Kind {
		case state.KindExecutionClaim:
			var claim gateauthorization.ExecutionClaim
			if err := json.Unmarshal(artifact.Body, &claim); err != nil {
				return false, fmt.Errorf("authorization_claim_malformed: %w", err)
			}
			claims[artifact.ID] = claim
		case state.KindExecutionResult:
			for _, parent := range artifact.Parents {
				resolved[parent] = struct{}{}
			}
		}
	}
	for id, claim := range claims {
		if claim.Subject.Repo == subject.Repo && claim.Subject.Number == subject.Number {
			if _, ok := resolved[id]; !ok {
				return true, nil
			}
		}
	}
	return false, nil
}

// EvidenceDigest hashes the exact evidence/verdict/judgment bodies consumed by
// one action, preserving their audited order but excluding random artifact IDs,
// wall-clock metadata, grants, and outcome records.
func EvidenceDigest(artifacts []state.Artifact, run string, through int) (string, error) {
	hash := sha256.New()
	count := 0
	for order, artifact := range artifacts {
		if order > through {
			break
		}
		if artifact.Run != run || !evidenceKind(artifact.Kind) {
			continue
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, artifact.Body); err != nil {
			return "", fmt.Errorf("authorization_evidence_malformed: %w", err)
		}
		fmt.Fprintf(hash, "%s\x00%d\x00", artifact.Kind, compact.Len())
		hash.Write(compact.Bytes())
		hash.Write([]byte{0})
		count++
	}
	if count == 0 {
		return "", errors.New("authorization_evidence_missing")
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

type indexedArtifact struct {
	artifact state.Artifact
	subject  contracts.Subject
	order    int
}

type artifactIndex struct {
	byID      map[string]indexedArtifact
	terminals []indexedArtifact
}

func buildIndex(artifacts []state.Artifact) (artifactIndex, error) {
	subjects := make(map[string]contracts.Subject)
	index := artifactIndex{byID: make(map[string]indexedArtifact)}
	for order, artifact := range artifacts {
		if artifact.Kind == state.KindVerdict {
			var verdict contracts.Verdict
			if err := json.Unmarshal(artifact.Body, &verdict); err != nil {
				return artifactIndex{}, fmt.Errorf("authorization_verdict_malformed: %w", err)
			}
			if verdict.Subject.Repo != "" && verdict.Subject.Number > 0 && verdict.Subject.HeadSHA != "" {
				subjects[artifact.Run] = verdict.Subject
			}
		}
		item := indexedArtifact{artifact: artifact, subject: subjects[artifact.Run], order: order}
		index.byID[artifact.ID] = item
		if artifact.Kind == state.KindAction || artifact.Kind == state.KindEscalation {
			item.subject = subjects[artifact.Run]
			index.byID[artifact.ID] = item
			index.terminals = append(index.terminals, item)
		}
	}
	return index, nil
}

func (index artifactIndex) newestTerminal(repo string, number int) indexedArtifact {
	var newest indexedArtifact
	for _, item := range index.terminals {
		if item.subject.Repo == repo && item.subject.Number == number {
			newest = item
		}
	}
	return newest
}

func checkClaimSnapshot(artifacts []state.Artifact, authorization gateauthorization.Artifact) error {
	index, err := buildIndex(artifacts)
	if err != nil {
		return err
	}
	target, ok := index.byID[authorization.Request.Gate.ActionID]
	if !ok {
		return ErrActionMissing
	}
	if target.artifact.Hash != authorization.Request.Gate.ActionHash ||
		target.artifact.Run != authorization.Request.Gate.RunID {
		return ErrActionMismatch
	}
	if err := validateAction(target.artifact, authorization.Request.Subject); err != nil {
		return err
	}
	newest := index.newestTerminal(
		authorization.Request.Subject.Repo,
		authorization.Request.Subject.Number,
	)
	if newest.artifact.ID != target.artifact.ID {
		return ErrSuperseded
	}
	for _, artifact := range artifacts {
		if artifact.Kind != state.KindExecutionClaim {
			continue
		}
		var existing gateauthorization.ExecutionClaim
		if err := json.Unmarshal(artifact.Body, &existing); err != nil {
			return fmt.Errorf("authorization_claim_malformed: %w", err)
		}
		if existing.Gate.ActionID == target.artifact.ID ||
			existing.AuthorizationID == authorization.AuthorizationID {
			return ErrAlreadyClaimed
		}
		if existing.Subject.Repo != authorization.Request.Subject.Repo ||
			existing.Subject.Number != authorization.Request.Subject.Number {
			continue
		}
		if !hasTerminalResult(artifacts, artifact.ID) {
			return ErrSubjectClaimPending
		}
	}
	return nil
}

func validateAction(artifact state.Artifact, subject gateauthorization.Subject) error {
	if artifact.Kind != state.KindAction {
		return ErrActionMismatch
	}
	var body actionBody
	if err := json.Unmarshal(artifact.Body, &body); err != nil {
		return fmt.Errorf("%w: malformed action: %v", ErrActionMismatch, err)
	}
	want := gateauthorization.ExpectedMergeArgv(subject)
	switch {
	case body.Outcome != gateauthorization.OutcomeWouldMerge:
		return ErrActionMismatch
	case !equal(body.Argv, want):
		return ErrActionMismatch
	case body.Command != join(body.Argv):
		return ErrActionMismatch
	case len(artifact.Parents) < 2:
		return ErrActionMismatch
	case body.Verdict != artifact.Parents[0] || body.Grant != artifact.Parents[1]:
		return ErrActionMismatch
	}
	return nil
}

func matchSubject(subject contracts.Subject, want gateauthorization.Subject) error {
	if subject.Repo != want.Repo || subject.Number != want.Number ||
		subject.HeadSHA != want.HeadSHA {
		return ErrActionMismatch
	}
	return nil
}

func validateTime(authorization gateauthorization.Artifact, now time.Time) error {
	now = now.UTC()
	if now.Before(authorization.Request.IssuedAt.Add(-time.Minute)) {
		return ErrNotYetValid
	}
	if !now.Before(authorization.Request.ExpiresAt) {
		return ErrExpired
	}
	return nil
}

func validateLive(subject gateauthorization.Subject, live LivePullRequest) error {
	if live.Repository != subject.Repo || live.Number != subject.Number ||
		live.State != "open" || live.HeadSHA != subject.HeadSHA ||
		live.BaseRef != subject.BaseRef || live.BaseSHA != subject.BaseSHA ||
		live.MergeBaseSHA != subject.MergeBaseSHA {
		return ErrLiveMismatch
	}
	if len(live.OpenPRsForHead) != 1 || live.OpenPRsForHead[0] != subject.Number {
		return ErrHeadAmbiguous
	}
	return nil
}

func matchingReviews(facts ApprovalFacts) []ApprovalReview {
	var reviews []ApprovalReview
	for _, review := range facts.Reviews {
		if review.WorkflowRunID != facts.WorkflowRunID ||
			!contains(review.Environments, gateauthorization.DefaultEnvironment) {
			continue
		}
		reviews = append(reviews, review)
	}
	return reviews
}

func hasTerminalResult(artifacts []state.Artifact, claimID string) bool {
	for _, artifact := range artifacts {
		if artifact.Kind == state.KindExecutionResult && hasParent(artifact.Parents, claimID) {
			return true
		}
	}
	return false
}

func evidenceKind(kind string) bool {
	switch kind {
	case state.KindEvidence, state.KindVerdict, state.KindJudgment:
		return true
	}
	return false
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasParent(parents []string, want string) bool {
	return contains(parents, want)
}

func equal(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func join(argv []string) string {
	var out bytes.Buffer
	for i, arg := range argv {
		if i > 0 {
			out.WriteByte(' ')
		}
		out.WriteString(arg)
	}
	return out.String()
}
