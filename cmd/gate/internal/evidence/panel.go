package evidence

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/contracts/reviewpanel"
)

const panelDeclarationPath = ".ship.json"

type contentResponse struct {
	SHA      string `json:"sha"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

type requestedResponse struct {
	Users []struct {
		Login string `json:"login"`
	} `json:"users"`
}

func fetchPanel(pr PRRef, headSHA string, reviews []rawComment, comments []Comment) reviewpanel.Evidence {
	panel := reviewpanel.Evidence{
		SchemaVersion: reviewpanel.SchemaVersion,
		Subject: reviewpanel.Subject{
			Repo: pr.Repo, Number: pr.Number, HeadSHA: headSHA,
		},
		Declaration: reviewpanel.Declaration{Path: panelDeclarationPath},
	}
	expected, revision, err := fetchExpectedReviewers(pr.Repo)
	if err != nil {
		panel.Unknown = []string{"declaration"}
		return panel
	}
	panel.Declaration.Expected = expected
	panel.Declaration.Revision = revision
	if len(expected) == 0 {
		panel.Unknown = []string{"declaration"}
		return panel
	}

	requested, err := fetchRequestedReviewers(pr)
	if err != nil {
		panel.Unknown = append([]string(nil), expected...)
		return panel
	}
	return classifyPanel(panel, reviews, requested, comments)
}

func fetchExpectedReviewers(repo string) ([]string, string, error) {
	raw, err := gh("api", fmt.Sprintf("repos/%s/contents/%s", repo, panelDeclarationPath))
	if err != nil {
		return nil, "", err
	}
	var content contentResponse
	if err := json.Unmarshal(raw, &content); err != nil {
		return nil, "", fmt.Errorf("evidence: parse panel declaration response: %w", err)
	}
	if content.Encoding != "base64" {
		return nil, "", fmt.Errorf("evidence: panel declaration encoding %q", content.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(content.Content, "\n", ""))
	if err != nil {
		return nil, "", fmt.Errorf("evidence: decode panel declaration: %w", err)
	}
	var declaration struct {
		Review struct {
			Require []string `json:"require"`
		} `json:"review"`
	}
	if err := json.Unmarshal(decoded, &declaration); err != nil {
		return nil, "", fmt.Errorf("evidence: parse panel declaration: %w", err)
	}
	expected := make([]string, 0, len(declaration.Review.Require))
	seen := make(map[string]struct{}, len(declaration.Review.Require))
	for _, name := range declaration.Review.Require {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			return nil, "", fmt.Errorf("evidence: panel declaration has blank reviewer")
		}
		if _, ok := seen[name]; ok {
			return nil, "", fmt.Errorf("evidence: panel declaration has duplicate reviewer %q", name)
		}
		seen[name] = struct{}{}
		expected = append(expected, name)
	}
	sort.Strings(expected)
	return expected, content.SHA, nil
}

func fetchRequestedReviewers(pr PRRef) ([]string, error) {
	raw, err := gh("api", fmt.Sprintf("repos/%s/pulls/%d/requested_reviewers", pr.Repo, pr.Number))
	if err != nil {
		return nil, err
	}
	var response requestedResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("evidence: parse requested reviewers: %w", err)
	}
	out := make([]string, 0, len(response.Users))
	for _, user := range response.Users {
		out = append(out, user.Login)
	}
	return out, nil
}

func classifyPanel(panel reviewpanel.Evidence, reviews []rawComment, requested []string, comments []Comment) reviewpanel.Evidence {
	for _, expected := range panel.Declaration.Expected {
		if reviewer, ok := panelCompletion(expected, panel.Subject.HeadSHA, reviews, comments); ok {
			panel.Completed = append(panel.Completed, reviewer)
			continue
		}
		if actorPresent(expected, requested) {
			panel.Pending = append(panel.Pending, expected)
			continue
		}
		panel.Missing = append(panel.Missing, expected)
	}
	return panel
}

// panelCompletion reports the head-bound evidence that satisfies one expected
// reviewer, in descending order of directness: the reviewer's own formal
// exact-head review, then the two structured sentinels that stand in for one
// when a provider only ever posts issue comments. Provider prose is never
// evidence here — each sentinel is emitted by a harness, not by a model.
func panelCompletion(expected, headSHA string, reviews []rawComment, comments []Comment) (reviewpanel.Reviewer, bool) {
	if review, ok := latestExactHeadReview(expected, headSHA, reviews); ok {
		return reviewpanel.Reviewer{
			Name: expected, Actor: review.User.Login, State: review.State,
			HeadSHA: review.CommitID, ReviewID: review.ID,
		}, true
	}
	if comment, ok := codexCleanCompletion(expected, headSHA, comments); ok {
		return reviewpanel.Reviewer{
			Name: expected, Actor: comment.Author, State: "CLEAN",
			HeadSHA: headSHA, ReviewID: comment.ID,
		}, true
	}
	if comment, ok := workflowAttestation(expected, headSHA, comments); ok {
		return reviewpanel.Reviewer{
			Name: expected, Actor: comment.Author, State: "COMMENTED",
			HeadSHA: headSHA, ReviewID: comment.ID,
		}, true
	}
	return reviewpanel.Reviewer{}, false
}

var codexReviewedCommit = regexp.MustCompile("(?m)^\\*\\*Reviewed commit:\\*\\* `([0-9a-f]{10})`\\r?$")

func codexCleanCompletion(expected, headSHA string, comments []Comment) (Comment, bool) {
	if expected != "codex" {
		return Comment{}, false
	}
	for i := len(comments) - 1; i >= 0; i-- {
		comment := comments[i]
		if comment.Author != "chatgpt-codex-connector[bot]" || !comment.IsBot ||
			comment.CommitID != "" || comment.Path != "" ||
			!strings.HasPrefix(comment.Body, "Codex Review: Didn't find any major issues.") {
			continue
		}
		match := codexReviewedCommit.FindStringSubmatch(comment.Body)
		if len(match) == 2 && strings.HasPrefix(headSHA, match[1]) {
			return comment, true
		}
	}
	return Comment{}, false
}

// attestationAuthor is the only actor whose attestation counts: the repository's
// own Actions token. A human, a PR author, or any other bot posting the same
// text is not this login, and the login cannot be spoofed on a comment.
const attestationAuthor = "github-actions[bot]"

// attestationMarker opens the body of a review attestation. Requiring it as a
// strict prefix keeps the sentinel out of reach of prose that merely quotes the
// two fields below — a review discussing this very feature must not clear a panel.
const attestationMarker = "<!-- gate:review-attestation -->"

// attestationFields pins the reviewer and the reviewed head to two adjacent
// lines, so no free text can drift between the name and the commit it claims.
// The commit is the full 40-hex SHA and must equal the judged head exactly.
var attestationFields = regexp.MustCompile("(?m)^\\*\\*Reviewer:\\*\\* ([a-z0-9-]{1,40})\\r?\\n\\*\\*Reviewed commit:\\*\\* `([0-9a-f]{40})`\\r?$")

// workflowAttestation reports a repository-workflow attestation that `expected`
// reviewed exactly headSHA.
//
// Some providers — claude[bot] today — publish their review only as an issue
// comment, which carries no commit anchor, and the body is model prose that Gate
// refuses as authority on principle. The authority instead belongs to the
// workflow that produced the review: it checked out a specific head and ran the
// reviewer against exactly that tree, so it can state the head as fact. This is
// the same structural role the Codex connector's reviewed-commit line plays —
// a harness-emitted, head-bound sentinel, not a provider's self-description.
func workflowAttestation(expected, headSHA string, comments []Comment) (Comment, bool) {
	for i := len(comments) - 1; i >= 0; i-- {
		comment := comments[i]
		if comment.Author != attestationAuthor || !comment.IsBot ||
			comment.CommitID != "" || comment.Path != "" ||
			!strings.HasPrefix(comment.Body, attestationMarker) {
			continue
		}
		match := attestationFields.FindStringSubmatch(comment.Body)
		if len(match) == 3 && match[1] == expected && match[2] == headSHA {
			return comment, true
		}
	}
	return Comment{}, false
}

func latestExactHeadReview(expected, headSHA string, reviews []rawComment) (rawComment, bool) {
	for i := len(reviews) - 1; i >= 0; i-- {
		review := reviews[i]
		if review.User.Type != "Bot" || !completedReviewState(review.State) ||
			review.CommitID != headSHA {
			continue
		}
		if actorMatches(expected, review.User.Login) {
			return review, true
		}
	}
	return rawComment{}, false
}

func completedReviewState(state string) bool {
	return state == "APPROVED" || state == "COMMENTED" || state == "CHANGES_REQUESTED"
}

func actorPresent(expected string, actors []string) bool {
	for _, actor := range actors {
		if actorMatches(expected, actor) {
			return true
		}
	}
	return false
}

func actorMatches(expected, actor string) bool {
	actor = strings.ToLower(strings.TrimSuffix(actor, "[bot]"))
	aliases := map[string][]string{
		"codex":      {"codex", "chatgpt-codex-connector"},
		"cursor":     {"cursor"},
		"claude":     {"claude"},
		"copilot":    {"copilot-pull-request-reviewer", "github-copilot"},
		"coderabbit": {"coderabbitai"},
	}
	values, ok := aliases[expected]
	if !ok {
		values = []string{expected}
	}
	for _, value := range values {
		if actor == value {
			return true
		}
	}
	return false
}
