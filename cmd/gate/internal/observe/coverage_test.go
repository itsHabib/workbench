package observe

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// tieredGrant is grant() with the ceilings under test made explicit.
func tieredGrant(repo, maxTier string, maxCycles int, expires time.Time) capability.Grant {
	g := grant(repo, expires)
	g.MaxTier, g.MaxCycles = maxTier, maxCycles
	return g
}

// tieredVerdict is subjectVerdict carrying a composed tier, which is what the
// coverage row compares against the grant's tier ceiling.
func tieredVerdict(run, id string, at time.Time, repo string, number int, t string) state.Artifact {
	return art(state.KindVerdict, run, id, at, map[string]any{
		"subject": map[string]any{"repo": repo, "number": number, "head_sha": "sha"},
		"tier":    t,
	})
}

// outcome is art with the parent verdict wired up — the join every cycle count
// follows (outcome → parent verdict → subject), so a fixture without it counts
// as zero cycles rather than one.
func outcome(kind, run, id, parent string, at time.Time, body any) state.Artifact {
	a := art(kind, run, id, at, body)
	a.Parents = []string{parent}
	return a
}

// mergeCmd is a stand-in for the pinned command gate emits; the coverage
// projection never parses it.
const mergeCmd = "gh-pr-merge-placeholder"

// readyArts is the minimal log that yields one ready row for repo#number.
func readyArts(repo string, number int) []state.Artifact {
	return []state.Artifact{
		subjectVerdict("run_a", "vrd_a", inboxBase, repo, number, "t", "sha"),
		outcome(state.KindAction, "run_a", "act_a", "vrd_a", inboxBase.Add(time.Minute), wouldMerge(mergeCmd)),
	}
}

// TestCoverageAbsentGrant is the friction this surface exists for: `gate next`
// recommended a PR whose repo had no merge grant at all, and the gap only
// surfaced at gate time. The ready row must name it and carry the mint.
func TestCoverageAbsentGrant(t *testing.T) {
	in := buildInbox(readyArts("o/cc-skills", 24), inboxBase.Add(time.Hour), NextRequest{StateArg: ""})
	if len(in.ReadyToMerge) != 1 {
		t.Fatalf("want one ready row, got %+v", in.ReadyToMerge)
	}
	c := in.ReadyToMerge[0].Coverage
	if c == nil || c.State != "absent" {
		t.Fatalf("coverage = %+v, want state absent", c)
	}
	if !strings.Contains(c.Why, "o/cc-skills") {
		t.Fatalf("why = %q, want it to name the repo", c.Why)
	}
	for _, want := range []string{"gate grant", "-repo o/cc-skills", "-action merge", "-max-tier", "-max-cycles", "-ttl 24h"} {
		if !strings.Contains(c.SuggestedMint, want) {
			t.Fatalf("suggested_mint %q missing %q", c.SuggestedMint, want)
		}
	}
}

// TestCoverageCoveredIsQuiet pins the "nothing to say" contract: a live grant
// whose ceilings admit the next cycle reports covered and prints nothing, so the
// inbox stays a queue of things to do rather than a status board.
func TestCoverageCoveredIsQuiet(t *testing.T) {
	arts := append(readyArts("o/r", 7),
		art(state.KindGrant, "run_g", "grt_a", inboxBase, tieredGrant("o/r", "T2", 3, inboxBase.Add(24*time.Hour))))
	in := buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: ""})
	c := in.ReadyToMerge[0].Coverage
	if c == nil || c.State != "covered" {
		t.Fatalf("coverage = %+v, want state covered", c)
	}
	if c.Grant != "grt_a" || c.MaxTier != "T2" || c.MaxCycles != 3 {
		t.Fatalf("covered row must name the grant it read: %+v", c)
	}
	if c.Why != "" || c.SuggestedMint != "" {
		t.Fatalf("covered row must be quiet, got why=%q mint=%q", c.Why, c.SuggestedMint)
	}
	var buf bytes.Buffer
	renderCoverage(&buf, c)
	if buf.Len() != 0 {
		t.Fatalf("covered row rendered %q, want nothing", buf.String())
	}
}

