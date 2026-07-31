// Package policy owns Codex's deterministic authority-bearing action policy.
//
// It classifies normalized tool calls and consults Gate only through its
// read-only JSON CLI seam. It never imports Gate decision code and never
// executes an action.
package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/itsHabib/workbench/contracts/automode"
)

var fullSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
var repoName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

const (
	// RulebookVersion identifies this exact deterministic rule set.
	RulebookVersion = "codexguard.v1"

	remedyUnknown = "run the action directly in a supported shell envelope or ask the operator to review it"
	remedyDenied  = "ask the operator to perform this authority-bearing action outside the governed Codex task"
)

// Request is the policy input supplied by a lifecycle adapter.
type Request struct {
	GateState string   `json:"-"`
	Envelope  Envelope `json:"envelope"`
}

// Envelope is one supported harness representation.
type Envelope struct {
	Kind      string          `json:"kind"`
	Shell     string          `json:"shell,omitempty"`
	Command   string          `json:"command,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Code      string          `json:"code,omitempty"`
}

// ReadyMerge is the stable slice read from gate next -json.
type ReadyMerge struct {
	Run          string `json:"run"`
	Repo         string `json:"repo"`
	Number       int    `json:"number"`
	HeadSHA      string `json:"head_sha"`
	MergeCommand string `json:"merge_command"`
}

// PullRequest is the independent live GitHub evidence for a merge.
type PullRequest struct {
	State   string
	HeadSHA string
}

// GateReader reads Gate's newest terminal projection without mutating it.
type GateReader interface {
	Ready(context.Context, string) ([]ReadyMerge, error)
}

// PullRequestReader independently reads the live PR.
type PullRequestReader interface {
	Get(context.Context, string, int) (PullRequest, error)
}

// Evaluator combines deterministic classification with read-only evidence.
type Evaluator struct {
	gate GateReader
	prs  PullRequestReader
}

// New returns a policy evaluator over fakeable read-only seams.
func New(gate GateReader, prs PullRequestReader) Evaluator {
	return Evaluator{gate: gate, prs: prs}
}

// Evaluate returns a validated AutoDecisionV1 and never executes the action.
func (e Evaluator) Evaluate(ctx context.Context, request Request) (automode.Decision, error) {
	action, err := normalize(request.Envelope)
	if err != nil {
		return decision(normalized{
			envelope:  "unknown",
			operation: "unknown",
		}, automode.OutcomePark, "envelope.unsupported", remedyUnknown, nil)
	}

	outcome, rule, remedy := classify(action)
	if action.operation != "github.pr.merge" {
		return decision(action, outcome, rule, remedy, nil)
	}
	if outcome == automode.OutcomeBlock {
		return decision(action, outcome, rule, remedy, nil)
	}
	return e.authorizeMerge(ctx, request, action)
}

func classify(action normalized) (string, string, string) {
	switch action.operation {
	case "read", "test":
		return automode.OutcomePass, "safe.read_or_test", ""
	case "github.pr.merge":
		if action.admin {
			return automode.OutcomeBlock, "authority.merge_admin", remedyDenied
		}
		return automode.OutcomeRefuse, "authority.merge_unverified", mergeRemedy(action)
	case "authority.mint":
		return automode.OutcomeBlock, "authority.mint", remedyDenied
	case "authority.state_mutation":
		return automode.OutcomeBlock, "authority.state_mutation", remedyDenied
	case "authority.elevation":
		return automode.OutcomeBlock, "authority.elevation", remedyDenied
	case "git.force_push":
		return automode.OutcomeBlock, "authority.force_push", remedyDenied
	case "github.repo.delete":
		return automode.OutcomeBlock, "authority.repo_delete", remedyDenied
	case "github.repo.visibility":
		return automode.OutcomeBlock, "authority.visibility_change", remedyDenied
	}
	return automode.OutcomePark, "action.unsupported", remedyUnknown
}

func (e Evaluator) authorizeMerge(ctx context.Context, request Request, action normalized) (automode.Decision, error) {
	remedy := mergeRemedy(action)
	if action.repo == "" || action.number == 0 || !fullSHA.MatchString(action.headSHA) {
		return decision(action, automode.OutcomeRefuse, "merge.command_malformed", remedy, nil)
	}
	if request.GateState == "" {
		return decision(action, automode.OutcomeRefuse, "merge.gate_state_missing", remedy, nil)
	}
	if e.gate == nil || e.prs == nil {
		return decision(action, automode.OutcomeRefuse, "merge.evidence_unavailable", remedy, nil)
	}
	rows, err := e.gate.Ready(ctx, request.GateState)
	if err != nil {
		return decision(action, automode.OutcomeRefuse, "merge.gate_read_failed", remedy,
			automode.Values{"gate_read": {Value: "failed"}})
	}
	row, ok := uniqueReady(rows, action.repo, action.number)
	if !ok {
		return decision(action, automode.OutcomeRefuse, "merge.ready_row_not_unique", remedy,
			automode.Values{"ready_rows": {Value: strconv.Itoa(countReady(rows, action.repo, action.number))}})
	}
	if row.MergeCommand != action.candidate {
		return decision(action, automode.OutcomeRefuse, "merge.command_mismatch", remedy,
			mergeObservables(row, "command_mismatch"))
	}
	if row.HeadSHA == "" || row.HeadSHA != action.headSHA {
		return decision(action, automode.OutcomeRefuse, "merge.gate_head_mismatch", remedy,
			mergeObservables(row, "head_mismatch"))
	}
	pr, err := e.prs.Get(ctx, action.repo, action.number)
	if err != nil {
		return decision(action, automode.OutcomeRefuse, "merge.github_read_failed", remedy,
			mergeObservables(row, "github_read_failed"))
	}
	if pr.State != "OPEN" {
		return decision(action, automode.OutcomeRefuse, "merge.pr_not_open", remedy,
			mergeLiveObservables(row, pr))
	}
	if pr.HeadSHA == "" || pr.HeadSHA != row.HeadSHA {
		return decision(action, automode.OutcomeRefuse, "merge.live_head_mismatch", remedy,
			mergeLiveObservables(row, pr))
	}
	return decision(action, automode.OutcomePass, "merge.exact_gate_command_live_head", "",
		mergeLiveObservables(row, pr))
}

func uniqueReady(rows []ReadyMerge, repo string, number int) (ReadyMerge, bool) {
	var found ReadyMerge
	count := 0
	for _, row := range rows {
		if row.Repo != repo || row.Number != number {
			continue
		}
		found = row
		count++
	}
	return found, count == 1
}

func countReady(rows []ReadyMerge, repo string, number int) int {
	count := 0
	for _, row := range rows {
		if row.Repo == repo && row.Number == number {
			count++
		}
	}
	return count
}

func mergeObservables(row ReadyMerge, status string) automode.Values {
	return automode.Values{
		"gate_command_digest": {Value: textDigest(row.MergeCommand)},
		"gate_head_sha":       {Value: row.HeadSHA},
		"gate_run":            {Value: row.Run},
		"gate_status":         {Value: status},
	}
}

func mergeLiveObservables(row ReadyMerge, pr PullRequest) automode.Values {
	return automode.Values{
		"gate_command_digest": {Value: textDigest(row.MergeCommand)},
		"gate_head_sha":       {Value: row.HeadSHA},
		"gate_run":            {Value: row.Run},
		"gate_status":         {Value: "ready_to_merge"},
		"live_head_sha":       {Value: pr.HeadSHA},
		"live_pr_state":       {Value: pr.State},
	}
}

func decision(action normalized, outcome, rule, remedy string, observables automode.Values) (automode.Decision, error) {
	envelope := action.envelope
	if envelope == "" {
		envelope = "unknown"
	}
	parameters := action.parameters
	if parameters == nil {
		parameters = automode.Values{}
	}
	if observables == nil {
		observables = automode.Values{}
	}
	inputs := automode.InputProjection{
		Action: automode.Action{
			Envelope:   envelope,
			Operation:  action.operation,
			Parameters: parameters,
		},
		Observables: observables,
	}
	digest, err := automode.Digest(inputs)
	if err != nil {
		return automode.Decision{}, err
	}
	d := automode.Decision{
		SchemaVersion:   automode.SchemaVersion,
		Outcome:         outcome,
		RuleFired:       rule,
		RulebookVersion: RulebookVersion,
		Remedy:          remedy,
		Harness:         "codex",
		Inputs:          inputs,
		ActionDigest:    digest,
	}
	if err := automode.ValidateDecision(d); err != nil {
		return automode.Decision{}, err
	}
	return d, nil
}

func validRepo(repo string) bool {
	return len(repo) <= 200 && repoName.MatchString(repo)
}

func mergeRemedy(action normalized) string {
	if action.repo == "" || action.number == 0 {
		return "run Gate for the PR, then retry the exact commit-pinned merge command Gate emits"
	}
	return fmt.Sprintf("run gate gate -repo %s -pr %d -grant grt_... using GATE_STATE, then retry Gate's exact emitted command", action.repo, action.number)
}

type normalized struct {
	envelope   string
	operation  string
	parameters automode.Values
	candidate  string
	repo       string
	number     int
	headSHA    string
	admin      bool
}

var errUnsupported = errors.New("unsupported envelope")

func safeName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", ".")
	return value
}
