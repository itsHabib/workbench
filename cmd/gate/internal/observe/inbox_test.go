package observe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

var inboxBase = time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)

// art builds an artifact with a marshaled body, for the pure buildInbox tests
// that never touch a store.
func art(kind, run, id string, at time.Time, body any) state.Artifact {
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return state.Artifact{ID: id, Kind: kind, Run: run, Time: at, Body: raw}
}

func esc(grant, question, code, repo string, number int) map[string]any {
	return map[string]any{
		"outcome": "parked_for_judgment", "verdict": "vrd_x", "grant": grant,
		"question": question, "code": code, "repo": repo, "number": number,
	}
}

func grant(repo string, expires time.Time) capability.Grant {
	return capability.Grant{Repo: repo, Action: "merge", MaxTier: "T1", MaxCycles: 3, ExpiresAt: expires, MintedBy: "test", Sig: "fixture"}
}

// TestBuildInboxParked pins the parked-run derivation: a run whose latest
// terminal is an escalation is awaiting judgment; one resolved by a later action
// is not; a run re-parked after a judgment is awaiting again; and the list is
// oldest-park-first.
func TestBuildInboxParked(t *testing.T) {
	arts := []state.Artifact{
		// Run A: a lone escalation, newest park.
		art(state.KindEscalation, "run_a", "esc_a", inboxBase.Add(10*time.Minute), esc("grt_a", "tier T2 exceeds ceiling T1", "grant_tier_exceeded", "o/widget", 142)),
		// Run B: escalation then a resolving action — no longer awaiting.
		art(state.KindEscalation, "run_b", "esc_b", inboxBase.Add(2*time.Minute), esc("grt_b", "q b", "", "o/api", 87)),
		art(state.KindAction, "run_b", "act_b", inboxBase.Add(3*time.Minute), map[string]any{"outcome": "blocked"}),
		// Run C: escalation, a judgment, then re-parked — still awaiting.
		art(state.KindEscalation, "run_c", "esc_c1", inboxBase.Add(4*time.Minute), esc("grt_c", "q c1", "", "o/api", 88)),
		art(state.KindJudgment, "run_c", "jdg_c", inboxBase.Add(5*time.Minute), map[string]any{"decision": "pass"}),
		art(state.KindEscalation, "run_c", "esc_c2", inboxBase.Add(6*time.Minute), esc("grt_c", "q c2 still over cap", "grant_cycle_exceeded", "o/api", 88)),
	}

	in := buildInbox(arts, inboxBase.Add(time.Hour), "")

	if len(in.Parked) != 2 {
		t.Fatalf("want 2 parked runs (A + re-parked C), got %d: %+v", len(in.Parked), in.Parked)
	}
	// Oldest park first: C's latest escalation (t+6m) precedes A's (t+10m).
	if in.Parked[0].Run != "run_c" || in.Parked[1].Run != "run_a" {
		t.Fatalf("parked order should be oldest-first [run_c, run_a], got [%s, %s]", in.Parked[0].Run, in.Parked[1].Run)
	}
	// The re-parked run reflects its LATEST escalation, not the resolved first one.
	if in.Parked[0].Question != "q c2 still over cap" || in.Parked[0].Code != "grant_cycle_exceeded" {
		t.Fatalf("re-parked run must carry the latest escalation, got %+v", in.Parked[0])
	}
	a := in.Parked[1]
	if a.Repo != "o/widget" || a.Number != 142 || a.Grant != "grt_a" {
		t.Fatalf("parked run A subject/grant wrong: %+v", a)
	}
	if a.ParkedAt != inboxBase.Add(10*time.Minute).Format(time.RFC3339) {
		t.Fatalf("parked_at = %q, want the escalation time", a.ParkedAt)
	}
}

func TestBuildInboxCollapsesRunsByPR(t *testing.T) {
	subject := map[string]any{"subject": map[string]any{
		"repo": "o/widget", "number": 142, "head_sha": "deadbeef",
	}}
	arts := []state.Artifact{
		art(state.KindEscalation, "run_old", "esc_old", inboxBase, esc("grt_a", "old park", "", "o/widget", 142)),
		art(state.KindVerdict, "run_new", "vrd_new", inboxBase.Add(time.Minute), subject),
		art(state.KindAction, "run_new", "act_new", inboxBase.Add(2*time.Minute), map[string]any{"outcome": "would_merge"}),
	}

	in := buildInbox(arts, inboxBase.Add(time.Hour), "")
	if len(in.Parked) != 0 {
		t.Fatalf("newer action for the same PR must suppress the old park, got %+v", in.Parked)
	}
}

