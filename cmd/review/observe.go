package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/contracts/reviewpanel"
	"github.com/itsHabib/workbench/contracts/reviewroute"
)

type observeOptions struct {
	plan string
	out  string
}

type rawReview struct {
	ID       int64  `json:"id"`
	State    string `json:"state"`
	CommitID string `json:"commit_id"`
	User     struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
}

type issueComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
}

type requestedReviewers struct {
	Users []struct {
		Login string `json:"login"`
	} `json:"users"`
}

func runObserve(
	ctx context.Context,
	args []string,
	runner commandRunner,
	stdout io.Writer,
) error {
	opts, err := parseObserveOptions(args)
	if err != nil {
		return err
	}
	var plan reviewroute.Plan
	if err := readArtifact(opts.plan, &plan); err != nil {
		return err
	}
	if err := reviewroute.ValidatePlan(plan); err != nil {
		return refusalError{err}
	}
	before, err := fetchPullRequest(ctx, runner, plan.Subject.Repo, plan.Subject.Number)
	if err != nil {
		return err
	}
	if !strings.EqualFold(before.HeadRefOID, plan.Subject.HeadSHA) {
		return refusalError{errors.New("review plan is stale before observation")}
	}
	reviews, err := fetchReviews(ctx, runner, plan.Subject)
	if err != nil {
		return err
	}
	comments, err := fetchIssueComments(ctx, runner, plan.Subject)
	if err != nil {
		return err
	}
	requested, err := fetchRequested(ctx, runner, plan.Subject)
	if err != nil {
		return err
	}
	evidence := buildPanel(plan, reviews, comments, requested)
	after, err := fetchPullRequest(ctx, runner, plan.Subject.Repo, plan.Subject.Number)
	if err != nil {
		return err
	}
	if !strings.EqualFold(before.HeadRefOID, after.HeadRefOID) {
		return refusalError{errors.New("pull request head changed during observation")}
	}
	if err := reviewpanel.Validate(evidence); err != nil {
		return refusalError{err}
	}
	return writeArtifact(opts.out, evidence, nil, stdout)
}

func parseObserveOptions(args []string) (observeOptions, error) {
	var opts observeOptions
	flags := flag.NewFlagSet("observe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.plan, "plan", "", "ReviewPlanV1 path")
	flags.StringVar(&opts.out, "out", "", "output ReviewPanelV1 path")
	if err := flags.Parse(args); err != nil {
		return observeOptions{}, err
	}
	if opts.plan == "" || opts.out == "" {
		return observeOptions{}, errors.New("-plan and -out are required")
	}
	return opts, nil
}

func fetchReviews(
	ctx context.Context,
	runner commandRunner,
	subject reviewroute.Subject,
) ([]rawReview, error) {
	endpoint := fmt.Sprintf("repos/%s/pulls/%d/reviews", subject.Repo, subject.Number)
	data, err := runner.Run(ctx, "gh", "api", "--paginate", "--slurp", endpoint)
	if err != nil {
		return nil, err
	}
	var pages [][]rawReview
	if err := json.Unmarshal(data, &pages); err != nil {
		return nil, err
	}
	var out []rawReview
	for _, page := range pages {
		out = append(out, page...)
	}
	return out, nil
}

func fetchIssueComments(
	ctx context.Context,
	runner commandRunner,
	subject reviewroute.Subject,
) ([]issueComment, error) {
	endpoint := fmt.Sprintf("repos/%s/issues/%d/comments", subject.Repo, subject.Number)
	data, err := runner.Run(ctx, "gh", "api", "--paginate", "--slurp", endpoint)
	if err != nil {
		return nil, err
	}
	var pages [][]issueComment
	if err := json.Unmarshal(data, &pages); err != nil {
		return nil, err
	}
	var out []issueComment
	for _, page := range pages {
		out = append(out, page...)
	}
	return out, nil
}

func fetchRequested(
	ctx context.Context,
	runner commandRunner,
	subject reviewroute.Subject,
) ([]string, error) {
	endpoint := fmt.Sprintf("repos/%s/pulls/%d/requested_reviewers",
		subject.Repo, subject.Number)
	data, err := runner.Run(ctx, "gh", "api", endpoint)
	if err != nil {
		return nil, err
	}
	var response requestedReviewers
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(response.Users))
	for _, user := range response.Users {
		out = append(out, user.Login)
	}
	return out, nil
}

