// Command review plans, requests, and decides exact-head tier-aware review.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/review/internal/policy"
	"github.com/itsHabib/workbench/contracts/reviewroute"
	"github.com/itsHabib/workbench/local"
)

const (
	exitOK      = 0
	exitRefused = 3
	exitError   = 4
)

type commandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
	RunInput(context.Context, []byte, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return runCommand(ctx, nil, name, args...)
}

func (execRunner) RunInput(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	return runCommand(ctx, input, name, args...)
}

func runCommand(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	if input != nil {
		command.Stdin = bytes.NewReader(input)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, errors.New(detail)
	}
	return output, nil
}

type refusalError struct{ err error }

func (err refusalError) Error() string { return err.err.Error() }
func (err refusalError) Unwrap() error { return err.err }

func main() {
	os.Exit(run(context.Background(), os.Args[1:], execRunner{}, os.Stdout, os.Stderr))
}

func run(
	ctx context.Context,
	args []string,
	runner commandRunner,
	stdout, stderr io.Writer,
) int {
	if len(args) == 0 {
		usage(stderr)
		return exitError
	}
	var err error
	switch args[0] {
	case "plan":
		err = runPlan(ctx, args[1:], runner, stdout)
	case "request":
		err = runRequest(ctx, args[1:], runner, stdout)
	case "observe":
		err = runObserve(ctx, args[1:], runner, stdout)
	case "decide":
		err = runDecide(ctx, args[1:], runner, stdout)
	case "advise":
		err = runAdvise(ctx, args[1:], stdout)
	default:
		usage(stderr)
		return exitError
	}
	if err == nil {
		return exitOK
	}
	fmt.Fprintln(stderr, err)
	var refused refusalError
	if errors.As(err, &refused) {
		return exitRefused
	}
	return exitError
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: review <plan|request|observe|decide|advise> [options]")
}

type planOptions struct {
	repo      string
	pr        int
	head      string
	policy    string
	out       string
	triageBin string
}

func runPlan(
	ctx context.Context,
	args []string,
	runner commandRunner,
	stdout io.Writer,
) error {
	opts, err := parsePlanOptions(args)
	if err != nil {
		return err
	}
	before, err := fetchPullRequest(ctx, runner, opts.repo, opts.pr)
	if err != nil {
		return fmt.Errorf("read pull request before classification: %w", err)
	}
	if !strings.EqualFold(before.State, "OPEN") {
		return refusalError{fmt.Errorf("pull request is %s", before.State)}
	}
	subject := reviewroute.Subject{
		Repo: strings.ToLower(opts.repo), Number: opts.pr,
		HeadSHA: strings.ToLower(before.HeadRefOID),
	}
	now := time.Now().UTC()
	if !strings.EqualFold(opts.head, before.HeadRefOID) {
		plan, planErr := policy.Fallback(subject, nil, now,
			"full_panel_fallback", "requested head is stale")
		return writeArtifact(opts.out, plan, planErr, stdout)
	}

	classification, classifyErr := classify(ctx, runner, opts)
	after, err := fetchPullRequest(ctx, runner, opts.repo, opts.pr)
	if err != nil {
		return fmt.Errorf("read pull request after classification: %w", err)
	}
	subject.HeadSHA = strings.ToLower(after.HeadRefOID)
	if !strings.EqualFold(before.HeadRefOID, after.HeadRefOID) {
		plan, planErr := policy.Fallback(subject, classification, now,
			"full_panel_fallback", "pull request head changed during classification")
		return writeArtifact(opts.out, plan, planErr, stdout)
	}
	if classifyErr != nil {
		plan, planErr := policy.Fallback(subject, nil, now,
			"full_panel_fallback", classifyErr.Error())
		return writeArtifact(opts.out, plan, planErr, stdout)
	}

	config, configErr := readPolicy(opts.policy)
	if configErr != nil {
		plan, planErr := policy.Fallback(subject, classification, now,
			"full_panel_fallback", "invalid review policy: "+configErr.Error())
		return writeArtifact(opts.out, plan, planErr, stdout)
	}
	plan, err := policy.Route(config, subject, *classification, now)
	return writeArtifact(opts.out, plan, err, stdout)
}

