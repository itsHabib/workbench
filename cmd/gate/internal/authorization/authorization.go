// Package authorization exports and validates the promotion of exact-head Gate
// decisions. GitHub transport remains in cmd/gate; this package contains only
// Gate-owned policy over typed state and GitHub facts.
package authorization

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/contracts"
	"github.com/itsHabib/workbench/contracts/gateauthorization"
)

const validity = 30 * time.Minute

type actionBody struct {
	Outcome string   `json:"outcome"`
	Verdict string   `json:"verdict"`
	Grant   string   `json:"grant"`
	Command string   `json:"command"`
	Argv    []string `json:"argv"`
}

type runFacts struct {
	subject contracts.Subject
	seen    bool
}

type terminal struct {
	artifact state.Artifact
	order    int
}

// Export derives an immutable authorization from an audited Gate snapshot.
func Export(audit state.AuditResult, run string) (gateauthorization.Artifact, error) {
	if !audit.OK {
		return gateauthorization.Artifact{}, fmt.Errorf("authorization_state_invalid: %s", audit.Reason)
	}
	facts, terminals, err := index(audit.All)
	if err != nil {
		return gateauthorization.Artifact{}, err
	}
	target, ok := terminals[run]
	if !ok {
		return gateauthorization.Artifact{}, fmt.Errorf("authorization_run_not_terminal: %s", run)
	}
	runFact, ok := facts[run]
	if !ok || !runFact.seen {
		return gateauthorization.Artifact{}, fmt.Errorf("authorization_subject_missing: %s", run)
	}
	if superseded(runFact.subject, target, facts, terminals) {
		return gateauthorization.Artifact{}, errors.New("authorization_superseded: a newer terminal outcome exists for the PR")
	}
	if target.artifact.Kind != state.KindAction {
		return gateauthorization.Artifact{}, errors.New("authorization_not_would_merge: newest terminal is an escalation")
	}
	var body actionBody
	if err := json.Unmarshal(target.artifact.Body, &body); err != nil {
		return gateauthorization.Artifact{}, fmt.Errorf("authorization_action_malformed: %w", err)
	}
	if body.Outcome != gateauthorization.OutcomeWouldMerge {
		return gateauthorization.Artifact{}, fmt.Errorf("authorization_not_would_merge: outcome is %q", body.Outcome)
	}
	if len(target.artifact.Parents) < 2 || body.Verdict != target.artifact.Parents[0] ||
		body.Grant != target.artifact.Parents[1] {
		return gateauthorization.Artifact{}, errors.New("authorization_action_lineage_mismatch")
	}
	subject := gateauthorization.Subject{
		Repo: runFact.subject.Repo, Number: runFact.subject.Number,
		HeadSHA: runFact.subject.HeadSHA,
	}
	want := gateauthorization.ExpectedMergeArgv(subject)
	if !equal(body.Argv, want) || body.Command != strings.Join(body.Argv, " ") {
		return gateauthorization.Artifact{}, errors.New("authorization_action_argv_mismatch")
	}
	artifact := gateauthorization.Artifact{
		SchemaVersion:   gateauthorization.SchemaVersion,
		AuthorizationID: "gau_" + target.artifact.Hash,
		Subject:         subject,
		Gate: gateauthorization.Decision{
			RunID: run, ActionID: target.artifact.ID, ActionHash: target.artifact.Hash,
		},
		Outcome:   body.Outcome,
		MergeArgv: append([]string(nil), body.Argv...),
		IssuedAt:  target.artifact.Time.UTC(),
		ExpiresAt: target.artifact.Time.UTC().Add(validity),
		TrustRoot: gateauthorization.TrustRoot{
			Kind:        gateauthorization.TrustGitHubEnvironment,
			Environment: gateauthorization.DefaultEnvironment,
		},
	}
	if err := gateauthorization.Validate(artifact); err != nil {
		return gateauthorization.Artifact{}, fmt.Errorf("authorization_contract_invalid: %w", err)
	}
	return artifact, nil
}

func index(artifacts []state.Artifact) (map[string]runFacts, map[string]terminal, error) {
	facts := make(map[string]runFacts)
	terminals := make(map[string]terminal)
	for order, artifact := range artifacts {
		if artifact.Kind == state.KindVerdict {
			var verdict contracts.Verdict
			if err := json.Unmarshal(artifact.Body, &verdict); err != nil {
				return nil, nil, fmt.Errorf("authorization_verdict_malformed: %w", err)
			}
			if verdict.Subject.Repo != "" && verdict.Subject.Number > 0 && verdict.Subject.HeadSHA != "" {
				facts[artifact.Run] = runFacts{subject: verdict.Subject, seen: true}
			}
		}
		if artifact.Kind == state.KindAction || artifact.Kind == state.KindEscalation {
			terminals[artifact.Run] = terminal{artifact: artifact, order: order}
		}
	}
	return facts, terminals, nil
}

