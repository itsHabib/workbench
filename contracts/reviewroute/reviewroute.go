// Package reviewroute defines the engine-neutral, exact-head vocabulary for
// selecting and continuing pull-request review.
package reviewroute

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// SchemaVersion is the supported major version for all reviewroute artifacts.
	SchemaVersion = 1

	// ActionStop declares that the exact head has satisfied review.
	ActionStop = "stop"
	// ActionContinue declares that targeted review work remains below the cap.
	ActionContinue = "continue"
	// ActionAddress declares that accepted findings must be executed first.
	ActionAddress = "address"
	// ActionEscalate declares that a new plan or higher tier is required.
	ActionEscalate = "escalate"
	// ActionPark declares that deterministic policy cannot continue unattended.
	ActionPark = "park"
)

// Subject binds every review artifact to one pull request revision.
type Subject struct {
	Repo    string `json:"repo"`
	Number  int    `json:"number"`
	HeadSHA string `json:"head_sha"`
}

// Reviewer describes one configured review provider.
type Reviewer struct {
	Name    string `json:"name"`
	Trigger string `json:"trigger"`
}

// Requirements are the additional completion conditions for one tier.
type Requirements struct {
	Coordinator             string `json:"coordinator"`
	LocalAdversarial        bool   `json:"local_adversarial"`
	AdversarialVerification bool   `json:"adversarial_verification"`
	AllowProofSubstitution  bool   `json:"allow_proof_substitution"`
}

// TierPolicy is the configured initial route and hard cycle ceiling.
type TierPolicy struct {
	Reviewers    []string     `json:"reviewers"`
	Required     []string     `json:"required"`
	MaxCycles    int          `json:"max_cycles"`
	Requirements Requirements `json:"requirements"`
}

// Policy is one named, validated routing configuration.
type Policy struct {
	SchemaVersion       int                   `json:"schema_version"`
	ID                  string                `json:"id"`
	EnabledRepositories []string              `json:"enabled_repositories"`
	FullPanel           []Reviewer            `json:"full_panel"`
	Tiers               map[string]TierPolicy `json:"tiers"`
}

// PolicyRef records the named policy and the digest of its validated content.
// Callers never select or increment a separate policy revision.
type PolicyRef struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

// Classification records triage-floor output without importing its policy.
type Classification struct {
	Tier    string   `json:"tier"`
	Reasons []string `json:"reasons"`
}

// Plan is the exact-head reviewer route selected before any requests are made.
type Plan struct {
	SchemaVersion  int             `json:"schema_version"`
	PlanID         string          `json:"plan_id"`
	GeneratedAt    time.Time       `json:"generated_at"`
	Subject        Subject         `json:"subject"`
	Disposition    string          `json:"disposition"`
	Reason         string          `json:"reason,omitempty"`
	Policy         *PolicyRef      `json:"policy,omitempty"`
	Classification *Classification `json:"classification,omitempty"`
	Reviewers      []Reviewer      `json:"reviewers"`
	Required       []string        `json:"required"`
	MaxCycles      int             `json:"max_cycles"`
	Requirements   Requirements    `json:"requirements"`
}

// Request records one provider request made from an exact-head plan.
type Request struct {
	Reviewer string `json:"reviewer"`
	Trigger  string `json:"trigger"`
	Status   string `json:"status"`
	Ref      string `json:"ref,omitempty"`
}

