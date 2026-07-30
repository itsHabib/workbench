// reviewfindings emits Ship-compatible ReviewFindingsV1 artifacts from exact-head
// GitHub inline review comments.
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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/itsHabib/workbench/contracts/reviewfindings"
)

const (
	exitOK      = 0
	exitParked  = 2
	exitRefused = 3
	exitError   = 4
)

type commandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

type refusalError struct {
	err error
}

func (e refusalError) Error() string { return e.err.Error() }
func (e refusalError) Unwrap() error { return e.err }

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strings.TrimSpace(stderr.String()), err)
	}
	return output, nil
}

type options struct {
	repo            string
	pr              int
	head            string
	requested       listFlag
	completed       listFlag
	out             string
	producer        string
	catalogRevision string
}

type listFlag []string

func (f *listFlag) String() string { return strings.Join(*f, ",") }
func (f *listFlag) Set(value string) error {
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			*f = append(*f, item)
		}
	}
	return nil
}

type pullRequest struct {
	HeadRefOID string `json:"headRefOid"`
	State      string `json:"state"`
}

type comment struct {
	ID               int64  `json:"id"`
	Body             string `json:"body"`
	HTMLURL          string `json:"html_url"`
	Path             string `json:"path"`
	Line             int    `json:"line"`
	OriginalCommitID string `json:"original_commit_id"`
	User             struct {
		Login string `json:"login"`
	} `json:"user"`
}

var severityPattern = regexp.MustCompile(`(?i)\b(P[0-3]|critical|high|medium|low|blocking|non-blocking|important|nit)\b`)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], execRunner{}, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, runner commandRunner, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "address" {
		return runAddress(ctx, args[1:], runner, stdout, stderr)
	}
	if len(args) == 0 || args[0] != "github" {
		fmt.Fprintln(stderr, "usage: reviewfindings github ... | reviewfindings address accept|claim|started|completed|resume ...")
		return exitError
	}
	opts, err := parseOptions(args[1:])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	artifact, err := produce(ctx, runner, opts, time.Now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, err)
		var refused refusalError
		if !errors.As(err, &refused) {
			return exitError
		}
		return exitRefused
	}
	data, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	data = append(data, '\n')
	if err := writeAtomic(opts.out, data); err != nil {
		fmt.Fprintln(stderr, err)
		return exitError
	}
	fmt.Fprintf(stdout, "%s\n", opts.out)
	return exitOK
}

func parseOptions(args []string) (options, error) {
	var opts options
	flags := flag.NewFlagSet("github", flag.ContinueOnError)
	flags.SetOutput(&bytes.Buffer{})
	flags.StringVar(&opts.repo, "repo", "", "owner/repo")
	flags.IntVar(&opts.pr, "pr", 0, "pull request number")
	flags.StringVar(&opts.head, "head", "", "exact reviewed head SHA")
	flags.Var(&opts.requested, "requested", "comma-separated requested reviewers")
	flags.Var(&opts.completed, "completed", "comma-separated completed reviewers")
	flags.StringVar(&opts.out, "out", "", "output artifact path")
	flags.StringVar(&opts.producer, "producer", "codex:reviewfindings-github", "producer id")
	flags.StringVar(&opts.catalogRevision, "catalog-revision", "", "canonical skill-catalog commit SHA or sha256 digest")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	switch {
	case opts.repo == "":
		return options{}, errors.New("-repo is required")
	case opts.pr < 1:
		return options{}, errors.New("-pr must be positive")
	case opts.head == "":
		return options{}, errors.New("-head is required to bind production to the reviewed revision")
	case len(opts.requested) == 0:
		return options{}, errors.New("-requested is required")
	case len(opts.completed) == 0:
		return options{}, errors.New("-completed is required")
	case opts.out == "":
		return options{}, errors.New("-out is required")
	}
	return opts, nil
}

func produce(ctx context.Context, runner commandRunner, opts options, now time.Time) (reviewfindings.Artifact, error) {
	pr, err := fetchPullRequest(ctx, runner, opts)
	if err != nil {
		return reviewfindings.Artifact{}, err
	}
	if !strings.EqualFold(pr.State, "OPEN") {
		return reviewfindings.Artifact{}, refusef("pull request is %s", pr.State)
	}
	if !strings.EqualFold(pr.HeadRefOID, opts.head) {
		return reviewfindings.Artifact{}, refusef(
			"requested head %s is stale; live head is %s", opts.head, pr.HeadRefOID,
		)
	}
	comments, err := fetchComments(ctx, runner, opts)
	if err != nil {
		return reviewfindings.Artifact{}, err
	}
	artifact, err := buildArtifact(opts, comments, now)
	if err != nil {
		return reviewfindings.Artifact{}, err
	}
	digest, err := reviewfindings.CanonicalSHA256(artifact)
	if err != nil {
		return reviewfindings.Artifact{}, refusalError{fmt.Errorf("malformed artifact: %w", err)}
	}
	artifact.ArtifactID = "rf_" + digest[:32]
	if err := reviewfindings.Validate(artifact); err != nil {
		return reviewfindings.Artifact{}, refusalError{fmt.Errorf("malformed artifact: %w", err)}
	}
	return artifact, nil
}

