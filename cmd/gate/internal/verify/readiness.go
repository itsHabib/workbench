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
func Readiness(st *state.Store, run, viewEvidenceID string, subject Subject, reviewsOptional bool) (state.Artifact, Subject, error) {
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
		art, err := Record(st, run, []string{viewEvidenceID}, v)
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
	if !reviewsOptional && pv.State != "MERGED" && pv.ReviewDecision == "" {
		escalations = append(escalations, "no review decision reported by GitHub")
	}
	if len(escalations) > 0 {
		v.Decision = DecisionEscalate
		v.Why = strings.Join(escalations, "; ") + " — cannot verify readiness"
		art, err := Record(st, run, []string{viewEvidenceID}, v)
		return art, subject, err
	}
	v.Why = fmt.Sprintf("state=%s draft=%v mergeable=%s checks=%d green",
		pv.State, pv.IsDraft, pv.Mergeable, effectiveChecks)
	// Persist the policy choice in the verdict when it actually changed the
	// outcome: a pass that accepted an ABSENT review decision only because
	// reviews were optional must say so, or the audit log cannot tell an
	// intentional acceptance from readiness wrongly passing an unverified PR
	// (gate's decisions must be reconstructable from state alone).
	if reviewsOptional && pv.State != "MERGED" && pv.ReviewDecision == "" {
		v.Why += "; reviews-optional: absent GitHub review decision accepted"
	}
	art, err := Record(st, run, []string{viewEvidenceID}, v)
	return art, subject, err
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
