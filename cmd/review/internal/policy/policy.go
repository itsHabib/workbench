// Package policy owns review's deterministic routing and continuation rules.
package policy

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/itsHabib/workbench/contracts/reviewroute"
)

var safePanel = []reviewroute.Reviewer{
	{Name: "codex", Trigger: "mention"},
	{Name: "claude", Trigger: "mention"},
	{Name: "cursor", Trigger: "mention"},
	{Name: "copilot", Trigger: "reviewer-request"},
}

// Route selects the configured tier policy or the complete safe panel when the
// repository is not explicitly enabled.
func Route(
	config reviewroute.Policy,
	subject reviewroute.Subject,
	classification reviewroute.Classification,
	now time.Time,
) (reviewroute.Plan, error) {
	if err := reviewroute.ValidatePolicy(config); err != nil {
		return reviewroute.Plan{}, err
	}
	digest, err := reviewroute.PolicyDigest(config)
	if err != nil {
		return reviewroute.Plan{}, err
	}
	if !enabled(config.EnabledRepositories, subject.Repo) {
		return fallback(config.FullPanel, subject, &classification,
			&reviewroute.PolicyRef{ID: config.ID, Digest: digest}, now,
			"full_panel_fallback", "repository is not explicitly enabled")
	}
	classification.Reasons = append([]string{}, classification.Reasons...)
	tier := config.Tiers[classification.Tier]
	reviewers, err := selectReviewers(config.FullPanel, tier.Reviewers)
	if err != nil {
		return reviewroute.Plan{}, err
	}
	plan := reviewroute.Plan{
		SchemaVersion:  reviewroute.SchemaVersion,
		GeneratedAt:    now.UTC(),
		Subject:        subject,
		Disposition:    "tier_routed",
		Policy:         &reviewroute.PolicyRef{ID: config.ID, Digest: digest},
		Classification: &classification,
		Reviewers:      reviewers,
		Required:       append([]string{}, tier.Required...),
		MaxCycles:      tier.MaxCycles,
		Requirements:   tier.Requirements,
	}
	return finalizePlan(plan)
}

// Fallback returns the complete hard-coded safe panel. It is used when policy
// bytes cannot be trusted enough to recover their configured full panel.
func Fallback(
	subject reviewroute.Subject,
	classification *reviewroute.Classification,
	now time.Time,
	disposition, reason string,
) (reviewroute.Plan, error) {
	return fallback(safePanel, subject, classification, nil, now, disposition, reason)
}

func fallback(
	panel []reviewroute.Reviewer,
	subject reviewroute.Subject,
	classification *reviewroute.Classification,
	policyRef *reviewroute.PolicyRef,
	now time.Time,
	disposition, reason string,
) (reviewroute.Plan, error) {
	if classification != nil {
		classificationCopy := *classification
		classificationCopy.Reasons = append([]string{}, classification.Reasons...)
		classification = &classificationCopy
	}
	plan := reviewroute.Plan{
		SchemaVersion:  reviewroute.SchemaVersion,
		GeneratedAt:    now.UTC(),
		Subject:        subject,
		Disposition:    disposition,
		Reason:         reason,
		Policy:         policyRef,
		Classification: classification,
		Reviewers:      append([]reviewroute.Reviewer(nil), panel...),
		Required:       reviewerNames(panel),
		MaxCycles:      3,
		Requirements: reviewroute.Requirements{
			Coordinator: "required",
		},
	}
	return finalizePlan(plan)
}

func finalizePlan(plan reviewroute.Plan) (reviewroute.Plan, error) {
	id, err := reviewroute.PlanID(plan)
	if err != nil {
		return reviewroute.Plan{}, err
	}
	plan.PlanID = id
	if err := reviewroute.ValidatePlan(plan); err != nil {
		return reviewroute.Plan{}, err
	}
	return plan, nil
}

