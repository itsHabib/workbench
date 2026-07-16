package tracelens

import (
	"strings"
	"testing"
)

// --- shared test builders -------------------------------------------------

func okStep(tool string, args map[string]any, obs string, cost float64) Step {
	v := true
	return Step{Tool: tool, Args: args, Observation: obs, OK: &v, CostUSD: cost}
}

func failStep(tool, msg string, cost float64) Step {
	v := false
	return Step{Tool: tool, OK: &v, Error: msg, CostUSD: cost}
}

func thoughtStep(text string) Step { return Step{Thought: text, Role: "assistant"} }

// traj assigns sequential indices so evidence indices are predictable.
func traj(steps ...Step) Trajectory {
	for i := range steps {
		steps[i].Index = i
	}
	return Trajectory{Steps: steps}
}

func hasKind(fs []Finding, kind string) *Finding {
	for i := range fs {
		if fs[i].Kind == kind {
			return &fs[i]
		}
	}
	return nil
}

// --- loop ------------------------------------------------------------------

func TestLoopDetector_PeriodOneSpin(t *testing.T) {
	tr := traj(
		okStep("search", map[string]any{"q": "x"}, "nothing", 0.01),
		okStep("search", map[string]any{"q": "x"}, "nothing", 0.01),
		okStep("search", map[string]any{"q": "x"}, "nothing", 0.01),
		okStep("search", map[string]any{"q": "x"}, "nothing", 0.01),
	)
	fs := LoopDetector{MinRepeats: 3}.Detect(tr)
	f := hasKind(fs, "loop")
	if f == nil {
		t.Fatal("expected a loop finding")
	}
	if len(f.Steps) != 4 {
		t.Fatalf("loop should span all 4 steps, got %v", f.Steps)
	}
	if f.Severity != Critical {
		t.Fatalf("loop should be critical, got %v", f.Severity)
	}
}

func TestLoopDetector_PeriodTwoCycle(t *testing.T) {
	tr := traj(
		okStep("plan", nil, "a", 0),
		okStep("act", nil, "b", 0),
		okStep("plan", nil, "a", 0),
		okStep("act", nil, "b", 0),
		okStep("plan", nil, "a", 0),
		okStep("act", nil, "b", 0),
	)
	f := hasKind(LoopDetector{MinRepeats: 3}.Detect(tr), "loop")
	if f == nil {
		t.Fatal("expected loop finding for A B A B A B")
	}
	if !strings.Contains(f.Summary, "period 2") {
		t.Fatalf("expected period 2 in summary: %q", f.Summary)
	}
	if len(f.Steps) != 6 {
		t.Fatalf("cycle should cover 6 steps, got %v", f.Steps)
	}
}

func TestLoopDetector_NoFalsePositiveOnDistinctCalls(t *testing.T) {
	tr := traj(
		okStep("a", nil, "1", 0),
		okStep("b", nil, "2", 0),
		okStep("c", nil, "3", 0),
		okStep("d", nil, "4", 0),
	)
	if f := hasKind(LoopDetector{MinRepeats: 3}.Detect(tr), "loop"); f != nil {
		t.Fatalf("distinct calls must not be a loop: %q", f.Summary)
	}
}

func TestLoopDetector_NormalizesOnlyVolatileMetadata(t *testing.T) {
	tr := traj(
		okStep("status", map[string]any{"job": "build", "request_id": "r1", "timestamp": "t1"}, "pending", 0),
		okStep("status", map[string]any{"job": "build", "request_id": "r2", "timestamp": "t2"}, "pending", 0),
		okStep("status", map[string]any{"job": "build", "request_id": "r3", "timestamp": "t3"}, "pending", 0),
	)
	f := hasKind(LoopDetector{MinRepeats: 3}.Detect(tr), "loop")
	if f == nil || !strings.Contains(f.Summary, "semantic loop") {
		t.Fatalf("volatile metadata loop = %+v", f)
	}
	sig := (LoopDetector{}).signature(tr.Steps[0])
	if !strings.Contains(sig, "__volatile__") || strings.Contains(f.Summary, `\u003c`) {
		t.Fatalf("normalized sentinel must remain inspectable: signature=%q summary=%q", sig, f.Summary)
	}
	if exact := hasKind(LoopDetector{MinRepeats: 3, KeepVolatileArgs: true}.Detect(tr), "loop"); exact != nil {
		t.Fatalf("exact mode must remain available, got %+v", exact)
	}
}

