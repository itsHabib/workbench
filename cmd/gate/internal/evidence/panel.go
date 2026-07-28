package evidence

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
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

func fetchPanel(pr PRRef, headSHA string) reviewpanel.Evidence {
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

	reviews, err := pagedComments(fmt.Sprintf("repos/%s/pulls/%d/reviews", pr.Repo, pr.Number))
	if err != nil {
		panel.Unknown = append([]string(nil), expected...)
		return panel
	}
	requested, err := fetchRequestedReviewers(pr)
	if err != nil {
		panel.Unknown = append([]string(nil), expected...)
		return panel
	}
	return classifyPanel(panel, reviews, requested)
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

func classifyPanel(panel reviewpanel.Evidence, reviews []rawComment, requested []string) reviewpanel.Evidence {
	for _, expected := range panel.Declaration.Expected {
		if review, ok := latestExactHeadReview(expected, panel.Subject.HeadSHA, reviews); ok {
			panel.Completed = append(panel.Completed, reviewpanel.Reviewer{
				Name: expected, Actor: review.User.Login, State: review.State,
				HeadSHA: review.CommitID, ReviewID: review.ID,
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
