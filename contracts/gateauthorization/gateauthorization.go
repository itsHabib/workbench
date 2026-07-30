// Package gateauthorization defines the versioned, provider-neutral artifacts
// exchanged by Gate's protected approval and custodied execution boundary.
//
// This package is vocabulary only. GitHub approval verification, Gate-state
// freshness, claiming, token custody, and command execution remain Gate policy.
package gateauthorization

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	// SchemaVersion is the only supported artifact major.
	SchemaVersion = 1
	// OutcomeWouldMerge is the only Gate action that may be authorized.
	OutcomeWouldMerge = "would_merge"
	// TrustGitHubEnvironment identifies a protected-environment approval.
	TrustGitHubEnvironment = "github_environment"
	// DefaultEnvironment is the static environment used by the executor.
	DefaultEnvironment = "gate-authorization"
	// ApprovalStateApproved is the only effectful approval state.
	ApprovalStateApproved = "approved"
	// ExecutionMerged records a confirmed GitHub merge.
	ExecutionMerged = "merged"
	// ExecutionFailed records a terminal failed attempt; it never enables replay.
	ExecutionFailed = "failed"
	// MaxArtifactBytes bounds any decoded contract document.
	MaxArtifactBytes = 64 * 1024
	// MaxValidity caps an approval's authority window.
	MaxValidity = 30 * time.Minute
)

// Artifact is one approved exact-action authorization. AuthorizationID is the
// semantic digest of Request; Receipt proves which protected workflow run and
// independent actor approved that exact request.
type Artifact struct {
	SchemaVersion   int             `json:"schema_version"`
	AuthorizationID string          `json:"authorization_id"`
	Request         Request         `json:"request"`
	Receipt         ApprovalReceipt `json:"receipt"`
}

// RequestArtifact is the pre-approval, versioned document shown to the
// independent environment reviewer.
type RequestArtifact struct {
	SchemaVersion   int     `json:"schema_version"`
	AuthorizationID string  `json:"authorization_id"`
	Request         Request `json:"request"`
}

// Request is the canonical content presented for independent approval.
type Request struct {
	Subject          Subject   `json:"subject"`
	Gate             Decision  `json:"gate"`
	Outcome          string    `json:"outcome"`
	MergeArgv        []string  `json:"merge_argv"`
	EvidenceDigest   string    `json:"evidence_digest"`
	JudgmentQuestion string    `json:"judgment_question"`
	IssuedAt         time.Time `json:"issued_at"`
	ExpiresAt        time.Time `json:"expires_at"`
	ReplayID         string    `json:"replay_id"`
	TrustRoot        TrustRoot `json:"trust_root"`
}

// Subject binds approval to one PR revision and the protected base evaluated
// by Gate. BaseRef is a short branch name, not a refs/heads path.
type Subject struct {
	Repo         string `json:"repo"`
	Number       int    `json:"number"`
	HeadSHA      string `json:"head_sha"`
	BaseRef      string `json:"base_ref"`
	BaseSHA      string `json:"base_sha"`
	MergeBaseSHA string `json:"merge_base_sha"`
}

// Decision identifies the audited Gate action under approval.
type Decision struct {
	RunID      string `json:"run_id"`
	ActionID   string `json:"action_id"`
	ActionHash string `json:"action_hash"`
}

// TrustRoot names the protected GitHub boundary that may approve a request.
type TrustRoot struct {
	Kind        string `json:"kind"`
	Environment string `json:"environment"`
}

// ApprovalReceipt is the immutable projection of one run-specific GitHub
// environment approval. GitHub's approval-history API does not expose a
// submission timestamp, so ObservedAt records when Gate fetched the approved
// history inside the protected job. ReceiptDigest covers all fields plus
// AuthorizationID.
type ApprovalReceipt struct {
	WorkflowRunID int64     `json:"workflow_run_id"`
	ActorLogin    string    `json:"actor_login"`
	ActorID       int64     `json:"actor_id"`
	State         string    `json:"state"`
	ObservedAt    time.Time `json:"observed_at"`
	Comment       string    `json:"comment"`
	ReceiptDigest string    `json:"receipt_digest"`
}

