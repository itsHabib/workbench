// Package rulesetplan validates the staged, non-effectful branch-policy plan
// for the Gate executor. It never calls GitHub or applies repository settings.
package rulesetplan

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
)

// SchemaVersion is the supported staged ruleset-plan major.
const (
	SchemaVersion    = 1
	StatusNotApplied = "authorized_not_applied"
)

// Plan is an actor-symbolic policy document. Operator-supplied GitHub actor IDs
// are deliberately absent until registration/bootstrap.
type Plan struct {
	SchemaVersion int       `json:"schema_version"`
	Repository    string    `json:"repository"`
	Status        string    `json:"status"`
	Rulesets      []Ruleset `json:"rulesets"`
}

// Ruleset is one symbolic target-and-actor policy layer.
type Ruleset struct {
	Name               string          `json:"name"`
	Includes           []string        `json:"includes"`
	Excludes           []string        `json:"excludes"`
	RestrictUpdates    bool            `json:"restrict_updates"`
	RequirePullRequest bool            `json:"require_pull_request"`
	BlockDeletion      bool            `json:"block_deletion"`
	BlockForcePush     bool            `json:"block_force_push"`
	RequiredChecks     []RequiredCheck `json:"required_checks"`
	BypassActors       []BypassActor   `json:"bypass_actors"`
	AllowedWriters     []string        `json:"allowed_writers"`
}

// RequiredCheck pins one status context to a symbolic Integration.
type RequiredCheck struct {
	Context        string `json:"context"`
	IntegrationRef string `json:"integration_ref"`
}

// BypassActor names an operator-resolved actor and its narrow bypass mode.
type BypassActor struct {
	ActorRef string `json:"actor_ref"`
	Type     string `json:"type"`
	Mode     string `json:"mode"`
}

// Load reads and validates a staged plan.
func Load(path string) (Plan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Plan{}, err
	}
	var plan Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return Plan{}, err
	}
	if err := Validate(plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// Validate pins the mutually exclusive identity composition. It is not a
// substitute for the operator's post-apply GitHub API verification.
func Validate(plan Plan) error {
	if plan.SchemaVersion != SchemaVersion {
		return fmt.Errorf("ruleset plan: unsupported schema_version %d", plan.SchemaVersion)
	}
	if plan.Repository == "" || plan.Status != StatusNotApplied {
		return errors.New("ruleset plan: repository and authorized_not_applied status required")
	}
	byName := make(map[string]Ruleset, len(plan.Rulesets))
	for _, ruleset := range plan.Rulesets {
		if _, exists := byName[ruleset.Name]; exists {
			return fmt.Errorf("ruleset plan: duplicate %s", ruleset.Name)
		}
		byName[ruleset.Name] = ruleset
	}
	if len(byName) != 4 {
		return errors.New("ruleset plan: exactly four layered rulesets required")
	}
	if err := validateMainUpdates(byName["main-updates"]); err != nil {
		return err
	}
	if err := validateStateUpdates(byName["gate-state-updates"]); err != nil {
		return err
	}
	if err := validateOtherBranches(byName["other-branches"]); err != nil {
		return err
	}
	return validateRequiredGate(byName["main-required-gate"])
}

func validateMainUpdates(ruleset Ruleset) error {
	if !exact(ruleset.Includes, []string{"refs/heads/main"}) ||
		!ruleset.RestrictUpdates || !ruleset.RequirePullRequest ||
		!ruleset.BlockDeletion || !ruleset.BlockForcePush ||
		!exactBypass(ruleset.BypassActors, []BypassActor{{
			ActorRef: "gate_app", Type: "Integration", Mode: "pull_request",
		}}) {
		return errors.New("ruleset plan: main updates must be PR-only with sole Gate App bypass")
	}
	return nil
}

func validateStateUpdates(ruleset Ruleset) error {
	if !exact(ruleset.Includes, []string{"refs/heads/gate-state"}) ||
		!ruleset.RestrictUpdates || ruleset.RequirePullRequest ||
		!ruleset.BlockDeletion || !ruleset.BlockForcePush ||
		len(ruleset.AllowedWriters) != 0 ||
		!exactBypass(ruleset.BypassActors, []BypassActor{{
			ActorRef: "gate_app", Type: "Integration", Mode: "always",
		}}) {
		return errors.New("ruleset plan: gate-state must allow only the Gate App")
	}
	return nil
}

func validateOtherBranches(ruleset Ruleset) error {
	if !exact(ruleset.Includes, []string{"~ALL"}) ||
		!exact(ruleset.Excludes, []string{"refs/heads/main", "refs/heads/gate-state"}) ||
		!ruleset.RestrictUpdates ||
		!exact(ruleset.AllowedWriters, []string{"repository_roles", "approved_existing_integrations"}) ||
		contains(ruleset.AllowedWriters, "gate_app") ||
		len(ruleset.BypassActors) != 0 {
		return errors.New("ruleset plan: all other branches must restrict updates to non-Gate writers")
	}
	return nil
}

func validateRequiredGate(ruleset Ruleset) error {
	if !exact(ruleset.Includes, []string{"refs/heads/main"}) ||
		len(ruleset.RequiredChecks) != 1 ||
		ruleset.RequiredChecks[0].Context != "gate" ||
		ruleset.RequiredChecks[0].IntegrationRef != "github_actions" ||
		!exactBypass(ruleset.BypassActors, []BypassActor{{
			ActorRef: "gate_app", Type: "Integration", Mode: "pull_request",
		}}) {
		return errors.New("ruleset plan: main gate check must retain App pin with sole Gate App PR bypass")
	}
	return nil
}

func exact(left, right []string) bool {
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
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

func exactBypass(left, right []BypassActor) bool {
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

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
