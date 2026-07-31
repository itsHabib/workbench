// Package automode defines the provider-neutral artifacts exchanged by
// deterministic auto-mode policy and harness lifecycle adapters.
package automode

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

const (
	// SchemaVersion is the one AutoDecision major version this package accepts.
	SchemaVersion = "auto-decision.v1"

	// OutcomePass allows the action.
	OutcomePass = "pass"
	// OutcomePark defers the action for more evidence or authority.
	OutcomePark = "park"
	// OutcomeBlock rejects a deterministically unsafe action.
	OutcomeBlock = "block"
	// OutcomeRefuse rejects an action the policy cannot safely authorize.
	OutcomeRefuse = "refuse"

	// EventDecision is written before execution.
	EventDecision = "decision"
	// EventCompletion records evidence after an allowed call.
	EventCompletion = "completion"

	// StatusSucceeded records a successful allowed call.
	StatusSucceeded = "succeeded"
	// StatusFailed records a failed allowed call.
	StatusFailed = "failed"

	// RedactionMarker is the only persisted value for a redacted input.
	RedactionMarker = "[REDACTED]"
)

// Decision is one replayable deterministic auto-mode decision.
type Decision struct {
	SchemaVersion   string          `json:"schema_version"`
	Outcome         string          `json:"outcome"`
	RuleFired       string          `json:"rule_fired"`
	RulebookVersion string          `json:"rulebook_version"`
	Remedy          string          `json:"remedy"`
	Harness         string          `json:"harness"`
	Inputs          InputProjection `json:"inputs"`
	ActionDigest    string          `json:"action_digest"`
}

// InputProjection is the secret-safe evidence a policy decision can replay.
type InputProjection struct {
	Action      Action       `json:"action"`
	Observables []NamedValue `json:"observables"`
}

// Action is a parsed semantic operation, never opaque command text.
type Action struct {
	Envelope   string       `json:"envelope"`
	Operation  string       `json:"operation"`
	Parameters []NamedValue `json:"parameters"`
}

// NamedValue is one normalized input. Redacted values must contain only
// RedactionMarker.
type NamedValue struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Redacted bool   `json:"redacted"`
}

// RoutingProjection is the stable subset lifecycle adapters route on.
type RoutingProjection struct {
	Outcome   string `json:"outcome"`
	RuleFired string `json:"rule_fired"`
	Remedy    string `json:"remedy"`
}

// AuditEvent is one append-only lifecycle record. Decision events are written
// before execution; completion events add evidence after an allowed call.
type AuditEvent struct {
	SchemaVersion string      `json:"schema_version"`
	EventID       string      `json:"event_id"`
	InvocationID  string      `json:"invocation_id"`
	RecordedAt    time.Time   `json:"recorded_at"`
	Kind          string      `json:"kind"`
	Decision      Decision    `json:"decision"`
	Completion    *Completion `json:"completion,omitempty"`
}

// Completion records the observable result of an allowed call.
type Completion struct {
	Status      string       `json:"status"`
	Observables []NamedValue `json:"observables"`
}

// DecodeDecision tolerantly decodes additive v1 fields, then applies contract
// validation. Unknown major versions reject.
func DecodeDecision(data []byte) (Decision, error) {
	var decision Decision
	if err := json.Unmarshal(data, &decision); err != nil {
		return Decision{}, fmt.Errorf("automode: decode decision: %w", err)
	}
	if err := ValidateDecision(decision); err != nil {
		return Decision{}, err
	}
	return decision, nil
}

// DecodeAuditEvent tolerantly decodes one audit event and validates it.
func DecodeAuditEvent(data []byte) (AuditEvent, error) {
	var event AuditEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return AuditEvent{}, fmt.Errorf("automode: decode audit event: %w", err)
	}
	if err := ValidateAuditEvent(event); err != nil {
		return AuditEvent{}, err
	}
	return event, nil
}

// Routing returns the only fields a lifecycle adapter may route on.
func (d Decision) Routing() RoutingProjection {
	return RoutingProjection{
		Outcome:   d.Outcome,
		RuleFired: d.RuleFired,
		Remedy:    d.Remedy,
	}
}

// Digest returns the canonical SHA-256 identity of a normalized input
// projection. It hashes the language-neutral framed representation documented
// in README.md; ordering of named values does not affect the result.
func Digest(inputs InputProjection) (string, error) {
	if err := validateInputs(inputs); err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonicalInputs(inputs))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func canonicalInputs(inputs InputProjection) []byte {
	out := []byte("workbench.automode.inputs.v1")
	out = appendString(out, inputs.Action.Envelope)
	out = appendString(out, inputs.Action.Operation)
	out = appendValues(out, sortedValues(inputs.Action.Parameters))
	return appendValues(out, sortedValues(inputs.Observables))
}

func appendValues(out []byte, values []NamedValue) []byte {
	out = binary.AppendUvarint(out, uint64(len(values)))
	for _, value := range values {
		out = appendString(out, value.Name)
		if value.Redacted {
			out = append(out, 1)
		} else {
			out = append(out, 0)
		}
		out = appendString(out, value.Value)
	}
	return out
}

func appendString(out []byte, value string) []byte {
	out = binary.AppendUvarint(out, uint64(len(value)))
	return append(out, value...)
}

func sortedValues(values []NamedValue) []NamedValue {
	out := make([]NamedValue, len(values))
	copy(out, values)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}
