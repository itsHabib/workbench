package verify

import (
	"errors"
	"sort"
	"testing"

	"github.com/itsHabib/workbench/cmd/gate/internal/tier"
	"pgregory.net/rapid"
)

// The ladder law is the gate's spine: a merge is authorized by composing
// verdicts monotonically, and a single fail-open in that composition merges an
// unready PR. The example tests pin the named cases (a code block is final,
// local cannot block, judgment resolves an escalation); these generators assert
// the ALGEBRAIC laws the composition claims — order-independence, max-tier,
// min-confidence, and a decision that can never fabricate a pass from nothing.

var propSources = []string{"readiness", "triage-floor", "review-consolidation", "custody"}

// genLadderVerdicts draws a set of well-formed verdicts the reducer accepts
// WITHOUT error: known classes and decisions, valid tiers, confidence in [0,1],
// no local-block (its own separately-tested rejection), and AT MOST ONE judgment
// (two disagreeing judgments are the one legitimately order-dependent case — the
// last wins — so the order-independence law is stated over a single decisive
// judgment, the real-world shape). None uses the ci-classify source, so every
// code verdict is a real floor.
func genLadderVerdicts(t *rapid.T) []Verdict {
	n := rapid.IntRange(0, 5).Draw(t, "n")
	out := make([]Verdict, 0, n+1)
	for i := 0; i < n; i++ {
		class := rapid.SampledFrom([]string{ClassCode, ClassLocal}).Draw(t, "class")
		decisions := []string{DecisionBlock, DecisionEscalate, DecisionPass}
		if class == ClassLocal {
			decisions = []string{DecisionEscalate, DecisionPass} // local may never block
		}
		out = append(out, Verdict{
			Source:     rapid.SampledFrom(propSources).Draw(t, "source"),
			Producer:   Producer{Class: class},
			Decision:   rapid.SampledFrom(decisions).Draw(t, "decision"),
			Tier:       rapid.SampledFrom([]string{"T0", "T1", "T2", "T3"}).Draw(t, "tier"),
			Confidence: rapid.Float64Range(0, 1).Draw(t, "conf"),
		})
	}
	if rapid.Bool().Draw(t, "hasJudgment") {
		out = append(out, Verdict{
			Source:     "operator",
			Producer:   Producer{Class: ClassJudgment},
			Decision:   rapid.SampledFrom([]string{DecisionBlock, DecisionEscalate, DecisionPass}).Draw(t, "jDecision"),
			Tier:       rapid.SampledFrom([]string{"T0", "T1", "T2", "T3"}).Draw(t, "jTier"),
			Confidence: rapid.Float64Range(0, 1).Draw(t, "jConf"),
		})
	}
	return out
}

// oracleDecision is an INDEPENDENT re-statement of the ladder law, written as a
// flat set of rules rather than the reducer's accumulate-then-resolve flow. The
// two agreeing over every generated set is the property.
func oracleDecision(vs []Verdict) string {
	var codeBlock, hasFloor, hasEscalation bool
	var judgment *Verdict
	for i := range vs {
		v := vs[i]
		switch v.Producer.Class {
		case ClassCode:
			if v.Source != "ci-classify" {
				hasFloor = true
			}
			if v.Decision == DecisionBlock {
				codeBlock = true
			}
		case ClassJudgment:
			judgment = &vs[i]
		}
		if v.Decision == DecisionEscalate {
			hasEscalation = true
		}
	}
	switch {
	case codeBlock:
		return DecisionBlock // a code block dominates and cannot be overridden
	case !hasFloor:
		return DecisionEscalate // absence of the floor never reads as green
	case judgment != nil:
		return judgment.Decision // judgment resolves, floor already present
	case hasEscalation:
		return DecisionEscalate
	default:
		return DecisionPass
	}
}

func maxTierRank(vs []Verdict) int {
	rank := tier.Rank("T0") // the reducer seeds the outcome at T0
	for _, v := range vs {
		if r := tier.Rank(v.Tier); r > rank {
			rank = r
		}
	}
	return rank
}

func minConfidence(vs []Verdict) float64 {
	lowest := 1.0 // the reducer seeds confidence at 1.0
	for _, v := range vs {
		if v.Confidence < lowest {
			lowest = v.Confidence
		}
	}
	return lowest
}

