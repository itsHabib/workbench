package placement

import (
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/dispatch/internal/policy"
)

func intp(n int) *int { return &n }

func loadedPolicy() policy.Loaded {
	return policy.Loaded{
		SHA256: "deadbeef",
		Policy: policy.Policy{
			Version: 1,
			Rules: []policy.Rule{
				{
					Name:  "small-mechanical",
					Match: policy.Match{TaskClass: policy.ClassMechanical, MaxWeightedLOC: intp(500), RiskTier: []string{"T0", "T1"}},
					Place: policy.Place{Engine: "ship-driver", Provider: "cursor", Model: "grok-4.5", Effort: "high", Runtime: "cloud"},
				},
				{
					Name:  "analytical-mid",
					Match: policy.Match{TaskClass: policy.ClassAnalytical, MaxWeightedLOC: intp(1500)},
					Place: policy.Place{Engine: "ship-driver", Provider: "claude", Model: "claude-sonnet-4-8", Effort: "high", Runtime: "cloud"},
				},
				{
					Name:  "catch-all",
					Match: policy.Match{},
					Place: policy.Place{Engine: "ship-driver", Provider: "claude", Model: "claude-opus-4-8", Effort: "max", Runtime: "local"},
				},
			},
		},
	}
}

func desc(class string, loc int, tier string) Descriptor {
	return Descriptor{Repo: "workbench", TaskClass: class, WeightedLOC: intp(loc), RiskTier: tier}
}

func TestParseDescriptorRejects(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing repo", body: `{"task_class":"mechanical","weighted_loc":10,"risk_tier":"T0"}`},
		{name: "unknown task_class", body: `{"repo":"r","task_class":"huge","weighted_loc":10,"risk_tier":"T0"}`},
		{name: "missing weighted_loc", body: `{"repo":"r","task_class":"mechanical","risk_tier":"T0"}`},
		{name: "negative weighted_loc", body: `{"repo":"r","task_class":"mechanical","weighted_loc":-1,"risk_tier":"T0"}`},
		{name: "missing risk_tier", body: `{"repo":"r","task_class":"mechanical","weighted_loc":10}`},
		{name: "unknown field", body: `{"repo":"r","task_class":"mechanical","weighted_loc":10,"risk_tier":"T0","x":1}`},
		{name: "malformed", body: `{`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseDescriptor([]byte(tt.body)); err == nil {
				t.Fatalf("ParseDescriptor(%s) must error", tt.body)
			}
		})
	}
}

func TestParseDescriptorAcceptsBudget(t *testing.T) {
	// budget is reserved: accepted and echoed, never gating a placement.
	d, err := ParseDescriptor([]byte(`{"repo":"r","task_class":"mechanical","weighted_loc":10,"risk_tier":"T0","budget":5}`))
	if err != nil {
		t.Fatalf("a descriptor with budget must parse: %v", err)
	}
	if string(d.Budget) != "5" {
		t.Fatalf("budget must be echoed, got %q", string(d.Budget))
	}
}

func TestDecideFirstMatchInFileOrder(t *testing.T) {
	lp := loadedPolicy()
	tests := []struct {
		name     string
		d        Descriptor
		wantRule string
	}{
		{name: "small mechanical hits first rule", d: desc(policy.ClassMechanical, 100, "T0"), wantRule: "small-mechanical"},
		{name: "mechanical over loc falls through to catch-all", d: desc(policy.ClassMechanical, 900, "T0"), wantRule: "catch-all"},
		{name: "mechanical wrong tier falls through", d: desc(policy.ClassMechanical, 100, "T3"), wantRule: "catch-all"},
		{name: "analytical mid hits second rule", d: desc(policy.ClassAnalytical, 1000, "T2"), wantRule: "analytical-mid"},
		{name: "generative goes to catch-all", d: desc(policy.ClassGenerative, 5000, "T3"), wantRule: "catch-all"},
		{name: "boundary loc is inclusive", d: desc(policy.ClassMechanical, 500, "T1"), wantRule: "small-mechanical"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pl, ok := Decide(lp, tt.d)
			if !ok {
				t.Fatalf("expected a match for %+v", tt.d)
			}
			if pl.Provenance.Rule != tt.wantRule {
				t.Fatalf("rule = %q, want %q", pl.Provenance.Rule, tt.wantRule)
			}
			if pl.SchemaVersion != SchemaVersion {
				t.Fatalf("schema_version = %d, want %d", pl.SchemaVersion, SchemaVersion)
			}
			if pl.Provenance.PolicySHA256 != lp.SHA256 {
				t.Fatalf("placement must carry the policy sha256")
			}
		})
	}
}

func TestDecideNoMatchFailsClosed(t *testing.T) {
	// A policy with no catch-all: a descriptor matching nothing returns false,
	// and the unmatched-values string carries the actual values.
	lp := policy.Loaded{Policy: policy.Policy{Version: 1, Rules: []policy.Rule{{
		Name:  "only-mechanical",
		Match: policy.Match{TaskClass: policy.ClassMechanical},
		Place: policy.Place{Engine: "e", Provider: "p", Model: "m", Effort: "f", Runtime: "r"},
	}}}}
	d := desc(policy.ClassAnalytical, 200, "T2")
	if _, ok := Decide(lp, d); ok {
		t.Fatal("a descriptor matching no rule must fail closed (ok=false)")
	}
	got := d.UnmatchedValues()
	for _, want := range []string{"task_class=analytical", "risk_tier=T2", "weighted_loc=200"} {
		if !strings.Contains(got, want) {
			t.Fatalf("UnmatchedValues() = %q, want to contain %q", got, want)
		}
	}
}

func TestDecodeIsDeterministic(t *testing.T) {
	lp := loadedPolicy()
	d := desc(policy.ClassMechanical, 100, "T0")
	first, _ := Decide(lp, d)
	second, _ := Decide(lp, d)
	if first != second {
		t.Fatalf("Decide must be deterministic: %+v != %+v", first, second)
	}
}