func parsePlanOptions(args []string) (planOptions, error) {
	var opts planOptions
	flags := flag.NewFlagSet("plan", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.repo, "repo", "", "owner/repo")
	flags.IntVar(&opts.pr, "pr", 0, "pull request number")
	flags.StringVar(&opts.head, "head", "", "expected exact head SHA")
	flags.StringVar(&opts.policy, "policy", "", "optional ReviewPolicyV1 override path")
	flags.StringVar(&opts.out, "out", "", "output ReviewPlanV1 path")
	flags.StringVar(&opts.triageBin, "triage-bin", "triage-floor", "triage-floor binary")
	if err := flags.Parse(args); err != nil {
		return planOptions{}, err
	}
	switch {
	case opts.repo == "":
		return planOptions{}, errors.New("-repo is required")
	case opts.pr < 1:
		return planOptions{}, errors.New("-pr must be positive")
	case opts.head == "":
		return planOptions{}, errors.New("-head is required")
	case opts.out == "":
		return planOptions{}, errors.New("-out is required")
	}
	return opts, nil
}

type pullRequest struct {
	HeadRefOID string `json:"headRefOid"`
	State      string `json:"state"`
}

func fetchPullRequest(
	ctx context.Context,
	runner commandRunner,
	repo string,
	pr int,
) (pullRequest, error) {
	data, err := runner.Run(ctx, "gh", "pr", "view", strconv.Itoa(pr),
		"-R", repo, "--json", "headRefOid,state")
	if err != nil {
		return pullRequest{}, err
	}
	var result pullRequest
	if err := json.Unmarshal(data, &result); err != nil {
		return pullRequest{}, fmt.Errorf("decode pull request: %w", err)
	}
	if result.HeadRefOID == "" {
		return pullRequest{}, errors.New("pull request has no head")
	}
	return result, nil
}

