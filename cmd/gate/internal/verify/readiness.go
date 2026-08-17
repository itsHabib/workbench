package verify

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// gateContext is the commit-status context gate posts its own verdict under
// (see .github/workflows/gate.yml "Post gate status", -f context=gate). Once
// gate is an enforced required check, a prior failure/error gate status on the
// same head is itself a non-green rollup entry — so readiness must skip it, or
// gate would block on its own past verdict and a red gate could only clear on a
// new push (a self-deadlock). Every OTHER non-green check still blocks, so
// fail-closed is unchanged.
const gateContext = "gate"

// authorizedContext is gate's OWN provenance stamp — the success commit status
// the binary posts on a pass (see internal/stamp, stamp.Context). Like
// gateContext it is gate's prior verdict on this head, not independent CI, so
// readiness must exclude it from both the block loop and the effective-CI count.
// Otherwise a judge/resolve pass that stamps a head with no real CI would let a
// LATER run read that stamp as a recorded check and skip the empty-CI escalation
// — the stamp manufacturing the very signal that escalation exists to catch.
// TestReadinessAuthorizedContextMatchesStamp pins this equal to stamp.Context so
// the two can never drift.
const authorizedContext = "gate/authorized"

// isOwnGateStatus reports whether a rollup entry is one of gate's OWN posted
// statuses: a commit *status* whose context is gate's verdict context or its
// authorization-stamp context. gate posts commit statuses
// (`gh api .../statuses -f context=gate` / `context=gate/authorized`), which
// surface in the rollup as StatusContexts — Context set, Name empty. The match
// is deliberately narrow: a check-RUN literally named "gate" (Name set) is NOT
// gate's status and still blocks, so an unrelated Actions job named "gate" can
// never be silently dropped. An unrelated context like "gate-foo" is not gate's
// either.
func isOwnGateStatus(c rollupCheck) bool {
	return c.Name == "" && (c.Context == gateContext || c.Context == authorizedContext)
}

type prView struct {
	State             string        `json:"state"`
	IsDraft           bool          `json:"isDraft"`
	Mergeable         string        `json:"mergeable"`
	ReviewDecision    string        `json:"reviewDecision"`
	HeadRefOid        string        `json:"headRefOid"`
	StatusCheckRollup []rollupCheck `json:"statusCheckRollup"`
	Author            struct {
		Login string `json:"login"`
	} `json:"author"`
	MergeCommit struct {
		Oid string `json:"oid"`
	} `json:"mergeCommit"`
}

// reviewStance mirrors the evidence package's ReviewStance on the JSON seam —
// evidence and verify share artifacts, never imports (see the repo charter).
type reviewStance struct {
	Author      string `json:"author"`
	IsBot       bool   `json:"is_bot"`
	Association string `json:"association"`
	Permission  string `json:"permission"`
	State       string `json:"state"`
	CommitID    string `json:"commit_id"`
}

// authoritativePermissions are the effective repository permissions that carry
// authority over what may merge. On a PUBLIC repository — which this one is —
// any GitHub user may submit an APPROVED review, so "not a bot" is no trust
// signal at all.
//
// author_association is NOT sufficient here and an earlier revision wrongly used
// it: an organization member with base read access still reports MEMBER, and an
// outside collaborator with read or triage access still reports COLLABORATOR,
// and read access is enough to approve. Only the effective permission answers
// "may this account decide what merges".
//
// GitHub's legacy permission field already collapses maintain into write and
// triage into read, which is exactly the line that matters. The set is a closed
// allow-list: read, none, empty (lookup failed), and any value this code does
// not know are all unauthorized. Fail closed.
var authoritativePermissions = map[string]bool{
	"admin": true,
	"write": true,
}

// approvalStateApproved is the review submission state that counts. Anything
// else — COMMENTED, CHANGES_REQUESTED, or a state this code does not know — is
// not an approval. Fail closed.
const approvalStateApproved = "APPROVED"

