package evidence

import (
	"encoding/json"
	"fmt"
	"net/url"
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
// protection endpoint would report "no protection" on a repo that has it. They
// are read as a union, not as alternatives — a base can carry both, and GitHub
// enforces whichever requires more.

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

// mechanism is one enforcement mechanism's answer for a base branch.
//
// The three states are distinct and all load-bearing: readable+present carries
// a real strictness fact, readable+absent means the mechanism is not configured
// on this branch, and unreadable means gate could not establish either — which
// is never the same as "no requirement".
type mechanism struct {
	readable bool
	present  bool
	strict   bool
	source   string
	note     string
}

// readProtection asks BOTH mechanisms and combines them. They are not
// alternatives: a base can carry classic protection with strict false AND an
// overlapping ruleset that requires up-to-date heads, and GitHub enforces the
// union. Falling through to rulesets only when classic protection is absent
// would record such a base as non-strict and emit the doomed merge command this
// rung exists to prevent.
func readProtection(pr PRRef, baseRef string, fetch protectionFetch) ProtectionBody {
	if baseRef == "" {
		return ProtectionBody{Note: "PR view recorded no base branch"}
	}
	return combine(
		classicProtection(pr, baseRef, fetch),
		rulesetProtection(pr, baseRef, fetch),
	)
}

// combine reduces the two mechanisms to one recorded fact. Strictness is a
// union — any mechanism requiring up-to-date heads makes the base strict. The
// negative answer is the demanding one: "nothing requires it" is only a fact
// when EVERY mechanism was read, so a single unreadable mechanism leaves the
// whole setting unknown.
func combine(mechanisms ...mechanism) ProtectionBody {
	var strictSources, presentSources, notes []string
	readable := true
	for _, m := range mechanisms {
		if !m.readable {
			readable = false
			notes = append(notes, m.note)
			continue
		}
		if m.present {
			presentSources = append(presentSources, m.source)
		}
		if m.strict {
			strictSources = append(strictSources, m.source)
		}
	}
	// A strict answer stands even if another mechanism was unreadable: the
	// requirement is already established, and the unread one could only add
	// another requirement gate is already honouring.
	if len(strictSources) > 0 {
		return ProtectionBody{Readable: true, Strict: true, Source: strings.Join(strictSources, "+")}
	}
	if !readable {
		return ProtectionBody{Note: strings.Join(notes, "; ")}
	}
	if len(presentSources) == 0 {
		return ProtectionBody{Readable: true, Source: ProtectionSourceNone}
	}
	return ProtectionBody{Readable: true, Source: strings.Join(presentSources, "+")}
}

func classicProtection(pr PRRef, baseRef string, fetch protectionFetch) mechanism {
	m := mechanism{source: ProtectionSourceBranch}
	raw, err := fetch(fmt.Sprintf("repos/%s/branches/%s/protection", pr.Repo, escapeRef(baseRef)))
	if err != nil {
		// Only GitHub's specific "Branch not protected" body is evidence the
		// branch carries no classic protection. A bare 404 is not: a missing
		// repository, a mistyped branch, and a token without admin scope all
		// return one too, and reading those as "unprotected" would let a strict
		// base pass the preflight and receive a doomed merge command.
		if notProtected(err) {
			m.readable = true
			return m
		}
		m.note = err.Error()
		return m
	}
	strict, perr := strictFromProtection(raw)
	if perr != nil {
		m.note = perr.Error()
		return m
	}
	m.readable, m.present, m.strict = true, true, strict
	return m
}

func rulesetProtection(pr PRRef, baseRef string, fetch protectionFetch) mechanism {
	m := mechanism{source: ProtectionSourceRuleset}
	raw, err := fetch(fmt.Sprintf("repos/%s/rules/branches/%s", pr.Repo, escapeRef(baseRef)))
	if err != nil {
		// This endpoint answers an empty list for a branch with no rules, so it
		// has no "not configured" error shape at all: every failure here is an
		// unread mechanism.
		m.note = err.Error()
		return m
	}
	rules, rerr := statusCheckRules(raw)
	if rerr != nil {
		m.note = rerr.Error()
		return m
	}
	m.readable, m.present, m.strict = true, rules.present, rules.strict
	return m
}

// notProtected reports whether the error is GitHub's specific "branch not
// protected" answer on the protection endpoint — the one 404 that IS a fact
// about the branch rather than a failure to read it.
func notProtected(err error) bool {
	return err != nil &&
		strings.Contains(err.Error(), "Branch not protected") &&
		strings.Contains(err.Error(), "HTTP 404")
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

// statusCheckRules reports whether the branch has a required-status-checks rule
// at all, and whether any such rule requires up-to-date heads.
func statusCheckRules(raw json.RawMessage) (struct{ present, strict bool }, error) {
	var out struct{ present, strict bool }
	var rules []struct {
		Type       string `json:"type"`
		Parameters *struct {
			Strict *bool `json:"strict_required_status_checks_policy"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &rules); err != nil {
		return out, fmt.Errorf("evidence: parse branch rules: %w", err)
	}
	for _, r := range rules {
		if r.Type != "required_status_checks" {
			continue
		}
		out.present = true
		if r.Parameters == nil || r.Parameters.Strict == nil {
			return out, fmt.Errorf("evidence: branch rule reports no strict policy field")
		}
		if *r.Parameters.Strict {
			out.strict = true
		}
	}
	return out, nil
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

// escapeRef makes a branch name safe as ONE path segment. gh api takes an
// already-formed endpoint and substitutes nothing here, so a base like
// release/1.2 would otherwise split into two segments: both reads would miss
// the real branch, degrade to unreadable, and the rung would pass a BEHIND head
// on a strict base straight to the doomed merge command. The degrade-to-pass
// rule is right; it must not be reachable through an avoidable bug.
//
// pr.Repo is deliberately NOT escaped — it is owner/repo, two segments by
// construction.
func escapeRef(ref string) string {
	return url.PathEscape(ref)
}

func ghAPI(path string) (json.RawMessage, error) {
	return gh("api", path)
}
