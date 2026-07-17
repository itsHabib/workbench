// Package evidence runs real reads against GitHub (via the authenticated gh
// CLI) and records what it saw as evidence artifacts. It never judges —
// verifiers downstream read these artifacts from state. Imports point down:
// state only.
package evidence

import (
	"encoding/json"
	"fmt"
	"os/exec"

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
	User struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
	Path         string `json:"path"`
	Line         *int   `json:"line"`
	OriginalLine *int   `json:"original_line"`
	Body         string `json:"body"`
}

const commentsPerPage = 100

// fetchComments pulls inline review comments plus issue-level comments —
// the two endpoints where the bot panel's findings land. Both are paged to
// exhaustion: the consolidator treats this artifact as the complete panel,
// so a truncated fetch would silently drop findings.
func fetchComments(pr PRRef) ([]Comment, error) {
	var out []Comment
	endpoints := []string{
		fmt.Sprintf("repos/%s/pulls/%d/comments", pr.Repo, pr.Number),
		fmt.Sprintf("repos/%s/issues/%d/comments", pr.Repo, pr.Number),
	}
	for _, ep := range endpoints {
		for page := 1; ; page++ {
			raw, err := gh("api", fmt.Sprintf("%s?per_page=%d&page=%d", ep, commentsPerPage, page))
			if err != nil {
				return nil, err
			}
			var rcs []rawComment
			if err := json.Unmarshal(raw, &rcs); err != nil {
				return nil, fmt.Errorf("evidence: parse comments: %w", err)
			}
			for _, rc := range rcs {
				out = append(out, Comment{
					Author: rc.User.Login,
					IsBot:  rc.User.Type == "Bot",
					Path:   rc.Path,
					Line:   lineOf(rc),
					Body:   rc.Body,
				})
			}
			if len(rcs) < commentsPerPage {
				break
			}
		}
	}
	return out, nil
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