// approvalStateChangesRequested is an outstanding human objection. It is not
// merely "not an approval": it must suppress someone else's approval too.
const approvalStateChangesRequested = "CHANGES_REQUESTED"

// humanApprovalAtHead reports the login of a human who has APPROVED this exact
// head with no human change request outstanding, if one exists.
//
// This is the readiness signal GitHub's reviewDecision is meant to carry and
// does not: GitHub reports reviewDecision as null whenever branch protection
// does not REQUIRE reviews, whether or not anyone approved. On a repository
// that requires none — every portfolio repo — an operator's explicit approval
// was therefore invisible, and readiness escalated on every PR forever.
//
// Conditions, each load-bearing:
//
//   - the approver holds write or admin permission (authoritativePermissions).
//     "Not a bot" is not trust: on a public repo anyone may review, and they
//     all report as user.type "User". Nor is author_association — read-access
//     members report MEMBER and can approve.
//
//   - no human's CURRENT stance is CHANGES_REQUESTED (humanChangeRequest). On
//     a protected repo GitHub would fold this into reviewDecision and
//     readinessBlocks would catch it — but this path exists precisely because
//     reviewDecision is null, so nothing else would. One human approving does
//     not clear another's outstanding objection, at any head: a change request
//     stands until its author withdraws or supersedes it, and the safe answer
//     to "approved by one, objected by another" is to escalate, not to pick.
//
//     Note the deliberate ASYMMETRY: approving requires repository authority,
//     objecting does not. Any human's change request suppresses the approval
//     path, including a drive-by account on a public repo. That is the
//     cheaper mistake in both directions. An unauthorized objection costs an
//     escalation — the pre-existing behavior, recoverable by dismissing the
//     review on GitHub. An unauthorized objection IGNORED could merge over a
//     real defect that an outside reviewer was first to spot. Escalating is
//     never worse than the status quo; silently discarding an objection can
//     be. Widen this only with a reason.
//
//   - the stance is APPROVED (not merely submitted)
//
//   - it was submitted against headSHA — a new commit is new code, and an
//     approval of earlier code is not an approval of this one
//
//   - the approver is a human other than the PR author
//
// Bots are excluded deliberately. A bot panel is findings, and findings are not
// authorization; panel completeness is a separate verifier with its own rung.
func humanApprovalAtHead(stances []reviewStance, headSHA, prAuthor string) (string, bool) {
	if headSHA == "" {
		return "", false
	}
	if _, objected := humanChangeRequest(stances); objected {
		return "", false
	}
	for _, s := range stances {
		if s.IsBot || s.State != approvalStateApproved || s.CommitID != headSHA {
			continue
		}
		if s.Author == "" || s.Author == prAuthor {
			continue
		}
		if !authoritativePermissions[s.Permission] {
			continue
		}
		return s.Author, true
	}
	return "", false
}

// humanChangeRequest names a human whose current stance asks for changes, if one
// exists. Deliberately head-agnostic and permission-agnostic: a change request
// stands until its author withdraws or supersedes it, and objecting needs no
// repository authority when approving does — humanApprovalAtHead spells out why
// that asymmetry is the cheaper mistake in both directions.
//
// It is the single definition of "a human is asking for changes", shared by the
// approval path and the review-panel stand-in, so neither can start disagreeing
// with the other about whether an objection is outstanding.
func humanChangeRequest(stances []reviewStance) (string, bool) {
	for _, s := range stances {
		if !s.IsBot && s.State == approvalStateChangesRequested {
			return s.Author, true
		}
	}
	return "", false
}

