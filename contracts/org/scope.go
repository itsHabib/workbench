// Scope membership is the mechanical predicate the skills already claim to
// apply: whether a work URI falls inside a charter's scope. The first live
// multi-lane run proved the claim was aspirational — assign accepted a
// `banana:` URI into a `github:`-scoped lane without a murmur — so the
// predicate now exists in one place, as a pure function, where intake reads
// it today and assign can enforce it later.
//
// A scope entry is a URI prefix that must end at a structural boundary, so
// `github:owner/repo` covers `github:owner/repo#88` but never
// `github:owner/repository`. An entry that itself ends in a separator is an
// explicit open prefix — `jira:PROJ-` covers a project, `github:owner/` an
// owner, `jira:` a whole scheme — which is how a charter states its grain
// without glob syntax. A scheme can never cover another scheme: `jira:…`
// falls outside every `github:` entry by construction, which the field
// report established as the deliberate rule, with any cross-scheme span
// stated at the charter as separate entries.

package org

import "strings"

// openPrefix reports whether an entry's final character marks it as an
// explicit open prefix. The '-' is here so `jira:PROJ-` can state project
// grain, but it is NOT a boundary below: `github:owner/my` must never cover
// `github:owner/my-repo`.
func openPrefix(c byte) bool {
	return c == '/' || c == '#' || c == ':' || c == '-'
}

// boundary reports whether c separates a work URI's structural segments.
func boundary(c byte) bool {
	return c == '/' || c == '#' || c == ':'
}

// InScope reports whether a single scope entry covers the work URI.
func InScope(entry, work string) bool {
	if entry == "" || work == "" {
		return false
	}
	if entry == work {
		return true
	}
	if !strings.HasPrefix(work, entry) {
		return false
	}
	if openPrefix(entry[len(entry)-1]) {
		return true
	}
	return boundary(work[len(entry)])
}

// MatchScope reports the first scope entry covering the work URI, in charter
// order, and whether one exists.
func MatchScope(scope []string, work string) (string, bool) {
	for _, entry := range scope {
		if InScope(entry, work) {
			return entry, true
		}
	}
	return "", false
}
