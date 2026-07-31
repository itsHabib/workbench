package reviewroute

import (
	"encoding/json"
	"slices"
	"strings"
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

func TestValidSHARequiresLowercase(t *testing.T) {
	if !validSHA(strings.Repeat("a", 40)) {
		t.Fatal("lowercase SHA unexpectedly invalid")
	}
	if validSHA(strings.Repeat("A", 40)) {
		t.Fatal("uppercase SHA unexpectedly valid")
	}
}

func TestRouteDispositionSchemaMatchesContract(t *testing.T) {
	want := []string{
		"deliberately_overridden",
		"full_panel_fallback",
		"parked_unverified",
		"tier_routed",
	}
	for name, schema := range map[string][]byte{
		"plan": PlanSchema, "decision": DecisionSchema,
	} {
		t.Run(name, func(t *testing.T) {
			var document struct {
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(schema, &document); err != nil {
				t.Fatal(err)
			}
			property := "disposition"
			if name == "decision" {
				property = "route_disposition"
			}
			got := document.Properties[property].Enum
			slices.Sort(got)
			if !slices.Equal(got, want) {
				t.Fatalf("%s enum = %v, want %v", property, got, want)
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

func TestPolicyDigestDetectsContentMutation(t *testing.T) {
	policy := validPolicy()
	before, err := PolicyDigest(policy)
	if err != nil {
		t.Fatal(err)
	}
	tier := policy.Tiers["T1"]
	tier.MaxCycles = 2
	policy.Tiers["T1"] = tier
	after, err := PolicyDigest(policy)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("policy mutation retained the same digest")
	}
}

func TestPlanIdentityDetectsMutation(t *testing.T) {
	plan := Plan{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
		Subject:       Subject{Repo: "itshabib/ship", Number: 1, HeadSHA: testHead},
		Disposition:   "tier_routed",
		Policy:        &PolicyRef{ID: "canary", Digest: "sha256:" + strings.Repeat("a", 64)},
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
		Subject:       Subject{Repo: "itshabib/ship", Number: 1, HeadSHA: testHead},
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

func TestProvedSafeRequiresClosureOrProof(t *testing.T) {
	input := CycleInput{
		SchemaVersion: SchemaVersion,
		Subject:       Subject{Repo: "itshabib/ship", Number: 1, HeadSHA: testHead},
		PlanID:        "rp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Cycle:         1,
		CurrentTier:   "T1",
		Findings: []FindingState{{
			ID: "f1", Severity: "low", Reviewers: []string{"codex"},
			Disposition: "proved_safe",
		}},
	}
	if err := ValidateCycleInput(input); err == nil {
		t.Fatal("unproved proved_safe finding unexpectedly valid")
	}
	input.Findings[0].ProofRef = "test:regression"
	if err := ValidateCycleInput(input); err != nil {
		t.Fatal(err)
	}
	input.Findings[0].ProofRef = ""
	input.Findings[0].ReviewerClosed = true
	if err := ValidateCycleInput(input); err != nil {
		t.Fatal(err)
	}
}

func TestCycleInputDigestDetectsEvidenceMutation(t *testing.T) {
	input := CycleInput{
		SchemaVersion: SchemaVersion,
		Subject:       Subject{Repo: "itshabib/ship", Number: 1, HeadSHA: testHead},
		PlanID:        "rp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Cycle:         1,
		CurrentTier:   "T1",
	}
	before, err := CycleInputDigest(input)
	if err != nil {
		t.Fatal(err)
	}
	input.ChecksPassed = true
	after, err := CycleInputDigest(input)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("cycle evidence mutation retained the same digest")
	}
}

func TestCycleInputRejectsDuplicateCompletedReviewers(t *testing.T) {
	input := CycleInput{
		SchemaVersion:      SchemaVersion,
		Subject:            Subject{Repo: "itshabib/ship", Number: 1, HeadSHA: testHead},
		PlanID:             "rp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Cycle:              1,
		CurrentTier:        "T1",
		CompletedReviewers: []string{"codex", "codex"},
	}
	if err := ValidateCycleInput(input); err == nil {
		t.Fatal("duplicate completed reviewer unexpectedly valid")
	}
}

func TestRequestReceiptCannotClaimSuccessOnAnotherHead(t *testing.T) {
	receipt := RequestReceipt{
		SchemaVersion: SchemaVersion,
		GeneratedAt:   time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
		Subject:       Subject{Repo: "itshabib/ship", Number: 1, HeadSHA: testHead},
		PlanID:        "rp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		HeadBefore:    strings.Repeat("b", 40),
		HeadAfter:     strings.Repeat("b", 40),
		Status:        "requested",
		Requests:      []Request{},
	}
	if err := ValidateRequestReceipt(receipt); err == nil {
		t.Fatal("cross-head successful request unexpectedly valid")
	}
	receipt.Status = "stale"
	receipt.Reason = "head changed"
	if err := ValidateRequestReceipt(receipt); err != nil {
		t.Fatalf("stale receipt rejected: %v", err)
	}
}

func TestDecisionRejectsDuplicateFindingIDs(t *testing.T) {
	decision := validDecision()
	decision.Findings = append(decision.Findings, decision.Findings[0])
	if err := ValidateDecision(decision); err == nil {
		t.Fatal("duplicate decision finding id unexpectedly valid")
	}
}

func validDecision() Decision {
	return Decision{
		SchemaVersion:      SchemaVersion,
		GeneratedAt:        time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
		Subject:            Subject{Repo: "itshabib/ship", Number: 1, HeadSHA: testHead},
		PlanID:             "rp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		InputDigest:        "sha256:" + strings.Repeat("b", 64),
		Policy:             &PolicyRef{ID: "canary", Digest: "sha256:" + strings.Repeat("c", 64)},
		RouteDisposition:   "tier_routed",
		Tier:               "T1",
		Cycle:              1,
		ContinuationWeight: 1,
		CumulativeWeight:   1,
		Action:             ActionContinue,
		ReasonCodes:        []string{"finding_unresolved"},
		NextReviewers:      []string{"codex"},
		Findings: []FindingState{{
			ID: "f1", Severity: "low", Reviewers: []string{"codex"},
			Disposition: "unresolved",
		}},
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
		SchemaVersion: SchemaVersion, ID: "canary",
		EnabledRepositories: []string{"itsHabib/ship"}, FullPanel: panel,
		Tiers: map[string]TierPolicy{
			"T0": tier(nil, nil, 1),
			"T1": tier([]string{"codex"}, []string{"codex"}, 3),
			"T2": tier([]string{"codex", "cursor"}, []string{"codex"}, 3),
			"T3": tier([]string{"codex", "claude", "cursor", "copilot"}, []string{"codex", "claude", "cursor"}, 8),
		},
	}
}
