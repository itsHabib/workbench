package verify

import (
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/contracts/reviewpanel"
)

// completePanelAt is the shape every configured portfolio PR produces: the
// repository-declared reviewers all completed the exact head as COMMENTED —
// what a provider that publishes issue comments rather than submitting a formal
// review is credited with, and precisely the case GitHub reports no aggregate
// reviewDecision for.
func completePanelAt(head string) reviewpanel.Evidence {
	return panelEvidence([]string{"codex", "claude"}, 0b11, head)
}

// The park this change exists to remove. Seven PRs on 2026-08-16/17 escalated
// here and every judgment reached the same conclusion from facts already in
// state: the panel rung passed at the exact head. That inference is fixed, so it
// belongs in code — and the verdict must say so, or the audit log cannot tell
// this acceptance from readiness passing an unreviewed PR.
func TestReadinessCompletePanelSatisfiesAbsentReviewDecision(t *testing.T) {
	v := readinessWithPanel(t, openPRAtHead("abc123"), completePanelAt("abc123"), nil)
	if v.Decision != DecisionPass {
		t.Fatalf("complete exact-head panel must satisfy readiness, got %s (%s)", v.Decision, v.Why)
	}
	if !strings.Contains(v.Why, "exact-head review panel") {
		t.Fatalf("verdict must record the panel as what carried readiness, got %q", v.Why)
	}
	if !strings.Contains(v.Why, "all 2 required reviewers completed") {
		t.Fatalf("verdict must record the completeness it relied on, got %q", v.Why)
	}
}

// The head binding is the property that makes the stand-in safe: a panel that
// reviewed earlier code has not reviewed this code.
func TestReadinessStalePanelDoesNotSatisfyReadiness(t *testing.T) {
	v := readinessWithPanel(t, openPRAtHead("newhead"), completePanelAt("oldhead"), nil)
	if v.Decision != DecisionEscalate {
		t.Fatalf("panel completed on a superseded head must not satisfy readiness, got %s (%s)", v.Decision, v.Why)
	}
}

// Panel evidence for another PR is not this PR's evidence, however complete.
func TestReadinessForeignPanelDoesNotSatisfyReadiness(t *testing.T) {
	panel := completePanelAt("abc123")
	panel.Subject.Number = subj.Number + 1
	v := readinessWithPanel(t, openPRAtHead("abc123"), panel, nil)
	if v.Decision != DecisionEscalate {
		t.Fatalf("another PR's panel must not satisfy readiness, got %s (%s)", v.Decision, v.Why)
	}
}

// The stand-in inherits the panel rung's rule whole, including the one
// non-judged head that rung already honours: a declared diff-equivalent refresh.
// Sharing evaluatePanel is what makes that automatic rather than a second
// head-matching rule readiness would have to be taught about separately.
func TestReadinessDiffEquivalentRefreshSatisfiesReadiness(t *testing.T) {
	v := readinessWithPanel(t, openPRAtHead("new"), refreshPanel(), nil)
	if v.Decision != DecisionPass {
		t.Fatalf("a diff-equivalent refresh must satisfy readiness, got %s (%s)", v.Decision, v.Why)
	}
	if !strings.Contains(v.Why, "carried from old") {
		t.Fatalf("verdict must record that credit was carried, got %q", v.Why)
	}
}

// ...and it inherits the refusals too. An equivalence this rung cannot read is
// not a head match, so nothing stands in.
func TestReadinessUnreadableEquivalenceDoesNotSatisfyReadiness(t *testing.T) {
	panel := refreshPanel()
	panel.Equivalence.DiffDigest = "md5:abc"
	v := readinessWithPanel(t, openPRAtHead("new"), panel, nil)
	if v.Decision != DecisionEscalate {
		t.Fatalf("an unreadable equivalence must not satisfy readiness, got %s (%s)", v.Decision, v.Why)
	}
}