type floorResult struct {
	Floor   string `json:"floor"`
	Signals []struct {
		Signal string `json:"signal"`
		Tier   string `json:"tier"`
		Why    string `json:"why"`
	} `json:"signals"`
	Files   int `json:"files"`
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

func classify(
	ctx context.Context,
	runner commandRunner,
	opts planOptions,
) (*reviewroute.Classification, error) {
	diff, err := runner.Run(ctx, "gh", "pr", "diff", strconv.Itoa(opts.pr), "-R", opts.repo)
	if err != nil {
		return nil, fmt.Errorf("read pull request diff: %w", err)
	}
	output, err := runner.RunInput(ctx, diff, opts.triageBin, "-repo", opts.repo)
	if err != nil {
		return nil, fmt.Errorf("triage-floor failed: %w", err)
	}
	var result floorResult
	if err := strictJSON(output, &result); err != nil {
		return nil, fmt.Errorf("triage-floor output is invalid: %w", err)
	}
	if !validTier(result.Floor) || result.Files < 0 || result.Added < 0 || result.Removed < 0 {
		return nil, errors.New("triage-floor output is incomplete")
	}
	reasons := make([]string, 0, len(result.Signals))
	for _, signal := range result.Signals {
		if strings.TrimSpace(signal.Signal) == "" ||
			!validTier(signal.Tier) || strings.TrimSpace(signal.Why) == "" {
			return nil, errors.New("triage-floor signal is incomplete")
		}
		reasons = append(reasons, signal.Why)
	}
	return &reviewroute.Classification{
		Tier: result.Floor, Reasons: sortedUnique(reasons),
	}, nil
}

func readPolicy(path string) (reviewroute.Policy, error) {
	data := defaultPolicyBytes
	if path != "" {
		var err error
		data, err = os.ReadFile(path)
		if err != nil {
			return reviewroute.Policy{}, err
		}
	}
	var result reviewroute.Policy
	if err := strictJSON(data, &result); err != nil {
		return reviewroute.Policy{}, err
	}
	if err := reviewroute.ValidatePolicy(result); err != nil {
		return reviewroute.Policy{}, err
	}
	return result, nil
}

type requestOptions struct {
	plan      string
	out       string
	reviewers string
}

func runRequest(
	ctx context.Context,
	args []string,
	runner commandRunner,
	stdout io.Writer,
) error {
	opts, err := parseRequestOptions(args)
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
	selected, err := selectedReviewers(plan, opts.reviewers)
	if err != nil {
		return refusalError{err}
	}
	before, err := fetchPullRequest(ctx, runner, plan.Subject.Repo, plan.Subject.Number)
	if err != nil {
		return err
	}
	if !strings.EqualFold(before.HeadRefOID, plan.Subject.HeadSHA) {
		return refusalError{errors.New("review plan is stale before reviewer request")}
	}
	receipt := reviewroute.RequestReceipt{
		SchemaVersion: reviewroute.SchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		Subject:       plan.Subject,
		PlanID:        plan.PlanID,
		HeadBefore:    strings.ToLower(before.HeadRefOID),
		HeadAfter:     strings.ToLower(before.HeadRefOID),
		Status:        "requested",
		Requests:      make([]reviewroute.Request, 0, len(selected)),
	}
	for index, reviewer := range selected {
		if index > 0 {
			current, currentErr := fetchPullRequest(
				ctx, runner, plan.Subject.Repo, plan.Subject.Number,
			)
			if currentErr != nil {
				return currentErr
			}
			receipt.HeadAfter = strings.ToLower(current.HeadRefOID)
		}
		if !strings.EqualFold(receipt.HeadAfter, plan.Subject.HeadSHA) {
			// Staleness supersedes any partial request failure because none of
			// the receipt remains admissible after the subject head changes.
			receipt.Status = "stale"
			receipt.Reason = "pull request head changed before reviewer request"
			break
		}
		request := triggerReviewer(ctx, runner, plan.Subject, reviewer)
		receipt.Requests = append(receipt.Requests, request)
		if request.Status == "failed" {
			receipt.Status = "failed"
			receipt.Reason = "one or more reviewer requests failed"
		}
		after, afterErr := fetchPullRequest(
			ctx, runner, plan.Subject.Repo, plan.Subject.Number,
		)
		if afterErr != nil {
			return afterErr
		}
		receipt.HeadAfter = strings.ToLower(after.HeadRefOID)
		if !strings.EqualFold(receipt.HeadAfter, plan.Subject.HeadSHA) {
			receipt.Status = "stale"
			receipt.Reason = "pull request head changed during reviewer request"
			break
		}
	}
	if err := reviewroute.ValidateRequestReceipt(receipt); err != nil {
		return refusalError{err}
	}
	return writeArtifact(opts.out, receipt, nil, stdout)
}

func parseRequestOptions(args []string) (requestOptions, error) {
	var opts requestOptions
	flags := flag.NewFlagSet("request", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.plan, "plan", "", "ReviewPlanV1 path")
	flags.StringVar(&opts.out, "out", "", "output ReviewRequestV1 path")
	flags.StringVar(&opts.reviewers, "reviewers", "", "optional targeted comma-separated reviewers")
	if err := flags.Parse(args); err != nil {
		return requestOptions{}, err
	}
	if opts.plan == "" || opts.out == "" {
		return requestOptions{}, errors.New("-plan and -out are required")
	}
	return opts, nil
}

func selectedReviewers(plan reviewroute.Plan, requested string) ([]reviewroute.Reviewer, error) {
	if strings.TrimSpace(requested) == "" {
		return append([]reviewroute.Reviewer(nil), plan.Reviewers...), nil
	}
	byName := make(map[string]reviewroute.Reviewer, len(plan.Reviewers))
	for _, reviewer := range plan.Reviewers {
		byName[reviewer.Name] = reviewer
	}
	var selected []reviewroute.Reviewer
	seen := make(map[string]struct{})
	for _, name := range strings.Split(requested, ",") {
		name = strings.TrimSpace(name)
		reviewer, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("target reviewer %s is absent from the plan", name)
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		selected = append(selected, reviewer)
	}
	return selected, nil
}

func triggerReviewer(
	ctx context.Context,
	runner commandRunner,
	subject reviewroute.Subject,
	reviewer reviewroute.Reviewer,
) reviewroute.Request {
	result := reviewroute.Request{
		Reviewer: reviewer.Name, Trigger: reviewer.Trigger, Status: "requested",
	}
	switch reviewer.Trigger {
	case "auto":
		result.Status = "observed"
		return result
	case "mention":
		output, err := runner.Run(ctx, "gh", "pr", "comment",
			strconv.Itoa(subject.Number), "-R", subject.Repo,
			"--body", "@"+reviewer.Name+" review")
		if err != nil {
			result.Status = "failed"
			result.Ref = err.Error()
			return result
		}
		result.Ref = strings.TrimSpace(string(output))
		return result
	case "reviewer-request":
		ref, err := requestGitHubReviewer(ctx, runner, subject, reviewer.Name)
		if err != nil {
			result.Status = "failed"
			result.Ref = err.Error()
			return result
		}
		result.Ref = ref
		return result
	}
	result.Status = "failed"
	result.Ref = "unknown trigger"
	return result
}

func requestGitHubReviewer(
	ctx context.Context,
	runner commandRunner,
	subject reviewroute.Subject,
	name string,
) (string, error) {
	endpoint := fmt.Sprintf("repos/%s/pulls/%d/requested_reviewers",
		subject.Repo, subject.Number)
	logins := []string{name}
	if name == "copilot" {
		logins = []string{
			"Copilot",
			"copilot-pull-request-reviewer",
			"copilot-pull-request-reviewer[bot]",
		}
	}
	var failures []string
	var observeAfter time.Time
	for _, login := range logins {
		attemptedAt := time.Now().UTC()
		output, err := runner.Run(ctx, "gh", "api", "-X", "POST", endpoint,
			"-f", "reviewers[]="+login)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", login, err))
			continue
		}
		recorded, decodeErr := responseRecordsReviewer(output, login)
		if decodeErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", login, decodeErr))
			continue
		}
		if recorded {
			return login, nil
		}
		failures = append(failures, login+": GitHub did not record the request")
		if name == "copilot" && observeAfter.IsZero() {
			observeAfter = attemptedAt
		}
	}
	if !observeAfter.IsZero() {
		ref, observed, err := observeCopilotRequest(ctx, runner, subject, observeAfter)
		if err != nil {
			failures = append(failures, fmt.Sprintf("Copilot observation: %v", err))
		}
		if err == nil && observed {
			return ref, nil
		}
	}
	return "", errors.New(strings.Join(failures, "; "))
}

