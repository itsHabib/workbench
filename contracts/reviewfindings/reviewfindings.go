// Package reviewfindings defines the ReviewFindingsV1 artifact exchanged by
// harness-native review producers and Ship's driver address boundary.
package reviewfindings

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	// SchemaVersion is the supported ReviewFindings major version.
	SchemaVersion = 1
	// DecisionAddress is the only effectful decision in version 1.
	DecisionAddress = "address"
	// SubjectPullRequest identifies the supported subject kind.
	SubjectPullRequest = "pull_request"
	// MaxArtifactBytes bounds input before JSON decoding.
	MaxArtifactBytes = 1024 * 1024
)

// Artifact is one immutable exact-head review result.
type Artifact struct {
	SchemaVersion int       `json:"schema_version"`
	ArtifactID    string    `json:"artifact_id"`
	Decision      string    `json:"decision"`
	Subject       Subject   `json:"subject"`
	Producer      Producer  `json:"producer"`
	Panel         Panel     `json:"panel"`
	Findings      []Finding `json:"findings"`
}

// Subject binds findings to one exact pull request revision.
type Subject struct {
	Type    string `json:"type"`
	Repo    string `json:"repo"`
	Number  int    `json:"number"`
	HeadSHA string `json:"head_sha"`
}

// Producer records the harness-native implementation that emitted the artifact.
type Producer struct {
	ID          string    `json:"id"`
	Harness     string    `json:"harness"`
	GeneratedAt time.Time `json:"generated_at"`
}

// Panel records requested, completed, and explicitly missing reviewers.
type Panel struct {
	Requested []string `json:"requested"`
	Completed []string `json:"completed"`
	Missing   []string `json:"missing"`
}

// Finding is one sourced item selected for an address cycle.
type Finding struct {
	ID       string   `json:"id"`
	Severity string   `json:"severity"`
	Summary  string   `json:"summary"`
	Evidence string   `json:"evidence"`
	Sources  []Source `json:"sources"`
}

