package verify

import (
	"encoding/json"
	"fmt"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// Base freshness answers the question the ladder's evidence cannot: will the
// merge gate is about to authorize build the tree that evidence judged? A merge
// lands merge(head, base) against the base branch's head at merge time. When the
// head already contains that base head, the merged tree IS the head's tree —
// the one every CI check on the head ran against. When it does not, the merge
// builds a tree no recorded check ran: GitHub keeps no record of which base a
// CI run merged against, so "the base moved, but CI probably saw it" is
// unprovable, and ivy#115 is what trusting it cost. A judgment at 16:30Z released
// a merge on CI evidence from the previous day while three merges had landed on
// main since; one of them hashed the very file #115 changed, and main went red.
//
// It is not a ladder rung. The ladder judges evidence at gather time; this is
// re-asked at the moment act would emit the merge command — on a gate run and
// again after a judgment — because hours can pass between the two. A head that
// fails it stops with a block, not a park: no judgment can make stale evidence
// current, and parking would page a human for a question with only one answer.

// CodeBaseMoved is the code act records when the judged head does not contain
// the base branch's current head. The name reads from the evidence's side: the
// base moved on after the base this evidence provably covers.
const CodeBaseMoved = "base_moved_since_evidence"

// Freshness is the answer for one recorded base read.
type Freshness struct {
	// Fresh reports whether the merge would build the judged head's own tree.
	Fresh bool
	// Why states the fact that carried the answer, for the outcome artifact.
	Why string
}

// baseHeadEvidence mirrors evidence.BaseHeadBody on the JSON seam — evidence and
// verify share artifacts, never imports (see the repo charter).
type baseHeadEvidence struct {
	PR struct {
		Repo   string `json:"repo"`
		Number int    `json:"number"`
	} `json:"pr"`
	State        string `json:"state"`
	BaseRef      string `json:"base_ref"`
	BaseSHA      string `json:"base_sha"`
	HeadSHA      string `json:"head_sha"`
	MergeBaseSHA string `json:"merge_base_sha"`
	Status       string `json:"status"`
	BehindBy     int    `json:"behind_by"`
}

// BaseFreshness reads the recorded base read and reports whether the judged
// head contains the base branch's head.
//
// Evidence about a different PR or head is an error, not a stale answer: it is
// a wiring fault, and reading it either way would decide this merge on a fact
// about another one. A merged PR passes — its merge is history, and replay
// (backtest) evaluates merged PRs on purpose.
func BaseFreshness(st *state.Store, evidenceID string, subject Subject) (Freshness, error) {
	b, err := loadBaseHead(st, evidenceID)
	if err != nil {
		return Freshness{}, err
	}
	if b.PR.Repo != subject.Repo || b.PR.Number != subject.Number || b.HeadSHA != subject.HeadSHA {
		return Freshness{}, fmt.Errorf("verify: base evidence %s reads %s#%d at %q, not the judged %s#%d at %q",
			evidenceID, b.PR.Repo, b.PR.Number, b.HeadSHA, subject.Repo, subject.Number, subject.HeadSHA)
	}
	if b.State == "MERGED" {
		return Freshness{Fresh: true, Why: "PR is already merged; no base can move under a merge that happened"}, nil
	}
	if headContainsBase(b) {
		return Freshness{Fresh: true, Why: fmt.Sprintf(
			"head %.12s contains %s's head %.12s: the merge builds the tree CI on this head ran",
			b.HeadSHA, b.BaseRef, b.BaseSHA)}, nil
	}
	return Freshness{Why: fmt.Sprintf(
		"%s: %s is at %.12s, which head %.12s does not contain (%d commit(s) since merge base %.12s, compare %q), "+
			"so the merge would build a tree no CI run on this head tested. "+
			"Merge %s into the branch, let CI re-run on the new head, then gate again; no judgment of this run can clear it",
		CodeBaseMoved, b.BaseRef, b.BaseSHA, b.HeadSHA, b.BehindBy, b.MergeBaseSHA, b.Status, b.BaseRef)}, nil
}

// headContainsBase is GitHub's compare saying the base's head is an ancestor of
// (or is) the judged head. Both fields must agree; any other status, including
// one this code does not know, is not containment. Fail closed.
func headContainsBase(b baseHeadEvidence) bool {
	return (b.Status == "ahead" || b.Status == "identical") && b.BehindBy == 0
}

// loadBaseHead decodes the recorded read. A body without the facts the answer
// rests on is an error: a read with no base head or no comparison must never
// fall through to either answer.
func loadBaseHead(st *state.Store, id string) (baseHeadEvidence, error) {
	a, err := st.Get(id)
	if err != nil {
		return baseHeadEvidence{}, err
	}
	var b baseHeadEvidence
	if err := json.Unmarshal(a.Body, &b); err != nil {
		return baseHeadEvidence{}, fmt.Errorf("verify: parse base evidence: %w", err)
	}
	if b.State == "" {
		return baseHeadEvidence{}, fmt.Errorf("verify: base evidence %s records no PR state", id)
	}
	if b.State != "MERGED" && (b.BaseSHA == "" || b.Status == "") {
		return baseHeadEvidence{}, fmt.Errorf("verify: base evidence %s records no base head or comparison", id)
	}
	return b, nil
}