// Readiness is the deterministic gh read-back: draft state, CI rollup, and
// mergeability. Producer class: code — its blocks are final; no judgment can
// talk a red check green.
// reviewsOptional, when true, tells readiness that an ABSENT GitHub review
// decision is acceptable — do not escalate on it. This is for the enforced-check
// context (gate.yml as a required status check): the gate run is itself the
// review gate (its review-consolidation rung consolidates the bot panel), and
// the repo deliberately requires no separate GitHub review, so GitHub reports an
// empty reviewDecision by design. Default false preserves the driver/judge
// behavior, where an empty decision escalates for a human to confirm. It only
// suppresses the ABSENCE escalation; an explicit non-APPROVED decision
// (CHANGES_REQUESTED, REVIEW_REQUIRED) still blocks below.
// stancesEvidenceID names the review-stance artifact (each reviewer's current
// position and the commit they took it against). A human APPROVED at the exact
// head satisfies the absent-reviewDecision escalation — see humanApprovalAtHead.
// panelEvidenceID names the ReviewPanelV1 artifact; a complete exact-head panel
// satisfies the same escalation — see panelStandIn. Either id may be empty, and
// a caller supplying neither behaves exactly as it did before they existed.
func Readiness(st *state.Store, run, viewEvidenceID, stancesEvidenceID, panelEvidenceID string, subject Subject, reviewsOptional bool) (state.Artifact, Subject, error) {
	a, err := st.Get(viewEvidenceID)
	if err != nil {
		return state.Artifact{}, subject, err
	}
	var body struct {
		Data prView `json:"data"`
	}
	if err := json.Unmarshal(a.Body, &body); err != nil {
		return state.Artifact{}, subject, fmt.Errorf("verify: parse view evidence: %w", err)
	}
	pv := body.Data
	subject.HeadSHA = pv.HeadRefOid

	stances, err := readStances(st, stancesEvidenceID)
	if err != nil {
		return state.Artifact{}, subject, err
	}
	stand, err := resolveStandIn(st, panelEvidenceID, subject, pv, stances, reviewsOptional)
	if err != nil {
		return state.Artifact{}, subject, err
	}

	// A verdict's parents name the evidence it judged (the artifact contract).
	// Stance and panel evidence are judged whenever they were supplied — not
	// only when they changed the outcome — so provenance traversal can reach
	// what readiness actually read, without inferring it from the verdict text.
	parents := []string{viewEvidenceID}
	if stancesEvidenceID != "" {
		parents = append(parents, stancesEvidenceID)
	}
	if panelEvidenceID != "" {
		parents = append(parents, panelEvidenceID)
	}

	v := Verdict{
		Subject:    subject,
		Source:     "readiness",
		Producer:   Producer{Class: ClassCode, Impl: "gh-readback"},
		Decision:   DecisionPass,
		Tier:       "T0",
		Confidence: 1.0,
	}
	blocks, effectiveChecks := readinessBlocks(pv)
	if len(blocks) > 0 {
		v.Decision = DecisionBlock
		v.Why = fmt.Sprint(blocks)
		for _, b := range blocks {
			v.Findings = append(v.Findings, Finding{Title: b, Severity: "block"})
		}
		art, err := Record(st, run, parents, v)
		return art, subject, err
	}
	// Absence of signal must not read as green. An empty rollup means CI
	// never ran; an empty reviewDecision means GitHub reported no live merge
	// policy — on an unprotected repo that would otherwise pass silently.
	// Escalate rather than block: a judge may know the repo genuinely has no
	// CI or requires no reviews. If both hold, one escalate naming the
	// reasons is enough. The reviewDecision branch is MERGED-exempt (a
	// backtested PR has no live review signal), mirroring the block checks
	// above; the empty-CI branch is not, matching its long-standing behavior.
	var escalations []string
	if effectiveChecks == 0 {
		escalations = append(escalations, "no non-gate CI checks recorded for this head")
	}
	// GitHub reported no aggregate review decision. An explicit one has already
	// been handled — APPROVED fell through the block loop, anything else blocked
	// there — and a merged subject has no live one, so this is exactly the
	// ABSENCE resolveStandIn answers.
	decisionAbsent := pv.State != "MERGED" && pv.ReviewDecision == ""
	if decisionAbsent && !stand.satisfied {
		escalations = append(escalations, standInEscalation(stand))
	}
	if len(escalations) > 0 {
		v.Decision = DecisionEscalate
		v.Why = strings.Join(escalations, "; ") + " — cannot verify readiness"
		art, err := Record(st, run, parents, v)
		return art, subject, err
	}
	v.Why = fmt.Sprintf("state=%s draft=%v mergeable=%s checks=%d green",
		pv.State, pv.IsDraft, pv.Mergeable, effectiveChecks)
	// Persist what actually carried the decision, or the audit log cannot tell
	// an intentional acceptance from readiness wrongly passing an unverified PR
	// (gate's decisions must be reconstructable from state alone).
	if decisionAbsent && stand.satisfied {
		v.Why += "; " + stand.why
	}
	art, err := Record(st, run, parents, v)
	return art, subject, err
}