// Anything short of complete keeps escalating exactly as it did before, and the
// readiness escalation stays quiet about it — the panel rung records the reason
// itself, and saying it twice double-counts one fact in the brief a judge reads.
func TestReadinessIncompletePanelDoesNotSatisfyReadiness(t *testing.T) {
	required := []string{"codex", "claude"}
	tests := map[string]func(reviewpanel.Evidence) reviewpanel.Evidence{
		"pending": func(p reviewpanel.Evidence) reviewpanel.Evidence {
			p.Completed, p.Pending = p.Completed[:1], required[1:]
			return p
		},
		"missing": func(p reviewpanel.Evidence) reviewpanel.Evidence {
			p.Completed, p.Missing = p.Completed[:1], required[1:]
			return p
		},
		"unknown": func(p reviewpanel.Evidence) reviewpanel.Evidence {
			p.Completed, p.Unknown = p.Completed[:1], required[1:]
			return p
		},
		"no declaration": func(p reviewpanel.Evidence) reviewpanel.Evidence {
			p.Completed, p.Declaration.Expected, p.Unknown = nil, nil, []string{"declaration"}
			return p
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			v := readinessWithPanel(t, openPRAtHead("abc123"), mutate(completePanelAt("abc123")), nil)
			if v.Decision != DecisionEscalate {
				t.Fatalf("%s panel must not satisfy readiness, got %s (%s)", name, v.Decision, v.Why)
			}
			if !strings.Contains(v.Why, "no review decision reported by GitHub") {
				t.Fatalf("escalation must still name the absent decision, got %q", v.Why)
			}
			if strings.Contains(v.Why, "panel") {
				t.Fatalf("readiness must leave the panel rung to report incompleteness, got %q", v.Why)
			}
		})
	}
}

// This path answers the ABSENCE of a review decision. An explicit negative one
// is not absence, and no amount of panel completeness may talk it green.
func TestReadinessPanelDoesNotOverrideGitHubChangesRequested(t *testing.T) {
	for _, decision := range []string{"CHANGES_REQUESTED", "REVIEW_REQUIRED"} {
		t.Run(decision, func(t *testing.T) {
			view := openPRAtHead("abc123")
			view["reviewDecision"] = decision
			v := readinessWithPanel(t, view, completePanelAt("abc123"), nil)
			if v.Decision != DecisionBlock {
				t.Fatalf("%s must still block behind a complete panel, got %s (%s)", decision, v.Decision, v.Why)
			}
		})
	}
}

// A CHANGES_REQUESTED review still COMPLETES the panel — the panel rung counts
// it, findings being a separate verdict — so completeness alone would have let a
// reviewer's own objection satisfy readiness. It must not, and the verdict has
// to say why the complete panel was refused or the reader hunts for a
// contradiction that is really a deliberate rejection.
func TestReadinessPanelReviewerChangeRequestDoesNotSatisfyReadiness(t *testing.T) {
	panel := completePanelAt("abc123")
	panel.Completed[0].State = "CHANGES_REQUESTED"
	v := readinessWithPanel(t, openPRAtHead("abc123"), panel, nil)
	if v.Decision != DecisionEscalate {
		t.Fatalf("a reviewer asking for changes must not satisfy readiness, got %s (%s)", v.Decision, v.Why)
	}
	if !strings.Contains(v.Why, panel.Completed[0].Name+" requested changes") {
		t.Fatalf("escalation must name the rejected panel and who objected, got %q", v.Why)
	}
}

// A human's outstanding objection outranks a clean bot panel, at any head — the
// same asymmetry humanApprovalAtHead already applies to the approval path.
func TestReadinessPanelDoesNotOverrideHumanChangeRequest(t *testing.T) {
	v := readinessWithPanel(t, openPRAtHead("abc123"), completePanelAt("abc123"), []map[string]any{
		{"author": "mh", "is_bot": false, "association": "OWNER", "permission": "write",
			"state": "CHANGES_REQUESTED", "commit_id": "oldhead"},
	})
	if v.Decision != DecisionEscalate {
		t.Fatalf("human change request must not be papered over by the panel, got %s (%s)", v.Decision, v.Why)
	}
	if !strings.Contains(v.Why, "mh requested changes") {
		t.Fatalf("escalation must name the objection, got %q", v.Why)
	}
}