// ExecutionClaim is the permanent one-time consumption of an approved action.
// A terminal execution failure does not remove or renew this claim.
type ExecutionClaim struct {
	SchemaVersion         int       `json:"schema_version"`
	ClaimID               string    `json:"claim_id"`
	ExecutionID           string    `json:"execution_id"`
	AuthorizationID       string    `json:"authorization_id"`
	Authorization         Artifact  `json:"authorization"`
	ExecutionGrantID      string    `json:"execution_grant_id"`
	Subject               Subject   `json:"subject"`
	Gate                  Decision  `json:"gate"`
	MergeArgv             []string  `json:"merge_argv"`
	ApprovalReceiptDigest string    `json:"approval_receipt_digest"`
	ClaimedAt             time.Time `json:"claimed_at"`
	ExpiresAt             time.Time `json:"expires_at"`
}

// ExecutionResult records the terminal effect attempted for one claim. It
// carries no token, private-key material, or command output.
type ExecutionResult struct {
	SchemaVersion int       `json:"schema_version"`
	ExecutionID   string    `json:"execution_id"`
	ClaimID       string    `json:"claim_id"`
	Outcome       string    `json:"outcome"`
	MergeArgv     []string  `json:"merge_argv"`
	CompletedAt   time.Time `json:"completed_at"`
	MergeCommit   string    `json:"merge_commit,omitempty"`
	ErrorCode     string    `json:"error_code,omitempty"`
}

// Decode parses and validates one bounded GateAuthorizationV1 document.
func Decode(data []byte) (Artifact, error) {
	if len(data) > MaxArtifactBytes {
		return Artifact{}, fmt.Errorf("authorization exceeds %d bytes", MaxArtifactBytes)
	}
	var artifact Artifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		return Artifact{}, fmt.Errorf("authorization is not valid JSON: %w", err)
	}
	if err := Validate(artifact); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

// DecodeRequest parses and validates one bounded pre-approval request.
func DecodeRequest(data []byte) (RequestArtifact, error) {
	if len(data) > MaxArtifactBytes {
		return RequestArtifact{}, fmt.Errorf("authorization request exceeds %d bytes", MaxArtifactBytes)
	}
	var artifact RequestArtifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		return RequestArtifact{}, fmt.Errorf("authorization request is not valid JSON: %w", err)
	}
	if err := ValidateRequestArtifact(artifact); err != nil {
		return RequestArtifact{}, err
	}
	return artifact, nil
}

// ValidateRequestArtifact validates the version and semantic request identity.
func ValidateRequestArtifact(artifact RequestArtifact) error {
	if artifact.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", artifact.SchemaVersion)
	}
	id, err := AuthorizationID(artifact.Request)
	if err != nil {
		return err
	}
	if artifact.AuthorizationID != id {
		return errors.New("authorization_id does not match canonical request")
	}
	return nil
}

// Validate enforces portable shape and semantic identities.
func Validate(artifact Artifact) error {
	if artifact.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", artifact.SchemaVersion)
	}
	if err := ValidateRequest(artifact.Request); err != nil {
		return err
	}
	id, err := AuthorizationID(artifact.Request)
	if err != nil {
		return err
	}
	if artifact.AuthorizationID != id {
		return errors.New("authorization_id does not match canonical request")
	}
	return ValidateReceipt(artifact.AuthorizationID, artifact.Request, artifact.Receipt)
}