// reviewStandIn is what stood in for an ABSENT GitHub aggregate review decision.
//
// why is the sentence the verdict records: the reason readiness held when
// satisfied, and otherwise the reason the nearest candidate was REJECTED — an
// escalation that says only "no review decision reported by GitHub" when a
// complete panel was sitting right there sends the reader hunting for a
// contradiction that is really a deliberate refusal.
type reviewStandIn struct {
	satisfied bool
	why       string
}

// standInEscalation is the absent-decision escalation line, naming the rejected
// near-miss when there was one. The leading clause is unchanged, so callers that
// match on it keep working.
func standInEscalation(stand reviewStandIn) string {
	const base = "no review decision reported by GitHub"
	if stand.why == "" {
		return base
	}
	return base + " (" + stand.why + ")"
}

// resolveStandIn reports what may stand in for an absent GitHub review decision,
// strongest evidence first:
//
//  1. a human's APPROVED review of the exact head — the decision GitHub declined
//     to report, stated directly by someone with authority to state it;
//  2. a complete exact-head reviewer panel (panelStandIn);
//  3. the reviews-optional policy flag, which asserts no separate GitHub review
//     is wanted here rather than showing evidence that one happened.
//
// Ordering is what the audit log records, not what it permits — any one of the
// three satisfies readiness. Facts are recorded in preference to the policy
// flag, so a verdict names the approval or the panel that actually carried it.
//
// It runs whether or not a decision is absent, so the verdict's parents can
// truthfully name every artifact readiness read; the CALLER applies the result
// only when the decision really is absent.
func resolveStandIn(
	st *state.Store,
	panelEvidenceID string,
	subject Subject,
	pv prView,
	stances []reviewStance,
	reviewsOptional bool,
) (reviewStandIn, error) {
	panel, err := panelStandIn(st, panelEvidenceID, subject, stances)
	if err != nil {
		return reviewStandIn{}, err
	}
	if approver, ok := humanApprovalAtHead(stances, pv.HeadRefOid, pv.Author.Login); ok {
		return reviewStandIn{
			satisfied: true,
			why: fmt.Sprintf("approved at head by %s (no GitHub review decision reported)%s",
				approver, objectionNote(panel)),
		}, nil
	}
	// reviews-optional is a policy assertion, not evidence, so it is consulted
	// last — but it is unconditional where it applies. A panel this run rejected
	// does not narrow it: suppressing the absence escalation is this flag's
	// entire job, and letting panel evidence veto it would change the
	// enforced-check context, which is not what this path is for.
	// TestReadinessReviewsOptionalIsNotNarrowedByPanelObjection pins that.
	if !panel.satisfied && reviewsOptional {
		return reviewStandIn{
			satisfied: true,
			why:       "reviews-optional: absent GitHub review decision accepted" + objectionNote(panel),
		}, nil
	}
	return panel, nil
}

