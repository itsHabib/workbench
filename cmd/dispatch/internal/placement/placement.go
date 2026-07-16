// Package placement turns a validated policy plus a task descriptor into a
// deterministic placement decision. It owns the descriptor schema, the
// placement shape (which versions itself — the CLI is the contract), and the
// decision provenance. The first-match scan lives in match.go; this file holds
// the descriptor, the placement, and the Decide composition.
//
// Determinism (FR4) is load-bearing: given the same descriptor and the same
// policy, Decide returns a byte-identical placement. The shape is a fixed-field
// struct — never a Go map — so json.Marshal emits a stable field order.
package placement

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/itsHabib/workbench/cmd/dispatch/internal/policy"
)

// SchemaVersion is the placement shape's own version — distinct from the policy
// version. The CLI stdout is the contract, so it versions itself independently
// of the policy file that fed a given decision.
const SchemaVersion = 1

// Descriptor is the task under placement (spec §5). budget is reserved: it is
// accepted and echoed into the receipt, but no v1 rule prices anything, so it
// is carried into no placement. RawMessage keeps the reserved field shape-open
// without committing to a type before telemetry earns one.
type Descriptor struct {
	Repo        string          `json:"repo"`
	TaskClass   string          `json:"task_class"`
	WeightedLOC *int            `json:"weighted_loc"`
	RiskTier    string          `json:"risk_tier"`
	Budget      json.RawMessage `json:"budget,omitempty"`
}

// Provenance names the rule that fired and pins the policy it fired under, so
// every decision is explainable (FR5) and replayable (the phase-2 gate).
type Provenance struct {
	Rule          string `json:"rule"`
	PolicyVersion int    `json:"policy_version"`
	PolicySHA256  string `json:"policy_sha256"`
}

// Placement is the stdout decision: the shape version, the matched rule's place
// and escalation, and the provenance. Fixed field order — the determinism
// guarantee depends on there being no map here.
type Placement struct {
	SchemaVersion int               `json:"schema_version"`
	Place         policy.Place      `json:"place"`
	Escalation    policy.Escalation `json:"escalation"`
	Provenance    Provenance        `json:"provenance"`
}

// ParseDescriptor decodes and validates a descriptor from raw JSON. Unknown
// fields and an unknown task_class are refused; the caller maps every error
// here to exit 4.
func ParseDescriptor(raw []byte) (Descriptor, error) {
	var d Descriptor
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return Descriptor{}, fmt.Errorf("descriptor: parse: %w", err)
	}
	if err := d.validate(); err != nil {
		return Descriptor{}, fmt.Errorf("descriptor: %w", err)
	}
	return d, nil
}

func (d Descriptor) validate() error {
	if d.Repo == "" {
		return errors.New("repo is required")
	}
	if !policy.ValidClass(d.TaskClass) {
		return fmt.Errorf("unknown task_class %q (want %s|%s|%s)", d.TaskClass, policy.ClassMechanical, policy.ClassAnalytical, policy.ClassGenerative)
	}
	if d.WeightedLOC == nil {
		return errors.New("weighted_loc is required")
	}
	if *d.WeightedLOC < 0 {
		return errors.New("weighted_loc must be >= 0")
	}
	if d.RiskTier == "" {
		return errors.New("risk_tier is required")
	}
	return nil
}

// loc returns the descriptor's weighted LOC. Callers reach it only after
// validate has guaranteed the pointer is non-nil; the nil guard keeps the zero
// value useful for a raw (unvalidated) descriptor.
func (d Descriptor) loc() int {
	if d.WeightedLOC == nil {
		return 0
	}
	return *d.WeightedLOC
}

// UnmatchedValues renders the descriptor's actual values for the exit-3 error —
// the caller must see what failed to match (task_class=analytical risk_tier=T2),
// never a field-name template and never a fallback placement.
func (d Descriptor) UnmatchedValues() string {
	return fmt.Sprintf("task_class=%s risk_tier=%s weighted_loc=%d", d.TaskClass, d.RiskTier, d.loc())
}

// Decide runs the first-match scan and builds the placement for the matched
// rule. The bool is false when no rule matched — a fail-closed miss the caller
// turns into exit 3, never a default placement. Pure: no clock, no I/O, no
// randomness.
func Decide(loaded policy.Loaded, d Descriptor) (Placement, bool) {
	rule, ok := match(loaded.Policy.Rules, d)
	if !ok {
		return Placement{}, false
	}
	return Placement{
		SchemaVersion: SchemaVersion,
		Place:         rule.Place,
		Escalation:    rule.Escalation,
		Provenance: Provenance{
			Rule:          rule.Name,
			PolicyVersion: loaded.Policy.Version,
			PolicySHA256:  loaded.SHA256,
		},
	}, true
}