func TestLoopDetector_PaginationRemainsProgress(t *testing.T) {
	tr := traj(
		okStep("list", map[string]any{"page": 1}, "a", 0),
		okStep("list", map[string]any{"page": 2}, "b", 0),
		okStep("list", map[string]any{"page": 3}, "c", 0),
	)
	if f := hasKind(LoopDetector{MinRepeats: 3}.Detect(tr), "loop"); f != nil {
		t.Fatalf("pagination must remain distinct: %+v", f)
	}
}

func TestLoopDetector_TimestampCursorWithChangingOutcomesIsProgress(t *testing.T) {
	tr := traj(
		okStep("logs", map[string]any{"job": "build", "ts": "10:00:00"}, "3 new events: pulling image", 0),
		okStep("logs", map[string]any{"job": "build", "ts": "10:00:30"}, "2 new events: starting container", 0),
		okStep("logs", map[string]any{"job": "build", "ts": "10:01:00"}, "1 new event: healthy", 0),
	)
	if f := hasKind(LoopDetector{MinRepeats: 3}.Detect(tr), "loop"); f != nil {
		t.Fatalf("changing confirmed outcomes are progress: %+v", f)
	}
}

func TestLoopDetector_SemanticMatchRequiresKnownOutcomes(t *testing.T) {
	tr := traj(
		Step{Tool: "status", Args: map[string]any{"request_id": "r1"}},
		Step{Tool: "status", Args: map[string]any{"request_id": "r2"}},
		Step{Tool: "status", Args: map[string]any{"request_id": "r3"}},
	)
	if f := hasKind(LoopDetector{MinRepeats: 3}.Detect(tr), "loop"); f != nil {
		t.Fatalf("unknown outcomes cannot prove a semantic loop: %+v", f)
	}
}

func TestLoopDetector_ExactLoopWithVolatileKeyKeepsExactLabel(t *testing.T) {
	tr := traj(
		okStep("status", map[string]any{"request_id": "same"}, "pending", 0),
		okStep("status", map[string]any{"request_id": "same"}, "pending", 0),
		okStep("status", map[string]any{"request_id": "same"}, "pending", 0),
	)
	f := hasKind(LoopDetector{MinRepeats: 3}.Detect(tr), "loop")
	if f == nil || strings.Contains(f.Summary, "semantic") {
		t.Fatalf("exact loop label = %+v", f)
	}
}

func TestBestTandem_PicksMostRepeated(t *testing.T) {
	// two AA pairs would be repeats=2; the CCCC run is repeats=4 and should win.
	seq := []string{"A", "A", "B", "C", "C", "C", "C"}
	sp, ok := bestTandem(seq, 3)
	if !ok {
		t.Fatal("expected a tandem")
	}
	if sp.period != 1 || sp.repeats != 4 || sp.start != 3 {
		t.Fatalf("want start3 period1 repeats4, got %+v", sp)
	}
}

// --- redundancy ------------------------------------------------------------

func TestRedundancy_SameCallSameResult(t *testing.T) {
	tr := traj(
		okStep("geocode", map[string]any{"city": "sf"}, "37.77,-122.4", 0.02),
		thoughtStep("hmm let me check again"),
		okStep("geocode", map[string]any{"city": "sf"}, "37.77,-122.4", 0.02),
		okStep("geocode", map[string]any{"city": "sf"}, "37.77,-122.4", 0.02),
	)
	f := hasKind(RedundancyDetector{}.Detect(tr), "redundancy")
	if f == nil {
		t.Fatal("expected redundancy finding")
	}
	if len(f.Steps) != 3 {
		t.Fatalf("all 3 identical calls are evidence, got %v", f.Steps)
	}
	if f.WastedUSD < 0.039 || f.WastedUSD > 0.041 {
		t.Fatalf("wasted should be ~0.04 (two repeats), got %v", f.WastedUSD)
	}
}

func TestRedundancy_DifferentResultNotFlagged(t *testing.T) {
	tr := traj(
		okStep("clock", nil, "10:00", 0.01),
		okStep("clock", nil, "10:01", 0.01),
		okStep("clock", nil, "10:02", 0.01),
	)
	if f := hasKind(RedundancyDetector{}.Detect(tr), "redundancy"); f != nil {
		t.Fatalf("same call w/ changing results is not redundant: %q", f.Summary)
	}
}

func TestRedundancy_UnknownOutcomesNotFlagged(t *testing.T) {
	tr := traj(
		Step{Tool: "fetch", Args: map[string]any{"url": "x"}},
		Step{Tool: "fetch", Args: map[string]any{"url": "x"}},
	)
	if f := hasKind(RedundancyDetector{}.Detect(tr), "redundancy"); f != nil {
		t.Fatalf("unknown outcomes are not confirmed identical results: %q", f.Summary)
	}
}