// objectionNote carries a rejected panel's reason onto a stand-in that did NOT
// rest on the panel, so a pass never silently steps over a recorded objection.
//
// A human's exact-head approval outranks a bot's change request — a deliberate,
// separately pinned decision (TestReadinessBotChangeRequestDoesNotSuppressHuman-
// Approval): bot findings are findings, and authorization belongs to the person
// with repository authority. Reversing that is not this rung's call. But the
// decision log has to SHOW that the approval carried readiness past an
// objection, or the one reader who needs to know — the person auditing why this
// PR merged — cannot tell it apart from a clean panel. Same for the
// reviews-optional flag.
//
// Empty when the panel satisfied readiness itself (its reason is already the
// verdict's) or when it was merely incomplete (the panel rung records that).
func objectionNote(panel reviewStandIn) string {
	if panel.satisfied || panel.why == "" {
		return ""
	}
	return "; " + panel.why
}

// panelStandIn reports whether the exact-head reviewer panel answers the
// question GitHub's aggregate reviewDecision did not: did the required reviewers
// review THIS head?
//
// GitHub populates reviewDecision only from submitted reviews. The configured
// panel providers publish issue COMMENTS — codex through its connector, claude
// through a workflow attestation — so reviewDecision stays empty however
// completely they reviewed, readiness escalated on every such PR, and a judge
// then reached one fixed conclusion from facts gate already held: the panel rung
// passed at the exact head. A fixed inference over recorded facts is a rule, not
// a judgment. This is that rule, so the park and the one-shot judge invocation
// it cost are gone.
//
// What keeps it safe, each condition load-bearing:
//
//   - It reuses evaluatePanel — the same rule the panel rung records its verdict
//     from — so readiness can never call a panel complete that the panel rung
//     parks. Incomplete, pending, missing, and unknown panels escalate exactly
//     as they did before.
//
//   - Completeness is bound to the EXACT head. evaluatePanel matches the panel
//     evidence's subject against the head readiness just read from the live PR
//     view, and reviewpanel.Validate has already rejected any completed reviewer
//     recorded against a different commit. A panel completed on an older commit
//     therefore cannot satisfy readiness for a newer one — that binding is the
//     property that makes standing in for reviewDecision safe at all.
//
//   - Nobody is asking for changes. A completed CHANGES_REQUESTED review is
//     still completeness — the panel rung counts it, findings being a separate
//     verdict — but it is precisely the negative decision reviewDecision would
//     have carried. This path answers the ABSENCE of a decision; it must never
//     paper over one that exists. A human's outstanding change request counts
//     here too, at any head, on humanChangeRequest's reasoning.
//
// An empty evidence id means the caller recorded no panel, and readiness then
// behaves exactly as it did before this existed.
func panelStandIn(st *state.Store, evidenceID string, subject Subject, stances []reviewStance) (reviewStandIn, error) {
	if evidenceID == "" {
		return reviewStandIn{}, nil
	}
	panel, err := loadPanel(st, evidenceID)
	if err != nil {
		return reviewStandIn{}, err
	}
	outcome := evaluatePanel(panel, subject)
	if !outcome.complete {
		// The panel rung records the incompleteness itself, with the same
		// reason; repeating it in the readiness escalation would double-count
		// one fact in the brief a judge reads.
		return reviewStandIn{}, nil
	}
	if reviewer, ok := panelChangeRequest(panel); ok {
		return reviewStandIn{why: "exact-head review panel complete but " + reviewer + " requested changes"}, nil
	}
	if human, ok := humanChangeRequest(stances); ok {
		return reviewStandIn{why: "exact-head review panel complete but " + human + " requested changes"}, nil
	}
	return reviewStandIn{
		satisfied: true,
		why:       "exact-head review panel stands in for the absent GitHub review decision: " + outcome.why,
	}, nil
}

