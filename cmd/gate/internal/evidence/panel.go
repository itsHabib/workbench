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
	// Deliberately omit a ref: GitHub serves the default branch, so a PR cannot lower its own review bar.
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
	if comment, state, ok := codexIssueCompletion(expected, headSHA, comments); ok {
		return reviewpanel.Reviewer{
			Name: expected, Actor: comment.Author, State: state,
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

// codexReviewPrefix opens every review the Codex connector posts, clean or not.
// It is the connector harness's own framing, not model prose the review can
// choose: a comment lacking it is not a Codex review submission at all.
const codexReviewPrefix = "Codex Review:"

// codexCleanPrefix additionally opens the no-findings variant. It selects the
// recorded state, never whether the review counts as completed.
const codexCleanPrefix = "Codex Review: Didn't find any major issues."

// codexIssueCompletion reports the Codex connector's latest head-bound review
// posted as an issue comment, and the panel state to record for it.
//
// Completion is not cleanliness. The panel asks one question — did this
// reviewer review THIS head — and the connector's harness-emitted
// reviewed-commit line answers it whether or not the review found anything.
// Requiring the clean opener conflated the two and parked the gate on evidence
// SHAPE: a Codex review that did its job and reported findings read as "codex
// never reviewed this head". Findings are a separate fact, extracted by the
// review-consolidation verifier from the same comments, and they still park
// the run for judgment on their own merits.
//
// The head anchor is the connector's line, never the review's words. A comment
// saying "Approved", "LGTM", or "ready to merge" without that line completes
// nothing — prose is not authority here, and a verdict with no commit anchor
// cannot state which tree it applies to.
func codexIssueCompletion(expected, headSHA string, comments []Comment) (Comment, string, bool) {
	if expected != "codex" {
		return Comment{}, "", false
	}
	for i := len(comments) - 1; i >= 0; i-- {
		comment := comments[i]
		if comment.Author != "chatgpt-codex-connector[bot]" || !comment.IsBot ||
			comment.CommitID != "" || comment.Path != "" ||
			!strings.HasPrefix(comment.Body, codexReviewPrefix) {
			continue
		}
		match := codexReviewedCommit.FindStringSubmatch(comment.Body)
		if len(match) != 2 || !strings.HasPrefix(headSHA, match[1]) {
			continue
		}
		if strings.HasPrefix(comment.Body, codexCleanPrefix) {
			return comment, "CLEAN", true
		}
		return comment, "COMMENTED", true
	}
	return Comment{}, "", false
}

// attestationAuthor is the only actor whose attestation counts: the repository's
// own Actions token. A human, a PR author, or any other bot posting the same
// text is not this login, and the login cannot be spoofed on a comment.
const attestationAuthor = "github-actions[bot]"

// attestationMarker opens the body of a review attestation. It is quoted here
// for the workflow that emits it; attestationBody is what actually matches.
const attestationMarker = "<!-- gate:review-attestation -->"

// attestationBody matches a whole attestation and nothing else: the marker, the
// reviewer, and the reviewed head, on three consecutive lines from the first
// byte of the comment, with only trailing whitespace permitted after. Matching
// the entire body rather than scanning it for fields is what keeps the sentinel
// out of reach of prose — a review quoting this very format, at any offset and
// with any amount of surrounding text, cannot clear a panel. The commit is the
// full 40-hex SHA and must equal the judged head exactly.
var attestationBody = regexp.MustCompile(
	"\\A" + regexp.QuoteMeta(attestationMarker) +
		"\\r?\\n\\*\\*Reviewer:\\*\\* ([a-z0-9-]{1,40})" +
		"\\r?\\n\\*\\*Reviewed commit:\\*\\* `([0-9a-f]{40})`\\s*\\z")

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
			comment.CommitID != "" || comment.Path != "" {
			continue
		}
		match := attestationBody.FindStringSubmatch(comment.Body)
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
