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
	State     string `json:"state"`
	IsDraft   bool   `json:"isDraft"`
	Mergeable string `json:"mergeable"`
	// MergeStateStatus is GitHub's mergeability *state*: BEHIND means the head
	// is not current with the base. Mergeable can read MERGEABLE at the same
	// time — the PR has no conflicts, it is merely out of date — which is
	// exactly the case that made GitHub reject a merge command gate emitted.
	MergeStateStatus  string        `json:"mergeStateStatus"`
	BaseRefName       string        `json:"baseRefName"`
	ReviewDecision    string        `json:"reviewDecision"`
	HeadRefOid        string        `json:"headRefOid"`
	StatusCheckRollup []rollupCheck `json:"statusCheckRollup"`
	Author            struct {
		Login string `json:"login"`
	} `json:"author"`
}

// baseProtection mirrors the evidence package's BaseProtection on the JSON seam
// (same reason as reviewStance below: artifacts, never imports).
type baseProtection struct {
	Base            string `json:"base"`
	RequireUpToDate bool   `json:"require_up_to_date"`
	Known           bool   `json:"known"`
	Reason          string `json:"reason,omitempty"`
}

// mergeStateBehind is GitHub's mergeStateStatus for a head that is not current
// with its base.
const mergeStateBehind = "BEHIND"

// upToDateRefresh is the prescription an out-of-date branch carries. Stating
// the whole sequence matters: refreshing the branch moves the head, so the CI
// gate read is no longer the CI of what would merge, and this run's pinned
// --match-head-commit is dead. Anything less sends the operator back into the
// same loop one step at a time — which is the friction this exists to remove.
const upToDateRefresh = "merge the base branch into it, let CI finish on the new head, then re-run gate"

// behindBase reports the base branch a LIVE PR is out of date with, if GitHub
// says it is out of date at all. A MERGED subject is exempt: it has no live
// merge state, and gating a historical PR (backtest) is about recorded
// evidence — the same exemption the mergeability block above takes.
func behindBase(pv prView) (string, bool) {
	if pv.State == "MERGED" || pv.MergeStateStatus != mergeStateBehind {
		return "", false
	}
	if pv.BaseRefName == "" {
		return "the base branch", true
	}
	return pv.BaseRefName, true
}

// upToDateBlock reports the block for a branch that is behind its base on a
// repository that PROVABLY requires branches to be current before merging —
// the case where GitHub would reject gate's own emitted `gh pr merge`. Saying
// it here, as a readiness block with the refresh sequence attached, is the
// point: an emitted command that cannot land is worse than no command.
//
// BEHIND alone is deliberately NOT enough, and this is the whole subtlety. A
// repository that does not require an up-to-date branch merges a behind PR
// happily, and blocking there would invent a refresh cycle per run. Only proof
// blocks:
//
//   - protection readable and strict → proven requirement, block.
//   - protection readable and not strict → proven absent, no block. (A merge
//     conflict is a different fact and already blocks on CONFLICTING.)
//   - protection unreadable → nothing is proven in either direction, so this
//     records no block. It escalates instead (upToDateEscalation), which is
//     gate's vocabulary for "cannot verify" — a judge can settle it, and an
//     unprovable guess never manufactures a refresh cycle.
func upToDateBlock(pv prView, prot baseProtection) (string, bool) {
	base, behind := behindBase(pv)
	if !behind || !prot.Known || !prot.RequireUpToDate {
		return "", false
	}
	return fmt.Sprintf("branch is behind %s and %s requires branches to be up to date before merging — %s",
		base, base, upToDateRefresh), true
}

// upToDateEscalation reports the escalation for a behind branch whose base
// protection gate could not read. GitHub answers 404 both for an unprotected
// branch and for a token without admin, so an unreadable answer proves nothing;
// naming it as unverified beats both silently passing (and emitting a merge
// command GitHub may reject) and silently blocking (a refresh cycle per run on
// every repo that requires nothing).
func upToDateEscalation(pv prView, prot baseProtection) (string, bool) {
	base, behind := behindBase(pv)
	if !behind || prot.Known {
		return "", false
	}
	return fmt.Sprintf("branch is behind %s and its protection could not be read (%s) — if the base requires up-to-date branches, %s",
		base, protectionReason(prot), upToDateRefresh), true
}