// readStances loads the review-stance evidence. An empty id means the caller
// recorded none, which is not an error: readiness then behaves exactly as it did
// before stances existed. A malformed body IS an error — evidence that cannot be
// parsed must never read as "nobody approved", which would silently downgrade a
// pass to an escalation and look like a policy decision.
func readStances(st *state.Store, id string) ([]reviewStance, error) {
	if id == "" {
		return nil, nil
	}
	a, err := st.Get(id)
	if err != nil {
		return nil, err
	}
	// RawMessage distinguishes an ABSENT key from an explicit null. A plain
	// slice cannot: schema drift or a wrong evidence id would decode cleanly to
	// nil and readiness would report "no review decision" — an infrastructure
	// failure wearing the costume of a policy decision.
	var body struct {
		Stances json.RawMessage `json:"stances"`
	}
	if err := json.Unmarshal(a.Body, &body); err != nil {
		return nil, fmt.Errorf("verify: parse stance evidence: %w", err)
	}
	if body.Stances == nil {
		return nil, fmt.Errorf("verify: stance evidence %s carries no stances field", id)
	}
	var stances []reviewStance
	if err := json.Unmarshal(body.Stances, &stances); err != nil {
		return nil, fmt.Errorf("verify: parse stances: %w", err)
	}
	return stances, nil
}

// readinessBlocks computes the hard blocks from a PR view and the count of
// non-gate checks (effectiveChecks) the caller uses for the empty-CI escalation.
// A block is final; the caller records a block verdict without escalating.
func readinessBlocks(pv prView) (blocks []string, effectiveChecks int) {
	if pv.IsDraft {
		blocks = append(blocks, "PR is a draft")
	}
	if pv.Mergeable == "CONFLICTING" {
		blocks = append(blocks, "merge conflicts against base")
	}
	// UNKNOWN means GitHub is still computing mergeability — not ready is
	// not green. Merged subjects are exempt: they have no live mergeability,
	// and gating a historical PR (backtest) is about its recorded evidence.
	if pv.State != "MERGED" && pv.Mergeable != "MERGEABLE" && pv.Mergeable != "CONFLICTING" {
		blocks = append(blocks, "mergeability not yet determined ("+pv.Mergeable+")")
	}
	// The review decision is GitHub's own merge-policy readback. An explicit
	// CHANGES_REQUESTED, a missing required approval, or any state this code
	// doesn't know blocks — resolve it on GitHub, not past it. Empty is not a
	// block but is not green either: it escalates in the caller (unless reviews
	// are optional there), alongside empty CI.
	if pv.State != "MERGED" && pv.ReviewDecision != "" && pv.ReviewDecision != "APPROVED" {
		blocks = append(blocks, "review decision: "+pv.ReviewDecision)
	}
	// effectiveChecks counts only non-gate rollup entries: gate's own status is
	// its prior verdict on this head, not independent CI signal, so it must not
	// count toward "CI was recorded" — otherwise a gate-only rollup would mask
	// the absence of real CI and pass silently, the exact hazard the empty-signal
	// escalation exists to catch.
	for _, c := range pv.StatusCheckRollup {
		if isOwnGateStatus(c) {
			continue
		}
		effectiveChecks++
		if c.green() {
			continue
		}
		blocks = append(blocks, fmt.Sprintf("check not green: %s (%s)", checkName(c.Name, c.Context), c.status()))
	}
	return blocks, effectiveChecks
}

func checkName(name, context string) string {
	if name != "" {
		return name
	}
	return context
}

// rollupCheck is one status-rollup entry: a check run (status/conclusion) or
// a commit status context (state).
type rollupCheck struct {
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// green accepts only explicitly successful (or explicitly ignorable) checks.
// Everything else — pending, queued, errored, cancelled, timed out, expected,
// or any state this code doesn't know — blocks: fail closed.
func (c rollupCheck) green() bool {
	switch c.Conclusion {
	case "SUCCESS", "NEUTRAL", "SKIPPED":
		return true
	}
	return c.State == "SUCCESS"
}

// status names the entry's most specific non-empty state for the block message.
func (c rollupCheck) status() string {
	if c.Conclusion != "" {
		return c.Conclusion
	}
	if c.State != "" {
		return c.State
	}
	if c.Status != "" {
		return c.Status
	}
	return "unknown"
}
