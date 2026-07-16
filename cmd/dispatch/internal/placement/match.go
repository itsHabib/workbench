package placement

import "github.com/itsHabib/workbench/cmd/dispatch/internal/policy"

// match scans rules in file order and returns the first whose constraints all
// hold. First-match is the whole ordering contract: a later, broader rule never
// shadows an earlier, narrower one, so the operator reads precedence top-down.
func match(rules []policy.Rule, d Descriptor) (policy.Rule, bool) {
	for _, r := range rules {
		if matches(r.Match, d) {
			return r, true
		}
	}
	return policy.Rule{}, false
}

// matches reports whether a descriptor satisfies one match block. Every set
// constraint must hold (AND); an unset constraint is "any", so an empty match
// is a catch-all.
func matches(m policy.Match, d Descriptor) bool {
	if m.TaskClass != "" && m.TaskClass != d.TaskClass {
		return false
	}
	if m.MaxWeightedLOC != nil && d.loc() > *m.MaxWeightedLOC {
		return false
	}
	if len(m.RiskTier) > 0 && !contains(m.RiskTier, d.RiskTier) {
		return false
	}
	return true
}

func contains(allow []string, v string) bool {
	for _, a := range allow {
		if a == v {
			return true
		}
	}
	return false
}
