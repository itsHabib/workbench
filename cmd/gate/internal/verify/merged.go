package verify

import (
	"encoding/json"
	"fmt"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// MergedView is the slice of view evidence the already-merged refusal reads:
// whether GitHub reports the PR as MERGED, the merge commit that landed, and
// the head the evidence was gathered against.
type MergedView struct {
	Merged   bool
	MergeSHA string
	HeadSHA  string
}

// MergedPR reads the run's view evidence and reports whether the subject is
// already merged. A merged PR has no merge left to authorize, so the live gate
// verb refuses before the verifier ladder can run and park; the readiness
// verifier's MERGED exemptions stay untouched for replay (backtest), whose
// whole purpose is evaluating historical, merged PRs.
func MergedPR(st *state.Store, viewEvidenceID string) (MergedView, error) {
	a, err := st.Get(viewEvidenceID)
	if err != nil {
		return MergedView{}, err
	}
	var body struct {
		Data prView `json:"data"`
	}
	if err := json.Unmarshal(a.Body, &body); err != nil {
		return MergedView{}, fmt.Errorf("verify: parse view evidence: %w", err)
	}
	return MergedView{
		Merged:   body.Data.State == "MERGED",
		MergeSHA: body.Data.MergeCommit.Oid,
		HeadSHA:  body.Data.HeadRefOid,
	}, nil
}
