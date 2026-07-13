// Package model defines the source-neutral Control Room read contract.
package model

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
)

// SourceState describes the freshness and availability of one source adapter.
type SourceState string

const (
	// SourceLoading means the source has not produced its first receipt yet.
	SourceLoading SourceState = "loading"
	// SourceOK means the source supplied a complete current observation.
	SourceOK SourceState = "ok"
	// SourceDegraded means the source supplied a usable but qualified observation.
	SourceDegraded SourceState = "degraded"
	// SourceUnavailable means the source could not be queried.
	SourceUnavailable SourceState = "unavailable"
	// SourceStale means retained data is no longer current.
	SourceStale SourceState = "stale"
)

// AvailabilityState distinguishes known, unknown, and unavailable values.
type AvailabilityState string

const (
	// Available means a producer supplied a value.
	Available AvailabilityState = "available"
	// Unknown means the producer supplied no authoritative value.
	Unknown AvailabilityState = "unknown"
	// Unavailable means the producer cannot supply this field.
	Unavailable AvailabilityState = "unavailable"
)

// Availability carries a value without conflating absence with a useful zero value.
type Availability[T any] struct {
	State AvailabilityState `json:"state"`
	Value *T                `json:"value,omitempty"`
}

// Validate reports invalid state/value combinations.
func (a Availability[T]) Validate() error {
	state := a.State
	if state == "" {
		state = Unknown
	}
	switch state {
	case Available:
		if a.Value == nil {
			return fmt.Errorf("available value is missing")
		}
	case Unknown, Unavailable:
		if a.Value != nil {
			return fmt.Errorf("%s value must be absent", state)
		}
	default:
		return fmt.Errorf("unknown availability state %q", state)
	}
	return nil
}

// Known constructs an available value.
func Known[T any](value T) Availability[T] { return Availability[T]{State: Available, Value: &value} }

// Missing constructs an unknown value.
func Missing[T any]() Availability[T] { return Availability[T]{State: Unknown} }

// NotAvailable constructs a value that its source cannot provide.
func NotAvailable[T any]() Availability[T] { return Availability[T]{State: Unavailable} }

// MarshalJSON normalizes the Go zero value to the explicit unknown state.
func (a Availability[T]) MarshalJSON() ([]byte, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	state := a.State
	if state == "" {
		state = Unknown
	}
	return json.Marshal(struct {
		State AvailabilityState `json:"state"`
		Value *T                `json:"value,omitempty"`
	}{State: state, Value: a.Value})
}

