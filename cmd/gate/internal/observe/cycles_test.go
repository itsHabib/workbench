package observe

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// parkedCycleArts is a log with one consumed pass (run_1) and a content park
// (run_2) for o/r#7 under grant grt_a: two cycles used, run_2 awaiting
// judgment.
func parkedCycleArts(maxCycles int) []state.Artifact {
	return []state.Artifact{
		art(state.KindGrant, "run_mint", "grt_a", inboxBase, tieredGrant("o/r", "T1", maxCycles, inboxBase.Add(5*time.Hour))),
		subjectVerdict("run_1", "vrd_1", inboxBase, "o/r", 7, "t", "sha"),
		outcome(state.KindAction, "run_1", "act_1", "vrd_1", inboxBase.Add(time.Minute), wouldMerge(mergeCmd)),
		subjectVerdict("run_2", "vrd_2", inboxBase.Add(2*time.Minute), "o/r", 7, "t", "sha"),
		outcome(state.KindEscalation, "run_2", "esc_2", "vrd_2", inboxBase.Add(3*time.Minute), esc("grt_a", "why?", "", "o/r", 7)),
	}
}

// TestParkedRowCarriesCycleBudget pins `gate next`'s budget label: the parked
// row carries cycles_used/cycles_max in JSON and "cycles N/M" on its head line,
// counted against the grant the run parked under.
func TestParkedRowCarriesCycleBudget(t *testing.T) {
	in := buildInbox(parkedCycleArts(3), inboxBase.Add(time.Hour), "")
	if len(in.Parked) != 1 {
		t.Fatalf("want one parked run, got %+v", in.Parked)
	}
	p := in.Parked[0]
	if p.CyclesUsed != 2 || p.CyclesMax != 3 {
		t.Fatalf("budget = %d/%d, want 2/3", p.CyclesUsed, p.CyclesMax)
	}
	var buf bytes.Buffer
	renderInbox(&buf, in)
	if !strings.Contains(buf.String(), "  o/r#7  run_2  cycles 2/3\n") {
		t.Fatalf("head line must carry the budget:\n%s", buf.String())
	}
}

// TestParkedRowUnboundedGrantReadsInfinity: a 0 ceiling is unbounded, never
// "0 of 0".
func TestParkedRowUnboundedGrantReadsInfinity(t *testing.T) {
	in := buildInbox(parkedCycleArts(0), inboxBase.Add(time.Hour), "")
	var buf bytes.Buffer
	renderInbox(&buf, in)
	if !strings.Contains(buf.String(), "  o/r#7  run_2  cycles 2/∞\n") {
		t.Fatalf("unbounded ceiling must render as ∞:\n%s", buf.String())
	}
}

// TestParkedRowShowsOverrun pins the failure the label exists to surface: a
// ceiling park left over the budget reads "cycles 2/1" — more used than the
// grant allows — so the overrun is visible before anyone runs judge.
func TestParkedRowShowsOverrun(t *testing.T) {
	arts := parkedCycleArts(1)
	arts = append(arts,
		subjectVerdict("run_3", "vrd_3", inboxBase.Add(4*time.Minute), "o/r", 7, "t", "sha"),
		outcome(state.KindEscalation, "run_3", "esc_3", "vrd_3", inboxBase.Add(5*time.Minute),
			esc("grt_a", "grant_cycle_exceeded: cycle 3", "grant_cycle_exceeded", "o/r", 7)),
	)
	in := buildInbox(arts, inboxBase.Add(time.Hour), "")
	if len(in.Parked) != 1 || in.Parked[0].Run != "run_3" {
		t.Fatalf("want the ceiling park as the newest terminal, got %+v", in.Parked)
	}
	// The ceiling park itself consumes nothing — the two content outcomes do.
	if p := in.Parked[0]; p.CyclesUsed != 2 || p.CyclesMax != 1 {
		t.Fatalf("budget = %d/%d, want 2/1", p.CyclesUsed, p.CyclesMax)
	}
}

// TestNeedsGrantSkipsCycleRefusal: a pre-flight cycle refusal shares the
// grant_needed kind but is not a lapse, so it never prints a needs-grant row.
func TestNeedsGrantSkipsCycleRefusal(t *testing.T) {
	at := inboxBase.Add(-time.Hour)
	arts := []state.Artifact{
		art(state.KindGrantNeeded, "run_r", "gnd_1", at, map[string]any{
			"repo": "o/r", "number": 7, "reason": "grant_cycle_exceeded",
			"grant": "grt_a", "cycles_used": 3, "cycles_max": 3, "at": at.Format(time.RFC3339),
		}),
	}
	in := buildInbox(arts, inboxBase, "")
	if len(in.NeedsGrant) != 0 {
		t.Fatalf("a cycle refusal must not read as a lapsed grant, got %+v", in.NeedsGrant)
	}
}

