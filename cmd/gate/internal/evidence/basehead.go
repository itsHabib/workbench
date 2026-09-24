package evidence

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// A merge lands merge(head, base), where base is the base branch's head at the
// moment the merge runs — not the commit the head was cut from, and not the
// base any CI run happened to merge against. CI evidence on the head covers
// that tree only when the head already contains the base's head: the merge's
// tree is then the head's own tree, which every check on the head ran against.
//
// GitHub records nothing that would prove more. A pull request's baseRefOid and
// a workflow run's pull_requests[].base.sha both keep the base the PR was opened
// against (ivy#115 still named its 2026-09-09 fork point when it merged on
// 2026-09-23, seven merges later), and a pull_request run's check runs carry the
// PR head, never the merge commit the job checked out. So this file records the
// one fact that decides the question without that record: the base branch's
// live head, and whether the judged head contains it. It records the fact;
// verify decides what the fact costs.

// BaseHeadBody is the recorded read of a pull request's base branch against the
// head gate judged.
type BaseHeadBody struct {
	PR PRRef `json:"pr"`
	// State is the PR's live state: OPEN, CLOSED, or MERGED.
	State string `json:"state"`
	// BaseRef is the branch the PR targets at read time, so a retarget since
	// the evidence sweep is what gets compared.
	BaseRef string `json:"base_ref"`
	// BaseSHA is BaseRef's head at read time.
	BaseSHA string `json:"base_sha"`
	// HeadSHA is the judged head the comparison ran against — never the PR's
	// live head, which may have moved since the judgment.
	HeadSHA string `json:"head_sha"`
	// MergeBaseSHA, Status, and BehindBy are GitHub's BaseSHA...HeadSHA
	// compare. Status "ahead" or "identical" means the head contains the base's
	// head; BehindBy counts the base commits it does not contain. Unset for a
	// merged PR, whose merge is already history.
	MergeBaseSHA string `json:"merge_base_sha,omitempty"`
	Status       string `json:"status,omitempty"`
	BehindBy     int    `json:"behind_by"`
}

// ghRead is one authenticated gh invocation's stdout. Split out so tests
// exercise the read without a network or an authenticated gh.
type ghRead func(args ...string) (json.RawMessage, error)

// BaseHead reads the PR's base branch as it stands now, compares it with the
// judged head, and records the read as evidence, returning the artifact id.
//
// Unlike the protection read, a failed read is an error, never degraded
// evidence: the caller is about to authorize a merge, and "could not see the
// base" must not read as "the base did not move".
func BaseHead(st *state.Store, run string, pr PRRef, headSHA string) (string, error) {
	return baseHeadFrom(st, run, pr, headSHA, gh)
}

func baseHeadFrom(st *state.Store, run string, pr PRRef, headSHA string, read ghRead) (string, error) {
	body, err := readBaseHead(pr, headSHA, read)
	if err != nil {
		return "", err
	}
	a, err := st.Append(state.KindEvidence, run, nil, body)
	if err != nil {
		return "", err
	}
	return a.ID, nil
}

// liveBaseQuery asks for the base branch's CURRENT head through the ref object
// (baseRef.target.oid), never baseRefOid, which is the stale value this file
// exists to route around.
const liveBaseQuery = `query($owner:String!,$name:String!,$number:Int!){
  repository(owner:$owner,name:$name){pullRequest(number:$number){
    state baseRefName baseRef{target{oid}}}}}`

func readBaseHead(pr PRRef, headSHA string, read ghRead) (BaseHeadBody, error) {
	body := BaseHeadBody{PR: pr, HeadSHA: headSHA}
	if !reSHA.MatchString(headSHA) {
		return body, fmt.Errorf("evidence: base head: judged head %q is not a commit id", headSHA)
	}
	owner, name, ok := strings.Cut(pr.Repo, "/")
	if !ok {
		return body, fmt.Errorf("evidence: bad repo %q", pr.Repo)
	}
	raw, err := read("api", "graphql",
		"-f", "query="+liveBaseQuery,
		"-f", "owner="+owner,
		"-f", "name="+name,
		"-F", fmt.Sprintf("number=%d", pr.Number),
	)
	if err != nil {
		return body, err
	}
	if err := parseLiveBase(raw, &body); err != nil {
		return body, err
	}
	if body.State == "MERGED" {
		return body, nil
	}
	raw, err = read("api", fmt.Sprintf("repos/%s/compare/%s...%s?per_page=1", pr.Repo, body.BaseSHA, headSHA))
	if err != nil {
		return body, err
	}
	return body, parseBaseCompare(raw, &body)
}

// parseLiveBase fills the PR's state and live base from the GraphQL read. A
// merged PR may have lost its base branch since; any other state needs a real
// base head, or there is nothing to compare the judged head against.
func parseLiveBase(raw json.RawMessage, body *BaseHeadBody) error {
	var resp struct {
		Data struct {
			Repository struct {
				PullRequest *struct {
					State       string `json:"state"`
					BaseRefName string `json:"baseRefName"`
					BaseRef     *struct {
						Target struct {
							OID string `json:"oid"`
						} `json:"target"`
					} `json:"baseRef"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("evidence: parse live base: %w", err)
	}
	pull := resp.Data.Repository.PullRequest
	if pull == nil || pull.State == "" {
		return fmt.Errorf("evidence: live base: %s#%d not found", body.PR.Repo, body.PR.Number)
	}
	body.State, body.BaseRef = pull.State, pull.BaseRefName
	if pull.BaseRef != nil {
		body.BaseSHA = pull.BaseRef.Target.OID
	}
	if body.State == "MERGED" {
		return nil
	}
	if !reSHA.MatchString(body.BaseSHA) {
		return fmt.Errorf("evidence: live base: branch %q of %s#%d has no readable head (%q)",
			body.BaseRef, body.PR.Repo, body.PR.Number, body.BaseSHA)
	}
	return nil
}

// parseBaseCompare fills the comparison. Every field is required: an absent
// behind_by decoding to zero would read as "the head contains the base", which
// is exactly the claim an unread fact must never make.
func parseBaseCompare(raw json.RawMessage, body *BaseHeadBody) error {
	var c struct {
		Status          string `json:"status"`
		BehindBy        *int   `json:"behind_by"`
		MergeBaseCommit struct {
			SHA string `json:"sha"`
		} `json:"merge_base_commit"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return fmt.Errorf("evidence: parse base compare: %w", err)
	}
	if c.Status == "" || c.BehindBy == nil || !reSHA.MatchString(c.MergeBaseCommit.SHA) {
		return fmt.Errorf("evidence: base compare %.12s...%.12s is missing status, behind_by, or merge base",
			body.BaseSHA, body.HeadSHA)
	}
	body.Status, body.BehindBy, body.MergeBaseSHA = c.Status, *c.BehindBy, c.MergeBaseCommit.SHA
	return nil
}
