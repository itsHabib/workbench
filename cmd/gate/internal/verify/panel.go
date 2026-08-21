package verify

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/contracts/reviewpanel"
)

// PanelCompleteness verifies that every repository-required reviewer completed
// a review of the exact head under judgment. It is deliberately separate from
// findings extraction: a clean result and an actionable result are different
// facts and compose monotonically in the reducer.
//
// The one head other than the judged one it will honour is a declared
// diff-equivalent refresh — see equivalenceHonoured for exactly what that buys.
func PanelCompleteness(st *state.Store, run, evidenceID string, subject Subject) (state.Artifact, error) {
	panel, err := loadPanel(st, evidenceID)
	if err != nil {
		return state.Artifact{}, err
	}
	v := Verdict{
		Subject: subject, Source: "review-panel-completeness",
		Producer: Producer{Class: ClassCode, Impl: "review-panel-v1"},
		Decision: DecisionPass, Tier: "T0", Confidence: 1,
	}
	outcome := evaluatePanel(panel, subject)
	v.Why = outcome.why
	if !outcome.complete {
		v.Decision = DecisionEscalate
	}
	return Record(st, run, []string{evidenceID}, v)
}

// loadPanel reads and contract-validates one ReviewPanelV1 evidence body. A body
// that cannot be parsed or does not satisfy the contract is an ERROR, never an
// incomplete panel: an infrastructure failure must not wear the costume of a
// review that did not happen.
func loadPanel(st *state.Store, evidenceID string) (reviewpanel.Evidence, error) {
	a, err := st.Get(evidenceID)
	if err != nil {
		return reviewpanel.Evidence{}, err
	}
	var panel reviewpanel.Evidence
	if err := json.Unmarshal(a.Body, &panel); err != nil {
		return reviewpanel.Evidence{}, fmt.Errorf("verify: parse review panel evidence: %w", err)
	}
	if err := reviewpanel.Validate(panel); err != nil {
		return reviewpanel.Evidence{}, fmt.Errorf("verify: invalid review panel evidence: %w", err)
	}
	return panel, nil
}

// panelOutcome is the completeness rule's answer for one judged head: whether
// every required reviewer completed it, and the sentence a verdict records
// either way.
type panelOutcome struct {
	complete bool
	why      string
}

// evaluatePanel applies the panel-completeness rule. It is the ONE definition of
// "the required panel completed this head": the rung above records its verdict
// from it, and readiness consults the same function when deciding whether a
// complete panel stands in for an absent GitHub review decision. Two callers,
// one rule — readiness can never call a panel complete that this rung parks, and
// the head binding, including exactly which non-judged head a declared
// diff-equivalent refresh buys, cannot drift between them.
func evaluatePanel(panel reviewpanel.Evidence, subject Subject) panelOutcome {
	if panel.Subject.Repo != subject.Repo || panel.Subject.Number != subject.Number ||
		panel.Subject.HeadSHA != subject.HeadSHA {
		return panelOutcome{why: "review panel evidence does not match the exact judged head"}
	}
	if err := equivalenceHonoured(panel); err != nil {
		return panelOutcome{why: err.Error()}
	}
	if len(panel.Declaration.Expected) == 0 || len(panel.Unknown) > 0 {
		return panelOutcome{why: fmt.Sprintf("review panel state unknown: %s", strings.Join(panel.Unknown, ", "))}
	}
	if len(panel.Pending) > 0 || len(panel.Missing) > 0 {
		return panelOutcome{why: fmt.Sprintf(
			"review panel incomplete: completed=%d expected=%d pending=[%s] missing=[%s]",
			len(panel.Completed), len(panel.Declaration.Expected),
			strings.Join(panel.Pending, ", "), strings.Join(panel.Missing, ", "),
		)}
	}
	return panelOutcome{complete: true, why: fmt.Sprintf(
		"all %d required reviewers completed the exact head declared by %s@%s%s",
		len(panel.Completed), panel.Declaration.Path, panel.Declaration.Revision,
		refreshNote(panel),
	)}
}

// panelChangeRequest names a required reviewer whose completed review asked for
// changes, if one exists.
//
// Deliberately NOT part of evaluatePanel: a CHANGES_REQUESTED review is still a
// COMPLETED one, and this rung's job is completeness — findings stay in the
// separate review-consolidation verdict, and the panel rung's behavior is
// unchanged by this existing. It matters only to readiness, which may stand in
// for an ABSENT review decision but must never stand in for a negative one.
func panelChangeRequest(panel reviewpanel.Evidence) (string, bool) {
	for _, reviewer := range panel.Completed {
		if reviewer.State == approvalStateChangesRequested {
			return reviewer.Name, true
		}
	}
	return "", false
}

// diffDigestPrefix is the only digest form this rung will honour. An evidence
// producer that moves to another algorithm must teach this rung about it; until
// then the unrecognized digest parks rather than passing on a claim no verifier
// here can read.
const diffDigestPrefix = "sha256:"

// equivalenceHonoured decides what a declared diff-equivalent refresh may buy.
//
// It buys exactly one thing: a reviewer anchored to the named earlier head is
// not stale, because the diff at that head is byte-identical to the judged
// one — the shape a conflict-free base refresh produces. It buys nothing else.
// The rest of the ladder is untouched: readiness re-reads CI at the judged head
// on this very run, and a content park still reaches a judge. The panel simply
// stops reporting reviewers as missing when nothing they reviewed has changed.
//
// A declaration that no completed reviewer relies on is refused rather than
// ignored: an equivalence in the evidence is a claim about why a review counts,
// and a claim with nothing resting on it means the producer and this rung
// disagree about what was collected.
func equivalenceHonoured(panel reviewpanel.Evidence) error {
	if panel.Equivalence == nil {
		return nil
	}
	if !strings.HasPrefix(panel.Equivalence.DiffDigest, diffDigestPrefix) {
		return fmt.Errorf("review panel declares an unreadable diff digest: %q", panel.Equivalence.DiffDigest)
	}
	for _, reviewer := range panel.Completed {
		if reviewer.HeadSHA == panel.Equivalence.ReviewedHeadSHA {
			return nil
		}
	}
	return fmt.Errorf(
		"review panel declares a diff-equivalent refresh from %s that no completed reviewer relies on",
		panel.Equivalence.ReviewedHeadSHA)
}

// refreshNote states, in the verdict's own words, that some credit came from a
// diff-equivalent refresh — so a reader of the decision log never has to infer
// it from head SHAs.
func refreshNote(panel reviewpanel.Evidence) string {
	if panel.Equivalence == nil {
		return ""
	}
	carried := 0
	for _, reviewer := range panel.Completed {
		if reviewer.HeadSHA == panel.Equivalence.ReviewedHeadSHA {
			carried++
		}
	}
	return fmt.Sprintf(
		"; %d carried from %s, a diff-equivalent refresh (%s)",
		carried, panel.Equivalence.ReviewedHeadSHA, panel.Equivalence.DiffDigest)
}