// TestCoverageTierCeiling pins the tier half of the ceiling check: a live grant
// narrower than the run's own composed verdict tier would ceiling-park, and the
// suggested mint must be wide enough to admit that tier — never narrower than
// the evidence already showed.
func TestCoverageTierCeiling(t *testing.T) {
	arts := []state.Artifact{
		tieredVerdict("run_a", "vrd_a", inboxBase, "o/r", 7, "T3"),
		outcome(state.KindAction, "run_a", "act_a", "vrd_a", inboxBase.Add(time.Minute), wouldMerge(mergeCmd)),
		art(state.KindGrant, "run_g", "grt_a", inboxBase, tieredGrant("o/r", "T1", 3, inboxBase.Add(24*time.Hour))),
	}
	in := buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: ""})
	c := in.ReadyToMerge[0].Coverage
	if c == nil || c.State != "ceiling" {
		t.Fatalf("coverage = %+v, want state ceiling", c)
	}
	if c.VerdictTier != "T3" || !strings.Contains(c.Why, "T3") || !strings.Contains(c.Why, "T1") {
		t.Fatalf("why must name both tiers: %+v", c)
	}
	if !strings.Contains(c.SuggestedMint, "-max-tier T3") {
		t.Fatalf("mint must widen to the observed tier: %q", c.SuggestedMint)
	}
}

// TestCoverageCycleCeiling pins the cycle half: two consumed cycles against a
// -max-cycles 2 grant means the NEXT run is cycle 3 and would ceiling-park. The
// count mirrors gate's own rule — one counting outcome per distinct run.
func TestCoverageCycleCeiling(t *testing.T) {
	arts := []state.Artifact{
		subjectVerdict("run_1", "vrd_1", inboxBase, "o/r", 7, "t", "sha"),
		outcome(state.KindEscalation, "run_1", "esc_1", "vrd_1", inboxBase.Add(time.Minute),
			map[string]any{"outcome": "parked_for_judgment", "code": ""}),
		subjectVerdict("run_2", "vrd_2", inboxBase.Add(time.Hour), "o/r", 7, "t", "sha"),
		outcome(state.KindAction, "run_2", "act_2", "vrd_2", inboxBase.Add(time.Hour+time.Minute), wouldMerge(mergeCmd)),
		art(state.KindGrant, "run_g", "grt_a", inboxBase, tieredGrant("o/r", "T1", 2, inboxBase.Add(24*time.Hour))),
	}
	in := buildInbox(arts, inboxBase.Add(2*time.Hour), NextRequest{StateArg: ""})
	c := in.ReadyToMerge[0].Coverage
	if c == nil || c.State != "ceiling" {
		t.Fatalf("coverage = %+v, want state ceiling", c)
	}
	if c.CyclesUsed != 2 || c.NextCycle != 3 {
		t.Fatalf("cycles_used/next_cycle = %d/%d, want 2/3", c.CyclesUsed, c.NextCycle)
	}
	if !strings.Contains(c.SuggestedMint, "-max-cycles 3") {
		t.Fatalf("mint must admit the next cycle: %q", c.SuggestedMint)
	}
}

