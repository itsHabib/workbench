package source

import (
	"strings"
	"testing"
	"time"
)

// The fixtures below are the real shape of gate's log: a grant artifact holding
// the ceilings, a reduced verdict holding the tier, and a content park naming
// both by id. Hash values are placeholders — Read only chains them, and these
// tests start from an empty cursor.
const (
	grantT1 = `{"id":"grt_ceiling","kind":"grant","run":"run_mint","time":"2026-08-21T20:00:00Z","body":{"repo":"itsHabib/workbench","action":"merge","max_tier":"T1","max_cycles":3,"expires_at":"2126-08-22T13:30:49Z","minted_by":"operator"},"prev":"h0","hash":"g1"}`
	grantT3 = `{"id":"grt_wide","kind":"grant","run":"run_mint","time":"2026-08-21T20:00:00Z","body":{"repo":"itsHabib/workbench","action":"merge","max_tier":"T3","max_cycles":3,"expires_at":"2126-08-22T13:30:49Z","minted_by":"operator"},"prev":"g1","hash":"g2"}`
	vrdT3   = `{"id":"vrd_reduced","kind":"verdict","run":"run_242","time":"2026-08-21T21:00:00Z","body":{"subject":{"repo":"itsHabib/workbench","number":242},"source":"reducer","decision":"escalate","tier":"T3","why":"unclassified paths"},"prev":"g2","hash":"v1"}`
	// The content park: no code, so notify's ceilingPark check lets it through.
	// This is the exact artifact whose Approve tap was burned in production.
	parkContent = `{"id":"esc_content","kind":"escalation","run":"run_242","time":"2026-08-21T21:00:01Z","body":{"schema_version":"escalation.v1","outcome":"parked_for_judgment","verdict":"vrd_reduced","grant":"grt_ceiling","question":"your call","repo":"itsHabib/workbench","number":242},"prev":"v1","hash":"e1"}`
)

func parkEvent(t *testing.T, lines ...string) map[string]string {
	t.Helper()
	src := gateFile(t, strings.Join(lines, "\n")+"\n")
	events, _, err := Read(src, Cursor{})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, ev := range events {
		if ev.Kind == "escalation" {
			return ev.Fields
		}
	}
	t.Fatalf("no escalation event in %d events", len(events))
	return nil
}

// TestPreflight_TierCeilingWithheldOnContentPark is the regression this whole
// change exists for. workbench#242 parked for CONTENT judgment (no park code,
// so nothing downstream withheld the buttons) while its reduced verdict was T3
// and its grant's ceiling was T1. The tap recorded a one-shot judgment and then
// died on grant_tier_exceeded. Both facts sat in the log flare was tailing.
func TestPreflight_TierCeilingWithheldOnContentPark(t *testing.T) {
	f := parkEvent(t, grantT1, grantT3, vrdT3, parkContent)
	if f["approvable"] != "no" {
		t.Fatalf("approvable = %q, want no — a T3 verdict cannot land under a T1 ceiling", f["approvable"])
	}
	if !strings.Contains(f["blocker"], "T3") || !strings.Contains(f["blocker"], "T1") {
		t.Errorf("blocker = %q, want it to name both tiers", f["blocker"])
	}
	if !strings.Contains(f["mint"], "-max-tier T3") || !strings.Contains(f["mint"], "-repo itsHabib/workbench") {
		t.Errorf("mint = %q, want a paste-ready T3 mint for the repo", f["mint"])
	}
	if !strings.Contains(f["mint"], "-state ") {
		t.Errorf("mint = %q, want it pinned to the watched state dir", f["mint"])
	}
	if f["verdict_tier"] != "T3" || f["grant_tier"] != "T1" {
		t.Errorf("tiers = verdict %q / grant %q, want T3 / T1", f["verdict_tier"], f["grant_tier"])
	}
}