// TestExplainShowsCycleBudgetOnAwaitingEscalation pins `gate explain`'s
// budget: the escalation still awaiting judgment carries "cycles N/M" on its
// line (and a Cycles projection in JSON); a run resolved by an action carries
// none, since nothing there awaits a judge.
func TestExplainShowsCycleBudgetOnAwaitingEscalation(t *testing.T) {
	st, err := state.Open(t.TempDir(), func() time.Time { return fixtureNow })
	if err != nil {
		t.Fatal(err)
	}
	g, err := st.Append(state.KindGrant, "run_mint", nil, tieredGrant("o/r", "T1", 3, fixtureNow.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	subject := map[string]any{"subject": map[string]any{"repo": "o/r", "number": 7, "head_sha": "sha"}, "tier": "T0"}
	va, err := st.Append(state.KindVerdict, "run_a", nil, subject)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(state.KindAction, "run_a", []string{va.ID}, wouldMerge(mergeCmd)); err != nil {
		t.Fatal(err)
	}
	vb, err := st.Append(state.KindVerdict, "run_b", nil, subject)
	if err != nil {
		t.Fatal(err)
	}
	escArt, err := st.Append(state.KindEscalation, "run_b", []string{vb.ID}, esc(g.ID, "why?", "", "o/r", 7))
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := Explain(&buf, st, "run_b"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "escalation  "+escArt.ID+"  cycles 2/3\n") {
		t.Fatalf("awaiting escalation must carry the budget:\n%s", buf.String())
	}
	proj, err := Project(st, "run_b")
	if err != nil {
		t.Fatal(err)
	}
	last := proj.Artifacts[len(proj.Artifacts)-1]
	if last.Cycles == nil || last.Cycles.Used != 2 || last.Cycles.Max != 3 || last.Cycles.Grant != g.ID {
		t.Fatalf("projection cycles = %+v, want 2/3 under %s", last.Cycles, g.ID)
	}

	resolved, err := Project(st, "run_a")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range resolved.Artifacts {
		if n.Cycles != nil {
			t.Fatalf("a resolved run carries no budget, got %+v on %s", n.Cycles, n.ID)
		}
	}
}

// TestJudgeableParkAtCeilingGetsNoWiderMint pins the recovery path the
// pre-flight refusal points at: a content park sitting exactly at its grant's
// cycle ceiling is still judgeable (a judgment spends no cycle), so `next`
// must print its judge command without a wider-cycles mint beside it. A
// ceiling park keeps its mint — judging it re-applies the ceiling.
func TestJudgeableParkAtCeilingGetsNoWiderMint(t *testing.T) {
	in := buildInbox(parkedCycleArts(2), inboxBase.Add(time.Hour), "")
	if len(in.Parked) != 1 {
		t.Fatalf("want one parked run, got %+v", in.Parked)
	}
	p := in.Parked[0]
	if p.CyclesUsed != 2 || p.CyclesMax != 2 {
		t.Fatalf("budget = %d/%d, want 2/2", p.CyclesUsed, p.CyclesMax)
	}
	if p.Coverage == nil || p.Coverage.State != "covered" || p.Coverage.SuggestedMint != "" {
		t.Fatalf("a judgeable park at its ceiling must not suggest a wider mint, got %+v", p.Coverage)
	}
	var buf bytes.Buffer
	renderInbox(&buf, in)
	if strings.Contains(buf.String(), "gate grant") {
		t.Fatalf("next must not print a mint beside a judgeable park:\n%s", buf.String())
	}

	arts := parkedCycleArts(1)
	arts = append(arts,
		subjectVerdict("run_3", "vrd_3", inboxBase.Add(4*time.Minute), "o/r", 7, "t", "sha"),
		outcome(state.KindEscalation, "run_3", "esc_3", "vrd_3", inboxBase.Add(5*time.Minute),
			esc("grt_a", "grant_cycle_exceeded: cycle 3", "grant_cycle_exceeded", "o/r", 7)),
	)
	in = buildInbox(arts, inboxBase.Add(time.Hour), "")
	if c := in.Parked[0].Coverage; c == nil || c.State != "ceiling" || c.SuggestedMint == "" {
		t.Fatalf("a ceiling park keeps its mint, got %+v", c)
	}
}