func fetchPullRequest(ctx context.Context, runner commandRunner, opts options) (pullRequest, error) {
	data, err := runner.Run(ctx, "gh", "pr", "view", strconv.Itoa(opts.pr), "-R", opts.repo, "--json", "headRefOid,state")
	if err != nil {
		return pullRequest{}, fmt.Errorf("read pull request: %w", err)
	}
	var pr pullRequest
	if err := json.Unmarshal(data, &pr); err != nil {
		return pullRequest{}, fmt.Errorf("decode pull request: %w", err)
	}
	return pr, nil
}

func fetchComments(ctx context.Context, runner commandRunner, opts options) ([]comment, error) {
	endpoint := fmt.Sprintf("repos/%s/pulls/%d/comments", opts.repo, opts.pr)
	data, err := runner.Run(ctx, "gh", "api", "--paginate", "--slurp", endpoint)
	if err != nil {
		return nil, fmt.Errorf("read review comments: %w", err)
	}
	var pages [][]comment
	if err := json.Unmarshal(data, &pages); err != nil {
		return nil, fmt.Errorf("decode review comments: %w", err)
	}
	var comments []comment
	for _, page := range pages {
		comments = append(comments, page...)
	}
	return comments, nil
}

func buildArtifact(opts options, comments []comment, now time.Time) (reviewfindings.Artifact, error) {
	requested := canonicalReviewers(opts.requested)
	completedList := canonicalReviewers(opts.completed)
	completed := stringSet(completedList)
	findings := make([]reviewfindings.Finding, 0, len(comments))
	for _, item := range comments {
		reviewer := canonicalReviewer(item.User.Login)
		if _, ok := completed[reviewer]; !ok || !strings.EqualFold(item.OriginalCommitID, opts.head) {
			continue
		}
		if strings.TrimSpace(item.Body) == "" {
			continue
		}
		findings = append(findings, findingFromComment(reviewer, item))
	}
	if len(findings) == 0 {
		return reviewfindings.Artifact{}, refusalError{errors.New("no sourced exact-head inline findings")}
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].ID < findings[j].ID })
	missing := difference(requested, completedList)
	return reviewfindings.Artifact{
		SchemaVersion: reviewfindings.SchemaVersion,
		ArtifactID:    "rf_pending",
		Decision:      reviewfindings.DecisionAddress,
		Subject: reviewfindings.Subject{
			Type: reviewfindings.SubjectPullRequest, Repo: strings.ToLower(opts.repo),
			Number: opts.pr, HeadSHA: strings.ToLower(opts.head),
		},
		Producer: reviewfindings.Producer{
			ID: opts.producer, Harness: "codex",
			CatalogRevision: opts.catalogRevision, GeneratedAt: now,
		},
		Panel: reviewfindings.Panel{
			Requested: requested, Completed: completedList, Missing: missing,
		},
		Findings: findings,
	}, nil
}

func findingFromComment(reviewer string, item comment) reviewfindings.Finding {
	summary := firstLine(item.Body)
	summary = truncateUTF8(summary, 512)
	severity := "advisory"
	if match := severityPattern.FindString(item.Body); match != "" {
		severity = strings.ToLower(match)
	}
	id := fmt.Sprintf("%s-%d", reviewer, item.ID)
	return reviewfindings.Finding{
		ID: id, Severity: severity, Summary: summary, Evidence: item.Body,
		Sources: []reviewfindings.Source{{
			Reviewer: reviewer, CommentID: strconv.FormatInt(item.ID, 10),
			URL: item.HTMLURL, File: item.Path, Line: item.Line,
		}},
	}
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}

func refusef(format string, args ...any) error {
	return refusalError{fmt.Errorf("refused: "+format, args...)}
}

func firstLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, "#*- "))
		if line != "" {
			return line
		}
	}
	return "review finding"
}

func canonicalReviewer(login string) string {
	login = strings.ToLower(strings.TrimSuffix(login, "[bot]"))
	switch {
	case strings.Contains(login, "codex"):
		return "codex"
	case strings.Contains(login, "copilot"):
		return "copilot"
	case strings.Contains(login, "cursor"):
		return "cursor"
	case strings.Contains(login, "claude"):
		return "claude"
	default:
		return login
	}
}

func sortedUnique(values []string) []string {
	set := stringSet(values)
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func canonicalReviewers(values []string) []string {
	canonical := make([]string, 0, len(values))
	for _, value := range values {
		canonical = append(canonical, canonicalReviewer(value))
	}
	return sortedUnique(canonical)
}

func difference(all, present []string) []string {
	presentSet := stringSet(present)
	var out []string
	for _, value := range all {
		if _, ok := presentSet[value]; !ok {
			out = append(out, value)
		}
	}
	return out
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".review-findings-*")
	if err != nil {
		return fmt.Errorf("create temporary output: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write temporary output: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary output: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("publish output: %w", err)
	}
	return nil
}