func protectionReason(prot baseProtection) string {
	if prot.Reason == "" {
		return "no protection evidence recorded"
	}
	return prot.Reason
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
//   - no human's CURRENT stance is CHANGES_REQUESTED. On a protected repo
//     GitHub would fold this into reviewDecision and readinessBlocks would
//     catch it — but this path exists precisely because reviewDecision is
//     null, so nothing else would. One human approving does not clear
//     another's outstanding objection, at any head: a change request stands
//     until its author withdraws or supersedes it, and the safe answer to
//     "approved by one, objected by another" is to escalate, not to pick.
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
	for _, s := range stances {
		if !s.IsBot && s.State == approvalStateChangesRequested {
			return "", false
		}
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
// Empty is tolerated so a caller with no stance evidence behaves exactly as
// before this existed.
// protectionEvidenceID names the base-branch protection artifact (whether the
// base requires branches to be current before merging). Empty is tolerated —
// readiness then treats the requirement as unproven, which only matters when
// GitHub also reports the branch BEHIND; see upToDateBlock.
func Readiness(st *state.Store, run, viewEvidenceID, stancesEvidenceID, protectionEvidenceID string, subject Subject, reviewsOptional bool) (state.Artifact, Subject, error) {
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
	approver, humanApproved := humanApprovalAtHead(stances, pv.HeadRefOid, pv.Author.Login)

	prot, err := readProtection(st, protectionEvidenceID)
	if err != nil {
		return state.Artifact{}, subject, err
	}

	// A verdict's parents name the evidence it judged (the artifact contract).
	// Stance evidence is judged whenever it was supplied — not only when it
	// changed the outcome — so provenance traversal can reach what readiness
	// actually read, without inferring it from the verdict text.
	parents := []string{viewEvidenceID}
	if stancesEvidenceID != "" {
		parents = append(parents, stancesEvidenceID)
	}
	if protectionEvidenceID != "" {
		parents = append(parents, protectionEvidenceID)
	}

	v := Verdict{
		Subject:    subject,
		Source:     "readiness",
		Producer:   Producer{Class: ClassCode, Impl: "gh-readback"},
		Decision:   DecisionPass,
		Tier:       "T0",
		Confidence: 1.0,
	}
	blocks, effectiveChecks := readinessBlocks(pv, prot)
	if len(blocks) > 0 {
		v.Decision = DecisionBlock
		v.Why = fmt.Sprint(blocks)
		for _, b := range blocks {
			v.Findings = append(v.Findings, Finding{Title: b, Severity: "block"})
		}
		art, err := Record(st, run, parents, v)
		return art, subject, err
	}
	escalations := readinessEscalations(pv, prot, effectiveChecks, humanApproved, reviewsOptional)
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
	// (gate's decisions must be reconstructable from state alone). The human
	// approval is the stronger, more specific fact — record it in preference to
	// the policy flag, and name the approver and the head it was given at.
	switch {
	case humanApproved && pv.State != "MERGED" && pv.ReviewDecision == "":
		v.Why += fmt.Sprintf("; approved at head by %s (no GitHub review decision reported)", approver)
	case reviewsOptional && pv.State != "MERGED" && pv.ReviewDecision == "":
		v.Why += "; reviews-optional: absent GitHub review decision accepted"
	}
	art, err := Record(st, run, parents, v)
	return art, subject, err
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

// readinessEscalations computes the "cannot verify" reasons from a PR view.
// Absence of signal must not read as green. An empty rollup means CI never ran;
// an empty reviewDecision means GitHub reported no live merge policy — on an
// unprotected repo that would otherwise pass silently; an unreadable base
// protection on a behind branch means gate cannot tell whether the merge
// command it is about to emit would even be accepted. Escalate rather than
// block: a judge may know the repo genuinely has no CI, requires no reviews, or
// does not require current branches. If several hold, one escalate naming every
// reason is enough.
func readinessEscalations(pv prView, prot baseProtection, effectiveChecks int, humanApproved, reviewsOptional bool) []string {
	var escalations []string
	// Said first: on a repo that turns out to require an up-to-date branch this
	// is the fact that decides the whole run, and the one the operator has to
	// act on before anything else — refreshing the branch re-runs CI anyway.
	if e, unproven := upToDateEscalation(pv, prot); unproven {
		escalations = append(escalations, e)
	}
	if effectiveChecks == 0 {
		escalations = append(escalations, "no non-gate CI checks recorded for this head")
	}
	// A human's exact-head approval IS the review decision GitHub declined to
	// report. Checked before reviewsOptional so the audit trail records the
	// approval as the reason readiness held, not a policy flag. MERGED-exempt (a
	// backtested PR has no live review signal), mirroring the block checks; the
	// empty-CI branch is not, matching its long-standing behavior.
	if !humanApproved && !reviewsOptional && pv.State != "MERGED" && pv.ReviewDecision == "" {
		escalations = append(escalations, "no review decision reported by GitHub")
	}
	return escalations
}

// readProtection loads the base-branch protection evidence. An empty id means
// the caller recorded none — readiness then behaves exactly as it did before
// this evidence existed. A malformed body IS an error, for the same reason as
// readStances: an infrastructure failure must not wear the costume of a policy
// decision on the one fact that decides whether the emitted merge command can
// land.
func readProtection(st *state.Store, id string) (baseProtection, error) {
	if id == "" {
		return baseProtection{Reason: "no protection evidence recorded"}, nil
	}
	a, err := st.Get(id)
	if err != nil {
		return baseProtection{}, err
	}
	var body struct {
		Protection json.RawMessage `json:"protection"`
	}
	if err := json.Unmarshal(a.Body, &body); err != nil {
		return baseProtection{}, fmt.Errorf("verify: parse protection evidence: %w", err)
	}
	if body.Protection == nil {
		return baseProtection{}, fmt.Errorf("verify: protection evidence %s carries no protection field", id)
	}
	var prot baseProtection
	if err := json.Unmarshal(body.Protection, &prot); err != nil {
		return baseProtection{}, fmt.Errorf("verify: parse protection: %w", err)
	}
	return prot, nil
}

// readinessBlocks computes the hard blocks from a PR view and the count of
// non-gate checks (effectiveChecks) the caller uses for the empty-CI escalation.
// A block is final; the caller records a block verdict without escalating.
func readinessBlocks(pv prView, prot baseProtection) (blocks []string, effectiveChecks int) {
	if pv.IsDraft {
		blocks = append(blocks, "PR is a draft")
	}
	if pv.Mergeable == "CONFLICTING" {
		blocks = append(blocks, "merge conflicts against base")
	}
	// Said before the check loop: a proven up-to-date requirement decides the
	// run, and refreshing the branch invalidates every check below it anyway.
	if b, behind := upToDateBlock(pv, prot); behind {
		blocks = append(blocks, b)
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
