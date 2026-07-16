package tracelens

import (
	"strings"
	"testing"

	"github.com/itsHabib/workbench/contracts"
)

func TestAnalyze_HealthyRunHasNoFindings(t *testing.T) {
	tr := traj(
		thoughtStep("plan the task"),
		okStep("read", map[string]any{"path": "a"}, "contents a", 0.01),
		okStep("write", map[string]any{"path": "b"}, "ok", 0.01),
		okStep("verify", nil, "passed", 0.01),
	)
	r := Analyze(tr, DefaultConfig())
	if r.Decision != contracts.DecisionPass {
		t.Fatalf("expected pass, got %q with findings %+v", r.Decision, r.Findings)
	}
	if len(r.Findings) != 0 {
		t.Fatalf("clean run should have no findings, got %d", len(r.Findings))
	}
	if !strings.Contains(r.Headline, "clean run") {
		t.Fatalf("headline should read clean: %q", r.Headline)
	}
}

// TestAnalyze_LoopMakesRunPathological is the guard test: if the detector core
// were ripped out or stubbed to return no findings, Analyze would report a
// healthy run and this test would fail. A green suite therefore certifies the
// agentic core actually runs.
func TestAnalyze_LoopMakesRunPathological(t *testing.T) {
	steps := []Step{}
	for i := 0; i < 5; i++ {
		steps = append(steps, okStep("search", map[string]any{"q": "same"}, "no result", 0.02))
	}
	r := Analyze(traj(steps...), DefaultConfig())
	if r.Decision != contracts.DecisionBlock {
		t.Fatalf("a 5x loop must block, got %q", r.Decision)
	}
	if !strings.HasPrefix(r.Headline, "loop:") {
		t.Fatalf("headline should lead with the loop, got %q", r.Headline)
	}
	if hasKind(r.Findings, "loop") == nil {
		t.Fatal("report must contain a loop finding")
	}
}

func TestAnalyze_RanksCriticalFirst(t *testing.T) {
	// A cost hotspot (info) plus a retry storm (critical); critical must lead.
	tr := traj(
		okStep("bigmodel", nil, "answer", 1.00),
		failStep("db", "deadlock", 0.01),
		failStep("db", "deadlock", 0.01),
		failStep("db", "deadlock", 0.01),
	)
	r := Analyze(tr, DefaultConfig())
	if len(r.Findings) < 2 {
		t.Fatalf("expected at least a hotspot and a retry storm, got %+v", r.Findings)
	}
	if r.Findings[0].Severity != Critical {
		t.Fatalf("critical finding must sort first, got %v", r.Findings[0].Severity)
	}
	if r.Decision != contracts.DecisionBlock {
		t.Fatalf("expected block, got %q", r.Decision)
	}
}

func TestAnalyze_WastedCostIsDisjointSum(t *testing.T) {
	// One redundant successful pair ($0.02 wasted) and one 3x retry storm
	// ($0.03 wasted). They must not double-count: total wasted == $0.05.
	tr := traj(
		okStep("geo", map[string]any{"c": "sf"}, "loc", 0.02),
		okStep("geo", map[string]any{"c": "sf"}, "loc", 0.02),
		failStep("api", "429", 0.01),
		failStep("api", "429", 0.01),
		failStep("api", "429", 0.01),
	)
	r := Analyze(tr, DefaultConfig())
	if r.WastedUSD < 0.049 || r.WastedUSD > 0.051 {
		t.Fatalf("wasted should sum to ~0.05, got %v", r.WastedUSD)
	}
}

func TestAnalyze_EscalateOnWarnOnly(t *testing.T) {
	// A redundancy (warn) with no critical -> escalate, not block.
	tr := traj(
		okStep("lookup", map[string]any{"id": 7}, "row", 0.01),
		okStep("lookup", map[string]any{"id": 7}, "row", 0.01),
	)
	r := Analyze(tr, DefaultConfig())
	if r.Decision != contracts.DecisionEscalate {
		t.Fatalf("warn-only run should escalate, got %q", r.Decision)
	}
}

func TestSeverityString(t *testing.T) {
	cases := map[Severity]string{Info: "INFO", Warn: "WARN", Critical: "CRITICAL"}
	for sev, want := range cases {
		if sev.String() != want {
			t.Fatalf("severity %d: want %q, got %q", sev, want, sev.String())
		}
	}
}