// RequestReceipt records reviewer-triggering writes and their freshness check.
type RequestReceipt struct {
	SchemaVersion int       `json:"schema_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	Subject       Subject   `json:"subject"`
	PlanID        string    `json:"plan_id"`
	HeadBefore    string    `json:"head_before"`
	HeadAfter     string    `json:"head_after"`
	Status        string    `json:"status"`
	Reason        string    `json:"reason,omitempty"`
	Requests      []Request `json:"requests"`
}

// FindingState is the frontier agent's explicit disposition of one sourced
// finding. Reviewers contains only finding authors whose closure may be needed.
// Changed records whether the accepted fix has already changed the exact head;
// fixed + false still requires the address execution boundary.
type FindingState struct {
	ID             string   `json:"id"`
	Severity       string   `json:"severity"`
	Reviewers      []string `json:"reviewers"`
	Disposition    string   `json:"disposition"`
	Changed        bool     `json:"changed"`
	ReviewerClosed bool     `json:"reviewer_closed"`
	ProofRef       string   `json:"proof_ref,omitempty"`
	DeferReason    string   `json:"defer_reason,omitempty"`
	Debt           bool     `json:"debt"`
	FollowUpRef    string   `json:"follow_up_ref,omitempty"`
}

// CycleInput is the complete deterministic evidence presented to the decision
// policy for one cycle.
type CycleInput struct {
	SchemaVersion         int            `json:"schema_version"`
	Subject               Subject        `json:"subject"`
	PlanID                string         `json:"plan_id"`
	Cycle                 int            `json:"cycle"`
	CurrentTier           string         `json:"current_tier"`
	ChecksPassed          bool           `json:"checks_passed"`
	PanelComplete         bool           `json:"panel_complete"`
	CoordinatorComplete   bool           `json:"coordinator_complete"`
	AdversarialComplete   bool           `json:"adversarial_complete"`
	ContinuationRationale string         `json:"continuation_rationale,omitempty"`
	Findings              []FindingState `json:"findings"`
}

// Decision is the engine-neutral result consumed by either execution adapter.
type Decision struct {
	SchemaVersion      int            `json:"schema_version"`
	GeneratedAt        time.Time      `json:"generated_at"`
	Subject            Subject        `json:"subject"`
	PlanID             string         `json:"plan_id"`
	InputDigest        string         `json:"input_digest"`
	Policy             *PolicyRef     `json:"policy,omitempty"`
	RouteDisposition   string         `json:"route_disposition"`
	RouteReason        string         `json:"route_reason,omitempty"`
	Tier               string         `json:"tier,omitempty"`
	TierReasons        []string       `json:"tier_reasons,omitempty"`
	Cycle              int            `json:"cycle"`
	ContinuationWeight int            `json:"continuation_weight"`
	CumulativeWeight   int            `json:"cumulative_weight"`
	Action             string         `json:"action"`
	ReasonCodes        []string       `json:"reason_codes"`
	NextReviewers      []string       `json:"next_reviewers"`
	Findings           []FindingState `json:"findings"`
}

// ValidatePolicy enforces the complete reviewer roster and tier-map contract.
func ValidatePolicy(policy Policy) error {
	if policy.SchemaVersion != SchemaVersion {
		return fmt.Errorf("reviewroute: unsupported policy schema_version %d", policy.SchemaVersion)
	}
	if strings.TrimSpace(policy.ID) == "" {
		return errors.New("reviewroute: policy id is required")
	}
	if err := validateUnique("enabled repository", policy.EnabledRepositories); err != nil {
		return err
	}
	for _, repo := range policy.EnabledRepositories {
		parts := strings.Split(repo, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("reviewroute: enabled repository %s must be owner/repo", repo)
		}
	}
	if len(policy.FullPanel) == 0 {
		return errors.New("reviewroute: full_panel must not be empty")
	}
	panel := make(map[string]Reviewer, len(policy.FullPanel))
	for _, reviewer := range policy.FullPanel {
		if err := validateReviewer(reviewer); err != nil {
			return err
		}
		if _, exists := panel[reviewer.Name]; exists {
			return fmt.Errorf("reviewroute: duplicate full_panel reviewer %s", reviewer.Name)
		}
		panel[reviewer.Name] = reviewer
	}
	if len(policy.Tiers) != 4 {
		return errors.New("reviewroute: tiers must contain exactly T0, T1, T2, and T3")
	}
	for _, tier := range []string{"T0", "T1", "T2", "T3"} {
		value, ok := policy.Tiers[tier]
		if !ok {
			return fmt.Errorf("reviewroute: tiers is missing %s", tier)
		}
		if err := validateTierPolicy(tier, value, panel); err != nil {
			return err
		}
	}
	return nil
}

// PolicyDigest returns the immutable identity of validated policy content.
func PolicyDigest(policy Policy) (string, error) {
	if err := ValidatePolicy(policy); err != nil {
		return "", err
	}
	data, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("reviewroute: marshal policy identity: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateTierPolicy(tier string, policy TierPolicy, panel map[string]Reviewer) error {
	if policy.MaxCycles < 1 || policy.MaxCycles > 8 {
		return fmt.Errorf("reviewroute: %s max_cycles must be 1..8", tier)
	}
	if err := validateUnique(tier+" reviewer", policy.Reviewers); err != nil {
		return err
	}
	if err := validateUnique(tier+" required reviewer", policy.Required); err != nil {
		return err
	}
	selected := stringSet(policy.Reviewers)
	for _, reviewer := range policy.Reviewers {
		if _, ok := panel[reviewer]; !ok {
			return fmt.Errorf("reviewroute: %s selects unknown reviewer %s", tier, reviewer)
		}
	}
	for _, reviewer := range policy.Required {
		if _, ok := selected[reviewer]; !ok {
			return fmt.Errorf("reviewroute: %s requires unselected reviewer %s", tier, reviewer)
		}
	}
	switch policy.Requirements.Coordinator {
	case "none", "on-findings", "required":
	default:
		return fmt.Errorf("reviewroute: %s has invalid coordinator mode", tier)
	}
	return nil
}

// ValidatePlan enforces exact-head identity and a self-consistent route.
func ValidatePlan(plan Plan) error {
	if plan.SchemaVersion != SchemaVersion {
		return fmt.Errorf("reviewroute: unsupported plan schema_version %d", plan.SchemaVersion)
	}
	if err := validateSubject(plan.Subject); err != nil {
		return err
	}
	if plan.GeneratedAt.IsZero() {
		return errors.New("reviewroute: plan generated_at is required")
	}
	if plan.MaxCycles < 1 || plan.MaxCycles > 8 {
		return errors.New("reviewroute: plan max_cycles must be 1..8")
	}
	if err := validatePlanRoute(plan); err != nil {
		return err
	}
	if err := validatePlanReviewers(plan.Reviewers, plan.Required); err != nil {
		return err
	}
	if plan.PlanID == "" {
		return errors.New("reviewroute: plan_id is required")
	}
	id, err := PlanID(plan)
	if err != nil {
		return err
	}
	if plan.PlanID != id {
		return errors.New("reviewroute: plan_id does not match plan content")
	}
	return nil
}

func validatePlanRoute(plan Plan) error {
	if !validDisposition(plan.Disposition) {
		return errors.New("reviewroute: plan disposition is invalid")
	}
	if plan.Disposition != "tier_routed" && strings.TrimSpace(plan.Reason) == "" {
		return errors.New("reviewroute: non-routed plan requires a reason")
	}
	if plan.Disposition == "tier_routed" &&
		(plan.Policy == nil || plan.Classification == nil) {
		return errors.New("reviewroute: routed plan requires policy and classification")
	}
	if plan.Policy != nil {
		if strings.TrimSpace(plan.Policy.ID) == "" || !validDigest(plan.Policy.Digest) {
			return errors.New("reviewroute: plan policy requires id and sha256 digest")
		}
	}
	if plan.Classification != nil && !validTier(plan.Classification.Tier) {
		return errors.New("reviewroute: plan classification tier is invalid")
	}
	return nil
}

func validatePlanReviewers(reviewers []Reviewer, required []string) error {
	names := make([]string, 0, len(reviewers))
	for _, reviewer := range reviewers {
		if err := validateReviewer(reviewer); err != nil {
			return err
		}
		names = append(names, reviewer.Name)
	}
	if err := validateUnique("plan reviewer", names); err != nil {
		return err
	}
	if err := validateUnique("plan required reviewer", required); err != nil {
		return err
	}
	selected := stringSet(names)
	for _, reviewer := range required {
		if _, ok := selected[reviewer]; !ok {
			return fmt.Errorf("reviewroute: plan requires unselected reviewer %s", reviewer)
		}
	}
	return nil
}

// PlanID returns the content-derived identity for a plan. The plan_id field is
// excluded from its own preimage.
func PlanID(plan Plan) (string, error) {
	plan.PlanID = ""
	data, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("reviewroute: marshal plan identity: %w", err)
	}
	sum := sha256.Sum256(data)
	return "rp_" + hex.EncodeToString(sum[:16]), nil
}

// ValidateCycleInput enforces complete, explicit finding dispositions.
func ValidateCycleInput(input CycleInput) error {
	if input.SchemaVersion != SchemaVersion {
		return fmt.Errorf("reviewroute: unsupported cycle input schema_version %d", input.SchemaVersion)
	}
	if err := validateSubject(input.Subject); err != nil {
		return err
	}
	if strings.TrimSpace(input.PlanID) == "" || input.Cycle < 1 || input.Cycle > 8 {
		return errors.New("reviewroute: cycle input plan_id and cycle 1..8 are required")
	}
	if !validTier(input.CurrentTier) {
		return errors.New("reviewroute: cycle input current_tier is invalid")
	}
	ids := make([]string, 0, len(input.Findings))
	for _, finding := range input.Findings {
		ids = append(ids, finding.ID)
		if err := validateFindingState(finding); err != nil {
			return err
		}
	}
	return validateUnique("finding id", ids)
}

// ValidateRequestReceipt enforces the exact-head request join without deciding
// whether a failed request should be retried.
func ValidateRequestReceipt(receipt RequestReceipt) error {
	if receipt.SchemaVersion != SchemaVersion {
		return fmt.Errorf("reviewroute: unsupported request schema_version %d", receipt.SchemaVersion)
	}
	if err := validateSubject(receipt.Subject); err != nil {
		return err
	}
	if receipt.GeneratedAt.IsZero() || strings.TrimSpace(receipt.PlanID) == "" {
		return errors.New("reviewroute: request generated_at and plan_id are required")
	}
	if !validSHA(receipt.HeadBefore) || !validSHA(receipt.HeadAfter) {
		return errors.New("reviewroute: request heads must be 40 hexadecimal characters")
	}
	switch receipt.Status {
	case "requested", "stale", "failed":
	default:
		return errors.New("reviewroute: request status is invalid")
	}
	if receipt.Status != "stale" &&
		(!strings.EqualFold(receipt.HeadBefore, receipt.Subject.HeadSHA) ||
			!strings.EqualFold(receipt.HeadAfter, receipt.Subject.HeadSHA)) {
		return errors.New("reviewroute: non-stale request heads must match the subject")
	}
	if receipt.Status != "requested" && strings.TrimSpace(receipt.Reason) == "" {
		return errors.New("reviewroute: non-successful request requires a reason")
	}
	names := make([]string, 0, len(receipt.Requests))
	for _, request := range receipt.Requests {
		names = append(names, request.Reviewer)
		if err := validateReviewer(Reviewer{Name: request.Reviewer, Trigger: request.Trigger}); err != nil {
			return err
		}
		switch request.Status {
		case "requested", "observed", "failed":
		default:
			return fmt.Errorf("reviewroute: request for %s has invalid status", request.Reviewer)
		}
		if request.Status == "failed" && strings.TrimSpace(request.Ref) == "" {
			return fmt.Errorf("reviewroute: failed request for %s requires detail", request.Reviewer)
		}
	}
	return validateUnique("request reviewer", names)
}

func validateFindingState(finding FindingState) error {
	if strings.TrimSpace(finding.ID) == "" || strings.TrimSpace(finding.Severity) == "" {
		return errors.New("reviewroute: finding id and severity are required")
	}
	switch finding.Disposition {
	case "fixed", "proved_safe", "deferred", "unresolved":
	default:
		return fmt.Errorf("reviewroute: finding %s has invalid disposition", finding.ID)
	}
	if len(finding.Reviewers) == 0 {
		return fmt.Errorf("reviewroute: finding %s requires at least one reviewer", finding.ID)
	}
	if err := validateUnique("finding reviewer", finding.Reviewers); err != nil {
		return err
	}
	if finding.Disposition == "deferred" && strings.TrimSpace(finding.DeferReason) == "" {
		return fmt.Errorf("reviewroute: deferred finding %s requires a reason", finding.ID)
	}
	if finding.Disposition == "proved_safe" && !finding.ReviewerClosed &&
		strings.TrimSpace(finding.ProofRef) == "" {
		return fmt.Errorf(
			"reviewroute: proved_safe finding %s requires reviewer_closed or proof_ref",
			finding.ID,
		)
	}
	if finding.Debt && strings.TrimSpace(finding.FollowUpRef) == "" {
		return fmt.Errorf("reviewroute: debt finding %s requires a follow-up reference", finding.ID)
	}
	return nil
}

// ValidateDecision enforces deterministic weights and a valid terminal action.
func ValidateDecision(decision Decision) error {
	if decision.SchemaVersion != SchemaVersion {
		return fmt.Errorf("reviewroute: unsupported decision schema_version %d", decision.SchemaVersion)
	}
	if err := validateDecisionIdentity(decision); err != nil {
		return err
	}
	if err := validateDecisionRoute(decision); err != nil {
		return err
	}
	if err := validateDecisionCycle(decision); err != nil {
		return err
	}
	if err := validateDecisionOutcome(decision); err != nil {
		return err
	}
	ids := make([]string, 0, len(decision.Findings))
	for _, finding := range decision.Findings {
		ids = append(ids, finding.ID)
		if err := validateFindingState(finding); err != nil {
			return err
		}
	}
	return validateUnique("decision finding id", ids)
}

func validateDecisionIdentity(decision Decision) error {
	if err := validateSubject(decision.Subject); err != nil {
		return err
	}
	if decision.GeneratedAt.IsZero() || decision.PlanID == "" {
		return errors.New("reviewroute: decision generated_at and plan_id are required")
	}
	if !validDigest(decision.InputDigest) {
		return errors.New("reviewroute: decision input_digest must be sha256 content identity")
	}
	return nil
}

func validateDecisionRoute(decision Decision) error {
	if !validDisposition(decision.RouteDisposition) {
		return errors.New("reviewroute: decision route_disposition is invalid")
	}
	if decision.RouteDisposition != "tier_routed" && strings.TrimSpace(decision.RouteReason) == "" {
		return errors.New("reviewroute: non-routed decision requires a route reason")
	}
	if decision.Policy != nil {
		if strings.TrimSpace(decision.Policy.ID) == "" || !validDigest(decision.Policy.Digest) {
			return errors.New("reviewroute: decision policy requires id and sha256 digest")
		}
	}
	if decision.Tier != "" && !validTier(decision.Tier) {
		return errors.New("reviewroute: decision tier is invalid")
	}
	if decision.RouteDisposition == "tier_routed" &&
		(decision.Policy == nil || decision.Tier == "") {
		return errors.New("reviewroute: routed decision requires policy and tier")
	}
	if err := validateUnique("decision tier reason", decision.TierReasons); err != nil {
		return err
	}
	return nil
}

func validateDecisionCycle(decision Decision) error {
	if decision.Cycle < 1 || decision.Cycle > 8 {
		return errors.New("reviewroute: decision cycle must be 1..8")
	}
	if decision.ContinuationWeight != 1<<(decision.Cycle-1) ||
		decision.CumulativeWeight != (1<<decision.Cycle)-1 {
		return errors.New("reviewroute: decision continuation weights are inconsistent")
	}
	return nil
}

func validateDecisionOutcome(decision Decision) error {
	switch decision.Action {
	case ActionStop, ActionContinue, ActionAddress, ActionEscalate, ActionPark:
	default:
		return errors.New("reviewroute: decision action is invalid")
	}
	if len(decision.ReasonCodes) == 0 {
		return errors.New("reviewroute: decision requires at least one reason code")
	}
	if err := validateUnique("decision reason code", decision.ReasonCodes); err != nil {
		return err
	}
	if err := validateUnique("next reviewer", decision.NextReviewers); err != nil {
		return err
	}
	return nil
}

// CycleInputDigest returns the immutable identity of validated cycle evidence.
func CycleInputDigest(input CycleInput) (string, error) {
	if err := ValidateCycleInput(input); err != nil {
		return "", err
	}
	data, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("reviewroute: marshal cycle input identity: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateSubject(subject Subject) error {
	parts := strings.Split(subject.Repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return errors.New("reviewroute: subject repo must be owner/repo")
	}
	if subject.Number < 1 {
		return errors.New("reviewroute: subject number must be positive")
	}
	if !validSHA(subject.HeadSHA) {
		return errors.New("reviewroute: subject head_sha must be 40 hexadecimal characters")
	}
	return nil
}

func validSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") {
		return false
	}
	return validLowerHex(value[len("sha256:"):], 64)
}

func validLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, char := range value {
		if char >= '0' && char <= '9' {
			continue
		}
		if char >= 'a' && char <= 'f' {
			continue
		}
		return false
	}
	return true
}

func validateReviewer(reviewer Reviewer) error {
	if strings.TrimSpace(reviewer.Name) == "" {
		return errors.New("reviewroute: reviewer name is required")
	}
	switch reviewer.Trigger {
	case "mention", "reviewer-request", "auto":
		return nil
	}
	return fmt.Errorf("reviewroute: reviewer %s has invalid trigger", reviewer.Name)
}

func validateUnique(name string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("reviewroute: %s must not be blank", name)
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("reviewroute: %s %s is duplicated", name, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validTier(tier string) bool {
	switch tier {
	case "T0", "T1", "T2", "T3":
		return true
	}
	return false
}

func validDisposition(disposition string) bool {
	switch disposition {
	case "tier_routed", "deliberately_overridden", "full_panel_fallback", "parked_unverified":
		return true
	}
	return false
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}