// Every other readiness input is untouched: the panel answers the review
// question and nothing else.
func TestReadinessPanelDoesNotMaskOtherBlocks(t *testing.T) {
	tests := map[string]struct {
		mutate func(map[string]any)
		want   string
	}{
		"draft":                {func(v map[string]any) { v["isDraft"] = true }, DecisionBlock},
		"conflicts":            {func(v map[string]any) { v["mergeable"] = "CONFLICTING" }, DecisionBlock},
		"mergeability unknown": {func(v map[string]any) { v["mergeable"] = "UNKNOWN" }, DecisionBlock},
		"red check": {func(v map[string]any) {
			v["statusCheckRollup"] = []map[string]any{{"name": "ci", "conclusion": "FAILURE"}}
		}, DecisionBlock},
		"no CI at all": {func(v map[string]any) {
			v["statusCheckRollup"] = []map[string]any{}
		}, DecisionEscalate},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			view := openPRAtHead("abc123")
			tc.mutate(view)
			v := readinessWithPanel(t, view, completePanelAt("abc123"), nil)
			if v.Decision != tc.want {
				t.Fatalf("%s must still be %s behind a complete panel, got %s (%s)",
					name, tc.want, v.Decision, v.Why)
			}
		})
	}
}

// A human's approval outranks a bot's change request — pinned separately by
// TestReadinessBotChangeRequestDoesNotSuppressHumanApproval, and not this rung's
// decision to reverse. What this rung owes is visibility: the verdict must show
// that the approval carried readiness PAST an objection, or an auditor cannot
// tell this pass from one over a clean panel. The gap that makes it matter is
// narrow but real — a bodyless CHANGES_REQUESTED review yields no findings for
// the review-consolidation rung either, so the log is the only place it appears.
func TestReadinessHumanApprovalRecordsThePanelObjectionItPassed(t *testing.T) {
	panel := completePanelAt("abc123")
	panel.Completed[0].State = "CHANGES_REQUESTED"
	v := readinessWithPanel(t, openPRAtHead("abc123"), panel, []map[string]any{
		{"author": "mh", "is_bot": false, "association": "OWNER", "permission": "write",
			"state": "APPROVED", "commit_id": "abc123"},
	})
	if v.Decision != DecisionPass {
		t.Fatalf("human approval must still outrank a bot change request, got %s (%s)", v.Decision, v.Why)
	}
	if !strings.Contains(v.Why, "approved at head by mh") {
		t.Fatalf("verdict must record the approval that carried readiness, got %q", v.Why)
	}
	if !strings.Contains(v.Why, panel.Completed[0].Name+" requested changes") {
		t.Fatalf("verdict must record the objection the approval passed, got %q", v.Why)
	}
}

// The enforced-check context is unchanged by any of this: reviews-optional
// suppresses the absence escalation unconditionally, and a panel objection does
// not narrow it. Pre-existing behavior — before the stand-in existed, the flag
// suppressed the escalation in every case — pinned here because the stand-in now
// sits in front of the flag and could quietly start vetoing it.
func TestReadinessReviewsOptionalIsNotNarrowedByPanelObjection(t *testing.T) {
	panel := completePanelAt("abc123")
	panel.Completed[0].State = "CHANGES_REQUESTED"
	v := readinessOptWithPanel(t, openPRAtHead("abc123"), panel)
	if v.Decision != DecisionPass {
		t.Fatalf("reviews-optional must still accept the absent decision, got %s (%s)", v.Decision, v.Why)
	}
	if !strings.Contains(v.Why, "reviews-optional") {
		t.Fatalf("verdict must record the flag that carried readiness, got %q", v.Why)
	}
	if !strings.Contains(v.Why, panel.Completed[0].Name+" requested changes") {
		t.Fatalf("verdict must record the objection the flag passed, got %q", v.Why)
	}
}