// TestPropReduceComposesMonotonically is the headline: over any accepted verdict
// set the composed decision matches the independent oracle, the tier is the max,
// and the confidence is the weakest link — the three monotonicity claims the
// reducer's doc comment makes, asserted generatively.
func TestPropReduceComposesMonotonically(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		vs := genLadderVerdicts(t)
		got, err := Reduce(subj, vs)
		if err != nil {
			t.Fatalf("well-formed verdicts must reduce without error: %v", err)
		}
		if want := oracleDecision(vs); got.Decision != want {
			t.Fatalf("decision %q disagrees with oracle %q for %+v", got.Decision, want, vs)
		}
		if got, want := tier.Rank(got.Tier), maxTierRank(vs); got != want {
			t.Fatalf("tier rank %d is not the max %d", got, want)
		}
		if got, want := got.Confidence, minConfidence(vs); got != want {
			t.Fatalf("confidence %v is not the weakest link %v", got, want)
		}
	})
}

// TestPropReduceOrderIndependent: verdict order does not matter — only producer
// class and decision do (the reducer's stated contract). Any permutation of an
// accepted set composes to the same decision, tier, and confidence.
func TestPropReduceOrderIndependent(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		vs := genLadderVerdicts(t)
		base, err := Reduce(subj, vs)
		if err != nil {
			t.Fatalf("reduce base: %v", err)
		}
		perm := permute(t, vs)
		got, err := Reduce(subj, perm)
		if err != nil {
			t.Fatalf("reduce permuted: %v", err)
		}
		if got.Decision != base.Decision || got.Tier != base.Tier || got.Confidence != base.Confidence {
			t.Fatalf("permutation changed the outcome:\n base=(%s,%s,%v)\n perm=(%s,%s,%v)\n input=%+v",
				base.Decision, base.Tier, base.Confidence, got.Decision, got.Tier, got.Confidence, vs)
		}
	})
}

// permute returns a rapid-chosen permutation of vs by pairing each element with
// a drawn sort key and stable-sorting on it.
func permute(t *rapid.T, vs []Verdict) []Verdict {
	keys := rapid.SliceOfN(rapid.Int(), len(vs), len(vs)).Draw(t, "permKeys")
	idx := make([]int, len(vs))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return keys[idx[a]] < keys[idx[b]] })
	out := make([]Verdict, len(vs))
	for i, j := range idx {
		out[i] = vs[j]
	}
	return out
}

// TestPropLocalBlockAlwaysRejected: a local-model verdict that tries to block is
// a ladder violation the reducer refuses structurally, wherever it sits in the
// set — small models confabulate, so escalation is the only safe failure.
func TestPropLocalBlockAlwaysRejected(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		vs := genLadderVerdicts(t)
		at := rapid.IntRange(0, len(vs)).Draw(t, "insertAt")
		bad := Verdict{Source: "review-consolidation", Producer: Producer{Class: ClassLocal}, Decision: DecisionBlock, Tier: "T0", Confidence: 1}
		vs = append(vs[:at:at], append([]Verdict{bad}, vs[at:]...)...)
		if _, err := Reduce(subj, vs); !errors.Is(err, ErrLocalBlock) {
			t.Fatalf("a local block anywhere must reject with ErrLocalBlock, got %v", err)
		}
	})
}

// TestPropUnknownFailsClosed: an unknown producer class or an unknown decision
// (including the zero value a drifted artifact leaves) fails the whole reduction
// closed — never silently dropped, never fallen through to a pass.
func TestPropUnknownFailsClosed(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		vs := genLadderVerdicts(t)
		at := rapid.IntRange(0, len(vs)).Draw(t, "insertAt")
		var bad Verdict
		var wantErr error
		if rapid.Bool().Draw(t, "badClass") {
			bad = Verdict{Source: "mystery", Producer: Producer{Class: rapid.SampledFrom([]string{"", "reviewer", "bot"}).Draw(t, "class")}, Decision: DecisionPass, Tier: "T0", Confidence: 1}
			wantErr = ErrUnknownProducer
		} else {
			bad = Verdict{Source: "mystery", Producer: Producer{Class: ClassCode}, Decision: rapid.SampledFrom([]string{"", "approve", "reject"}).Draw(t, "decision"), Tier: "T0", Confidence: 1}
			wantErr = ErrUnknownDecision
		}
		vs = append(vs[:at:at], append([]Verdict{bad}, vs[at:]...)...)
		if _, err := Reduce(subj, vs); !errors.Is(err, wantErr) {
			t.Fatalf("unknown input must fail closed with %v, got %v", wantErr, err)
		}
	})
}
