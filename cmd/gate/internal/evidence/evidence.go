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
	Author string `json:"author"`
	IsBot  bool   `json:"is_bot"`
	Path   string `json:"path,omitempty"`
	Line   int    `json:"line,omitempty"`
	Body   string `json:"body"`
	// CommitID is the head commit the comment was originally posted against
	// (original_commit_id). Empty for issue-level comments, which have no
	// commit anchor. Verifiers use it to tell a finding about the judged head
	// from one layered onto an earlier cycle.
	CommitID string `json:"commit_id,omitempty"`
	// Resolved reports whether the comment's review thread has been resolved
	// on GitHub. Always false for issue-level comments (no threads there).
	Resolved bool `json:"resolved,omitempty"`
}

type commentsBody struct {
	PR       PRRef     `json:"pr"`
	Comments []Comment `json:"comments"`
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

	comments, err := fetchComments(pr)
	if err != nil {
		return b, err
	}
	a, err = st.Append(state.KindEvidence, run, nil, commentsBody{PR: pr, Comments: comments})
	if err != nil {
		return b, err
	}
	b.Comments = a.ID
	return b, nil
}

type rawComment struct {
	ID   int64 `json:"id"`
	User struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
	Path             string `json:"path"`
	Line             *int   `json:"line"`
	OriginalLine     *int   `json:"original_line"`
	Body             string `json:"body"`
	OriginalCommitID string `json:"original_commit_id"`
}

const commentsPerPage = 100

// fetchComments pulls inline review comments plus issue-level comments —
// the two endpoints where the bot panel's findings land. Both are paged to
// exhaustion: the consolidator treats this artifact as the complete panel,
// so a truncated fetch would silently drop findings.
func fetchComments(pr PRRef) ([]Comment, error) {
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
			Author:   rc.User.Login,
			IsBot:    rc.User.Type == "Bot",
			Path:     rc.Path,
			Line:     lineOf(rc),
			Body:     rc.Body,
			CommitID: rc.OriginalCommitID,
			Resolved: resolved[rc.ID],
		})
	}
	for _, rc := range issue {
		out = append(out, Comment{
			Author: rc.User.Login,
			IsBot:  rc.User.Type == "Bot",
			Body:   rc.Body,
		})
	}
	return out, nil
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
// joined onto the REST comment fetch.
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