func superseded(subject contracts.Subject, target terminal, facts map[string]runFacts, terminals map[string]terminal) bool {
	for run, candidate := range terminals {
		if candidate.order <= target.order {
			continue
		}
		fact, ok := facts[run]
		if !ok {
			continue
		}
		if fact.subject.Repo == subject.Repo && fact.subject.Number == subject.Number {
			return true
		}
	}
	return false
}

func equal(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// PromotionFacts are the trusted workflow and live GitHub facts checked before
// the required status may be written.
type PromotionFacts struct {
	Now            time.Time
	Repository     string
	DefaultBranch  string
	WorkflowRef    string
	WorkflowEvent  string
	GitHubActions  bool
	Environment    Environment
	PullRequest    PullRequest
	OpenPRsForHead []int
	Statuses       []Status
}

// Actor is the GitHub identity attached to a commit status.
type Actor struct {
	Login string
	Type  string
}

// Environment is the security projection of GitHub environment settings.
type Environment struct {
	Name                  string
	AdminBypassKnown      bool
	CanAdminsBypass       bool
	ProtectedBranchesOnly bool
	RequiredReviewers     int
	PreventSelfReview     bool
}

// PullRequest is the live exact-head GitHub projection.
type PullRequest struct {
	Number  int
	State   string
	HeadSHA string
}

// Status is the replay-relevant projection of a commit status.
type Status struct {
	Context string
	State   string
	Body    string
	Creator Actor
}

// Promotion is the status effect CheckPromotion authorized.
type Promotion struct {
	Duplicate   bool
	Description string
}

// CheckPromotion refuses every input except a fresh, unambiguous exact-head
// authorization running behind the configured protected environment.
func CheckPromotion(artifact gateauthorization.Artifact, facts PromotionFacts) (Promotion, error) {
	if err := gateauthorization.Validate(artifact); err != nil {
		return Promotion{}, fmt.Errorf("authorization_malformed: %w", err)
	}
	environmentErr := errEnvironment(facts.Environment)
	switch {
	case facts.Now.Before(artifact.IssuedAt.Add(-time.Minute)):
		return Promotion{}, errors.New("authorization_not_yet_valid")
	case !facts.Now.Before(artifact.ExpiresAt):
		return Promotion{}, errors.New("authorization_expired")
	case !facts.GitHubActions || facts.WorkflowEvent != "workflow_dispatch":
		return Promotion{}, errors.New("authorization_untrusted_runtime")
	case facts.Repository != artifact.Subject.Repo:
		return Promotion{}, errors.New("authorization_repo_mismatch")
	case facts.WorkflowRef != "refs/heads/"+facts.DefaultBranch:
		return Promotion{}, errors.New("authorization_untrusted_ref")
	case environmentErr != nil:
		return Promotion{}, environmentErr
	case facts.PullRequest.Number != artifact.Subject.Number:
		return Promotion{}, errors.New("authorization_pr_mismatch")
	case facts.PullRequest.State != "open":
		return Promotion{}, errors.New("authorization_pr_not_open")
	case facts.PullRequest.HeadSHA != artifact.Subject.HeadSHA:
		return Promotion{}, errors.New("authorization_head_mismatch")
	case len(facts.OpenPRsForHead) != 1 || facts.OpenPRsForHead[0] != artifact.Subject.Number:
		return Promotion{}, errors.New("authorization_head_ambiguous")
	}
	description := "authorized " + artifact.AuthorizationID + " run=" + artifact.Gate.RunID
	for _, status := range facts.Statuses {
		if status.Context == "gate" && status.State == "success" && status.Body == description &&
			status.Creator.Login == "github-actions[bot]" && status.Creator.Type == "Bot" {
			return Promotion{Duplicate: true, Description: description}, nil
		}
	}
	return Promotion{Description: description}, nil
}

func errEnvironment(environment Environment) error {
	switch {
	case environment.Name != gateauthorization.DefaultEnvironment:
		return errors.New("authorization_environment_missing")
	case !environment.AdminBypassKnown:
		return errors.New("authorization_environment_admin_bypass_unknown")
	case environment.CanAdminsBypass:
		return errors.New("authorization_environment_admin_bypass_enabled")
	case !environment.ProtectedBranchesOnly:
		return errors.New("authorization_environment_branch_policy_weak")
	case environment.RequiredReviewers < 1:
		return errors.New("authorization_environment_review_missing")
	case !environment.PreventSelfReview:
		return errors.New("authorization_environment_self_review_enabled")
	default:
		return nil
	}
}
