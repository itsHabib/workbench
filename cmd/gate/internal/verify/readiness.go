package verify

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

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
func Readiness(st *state.Store, run, viewEvidenceID string, subject Subject) (state.Artifact, Subject, error) {
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
	var blocks []string
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
	// block but is not green either: it escalates below, alongside empty CI.
	if pv.State != "MERGED" && pv.ReviewDecision != "" && pv.ReviewDecision != "APPROVED" {
		blocks = append(blocks, "review decision: "+pv.ReviewDecision)
	}
	for _, c := range pv.StatusCheckRollup {
		if c.green() {
			continue
		}
		blocks = append(blocks, fmt.Sprintf("check not green: %s (%s)", checkName(c.Name, c.Context), c.status()))
	}
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
	if len(pv.StatusCheckRollup) == 0 {
		escalations = append(escalations, "no CI checks recorded for this head")
	}
	if pv.State != "MERGED" && pv.ReviewDecision == "" {
		escalations = append(escalations, "no review decision reported by GitHub")
	}
	if len(escalations) > 0 {
		v.Decision = DecisionEscalate
		v.Why = strings.Join(escalations, "; ") + " — cannot verify readiness"
		art, err := Record(st, run, []string{viewEvidenceID}, v)
		return art, subject, err
	}
	v.Why = fmt.Sprintf("state=%s draft=%v mergeable=%s checks=%d green",
		pv.State, pv.IsDraft, pv.Mergeable, len(pv.StatusCheckRollup))
	art, err := Record(st, run, []string{viewEvidenceID}, v)
	return art, subject, err
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
