package verify

import (
	"encoding/json"
	"fmt"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// The up-to-date rung answers one question the rest of the ladder never asked:
// will GitHub accept the merge command gate is about to emit? On a base branch
// that requires the head to be current, a BEHIND pull request is refused at the
// API — so gate would emit a pinned command that was never going to land, and
// the operator learns the requirement from the rejection.
//
// The rung is deliberately narrow. BEHIND alone is NOT a reason to refresh:
// most portfolio repositories are unprotected or non-strict (see
// docs/auto-mode-defaults.md), and demanding a refresh there would add a
// pointless CI cycle to nearly every merge. Only strict protection makes BEHIND
// decisive; conflicts are already readiness's block.
//
// Unreadable protection degrades to the previous behaviour — pass, with the
// degradation recorded in the verdict — never to a block. An unread fact is not
// evidence, and gate must not stop merges on a permission it lacks.

// behind is GitHub's mergeStateStatus for a head that trails its base. It is
// the only status this rung acts on: BLOCKED, DIRTY, and UNSTABLE are other
// rungs' business, and acting on them here would double-block.
const behindState = "BEHIND"

// upToDateView is the slice of the PR view this rung reads.
type upToDateView struct {
	State            string `json:"state"`
	MergeStateStatus string `json:"mergeStateStatus"`
	BaseRefName      string `json:"baseRefName"`
	HeadRefOid       string `json:"headRefOid"`
}

// protectionEvidence mirrors evidence.ProtectionBody on the JSON seam —
// evidence and verify share artifacts, never imports (see the repo charter).
type protectionEvidence struct {
	BaseRef  string `json:"base_ref"`
	Readable bool   `json:"readable"`
	Strict   bool   `json:"strict"`
	Source   string `json:"source"`
	Note     string `json:"note,omitempty"`
}

// UpToDate blocks a BEHIND pull request whose base branch requires up-to-date
// heads, and names the fix: refresh from base, let CI re-run, gate again.
//
// An empty protectionEvidenceID means the caller recorded no protection read;
// the rung then behaves exactly as gate did before it existed.
func UpToDate(st *state.Store, run, viewEvidenceID, protectionEvidenceID string, subject Subject) (state.Artifact, error) {
	pv, err := readUpToDateView(st, viewEvidenceID)
	if err != nil {
		return state.Artifact{}, err
	}
	prot, err := readProtectionEvidence(st, protectionEvidenceID)
	if err != nil {
		return state.Artifact{}, err
	}

	parents := []string{viewEvidenceID}
	if protectionEvidenceID != "" {
		parents = append(parents, protectionEvidenceID)
	}
	v := Verdict{
		Subject:    subject,
		Source:     "up-to-date",
		Producer:   Producer{Class: ClassCode, Impl: "branch-protection-preflight"},
		Decision:   DecisionPass,
		Tier:       "T0",
		Confidence: 1.0,
		Why:        upToDateWhy(pv, prot),
	}
	// A merged subject has no live merge to preflight (backtest replays merged
	// PRs), matching readiness's MERGED exemptions.
	if pv.State != "MERGED" && prot.Readable && prot.Strict && pv.MergeStateStatus == behindState {
		v.Decision = DecisionBlock
		v.Findings = []Finding{{Title: v.Why, Severity: "block"}}
	}
	return Record(st, run, parents, v)
}

// upToDateWhy states the fact that carried the decision, so an audit reader can
// tell a repo with no requirement from one whose requirement gate could not
// read — the two pass identically and cost very differently.
func upToDateWhy(pv upToDateView, prot protectionEvidence) string {
	if !prot.Readable {
		return fmt.Sprintf(
			"branch protection unreadable for base %s (%s) — up-to-date requirement not verified; merge_state=%s",
			prot.BaseRef, prot.Note, pv.MergeStateStatus)
	}
	if !prot.Strict {
		return fmt.Sprintf("base %s does not require up-to-date heads (%s); merge_state=%s",
			prot.BaseRef, prot.Source, pv.MergeStateStatus)
	}
	if pv.MergeStateStatus != behindState {
		return fmt.Sprintf("base %s requires up-to-date heads (%s) and this head is current; merge_state=%s",
			prot.BaseRef, prot.Source, pv.MergeStateStatus)
	}
	return fmt.Sprintf(
		"base %s requires the head to be up to date (%s) and this PR is BEHIND: GitHub would reject the merge. "+
			"Merge %s into the branch, let CI re-run on the new head, then gate again — the judgment binds to the head it was made against.",
		prot.BaseRef, prot.Source, prot.BaseRef)
}

func readUpToDateView(st *state.Store, id string) (upToDateView, error) {
	a, err := st.Get(id)
	if err != nil {
		return upToDateView{}, err
	}
	var body struct {
		Data upToDateView `json:"data"`
	}
	if err := json.Unmarshal(a.Body, &body); err != nil {
		return upToDateView{}, fmt.Errorf("verify: parse view evidence: %w", err)
	}
	return body.Data, nil
}

// readProtectionEvidence loads the recorded protection read. A malformed body
// is an error, not a silent "unreadable": that would hide schema drift behind
// the degraded path, which is exactly the branch nobody looks at.
func readProtectionEvidence(st *state.Store, id string) (protectionEvidence, error) {
	if id == "" {
		return protectionEvidence{Note: "no protection read recorded"}, nil
	}
	a, err := st.Get(id)
	if err != nil {
		return protectionEvidence{}, err
	}
	var prot protectionEvidence
	if err := json.Unmarshal(a.Body, &prot); err != nil {
		return protectionEvidence{}, fmt.Errorf("verify: parse protection evidence: %w", err)
	}
	return prot, nil
}