type copilotTimelineEvent struct {
	Event             string    `json:"event"`
	CreatedAt         time.Time `json:"created_at"`
	RequestedReviewer struct {
		Login string `json:"login"`
	} `json:"requested_reviewer"`
	Actor struct {
		Login string `json:"login"`
	} `json:"actor"`
}

type copilotReview struct {
	ID       int64  `json:"id"`
	CommitID string `json:"commit_id"`
	User     struct {
		Login string `json:"login"`
	} `json:"user"`
}

func observeCopilotRequest(
	ctx context.Context,
	runner commandRunner,
	subject reviewroute.Subject,
	attemptedAt time.Time,
) (string, bool, error) {
	timelineEndpoint := fmt.Sprintf(
		"repos/%s/issues/%d/timeline?per_page=100",
		subject.Repo,
		subject.Number,
	)
	timelineData, timelineErr := runner.Run(
		ctx, "gh", "api", "--paginate", "--slurp", timelineEndpoint,
	)
	if timelineErr == nil {
		var pages [][]copilotTimelineEvent
		if err := json.Unmarshal(timelineData, &pages); err != nil {
			timelineErr = fmt.Errorf("decode review timeline: %w", err)
		}
		if ref, observed := freshCopilotTimelineEvent(pages, attemptedAt); observed {
			return ref, true, nil
		}
	}

	reviewsEndpoint := fmt.Sprintf(
		"repos/%s/pulls/%d/reviews?per_page=100",
		subject.Repo,
		subject.Number,
	)
	reviewData, reviewErr := runner.Run(
		ctx, "gh", "api", "--paginate", "--slurp", reviewsEndpoint,
	)
	if reviewErr == nil {
		var pages [][]copilotReview
		if err := json.Unmarshal(reviewData, &pages); err != nil {
			reviewErr = fmt.Errorf("decode pull request reviews: %w", err)
		}
		if ref, observed := exactHeadCopilotReview(pages, subject.HeadSHA); observed {
			return ref, true, nil
		}
	}
	if timelineErr != nil || reviewErr != nil {
		return "", false, fmt.Errorf(
			"verify Copilot request (timeline: %v; reviews: %v)",
			timelineErr,
			reviewErr,
		)
	}
	return "", false, nil
}