// TestCoverageCycleCountSkipsCeilingParks mirrors gate's countsAsCycle: an
// escalation carrying an authorization code is policy exhaustion, not consumed
// review work, so it must not burn the cycle a wider grant was minted to free.
func TestCoverageCycleCountSkipsCeilingParks(t *testing.T) {
	arts := []state.Artifact{
		subjectVerdict("run_1", "vrd_1", inboxBase, "o/r", 7, "t", "sha"),
		outcome(state.KindEscalation, "run_1", "esc_1", "vrd_1", inboxBase.Add(time.Minute),
			map[string]any{"outcome": "parked_for_judgment", "code": "grant_cycles_exceeded"}),
		subjectVerdict("run_2", "vrd_2", inboxBase.Add(time.Hour), "o/r", 7, "t", "sha"),
		outcome(state.KindAction, "run_2", "act_2", "vrd_2", inboxBase.Add(time.Hour+time.Minute),
			map[string]any{"outcome": "capability_refused"}),
		subjectVerdict("run_3", "vrd_3", inboxBase.Add(2*time.Hour), "o/r", 7, "t", "sha"),
		outcome(state.KindAction, "run_3", "act_3", "vrd_3", inboxBase.Add(2*time.Hour+time.Minute), wouldMerge(mergeCmd)),
	}
	in := buildInbox(arts, inboxBase.Add(3*time.Hour), NextRequest{StateArg: ""})
	c := in.ReadyToMerge[0].Coverage
	if c == nil || c.CyclesUsed != 1 {
		t.Fatalf("cycles_used = %+v, want only the would_merge run counted", c)
	}
}

// TestCoverageExpiredNamesTheLapse distinguishes a re-mint from a first mint: a
// repo whose grants lapsed says so, and carries the lapse instant.
func TestCoverageExpiredNamesTheLapse(t *testing.T) {
	expiry := inboxBase.Add(-time.Hour)
	arts := append(readyArts("o/r", 7),
		art(state.KindGrant, "run_g", "grt_a", inboxBase.Add(-2*time.Hour), tieredGrant("o/r", "T1", 3, expiry)))
	in := buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: ""})
	c := in.ReadyToMerge[0].Coverage
	if c == nil || c.State != "expired" {
		t.Fatalf("coverage = %+v, want state expired", c)
	}
	if c.LastExpiredAt != expiry.UTC().Format(time.RFC3339) {
		t.Fatalf("last_expired_at = %q, want %q", c.LastExpiredAt, expiry.UTC().Format(time.RFC3339))
	}
	if c.Grant != "" {
		t.Fatalf("an expired grant must not be reported as the row's live grant: %+v", c)
	}
}

// TestCoverageWidestLiveGrantWins pins the grant selection: the widest live
// grant is the one an operator would actually spend, so it is the one the row
// reports on — a narrow grant alongside a wide one must not manufacture a
// ceiling that does not exist.
func TestCoverageWidestLiveGrantWins(t *testing.T) {
	arts := []state.Artifact{
		tieredVerdict("run_a", "vrd_a", inboxBase, "o/r", 7, "T2"),
		outcome(state.KindAction, "run_a", "act_a", "vrd_a", inboxBase.Add(time.Minute), wouldMerge(mergeCmd)),
		art(state.KindGrant, "run_g", "grt_narrow", inboxBase, tieredGrant("o/r", "T0", 1, inboxBase.Add(24*time.Hour))),
		art(state.KindGrant, "run_g", "grt_wide", inboxBase, tieredGrant("o/r", "T3", 0, inboxBase.Add(12*time.Hour))),
	}
	in := buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: ""})
	c := in.ReadyToMerge[0].Coverage
	if c == nil || c.State != "covered" || c.Grant != "grt_wide" {
		t.Fatalf("coverage = %+v, want covered by grt_wide", c)
	}
}

// TestCoverageUnboundedCyclesNeverCeiling pins the 0 = unbounded contract on the
// cycle ceiling, which a naive `next > max` comparison gets exactly backwards.
func TestCoverageUnboundedCyclesNeverCeiling(t *testing.T) {
	arts := append(readyArts("o/r", 7),
		art(state.KindGrant, "run_g", "grt_a", inboxBase, tieredGrant("o/r", "T3", 0, inboxBase.Add(24*time.Hour))))
	in := buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: ""})
	if c := in.ReadyToMerge[0].Coverage; c == nil || c.State != "covered" {
		t.Fatalf("coverage = %+v, want covered under an unbounded cycle ceiling", c)
	}
}

