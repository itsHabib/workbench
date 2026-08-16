package evidence

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// Branch protection decides whether a BEHIND pull request may merge at all.
// GitHub rejects the pinned merge command on a repository whose base branch
// requires the head to be up to date, and gate only learned that from the
// rejection — after it had already emitted a command that was never going to
// land. This file records the one fact that makes the difference decidable:
// does the base branch require up-to-date-ness, and by which mechanism.
//
// It records the fact; it never decides what the fact costs. BEHIND alone is
// not a refresh requirement — on the portfolio's unprotected and non-strict
// repositories a behind branch merges cleanly, and demanding a refresh there
// would buy a wasted CI cycle on every merge. The verifier reads strictness and
// BEHIND together (see verify.UpToDate).
//
// Two mechanisms carry the setting and both are live in the portfolio (see
// docs/auto-mode-defaults.md): classic branch protection
// (required_status_checks.strict) and repository rulesets
// (required_status_checks.strict_required_status_checks_policy). Workbench's
// own main is unprotected classically yet carries rulesets, so reading only the
// protection endpoint would report "no protection" on a repo that has it.

// Protection sources, recorded so an audit reader can tell which mechanism
// answered — the two endpoints disagree in the wild.
const (
	ProtectionSourceBranch  = "branch-protection"
	ProtectionSourceRuleset = "ruleset"
	ProtectionSourceNone    = "none"
)

// ProtectionBody is the recorded protection read for a PR's base branch.
//
// Readable false means gate could not establish the setting — a token without
// admin scope on the repository, a network failure, an endpoint shape it does
// not understand. It is NOT "no protection": consumers must degrade to the
// behaviour they had before this evidence existed and say so, never harden into
// a block on an unread fact.
type ProtectionBody struct {
	PR      PRRef  `json:"pr"`
	BaseRef string `json:"base_ref"`
	// Readable reports whether Strict is a fact. False leaves Strict meaningless.
	Readable bool `json:"readable"`
	// Strict reports whether the base branch requires a PR to be up to date
	// with it before merging.
	Strict bool   `json:"strict"`
	Source string `json:"source"`
	// Note carries the human-readable reason a read was unreadable, for the
	// audit trail and the verdict's degraded-mode text.
	Note string `json:"note,omitempty"`
}

// protectionFetch reads one gh api endpoint. Split out so tests exercise the
// classification without a network or an authenticated gh.
type protectionFetch func(path string) (json.RawMessage, error)

// Protection records the base branch's up-to-date requirement as evidence and
// returns the artifact id.
//
// A failed read is recorded as unreadable evidence, never returned as an error:
// gate must not turn "I could not see the protection setting" into a hard
// failure that blocks a merge flow which worked fine before the read existed.
// Only the state append can fail the call.
func Protection(st *state.Store, run string, pr PRRef, baseRef string) (string, error) {
	return protectionFrom(st, run, pr, baseRef, ghAPI)
}

func protectionFrom(st *state.Store, run string, pr PRRef, baseRef string, fetch protectionFetch) (string, error) {
	body := readProtection(pr, baseRef, fetch)
	body.PR, body.BaseRef = pr, baseRef
	a, err := st.Append(state.KindEvidence, run, nil, body)
	if err != nil {
		return "", err
	}
	return a.ID, nil
}

// readProtection classifies the base branch: classic protection first, then
// rulesets, which is the order of specificity — a repo with classic protection
// has the answer there, and the ruleset endpoint is the fallback for repos
// (workbench) whose only enforcement is a ruleset.
func readProtection(pr PRRef, baseRef string, fetch protectionFetch) ProtectionBody {
	if baseRef == "" {
		return ProtectionBody{Note: "PR view recorded no base branch"}
	}
	raw, err := fetch(fmt.Sprintf("repos/%s/branches/%s/protection", pr.Repo, baseRef))
	if err == nil {
		strict, perr := strictFromProtection(raw)
		if perr != nil {
			return ProtectionBody{Note: perr.Error()}
		}
		return ProtectionBody{Readable: true, Strict: strict, Source: ProtectionSourceBranch}
	}
	// 404 is the documented answer for "this branch has no classic protection",
	// not a failed read. Anything else (403 on a token without admin scope, a
	// transport failure) leaves the setting genuinely unknown.
	if !notProtected(err) {
		return ProtectionBody{Note: err.Error()}
	}
	return rulesetProtection(pr, baseRef, fetch)
}

func rulesetProtection(pr PRRef, baseRef string, fetch protectionFetch) ProtectionBody {
	raw, err := fetch(fmt.Sprintf("repos/%s/rules/branches/%s", pr.Repo, baseRef))
	if err != nil {
		return ProtectionBody{Note: err.Error()}
	}
	strict, rerr := strictFromRules(raw)
	if rerr != nil {
		return ProtectionBody{Note: rerr.Error()}
	}
	if !strict {
		return ProtectionBody{Readable: true, Source: ProtectionSourceNone}
	}
	return ProtectionBody{Readable: true, Strict: true, Source: ProtectionSourceRuleset}
}

// notProtected reports whether the error is GitHub's "branch not protected"
// answer. gh surfaces it as an HTTP 404 on the protection endpoint.
func notProtected(err error) bool {
	return err != nil && strings.Contains(err.Error(), "HTTP 404")
}

func strictFromProtection(raw json.RawMessage) (bool, error) {
	var p struct {
		RequiredStatusChecks *struct {
			Strict *bool `json:"strict"`
		} `json:"required_status_checks"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return false, fmt.Errorf("evidence: parse branch protection: %w", err)
	}
	// No required status checks configured: protection exists but carries no
	// up-to-date requirement. A present block with a null strict is shape drift,
	// and an unread fact must not read as false.
	if p.RequiredStatusChecks == nil {
		return false, nil
	}
	if p.RequiredStatusChecks.Strict == nil {
		return false, fmt.Errorf("evidence: branch protection reports no strict field")
	}
	return *p.RequiredStatusChecks.Strict, nil
}

func strictFromRules(raw json.RawMessage) (bool, error) {
	var rules []struct {
		Type       string `json:"type"`
		Parameters *struct {
			Strict *bool `json:"strict_required_status_checks_policy"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &rules); err != nil {
		return false, fmt.Errorf("evidence: parse branch rules: %w", err)
	}
	for _, r := range rules {
		if r.Type != "required_status_checks" || r.Parameters == nil || r.Parameters.Strict == nil {
			continue
		}
		if *r.Parameters.Strict {
			return true, nil
		}
	}
	return false, nil
}

// BaseRef reads the PR's base branch out of recorded view evidence. Empty when
// the view carries none, which Protection records as an unreadable setting.
func BaseRef(view json.RawMessage) string {
	var v struct {
		BaseRefName string `json:"baseRefName"`
	}
	if err := json.Unmarshal(view, &v); err != nil {
		return ""
	}
	return v.BaseRefName
}

func ghAPI(path string) (json.RawMessage, error) {
	return gh("api", path)
}
