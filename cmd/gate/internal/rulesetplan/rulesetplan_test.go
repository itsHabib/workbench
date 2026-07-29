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

func TestOtherBranchRuleMustCoverNestedBranchesAndRestrictUpdates(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "docs", "features",
		"trusted-gate-judgment-bridge", "ruleset-plan-v1.json")
	tests := map[string]func(*Ruleset){
		"single segment glob": func(ruleset *Ruleset) {
			ruleset.Includes = []string{"refs/heads/*"}
		},
		"updates unrestricted": func(ruleset *Ruleset) {
			ruleset.RestrictUpdates = false
		},
		"implicit writers": func(ruleset *Ruleset) {
			ruleset.AllowedWriters = nil
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			plan, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			for i := range plan.Rulesets {
				if plan.Rulesets[i].Name == "other-branches" {
					mutate(&plan.Rulesets[i])
				}
			}
			if err := Validate(plan); err == nil {
				t.Fatal("unsafe other-branch rule must refuse")
			}
		})
	}
}

func TestGenericActionsCannotWriteGateState(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "docs", "features",
		"trusted-gate-judgment-bridge", "ruleset-plan-v1.json")
	plan, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range plan.Rulesets {
		if plan.Rulesets[i].Name == "gate-state-updates" {
			plan.Rulesets[i].AllowedWriters = []string{"github_actions"}
		}
	}
	if err := Validate(plan); err == nil {
		t.Fatal("generic GitHub Actions state writer must refuse")
	}
}