func TestBuildInboxRecoversSubjectDisplayFacts(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindEvidence, "run_old", "evd_old", inboxBase, map[string]any{
			"pr":   map[string]any{"repo": "o/widget", "number": 142},
			"data": map[string]any{"title": "fix the docket", "headRefOid": "abc123"},
		}),
		// The legacy escalation body carries no subject; evidence recovers it.
		art(state.KindEscalation, "run_old", "esc_old", inboxBase.Add(time.Minute), esc("grt_a", "needs judgment", "", "", 0)),
	}

	in := buildInbox(arts, inboxBase.Add(time.Hour), "")
	if len(in.Parked) != 1 {
		t.Fatalf("want one recovered actionable park, got %+v", in)
	}
	p := in.Parked[0]
	if p.Repo != "o/widget" || p.Number != 142 || p.Title != "fix the docket" || p.HeadSHA != "abc123" {
		t.Fatalf("display facts not recovered: %+v", p)
	}
	if p.URL != "https://github.com/o/widget/pull/142" {
		t.Fatalf("canonical PR URL = %q", p.URL)
	}
}

func TestBuildInboxNewestParkWinsForPR(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindEscalation, "run_old", "esc_old", inboxBase, esc("grt_a", "old", "", "o/r", 7)),
		art(state.KindEscalation, "run_new", "esc_new", inboxBase.Add(time.Minute), esc("grt_b", "new", "", "o/r", 7)),
	}
	in := buildInbox(arts, inboxBase.Add(time.Hour), "")
	if len(in.Parked) != 1 || in.Parked[0].Run != "run_new" {
		t.Fatalf("want only newest parked run for PR, got %+v", in.Parked)
	}
}

func TestReconcileLiveKeepsOnlyConfirmedOpenOrUnknown(t *testing.T) {
	parked := []ParkedRun{
		{Run: "run_open", Repo: "o/r", Number: 1, Title: "stale title"},
		{Run: "run_merged", Repo: "o/r", Number: 2},
		{Run: "run_unknown", Repo: "o/r", Number: 3},
	}
	lookup := func(_ string, number int) (LivePR, error) {
		switch number {
		case 1:
			return LivePR{State: "OPEN", Title: "live title", HeadSHA: "abc", URL: "https://github.com/o/r/pull/1"}, nil
		case 2:
			return LivePR{State: "MERGED"}, nil
		default:
			return LivePR{}, fmt.Errorf("lookup unavailable")
		}
	}

	got := reconcileLive(parked, lookup)
	if len(got) != 2 || got[0].Run != "run_open" || got[1].Run != "run_unknown" {
		t.Fatalf("live reconcile = %+v", got)
	}
	if got[0].PRState != "OPEN" || got[0].Title != "live title" || got[0].HeadSHA != "abc" {
		t.Fatalf("open PR was not enriched: %+v", got[0])
	}
	if got[1].PRState != "unknown" {
		t.Fatalf("failed lookup must remain visible as unknown: %+v", got[1])
	}
}

// TestBuildInboxJudgeCommand pins that the suggested judge command carries the
// run's own grant id and the stateArg, so resolving a park is a paste, never an
// id hunt.
func TestBuildInboxJudgeCommand(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindEscalation, "run_a", "esc_a", inboxBase, esc("grt_live", "why", "grant_tier_exceeded", "o/r", 5)),
	}

	in := buildInbox(arts, inboxBase, "")
	want := `gate judge -run run_a -grant grt_live -decision <pass|block> -why "..."`
	if in.Parked[0].JudgeCommand != want {
		t.Fatalf("judge command = %q, want %q", in.Parked[0].JudgeCommand, want)
	}
	if in.Parked[0].ExplainCommand != "gate explain -run run_a -html" {
		t.Fatalf("explain command = %q", in.Parked[0].ExplainCommand)
	}

	// A custom state dir is spliced into every suggested command.
	in2 := buildInbox(arts, inboxBase, " -state /custom")
	if !strings.Contains(in2.Parked[0].JudgeCommand, "gate judge -state /custom -run run_a") {
		t.Fatalf("stateArg not spliced into judge command: %q", in2.Parked[0].JudgeCommand)
	}
	if !strings.Contains(in2.Parked[0].ExplainCommand, "gate explain -state /custom -run run_a") {
		t.Fatalf("stateArg not spliced into explain command: %q", in2.Parked[0].ExplainCommand)
	}
}