// ValidateRequest validates exact action, subject, evidence, and time bounds.
func ValidateRequest(request Request) error {
	switch {
	case !validSubject(request.Subject):
		return errors.New("authorization subject is invalid")
	case !validID(request.Gate.RunID, "run_", 16):
		return errors.New("gate run_id must be run_<16 lowercase hexadecimal characters>")
	case !validID(request.Gate.ActionID, "act_", 16):
		return errors.New("gate action_id must be act_<16 lowercase hexadecimal characters>")
	case !validLowerHex(request.Gate.ActionHash, 64):
		return errors.New("gate action_hash must be 64 lowercase hexadecimal characters")
	case request.Outcome != OutcomeWouldMerge:
		return fmt.Errorf("unsupported outcome %q", request.Outcome)
	case !validDigest(request.EvidenceDigest):
		return errors.New("evidence_digest must be sha256:<64 lowercase hexadecimal characters>")
	case strings.TrimSpace(request.JudgmentQuestion) == "":
		return errors.New("judgment_question is required")
	case len(request.JudgmentQuestion) > 4096:
		return errors.New("judgment_question exceeds 4096 bytes")
	case request.IssuedAt.IsZero() || request.ExpiresAt.IsZero():
		return errors.New("issued_at and expires_at are required")
	case !request.ExpiresAt.After(request.IssuedAt):
		return errors.New("expires_at must be after issued_at")
	case request.ExpiresAt.Sub(request.IssuedAt) > MaxValidity:
		return fmt.Errorf("authorization validity exceeds %s", MaxValidity)
	case !validID(request.ReplayID, "evt_", 32):
		return errors.New("replay_id must be evt_<32 lowercase hexadecimal characters>")
	case request.TrustRoot.Kind != TrustGitHubEnvironment:
		return fmt.Errorf("unsupported trust root %q", request.TrustRoot.Kind)
	case request.TrustRoot.Environment != DefaultEnvironment:
		return fmt.Errorf("unsupported trust environment %q", request.TrustRoot.Environment)
	}
	return validateMergeArgv(request.Subject, request.MergeArgv)
}