func freshCopilotTimelineEvent(
	pages [][]copilotTimelineEvent,
	attemptedAt time.Time,
) (string, bool) {
	for _, events := range pages {
		for _, event := range events {
			fresh := !event.CreatedAt.Before(attemptedAt.Add(-2 * time.Second))
			requested := event.Event == "review_requested" &&
				isCopilotLogin(event.RequestedReviewer.Login)
			started := event.Event == "copilot_work_started" &&
				isCopilotLogin(event.Actor.Login)
			if fresh && (requested || started) {
				return "timeline:" + event.Event, true
			}
		}
	}
	return "", false
}

func exactHeadCopilotReview(pages [][]copilotReview, head string) (string, bool) {
	for _, reviews := range pages {
		for _, review := range reviews {
			if isCopilotLogin(review.User.Login) &&
				strings.EqualFold(review.CommitID, head) {
				return fmt.Sprintf("review:%d", review.ID), true
			}
		}
	}
	return "", false
}

func responseRecordsReviewer(data []byte, login string) (bool, error) {
	var response struct {
		RequestedReviewers []struct {
			Login string `json:"login"`
		} `json:"requested_reviewers"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return false, fmt.Errorf("decode reviewer request response: %w", err)
	}
	for _, reviewer := range response.RequestedReviewers {
		if strings.EqualFold(reviewer.Login, login) {
			return true, nil
		}
		if isCopilotLogin(login) && isCopilotLogin(reviewer.Login) {
			return true, nil
		}
	}
	return false, nil
}

func isCopilotLogin(login string) bool {
	login = strings.ToLower(strings.TrimSuffix(login, "[bot]"))
	switch login {
	case "copilot", "copilot-pull-request-reviewer", "github-copilot":
		return true
	}
	return false
}

type decideOptions struct {
	plan  string
	input string
	out   string
}

func runDecide(
	ctx context.Context,
	args []string,
	runner commandRunner,
	stdout io.Writer,
) error {
	opts, err := parseDecideOptions(args)
	if err != nil {
		return err
	}
	var plan reviewroute.Plan
	if err := readArtifact(opts.plan, &plan); err != nil {
		return err
	}
	var input reviewroute.CycleInput
	if err := readArtifact(opts.input, &input); err != nil {
		return err
	}
	if err := reviewroute.ValidatePlan(plan); err != nil {
		return refusalError{err}
	}
	if err := reviewroute.ValidateCycleInput(input); err != nil {
		return refusalError{err}
	}
	before, err := fetchPullRequest(ctx, runner, plan.Subject.Repo, plan.Subject.Number)
	if err != nil {
		return err
	}
	if !strings.EqualFold(before.HeadRefOID, plan.Subject.HeadSHA) {
		return refusalError{errors.New("review plan is stale before decision")}
	}
	if !strings.EqualFold(before.State, "OPEN") {
		return refusalError{fmt.Errorf("pull request is %s", before.State)}
	}
	decision, err := policy.Decide(plan, input, time.Now().UTC())
	if err != nil {
		return refusalError{err}
	}
	after, err := fetchPullRequest(ctx, runner, plan.Subject.Repo, plan.Subject.Number)
	if err != nil {
		return err
	}
	if !strings.EqualFold(before.HeadRefOID, after.HeadRefOID) {
		return refusalError{errors.New("pull request head changed during decision")}
	}
	return writeArtifact(opts.out, decision, nil, stdout)
}

func parseDecideOptions(args []string) (decideOptions, error) {
	var opts decideOptions
	flags := flag.NewFlagSet("decide", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.plan, "plan", "", "ReviewPlanV1 path")
	flags.StringVar(&opts.input, "input", "", "ReviewCycleInputV1 path")
	flags.StringVar(&opts.out, "out", "", "output ReviewDecisionV1 path")
	if err := flags.Parse(args); err != nil {
		return decideOptions{}, err
	}
	if opts.plan == "" || opts.input == "" || opts.out == "" {
		return decideOptions{}, errors.New("-plan, -input, and -out are required")
	}
	return opts, nil
}

type adviseOptions struct {
	input string
	out   string
	model string
}

func runAdvise(ctx context.Context, args []string, stdout io.Writer) error {
	var opts adviseOptions
	flags := flag.NewFlagSet("advise", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.input, "input", "", "finding/evidence input path")
	flags.StringVar(&opts.out, "out", "", "output advisory path")
	flags.StringVar(&opts.model, "model", "", "optional Ollama model")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if opts.input == "" || opts.out == "" {
		return errors.New("-input and -out are required")
	}
	input, err := os.ReadFile(opts.input)
	if err != nil {
		return err
	}
	schema := json.RawMessage(`{
		"type":"object",
		"required":["recommendation","rationale","confidence"],
		"properties":{
			"recommendation":{"enum":["fix","prove","defer","rereview"]},
			"rationale":{"type":"string","minLength":1},
			"confidence":{"type":"number","minimum":0,"maximum":1}
		},
		"additionalProperties":false
	}`)
	result, err := local.Ask(ctx, local.Req{
		Prompt: "Advise on the supplied review finding using only verbatim evidence. Critical findings must be rereviewed and may not be deferred. Return fix, prove, defer, or rereview.",
		Input:  string(input),
		Schema: schema,
		Model:  opts.model,
	}, local.Opts{
		Verify:        validAdvisory,
		MinConfidence: 0.75,
	})
	if err != nil {
		return err
	}
	envelope := struct {
		Answer json.RawMessage `json:"answer"`
		Source string          `json:"source"`
		Reason string          `json:"reason,omitempty"`
	}{Answer: result.Raw, Source: result.Source, Reason: result.Reason}
	return writeArtifact(opts.out, envelope, nil, stdout)
}

func validAdvisory(raw json.RawMessage) bool {
	var answer struct {
		Recommendation string  `json:"recommendation"`
		Rationale      string  `json:"rationale"`
		Confidence     float64 `json:"confidence"`
	}
	if err := strictJSON(raw, &answer); err != nil {
		return false
	}
	switch answer.Recommendation {
	case "fix", "prove", "defer", "rereview":
	default:
		return false
	}
	return strings.TrimSpace(answer.Rationale) != "" &&
		answer.Confidence >= 0 && answer.Confidence <= 1
}

func readArtifact(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := strictJSON(data, target); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func strictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}

func writeArtifact(path string, value any, prior error, stdout io.Writer) error {
	if prior != nil {
		return prior
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := writeAtomic(path, data); err != nil {
		return err
	}
	fmt.Fprintln(stdout, path)
	return nil
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".review-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

func validTier(tier string) bool {
	switch tier {
	case "T0", "T1", "T2", "T3":
		return true
	}
	return false
}

func sortedUnique(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
