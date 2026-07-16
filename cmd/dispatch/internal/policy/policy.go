// Package policy is dispatch's data model and fail-closed loader: it reads a
// versioned policy file, content-hashes the exact file bytes, and validates the
// frozen task_class taxonomy before any placement decision is made. It is the
// leaf of the tool — it holds the vocabulary (rules, matches, placements) and
// the schema rules, and carries no matching or receipt logic.
package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// Version is the policy-file major this binary understands. An unknown version
// is refused, never best-effort parsed — a schema break must be explicit.
const Version = 1

// The frozen task_class taxonomy (spec §5, §10.1). Complexity/novelty only —
// size lives in weighted_loc, never here. Frozen in phase 1 because the phase-2
// replay gate is circular unless the enum is fixed before any descriptor is
// derived: a typo like "task_clas" must fail loudly (exit 2 in a policy match
// block, exit 4 in a descriptor) instead of silently never matching.
const (
	ClassMechanical = "mechanical"
	ClassAnalytical = "analytical"
	ClassGenerative = "generative"
)

// ValidClass reports whether c is one of the frozen task classes. The single
// source of truth for the enum — both the policy loader and the descriptor
// parser gate on it.
func ValidClass(c string) bool {
	switch c {
	case ClassMechanical, ClassAnalytical, ClassGenerative:
		return true
	}
	return false
}

// Match selects descriptors. An empty Match ({}) is a catch-all: it matches
// anything. Every set constraint must hold (AND semantics); a zero/absent
// constraint is "any". RiskTier is an opaque-string allow-list (the tier
// vocabulary is /pr-risk's, shared as contract not call stack).
type Match struct {
	TaskClass      string   `json:"task_class,omitempty"`
	MaxWeightedLOC *int     `json:"max_weighted_loc,omitempty"`
	RiskTier       []string `json:"risk_tier,omitempty"`
}

// IsCatchAll reports whether the match constrains nothing — the explicit
// catch-all the fail-closed design requires an operator to write down.
func (m Match) IsCatchAll() bool {
	return m.TaskClass == "" && m.MaxWeightedLOC == nil && len(m.RiskTier) == 0
}

// Place is the placement a matched rule emits: the engine/provider/model and
// how hard to run it. All five fields are required — a rule that places
// nowhere is a schema error.
type Place struct {
	Engine   string `json:"engine"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Effort   string `json:"effort"`
	Runtime  string `json:"runtime"`
}

// Escalation is the advisory escalation rule carried in the placement; the seat
// driving the run enforces it — dispatch never does (spec §4.4). Optional per
// rule; the zero value means "no escalation stated".
type Escalation struct {
	MaxRoundsPerDefectClass int    `json:"max_rounds_per_defect_class"`
	EscalateTo              string `json:"escalate_to"`
}

// Rule is one policy row: a name (carried into provenance so every decision is
// explainable), a match, its placement, and an optional escalation.
type Rule struct {
	Name       string     `json:"name"`
	Match      Match      `json:"match"`
	Place      Place      `json:"place"`
	Escalation Escalation `json:"escalation"`
}

// Policy is the whole versioned policy file: rules are scanned first-match in
// file order.
type Policy struct {
	Version int    `json:"version"`
	Rules   []Rule `json:"rules"`
}

// HasCatchAll reports whether any rule matches everything — the validate verb's
// catch-all lint keys on this.
func (p Policy) HasCatchAll() bool {
	for _, r := range p.Rules {
		if r.Match.IsCatchAll() {
			return true
		}
	}
	return false
}

// Loaded is a validated policy plus the sha256 of the exact file bytes it was
// read from. The hash pins every decision made under it regardless of the
// version field.
type Loaded struct {
	Policy Policy
	SHA256 string
}

// Load reads, hashes, and validates a policy file. The sha256 is computed over
// the exact on-disk bytes before parsing, so it pins content independent of
// schema. Unknown fields, unknown versions, an empty rule set, and an unknown
// task_class in any match block are all refused — the caller maps every error
// here to exit 2, before any descriptor is read.
func Load(path string) (Loaded, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Loaded{}, fmt.Errorf("policy: read %s: %w", path, err)
	}
	sum := sha256.Sum256(raw)
	var p Policy
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return Loaded{}, fmt.Errorf("policy: parse %s: %w", path, err)
	}
	if err := validate(p); err != nil {
		return Loaded{}, fmt.Errorf("policy: %s: %w", path, err)
	}
	return Loaded{Policy: p, SHA256: hex.EncodeToString(sum[:])}, nil
}

func validate(p Policy) error {
	if p.Version != Version {
		return fmt.Errorf("version %d not supported (want %d)", p.Version, Version)
	}
	if len(p.Rules) == 0 {
		return errors.New("no rules: an empty policy is an authoring error, not a descriptor mismatch")
	}
	for i, r := range p.Rules {
		if err := validateRule(r); err != nil {
			return fmt.Errorf("rule %d (%q): %w", i, r.Name, err)
		}
	}
	return nil
}

func validateRule(r Rule) error {
	if r.Name == "" {
		return errors.New("name is required")
	}
	if r.Match.TaskClass != "" && !ValidClass(r.Match.TaskClass) {
		return fmt.Errorf("unknown task_class %q (want %s|%s|%s)", r.Match.TaskClass, ClassMechanical, ClassAnalytical, ClassGenerative)
	}
	return validatePlace(r.Place)
}

func validatePlace(pl Place) error {
	if pl.Engine == "" || pl.Provider == "" || pl.Model == "" || pl.Effort == "" || pl.Runtime == "" {
		return errors.New("place requires engine, provider, model, effort, runtime")
	}
	return nil
}
