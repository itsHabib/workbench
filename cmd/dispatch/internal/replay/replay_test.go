package replay

import (
	"testing"

	"github.com/itsHabib/workbench/cmd/dispatch/internal/placement"
	"github.com/itsHabib/workbench/cmd/dispatch/internal/policy"
)

const testPolicyPath = "testdata/dispatch-policy.json"
const testHistoricalPath = "testdata/historical.json"

// mustReproducePolicyDriven names the two streams whose actual placement was
// POLICY-driven (spec §11's binding gate): the two dispatch phases must
// separate by derived task_class — decide-core is generative,
// replay-validation is analytical — and both must MATCH.
var mustReproducePolicyDriven = []string{"dispatch-decide-core", "dispatch-replay-validation"}

// TestReplayHistoricalPlacements is the phase-2 validation gate. For each of
// the 8 baked historical streams it derives the descriptor (already baked via
// the phase-1 rules in testdata/historical.json, documented per-field in
// "derivation"), decides it against the authored policy through the real
// decide engine, and classifies the outcome:
//
//   - the two dispatch phases (policy-driven) MUST reproduce the actual
//     placement byte-for-byte;
//   - the six experiment-driven streams (grok override / fair-trio pool) MAY
//     diverge, but only with a rule firing and a recorded defensible reason —
//     never a silent no-match;
//   - no rule may fire on exactly one of the 8 streams — that would be
//     per-stream special-casing, which spec §11 calls a no-go, not a rounding
//     error.
func TestReplayHistoricalPlacements(t *testing.T) {
	loaded, err := policy.Load(testPolicyPath)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	streams, err := LoadHistorical(testHistoricalPath)
	if err != nil {
		t.Fatalf("load historical fixture: %v", err)
	}
	if len(streams) != 8 {
		t.Fatalf("fixture has %d streams, want 8 (the frozen historical set)", len(streams))
	}

	ruleFireCount := map[string]int{}
	for _, h := range streams {
		res, err := Replay(loaded, h)
		if err != nil {
			t.Fatalf("%s: %v", h.Stream, err)
		}
		if !res.Matched {
			t.Fatalf("%s: no policy rule matched this historical descriptor — every real historical stream must be coverable by some rule (fixture task_class=%s risk_tier=%s weighted_loc=%d)",
				h.Stream, h.Descriptor.TaskClass, h.Descriptor.RiskTier, h.Descriptor.WeightedLOC)
		}
		ruleFireCount[res.Rule]++
		assertExpectation(t, h, res)
	}

	for rule, n := range ruleFireCount {
		if n < 2 {
			t.Errorf("rule %q fired on only %d historical stream — a rule that fires on exactly one stream is per-stream special-casing (spec §11: NO-GO, not a rounding error)", rule, n)
		}
	}

	byStream := make(map[string]HistoricalStream, len(streams))
	for _, h := range streams {
		byStream[h.Stream] = h
	}
	for _, slug := range mustReproducePolicyDriven {
		h, ok := byStream[slug]
		if !ok {
			t.Fatalf("fixture is missing the policy-driven stream %q", slug)
		}
		if !h.PolicyDriven || h.Expect != "match" {
			t.Fatalf("%s: must be fixture-marked policy_driven=true, expect=match (this is the binding phase-2 gate)", slug)
		}
	}
}

// assertExpectation checks one stream's replay result against its fixture
// expectation. A "match" stream is policy-driven and must reproduce the
// actual placement byte-for-byte; a "diverge" stream is experiment-driven and
// must NOT reproduce it (a policy that accidentally reproduced an
// experiment-driven pick would be proving nothing — it would mean the rule
// happened to special-case that one stream), and must carry a recorded reason.
func assertExpectation(t *testing.T, h HistoricalStream, res Result) {
	t.Helper()
	switch h.Expect {
	case "match":
		if !h.PolicyDriven {
			t.Errorf("%s: expect=match but fixture says policy_driven=false — inconsistent fixture", h.Stream)
		}
		if !res.Reproduced {
			t.Errorf("%s: expected MATCH (policy-driven) but rule %q emitted %+v, actual was %+v", h.Stream, res.Rule, res.Emitted, res.Actual)
		}
	case "diverge":
		if h.PolicyDriven {
			t.Errorf("%s: expect=diverge but fixture says policy_driven=true — inconsistent fixture", h.Stream)
		}
		if res.Reproduced {
			t.Errorf("%s: expected DIVERGE (experiment-driven: %s) but the policy reproduced the actual placement — either the fixture is stale or the rule accidentally special-cases this stream", h.Stream, h.HowChosen)
		}
		if h.HowChosen == "" {
			t.Errorf("%s: a divergence must record a defensible reason (how_chosen) — an unexplained divergence is not distinguishable from a policy gap", h.Stream)
		}
	default:
		t.Fatalf("%s: unknown expect %q (want match|diverge)", h.Stream, h.Expect)
	}
}

// TestReplayNegativeControl proves the gate can fail: a descriptor whose
// task_class/risk_tier combination no rule in the authored policy covers must
// exit unmatched. mechanical+T3 is deliberately absent from both rules —
// "generative-high-risk-to-opus-max" requires task_class=generative, and
// "contained-t1-to-sonnet-extra" requires risk_tier=T1 — and it is not one of
// the 8 historical descriptors, so this is a genuine negative control, not a
// restatement of a covered case.
func TestReplayNegativeControl(t *testing.T) {
	loaded, err := policy.Load(testPolicyPath)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	raw := []byte(`{"repo":"workbench","task_class":"mechanical","weighted_loc":50,"risk_tier":"T3"}`)
	d, err := placement.ParseDescriptor(raw)
	if err != nil {
		t.Fatalf("parse negative-control descriptor: %v", err)
	}
	if _, ok := placement.Decide(loaded, d); ok {
		t.Fatal("negative control: expected no rule to match mechanical+T3, but one did — the gate must be able to fail, and this policy is supposed to have no catch-all")
	}
}

// TestAuthoredPolicyHasNoCatchAll pins the deliberate design choice the
// negative control depends on: this policy has no catch-all rule, so an
// uncovered descriptor genuinely exits unmatched instead of silently landing
// on a default placement. dispatch validate is expected to pass with the
// "no catch-all" warning (exit 1), asserted end-to-end in
// cmd/dispatch/replay_validate_test.go.
func TestAuthoredPolicyHasNoCatchAll(t *testing.T) {
	loaded, err := policy.Load(testPolicyPath)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	if loaded.Policy.HasCatchAll() {
		t.Fatal("the authored replay-gate policy must have no catch-all rule — the negative control depends on an uncovered descriptor genuinely exiting unmatched")
	}
}
