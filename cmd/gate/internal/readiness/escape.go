// Package readiness owns Gate's policy for explaining how an operator can
// leave a non-zero terminal state. It records no artifacts and imports no
// other Workbench tool.
package readiness

import "strings"

// SubstrateRoute is the external command prefix used when Gate's state store
// cannot safely answer diagnostic queries itself.
const SubstrateRoute = "ls -ld"

// Route is the one runnable way forward printed with a terminal state.
type Route struct {
	Why  string `json:"why"`
	Next string `json:"next"`
}

// Escape returns a route for every code, including an empty or previously
// unseen one. A broken state substrate cannot route through next/console/
// resolve because all three read that substrate; audit is the external
// diagnostic boundary for that case.
func Escape(code string, substrateOK bool) Route {
	if !substrateOK {
		return Route{
			Why:  "the gate state substrate is unavailable; diagnose custody and chain integrity before retrying",
			Next: SubstrateRoute,
		}
	}
	if SelfGated(code) {
		return Route{
			Why:  "the automated decision path failed; inspect the recorded run and take the independent route it names",
			Next: "gate next",
		}
	}
	return Route{
		Why:  "inspect Gate's current state before choosing another effectful verb",
		Next: "gate next",
	}
}

// Code extracts Gate's stable reason code from a reason or wrapped error. It
// deliberately returns an empty string when none is present; Escape is total
// over that value rather than pretending an unrecognized error was classified.
func Code(reason string) string {
	for _, code := range selfGatedCodes {
		if containsCode(reason, code) {
			return code
		}
	}
	for _, code := range knownCodes {
		if containsCode(reason, code) {
			return code
		}
	}
	return ""
}

func containsCode(reason, code string) bool {
	for start := strings.Index(reason, code); start >= 0; {
		end := start + len(code)
		if codeBoundary(reason, start-1) && codeBoundary(reason, end) {
			return true
		}
		next := strings.Index(reason[end:], code)
		if next < 0 {
			return false
		}
		start = end + next
	}
	return false
}

func codeBoundary(reason string, at int) bool {
	if at < 0 || at >= len(reason) {
		return true
	}
	b := reason[at]
	return !(b >= 'a' && b <= 'z') && !(b >= 'A' && b <= 'Z') && !(b >= '0' && b <= '9') && b != '_'
}

// SelfGated reports whether retrying the same automated component cannot fix
// the named failure. Registry drift may reduce message precision, but Escape
// remains present for every code.
func SelfGated(code string) bool {
	for _, registered := range selfGatedCodes {
		if code == registered {
			return true
		}
	}
	return false
}

// RetryHelps is conservative: a non-zero terminal never tells the caller to
// repeat the same command blindly. A specialized route may name a changed
// provider or wider grant, but that is a different invocation.
func RetryHelps(string) bool { return false }

var selfGatedCodes = []string{
	"judge_provider_failed",
	"judgment_malformed",
	"judgment_malformed_escalation",
	"judge_provider_unconfigured",
	"judge_provider_unsupported",
}

var knownCodes = []string{
	"grant_cycle_exceeded",
	"grant_tier_exceeded",
	"cycle_count_unreadable",
	"grant_expired",
	"grant_absent",
	"grant_key_missing",
	"grant_key_invalid",
	"capability_refused",
	"log_integrity_failed",
	"anchor_key_missing",
	"anchor_key_invalid",
	"anchor_missing",
}