// UnmarshalJSON preserves forward-compatible fields and normalizes an absent state.
func (a *Availability[T]) UnmarshalJSON(data []byte) error {
	var wire struct {
		State AvailabilityState `json:"state"`
		Value *T                `json:"value"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.State == "" {
		wire.State = Unknown
	}
	if err := (Availability[T]{State: wire.State, Value: wire.Value}).Validate(); err != nil {
		return err
	}
	a.State, a.Value = wire.State, wire.Value
	return nil
}

// Liveness is the deterministic activity classification assigned by policy.
type Liveness string

const (
	// LivenessUnknown is the fail-closed classification when evidence is insufficient.
	LivenessUnknown Liveness = "unknown"
	// LivenessDone identifies completed tasks.
	LivenessDone Liveness = "done"
	// LivenessRetryLoop identifies repeated equivalent run failures.
	LivenessRetryLoop Liveness = "on_fire/retry_loop"
	// LivenessStalledActive identifies an active run with no recent update.
	LivenessStalledActive Liveness = "on_fire/stalled_active"
	// LivenessLive identifies current work or current linked evidence.
	LivenessLive Liveness = "live"
	// LivenessIdle identifies work outside the live window but not yet stale.
	LivenessIdle Liveness = "idle"
	// LivenessStaleClaim identifies an old claim with no exact current work.
	LivenessStaleClaim Liveness = "stale_claim"
	// LivenessBlockedNoPath identifies blocked work without a resolvable path.
	LivenessBlockedNoPath Liveness = "blocked_no_path"
)

// Snapshot is the immutable, source-neutral read model consumed by the UI.
type Snapshot struct {
	Version      uint64          `json:"version"`
	Mode         string          `json:"mode"`
	GeneratedAt  time.Time       `json:"generated_at"`
	Sources      []SourceReceipt `json:"sources"`
	Runs         []Run           `json:"runs"`
	Tasks        []Task          `json:"tasks"`
	PullRequests []PullRequest   `json:"pull_requests"`
	Reliability  []Diagnosis     `json:"reliability"`
	ToolHealth   []ToolHealth    `json:"tool_health"`
	Attention    []AttentionItem `json:"attention"`
	Repositories []string        `json:"repositories"`
}

// SourceReceipt records one adapter observation used to qualify derived policy.
type SourceReceipt struct {
	Source     string      `json:"source"`
	State      SourceState `json:"state"`
	ObservedAt time.Time   `json:"observed_at"`
	DurationMS int64       `json:"duration_ms"`
	ErrorCode  string      `json:"error_code,omitempty"`
	Message    string      `json:"message,omitempty"`
}

// Validate reports malformed source receipt identity or state.
func (r SourceReceipt) Validate() error {
	if strings.TrimSpace(r.Source) == "" {
		return fmt.Errorf("source is required")
	}
	switch r.State {
	case SourceLoading, SourceOK, SourceDegraded, SourceUnavailable, SourceStale:
		return nil
	default:
		return fmt.Errorf("unknown source state %q", r.State)
	}
}

// SafeLink is a display label paired with an HTTPS URL or repository-relative path.
type SafeLink struct {
	Label string `json:"label"`
	URL   string `json:"url,omitempty"`
	Path  string `json:"path,omitempty"`
}

// Validate reports links that are not HTTPS URLs or clean repository-relative paths.
func (l SafeLink) Validate() error {
	if strings.TrimSpace(l.Label) == "" {
		return fmt.Errorf("link label is required")
	}
	if (l.URL == "") == (l.Path == "") {
		return fmt.Errorf("link requires exactly one URL or path")
	}
	if l.URL != "" {
		parsed, err := url.Parse(l.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return fmt.Errorf("link URL must be an HTTPS URL without credentials")
		}
		return nil
	}
	clean := path.Clean(l.Path)
	if strings.Contains(l.Path, "\\") || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || clean != l.Path {
		return fmt.Errorf("link path must be a clean repository-relative path")
	}
	return nil
}

// RuntimeDetails records requested or actual execution coordinates.
type RuntimeDetails struct {
	Runtime  Availability[string] `json:"runtime"`
	Provider Availability[string] `json:"provider"`
	Model    Availability[string] `json:"model"`
}

// Run represents a Ship workflow or driver run.
type Run struct {
	ID         string                  `json:"id"`
	Kind       string                  `json:"kind"`
	Repository string                  `json:"repository,omitempty"`
	Project    string                  `json:"project,omitempty"`
	Task       string                  `json:"task,omitempty"`
	Spec       string                  `json:"spec,omitempty"`
	DocPath    Availability[string]    `json:"doc_path"`
	SpecPath   Availability[string]    `json:"spec_path"`
	Branch     string                  `json:"branch,omitempty"`
	Status     string                  `json:"status"`
	Phase      string                  `json:"phase,omitempty"`
	Requested  RuntimeDetails          `json:"requested"`
	Actual     RuntimeDetails          `json:"actual"`
	CreatedAt  time.Time               `json:"created_at"`
	UpdatedAt  time.Time               `json:"updated_at"`
	StartedAt  Availability[time.Time] `json:"started_at"`
	EndedAt    Availability[time.Time] `json:"ended_at"`
	DurationMS Availability[int64]     `json:"duration_ms"`
	Failure    string                  `json:"failure,omitempty"`
	Evidence   []SafeLink              `json:"evidence"`
	Liveness   Liveness                `json:"liveness"`
}

// Task represents one Dossier task and its dependency evidence.
type Task struct {
	ID           string     `json:"id"`
	Slug         string     `json:"slug"`
	Title        string     `json:"title"`
	Project      string     `json:"project"`
	Phase        string     `json:"phase,omitempty"`
	Status       string     `json:"status"`
	Assignee     string     `json:"assignee,omitempty"`
	Dependencies []string   `json:"dependencies"`
	Blockers     []string   `json:"blockers"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	Artifacts    []SafeLink `json:"artifacts"`
	Liveness     Liveness   `json:"liveness"`
}

// Check represents a visible GitHub check rollup entry.
type Check struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion,omitempty"`
}

