// Package verify turns recorded evidence into verdict artifacts and composes
// them monotonically. The ladder law, encoded here rather than in prose:
//
//   - the deterministic floor (code) always runs and can never be lowered;
//   - the local-model rung may only pass or escalate — never block on its own
//     comprehension (small models confabulate on dense content; escalation is
//     the safe failure);
//   - judgment (premium model or operator) resolves escalations, but cannot
//     override a code block — red evidence stays red.
//
// Composition is monotone: worst decision wins, max tier wins, min confidence
// carries. Imports point down: state and the shared contracts vocabulary only.
package verify

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/tier"
	"github.com/itsHabib/workbench/contracts"
)

// The verdict vocabulary is the shared contract: gate emits the canonical
// shape every workbench verifier speaks, so these are aliases, not copies.
// Everything that DECIDES — Reduce, the ladder law, the tier logic — stays
// here: a contract carries no decisions, and a reducer is a decision.
type (
	// Verdict is the one artifact body every verifier emits — code, local,
	// or judgment. Decision and Tier are deliberately orthogonal axes:
	// decision says who may proceed, tier says who must approve.
	Verdict = contracts.Verdict
	// Producer identifies who stands behind a verdict. Class carries the
	// ladder semantics; Impl is provenance only — nothing may branch on it.
	Producer = contracts.Producer
	// Subject names what a verdict is about.
	Subject = contracts.Subject
	// Finding is one piece of a verdict's supporting evidence.
	Finding = contracts.Finding
)

// Producer classes, the only values the reducer accepts.
const (
	ClassCode     = contracts.ClassCode
	ClassLocal    = contracts.ClassLocal
	ClassJudgment = contracts.ClassJudgment
)

// ProducerString renders a producer as class or class/impl for humans.
// Presentation lives with gate, not on the contract type: how a producer
// reads in output is this tool's concern, and the shared vocabulary stays
// logic-free.
func ProducerString(p Producer) string {
	if p.Impl == "" {
		return p.Class
	}
	return p.Class + "/" + p.Impl
}

func isLocal(v Verdict) bool    { return v.Producer.Class == ClassLocal }
func isCode(v Verdict) bool     { return v.Producer.Class == ClassCode }
func isJudgment(v Verdict) bool { return v.Producer.Class == ClassJudgment }

// Decisions, worst to best: block > escalate > pass.
const (
	DecisionBlock    = "block"
	DecisionEscalate = "escalate"
	DecisionPass     = "pass"
)

// knownDecision reports whether d is a decision the ladder defines. Reduce
// refuses any other value — including the empty zero value a drifted or foreign
// artifact leaves — rather than letting it fall through to pass. Unknown
// decisions fail closed, like unknown producer classes and tiers.
func knownDecision(d string) bool {
	return d == DecisionBlock || d == DecisionEscalate || d == DecisionPass
}

// ErrLocalBlock fires when a local-model verifier tries to block — a ladder-law
// violation the reducer refuses structurally, not by convention.
var ErrLocalBlock = errors.New("ladder_violation_local_block")

// ErrUnknownProducer fires when a verdict names a producer class the ladder
// doesn't define. Ignoring such a verdict would silently drop its decision —
// a fail-open — so the reducer refuses instead.
var ErrUnknownProducer = errors.New("unknown_producer_class")

// ErrUnknownDecision fires when a verdict carries a decision the ladder does
// not define. The reducer already fails closed on an unknown producer class and
// an unknown tier; a decision it cannot reason about — including the zero value
// an empty or drifted artifact leaves — is no different. Absence of a real
// decision is absence of signal, and must never fall through to a pass.
var ErrUnknownDecision = errors.New("unknown_decision")

// noFloorWhy is the reason Reduce escalates when the set carries no code-class
// verdict. Absence of the deterministic floor must never read as green — the
// same fail-open shape as a never-triggered CI check passing.
const noFloorWhy = "no code-floor verdict present — cannot verify readiness"