// TestPreflight_ApprovableUnderALiveCeiling is the other half: the same park
// under a grant that can actually authorize it must stay approvable, or the
// check would have quietly deleted the phone path for every park.
func TestPreflight_ApprovableUnderALiveCeiling(t *testing.T) {
	wide := strings.Replace(parkContent, `"grant":"grt_ceiling"`, `"grant":"grt_wide"`, 1)
	f := parkEvent(t, grantT1, grantT3, vrdT3, wide)
	if f["approvable"] != "yes" {
		t.Fatalf("approvable = %q, want yes (T3 verdict, T3 ceiling)", f["approvable"])
	}
	for _, k := range []string{"blocker", "needs", "mint"} {
		if f[k] != "" {
			t.Errorf("approvable park must carry no %s, got %q", k, f[k])
		}
	}
}

// TestPreflight_CycleCeiling pins the second ceiling: a PR that has spent its
// grant's review-cycle budget cannot be approved either, and gate refuses at
// cycle used+1 — so the count must exclude the run being judged, or a park
// would always count itself and refuse one cycle early.
func TestPreflight_CycleCeiling(t *testing.T) {
	// Three prior runs, each with a counting outcome over a verdict naming
	// workbench#242 — exactly the budget of a -max-cycles 3 grant.
	var lines []string
	lines = append(lines, grantT3)
	for _, r := range []string{"run_a", "run_b", "run_c"} {
		lines = append(lines,
			`{"id":"vrd_`+r+`","kind":"verdict","run":"`+r+`","time":"2026-08-20T10:00:00Z","body":{"subject":{"repo":"itsHabib/workbench","number":242},"source":"reducer","decision":"pass","tier":"T1","why":"ok"},"prev":"x","hash":"x"}`,
			`{"id":"act_`+r+`","kind":"action","run":"`+r+`","time":"2026-08-20T10:00:01Z","parents":["vrd_`+r+`"],"body":{"outcome":"would_merge"},"prev":"x","hash":"x"}`)
	}
	wide := strings.Replace(parkContent, `"grant":"grt_ceiling"`, `"grant":"grt_wide"`, 1)
	f := parkEvent(t, append(lines, vrdT3, wide)...)
	if f["approvable"] != "no" {
		t.Fatalf("approvable = %q, want no — 3 of 3 cycles consumed", f["approvable"])
	}
	if !strings.Contains(f["blocker"], "cycle 4 exceeds grant ceiling 3") {
		t.Errorf("blocker = %q, want the cycle arithmetic", f["blocker"])
	}
	if !strings.Contains(f["mint"], "-max-cycles 4") {
		t.Errorf("mint = %q, want a wider -max-cycles", f["mint"])
	}
}

// TestPreflight_CycleCountExcludesCeilingParks mirrors gate's own rule: an
// escalation carrying a park CODE is authorization exhaustion, not review work,
// so it burns no cycle. Counting it would make the re-mint self-defeating —
// each failed retry would spend the cycle the wider grant was minted to free.
func TestPreflight_CycleCountExcludesCeilingParks(t *testing.T) {
	coded := `{"id":"esc_coded","kind":"escalation","run":"run_prior","time":"2026-08-20T10:00:01Z","parents":["vrd_prior"],"body":{"outcome":"parked_for_judgment","code":"grant_tier_exceeded","verdict":"vrd_prior","grant":"grt_wide","repo":"itsHabib/workbench","number":242}}`
	prior := `{"id":"vrd_prior","kind":"verdict","run":"run_prior","time":"2026-08-20T10:00:00Z","body":{"subject":{"repo":"itsHabib/workbench","number":242},"source":"reducer","decision":"escalate","tier":"T3","why":"x"},"prev":"x","hash":"x"}`
	refused := `{"id":"act_refused","kind":"action","run":"run_ref","time":"2026-08-20T11:00:00Z","parents":["vrd_prior"],"body":{"outcome":"capability_refused"}}`
	oneCycle := strings.Replace(grantT3, `"max_cycles":3`, `"max_cycles":1`, 1)
	wide := strings.Replace(parkContent, `"grant":"grt_ceiling"`, `"grant":"grt_wide"`, 1)
	f := parkEvent(t, oneCycle, prior, coded, refused, vrdT3, wide)
	if f["approvable"] != "yes" {
		t.Fatalf("approvable = %q, want yes: neither a coded park nor a capability refusal consumes a cycle (blocker %q)", f["approvable"], f["blocker"])
	}
}

