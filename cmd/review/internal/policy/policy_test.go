package policy

import (
	"testing"
	"time"

	"github.com/itsHabib/workbench/contracts/reviewroute"
)

const (
	headA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	headB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

var testNow = time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

func TestRouteSelectsConfiguredTierStrategies(t *testing.T) {
	tests := []struct {
		tier      string
		reviewers []string
		cycles    int
	}{
		{tier: "T0", cycles: 1},
		{tier: "T1", reviewers: []string{"codex"}, cycles: 3},
		{tier: "T2", reviewers: []string{"codex", "copilot", "cursor"}, cycles: 3},
		{tier: "T3", reviewers: []string{"claude", "codex", "copilot", "cursor"}, cycles: 8},
	}
	for _, tt := range tests {
		t.Run(tt.tier, func(t *testing.T) {
			plan, err := Route(testPolicy(), subject(headA),
				reviewroute.Classification{Tier: tt.tier}, testNow)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Disposition != "tier_routed" || plan.MaxCycles != tt.cycles {
				t.Fatalf("route = %s/%d, want tier_routed/%d", plan.Disposition, plan.MaxCycles, tt.cycles)
			}
			got := reviewerNames(plan.Reviewers)
			if !equal(got, tt.reviewers) {
				t.Fatalf("reviewers = %v, want %v", got, tt.reviewers)
			}
			if err := reviewroute.ValidatePlan(plan); err != nil {
				t.Fatalf("invalid plan: %v", err)
			}
		})
	}
}

func TestRouteDoesNotReduceUnenabledRepository(t *testing.T) {
	plan, err := Route(testPolicy(), reviewroute.Subject{
		Repo: "work/example", Number: 7, HeadSHA: headA,
	}, reviewroute.Classification{Tier: "T0"}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Disposition != "full_panel_fallback" || len(plan.Reviewers) != 4 {
		t.Fatalf("route = %s with %d reviewers", plan.Disposition, len(plan.Reviewers))
	}
}

func TestFallbackDoesNotTrustInvalidPolicy(t *testing.T) {
	plan, err := Fallback(subject(headA), nil, testNow,
		"full_panel_fallback", "invalid policy")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Reviewers) != 4 || len(plan.Required) != 4 {
		t.Fatalf("fallback panel = %v required %v", plan.Reviewers, plan.Required)
	}
}

func TestDecideStopsBelowCapWhenEvidenceIsComplete(t *testing.T) {
	plan := routedPlan(t, "T2")
	input := cleanInput(plan, 1)
	decision, err := Decide(plan, input, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != reviewroute.ActionStop {
		t.Fatalf("action = %s, reasons %v", decision.Action, decision.ReasonCodes)
	}
	if decision.ContinuationWeight != 1 || decision.CumulativeWeight != 1 {
		t.Fatalf("weights = %d/%d", decision.ContinuationWeight, decision.CumulativeWeight)
	}
}

func TestDecideTargetsOnlyFindingAuthorsStillInPlay(t *testing.T) {
	plan := routedPlan(t, "T2")
	input := cleanInput(plan, 2)
	input.Findings = []reviewroute.FindingState{
		{
			ID: "codex-1", Severity: "medium", Reviewers: []string{"codex"},
			Disposition: "fixed", Changed: true, ReviewerClosed: false,
		},
		{
			ID: "cursor-2", Severity: "low", Reviewers: []string{"cursor"},
			Disposition: "fixed", Changed: true, ReviewerClosed: true,
		},
	}
	decision, err := Decide(plan, input, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != reviewroute.ActionContinue ||
		!equal(decision.NextReviewers, []string{"codex"}) {
		t.Fatalf("decision = %s next %v reasons %v", decision.Action, decision.NextReviewers, decision.ReasonCodes)
	}
	if decision.ContinuationWeight != 2 || decision.CumulativeWeight != 3 {
		t.Fatalf("weights = %d/%d", decision.ContinuationWeight, decision.CumulativeWeight)
	}
}

func TestProofSubstitutionClosesNoncriticalT0ToT2(t *testing.T) {
	for _, tier := range []string{"T0", "T1", "T2"} {
		t.Run(tier, func(t *testing.T) {
			plan := routedPlan(t, tier)
			input := cleanInput(plan, 1)
			input.Findings = []reviewroute.FindingState{{
				ID: "codex-1", Severity: "medium", Reviewers: []string{"codex"},
				Disposition: "fixed", Changed: true, ProofRef: "test:regression",
			}}
			decision, err := Decide(plan, input, testNow)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != reviewroute.ActionStop {
				t.Fatalf("action = %s, reasons %v", decision.Action, decision.ReasonCodes)
			}
		})
	}
}

func TestProofSubstitutionCanReplaceLaterBotAndCoordinatorRereview(t *testing.T) {
	plan := routedPlan(t, "T2")
	input := cleanInput(plan, 2)
	input.PanelComplete = false
	input.CompletedReviewers = nil
	input.CoordinatorComplete = false
	input.Findings = []reviewroute.FindingState{{
		ID: "codex-1", Severity: "medium", Reviewers: []string{"codex"},
		Disposition: "fixed", Changed: true, ProofRef: "test:exact-head-regression",
	}}
	decision, err := Decide(plan, input, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != reviewroute.ActionStop ||
		!contains(decision.ReasonCodes, "deterministic_proof_substitution") {
		t.Fatalf("decision = %s reasons %v", decision.Action, decision.ReasonCodes)
	}
}

func TestCriticalFindingCannotBeDeferredOrProofSubstituted(t *testing.T) {
	for _, disposition := range []string{"deferred", "fixed"} {
		t.Run(disposition, func(t *testing.T) {
			plan := routedPlan(t, "T2")
			input := cleanInput(plan, 1)
			input.Findings = []reviewroute.FindingState{{
				ID: "codex-1", Severity: "critical", Reviewers: []string{"codex"},
				Disposition: disposition, Changed: true, ProofRef: "test:regression",
				DeferReason: "later", FollowUpRef: "issue:1", Debt: disposition == "deferred",
			}}
			decision, err := Decide(plan, input, testNow)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != reviewroute.ActionContinue {
				t.Fatalf("action = %s, reasons %v", decision.Action, decision.ReasonCodes)
			}
			if !equal(decision.NextReviewers, []string{"codex"}) {
				t.Fatalf("next reviewers = %v", decision.NextReviewers)
			}
		})
	}
}

func TestValidNoncriticalDefermentStops(t *testing.T) {
	plan := routedPlan(t, "T1")
	input := cleanInput(plan, 1)
	input.Findings = []reviewroute.FindingState{{
		ID: "codex-1", Severity: "low", Reviewers: []string{"codex"},
		Disposition: "deferred", DeferReason: "outside this PR",
		Debt: true, FollowUpRef: "issue:42",
	}}
	decision, err := Decide(plan, input, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != reviewroute.ActionStop {
		t.Fatalf("action = %s, reasons %v", decision.Action, decision.ReasonCodes)
	}
}

func TestT0UncertaintyEscalatesInsteadOfTakingSecondCycle(t *testing.T) {
	plan := routedPlan(t, "T0")
	input := cleanInput(plan, 1)
	input.ChecksPassed = false
	decision, err := Decide(plan, input, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != reviewroute.ActionEscalate ||
		!contains(decision.ReasonCodes, "t0_uncertainty_requires_reclassification") {
		t.Fatalf("decision = %s reasons %v", decision.Action, decision.ReasonCodes)
	}
}

func TestT0RequiresLocalAdversarialPass(t *testing.T) {
	plan := routedPlan(t, "T0")
	input := cleanInput(plan, 1)
	input.AdversarialComplete = false
	decision, err := Decide(plan, input, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != reviewroute.ActionEscalate ||
		!contains(decision.ReasonCodes, "local_adversarial_incomplete") {
		t.Fatalf("decision = %s reasons %v", decision.Action, decision.ReasonCodes)
	}
}

func TestOnFindingsCoordinatorRequired(t *testing.T) {
	plan := routedPlan(t, "T1")
	input := cleanInput(plan, 1)
	input.CoordinatorComplete = false
	input.Findings = []reviewroute.FindingState{{
		ID: "codex-1", Severity: "low", Reviewers: []string{"codex"},
		Disposition: "deferred", DeferReason: "outside this PR",
	}}
	decision, err := Decide(plan, input, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != reviewroute.ActionContinue ||
		!contains(decision.ReasonCodes, "coordinator_incomplete") {
		t.Fatalf("decision = %s reasons %v", decision.Action, decision.ReasonCodes)
	}
}

func TestCycleCapParksWithoutTurningFailureIntoSuccess(t *testing.T) {
	plan := routedPlan(t, "T1")
	input := cleanInput(plan, 3)
	input.ChecksPassed = false
	decision, err := Decide(plan, input, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != reviewroute.ActionPark ||
		!contains(decision.ReasonCodes, "cycle_cap_reached") {
		t.Fatalf("decision = %s reasons %v", decision.Action, decision.ReasonCodes)
	}
}

func TestLateCyclesRequireT3AndRationale(t *testing.T) {
	for _, tt := range []struct {
		name      string
		tier      string
		rationale string
		action    string
	}{
		{name: "T2 cannot use cycle 4", tier: "T2", rationale: "finding open", action: "park"},
		{name: "T3 needs rationale", tier: "T3", action: "park"},
		{name: "T3 rationale continues", tier: "T3", rationale: "critical finding open", action: "continue"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan := routedPlan(t, tt.tier)
			input := cleanInput(plan, 4)
			input.ChecksPassed = false
			input.ContinuationRationale = tt.rationale
			decision, err := Decide(plan, input, testNow)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tt.action {
				t.Fatalf("action = %s reasons %v", decision.Action, decision.ReasonCodes)
			}
			if decision.ContinuationWeight != 8 || decision.CumulativeWeight != 15 {
				t.Fatalf("weights = %d/%d", decision.ContinuationWeight, decision.CumulativeWeight)
			}
		})
	}
}

func TestContinuationWeightsAreExponential(t *testing.T) {
	plan := routedPlan(t, "T3")
	for cycle := 1; cycle <= 8; cycle++ {
		input := cleanInput(plan, cycle)
		if cycle > 3 {
			input.ContinuationRationale = "weight trace"
		}
		decision, err := Decide(plan, input, testNow)
		if err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
		wantWeight := 1 << (cycle - 1)
		wantCumulative := 1<<cycle - 1
		if decision.ContinuationWeight != wantWeight ||
			decision.CumulativeWeight != wantCumulative {
			t.Fatalf("cycle %d weights = %d/%d, want %d/%d",
				cycle, decision.ContinuationWeight, decision.CumulativeWeight,
				wantWeight, wantCumulative)
		}
	}
}

func TestTierIncreaseEscalates(t *testing.T) {
	plan := routedPlan(t, "T1")
	input := cleanInput(plan, 1)
	input.CurrentTier = "T2"
	decision, err := Decide(plan, input, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != reviewroute.ActionEscalate ||
		!contains(decision.ReasonCodes, "tier_increased") {
		t.Fatalf("decision = %s reasons %v", decision.Action, decision.ReasonCodes)
	}
}

func TestIncompletePanelTargetsOnlyMissingRequiredReviewers(t *testing.T) {
	plan := routedPlan(t, "T2")
	input := cleanInput(plan, 1)
	input.PanelComplete = false
	input.CompletedReviewers = []string{"codex"}
	decision, err := Decide(plan, input, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != reviewroute.ActionContinue ||
		!equal(decision.NextReviewers, []string{"cursor"}) {
		t.Fatalf("decision = %s next %v reasons %v",
			decision.Action, decision.NextReviewers, decision.ReasonCodes)
	}
}

func TestPanelCompletionMustMatchRequiredReviewers(t *testing.T) {
	plan := routedPlan(t, "T2")
	input := cleanInput(plan, 1)
	input.CompletedReviewers = []string{"codex"}
	if _, err := Decide(plan, input, testNow); err == nil {
		t.Fatal("complete panel with a missing required reviewer unexpectedly accepted")
	}
	input.CompletedReviewers = []string{"codex", "cursor", "unknown"}
	if _, err := Decide(plan, input, testNow); err == nil {
		t.Fatal("unknown completed reviewer unexpectedly accepted")
	}
}

func TestExactHeadArtifactsCannotReplay(t *testing.T) {
	plan := routedPlan(t, "T1")
	input := cleanInput(plan, 1)
	input.Subject.HeadSHA = headB
	if _, err := Decide(plan, input, testNow); err == nil {
		t.Fatal("replayed evidence unexpectedly joined")
	}
}

func TestDecideRejectsRepoCaseMismatch(t *testing.T) {
	plan := routedPlan(t, "T1")
	input := cleanInput(plan, 1)
	input.Subject.Repo = "ITSHABIB/SHIP"
	if _, err := Decide(plan, input, testNow); err == nil {
		t.Fatal("case-mismatched repository unexpectedly joined")
	}
}

func routedPlan(t *testing.T, tier string) reviewroute.Plan {
	t.Helper()
	plan, err := Route(testPolicy(), subject(headA),
		reviewroute.Classification{Tier: tier}, testNow)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func cleanInput(plan reviewroute.Plan, cycle int) reviewroute.CycleInput {
	return reviewroute.CycleInput{
		SchemaVersion:       reviewroute.SchemaVersion,
		Subject:             plan.Subject,
		PlanID:              plan.PlanID,
		Cycle:               cycle,
		CurrentTier:         plan.Classification.Tier,
		ChecksPassed:        true,
		PanelComplete:       true,
		CompletedReviewers:  append([]string{}, plan.Required...),
		CoordinatorComplete: true,
		AdversarialComplete: true,
		Findings:            []reviewroute.FindingState{},
	}
}

func subject(head string) reviewroute.Subject {
	return reviewroute.Subject{Repo: "itsHabib/ship", Number: 7, HeadSHA: head}
}

func testPolicy() reviewroute.Policy {
	panel := []reviewroute.Reviewer{
		{Name: "codex", Trigger: "mention"},
		{Name: "claude", Trigger: "mention"},
		{Name: "cursor", Trigger: "mention"},
		{Name: "copilot", Trigger: "reviewer-request"},
	}
	return reviewroute.Policy{
		SchemaVersion:       reviewroute.SchemaVersion,
		ID:                  "tier-aware-canary",
		EnabledRepositories: []string{"itsHabib/ship"},
		FullPanel:           panel,
		Tiers: map[string]reviewroute.TierPolicy{
			"T0": tierPolicy(nil, nil, 1, "none", true, false, true),
			"T1": tierPolicy([]string{"codex"}, []string{"codex"}, 3, "on-findings", false, false, true),
			"T2": tierPolicy([]string{"codex", "cursor", "copilot"}, []string{"codex", "cursor"}, 3, "required", false, false, true),
			"T3": tierPolicy([]string{"codex", "claude", "cursor", "copilot"}, []string{"codex", "claude", "cursor"}, 8, "required", false, true, false),
		},
	}
}

func tierPolicy(
	reviewers, required []string,
	cycles int,
	coordinator string,
	local, adversarial, proof bool,
) reviewroute.TierPolicy {
	return reviewroute.TierPolicy{
		Reviewers: reviewers,
		Required:  required,
		MaxCycles: cycles,
		Requirements: reviewroute.Requirements{
			Coordinator:             coordinator,
			LocalAdversarial:        local,
			AdversarialVerification: adversarial,
			AllowProofSubstitution:  proof,
		},
	}
}

func equal(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
