// Package replay is the phase-2 validation gate (spec §11): it reproduces the
// operator's real historical placements by deriving each stream's descriptor
// via the phase-1 rules (docs/DESIGN.md) and running it through the exact
// decide engine (policy.Load + placement.Decide) — never hand-labeling a
// descriptor to fit a historical choice, and never reimplementing matching.
//
// It carries no decision logic of its own: it is a thin harness over the
// phase-1 packages, driven entirely by the fixtures in testdata/. The gate
// itself is the replay_test.go table test in this package.
package replay

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/itsHabib/workbench/cmd/dispatch/internal/placement"
	"github.com/itsHabib/workbench/cmd/dispatch/internal/policy"
)

// Descriptor is the derived descriptor baked into the fixture — the same four
// fields placement.Descriptor requires, kept as plain JSON so a fixture stays
// human-diffable and round-trips through the real ParseDescriptor path rather
// than a hand-built struct that could accidentally skip a validation rule.
type Descriptor struct {
	Repo        string `json:"repo"`
	TaskClass   string `json:"task_class"`
	WeightedLOC int    `json:"weighted_loc"`
	RiskTier    string `json:"risk_tier"`
}

// Derivation records, per descriptor field, which phase-1 rule and which
// source produced the value — so a reviewer can check every derivation against
// docs/DESIGN.md without re-deriving it themselves.
type Derivation struct {
	TaskClassSignal   string `json:"task_class_signal"`
	WeightedLOCSource string `json:"weighted_loc_source"`
	RiskTierSource    string `json:"risk_tier_source"`
}

// HistoricalStream is one row of the replay fixture: a real dispatched
// stream's derived descriptor and derivation trail, the placement it actually
// got, whether that placement was policy-driven (must MATCH) or
// experiment-driven (must DIVERGE with a defensible reason), and the expected
// classification the test asserts against.
type HistoricalStream struct {
	Stream       string       `json:"stream"`
	Repo         string       `json:"repo"`
	Phase        string       `json:"phase"`
	Source       string       `json:"source"`
	Descriptor   Descriptor   `json:"descriptor"`
	Derivation   Derivation   `json:"derivation"`
	Actual       policy.Place `json:"actual"`
	HowChosen    string       `json:"how_chosen"`
	PolicyDriven bool         `json:"policy_driven"`
	Expect       string       `json:"expect"` // "match" | "diverge"
}

// LoadHistorical reads the baked fixture of historical streams. Hermetic: it
// never reads a cross-repo live file at test time — the fixture is the
// frozen, committed evidence.
func LoadHistorical(path string) ([]HistoricalStream, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("replay: read %s: %w", path, err)
	}
	var streams []HistoricalStream
	if err := json.Unmarshal(raw, &streams); err != nil {
		return nil, fmt.Errorf("replay: parse %s: %w", path, err)
	}
	return streams, nil
}

// descriptorJSON marshals the fixture's derived descriptor back into the exact
// wire shape placement.ParseDescriptor expects, so the replay exercises the
// real parse-and-validate path rather than constructing a placement.Descriptor
// by hand.
func (h HistoricalStream) descriptorJSON() ([]byte, error) {
	b, err := json.Marshal(h.Descriptor)
	if err != nil {
		return nil, fmt.Errorf("replay: marshal descriptor for %s: %w", h.Stream, err)
	}
	return b, nil
}

// Result is the outcome of replaying one historical stream through a loaded
// policy: whether a rule fired at all, which one, what it emitted, and whether
// the emitted placement reproduces the actual historical placement.
type Result struct {
	Stream     string
	Matched    bool
	Rule       string
	Emitted    policy.Place
	Actual     policy.Place
	Reproduced bool
}

// Replay derives h's descriptor via the real parser and decides it against
// loaded via the real matcher — the same two calls the CLI's decide verb makes
// (main.go). No matching logic is reimplemented here.
func Replay(loaded policy.Loaded, h HistoricalStream) (Result, error) {
	raw, err := h.descriptorJSON()
	if err != nil {
		return Result{}, err
	}
	d, err := placement.ParseDescriptor(raw)
	if err != nil {
		return Result{}, fmt.Errorf("replay: %s: descriptor invalid: %w", h.Stream, err)
	}
	res := Result{Stream: h.Stream, Actual: h.Actual}
	pl, ok := placement.Decide(loaded, d)
	res.Matched = ok
	if !ok {
		return res, nil
	}
	res.Rule = pl.Provenance.Rule
	res.Emitted = pl.Place
	res.Reproduced = pl.Place == h.Actual
	return res, nil
}