// --- retry storm -----------------------------------------------------------

func TestRetryStorm_ConsecutiveFailures(t *testing.T) {
	tr := traj(
		failStep("http", "503 unavailable", 0.01),
		failStep("http", "503 unavailable", 0.01),
		failStep("http", "503 unavailable", 0.01),
	)
	f := hasKind(RetryStormDetector{Threshold: 3}.Detect(tr), "retry_storm")
	if f == nil {
		t.Fatal("expected retry storm finding")
	}
	if f.Severity != Critical {
		t.Fatalf("retry storm should be critical, got %v", f.Severity)
	}
	if !strings.Contains(f.Summary, "503") {
		t.Fatalf("error text should appear in summary: %q", f.Summary)
	}
	if f.WastedUSD < 0.029 || f.WastedUSD > 0.031 {
		t.Fatalf("wasted should be ~0.03, got %v", f.WastedUSD)
	}
}

func TestRetryStorm_BrokenBySuccessDoesNotFire(t *testing.T) {
	tr := traj(
		failStep("http", "boom", 0.01),
		failStep("http", "boom", 0.01),
		okStep("http", nil, "200 ok", 0.01),
		failStep("http", "boom", 0.01),
	)
	if f := hasKind(RetryStormDetector{Threshold: 3}.Detect(tr), "retry_storm"); f != nil {
		t.Fatalf("a success breaks the run; should not fire: %q", f.Summary)
	}
}

// --- cost hotspot ----------------------------------------------------------

func TestCostHotspot_DominantTool(t *testing.T) {
	tr := traj(
		okStep("cheap", nil, "a", 0.01),
		okStep("expensive", nil, "b", 0.90),
		okStep("cheap", nil, "c", 0.01),
	)
	f := hasKind(CostHotspotDetector{Frac: 0.4}.Detect(tr), "cost_hotspot")
	if f == nil {
		t.Fatal("expected cost hotspot")
	}
	if !strings.Contains(f.Summary, "expensive") {
		t.Fatalf("dominant tool should be named: %q", f.Summary)
	}
	if f.Severity != Info {
		t.Fatalf("cost hotspot is info-level, got %v", f.Severity)
	}
}

func TestCostHotspot_NoneWhenSpread(t *testing.T) {
	tr := traj(
		okStep("a", nil, "1", 0.10),
		okStep("b", nil, "2", 0.10),
		okStep("c", nil, "3", 0.10),
	)
	if f := hasKind(CostHotspotDetector{Frac: 0.4}.Detect(tr), "cost_hotspot"); f != nil {
		t.Fatalf("evenly spread spend has no hotspot: %q", f.Summary)
	}
}

// --- stuck / progress model ------------------------------------------------

func TestStuck_TrailingRevisitsFire(t *testing.T) {
	// First three states are new (progress); then the agent tries different
	// queries that all return an already-seen empty result -> no new state.
	tr := traj(
		okStep("search", map[string]any{"q": "a"}, "", 0),
		okStep("search", map[string]any{"q": "b"}, "hit", 0),
		okStep("search", map[string]any{"q": "c"}, "miss", 0),
		okStep("search", map[string]any{"q": "d"}, "", 0),     // revisits "" (from q=a)
		okStep("search", map[string]any{"q": "e"}, "hit", 0),  // revisits "hit"
		okStep("search", map[string]any{"q": "f"}, "miss", 0), // revisits "miss"
		okStep("search", map[string]any{"q": "g"}, "", 0),     // revisits ""
	)
	f := hasKind(StuckDetector{Window: 4}.Detect(tr), "stuck")
	if f == nil {
		t.Fatal("expected stuck finding for trailing no-progress run")
	}
	if len(f.Steps) != 4 {
		t.Fatalf("trailing stall is the last 4 steps, got %v", f.Steps)
	}
}

func TestStuck_AllDistinctDoesNotFire(t *testing.T) {
	tr := traj(
		okStep("s", map[string]any{"q": "a"}, "r1", 0),
		okStep("s", map[string]any{"q": "b"}, "r2", 0),
		okStep("s", map[string]any{"q": "c"}, "r3", 0),
		okStep("s", map[string]any{"q": "d"}, "r4", 0),
		okStep("s", map[string]any{"q": "e"}, "r5", 0),
	)
	if f := hasKind(StuckDetector{Window: 4}.Detect(tr), "stuck"); f != nil {
		t.Fatalf("all-new states makes progress; should not fire: %q", f.Summary)
	}
}
