package floor

import (
	"regexp"
	"strings"
)

// Per-repo path overrides — the deterministic compensating control for the
// floor's documented gate-machinery blind spot (HELDOUT-01: 8 of 15 under-calls
// were merge gates, verifiers, and driver paths the floor reads as "internal →
// T1"). The table below is rubric-shaped and compiled-in: it encodes OUR repo
// layout, so changing it is a reviewed PR — exactly right for a classifier
// control-plane change. Overrides are applied per file as max(floor, override),
// so they only ever RAISE the floor, never lower it, and a repo not passed via
// -repo contributes no globs. Policy lives here in the rubric layer; the
// classifier core in floor.go only applies it.

// overrideRule raises the floor for one path glob within one repo. The human
// label (why) is what a -v run prints, so a raised verdict stays explainable.
type overrideRule struct {
	re   *regexp.Regexp
	tier Tier
	why  string
}

// mustOverride compiles a path glob into an override rule. Globs use `**`
// (any run of characters, crossing `/`) and `*` (any run within one path
// segment); every other character is literal.
func mustOverride(glob string, tier Tier, why string) overrideRule {
	return overrideRule{re: globToRe(glob), tier: tier, why: why}
}

// repoOverrides is the compiled-in table: repo ("owner/name") → path override
// rules. Two bands, split by consequence:
//
//   - T3 — merge-authorization state, the verifier ladder, and the exit-code
//     seam. A fail-open here drops @claude and the adversarial pass exactly
//     where it matters most (HELDOUT-01 labeled gate#3/#5/#9 at T3).
//   - T2 — the broader gate/driver/triage machinery: owner review.
//
// A file can match both a T3 and a T2 rule (e.g. cmd/gate/main.go matches the
// exit-code rule and the broad gate rule); max(floor, override) resolves it to
// the higher tier, so the bands need no mutual exclusion. Existing RUBRIC path
// rules are untouched — labels/** already floors at T3, and it wins by max.
// Keys are lowercased owner/repo: GitHub treats them case-insensitively, and
// ClassifyRepo lowercases the -repo arg before indexing, so a mis-cased caller
// (itshabib/workbench) still hits the control rather than silently skipping it.
var repoOverrides = map[string][]overrideRule{
	"itshabib/workbench": {
		// T3 band — merge-authorization and the load-bearing exit-code contract.
		mustOverride("cmd/gate/internal/state/**", T3, "gate merge-authorization state"),
		mustOverride("cmd/gate/internal/verify/**", T3, "gate verifier ladder"),
		mustOverride("cmd/gate/internal/capability/**", T3, "gate grant minting/checking (signing path)"),
		mustOverride("cmd/gate/internal/tier/**", T3, "gate tier ordering (verdict composition + grant ceilings)"),
		mustOverride("cmd/gate/main.go", T3, "gate exit-code contract"),
		// T3 — this override table is classifier control-plane, same class as
		// RUBRIC §5.4's labels/**: a bad glob here silently re-opens the blind
		// spot, so its own edits earn the adversarial pass.
		mustOverride("cmd/triage/internal/floor/overrides.go", T3, "classifier control-plane (override table)"),
		// T2 band — the rest of gate, plus triage's own classifier machinery.
		mustOverride("cmd/gate/**", T2, "gate machinery"),
		mustOverride("cmd/triage/**", T2, "triage machinery"),
	},
	"itshabib/ship": {
		// T2 band — the driver merge/state machine.
		mustOverride("packages/driver/**", T2, "driver machinery"),
	},
}

// globToRe compiles a restricted path glob into an anchored regexp. `**`
// matches any run of characters (including `/`); `*` matches any run within a
// single path segment (no `/`); every other character is literal. The table
// uses only directory-prefix (`dir/**`) and exact-file globs today, but both
// wildcard forms keep the glob language honest for future rows.
func globToRe(glob string) *regexp.Regexp {
	var b strings.Builder
	b.WriteByte('^')
	for i := 0; i < len(glob); {
		switch {
		case strings.HasPrefix(glob[i:], "**"):
			b.WriteString(".*")
			i += 2
		case glob[i] == '*':
			b.WriteString("[^/]*")
			i++
		default:
			b.WriteString(regexp.QuoteMeta(glob[i : i+1]))
			i++
		}
	}
	b.WriteByte('$')
	return regexp.MustCompile(b.String())
}