// Source links a finding to one GitHub review comment.
type Source struct {
	Reviewer  string `json:"reviewer"`
	CommentID string `json:"comment_id"`
	URL       string `json:"url"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
}

// Decode parses and validates one bounded ReviewFindingsV1 document.
func Decode(data []byte) (Artifact, error) {
	if len(data) > MaxArtifactBytes {
		return Artifact{}, errors.New("findings file exceeds 1 MiB")
	}
	var artifact Artifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		return Artifact{}, fmt.Errorf("findings file is not valid JSON: %w", err)
	}
	if err := Validate(artifact); err != nil {
		return Artifact{}, err
	}
	return artifact, nil
}

// Validate enforces the shared transport and consistency laws.
func Validate(artifact Artifact) error {
	switch {
	case artifact.SchemaVersion != SchemaVersion:
		return fmt.Errorf("unsupported schema_version %d", artifact.SchemaVersion)
	case artifact.Decision != DecisionAddress:
		return fmt.Errorf("unsupported decision %q", artifact.Decision)
	case artifact.Subject.Type != SubjectPullRequest:
		return fmt.Errorf("unsupported subject type %q", artifact.Subject.Type)
	case !validRepo(artifact.Subject.Repo):
		return errors.New("subject repo must be owner/repo")
	case artifact.Subject.Number < 1:
		return errors.New("subject number must be positive")
	case !validSHA(artifact.Subject.HeadSHA):
		return errors.New("subject head_sha must be 40 hexadecimal characters")
	case artifact.Producer.GeneratedAt.IsZero():
		return errors.New("producer generated_at is required")
	case len(artifact.Findings) == 0:
		return errors.New("findings must contain at least one finding")
	case len(artifact.Findings) > 100:
		return errors.New("findings exceeds 100 entries")
	}
	if err := validateBounded("artifact id", artifact.ArtifactID, 128); err != nil {
		return err
	}
	if err := validateBounded("producer id", artifact.Producer.ID, 128); err != nil {
		return err
	}
	if err := validateBounded("producer harness", artifact.Producer.Harness, 128); err != nil {
		return err
	}
	if err := validatePanel(artifact.Panel); err != nil {
		return err
	}
	return validateFindings(artifact.Findings, artifact.Panel.Completed)
}

// CanonicalSHA256 returns Ship's semantic replay identity for an artifact.
func CanonicalSHA256(artifact Artifact) (string, error) {
	if err := Validate(artifact); err != nil {
		return "", err
	}
	projection := canonicalProjection(artifact)
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(projection); err != nil {
		return "", fmt.Errorf("marshal canonical findings: %w", err)
	}
	sum := sha256.Sum256(bytes.TrimSuffix(data.Bytes(), []byte{'\n'}))
	return hex.EncodeToString(sum[:]), nil
}

func validatePanel(panel Panel) error {
	if len(panel.Requested) > 16 || len(panel.Completed) > 16 || len(panel.Missing) > 16 {
		return errors.New("panel sets may contain at most 16 members")
	}
	if err := unique("panel.requested", panel.Requested); err != nil {
		return err
	}
	if err := unique("panel.completed", panel.Completed); err != nil {
		return err
	}
	if err := unique("panel.missing", panel.Missing); err != nil {
		return err
	}
	requested := stringSet(panel.Requested)
	completed := stringSet(panel.Completed)
	missing := stringSet(panel.Missing)
	for member := range completed {
		if _, ok := requested[member]; !ok {
			return errors.New("panel completed/missing must partition requested")
		}
		if _, ok := missing[member]; ok {
			return errors.New("panel completed/missing must partition requested")
		}
	}
	for member := range missing {
		if _, ok := requested[member]; !ok {
			return errors.New("panel completed/missing must partition requested")
		}
	}
	if len(completed)+len(missing) != len(requested) {
		return errors.New("panel completed/missing must partition requested")
	}
	return nil
}

func validateFindings(findings []Finding, completed []string) error {
	ids := make([]string, 0, len(findings))
	completedSet := stringSet(completed)
	for _, finding := range findings {
		ids = append(ids, finding.ID)
		if err := validateFinding(finding, completedSet); err != nil {
			return err
		}
	}
	return unique("finding ids", ids)
}

func validateFinding(finding Finding, completed map[string]struct{}) error {
	for name, value := range map[string]string{
		"finding id": finding.ID, "finding severity": finding.Severity,
		"finding summary": finding.Summary, "finding evidence": finding.Evidence,
	} {
		maxBytes := 128
		if name == "finding summary" {
			maxBytes = 512
		}
		if name == "finding evidence" {
			maxBytes = 32 * 1024
		}
		if err := validateBounded(name, value, maxBytes); err != nil {
			return err
		}
	}
	if len(finding.Sources) == 0 || len(finding.Sources) > 32 {
		return errors.New("finding sources must contain 1 to 32 entries")
	}
	keys := make([]string, 0, len(finding.Sources))
	for _, source := range finding.Sources {
		if err := validateSource(source, completed); err != nil {
			return err
		}
		keys = append(keys, sourceKey(source))
	}
	return uniqueRaw("finding sources", keys)
}

func validateSource(source Source, completed map[string]struct{}) error {
	if err := validateBounded("source reviewer", source.Reviewer, 128); err != nil {
		return err
	}
	if err := validateBounded("source comment_id", source.CommentID, 128); err != nil {
		return err
	}
	if err := validateBounded("source url", source.URL, 2048); err != nil {
		return err
	}
	if parsed, err := url.ParseRequestURI(source.URL); err != nil || parsed.Scheme == "" {
		return errors.New("source url must be a URL")
	}
	if source.File != "" {
		if err := validateBounded("source file", source.File, 1024); err != nil {
			return err
		}
	}
	if source.Line < 0 || source.Line > 0 && source.File == "" {
		return errors.New("source line requires source file and must be positive")
	}
	if _, ok := completed[source.Reviewer]; !ok {
		return fmt.Errorf("source reviewer %s is absent from panel.completed", source.Reviewer)
	}
	return nil
}

func validateBounded(name, value string, maxBytes int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must not be blank", name)
	}
	if len([]byte(value)) > maxBytes {
		return fmt.Errorf("%s exceeds %d bytes", name, maxBytes)
	}
	return nil
}

func unique(name string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if err := validateBounded(name+" member", value, 128); err != nil {
			return err
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("%s must be unique", name)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func uniqueRaw(name string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			return fmt.Errorf("%s must be unique", name)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validRepo(repo string) bool {
	parts := strings.Split(repo, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != "" &&
		strings.IndexFunc(repo, unicode.IsSpace) == -1
}

func validSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

type canonicalArtifact struct {
	SchemaVersion int                `json:"schema_version"`
	Decision      string             `json:"decision"`
	Subject       Subject            `json:"subject"`
	Panel         Panel              `json:"panel"`
	Findings      []canonicalFinding `json:"findings"`
}

type canonicalFinding struct {
	ID       string   `json:"id"`
	Severity string   `json:"severity"`
	Summary  string   `json:"summary"`
	Evidence string   `json:"evidence"`
	Sources  []Source `json:"sources"`
}

func canonicalProjection(artifact Artifact) canonicalArtifact {
	panel := Panel{
		Requested: append([]string(nil), artifact.Panel.Requested...),
		Completed: append([]string(nil), artifact.Panel.Completed...),
		Missing:   append([]string(nil), artifact.Panel.Missing...),
	}
	sort.Strings(panel.Requested)
	sort.Strings(panel.Completed)
	sort.Strings(panel.Missing)
	findings := make([]canonicalFinding, 0, len(artifact.Findings))
	for _, finding := range artifact.Findings {
		sources := append([]Source(nil), finding.Sources...)
		sort.Slice(sources, func(i, j int) bool { return sourceKey(sources[i]) < sourceKey(sources[j]) })
		findings = append(findings, canonicalFinding{
			ID: finding.ID, Severity: finding.Severity, Summary: finding.Summary,
			Evidence: finding.Evidence, Sources: sources,
		})
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].ID < findings[j].ID })
	artifact.Subject.Repo = strings.ToLower(artifact.Subject.Repo)
	artifact.Subject.HeadSHA = strings.ToLower(artifact.Subject.HeadSHA)
	return canonicalArtifact{
		SchemaVersion: artifact.SchemaVersion, Decision: artifact.Decision,
		Subject: artifact.Subject, Panel: panel, Findings: findings,
	}
}

func sourceKey(source Source) string {
	data, _ := json.Marshal([]any{
		source.Reviewer, source.CommentID, source.URL, source.File,
		func() any {
			if source.Line == 0 {
				return ""
			}
			return source.Line
		}(),
	})
	return string(data)
}
