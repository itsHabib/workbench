// Package evidence runs real reads against GitHub (via the authenticated gh
// CLI) and records what it saw as evidence artifacts. It never judges —
// verifiers downstream read these artifacts from state. Imports point down:
// state only.
package evidence

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/contracts/reviewpanel"
)

// PRRef names the subject under the gate.
type PRRef struct {
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

// Bundle holds the ids of the three evidence artifacts a gate run records.
type Bundle struct {
	View     string // gh pr view JSON (checks, draft, mergeable, review decision)
	Diff     string // unified diff
	Comments string // bot review comments (inline + issue-level)
	Panel    string // ReviewPanelV1 declaration + exact-head completion evidence
}

type viewBody struct {
	PR   PRRef           `json:"pr"`
	Data json.RawMessage `json:"data"`
}

type diffBody struct {
	PR   PRRef  `json:"pr"`
	Diff string `json:"diff"`
	// Provenance — reconstructable from state alone which path produced the
	// diff and which commits it spans. "api" = GitHub's merge-base diff;
	// "local-merge-base" = the oversized-PR fallback.
	Method    string `json:"method,omitempty"`
	Head      string `json:"head,omitempty"`
	MergeBase string `json:"merge_base,omitempty"`
}

// Comment is one review comment as verifiers will consume it.
type Comment struct {
	ID     int64  `json:"id,omitempty"`
	Author string `json:"author"`
	IsBot  bool   `json:"is_bot"`
	Path   string `json:"path,omitempty"`
	Line   int    `json:"line,omitempty"`
	Body   string `json:"body"`
	// CommitID is the commit the comment currently targets (GitHub re-anchors
	// live comments as the head moves). Empty for issue-level comments, which
	// have no commit anchor. Verifiers use it to tell a finding about the
	// judged head from one layered onto an earlier cycle.
	CommitID string `json:"commit_id,omitempty"`
	// Resolved reports whether the comment's review thread has been resolved
	// on GitHub. Always false for issue-level comments (no threads there).
	Resolved bool `json:"resolved,omitempty"`
}

type commentsBody struct {
	PR       PRRef     `json:"pr"`
	Comments []Comment `json:"comments"`
}

type reviewFetchers struct {
	reviews  func(PRRef) ([]rawComment, error)
	comments func(PRRef, []rawComment) ([]Comment, error)
	panel    func(PRRef, string, []rawComment, []Comment) reviewpanel.Evidence
}

// Gather records view, diff, and comments evidence for a PR and returns their ids.
func Gather(st *state.Store, run string, pr PRRef) (Bundle, error) {
	var b Bundle
	view, err := gh("pr", "view", fmt.Sprint(pr.Number), "-R", pr.Repo, "--json",
		"state,isDraft,mergeable,reviewDecision,statusCheckRollup,headRefOid,title,mergedAt")
	if err != nil {
		return b, err
	}
	a, err := st.Append(state.KindEvidence, run, nil, viewBody{PR: pr, Data: view})
	if err != nil {
		return b, err
	}
	b.View = a.ID
	var viewed struct {
		HeadRefOid string `json:"headRefOid"`
	}
	if err := json.Unmarshal(view, &viewed); err != nil {
		return b, fmt.Errorf("evidence: parse PR head: %w", err)
	}

	// method "api" records only that GitHub served the diff — not head/merge_base:
	// gh pr diff reads by PR number and doesn't report which head it rendered, so
	// stamping the view's head would claim a span this path never verified. The
	// fallback path controls exact SHAs and stamps them.
	body := diffBody{PR: pr, Method: "api"}
	diff, err := gh("pr", "diff", fmt.Sprint(pr.Number), "-R", pr.Repo)
	if tooLarge(err) {
		var r diffResult
		r, err = fallbackDiff(pr, view)
		body.Diff, body.Method, body.MergeBase, body.Head = r.Diff, "local-merge-base", r.MergeBase, r.Head
	}
	if err != nil {
		return b, err
	}
	if body.Method == "api" {
		body.Diff = string(diff)
	}
	a, err = st.Append(state.KindEvidence, run, nil, body)
	if err != nil {
		return b, err
	}
	b.Diff = a.ID

	comments, panel, err := fetchReviewEvidence(pr, viewed.HeadRefOid, reviewFetchers{
		reviews: fetchReviews, comments: fetchComments, panel: fetchPanel,
	})
	if err != nil {
		return b, err
	}
	a, err = st.Append(state.KindEvidence, run, nil, commentsBody{PR: pr, Comments: comments})
	if err != nil {
		return b, err
	}
	b.Comments = a.ID
	if err := reviewpanel.Validate(panel); err != nil {
		return b, err
	}
	a, err = st.Append(state.KindEvidence, run, nil, panel)
	if err != nil {
		return b, err
	}
	b.Panel = a.ID
	return b, nil
}

// fetchReviewEvidence takes the review-submission snapshot first. Both panel
// completion and findings then derive from that same set; comments are fetched
// afterward so an actionable inline finding belonging to a credited review
// cannot be absent merely because it appeared between two review snapshots.
func fetchReviewEvidence(pr PRRef, headSHA string, fetchers reviewFetchers) ([]Comment, reviewpanel.Evidence, error) {
	reviews, err := fetchers.reviews(pr)
	if err != nil {
		return nil, reviewpanel.Evidence{}, err
	}
	comments, err := fetchers.comments(pr, reviews)
	if err != nil {
		return nil, reviewpanel.Evidence{}, err
	}
	return comments, fetchers.panel(pr, headSHA, reviews, comments), nil
}

type rawComment struct {
	ID   int64 `json:"id"`
	User struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
	Path         string `json:"path"`
	Line         *int   `json:"line"`
	OriginalLine *int   `json:"original_line"`
	Body         string `json:"body"`
	// CommitID is the commit the comment currently targets — GitHub re-anchors
	// it as the PR head moves while the comment still applies. original_commit_id
	// is deliberately not used: it stays pinned to the head the comment was first
	// posted against, so a live comment on the judged head could read as stale.
	CommitID string `json:"commit_id"`
	// State is set only on review submissions (pulls/.../reviews): COMMENTED,
	// APPROVED, CHANGES_REQUESTED, DISMISSED. Empty for comments. A DISMISSED
	// review has been withdrawn and must not count as review evidence.
	State string `json:"state"`
}

const commentsPerPage = 100

// fetchComments pulls inline review comments plus issue-level comments —
// the two endpoints where the bot panel's findings land. Both are paged to
// exhaustion: the consolidator treats this artifact as the complete panel,
// so a truncated fetch would silently drop findings.
func fetchComments(pr PRRef, reviews []rawComment) ([]Comment, error) {
	resolved, err := fetchResolvedIDs(pr)
	if err != nil {
		return nil, err
	}
	inline, err := pagedComments(fmt.Sprintf("repos/%s/pulls/%d/comments", pr.Repo, pr.Number))
	if err != nil {
		return nil, err
	}
	issue, err := pagedComments(fmt.Sprintf("repos/%s/issues/%d/comments", pr.Repo, pr.Number))
	if err != nil {
		return nil, err
	}
	var out []Comment
	for _, rc := range inline {
		out = append(out, Comment{
			ID:       rc.ID,
			Author:   rc.User.Login,
			IsBot:    rc.User.Type == "Bot",
			Path:     rc.Path,
			Line:     lineOf(rc),
			Body:     rc.Body,
			CommitID: rc.CommitID,
			Resolved: resolved[rc.ID],
		})
	}
	for _, rc := range issue {
		out = append(out, Comment{
			ID:     rc.ID,
			Author: rc.User.Login,
			IsBot:  rc.User.Type == "Bot",
			Body:   rc.Body,
		})
	}
	out = append(out, reviewBodies(reviews)...)
	return out, nil
}

func fetchReviews(pr PRRef) ([]rawComment, error) {
	return pagedComments(fmt.Sprintf("repos/%s/pulls/%d/reviews", pr.Repo, pr.Number))
}

// reviewBodies reduces a PR's review submissions (oldest-first) to the review
// bodies worth judging: each author's LATEST non-dismissed review that carries a
// top-level body. A bot can submit several reviews without a new commit (request
// changes, then approve) — all sharing a commit_id, so staleComment cannot tell
// the superseded one apart; only the latest reflects the author's current stance.
// A DISMISSED review is withdrawn evidence; a body-less review (inline-only or a
// bare approval) adds nothing its inline comments don't already carry.
func reviewBodies(reviews []rawComment) []Comment {
	// The author's current stance is their latest NON-DISMISSED review — INCLUDING
	// a bare approval with no body: a later "APPROVED" supersedes an earlier
	// actionable body, so that finding must not keep escalating. Find that latest
	// review per author first (regardless of body), then emit it only if it
	// carries a body; a bodyless latest review (or one whose only later reviews
	// were dismissed) contributes nothing.
	latest := map[string]int{}
	for i, rv := range reviews {
		if rv.State == "DISMISSED" {
			continue
		}
		latest[rv.User.Login] = i
	}
	var out []Comment
	for i, rv := range reviews {
		if idx, ok := latest[rv.User.Login]; !ok || idx != i {
			continue
		}
		if strings.TrimSpace(rv.Body) == "" {
			continue
		}
		out = append(out, Comment{
			ID:       rv.ID,
			Author:   rv.User.Login,
			IsBot:    rv.User.Type == "Bot",
			Body:     rv.Body,
			CommitID: rv.CommitID,
		})
	}
	return out
}

func pagedComments(ep string) ([]rawComment, error) {
	var out []rawComment
	for page := 1; ; page++ {
		raw, err := gh("api", fmt.Sprintf("%s?per_page=%d&page=%d", ep, commentsPerPage, page))
		if err != nil {
			return nil, err
		}
		var rcs []rawComment
		if err := json.Unmarshal(raw, &rcs); err != nil {
			return nil, fmt.Errorf("evidence: parse comments: %w", err)
		}
		out = append(out, rcs...)
		if len(rcs) < commentsPerPage {
			return out, nil
		}
	}
}

// resolvedThreadsQuery pages a PR's review threads with each thread's
// resolution state and its comments' REST database ids, so resolution can be
// joined onto the REST comment fetch. The inner comments list is capped at its
// first 100 — un-paged on purpose: a longer thread's overflow comments simply
// aren't marked resolved, which fails safe (they stay in the panel and can
// only add a judgment call, never hide a finding).
const resolvedThreadsQuery = `query($owner:String!,$name:String!,$number:Int!,$cursor:String){
  repository(owner:$owner,name:$name){pullRequest(number:$number){
    reviewThreads(first:100,after:$cursor){
      pageInfo{hasNextPage endCursor}
      nodes{isResolved comments(first:100){nodes{fullDatabaseId}}}}}}}`

// fetchResolvedIDs returns the REST ids of every inline comment whose review
// thread is resolved. Resolution lives only on the GraphQL reviewThreads
// surface; the REST comments endpoints don't carry it, so the two fetches are
// joined by comment database id.
func fetchResolvedIDs(pr PRRef) (map[int64]bool, error) {
	owner, name, ok := strings.Cut(pr.Repo, "/")
	if !ok {
		return nil, fmt.Errorf("evidence: bad repo %q", pr.Repo)
	}
	out := make(map[int64]bool)
	cursor := ""
	for {
		args := []string{"api", "graphql",
			"-f", "query=" + resolvedThreadsQuery,
			"-f", "owner=" + owner,
			"-f", "name=" + name,
			"-F", fmt.Sprintf("number=%d", pr.Number),
		}
		if cursor != "" {
			args = append(args, "-f", "cursor="+cursor)
		}
		raw, err := gh(args...)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Data struct {
				Repository struct {
					PullRequest struct {
						ReviewThreads struct {
							PageInfo struct {
								HasNextPage bool   `json:"hasNextPage"`
								EndCursor   string `json:"endCursor"`
							} `json:"pageInfo"`
							Nodes []struct {
								IsResolved bool `json:"isResolved"`
								Comments   struct {
									Nodes []struct {
										FullDatabaseID string `json:"fullDatabaseId"`
									} `json:"nodes"`
								} `json:"comments"`
							} `json:"nodes"`
						} `json:"reviewThreads"`
					} `json:"pullRequest"`
				} `json:"repository"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("evidence: parse review threads: %w", err)
		}
		threads := resp.Data.Repository.PullRequest.ReviewThreads
		for _, th := range threads.Nodes {
			if !th.IsResolved {
				continue
			}
			for _, c := range th.Comments.Nodes {
				id, err := strconv.ParseInt(c.FullDatabaseID, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("evidence: parse thread comment id %q: %w", c.FullDatabaseID, err)
				}
				out[id] = true
			}
		}
		if !threads.PageInfo.HasNextPage {
			return out, nil
		}
		cursor = threads.PageInfo.EndCursor
	}
}

func lineOf(rc rawComment) int {
	if rc.Line != nil {
		return *rc.Line
	}
	if rc.OriginalLine != nil {
		return *rc.OriginalLine
	}
	return 0
}

func gh(args ...string) (json.RawMessage, error) {
	out, err := exec.Command("gh", args...).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("evidence: gh %v: %s", args[:2], ee.Stderr)
		}
		return nil, fmt.Errorf("evidence: gh %v: %w", args[:2], err)
	}
	return out, nil
}
