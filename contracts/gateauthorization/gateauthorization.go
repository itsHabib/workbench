// Package gateauthorization defines the exact-head authorization artifact
// promoted through Gate's trusted GitHub Actions boundary.
package gateauthorization

import (
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
	// SchemaVersion is the only supported GateAuthorization major version.
	SchemaVersion = 1
	// OutcomeWouldMerge is the only promotable Gate outcome.
	OutcomeWouldMerge = "would_merge"
	// TrustGitHubEnvironment identifies protected-environment approval.
	TrustGitHubEnvironment = "github_environment"
	// DefaultEnvironment is the protected environment the trusted workflow uses.
	DefaultEnvironment = "gate-authorization"
	// MaxArtifactBytes bounds input before JSON decoding.
	MaxArtifactBytes = 32 * 1024
	// MaxValidity caps the authority window encoded by any artifact.
	MaxValidity = time.Hour
)

// Artifact is one immutable authorization for one Gate action and PR head.
// AuthorizationID is the action hash prefixed with "gau_"; this makes repeated
// export of the same decision naturally idempotent.
type Artifact struct {
	SchemaVersion   int       `json:"schema_version"`
	AuthorizationID string    `json:"authorization_id"`
	Subject         Subject   `json:"subject"`
	Gate            Decision  `json:"gate"`
	Outcome         string    `json:"outcome"`
	MergeArgv       []string  `json:"merge_argv"`
	IssuedAt        time.Time `json:"issued_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	TrustRoot       TrustRoot `json:"trust_root"`
}

// Subject binds an authorization to one exact pull request head.
type Subject struct {
	Repo    string `json:"repo"`
	Number  int    `json:"number"`
	HeadSHA string `json:"head_sha"`
}

// Decision identifies the Gate state artifact that authorized the merge.
type Decision struct {
	RunID      string `json:"run_id"`
	ActionID   string `json:"action_id"`
	ActionHash string `json:"action_hash"`
}

// TrustRoot names the external authority that approves promotion.
type TrustRoot struct {
	Kind        string `json:"kind"`
	Environment string `json:"environment"`
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

// Validate enforces the portable shape. Freshness and GitHub state are consumer
// policy and intentionally stay outside this leaf contract package.
func Validate(artifact Artifact) error {
	switch {
	case artifact.SchemaVersion != SchemaVersion:
		return fmt.Errorf("unsupported schema_version %d", artifact.SchemaVersion)
	case !validRepo(artifact.Subject.Repo):
		return errors.New("subject repo must be canonical owner/repo")
	case artifact.Subject.Number < 1:
		return errors.New("subject number must be positive")
	case !validLowerHex(artifact.Subject.HeadSHA, 40):
		return errors.New("subject head_sha must be 40 lowercase hexadecimal characters")
	case !validID(artifact.Gate.RunID, "run_", 16):
		return errors.New("gate run_id must be run_<16 lowercase hexadecimal characters>")
	case !validID(artifact.Gate.ActionID, "act_", 16):
		return errors.New("gate action_id must be act_<16 lowercase hexadecimal characters>")
	case !validLowerHex(artifact.Gate.ActionHash, 64):
		return errors.New("gate action_hash must be 64 lowercase hexadecimal characters")
	case artifact.AuthorizationID != "gau_"+artifact.Gate.ActionHash:
		return errors.New("authorization_id must equal gau_<action_hash>")
	case artifact.Outcome != OutcomeWouldMerge:
		return fmt.Errorf("unsupported outcome %q", artifact.Outcome)
	case artifact.IssuedAt.IsZero() || artifact.ExpiresAt.IsZero():
		return errors.New("issued_at and expires_at are required")
	case !artifact.ExpiresAt.After(artifact.IssuedAt):
		return errors.New("expires_at must be after issued_at")
	case artifact.ExpiresAt.Sub(artifact.IssuedAt) > MaxValidity:
		return fmt.Errorf("authorization validity exceeds %s", MaxValidity)
	case artifact.TrustRoot.Kind != TrustGitHubEnvironment:
		return fmt.Errorf("unsupported trust root %q", artifact.TrustRoot.Kind)
	case artifact.TrustRoot.Environment != DefaultEnvironment:
		return fmt.Errorf("unsupported trust environment %q", artifact.TrustRoot.Environment)
	}
	return validateMergeArgv(artifact)
}

func validateMergeArgv(artifact Artifact) error {
	want := ExpectedMergeArgv(artifact.Subject)
	if len(artifact.MergeArgv) != len(want) {
		return errors.New("merge_argv is not Gate's exact commit-pinned squash command")
	}
	for i := range want {
		if artifact.MergeArgv[i] != want[i] {
			return errors.New("merge_argv is not Gate's exact commit-pinned squash command")
		}
	}
	return nil
}

// ExpectedMergeArgv returns the one command shape Gate v1 authorizes. Consumers
// compare against it but never execute it.
func ExpectedMergeArgv(subject Subject) []string {
	return []string{
		"gh", "pr", "merge", strconv.Itoa(subject.Number),
		"-R", subject.Repo, "--squash", "--delete-branch",
		"--match-head-commit", subject.HeadSHA,
	}
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