func buildPanel(
	plan reviewroute.Plan,
	reviews []rawReview,
	comments []issueComment,
	requested []string,
) reviewpanel.Evidence {
	expected := append([]string{}, plan.Required...)
	sort.Strings(expected)
	evidence := reviewpanel.Evidence{
		SchemaVersion: reviewpanel.SchemaVersion,
		Subject: reviewpanel.Subject{
			Repo: plan.Subject.Repo, Number: plan.Subject.Number,
			HeadSHA: plan.Subject.HeadSHA,
		},
		Declaration: reviewpanel.Declaration{
			Path:     "ReviewPlanV1:" + plan.PlanID,
			Expected: expected,
		},
		Completed: []reviewpanel.Reviewer{},
		Pending:   []string{},
		Missing:   []string{},
		Unknown:   []string{},
	}
	if plan.Policy != nil {
		evidence.Declaration.Revision = fmt.Sprintf("%s@%d",
			plan.Policy.ID, plan.Policy.Revision)
	}
	for _, reviewer := range expected {
		if completed, ok := latestReview(reviewer, plan.Subject.HeadSHA, reviews); ok {
			evidence.Completed = append(evidence.Completed, completed)
			continue
		}
		if completed, ok := cleanComment(reviewer, plan.Subject.HeadSHA, comments); ok {
			evidence.Completed = append(evidence.Completed, completed)
			continue
		}
		if actorPresent(reviewer, requested) {
			evidence.Pending = append(evidence.Pending, reviewer)
			continue
		}
		evidence.Missing = append(evidence.Missing, reviewer)
	}
	return evidence
}

func latestReview(
	expected, head string,
	reviews []rawReview,
) (reviewpanel.Reviewer, bool) {
	for index := len(reviews) - 1; index >= 0; index-- {
		review := reviews[index]
		if !strings.EqualFold(review.CommitID, head) ||
			!completedReviewState(review.State) ||
			!actorMatches(expected, review.User.Login) {
			continue
		}
		return reviewpanel.Reviewer{
			Name: expected, Actor: review.User.Login,
			State: strings.ToUpper(review.State), HeadSHA: strings.ToLower(head),
			ReviewID: review.ID,
		}, true
	}
	return reviewpanel.Reviewer{}, false
}

func completedReviewState(state string) bool {
	switch strings.ToUpper(state) {
	case "APPROVED", "COMMENTED", "CHANGES_REQUESTED":
		return true
	}
	return false
}

var codexReviewedCommit = regexp.MustCompile(
	"(?m)^\\*\\*Reviewed commit:\\*\\* `([0-9a-fA-F]{7,40})`\\r?$",
)

func cleanComment(
	expected, head string,
	comments []issueComment,
) (reviewpanel.Reviewer, bool) {
	if expected != "codex" {
		return reviewpanel.Reviewer{}, false
	}
	for index := len(comments) - 1; index >= 0; index-- {
		comment := comments[index]
		if !actorMatches(expected, comment.User.Login) ||
			!strings.HasPrefix(comment.Body, "Codex Review: Didn't find any major issues.") {
			continue
		}
		match := codexReviewedCommit.FindStringSubmatch(comment.Body)
		if len(match) != 2 || !strings.HasPrefix(strings.ToLower(head), strings.ToLower(match[1])) {
			continue
		}
		return reviewpanel.Reviewer{
			Name: expected, Actor: comment.User.Login, State: "CLEAN",
			HeadSHA: strings.ToLower(head), ReviewID: comment.ID,
		}, true
	}
	return reviewpanel.Reviewer{}, false
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
		"codex":   {"codex", "chatgpt-codex-connector"},
		"cursor":  {"cursor"},
		"claude":  {"claude"},
		"copilot": {"copilot-pull-request-reviewer", "github-copilot"},
	}
	values := aliases[expected]
	if len(values) == 0 {
		values = []string{expected}
	}
	for _, value := range values {
		if actor == value {
			return true
		}
	}
	return false
}