// TestBuildVerdictDecisionTierOrthogonal pins the max-severity → (decision,tier)
// mapping across all four bands. Decision and Tier are independent fields; this
// asserts both, not one derived from the other at read time.
func TestBuildVerdictDecisionTierOrthogonal(t *testing.T) {
	steps := func(n int) []int {
		xs := make([]int, n)
		for i := range xs {
			xs[i] = i
		}
		return xs
	}
	cases := []struct {
		name         string
		findings     []Finding
		wantDecision string
		wantTier     string
	}{
		{"critical", []Finding{{Kind: "loop", Severity: Critical, Summary: "loop", Steps: steps(3)}}, contracts.DecisionBlock, TierT3},
		{"warn", []Finding{{Kind: "redundancy", Severity: Warn, Summary: "redundant", Steps: steps(2)}}, contracts.DecisionEscalate, TierT2},
		{"info", []Finding{{Kind: "cost_hotspot", Severity: Info, Summary: "hotspot", Steps: steps(1)}}, contracts.DecisionPass, TierT1},
		{"empty", nil, contracts.DecisionPass, TierT0},
	}
	for _, c := range cases {
		v := buildVerdict(traj(), c.findings)
		if v.Decision != c.wantDecision {
			t.Fatalf("%s: decision want %q, got %q", c.name, c.wantDecision, v.Decision)
		}
		if v.Tier != c.wantTier {
			t.Fatalf("%s: tier want %q, got %q", c.name, c.wantTier, v.Tier)
		}
	}
}

// TestVerdictCarriesProducer checks every produced verdict is stamped with the
// deterministic code producer — the run-level provenance the gate contract wants.
func TestVerdictCarriesProducer(t *testing.T) {
	verdicts := []contracts.Verdict{
		buildVerdict(traj(), nil),
		buildVerdict(traj(), []Finding{{Kind: "redundancy", Severity: Warn, Summary: "r", Steps: []int{0, 1}}}),
		buildVerdict(traj(), []Finding{{Kind: "loop", Severity: Critical, Summary: "l", Steps: []int{0, 1, 2}}}),
	}
	for i, v := range verdicts {
		if v.Producer.Class != contracts.ClassCode {
			t.Fatalf("verdict %d: producer class want %q, got %q", i, contracts.ClassCode, v.Producer.Class)
		}
		if v.Producer.Impl == "" {
			t.Fatalf("verdict %d: producer impl must record the implementation, got empty", i)
		}
	}
}

// TestEscalateCarriesQuestion asserts an escalate verdict's Why holds the full
// aggregated reasoning — the headline plus each finding summary — not a bare
// flag. A downstream reader must be able to act on Why alone.
func TestEscalateCarriesQuestion(t *testing.T) {
	tr := traj(
		okStep("lookup", map[string]any{"id": 7}, "row", 0.01),
		okStep("lookup", map[string]any{"id": 7}, "row", 0.01),
		okStep("geo", map[string]any{"c": "sf"}, "loc", 0.02),
		okStep("geo", map[string]any{"c": "sf"}, "loc", 0.02),
	)
	r := Analyze(tr, DefaultConfig())
	if r.Decision != contracts.DecisionEscalate {
		t.Fatalf("two redundant pairs should escalate, got %q", r.Decision)
	}
	v := r.Verdict()
	if len(r.Findings) < 2 {
		t.Fatalf("expected at least two findings to aggregate, got %d", len(r.Findings))
	}
	if !strings.Contains(v.Why, r.Headline) {
		t.Fatalf("Why must contain the headline %q: %q", r.Headline, v.Why)
	}
	for _, f := range r.Findings {
		if !strings.Contains(v.Why, f.Summary) {
			t.Fatalf("Why must contain each finding summary %q: %q", f.Summary, v.Why)
		}
	}
	if len(v.Why) <= len(r.Headline) {
		t.Fatalf("Why should aggregate beyond a single line, got %q", v.Why)
	}
}

// TestTierRankFailsClosed pins the ordering and the fail-closed default: an
// unknown/garbage tier must rank strictly above every known tier.
func TestTierRankFailsClosed(t *testing.T) {
	if !(tierRank(TierT0) < tierRank(TierT1) &&
		tierRank(TierT1) < tierRank(TierT2) &&
		tierRank(TierT2) < tierRank(TierT3)) {
		t.Fatalf("tiers must rank T0<T1<T2<T3, got %d,%d,%d,%d",
			tierRank(TierT0), tierRank(TierT1), tierRank(TierT2), tierRank(TierT3))
	}
	for _, garbage := range []string{"", "T4", "unknown", "t3", "T-1"} {
		if tierRank(garbage) <= tierRank(TierT3) {
			t.Fatalf("unknown tier %q must rank highest (fail closed), got %d not > %d",
				garbage, tierRank(garbage), tierRank(TierT3))
		}
	}
}
