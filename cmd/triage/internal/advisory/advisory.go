// Package advisory verifies the agent advisory pass — the escalate-only cloud
// tier above the deterministic floor (RUBRIC §6, spec §4).
//
// The advisory itself is judgment (a host agent reading the diff); this package
// is the part that must NOT be judgment: a deterministic verifier that decides
// whether a proposed escalation earned trust, and the merge that computes the
// final tier. An escalation the verifier rejects contributes nothing — the
// advisory must point at real evidence in the real diff or the floor stands.
// Verifier > confidence: the checks are structural, the confidence field is
// recorded but never trusted (EVAL-01 and the S2 gate both measured confidence
// carrying zero signal).
package advisory

import (
	"regexp"
	"strings"

	"github.com/itsHabib/workbench/cmd/triage/internal/floor"
)

// Triggers are the RUBRIC §6 escalation triggers. An escalating proposal must
// name one — "none" is schema-legal for non-escalating results only. Growing
// this set is a RUBRIC §6 edit (control-plane, T3).
var Triggers = map[string]bool{
	"trust-boundary-widening": true,
	"production-default":      true,
	"invariant-relocation":    true,
	"gate-machinery":          true,
	"plan-of-record":          true,
}

// MinEvidence is the minimum evidence length: a single common word or a run of
// whitespace is a substring of almost any diff, so shorter quotes prove nothing.
const MinEvidence = 20

// Proposal is one advisory escalation as produced by the host agent.
type Proposal struct {
	Escalate   string  `json:"escalate"`   // "none" | "T2" | "T3"
	Trigger    string  `json:"trigger"`    // "none" or a Triggers key
	Evidence   string  `json:"evidence"`   // verbatim quote from the diff
	Confidence float64 `json:"confidence"` // recorded, never trusted
	Why        string  `json:"why"`
}

// Verdict is the verified advisory outcome joined with the floor.
type Verdict struct {
	Floor    string   `json:"floor"`
	Escalate string   `json:"escalate"` // the proposal, as given
	Trigger  string   `json:"trigger"`
	Evidence string   `json:"evidence"`
	Why      string   `json:"why,omitempty"`
	Rejected []string `json:"rejected,omitempty"` // verifier failures; non-empty = proposal contributed nothing
	Final    string   `json:"final"`              // max(floor, trusted escalation)
	Route    string   `json:"route"`
}

var tierRank = map[string]int{"T0": 0, "T1": 1, "T2": 2, "T3": 3}

var routes = map[string]string{
	"T0": "auto-eligible (recommend-only)",
	"T1": "peer",
	"T2": "owner",
	"T3": "owner+adversarial",
}

// normSpace collapses all whitespace runs so a quote survives reflow. A quote
// that normalizes to empty matches everything and proves nothing.
var reSpace = regexp.MustCompile(`\s+`)

func normSpace(s string) string {
	return strings.TrimSpace(reSpace.ReplaceAllString(s, " "))
}

// searchText builds the corpus an evidence quote is checked against: the raw
// diff plus a content view with each line's +/-/context marker stripped —
// agents quote code, not diff markup, and a multi-line quote must not fail on
// the per-line '+' prefixes.
func searchText(diffText string) string {
	lines := strings.Split(diffText, "\n")
	stripped := make([]string, len(lines))
	for i, ln := range lines {
		if len(ln) > 0 && (ln[0] == '+' || ln[0] == '-' || ln[0] == ' ') {
			ln = ln[1:]
		}
		stripped[i] = ln
	}
	return normSpace(diffText) + "\n" + normSpace(strings.Join(stripped, "\n"))
}

// Check verifies a proposal against the diff text the agent claims to have
// read. A non-escalating proposal has nothing to verify. An escalating one
// must pass ALL checks; each failure is named so the host can retry with the
// reason. (spec §4.2 — the three-part verifier.)
func Check(diffText string, p Proposal) []string {
	if p.Escalate == "" || p.Escalate == "none" {
		return nil
	}
	var fails []string
	if p.Escalate != "T2" && p.Escalate != "T3" {
		fails = append(fails, "escalate must be none|T2|T3")
	}
	if p.Trigger == "none" || !Triggers[p.Trigger] {
		fails = append(fails, "escalation must name a real §6 trigger, not none")
	}
	// length is checked AFTER normalization: raw whitespace padding (24 spaces +
	// "return nil, err") passes a raw-byte length gate but normalizes to a common
	// 15-char phrase that proves nothing (adversarial review of PR #4).
	ev := normSpace(p.Evidence)
	if len(ev) < MinEvidence {
		fails = append(fails, "evidence shorter than 20 chars (normalized) proves nothing")
	}
	if ev == "" || !strings.Contains(searchText(diffText), ev) {
		fails = append(fails, "evidence is not a verbatim quote from the diff")
	}
	return fails
}

// Merge computes the final verdict: the floor always an operand, a trusted
// escalation the only other one. A rejected escalation cannot raise (the
// advisory must earn its slot) and nothing can ever lower the floor.
func Merge(res floor.Result, diffText string, p Proposal) Verdict {
	v := Verdict{
		Floor:    res.Floor.String(),
		Escalate: p.Escalate,
		Trigger:  p.Trigger,
		Evidence: p.Evidence,
		Why:      p.Why,
		Rejected: Check(diffText, p),
		Final:    res.Floor.String(),
	}
	if v.Escalate == "" {
		v.Escalate = "none"
	}
	trusted := len(v.Rejected) == 0 && tierRank[p.Escalate] > tierRank[v.Floor]
	if trusted {
		v.Final = p.Escalate
	}
	v.Route = routes[v.Final]
	return v
}