// ValidateReceipt verifies the portable receipt and its semantic digest.
func ValidateReceipt(authorizationID string, request Request, receipt ApprovalReceipt) error {
	switch {
	case receipt.WorkflowRunID < 1:
		return errors.New("approval workflow_run_id must be positive")
	case !validLogin(receipt.ActorLogin):
		return errors.New("approval actor_login is invalid")
	case receipt.ActorID < 1:
		return errors.New("approval actor_id must be positive")
	case receipt.State != ApprovalStateApproved:
		return fmt.Errorf("unsupported approval state %q", receipt.State)
	case receipt.ObservedAt.IsZero():
		return errors.New("approval observed_at is required")
	case receipt.ObservedAt.Before(request.IssuedAt):
		return errors.New("approval observation predates authorization request")
	case receipt.ObservedAt.After(request.ExpiresAt):
		return errors.New("approval observation is outside authorization validity")
	case receipt.Comment != ExpectedApprovalComment(authorizationID, request):
		return errors.New("approval comment does not bind the exact authorization")
	}
	digest, err := ReceiptDigest(authorizationID, receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != digest {
		return errors.New("approval receipt_digest does not match canonical receipt")
	}
	return nil
}

// ValidateClaim checks that a claim is an exact projection of its authorization.
func ValidateClaim(claim ExecutionClaim, authorization Artifact) error {
	if err := Validate(authorization); err != nil {
		return err
	}
	switch {
	case claim.SchemaVersion != SchemaVersion:
		return fmt.Errorf("unsupported claim schema_version %d", claim.SchemaVersion)
	case claim.ExecutionID != ExecutionID(authorization.AuthorizationID):
		return errors.New("execution_id does not match authorization")
	case claim.AuthorizationID != authorization.AuthorizationID:
		return errors.New("claim authorization_id mismatch")
	case claim.Authorization.AuthorizationID != authorization.AuthorizationID:
		return errors.New("claim embedded authorization mismatch")
	case claim.Authorization.Receipt.ReceiptDigest != claim.ApprovalReceiptDigest:
		return errors.New("claim embedded approval receipt mismatch")
	case !validID(claim.ExecutionGrantID, "grt_", 16):
		return errors.New("claim execution_grant_id is invalid")
	case claim.Subject != authorization.Request.Subject:
		return errors.New("claim subject mismatch")
	case claim.Gate != authorization.Request.Gate:
		return errors.New("claim gate decision mismatch")
	case !equal(claim.MergeArgv, authorization.Request.MergeArgv):
		return errors.New("claim merge_argv mismatch")
	case claim.ApprovalReceiptDigest != authorization.Receipt.ReceiptDigest:
		return errors.New("claim approval_receipt_digest mismatch")
	case claim.ClaimedAt.Before(authorization.Receipt.ObservedAt):
		return errors.New("claim predates approval observation")
	case claim.ExpiresAt != authorization.Request.ExpiresAt:
		return errors.New("claim expiry mismatch")
	}
	if err := Validate(claim.Authorization); err != nil {
		return fmt.Errorf("claim embedded authorization: %w", err)
	}
	id, err := ClaimID(authorization)
	if err != nil {
		return err
	}
	if claim.ClaimID != id {
		return errors.New("claim_id does not match authorization")
	}
	return validateMergeArgv(claim.Subject, claim.MergeArgv)
}

// ValidateResult checks a terminal, secret-free execution record.
func ValidateResult(result ExecutionResult, claim ExecutionClaim) error {
	switch {
	case result.SchemaVersion != SchemaVersion:
		return fmt.Errorf("unsupported result schema_version %d", result.SchemaVersion)
	case result.ExecutionID != claim.ExecutionID:
		return errors.New("result execution_id mismatch")
	case result.ClaimID != claim.ClaimID:
		return errors.New("result claim_id mismatch")
	case result.Outcome != ExecutionMerged && result.Outcome != ExecutionFailed:
		return fmt.Errorf("unsupported execution outcome %q", result.Outcome)
	case !equal(result.MergeArgv, claim.MergeArgv):
		return errors.New("result merge_argv mismatch")
	case result.CompletedAt.Before(claim.ClaimedAt):
		return errors.New("execution completed before claim")
	case result.Outcome == ExecutionMerged && !validLowerHex(result.MergeCommit, 40):
		return errors.New("merged execution requires a full merge_commit")
	case result.Outcome == ExecutionMerged && result.ErrorCode != "":
		return errors.New("merged execution cannot carry error_code")
	case result.Outcome == ExecutionFailed && strings.TrimSpace(result.ErrorCode) == "":
		return errors.New("failed execution requires error_code")
	case result.Outcome == ExecutionFailed && result.MergeCommit != "":
		return errors.New("failed execution cannot carry merge_commit")
	}
	return nil
}

// AuthorizationID returns the natural replay identity of a request.
func AuthorizationID(request Request) (string, error) {
	if err := ValidateRequest(request); err != nil {
		return "", err
	}
	data, err := canonicalJSON(request)
	if err != nil {
		return "", err
	}
	return "gau_" + sum(data), nil
}

// ReceiptDigest covers a receipt without recursively covering its digest field.
func ReceiptDigest(authorizationID string, receipt ApprovalReceipt) (string, error) {
	projection := struct {
		AuthorizationID string    `json:"authorization_id"`
		WorkflowRunID   int64     `json:"workflow_run_id"`
		ActorLogin      string    `json:"actor_login"`
		ActorID         int64     `json:"actor_id"`
		State           string    `json:"state"`
		ObservedAt      time.Time `json:"observed_at"`
		Comment         string    `json:"comment"`
	}{
		AuthorizationID: authorizationID, WorkflowRunID: receipt.WorkflowRunID,
		ActorLogin: receipt.ActorLogin, ActorID: receipt.ActorID,
		State: receipt.State, ObservedAt: receipt.ObservedAt.UTC(),
		Comment: receipt.Comment,
	}
	data, err := canonicalJSON(projection)
	if err != nil {
		return "", err
	}
	return "sha256:" + sum(data), nil
}

// ClaimID is stable for an authorization; a retry cannot mint a new claim ID.
func ClaimID(authorization Artifact) (string, error) {
	if err := Validate(authorization); err != nil {
		return "", err
	}
	data := authorization.AuthorizationID + "|" + authorization.Request.Gate.ActionHash +
		"|" + authorization.Receipt.ReceiptDigest
	return "gxc_" + sum([]byte(data)), nil
}

// ExecutionID is stable for an authorization and names the one permitted try.
func ExecutionID(authorizationID string) string {
	return "gxe_" + sum([]byte(authorizationID))
}

// ExpectedApprovalComment is the exact protected-environment approval comment.
// It binds the complete request through AuthorizationID and repeats the two
// dense evidence fields for operator inspection.
func ExpectedApprovalComment(authorizationID string, request Request) string {
	question := sha256.Sum256([]byte(request.JudgmentQuestion))
	return fmt.Sprintf("gate approve %s evidence=%s question=sha256:%s",
		authorizationID, request.EvidenceDigest, hex.EncodeToString(question[:]))
}

// ExpectedMergeArgv returns the only canonical merge intent.
func ExpectedMergeArgv(subject Subject) []string {
	return []string{
		"gh", "pr", "merge", strconv.Itoa(subject.Number),
		"-R", subject.Repo, "--squash", "--match-head-commit",
		subject.HeadSHA,
	}
}

func validateMergeArgv(subject Subject, argv []string) error {
	want := ExpectedMergeArgv(subject)
	if !equal(argv, want) {
		return errors.New("merge_argv is not Gate's exact commit-pinned squash intent")
	}
	for _, arg := range argv {
		if arg == "--admin" {
			return errors.New("merge_argv must never contain --admin")
		}
	}
	return nil
}

func validSubject(subject Subject) bool {
	return validRepo(subject.Repo) && subject.Number > 0 &&
		validLowerHex(subject.HeadSHA, 40) && validBaseRef(subject.BaseRef) &&
		validLowerHex(subject.BaseSHA, 40) && validLowerHex(subject.MergeBaseSHA, 40)
}

func validRepo(repo string) bool {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	if repo != strings.TrimSpace(repo) || strings.IndexFunc(repo, unicode.IsSpace) >= 0 {
		return false
	}
	for _, part := range parts {
		if part == "." || part == ".." || strings.HasPrefix(part, "-") || strings.HasSuffix(part, ".git") {
			return false
		}
		for _, r := range part {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._-", r) {
				continue
			}
			return false
		}
	}
	return true
}

func validBaseRef(ref string) bool {
	return ref != "" && ref == strings.TrimSpace(ref) &&
		!strings.HasPrefix(ref, "refs/") && !strings.Contains(ref, "..") &&
		!strings.ContainsAny(ref, "~^:?*[\\") && !strings.HasSuffix(ref, "/") &&
		!strings.HasSuffix(ref, ".lock") && !strings.Contains(ref, "//")
}

func validLogin(login string) bool {
	return login != "" && len(login) <= 39 && login == strings.TrimSpace(login) &&
		strings.IndexFunc(login, unicode.IsSpace) < 0
}

func validDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") && validLowerHex(strings.TrimPrefix(value, "sha256:"), 64)
}

func validID(value, prefix string, size int) bool {
	return strings.HasPrefix(value, prefix) && validLowerHex(strings.TrimPrefix(value, prefix), size)
}

func validLowerHex(value string, size int) bool {
	if len(value) != size || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func canonicalJSON(value any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("canonical JSON: %w", err)
	}
	return bytes.TrimSuffix(out.Bytes(), []byte{'\n'}), nil
}

func sum(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
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