// TestBuildInboxUnparseableEscalation pins fail-visible decoding: an escalation
// whose body isn't the expected object still lists its run (so the park is never
// silently dropped), just without the decoded fields.
func TestBuildInboxUnparseableEscalation(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindEscalation, "run_bad", "esc_bad", inboxBase, []string{"not", "an", "object"}),
	}
	in := buildInbox(arts, inboxBase, "")
	if len(in.Parked) != 0 || len(in.Unattributed) != 1 || in.Unattributed[0].Run != "run_bad" {
		t.Fatalf("unparseable escalation must stay visible but not actionable, got %+v", in)
	}
	if in.Unattributed[0].Question != "" {
		t.Fatalf("want empty question for unparseable body, got %q", in.Unattributed[0].Question)
	}
	// The grant placeholder keeps the command runnable-shaped even with no id.
	if !strings.Contains(in.Unattributed[0].JudgeCommand, "-grant grt_...") {
		t.Fatalf("missing grant placeholder: %q", in.Unattributed[0].JudgeCommand)
	}
}

// TestBuildInboxGrants pins the ledger: live grants soonest-expiry first, then
// grants expired within the window most-recent first, and grants expired beyond
// the window omitted entirely.
func TestBuildInboxGrants(t *testing.T) {
	now := inboxBase
	arts := []state.Artifact{
		art(state.KindGrant, "run_mint", "grt_far", now, grant("o/widget", now.Add(5*time.Hour+49*time.Minute))),
		art(state.KindGrant, "run_mint", "grt_soon", now, grant("o/api", now.Add(21*time.Minute))),
		art(state.KindGrant, "run_mint", "grt_recent", now, grant("o/api", now.Add(-16*time.Hour))),
		art(state.KindGrant, "run_mint", "grt_old", now, grant("o/api", now.Add(-30*time.Hour))),
	}

	in := buildInbox(arts, now, "")

	if len(in.Grants) != 3 {
		t.Fatalf("want 3 ledger rows (2 live + 1 recently expired; old omitted), got %d: %+v", len(in.Grants), in.Grants)
	}
	if in.Grants[0].ID != "grt_soon" || in.Grants[1].ID != "grt_far" {
		t.Fatalf("live grants must lead soonest-expiry first, got [%s, %s]", in.Grants[0].ID, in.Grants[1].ID)
	}
	if in.Grants[0].Expired || in.Grants[0].Remaining != "in 21m" {
		t.Fatalf("soon grant should be live 'in 21m', got expired=%v remaining=%q", in.Grants[0].Expired, in.Grants[0].Remaining)
	}
	if in.Grants[1].Remaining != "in 5h49m" {
		t.Fatalf("far grant remaining = %q, want 'in 5h49m'", in.Grants[1].Remaining)
	}
	exp := in.Grants[2]
	if exp.ID != "grt_recent" || !exp.Expired || exp.Remaining != "16h0m ago" {
		t.Fatalf("recently-expired row wrong: %+v", exp)
	}
}

// TestBuildInboxGrantsDeterministicTie pins the review nit fix: two grants
// sharing an expiry instant (indistinguishable at the second-precision string)
// order deterministically by id, so `gate next` output doesn't churn run to run.
func TestBuildInboxGrantsDeterministicTie(t *testing.T) {
	exp := inboxBase.Add(time.Hour)
	arts := []state.Artifact{
		art(state.KindGrant, "run_mint", "grt_bbb", inboxBase, grant("o/r", exp)),
		art(state.KindGrant, "run_mint", "grt_aaa", inboxBase, grant("o/r", exp)),
	}
	in := buildInbox(arts, inboxBase, "")
	if len(in.Grants) != 2 || in.Grants[0].ID != "grt_aaa" || in.Grants[1].ID != "grt_bbb" {
		t.Fatalf("equal-expiry grants must order by id, got %+v", in.Grants)
	}
}

