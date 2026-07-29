package evidence

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
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
		if review, ok := latestExactHeadReview(expected, panel.Subject.HeadSHA, reviews); ok {
			panel.Completed = append(panel.Completed, reviewpanel.Reviewer{
				Name: expected, Actor: review.User.Login, State: review.State,
				HeadSHA: review.CommitID, ReviewID: review.ID,
			})
			continue
		}
		if comment, ok := cleanIssueCompletion(expected, panel.Subject.HeadSHA, comments); ok {
			panel.Completed = append(panel.Completed, reviewpanel.Reviewer{
				Name: expected, Actor: comment.Author, State: "CLEAN",
				HeadSHA: commentHead(comment, panel.Subject.HeadSHA), ReviewID: comment.ID,
			})
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

var codexReviewedCommit = regexp.MustCompile("(?m)^\\*\\*Reviewed commit:\\*\\* `([0-9a-f]{10})`\\r?$")
var claudeReviewHeading = regexp.MustCompile(`(?m)^### (?:Code|PR) Review(?:[ (].*)?\r?$`)
var claudeCleanVerdict = regexp.MustCompile(`(?m)^(?:LGTM\. Ready to merge\.|No (?:new )?(?:issues|findings)(?: found)?\. Ready to merge\.)\r?$`)
var claudeActionable = regexp.MustCompile(`(?mi)(?:^\s*#{1,6}\s+(?:finding|issue|bug)\b|\[Fix this(?: →)?\])`)

func cleanIssueCompletion(expected, headSHA string, comments []Comment) (Comment, bool) {
	switch expected {
	case "codex":
		return codexCleanCompletion(headSHA, comments)
	case "claude":
		return claudeCleanCompletion(headSHA, comments)
	}
	return Comment{}, false
}

func codexCleanCompletion(headSHA string, comments []Comment) (Comment, bool) {
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

func claudeCleanCompletion(headSHA string, comments []Comment) (Comment, bool) {
	for i := len(comments) - 1; i >= 0; i-- {
		comment := comments[i]
		if comment.Author != "claude[bot]" || !comment.IsBot ||
			comment.Path != "" || comment.CommitID != headSHA ||
			!strings.HasPrefix(comment.Body, "**Claude finished ") ||
			!claudeReviewHeading.MatchString(comment.Body) ||
			claudeActionable.MatchString(comment.Body) ||
			!claudeCleanVerdict.MatchString(comment.Body) {
			continue
		}
		return comment, true
	}
	return Comment{}, false
}

func commentHead(comment Comment, fallback string) string {
	if comment.CommitID != "" {
		return comment.CommitID
	}
	return fallback
}

type workflowRunResponse struct {
	ID             int64  `json:"id"`
	HeadSHA        string `json:"head_sha"`
	Event          string `json:"event"`
	Conclusion     string `json:"conclusion"`
	HeadRepository struct {
		FullName string `json:"full_name"`
	} `json:"head_repository"`
}

type workflowRunFetcher func(string, int64) (workflowRunResponse, error)

func bindClaudeIssueHeads(repo string, comments []rawComment, fetch workflowRunFetcher) error {
	for i := len(comments) - 1; i >= 0; i-- {
		comment := &comments[i]
		if comment.User.Login != "claude[bot]" || comment.User.Type != "Bot" ||
			comment.Path != "" || comment.CommitID != "" ||
			!strings.HasPrefix(comment.Body, "**Claude finished ") {
			continue
		}
		// The latest Claude response is its current stance. Never fall back to
		// an older clean result when the latest is actionable or malformed.
		if !claudeReviewHeading.MatchString(comment.Body) ||
			claudeActionable.MatchString(comment.Body) ||
			!claudeCleanVerdict.MatchString(comment.Body) {
			return nil
		}
		runID, ok := claudeRunID(repo, comment.Body)
		if !ok {
			return nil
		}
		run, err := fetch(repo, runID)
		if err != nil {
			return err
		}
		if run.ID != runID || run.Event != "issue_comment" ||
			run.Conclusion != "success" ||
			run.HeadRepository.FullName != repo ||
			run.HeadSHA == "" {
			return nil
		}
		comment.CommitID = run.HeadSHA
		return nil
	}
	return nil
}

func claudeRunID(repo, body string) (int64, bool) {
	pattern := regexp.MustCompile(`https://github\.com/` + regexp.QuoteMeta(repo) + `/actions/runs/([0-9]+)`)
	matches := pattern.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return 0, false
	}
	id, err := strconv.ParseInt(matches[0][1], 10, 64)
	if err != nil {
		return 0, false
	}
	for _, match := range matches[1:] {
		if match[1] != matches[0][1] {
			return 0, false
		}
	}
	return id, true
}

func fetchWorkflowRun(repo string, runID int64) (workflowRunResponse, error) {
	raw, err := gh("api", fmt.Sprintf("repos/%s/actions/runs/%d", repo, runID))
	if err != nil {
		return workflowRunResponse{}, err
	}
	var run workflowRunResponse
	if err := json.Unmarshal(raw, &run); err != nil {
		return workflowRunResponse{}, fmt.Errorf("evidence: parse Claude workflow run: %w", err)
	}
	return run, nil
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
