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
	a, err := st.Get(evidenceID)
	if err != nil {
		return state.Artifact{}, err
	}
	var panel reviewpanel.Evidence
	if err := json.Unmarshal(a.Body, &panel); err != nil {
		return state.Artifact{}, fmt.Errorf("verify: parse review panel evidence: %w", err)
	}
	if err := reviewpanel.Validate(panel); err != nil {
		return state.Artifact{}, fmt.Errorf("verify: invalid review panel evidence: %w", err)
	}
	v := Verdict{
		Subject: subject, Source: "review-panel-completeness",
		Producer: Producer{Class: ClassCode, Impl: "review-panel-v1"},
		Decision: DecisionPass, Tier: "T0", Confidence: 1,
	}
	if panel.Subject.Repo != subject.Repo || panel.Subject.Number != subject.Number ||
		panel.Subject.HeadSHA != subject.HeadSHA {
		v.Decision = DecisionEscalate
		v.Why = "review panel evidence does not match the exact judged head"
		return Record(st, run, []string{evidenceID}, v)
	}
	if err := equivalenceHonoured(panel); err != nil {
		v.Decision = DecisionEscalate
		v.Why = err.Error()
		return Record(st, run, []string{evidenceID}, v)
	}
	if len(panel.Declaration.Expected) == 0 || len(panel.Unknown) > 0 {
		v.Decision = DecisionEscalate
		v.Why = fmt.Sprintf("review panel state unknown: %s", strings.Join(panel.Unknown, ", "))
		return Record(st, run, []string{evidenceID}, v)
	}
	if len(panel.Pending) > 0 || len(panel.Missing) > 0 {
		v.Decision = DecisionEscalate
		v.Why = fmt.Sprintf(
			"review panel incomplete: completed=%d expected=%d pending=[%s] missing=[%s]",
			len(panel.Completed), len(panel.Declaration.Expected),
			strings.Join(panel.Pending, ", "), strings.Join(panel.Missing, ", "),
		)
		return Record(st, run, []string{evidenceID}, v)
	}
	v.Why = fmt.Sprintf(
		"all %d required reviewers completed the exact head declared by %s@%s%s",
		len(panel.Completed), panel.Declaration.Path, panel.Declaration.Revision,
		refreshNote(panel),
	)
	return Record(st, run, []string{evidenceID}, v)
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