// TestBuildInboxExpiryBoundaryMatchesCheck pins that a grant exactly at its
// expiry reads as live, matching capability.Check (expired strictly after).
func TestBuildInboxExpiryBoundaryMatchesCheck(t *testing.T) {
	now := inboxBase
	arts := []state.Artifact{art(state.KindGrant, "run_mint", "grt_edge", now, grant("o/r", now))}
	in := buildInbox(arts, now, "")
	if len(in.Grants) != 1 || in.Grants[0].Expired {
		t.Fatalf("grant at exactly its expiry must read live, got %+v", in.Grants)
	}
}

func TestShortDur(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "<1m"},
		{45 * time.Minute, "45m"},
		{5*time.Hour + 49*time.Minute, "5h49m"},
		{2*24*time.Hour + 3*time.Hour, "2d3h"},
		{16 * time.Hour, "16h0m"},
	}
	for _, c := range cases {
		if got := shortDur(c.d); got != c.want {
			t.Errorf("shortDur(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

// TestNextTextEmpty pins the designed empty state: no parked runs prints a calm
// line, not a blank page or an error.
func TestNextTextEmpty(t *testing.T) {
	st, err := state.Open(t.TempDir(), func() time.Time { return inboxBase })
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := NextText(&buf, st, func() time.Time { return inboxBase }, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "nothing awaits judgment.") {
		t.Fatalf("empty inbox should say so, got %q", buf.String())
	}
}

// TestNextTextRendersParked pins the human layout: the run/subject/code header,
// the quoted question, and the two paste-ready arrows, followed by the grants
// table.
func TestNextTextRendersParked(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindGrant, "run_mint", "grt_a1b2c3d4", inboxBase, grant("acme/widget", inboxBase.Add(5*time.Hour+49*time.Minute))),
		art(state.KindEscalation, "run_9f3a41c2", "esc_f6789012", inboxBase.Add(time.Minute),
			esc("grt_a1b2c3d4", "verdict tier T2 exceeds grant ceiling T1; flake is known", "grant_tier_exceeded", "acme/widget", 142)),
	}
	var buf bytes.Buffer
	renderInbox(&buf, buildInbox(arts, inboxBase.Add(time.Hour), ""))
	out := buf.String()
	t.Logf("\n%s", out)

	for _, want := range []string{
		"awaiting judgment (1)",
		"acme/widget#142  run_9f3a41c2  grant_tier_exceeded",
		`"verdict tier T2 exceeds grant ceiling T1; flake is known"`,
		"→ gate judge -run run_9f3a41c2 -grant grt_a1b2c3d4 -decision <pass|block>",
		"→ gate explain -run run_9f3a41c2 -html",
		"grants",
		"grt_a1b2c3d4  acme/widget  merge  T1  in 4h49m",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered inbox missing %q\n---\n%s", want, out)
		}
	}
}

// TestNextJSONOverStore drives the full read path — store scan → projection →
// JSON — over a store built with the real state API, the shape the console feed
// consumes.
func TestNextJSONOverStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	st, err := state.Open(dir, func() time.Time { return inboxBase })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(state.KindGrant, "run_mint", nil, grant("o/r", inboxBase.Add(3*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(state.KindEscalation, "run_park", []string{"vrd_x", "grt_x"}, esc("grt_x", "tier T2 exceeds ceiling T1", "grant_tier_exceeded", "o/r", 42)); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := NextJSON(&buf, st, func() time.Time { return inboxBase }, ""); err != nil {
		t.Fatal(err)
	}
	var in Inbox
	if err := json.Unmarshal(buf.Bytes(), &in); err != nil {
		t.Fatalf("next -json is not valid Inbox JSON: %v\n%s", err, buf.Bytes())
	}
	if len(in.Parked) != 1 || in.Parked[0].Run != "run_park" || in.Parked[0].Grant != "grt_x" {
		t.Fatalf("parked projection wrong: %+v", in.Parked)
	}
	if len(in.Grants) != 1 || in.Grants[0].Repo != "o/r" || in.Grants[0].Expired {
		t.Fatalf("grant ledger wrong: %+v", in.Grants)
	}
}
