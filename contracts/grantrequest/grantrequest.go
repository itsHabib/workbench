// Package grantrequest defines the versioned, provider-neutral request Gate
// publishes when an operator may mint one exact T0 capability from Slack.
//
// It is vocabulary and structural law only. Slack authentication, operator
// authorization, live-head checks, minting, and state effects remain Gate
// policy.
package grantrequest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	// SchemaVersion is the one wire version accepted by this package.
	SchemaVersion = "grant-request.v1"
	// ActionMerge is the only action the T0 request may authorize.
	ActionMerge = "merge"
	// MaxTier and MaxCycles are fixed policy encoded into every request.
	MaxTier = "T0"
	// MaxCycles is the fixed review-cycle ceiling encoded into every request.
	MaxCycles = 3
	// MaxValidity is the exact lifetime of one request and resulting grant.
	MaxValidity = 10 * time.Minute

	// DecisionDenied records an operator's explicit refusal.
	DecisionDenied = "denied"
	// DecisionExpired records a request whose authority window elapsed.
	DecisionExpired = "expired"
	// DecisionStale records a request whose exact head moved before approval.
	DecisionStale = "stale_head"

	// ActionApprove and ActionDeny are the Slack button action-id vocabulary.
	ActionApprove = "gate_t0_approve"
	// ActionDeny is the Slack button action id for an explicit refusal.
	ActionDeny = "gate_t0_deny"
)

var repoRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Subject is the complete capability subject shown to the operator.
type Subject struct {
	Repo    string `json:"repo"`
	Number  int    `json:"number"`
	HeadSHA string `json:"head_sha"`
}

// Request is the canonical content whose digest becomes AuthorizationID.
type Request struct {
	Subject   Subject   `json:"subject"`
	Action    string    `json:"action"`
	MaxTier   string    `json:"max_tier"`
	MaxCycles int       `json:"max_cycles"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// RequestArtifact is the immutable pre-approval document Gate appends.
type RequestArtifact struct {
	SchemaVersion   string  `json:"schema_version"`
	AuthorizationID string  `json:"authorization_id"`
	Request         Request `json:"request"`
}

// Denial is a terminal refusal of a request. Approval is represented by the
// existing signed grant artifact parented to the request.
type Denial struct {
	SchemaVersion string          `json:"schema_version"`
	Request       RequestArtifact `json:"request"`
	Decision      string          `json:"decision"`
	Who           string          `json:"who"`
	At            time.Time       `json:"at"`
	Reason        string          `json:"reason"`
}

// New constructs the one supported T0 request and binds its semantic id.
func New(subject Subject, issuedAt time.Time) (RequestArtifact, error) {
	request := Request{
		Subject: subject, Action: ActionMerge, MaxTier: MaxTier,
		MaxCycles: MaxCycles, IssuedAt: issuedAt.UTC(),
		ExpiresAt: issuedAt.UTC().Add(MaxValidity),
	}
	id, err := AuthorizationID(request)
	if err != nil {
		return RequestArtifact{}, err
	}
	return RequestArtifact{
		SchemaVersion: SchemaVersion, AuthorizationID: id, Request: request,
	}, nil
}

// AuthorizationID returns the domain-separated digest of the canonical
// request. Its gau_ shape is accepted by Gate's existing bound grant.
func AuthorizationID(request Request) (string, error) {
	if err := ValidateRequest(request); err != nil {
		return "", err
	}
	body, err := json.Marshal(struct {
		Domain  string  `json:"domain"`
		Request Request `json:"request"`
	}{Domain: SchemaVersion, Request: request})
	if err != nil {
		return "", fmt.Errorf("grantrequest: canonicalize request: %w", err)
	}
	sum := sha256.Sum256(body)
	return "gau_" + hex.EncodeToString(sum[:]), nil
}

// Validate checks the version, fixed T0 scope, time bound, and semantic id.
func Validate(artifact RequestArtifact) error {
	if artifact.SchemaVersion != SchemaVersion {
		return fmt.Errorf("grantrequest: unsupported schema_version %q", artifact.SchemaVersion)
	}
	if err := ValidateRequest(artifact.Request); err != nil {
		return err
	}
	id, err := AuthorizationID(artifact.Request)
	if err != nil {
		return err
	}
	if artifact.AuthorizationID != id {
		return errors.New("grantrequest: authorization_id does not match canonical request")
	}
	return nil
}

// ValidateRequest admits no knobs beyond one exact merge subject.
func ValidateRequest(request Request) error {
	switch {
	case !validRepo(request.Subject.Repo):
		return fmt.Errorf("grantrequest: invalid repo %q", request.Subject.Repo)
	case request.Subject.Number < 1:
		return errors.New("grantrequest: PR number must be positive")
	case !validSHA(request.Subject.HeadSHA):
		return errors.New("grantrequest: head_sha must be 40 lowercase hexadecimal characters")
	case request.Action != ActionMerge:
		return fmt.Errorf("grantrequest: unsupported action %q", request.Action)
	case request.MaxTier != MaxTier:
		return fmt.Errorf("grantrequest: max_tier must be %s", MaxTier)
	case request.MaxCycles != MaxCycles:
		return fmt.Errorf("grantrequest: max_cycles must be %d", MaxCycles)
	case request.IssuedAt.IsZero() || request.ExpiresAt.IsZero():
		return errors.New("grantrequest: issued_at and expires_at are required")
	case request.IssuedAt.Location() != time.UTC || request.ExpiresAt.Location() != time.UTC:
		return errors.New("grantrequest: times must be UTC")
	case !request.ExpiresAt.After(request.IssuedAt):
		return errors.New("grantrequest: expires_at must be after issued_at")
	case request.ExpiresAt.Sub(request.IssuedAt) != MaxValidity:
		return fmt.Errorf("grantrequest: validity must be exactly %s", MaxValidity)
	}
	return nil
}

// ValidateDenial checks a complete, bounded terminal refusal.
func ValidateDenial(denial Denial) error {
	if denial.SchemaVersion != SchemaVersion {
		return fmt.Errorf("grantrequest: unsupported denial schema_version %q", denial.SchemaVersion)
	}
	if err := Validate(denial.Request); err != nil {
		return err
	}
	if denial.Decision != DecisionDenied && denial.Decision != DecisionExpired && denial.Decision != DecisionStale {
		return fmt.Errorf("grantrequest: unsupported denial decision %q", denial.Decision)
	}
	if strings.TrimSpace(denial.Who) == "" || len(denial.Who) > 256 || denial.At.IsZero() {
		return errors.New("grantrequest: denial who must contain 1..256 bytes and at is required")
	}
	if denial.At.Location() != time.UTC {
		return errors.New("grantrequest: denial at must be UTC")
	}
	if denial.At.Before(denial.Request.Request.IssuedAt) {
		return errors.New("grantrequest: denial predates request")
	}
	if strings.TrimSpace(denial.Reason) == "" || len(denial.Reason) > 512 {
		return errors.New("grantrequest: denial reason must contain 1..512 bytes")
	}
	return nil
}

func validRepo(value string) bool {
	if !repoRE.MatchString(value) {
		return false
	}
	owner, repo, ok := strings.Cut(value, "/")
	return ok && owner != "." && owner != ".." && repo != "." && repo != ".."
}

func validSHA(value string) bool {
	if len(value) != 40 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