// TestPreflight_FailsOpen pins the sink law at the seam that matters: when the
// join cannot be made, flare writes no verdict and the card renders as it
// always did. A missing artifact must never cost the operator their one remote
// path.
func TestPreflight_FailsOpen(t *testing.T) {
	cases := map[string][]string{
		"grant artifact absent":   {vrdT3, parkContent},
		"verdict artifact absent": {grantT1, parkContent},
		"park names no grant": {grantT1, vrdT3,
			strings.Replace(parkContent, `"grant":"grt_ceiling"`, `"grant":""`, 1)},
		"park names a none: sentinel": {grantT1, vrdT3,
			strings.Replace(parkContent, `"grant":"grt_ceiling"`, `"grant":"none:not-a-gate-run"`, 1)},
	}
	for name, lines := range cases {
		t.Run(name, func(t *testing.T) {
			f := parkEvent(t, lines...)
			if f["approvable"] == "no" {
				t.Fatalf("an unreadable join must not withhold the button, got blocker %q", f["blocker"])
			}
		})
	}
}

// TestLedgerSkipsUndecodableLines keeps the ledger's tolerance distinct from
// Read's strictness. Read still fails a corrupt artifact line loudly (a corrupt
// log must not read as quiet); the ledger, whose only power is to REMOVE a
// button, must degrade to "cannot say" instead.
func TestLedgerSkipsUndecodableLines(t *testing.T) {
	lg := buildLedger([]byte("{not json\n" + grantT1 + "\n"))
	if _, ok := lg.grants["grt_ceiling"]; !ok {
		t.Fatal("a torn line must not stop the index at the lines after it")
	}
	if _, known := lg.cycles("itsHabib/workbench", 242, "run_x"); !known {
		t.Error("an index with no outcomes still knows the count is zero")
	}
}

// TestStrictLedgerFailsOnCorruptLine pins the digest's posture, which is
// Read's and not the tolerant join's: a corrupt complete line fails the build
// loudly, because a skipped grant or terminal would render as "nothing parked"
// or "no live grant" — the quiet wrong answer about the operator's authority.
func TestStrictLedgerFailsOnCorruptLine(t *testing.T) {
	if _, err := strictLedger([]byte("{not json\n" + grantT1 + "\n")); err == nil {
		t.Fatal("a corrupt line must fail the authority read")
	}
	if _, err := strictLedger([]byte(grantT1 + "\n{torn")); err != nil {
		t.Fatalf("a torn final line is an in-progress write, not corruption: %v", err)
	}
}

// TestLastCeilingsIsOnePriorGrant pins that a proposed re-mint is a tuple the
// operator actually signed. (T3, 1 cycle) and (T1, 9 cycles) must never
// compose into (T3, 9) — a grant nobody minted, presented as a safe re-mint.
func TestLastCeilingsIsOnePriorGrant(t *testing.T) {
	narrowWide := `{"id":"grt_a","kind":"grant","run":"run_mint","time":"2026-08-21T20:00:00Z","body":{"repo":"itsHabib/workbench","action":"merge","max_tier":"T3","max_cycles":1,"expires_at":"2026-08-22T13:30:49Z"}}`
	lowLong := `{"id":"grt_b","kind":"grant","run":"run_mint","time":"2026-08-21T21:00:00Z","body":{"repo":"itsHabib/workbench","action":"merge","max_tier":"T1","max_cycles":9,"expires_at":"2026-08-22T13:30:49Z"}}`
	lg := buildLedger([]byte(narrowWide + "\n" + lowLong + "\n"))
	tier, cycles := lg.lastCeilingsFor("itsHabib/workbench")
	if tier != "T1" || cycles != 9 {
		t.Fatalf("ceilings = (%s, %d), want the most recent grant's own (T1, 9)", tier, cycles)
	}
	lg = buildLedger([]byte(lowLong + "\n" + narrowWide + "\n"))
	if tier, cycles = lg.lastCeilingsFor("itsHabib/workbench"); tier != "T3" || cycles != 1 {
		t.Fatalf("ceilings = (%s, %d), want the most recent grant's own (T3, 1)", tier, cycles)
	}
}