// Reduce composes verdicts monotonically into one. Verdict order does not
// matter; only producer class and decision do.
//
// A code-class verdict must be present for the result to pass: absence of the
// deterministic floor escalates (never passes), and no judgment verdict can
// substitute for it. This invariant lives here, not in the caller's rung
// ordering, so a pass can never be composed from nothing observed.
func Reduce(subject Subject, verdicts []Verdict) (Verdict, error) {
	out := Verdict{
		Subject:    subject,
		Source:     "reducer",
		Producer:   Producer{Class: ClassCode},
		Decision:   DecisionPass,
		Tier:       "T0",
		Confidence: 1.0,
	}
	var judged *Verdict
	var escalations []string
	var hasCode bool
	for i, v := range verdicts {
		if !isCode(v) && !isLocal(v) && !isJudgment(v) {
			return Verdict{}, fmt.Errorf("%w: %q from %s", ErrUnknownProducer, v.Producer.Class, v.Source)
		}
		// A verdict whose decision the ladder cannot name is nothing observed,
		// not a pass — fail closed before any of it counts toward the outcome.
		if !knownDecision(v.Decision) {
			return Verdict{}, fmt.Errorf("%w: %q from %s", ErrUnknownDecision, v.Decision, v.Source)
		}
		if isLocal(v) && v.Decision == DecisionBlock {
			return Verdict{}, fmt.Errorf("%w: %s", ErrLocalBlock, v.Source)
		}
		// Enrichment rungs never satisfy the floor-presence invariant: a
		// verdict whose own contract says classification never gates cannot
		// stand in for the deterministic floor, even though its signature
		// path is code-class. Without this, a set containing only a
		// ci-classify pass would compose green from nothing readiness-shaped
		// observed.
		if isCode(v) && v.Source != sourceCIClassify {
			hasCode = true
		}
		if tierRank(v.Tier) > tierRank(out.Tier) {
			out.Tier = v.Tier
		}
		if v.Confidence < out.Confidence {
			out.Confidence = v.Confidence
		}
		if isCode(v) && v.Decision == DecisionBlock {
			out.Decision = DecisionBlock
			out.Why = join(out.Why, v.Source+": "+v.Why)
		}
		if v.Decision == DecisionEscalate {
			escalations = append(escalations, v.Source+": "+v.Why)
		}
		if isJudgment(v) {
			judged = &verdicts[i]
		}
	}
	// A block dominates, but it must not bury the escalation reasons gathered
	// alongside it: an infra escalation composing under a readiness block on
	// the same red check would otherwise vanish from the outcome. Visibility
	// only — the decision stays blocked, and no rung's Findings are lifted
	// into the composed verdict (they live on the rung's own artifact).
	if out.Decision == DecisionBlock {
		out.Why = joinAll(out.Why, escalations)
		return out, nil
	}
	// Floor-presence dominates the pass path — and the judgment carve-out
	// below. Absence of a code-class verdict must never read as green, and a
	// judgment pass must not be able to launder a missing floor. Resolved
	// before the judgment step so it cannot turn this green.
	if !hasCode {
		out.Decision = DecisionEscalate
		out.Why = join(out.Why, noFloorWhy)
		return out, nil
	}
	if judged != nil {
		out.Decision = judged.Decision
		out.Why = join(out.Why, "judgment: "+judged.Why)
		return out, nil
	}
	if len(escalations) > 0 {
		out.Decision = DecisionEscalate
		out.Why = joinAll(out.Why, escalations)
		return out, nil
	}
	out.Why = join(out.Why, "all verifiers pass")
	return out, nil
}

// Record appends a verdict artifact whose parents name the evidence it judged.
func Record(st *state.Store, run string, parents []string, v Verdict) (state.Artifact, error) {
	return st.Append(state.KindVerdict, run, parents, v)
}

// Load parses a verdict artifact body.
func Load(a state.Artifact) (Verdict, error) {
	var v Verdict
	if err := json.Unmarshal(a.Body, &v); err != nil {
		return Verdict{}, fmt.Errorf("verify: parse verdict %s: %w", a.ID, err)
	}
	return v, nil
}

func tierRank(t string) int { return tier.Rank(t) }

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

func joinAll(why string, reasons []string) string {
	for _, r := range reasons {
		why = join(why, r)
	}
	return why
}

// severityTier maps reviewer-stated severity words onto the shared tier scale.
func severityTier(sev string) int {
	switch strings.ToLower(sev) {
	case "critical", "p0", "blocking", "block":
		return 3
	case "high", "p1":
		return 2
	case "medium", "p2":
		return 1
	default:
		return 0
	}
}
