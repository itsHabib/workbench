package rulesetplan

import (
	"path/filepath"
	"testing"
)

func TestStagedRepositoryPlan(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "docs", "features",
		"trusted-gate-judgment-bridge", "ruleset-plan-v1.json")
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
}

func TestGateAppCannotGainOtherBranchAuthority(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "docs", "features",
		"trusted-gate-judgment-bridge", "ruleset-plan-v1.json")
	plan, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range plan.Rulesets {
		if plan.Rulesets[i].Name == "other-branches" {
			plan.Rulesets[i].AllowedWriters = append(plan.Rulesets[i].AllowedWriters, "gate_app")
		}
	}
	if err := Validate(plan); err == nil {
		t.Fatal("Gate App authority outside main must refuse")
	}
}