// PullRequest contains the bounded GitHub facts required by policy and presentation.
type PullRequest struct {
	ID                   string    `json:"id"`
	Repository           string    `json:"repository"`
	Number               int       `json:"number"`
	Title                string    `json:"title"`
	URL                  string    `json:"url"`
	Author               string    `json:"author"`
	Head                 string    `json:"head"`
	Base                 string    `json:"base"`
	Draft                bool      `json:"draft"`
	State                string    `json:"state"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
	Checks               []Check   `json:"checks"`
	ReviewDecision       string    `json:"review_decision"`
	RequestedReviewers   int       `json:"requested_reviewers"`
	UnresolvedThreads    int       `json:"unresolved_threads"`
	Mergeable            string    `json:"mergeable"`
	MergeStateStatus     string    `json:"merge_state_status"`
	DetailState          string    `json:"detail_state"`
	TruncatedConnections []string  `json:"truncated_connections"`
	NextCondition        string    `json:"next_condition,omitempty"`
}

// Finding is one normalized TraceLens diagnosis finding.
type Finding struct {
	Title      string  `json:"title"`
	Severity   string  `json:"severity,omitempty"`
	Locus      string  `json:"locus,omitempty"`
	Evidence   string  `json:"evidence,omitempty"`
	Repair     string  `json:"repair,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

// Diagnosis records a TraceLens report and its provenance.
type Diagnosis struct {
	RunID        string                 `json:"run_id"`
	Verdict      string                 `json:"verdict"`
	Tier         string                 `json:"tier"`
	Dialect      string                 `json:"dialect"`
	Findings     []Finding              `json:"findings"`
	Report       Availability[SafeLink] `json:"report"`
	Evidence     []SafeLink             `json:"evidence"`
	InputTokens  Availability[int64]    `json:"input_tokens"`
	OutputTokens Availability[int64]    `json:"output_tokens"`
	CostUSD      Availability[float64]  `json:"cost_usd"`
	LatencyMS    Availability[int64]    `json:"latency_ms"`
}

// ToolHealth summarizes accumulated operational friction for one tool.
type ToolHealth struct {
	Tool           string    `json:"tool"`
	WorstSeverity  string    `json:"worst_severity"`
	SessionCount   int       `json:"session_count"`
	LastOccurrence time.Time `json:"last_occurrence"`
	Pain           []string  `json:"pain"`
	Kind           string    `json:"kind"`
	Stale          bool      `json:"stale"`
}

// AttentionItem is a deterministic, evidence-backed next-action candidate.
type AttentionItem struct {
	ID                string     `json:"id"`
	Category          string     `json:"category"`
	Score             int        `json:"score"`
	RuleID            string     `json:"rule_id"`
	Title             string     `json:"title"`
	Reason            string     `json:"reason"`
	Repository        string     `json:"repository,omitempty"`
	Project           string     `json:"project,omitempty"`
	Links             []SafeLink `json:"links"`
	SupportingSources []string   `json:"supporting_sources"`
	UpdatedAt         time.Time  `json:"updated_at"`
	Stale             bool       `json:"stale"`
}