// TestNonMergeGrantsAreNotAuthority: a grant minted for another action neither
// covers parked merge work nor lends its ceilings to a proposed merge grant.
func TestNonMergeGrantsAreNotAuthority(t *testing.T) {
	lapsedMerge := `{"id":"grt_lapsed","kind":"grant","run":"run_mint","time":"2026-08-21T20:00:00Z","body":{"repo":"itsHabib/workbench","action":"merge","max_tier":"T1","max_cycles":3,"expires_at":"2026-08-22T13:30:49Z"}}`
	deploy := `{"id":"grt_deploy","kind":"grant","run":"run_mint","time":"2026-08-21T22:00:00Z","body":{"repo":"itsHabib/workbench","action":"deploy","max_tier":"T3","max_cycles":9,"expires_at":"2126-08-22T13:30:49Z"}}`
	park := `{"id":"esc_p","kind":"escalation","run":"run_242","time":"2026-08-21T21:00:01Z","parents":["vrd_reduced","grt_lapsed"],"body":{"outcome":"parked_for_judgment","verdict":"vrd_reduced","grant":"grt_lapsed","question":"your call","repo":"itsHabib/workbench","number":242}}`
	lg := buildLedger([]byte(lapsedMerge + "\n" + deploy + "\n" + vrdT3 + "\n" + park + "\n"))
	if _, ok := lg.grants["grt_deploy"]; ok {
		t.Fatal("a non-merge grant must not be indexed as merge authority")
	}
	if tier, cycles := lg.lastCeilingsFor("itsHabib/workbench"); tier != "T1" || cycles != 3 {
		t.Fatalf("ceilings = (%s, %d), want the merge grant's (T1, 3)", tier, cycles)
	}
	rows := lg.authority(time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC))
	if len(rows) != 1 || rows[0].Live || rows[0].Parked != 1 {
		t.Fatalf("a lapsed merge grant plus a live deploy grant must read as parked with no live grant: %+v", rows)
	}
}

// TestAlreadyMergedActionRetiresThePark: gate's already_merged action carries
// its subject in its body and is parented to view evidence, not a verdict. It
// must still supersede the older park, or the digest keeps counting — and
// paging for — a PR that is already merged.
func TestAlreadyMergedActionRetiresThePark(t *testing.T) {
	merged := `{"id":"act_merged","kind":"action","run":"run_243","time":"2026-08-21T22:00:00Z","parents":["view_1"],"body":{"outcome":"already_merged","repo":"itsHabib/workbench","number":242,"merge_commit":"abc"}}`
	park := `{"id":"esc_p","kind":"escalation","run":"run_242","time":"2026-08-21T21:00:01Z","parents":["vrd_reduced","grt_ceiling"],"body":{"outcome":"parked_for_judgment","verdict":"vrd_reduced","grant":"grt_ceiling","question":"your call","repo":"itsHabib/workbench","number":242}}`
	lg := buildLedger([]byte(grantT1 + "\n" + vrdT3 + "\n" + park + "\n"))
	if n := lg.parkedByRepo()["itsHabib/workbench"]; n != 1 {
		t.Fatalf("test premise: parked = %d before already_merged, want 1", n)
	}
	lg = buildLedger([]byte(grantT1 + "\n" + vrdT3 + "\n" + park + "\n" + merged + "\n"))
	if n := lg.parkedByRepo()["itsHabib/workbench"]; n != 0 {
		t.Fatalf("parked = %d after already_merged, want 0", n)
	}
}

// TestLedgerCyclesUnknownOnDanglingParent pins the fail-open direction of the
// count. gate treats an outcome whose parent verdict is missing as an error and
// parks; flare cannot see a truncated prefix of the log as anything but a gap,
// and an undercount here would withhold a working button.
func TestLedgerCyclesUnknownOnDanglingParent(t *testing.T) {
	orphan := `{"id":"act_orphan","kind":"action","run":"run_z","time":"2026-08-20T10:00:01Z","parents":["vrd_gone"],"body":{"outcome":"would_merge"}}`
	lg := buildLedger([]byte(orphan + "\n"))
	if _, known := lg.cycles("itsHabib/workbench", 242, "run_x"); known {
		t.Fatal("a dangling parent verdict must make the count unknown, not zero")
	}
}