// Decide applies the deterministic early-stop and targeted-continuation rule.
func Decide(plan reviewroute.Plan, input reviewroute.CycleInput, now time.Time) (reviewroute.Decision, error) {
	if err := reviewroute.ValidatePlan(plan); err != nil {
		return reviewroute.Decision{}, fmt.Errorf("review plan: %w", err)
	}
	if err := reviewroute.ValidateCycleInput(input); err != nil {
		return reviewroute.Decision{}, fmt.Errorf("cycle input: %w", err)
	}
	if plan.PlanID != input.PlanID || plan.Subject != input.Subject {
		return reviewroute.Decision{}, errors.New("review artifacts do not join at the exact plan and head")
	}
	if err := validatePanelCompletion(plan, input); err != nil {
		return reviewroute.Decision{}, err
	}
	inputDigest, err := reviewroute.CycleInputDigest(input)
	if err != nil {
		return reviewroute.Decision{}, err
	}

	reasons, next := blockers(plan, input)
	action := reviewroute.ActionContinue
	if len(reasons) == 0 {
		action = reviewroute.ActionStop
		reasons = []string{"stop_conditions_satisfied"}
		if proofSubstitutesPanel(plan, input) {
			reasons = append(reasons, "deterministic_proof_substitution")
		}
	}
	if raisedTier(plan, input.CurrentTier) {
		action = reviewroute.ActionEscalate
		reasons = append(reasons, "tier_increased")
	}
	// A late cycle without its T3 rationale parks before any further action,
	// including a raised-tier escalation; the tier reason remains recorded.
	if input.Cycle > 3 && (input.CurrentTier != "T3" || strings.TrimSpace(input.ContinuationRationale) == "") {
		action = reviewroute.ActionPark
		reasons = append(reasons, "late_cycle_requires_t3_rationale")
	}
	if action == reviewroute.ActionContinue && input.CurrentTier == "T0" {
		action = reviewroute.ActionEscalate
		reasons = append(reasons, "t0_uncertainty_requires_reclassification")
	}
	if action == reviewroute.ActionContinue && input.Cycle >= plan.MaxCycles {
		action = reviewroute.ActionPark
		reasons = append(reasons, "cycle_cap_reached")
	}
	action, reasons = addressAction(action, input.Findings, reasons)

	decision := reviewroute.Decision{
		SchemaVersion:       reviewroute.SchemaVersion,
		GeneratedAt:         now.UTC(),
		Subject:             input.Subject,
		PlanID:              input.PlanID,
		InputDigest:         inputDigest,
		Policy:              plan.Policy,
		RouteDisposition:    plan.Disposition,
		RouteReason:         plan.Reason,
		Tier:                input.CurrentTier,
		Cycle:               input.Cycle,
		ContinuationWeight:  1 << (input.Cycle - 1),
		CumulativeWeight:    (1 << input.Cycle) - 1,
		Action:              action,
		ReasonCodes:         sortedUnique(reasons),
		NextReviewers:       sortedUnique(next),
		AdversarialEvidence: cloneAdversarialEvidence(input.AdversarialEvidence),
		Findings:            append([]reviewroute.FindingState{}, input.Findings...),
	}
	if plan.Classification != nil {
		decision.TierReasons = append([]string{}, plan.Classification.Reasons...)
	}
	if err := reviewroute.ValidateDecision(decision); err != nil {
		return reviewroute.Decision{}, err
	}
	return decision, nil
}

func blockers(plan reviewroute.Plan, input reviewroute.CycleInput) ([]string, []string) {
	var reasons []string
	var reviewers []string
	proofOnly := proofSubstitutesPanel(plan, input)
	if !input.ChecksPassed {
		reasons = append(reasons, "checks_failed")
	}
	if !input.PanelComplete && !proofOnly {
		reasons = append(reasons, "panel_incomplete")
		reviewers = append(reviewers, missingRequiredReviewers(plan, input)...)
	}
	if coordinatorRequired(plan, input) && !input.CoordinatorComplete && !proofOnly {
		reasons = append(reasons, "coordinator_incomplete")
	}
	if plan.Requirements.LocalAdversarial && !localAdversarialComplete(input) {
		reasons = append(reasons, "local_adversarial_incomplete")
	}
	if plan.Requirements.AdversarialVerification && !input.AdversarialComplete {
		reasons = append(reasons, "adversarial_incomplete")
	}
	for _, finding := range input.Findings {
		findingReasons, needsReview := findingBlockers(plan, input.CurrentTier, finding)
		reasons = append(reasons, findingReasons...)
		if needsReview {
			reviewers = append(reviewers, finding.Reviewers...)
		}
	}
	return sortedUnique(reasons), sortedUnique(reviewers)
}

func localAdversarialComplete(input reviewroute.CycleInput) bool {
	return input.AdversarialComplete &&
		input.AdversarialEvidence != nil &&
		input.AdversarialEvidence.Source == "local"
}

func cloneAdversarialEvidence(
	evidence *reviewroute.AdversarialEvidence,
) *reviewroute.AdversarialEvidence {
	if evidence == nil {
		return nil
	}
	cloned := *evidence
	return &cloned
}

func validatePanelCompletion(plan reviewroute.Plan, input reviewroute.CycleInput) error {
	allowed := make(map[string]struct{}, len(plan.Reviewers))
	for _, reviewer := range plan.Reviewers {
		allowed[reviewer.Name] = struct{}{}
	}
	for _, reviewer := range input.CompletedReviewers {
		if _, ok := allowed[reviewer]; !ok {
			return fmt.Errorf("completed reviewer %s is not in the review plan", reviewer)
		}
	}
	missing := missingRequiredReviewers(plan, input)
	if input.PanelComplete && len(missing) > 0 {
		return errors.New("panel_complete is true with required reviewers missing")
	}
	if !input.PanelComplete && len(missing) == 0 {
		return errors.New("panel_complete is false with no required reviewers missing")
	}
	return nil
}

