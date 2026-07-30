package gateauthorization

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// PreparationRequestArtifact is the exact-head request shown to the protected
// environment reviewer before Gate may append and publish a hosted decision.
type PreparationRequestArtifact struct {
	SchemaVersion int                `json:"schema_version"`
	PreparationID string             `json:"preparation_id"`
	Request       PreparationRequest `json:"request"`
}

// PreparationRequest authorizes evaluation and publication, never merge.
type PreparationRequest struct {
	Subject   Subject   `json:"subject"`
	GrantID   string    `json:"grant_id"`
	Decision  string    `json:"decision"`
	Why       string    `json:"why"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
	ReplayID  string    `json:"replay_id"`
	TrustRoot TrustRoot `json:"trust_root"`
}

// PreparationApproval is the immutable receipt for one protected preparation.
type PreparationApproval struct {
	SchemaVersion int                `json:"schema_version"`
	PreparationID string             `json:"preparation_id"`
	Request       PreparationRequest `json:"request"`
	Receipt       ApprovalReceipt    `json:"receipt"`
}

// PreparationID is stable for one exact preparation request.
func PreparationID(request PreparationRequest) (string, error) {
	if err := ValidatePreparationRequest(request); err != nil {
		return "", err
	}
	data, err := canonicalJSON(request)
	if err != nil {
		return "", err
	}
	return "gpr_" + sum(data), nil
}

// ValidatePreparationRequest rejects requests that could drift from one exact
// PR head or exceed the existing protected-approval validity window.
func ValidatePreparationRequest(request PreparationRequest) error {
	switch {
	case !validSubject(request.Subject):
		return errors.New("preparation subject is invalid")
	case !validID(request.GrantID, "grt_", 16):
		return errors.New("preparation grant_id is invalid")
	case request.Decision != "pass" && request.Decision != "block":
		return fmt.Errorf("unsupported preparation decision %q", request.Decision)
	case strings.TrimSpace(request.Why) == "":
		return errors.New("preparation why is required")
	case len(request.Why) > 4096:
		return errors.New("preparation why exceeds 4096 bytes")
	case request.IssuedAt.IsZero() || request.ExpiresAt.IsZero():
		return errors.New("preparation issued_at and expires_at are required")
	case !request.ExpiresAt.After(request.IssuedAt):
		return errors.New("preparation expires_at must be after issued_at")
	case request.ExpiresAt.Sub(request.IssuedAt) > MaxValidity:
		return fmt.Errorf("preparation validity exceeds %s", MaxValidity)
	case !validID(request.ReplayID, "evt_", 32):
		return errors.New("preparation replay_id must be evt_<32 lowercase hexadecimal characters>")
	case request.TrustRoot.Kind != TrustGitHubEnvironment:
		return fmt.Errorf("unsupported preparation trust root %q", request.TrustRoot.Kind)
	case request.TrustRoot.Environment != DefaultEnvironment:
		return fmt.Errorf("unsupported preparation trust environment %q", request.TrustRoot.Environment)
	}
	return nil
}

// ValidatePreparationRequestArtifact checks the canonical request identity.
func ValidatePreparationRequestArtifact(artifact PreparationRequestArtifact) error {
	if artifact.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported preparation schema_version %d", artifact.SchemaVersion)
	}
	id, err := PreparationID(artifact.Request)
	if err != nil {
		return err
	}
	if artifact.PreparationID != id {
		return errors.New("preparation_id does not match canonical request")
	}
	return nil
}

// ValidatePreparationApproval checks the exact protected approval receipt.
func ValidatePreparationApproval(approval PreparationApproval) error {
	if approval.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported preparation approval schema_version %d", approval.SchemaVersion)
	}
	id, err := PreparationID(approval.Request)
	if err != nil {
		return err
	}
	if approval.PreparationID != id {
		return errors.New("preparation approval id mismatch")
	}
	receipt := approval.Receipt
	switch {
	case receipt.WorkflowRunID < 1 || receipt.ActorID < 1 || !validLogin(receipt.ActorLogin):
		return errors.New("preparation approval actor is invalid")
	case receipt.State != ApprovalStateApproved:
		return fmt.Errorf("unsupported preparation approval state %q", receipt.State)
	case receipt.ObservedAt.Before(approval.Request.IssuedAt) ||
		receipt.ObservedAt.After(approval.Request.ExpiresAt):
		return errors.New("preparation approval observation is outside request validity")
	case receipt.Comment != ExpectedPreparationApprovalComment(id, approval.Request):
		return errors.New("preparation approval comment does not bind the exact request")
	}
	digest, err := ReceiptDigest(id, receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != digest {
		return errors.New("preparation approval receipt_digest mismatch")
	}
	return nil
}

// ExpectedPreparationApprovalComment is the exact reviewer acknowledgement.
func ExpectedPreparationApprovalComment(id string, request PreparationRequest) string {
	return fmt.Sprintf(
		"approve gate preparation %s for %s#%d@%s",
		id, request.Subject.Repo, request.Subject.Number, request.Subject.HeadSHA,
	)
}

// DecodePreparationRequest bounds and validates one preparation document.
func DecodePreparationRequest(data []byte) (PreparationRequestArtifact, error) {
	if len(data) > MaxArtifactBytes {
		return PreparationRequestArtifact{}, errors.New("preparation request exceeds maximum size")
	}
	var artifact PreparationRequestArtifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		return PreparationRequestArtifact{}, fmt.Errorf("decode preparation request: %w", err)
	}
	if err := ValidatePreparationRequestArtifact(artifact); err != nil {
		return PreparationRequestArtifact{}, err
	}
	return artifact, nil
}
