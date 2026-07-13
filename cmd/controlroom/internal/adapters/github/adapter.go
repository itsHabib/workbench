// Package github adapts a bounded, explicitly scoped GitHub GraphQL inventory.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
)

const (
	defaultTimeout = 10 * time.Second
	pageSize       = 50
	maxPages       = 4
	queryVersion   = "control-room-pr-inventory-v1"
	graphQLQuery   = `# control-room-pr-inventory-v1
query ControlRoomPullRequests($q: String!, $first: Int!, $after: String) {
  search(query: $q, type: ISSUE, first: $first, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on PullRequest {
        id repository { nameWithOwner } number title url author { login }
        baseRefName headRefName isDraft state createdAt updatedAt
        mergeable mergeStateStatus reviewDecision reviewRequests { totalCount }
        statusCheckRollup { contexts(first: 100) { pageInfo { hasNextPage endCursor } nodes { __typename ... on CheckRun { name status conclusion } ... on StatusContext { context state } } } }
        reviewThreads(first: 100) { pageInfo { hasNextPage endCursor } nodes { isResolved } }
      }
    }
  }
}`
)

var (
	versionPattern = regexp.MustCompile(`(?m)gh version (\d+)\.(\d+)\.(\d+)`)
	namePattern    = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9_.-]*[A-Za-z0-9])?$`)
)

// Result is GitHub's source-local collection result.
type Result struct {
	PullRequests []model.PullRequest
	Receipt      model.SourceReceipt
}

type runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, executable string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, executable, args...).Output()
}

// Adapter owns validated scopes and the bounded query loop.
type Adapter struct {
	executable string
	scopes     []string
	timeout    time.Duration
	runner     runner
	now        func() time.Time
}

// New validates and freezes one to four explicit GitHub scopes.
func New(executable string, scopes []string) (*Adapter, error) {
	validated, err := validateScopes(scopes)
	if err != nil {
		return nil, err
	}
	return &Adapter{executable: executable, scopes: validated, timeout: defaultTimeout, runner: commandRunner{}, now: time.Now}, nil
}

// Collect executes version/auth checks and at most four GraphQL pages.
func (a *Adapter) Collect(ctx context.Context) Result {
	started := a.now()
	receipt := model.SourceReceipt{Source: "github", ObservedAt: started}
	finish := func(state model.SourceState, code, message string, prs []model.PullRequest) Result {
		receipt.State, receipt.ErrorCode, receipt.Message = state, code, message
		receipt.DurationMS = max(0, a.now().Sub(started).Milliseconds())
		sort.Slice(prs, func(i, j int) bool {
			if !prs[i].UpdatedAt.Equal(prs[j].UpdatedAt) {
				return prs[i].UpdatedAt.After(prs[j].UpdatedAt)
			}
			if prs[i].Repository != prs[j].Repository {
				return prs[i].Repository < prs[j].Repository
			}
			return prs[i].Number < prs[j].Number
		})
		return Result{PullRequests: prs, Receipt: receipt}
	}
	if a.executable == "" {
		return finish(model.SourceUnavailable, "not_configured", "GitHub CLI is not configured", nil)
	}

	sourceCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	login, code, message := a.preflight(sourceCtx)
	if code != "" {
		return finish(model.SourceUnavailable, code, message, nil)
	}
	prs, state, code, message := a.collectPages(sourceCtx, login)
	return finish(state, code, message, prs)
}

func (a *Adapter) preflight(ctx context.Context) (string, string, string) {
	versionOut, err := a.runner.Run(ctx, a.executable, "--version")
	if err != nil {
		return "", commandCode(ctx, err), "GitHub CLI version check failed"
	}
	if !supportedVersion(string(versionOut)) {
		return "", "unsupported_version", "GitHub CLI 2.90.0 or newer is required"
	}
	loginOut, err := a.runner.Run(ctx, a.executable, "api", "user", "--jq", ".login")
	if err != nil {
		return "", commandCode(ctx, err), "GitHub authentication check failed"
	}
	login := strings.TrimSpace(string(loginOut))
	if !namePattern.MatchString(login) {
		return "", "invalid_login", "GitHub returned an invalid authenticated login"
	}
	return login, "", ""
}

type scopeState struct {
	scope  string
	cursor string
	active bool
}

func (a *Adapter) collectPages(ctx context.Context, login string) ([]model.PullRequest, model.SourceState, string, string) {
	states := make([]scopeState, len(a.scopes))
	for i, scope := range a.scopes {
		states[i] = scopeState{scope: scope, active: true}
	}
	seen := make(map[string]struct{})
	prs := make([]model.PullRequest, 0)
	pages := 0
	for pages < maxPages {
		progress := false
		for i := range states {
			if pages >= maxPages {
				break
			}
			attempted, code, message := a.collectScopePage(ctx, login, &states[i], seen, &prs)
			if !attempted {
				continue
			}
			progress = true
			pages++
			if code != "" {
				state := model.SourceDegraded
				if pages == 1 {
					state = model.SourceUnavailable
				}
				return prs, state, code, message
			}
		}
		if !progress {
			break
		}
	}
	for _, state := range states {
		if state.active {
			return prs, model.SourceDegraded, "inventory_truncated", "GitHub inventory was capped at four pages"
		}
	}
	return prs, model.SourceOK, "", ""
}

func (a *Adapter) collectScopePage(ctx context.Context, login string, state *scopeState, seen map[string]struct{}, prs *[]model.PullRequest) (bool, string, string) {
	if !state.active {
		return false, "", ""
	}
	out, err := a.runner.Run(ctx, a.executable, graphQLArgs(login, state.scope, state.cursor)...)
	if err != nil {
		return true, commandCode(ctx, err), "GitHub inventory page failed"
	}
	var page graphQLResponse
	if json.Unmarshal(out, &page) != nil {
		return true, "malformed_page", "GitHub returned a malformed inventory page"
	}
	for _, node := range page.Data.Search.Nodes {
		pr, ok := normalizePR(node)
		if !ok {
			continue
		}
		key := node.ID
		if key == "" {
			key = pr.Repository + "#" + strconv.Itoa(pr.Number)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		*prs = append(*prs, pr)
	}
	state.cursor = page.Data.Search.PageInfo.EndCursor
	state.active = page.Data.Search.PageInfo.HasNextPage
	return true, "", ""
}

func validateScopes(scopes []string) ([]string, error) {
	if len(scopes) < 1 || len(scopes) > 4 {
		return nil, fmt.Errorf("github scopes must contain one to four entries")
	}
	seen := make(map[string]struct{}, len(scopes))
	validated := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		parts := strings.Split(scope, ":")
		if len(parts) != 2 || (parts[0] != "user" && parts[0] != "org" && parts[0] != "repo") {
			return nil, fmt.Errorf("invalid github scope %q", scope)
		}
		if parts[0] == "repo" {
			repo := strings.Split(parts[1], "/")
			if len(repo) != 2 || !namePattern.MatchString(repo[0]) || !namePattern.MatchString(repo[1]) {
				return nil, fmt.Errorf("invalid github repository scope %q", scope)
			}
		} else if !namePattern.MatchString(parts[1]) {
			return nil, fmt.Errorf("invalid github account scope %q", scope)
		}
		if _, exists := seen[scope]; exists {
			return nil, fmt.Errorf("duplicate github scope %q", scope)
		}
		seen[scope] = struct{}{}
		validated = append(validated, scope)
	}
	// Scope priority is deliberately lexical so identical configurations
	// produce the same round-robin order regardless of caller map iteration.
	sort.Strings(validated)
	return validated, nil
}

func supportedVersion(output string) bool {
	match := versionPattern.FindStringSubmatch(output)
	if len(match) != 4 {
		return false
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	return major > 2 || (major == 2 && minor >= 90)
}

func graphQLArgs(login, scope, cursor string) []string {
	search := "is:pr is:open author:" + login + " archived:false " + scope
	args := []string{"api", "graphql", "-f", "query=" + graphQLQuery, "-F", "q=" + search, "-F", "first=" + strconv.Itoa(pageSize)}
	if cursor != "" {
		args = append(args, "-F", "after="+cursor)
	}
	return args
}

type graphQLResponse struct {
	Data struct {
		Search struct {
			PageInfo pageInfo `json:"pageInfo"`
			Nodes    []prWire `json:"nodes"`
		} `json:"search"`
	} `json:"data"`
}

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type prWire struct {
	ID         string `json:"id"`
	Repository struct {
		Name string `json:"nameWithOwner"`
	} `json:"repository"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Base             string    `json:"baseRefName"`
	Head             string    `json:"headRefName"`
	Draft            bool      `json:"isDraft"`
	State            string    `json:"state"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
	Mergeable        string    `json:"mergeable"`
	MergeStateStatus string    `json:"mergeStateStatus"`
	ReviewDecision   string    `json:"reviewDecision"`
	ReviewRequests   struct {
		Total int `json:"totalCount"`
	} `json:"reviewRequests"`
	StatusCheckRollup *struct {
		Contexts connection[checkWire] `json:"contexts"`
	} `json:"statusCheckRollup"`
	ReviewThreads connection[threadWire] `json:"reviewThreads"`
}

type connection[T any] struct {
	PageInfo pageInfo `json:"pageInfo"`
	Nodes    []T      `json:"nodes"`
}

type checkWire struct {
	Type       string `json:"__typename"`
	Name       string `json:"name"`
	Context    string `json:"context"`
	Status     string `json:"status"`
	State      string `json:"state"`
	Conclusion string `json:"conclusion"`
}

type threadWire struct {
	Resolved bool `json:"isResolved"`
}

func normalizePR(w prWire) (model.PullRequest, bool) {
	if w.Repository.Name == "" || w.Number < 1 || !strings.HasPrefix(w.URL, "https://github.com/") || w.CreatedAt.IsZero() || w.UpdatedAt.IsZero() {
		return model.PullRequest{}, false
	}
	id := w.ID
	if id == "" {
		id = w.Repository.Name + "#" + strconv.Itoa(w.Number)
	}
	pr := model.PullRequest{
		ID: id, Repository: w.Repository.Name, Number: w.Number, Title: w.Title, URL: w.URL, Author: w.Author.Login,
		Head: w.Head, Base: w.Base, Draft: w.Draft, State: strings.ToLower(w.State), CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt,
		ReviewDecision: w.ReviewDecision, RequestedReviewers: w.ReviewRequests.Total, Mergeable: w.Mergeable, MergeStateStatus: w.MergeStateStatus,
		Checks: []model.Check{}, TruncatedConnections: []string{}, DetailState: "complete",
	}
	if w.StatusCheckRollup != nil {
		for _, check := range w.StatusCheckRollup.Contexts.Nodes {
			name := firstNonempty(check.Name, check.Context)
			status := firstNonempty(check.Status, check.State)
			pr.Checks = append(pr.Checks, model.Check{Name: name, Status: status, Conclusion: check.Conclusion})
		}
		if w.StatusCheckRollup.Contexts.PageInfo.HasNextPage {
			pr.TruncatedConnections = append(pr.TruncatedConnections, "checks")
		}
	}
	for _, thread := range w.ReviewThreads.Nodes {
		if !thread.Resolved {
			pr.UnresolvedThreads++
		}
	}
	if w.ReviewThreads.PageInfo.HasNextPage {
		pr.TruncatedConnections = append(pr.TruncatedConnections, "review_threads")
	}
	if len(pr.TruncatedConnections) > 0 {
		pr.DetailState = "truncated"
	}
	sort.Slice(pr.Checks, func(i, j int) bool { return pr.Checks[i].Name < pr.Checks[j].Name })
	return pr, true
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func commandCode(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return "executable_not_found"
	}
	return "command_failed"
}

// QueryVersion identifies the immutable embedded GraphQL contract in receipts
// and tests without exporting the query text itself.
func QueryVersion() string { return queryVersion }