// A human's exact-head approval is the more specific fact, so it stays what the
// verdict records when both hold.
func TestReadinessHumanApprovalOutranksPanelInTheRecord(t *testing.T) {
	v := readinessWithPanel(t, openPRAtHead("abc123"), completePanelAt("abc123"), []map[string]any{
		{"author": "mh", "is_bot": false, "association": "OWNER", "permission": "write",
			"state": "APPROVED", "commit_id": "abc123"},
	})
	if v.Decision != DecisionPass {
		t.Fatalf("approval and a complete panel must pass, got %s (%s)", v.Decision, v.Why)
	}
	if !strings.Contains(v.Why, "approved at head by mh") {
		t.Fatalf("verdict must record the approval as what carried readiness, got %q", v.Why)
	}
}

// Provenance traversal has to reach what readiness actually read, without
// inferring it from the verdict text.
func TestReadinessVerdictParentsIncludePanelEvidence(t *testing.T) {
	art, ids := readinessRunWithPanel(t, openPRAtHead("abc123"), completePanelAt("abc123"), nil)
	panelID := ids[2]
	for _, parent := range art.Parents {
		if parent == panelID {
			return
		}
	}
	t.Fatalf("readiness verdict parents %v must name the panel evidence %s", art.Parents, panelID)
}

// Evidence that cannot be read is an infrastructure failure. Treating it as an
// incomplete panel would silently downgrade a pass to a park wearing the costume
// of a policy decision — the same rule readStances already follows.
func TestReadinessMalformedPanelEvidenceIsAnError(t *testing.T) {
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	evd, err := st.Append(state.KindEvidence, "run_t", nil, map[string]any{"data": openPRAtHead("abc123")})
	if err != nil {
		t.Fatal(err)
	}
	// Schema-valid JSON, contract-invalid panel: no schema version, no subject.
	pnl, err := st.Append(state.KindEvidence, "run_t", nil, map[string]any{"completed": "not a list"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Readiness(st, "run_t", evd.ID, "", pnl.ID, subj, false); err == nil {
		t.Fatal("unreadable panel evidence must be an error, not a silent escalation")
	}
}

// readinessWithPanel drives readiness over the view, panel, and optional stance
// evidence a real gate run records.
func readinessWithPanel(t *testing.T, view map[string]any, panel reviewpanel.Evidence, stances []map[string]any) Verdict {
	t.Helper()
	art, _ := readinessRunWithPanel(t, view, panel, stances)
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// readinessOptWithPanel drives readiness with reviewsOptional=true (the
// enforced-check context) over panel evidence, mirroring readinessOptFor.
func readinessOptWithPanel(t *testing.T, view map[string]any, panel reviewpanel.Evidence) Verdict {
	t.Helper()
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	evd, err := st.Append(state.KindEvidence, "run_t", nil, map[string]any{"data": view})
	if err != nil {
		t.Fatal(err)
	}
	pnl, err := st.Append(state.KindEvidence, "run_t", nil, panel)
	if err != nil {
		t.Fatal(err)
	}
	art, _, err := Readiness(st, "run_t", evd.ID, "", pnl.ID, subj, true)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Load(art)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// readinessRunWithPanel returns the readiness artifact and the evidence ids it
// was given, in view/stances/panel order — stances is empty when none were
// recorded.
func readinessRunWithPanel(t *testing.T, view map[string]any, panel reviewpanel.Evidence, stances []map[string]any) (state.Artifact, []string) {
	t.Helper()
	st, err := state.Open(t.TempDir(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	evd, err := st.Append(state.KindEvidence, "run_t", nil, map[string]any{"data": view})
	if err != nil {
		t.Fatal(err)
	}
	stanceID := appendStances(t, st, stances)
	pnl, err := st.Append(state.KindEvidence, "run_t", nil, panel)
	if err != nil {
		t.Fatal(err)
	}
	art, _, err := Readiness(st, "run_t", evd.ID, stanceID, pnl.ID, subj, false)
	if err != nil {
		t.Fatal(err)
	}
	return art, []string{evd.ID, stanceID, pnl.ID}
}

func appendStances(t *testing.T, st *state.Store, stances []map[string]any) string {
	t.Helper()
	if stances == nil {
		return ""
	}
	a, err := st.Append(state.KindEvidence, "run_t", nil, map[string]any{"stances": stances})
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}
