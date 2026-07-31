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

func TestCycleInputSchemaRequiresPassEvidenceForAdversarialCompletion(t *testing.T) {
	var schema struct {
		AllOf []struct {
			If struct {
				Properties map[string]struct {
					Const bool `json:"const"`
				} `json:"properties"`
				Required []string `json:"required"`
			} `json:"if"`
			Then struct {
				Properties map[string]struct {
					Type       string `json:"type"`
					Properties map[string]struct {
						Const string `json:"const"`
					} `json:"properties"`
					Required []string `json:"required"`
				} `json:"properties"`
			} `json:"then"`
		} `json:"allOf"`
	}
	if err := json.Unmarshal(CycleInputSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.AllOf) != 1 {
		t.Fatalf("cycle input schema constraints = %d, want 1", len(schema.AllOf))
	}
	conditional := schema.AllOf[0]
	if !conditional.If.Properties["adversarial_complete"].Const ||
		!slices.Equal(conditional.If.Required, []string{"adversarial_complete"}) {
		t.Fatalf("malformed adversarial completion condition: %+v", conditional.If)
	}
	evidence := conditional.Then.Properties["adversarial_evidence"]
	if evidence.Type != "object" ||
		evidence.Properties["result"].Const != "pass" ||
		!slices.Equal(evidence.Required, []string{"result"}) {
		t.Fatalf("malformed adversarial evidence requirement: %+v", evidence)
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

func TestDecisionActionSchemaMatchesContract(t *testing.T) {
	var document struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(DecisionSchema, &document); err != nil {
		t.Fatal(err)
	}
	got := document.Properties["action"].Enum
	slices.Sort(got)
	want := []string{ActionAddress, ActionContinue, ActionEscalate, ActionPark, ActionStop}
	if !slices.Equal(got, want) {
		t.Fatalf("action schema = %v, want %v", got, want)
	}
}

func TestDecisionSchemaRequiresNextReviewerForAddress(t *testing.T) {
	var schema struct {
		AllOf []struct {
			If struct {
				Properties map[string]struct {
					Const string `json:"const"`
				} `json:"properties"`
				Required []string `json:"required"`
			} `json:"if"`
			Then struct {
				Properties map[string]struct {
					MinItems int `json:"minItems"`
				} `json:"properties"`
			} `json:"then"`
		} `json:"allOf"`
	}
	if err := json.Unmarshal(DecisionSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema.AllOf) != 1 {
		t.Fatalf("decision schema constraints = %d, want 1", len(schema.AllOf))
	}
	conditional := schema.AllOf[0]
	if conditional.If.Properties["action"].Const != ActionAddress ||
		!slices.Equal(conditional.If.Required, []string{"action"}) ||
		conditional.Then.Properties["next_reviewers"].MinItems != 1 {
		t.Fatalf("malformed address reviewer requirement: %+v", conditional)
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

func TestAdversarialCompleteRequiresBoundPassEvidence(t *testing.T) {
	input := CycleInput{
		SchemaVersion:       SchemaVersion,
		Subject:             Subject{Repo: "itshabib/ship", Number: 1, HeadSHA: testHead},
		PlanID:              "rp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Cycle:               1,
		CurrentTier:         "T0",
		AdversarialComplete: true,
	}
	if err := ValidateCycleInput(input); err == nil {
		t.Fatal("unbound adversarial completion unexpectedly valid")
	}
	input.AdversarialEvidence = &AdversarialEvidence{
		Subject:        input.Subject,
		Source:         "local",
		Result:         "pass",
		Confidence:     1,
		ArtifactDigest: "sha256:" + strings.Repeat("d", 64),
	}
	if err := ValidateCycleInput(input); err != nil {
		t.Fatal(err)
	}
	before, err := CycleInputDigest(input)
	if err != nil {
		t.Fatal(err)
	}
	input.AdversarialEvidence.Confidence = 0.9
	after, err := CycleInputDigest(input)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("adversarial evidence mutation retained the same digest")
	}
	input.AdversarialEvidence.Result = "escalate"
	if err := ValidateCycleInput(input); err == nil {
		t.Fatal("escalated adversarial result claimed completion")
	}
	input.AdversarialComplete = false
	input.Subject.HeadSHA = strings.Repeat("b", 40)
	if err := ValidateCycleInput(input); err == nil {
		t.Fatal("cross-head adversarial evidence unexpectedly valid")
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

func TestDecisionAcceptsAddressAction(t *testing.T) {
	decision := validDecision()
	decision.Action = ActionAddress
	decision.ReasonCodes = []string{"accepted_findings_require_address"}
	decision.Findings = []FindingState{{
		ID: "f1", Severity: "low", Reviewers: []string{"codex"},
		Disposition: "fixed",
	}}
	if err := ValidateDecision(decision); err != nil {
		t.Fatalf("address decision rejected: %v", err)
	}
}

func TestDecisionRejectsAddressWithoutNextReviewer(t *testing.T) {
	decision := validDecision()
	decision.Action = ActionAddress
	decision.NextReviewers = nil
	if err := ValidateDecision(decision); err == nil {
		t.Fatal("address decision without next reviewer unexpectedly valid")
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
