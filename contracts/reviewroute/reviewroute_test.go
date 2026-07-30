package reviewroute

import (
	"encoding/json"
	"testing"
	"time"
)

const testHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestEmbeddedSchemasAreJSON(t *testing.T) {
	for name, schema := range map[string][]byte{
		"policy": PolicySchema, "plan": PlanSchema, "request": RequestSchema,
		"cycle input": CycleInputSchema, "decision": DecisionSchema,
	} {
		t.Run(name, func(t *testing.T) {
			var value any
			if err := json.Unmarshal(schema, &value); err != nil {
				t.Fatalf("invalid schema JSON: %v", err)
			}
		})
	}
}

func TestPolicyRequiresCompleteTierMap(t *testing.T) {
	policy := validPolicy()
	delete(policy.Tiers, "T3")
	if err := ValidatePolicy(policy); err == nil {
		t.Fatal("incomplete policy unexpectedly valid")
	}
}

func TestPolicyRejectsUnknownReviewerAndBadCycleCap(t *testing.T) {
	policy := validPolicy()
	tier := policy.Tiers["T2"]
	tier.Reviewers = append(tier.Reviewers, "unknown")
	policy.Tiers["T2"] = tier
	if err := ValidatePolicy(policy); err == nil {
		t.Fatal("unknown reviewer unexpectedly valid")
	}

	policy = validPolicy()
	tier = policy.Tiers["T3"]
	tier.MaxCycles = 9
	policy.Tiers["T3"] = tier
	if err := ValidatePolicy(policy); err == nil {
		t.Fatal("cycle cap above eight unexpectedly valid")
	}
}

func TestPlanIdentityDetectsMutation(t *testing.T) {
	plan := Plan{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
		Subject:       Subject{Repo: "itsHabib/ship", Number: 1, HeadSHA: testHead},
		Disposition:   "tier_routed",
		Policy:        &PolicyRef{ID: "canary", Revision: 1},
		Classification: &Classification{
			Tier: "T1", Reasons: []string{},
		},
		Reviewers: []Reviewer{{Name: "codex", Trigger: "mention"}},
		Required:  []string{"codex"},
		MaxCycles: 3,
		Requirements: Requirements{
			Coordinator: "on-findings",
		},
	}
	id, err := PlanID(plan)
	if err != nil {
		t.Fatal(err)
	}
	plan.PlanID = id
	if err := ValidatePlan(plan); err != nil {
		t.Fatal(err)
	}
	plan.MaxCycles = 2
	if err := ValidatePlan(plan); err == nil {
		t.Fatal("mutated plan retained a valid identity")
	}
}

func TestDeferredDebtRequiresReasonAndFollowUp(t *testing.T) {
	input := CycleInput{
		SchemaVersion: SchemaVersion,
		Subject:       Subject{Repo: "itsHabib/ship", Number: 1, HeadSHA: testHead},
		PlanID:        "rp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Cycle:         1,
		CurrentTier:   "T1",
		Findings: []FindingState{{
			ID: "f1", Severity: "low", Reviewers: []string{"codex"},
			Disposition: "deferred", Debt: true,
		}},
	}
	if err := ValidateCycleInput(input); err == nil {
		t.Fatal("unexplained debt deferment unexpectedly valid")
	}
	input.Findings[0].DeferReason = "outside scope"
	input.Findings[0].FollowUpRef = "issue:1"
	if err := ValidateCycleInput(input); err != nil {
		t.Fatal(err)
	}
}

func validPolicy() Policy {
	panel := []Reviewer{
		{Name: "codex", Trigger: "mention"},
		{Name: "claude", Trigger: "mention"},
		{Name: "cursor", Trigger: "mention"},
		{Name: "copilot", Trigger: "reviewer-request"},
	}
	tier := func(reviewers, required []string, cycles int) TierPolicy {
		return TierPolicy{
			Reviewers: reviewers, Required: required, MaxCycles: cycles,
			Requirements: Requirements{Coordinator: "none"},
		}
	}
	return Policy{
		SchemaVersion: SchemaVersion, ID: "canary", Revision: 1,
		EnabledRepositories: []string{"itsHabib/ship"}, FullPanel: panel,
		Tiers: map[string]TierPolicy{
			"T0": tier(nil, nil, 1),
			"T1": tier([]string{"codex"}, []string{"codex"}, 3),
			"T2": tier([]string{"codex", "cursor"}, []string{"codex"}, 3),
			"T3": tier([]string{"codex", "claude", "cursor", "copilot"}, []string{"codex", "claude", "cursor"}, 8),
		},
	}
}