func missingRequiredReviewers(plan reviewroute.Plan, input reviewroute.CycleInput) []string {
	completed := make(map[string]struct{}, len(input.CompletedReviewers))
	for _, reviewer := range input.CompletedReviewers {
		completed[reviewer] = struct{}{}
	}
	var missing []string
	for _, reviewer := range plan.Required {
		if _, ok := completed[reviewer]; ok {
			continue
		}
		missing = append(missing, reviewer)
	}
	return missing
}

func coordinatorRequired(plan reviewroute.Plan, input reviewroute.CycleInput) bool {
	if plan.Requirements.Coordinator == "required" {
		return true
	}
	return plan.Requirements.Coordinator == "on-findings" && len(input.Findings) > 0
}

func proofSubstitutesPanel(plan reviewroute.Plan, input reviewroute.CycleInput) bool {
	// Proof substitution closes a rereview only after an earlier reviewer cycle
	// established the finding; it cannot replace the initial review baseline.
	if input.Cycle < 2 || !plan.Requirements.AllowProofSubstitution || len(input.Findings) == 0 {
		return false
	}
	for _, finding := range input.Findings {
		if finding.Disposition == "unresolved" {
			return false
		}
		if finding.Disposition == "deferred" || finding.ReviewerClosed {
			continue
		}
		if !proofCloses(plan, input.CurrentTier, criticalSeverity(finding.Severity), finding.ProofRef) {
			return false
		}
	}
	return true
}

func findingBlockers(
	plan reviewroute.Plan,
	tier string,
	finding reviewroute.FindingState,
) ([]string, bool) {
	critical := criticalSeverity(finding.Severity)
	if finding.Disposition == "unresolved" {
		if critical {
			return []string{"critical_unresolved"}, true
		}
		return []string{"finding_unresolved"}, true
	}
	if finding.Disposition == "deferred" {
		if critical {
			return []string{"critical_deferred"}, true
		}
		return nil, false
	}
	if finding.ReviewerClosed {
		return nil, false
	}
	if proofCloses(plan, tier, critical, finding.ProofRef) {
		return nil, false
	}
	if critical {
		return []string{"critical_reviewer_closure_missing"}, true
	}
	return []string{"reviewer_closure_missing"}, true
}

func needsAddress(findings []reviewroute.FindingState, reasons []string) bool {
	if !onlyClosureBlockers(reasons) {
		return false
	}
	for _, finding := range findings {
		if finding.Disposition == "fixed" && !finding.Changed && !finding.ReviewerClosed {
			return true
		}
	}
	return false
}

func addressAction(
	action string,
	findings []reviewroute.FindingState,
	reasons []string,
) (string, []string) {
	if action != reviewroute.ActionContinue || !needsAddress(findings, reasons) {
		return action, reasons
	}
	return reviewroute.ActionAddress, append(reasons, "accepted_findings_require_address")
}

func onlyClosureBlockers(reasons []string) bool {
	if len(reasons) == 0 {
		return false
	}
	for _, reason := range reasons {
		if reason == "reviewer_closure_missing" || reason == "critical_reviewer_closure_missing" {
			continue
		}
		return false
	}
	return true
}

func proofCloses(plan reviewroute.Plan, tier string, critical bool, proofRef string) bool {
	if critical || !plan.Requirements.AllowProofSubstitution || strings.TrimSpace(proofRef) == "" {
		return false
	}
	// T3 always requires reviewer closure, even if a custom policy mistakenly
	// enables proof substitution for the tier.
	return tier == "T0" || tier == "T1" || tier == "T2"
}

func criticalSeverity(severity string) bool {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical", "p0", "blocking":
		return true
	}
	return false
}

func raisedTier(plan reviewroute.Plan, current string) bool {
	if plan.Classification == nil {
		return false
	}
	return tierRank(current) > tierRank(plan.Classification.Tier)
}

func tierRank(tier string) int {
	switch tier {
	case "T0":
		return 0
	case "T1":
		return 1
	case "T2":
		return 2
	case "T3":
		return 3
	}
	return 4
}

func enabled(repositories []string, repo string) bool {
	for _, candidate := range repositories {
		if strings.EqualFold(candidate, repo) {
			return true
		}
	}
	return false
}

func selectReviewers(panel []reviewroute.Reviewer, selected []string) ([]reviewroute.Reviewer, error) {
	byName := make(map[string]reviewroute.Reviewer, len(panel))
	for _, reviewer := range panel {
		byName[reviewer.Name] = reviewer
	}
	out := make([]reviewroute.Reviewer, 0, len(selected))
	for _, name := range selected {
		reviewer, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("review policy selects unknown reviewer %s", name)
		}
		out = append(out, reviewer)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func reviewerNames(reviewers []reviewroute.Reviewer) []string {
	names := make([]string, 0, len(reviewers))
	for _, reviewer := range reviewers {
		names = append(names, reviewer.Name)
	}
	return sortedUnique(names)
}

func sortedUnique(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