// TestCoverageIgnoresOtherReposAndActions pins the scoping: a live grant for a
// different repo, and a non-merge grant for this one, both leave the row
// uncovered. A grant that does not authorize this merge is not coverage.
func TestCoverageIgnoresOtherReposAndActions(t *testing.T) {
	other := grant("o/other", inboxBase.Add(24*time.Hour))
	nonMerge := grant("o/r", inboxBase.Add(24*time.Hour))
	nonMerge.Action = "deploy"
	arts := append(readyArts("o/r", 7),
		art(state.KindGrant, "run_g", "grt_other", inboxBase, other),
		art(state.KindGrant, "run_g", "grt_deploy", inboxBase, nonMerge))
	in := buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: ""})
	if c := in.ReadyToMerge[0].Coverage; c == nil || c.State != "absent" {
		t.Fatalf("coverage = %+v, want absent", c)
	}
}

// TestCoverageOnParkedRuns pins the second surface: judging a park clean is
// wasted work if no live grant covers the repo the merge needs.
func TestCoverageOnParkedRuns(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindEscalation, "run_p", "esc_p", inboxBase, esc("grt_x", "why?", "", "o/r", 7)),
	}
	in := buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: ""})
	if len(in.Parked) != 1 {
		t.Fatalf("want one parked run, got %+v", in.Parked)
	}
	if c := in.Parked[0].Coverage; c == nil || c.State != "absent" {
		t.Fatalf("parked coverage = %+v, want absent", c)
	}
}

// TestCoverageMintSplicesStateArg keeps a copied mint pointed at the same log
// the inbox read, matching every other suggested command.
func TestCoverageMintSplicesStateArg(t *testing.T) {
	in := buildInbox(readyArts("o/r", 7), inboxBase.Add(time.Hour), NextRequest{StateArg: " -state /custom"})
	if mint := in.ReadyToMerge[0].Coverage.SuggestedMint; !strings.Contains(mint, "-state /custom") {
		t.Fatalf("suggested_mint %q missing -state", mint)
	}
	in2 := buildInbox(readyArts("o/r", 7), inboxBase.Add(time.Hour), NextRequest{StateArg: ""})
	if mint := in2.ReadyToMerge[0].Coverage.SuggestedMint; strings.Contains(mint, "-state") {
		t.Fatalf("ambient state dir must omit -state, got %q", mint)
	}
}

// TestCoverageExpiryBoundaryMatchesCheck pins the projection to capability.Check
// exactly: expired is strictly AFTER the expiry instant, so a grant sitting at
// its expiry is still live on both paths.
func TestCoverageExpiryBoundaryMatchesCheck(t *testing.T) {
	at := inboxBase.Add(time.Hour)
	arts := append(readyArts("o/r", 7),
		art(state.KindGrant, "run_g", "grt_a", inboxBase, tieredGrant("o/r", "T3", 0, at)))
	if c := buildInbox(arts, at, NextRequest{StateArg: ""}).ReadyToMerge[0].Coverage; c == nil || c.State != "covered" {
		t.Fatalf("at expiry: coverage = %+v, want covered", c)
	}
	if c := buildInbox(arts, at.Add(time.Nanosecond), NextRequest{StateArg: ""}).ReadyToMerge[0].Coverage; c == nil || c.State != "expired" {
		t.Fatalf("past expiry: coverage = %+v, want expired", c)
	}
}

// TestCoverageRendersUnderTheRow pins the text surface: the gap and its mint
// print under the PR they belong to, so the operator never has to join them by
// hand.
func TestCoverageRendersUnderTheRow(t *testing.T) {
	var buf bytes.Buffer
	renderReadyToMerge(&buf, buildInbox(readyArts("o/cc-skills", 24), inboxBase.Add(time.Hour), NextRequest{StateArg: ""}).ReadyToMerge)
	out := buf.String()
	for _, want := range []string{"grant absent", "o/cc-skills", "gate grant"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered ready section %q missing %q", out, want)
		}
	}
}
