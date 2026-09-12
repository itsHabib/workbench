package observe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
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

// TestCeilingParkSurfacesEscapeRoute pins that a ceiling park projects an
// escape route instead of a judge command: judging under the same grant
// re-applies the ceiling, so advertising `gate judge` contradicts the recorded
// route. The command is rebuilt against the state path THIS projection was
// invoked with — the sealed escape's absolute -state goes stale when the
// ledger is copied or mounted elsewhere — and only the sealed prose is kept.
// A legacy ceiling park with no sealed escape gets the same derived route.
func TestCeilingParkSurfacesEscapeRoute(t *testing.T) {
	ceiling := esc("grt_a", "tier T2 exceeds ceiling T1", "grant_tier_exceeded", "o/widget", 142)
	ceiling["escape"] = map[string]any{"why": "mint a wider grant", "next": "gate explain -run run_a -state /old/mount"}
	legacy := esc("grt_c", "cycle cap hit", "grant_cycle_exceeded", "o/widget", 143)
	content := esc("grt_b", "needs judgment", "", "o/api", 87)
	content["escape"] = map[string]any{"why": "inspect state", "next": "gate next"}
	arts := []state.Artifact{
		art(state.KindEscalation, "run_a", "esc_a", inboxBase, ceiling),
		art(state.KindEscalation, "run_c", "esc_c", inboxBase.Add(time.Minute), legacy),
		art(state.KindEscalation, "run_b", "esc_b", inboxBase.Add(2*time.Minute), content),
	}

	in := buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: " -state /new/mount"})

	if len(in.Parked) != 3 {
		t.Fatalf("want 3 parked runs, got %d: %+v", len(in.Parked), in.Parked)
	}
	byRun := make(map[string]ParkedRun, len(in.Parked))
	for _, p := range in.Parked {
		byRun[p.Run] = p
	}
	if p := byRun["run_a"]; p.JudgeCommand != "" || p.Escape == nil ||
		p.Escape.Next != "gate explain -state /new/mount -run run_a" || p.Escape.Why != "mint a wider grant" {
		t.Fatalf("ceiling park must rebind the escape to the current state path and drop the judge command, got %+v", p)
	}
	if p := byRun["run_c"]; p.JudgeCommand != "" || p.Escape == nil || p.Escape.Next != "gate explain -state /new/mount -run run_c" {
		t.Fatalf("legacy ceiling park must derive an escape and drop the judge command, got %+v", p)
	}
	if p := byRun["run_b"]; p.JudgeCommand == "" || p.Escape == nil || p.Escape.Next != "gate next" {
		t.Fatalf("content park must keep its judge command and sealed escape, got %+v", p)
	}
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

	in := buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: ""})

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
	// The projected escalation id is the LATEST park's artifact id — the key a
	// remote resolver joins to the grant. It must track the re-park, not the id
	// of the escalation an earlier judgment already resolved.
	if in.Parked[0].Escalation != "esc_c2" {
		t.Fatalf("re-parked escalation id = %q, want esc_c2", in.Parked[0].Escalation)
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
		art(state.KindVerdict, "run_new", "vrd_new", inboxBase.Add(-time.Minute), subject),
		// Append order is authoritative even when the clock moves backward.
		art(state.KindAction, "run_new", "act_new", inboxBase.Add(-2*time.Minute), map[string]any{"outcome": "would_merge"}),
	}

	in := buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: ""})
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

	in := buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: ""})
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
	in := buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: ""})
	if len(in.Parked) != 1 || in.Parked[0].Run != "run_new" {
		t.Fatalf("want only newest parked run for PR, got %+v", in.Parked)
	}
}

// snapshot builds a liveRepos from a per-repo open-PR map and a per-repo error
// map — the batched result the row reconcilers read against, without spinning
// the worker pool.
func snapshot(open map[string]map[int]LivePR, errs map[string]error) liveRepos {
	if open == nil {
		open = map[string]map[int]LivePR{}
	}
	if errs == nil {
		errs = map[string]error{}
	}
	return liveRepos{open: open, errs: errs}
}

// TestReconcileLiveKeepsOpenDropsAbsentUnknownOnError pins the parked reconcile
// against the batched snapshot: a PR present in its repo's open set stays OPEN
// and enriched; a PR absent from the set (no fetch error) is not open and drops;
// a repo whose fetch errored keeps its rows visible as unknown.
func TestReconcileLiveKeepsOpenDropsAbsentUnknownOnError(t *testing.T) {
	parked := []ParkedRun{
		{Run: "run_open", Repo: "o/r", Number: 1, Title: "stale title"},
		{Run: "run_absent", Repo: "o/r", Number: 2},
		{Run: "run_broken", Repo: "o/broken", Number: 3},
	}
	live := snapshot(
		map[string]map[int]LivePR{
			"o/r": {1: {State: "OPEN", Title: "live title", HeadSHA: "abc", URL: "https://github.com/o/r/pull/1"}},
		},
		map[string]error{"o/broken": fmt.Errorf("lookup unavailable")},
	)

	got, _ := reconcileLive(parked, live)
	if len(got) != 2 || got[0].Run != "run_open" || got[1].Run != "run_broken" {
		t.Fatalf("live reconcile = %+v", got)
	}
	if got[0].PRState != "OPEN" || got[0].Title != "live title" || got[0].HeadSHA != "abc" {
		t.Fatalf("open PR was not enriched: %+v", got[0])
	}
	if got[1].PRState != "unknown" || !strings.Contains(got[1].PRStateReason, "lookup unavailable") {
		t.Fatalf("failed repo fetch must remain visible as unknown: %+v", got[1])
	}
	var text bytes.Buffer
	renderInbox(&text, Inbox{Parked: got})
	if !strings.Contains(text.String(), "PR state unknown: lookup unavailable") {
		t.Fatalf("text view must surface live lookup failure:\n%s", text.String())
	}
}

// TestReconcileInboxFetchesOncePerDistinctRepo pins the batching invariant — the
// whole point of the seam: the lister is called ONCE per DISTINCT repo, never
// once per row, even with many rows spread across few repos.
func TestReconcileInboxFetchesOncePerDistinctRepo(t *testing.T) {
	in := Inbox{
		Parked: []ParkedRun{
			{Run: "p1", Repo: "o/a", Number: 1},
			{Run: "p2", Repo: "o/a", Number: 2},
			{Run: "p3", Repo: "o/b", Number: 3},
		},
		ReadyToMerge: []ReadyRow{
			{Run: "r1", Repo: "o/a", Number: 4},
			{Run: "r2", Repo: "o/c", Number: 5},
		},
		NeedsGrant: []NeedsGrantRow{
			{Repo: "o/b", GrantState: "expired"},
			{Repo: "o/d", GrantState: "absent"},
		},
	}
	// resolveRepos calls fetch from multiple worker goroutines concurrently, so
	// the counter map must be guarded — a plain map write would race (CI runs
	// -race) and can panic on concurrent writes.
	var mu sync.Mutex
	calls := make(map[string]int)
	fetch := func(repo string) (map[int]LivePR, error) {
		mu.Lock()
		calls[repo]++
		mu.Unlock()
		return map[int]LivePR{}, nil
	}
	reconcileInbox(in, fetch)

	// Four distinct repos across 3 parked + 2 ready + 2 needs_grant rows.
	if len(calls) != 4 {
		t.Fatalf("want 4 distinct-repo fetches, got %d: %+v", len(calls), calls)
	}
	for repo, n := range calls {
		if n != 1 {
			t.Fatalf("repo %s fetched %d times, want exactly 1 (O(repos), not O(rows))", repo, n)
		}
	}
}

// TestReconcileInboxRepoErrorPreservesEverySurface pins that a single repo whose
// fetch errors degrades — never drops — every surface: parked stays as unknown,
// ready stays as-is, needs_grant stays with a nil OpenPRs.
func TestReconcileInboxRepoErrorPreservesEverySurface(t *testing.T) {
	in := Inbox{
		Parked:       []ParkedRun{{Run: "p", Repo: "o/broken", Number: 1}},
		ReadyToMerge: []ReadyRow{{Run: "r", Repo: "o/broken", Number: 2, MergeCommand: "gh pr merge 2"}},
		NeedsGrant:   []NeedsGrantRow{{Repo: "o/broken", GrantState: "expired"}},
	}
	fetch := func(_ string) (map[int]LivePR, error) {
		return nil, fmt.Errorf("gh unreachable")
	}
	got := reconcileInbox(in, fetch)
	if len(got.Parked) != 1 || got.Parked[0].PRState != "unknown" {
		t.Fatalf("parked must survive as unknown on a repo error: %+v", got.Parked)
	}
	if len(got.ReadyToMerge) != 1 || got.ReadyToMerge[0].Run != "r" {
		t.Fatalf("ready must survive as-is on a repo error: %+v", got.ReadyToMerge)
	}
	if len(got.NeedsGrant) != 1 || got.NeedsGrant[0].OpenPRs != nil {
		t.Fatalf("needs_grant must survive with nil open_prs on a repo error: %+v", got.NeedsGrant)
	}
}

// TestBuildInboxJudgeCommand pins that the suggested judge command carries the
// run's own grant id and the stateArg, so resolving a park is a paste, never an
// id hunt.
func TestBuildInboxJudgeCommand(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindEscalation, "run_a", "esc_a", inboxBase, esc("grt_live", "why", "", "o/r", 5)),
	}

	in := buildInbox(arts, inboxBase, NextRequest{StateArg: ""})
	want := `gate judge -run run_a -grant grt_live -decision <pass|block> -why "..."`
	if in.Parked[0].JudgeCommand != want {
		t.Fatalf("judge command = %q, want %q", in.Parked[0].JudgeCommand, want)
	}
	if in.Parked[0].ExplainCommand != "gate explain -run run_a -html" {
		t.Fatalf("explain command = %q", in.Parked[0].ExplainCommand)
	}

	// A custom state dir is spliced into every suggested command.
	in2 := buildInbox(arts, inboxBase, NextRequest{StateArg: " -state /custom"})
	if !strings.Contains(in2.Parked[0].JudgeCommand, "gate judge -state /custom -run run_a") {
		t.Fatalf("stateArg not spliced into judge command: %q", in2.Parked[0].JudgeCommand)
	}
	if !strings.Contains(in2.Parked[0].ExplainCommand, "gate explain -state /custom -run run_a") {
		t.Fatalf("stateArg not spliced into explain command: %q", in2.Parked[0].ExplainCommand)
	}
}

// TestBuildInboxResolveCommand pins the human route next to the judge one: a
// park carrying both its artifact id and its grant projects a paste-ready
// `escalate resolve` line (stateArg spliced like every other suggestion), while
// a grantless park projects none — escalate's ingest refuses an empty grant, so
// a placeholder there would be a command guaranteed to fail.
func TestBuildInboxResolveCommand(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindEscalation, "run_a", "esc_a", inboxBase, esc("grt_live", "why", "", "o/r", 5)),
		art(state.KindEscalation, "run_b", "esc_b", inboxBase, esc("", "why", "", "o/r", 6)),
	}

	in := buildInbox(arts, inboxBase, NextRequest{StateArg: ""})
	byRun := map[string]ParkedRun{}
	for _, p := range in.Parked {
		byRun[p.Run] = p
	}
	want := `escalate resolve -escalation esc_a -grant grt_live -decision <pass|block> -who <you> -why "..."`
	if got := byRun["run_a"].ResolveCommand; got != want {
		t.Fatalf("resolve command = %q, want %q", got, want)
	}
	if got := byRun["run_b"].ResolveCommand; got != "" {
		t.Fatalf("a grantless park must project no resolve command, got %q", got)
	}

	in2 := buildInbox(arts, inboxBase, NextRequest{StateArg: " -state /custom"})
	for _, p := range in2.Parked {
		if p.Run != "run_a" {
			continue
		}
		if !strings.Contains(p.ResolveCommand, "escalate resolve -state /custom -escalation esc_a") {
			t.Fatalf("stateArg not spliced into resolve command: %q", p.ResolveCommand)
		}
	}
}

// TestBuildInboxUnparseableEscalation pins fail-visible decoding: an escalation
// whose body isn't the expected object still lists its run (so the park is never
// silently dropped), just without the decoded fields.
func TestBuildInboxUnparseableEscalation(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindEscalation, "run_bad", "esc_bad", inboxBase, []string{"not", "an", "object"}),
	}
	in := buildInbox(arts, inboxBase, NextRequest{StateArg: ""})
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

	in := buildInbox(arts, now, NextRequest{StateArg: ""})

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
	in := buildInbox(arts, inboxBase, NextRequest{StateArg: ""})
	if len(in.Grants) != 2 || in.Grants[0].ID != "grt_aaa" || in.Grants[1].ID != "grt_bbb" {
		t.Fatalf("equal-expiry grants must order by id, got %+v", in.Grants)
	}
}

// TestBuildInboxExpiryBoundaryMatchesCheck pins that a grant exactly at its
// expiry reads as live, matching capability.Check (expired strictly after).
func TestBuildInboxExpiryBoundaryMatchesCheck(t *testing.T) {
	now := inboxBase
	arts := []state.Artifact{art(state.KindGrant, "run_mint", "grt_edge", now, grant("o/r", now))}
	in := buildInbox(arts, now, NextRequest{StateArg: ""})
	if len(in.Grants) != 1 || in.Grants[0].Expired {
		t.Fatalf("grant at exactly its expiry must read live, got %+v", in.Grants)
	}
}

// needed builds a grant_needed record body, the shape main.go's recordGrantNeeded writes.
func needed(repo, reason string, at time.Time) map[string]any {
	return map[string]any{"repo": repo, "reason": reason, "at": at.UTC().Format(time.RFC3339)}
}

// TestNeedsGrantExpiredOnlyShows pins the base dedup law: a repo whose only
// grant record is an expired refusal shows exactly one row, grant_state
// "expired", with the refusal timestamp as last_expired_at and no live grant to
// suppress it.
func TestNeedsGrantExpiredOnlyShows(t *testing.T) {
	at := inboxBase.Add(-time.Hour)
	arts := []state.Artifact{
		art(state.KindGrantNeeded, "run_r1", "gnd_1", at, needed("o/widget", "grant_expired", at)),
	}
	in := buildInbox(arts, inboxBase, NextRequest{StateArg: ""})
	if len(in.NeedsGrant) != 1 {
		t.Fatalf("want exactly one needs_grant row, got %d: %+v", len(in.NeedsGrant), in.NeedsGrant)
	}
	r := in.NeedsGrant[0]
	if r.Repo != "o/widget" || r.GrantState != "expired" {
		t.Fatalf("row = %+v, want o/widget expired", r)
	}
	if r.LastExpiredAt != at.UTC().Format(time.RFC3339) {
		t.Fatalf("last_expired_at = %q, want %q", r.LastExpiredAt, at.UTC().Format(time.RFC3339))
	}
	if r.OpenPRs != nil {
		t.Fatalf("non-live projection must omit open_prs, got %v", *r.OpenPRs)
	}
}

// TestNeedsGrantLiveGrantSuppresses pins the suppression law: a repo with a
// currently-live merge grant never appears, even though it carries a refusal
// record from before the grant was minted.
func TestNeedsGrantLiveGrantSuppresses(t *testing.T) {
	at := inboxBase.Add(-2 * time.Hour)
	arts := []state.Artifact{
		art(state.KindGrantNeeded, "run_r1", "gnd_1", at, needed("o/api", "grant_expired", at)),
		art(state.KindGrant, "run_mint", "grt_live", inboxBase, grant("o/api", inboxBase.Add(5*time.Hour))),
	}
	in := buildInbox(arts, inboxBase, NextRequest{StateArg: ""})
	if len(in.NeedsGrant) != 0 {
		t.Fatalf("a live grant must suppress needs_grant, got %+v", in.NeedsGrant)
	}
}

// TestNeedsGrantLiveButExpiringSuppresses pins that a grant still live at now —
// even one about to expire — suppresses the row: only a genuinely lapsed repo
// shows. Matches capability.Check (live up to and including the expiry instant).
func TestNeedsGrantLiveButExpiringSuppresses(t *testing.T) {
	at := inboxBase.Add(-2 * time.Hour)
	arts := []state.Artifact{
		art(state.KindGrantNeeded, "run_r1", "gnd_1", at, needed("o/api", "grant_expired", at)),
		// Live by one minute, and even exactly at the expiry instant is still live.
		art(state.KindGrant, "run_mint", "grt_soon", inboxBase, grant("o/api", inboxBase.Add(time.Minute))),
		art(state.KindGrant, "run_mint", "grt_edge", inboxBase, grant("o/edge", inboxBase)),
		art(state.KindGrantNeeded, "run_r2", "gnd_2", at, needed("o/edge", "grant_expired", at)),
	}
	in := buildInbox(arts, inboxBase, NextRequest{StateArg: ""})
	if len(in.NeedsGrant) != 0 {
		t.Fatalf("a live-but-expiring grant (and one exactly at expiry) must suppress, got %+v", in.NeedsGrant)
	}
}

// TestNeedsGrantDedupsToMostRecent pins that many refusals for one repo fold
// into a single row carrying the most-recent record's grant_state and timestamp.
func TestNeedsGrantDedupsToMostRecent(t *testing.T) {
	early := inboxBase.Add(-3 * time.Hour)
	mid := inboxBase.Add(-2 * time.Hour)
	late := inboxBase.Add(-1 * time.Hour)
	arts := []state.Artifact{
		art(state.KindGrantNeeded, "run_a", "gnd_a", early, needed("o/r", "grant_absent", early)),
		art(state.KindGrantNeeded, "run_b", "gnd_b", late, needed("o/r", "grant_expired", late)),
		art(state.KindGrantNeeded, "run_c", "gnd_c", mid, needed("o/r", "grant_absent", mid)),
	}
	in := buildInbox(arts, inboxBase, NextRequest{StateArg: ""})
	if len(in.NeedsGrant) != 1 {
		t.Fatalf("multiple refusals for one repo must fold to one row, got %+v", in.NeedsGrant)
	}
	r := in.NeedsGrant[0]
	if r.GrantState != "expired" || r.LastExpiredAt != late.UTC().Format(time.RFC3339) {
		t.Fatalf("dedup must keep the most-recent record, got %+v", r)
	}
}

// TestNeedsGrantEqualTimestampLastLogOrderWins pins the tie-break: two refusals
// for one repo sharing a (second-precision) timestamp must fold to the LATER
// log-order record, matching "most-recent wins" even when the clock can't
// distinguish them.
func TestNeedsGrantEqualTimestampLastLogOrderWins(t *testing.T) {
	same := inboxBase.Add(-time.Hour)
	arts := []state.Artifact{
		// Earlier in log order: absent. Later in log order: expired. Same timestamp.
		art(state.KindGrantNeeded, "run_a", "gnd_a", same, needed("o/r", "grant_absent", same)),
		art(state.KindGrantNeeded, "run_b", "gnd_b", same, needed("o/r", "grant_expired", same)),
	}
	in := buildInbox(arts, inboxBase, NextRequest{StateArg: ""})
	if len(in.NeedsGrant) != 1 {
		t.Fatalf("want one folded row, got %+v", in.NeedsGrant)
	}
	if in.NeedsGrant[0].GrantState != "expired" {
		t.Fatalf("on an equal timestamp the later log-order record must win, got %+v", in.NeedsGrant[0])
	}
}

// TestNeedsGrantAbsentHasNoExpiry pins that an absent grant reads grant_state
// "absent" and carries no last_expired_at — there was never an expiry to name.
func TestNeedsGrantAbsentHasNoExpiry(t *testing.T) {
	at := inboxBase.Add(-time.Hour)
	arts := []state.Artifact{
		art(state.KindGrantNeeded, "run_r1", "gnd_1", at, needed("o/r", "grant_absent", at)),
	}
	in := buildInbox(arts, inboxBase, NextRequest{StateArg: ""})
	if len(in.NeedsGrant) != 1 || in.NeedsGrant[0].GrantState != "absent" {
		t.Fatalf("want one absent row, got %+v", in.NeedsGrant)
	}
	if in.NeedsGrant[0].LastExpiredAt != "" {
		t.Fatalf("absent grant must carry no last_expired_at, got %q", in.NeedsGrant[0].LastExpiredAt)
	}
}

// TestSuggestedMintParsesToGrant pins that suggested_mint is a valid `gate grant`
// invocation and carries -state when the inbox read under an explicit state dir.
func TestSuggestedMintParsesToGrant(t *testing.T) {
	at := inboxBase.Add(-time.Hour)
	arts := []state.Artifact{
		art(state.KindGrantNeeded, "run_r1", "gnd_1", at, needed("o/r", "grant_expired", at)),
	}
	in := buildInbox(arts, inboxBase, NextRequest{StateArg: " -state /custom"})
	cmd := in.NeedsGrant[0].SuggestedMint
	for _, want := range []string{"gate grant ", "-repo o/r", "-action merge", "-max-tier T1", "-ttl 24h", "-state /custom"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("suggested_mint %q missing %q", cmd, want)
		}
	}
	// Ambient state dir omits -state, keeping the command short.
	in2 := buildInbox(arts, inboxBase, NextRequest{StateArg: ""})
	if strings.Contains(in2.NeedsGrant[0].SuggestedMint, "-state") {
		t.Fatalf("ambient state dir must omit -state, got %q", in2.NeedsGrant[0].SuggestedMint)
	}
}

// TestEnrichNeedsGrantBestEffort pins the live enrichment contract: a per-repo
// gh failure KEEPS the row with a nil OpenPRs (degrade, not drop — the lapsed
// grant is the primary signal), every reachable repo carries its open-PR count,
// and the projection as a whole never fails.
func TestEnrichNeedsGrantBestEffort(t *testing.T) {
	rows := []NeedsGrantRow{
		{Repo: "o/ok", GrantState: "expired"},
		{Repo: "o/broken", GrantState: "absent"},
		{Repo: "o/also-ok", GrantState: "expired"},
	}
	live := snapshot(
		map[string]map[int]LivePR{
			"o/ok":      {10: {State: "OPEN"}, 11: {State: "OPEN"}},
			"o/also-ok": {20: {State: "OPEN"}, 21: {State: "OPEN"}},
		},
		map[string]error{"o/broken": fmt.Errorf("gh unreachable")},
	)
	got := enrichNeedsGrant(rows, live)
	if len(got) != 3 {
		t.Fatalf("a per-repo failure must keep the row, not drop it, got %+v", got)
	}
	byRepo := make(map[string]NeedsGrantRow, len(got))
	for _, r := range got {
		byRepo[r.Repo] = r
	}
	broken, ok := byRepo["o/broken"]
	if !ok || broken.OpenPRs != nil {
		t.Fatalf("the failing repo must survive with nil open_prs, got %+v", broken)
	}
	for _, repo := range []string{"o/ok", "o/also-ok"} {
		r := byRepo[repo]
		if r.OpenPRs == nil || *r.OpenPRs != 2 {
			t.Fatalf("reachable row %s must carry its open-PR count, got %+v", repo, r)
		}
	}
}

// TestNeedsGrantRendersText pins the symmetric text section.
func TestNeedsGrantRendersText(t *testing.T) {
	two := 2
	in := Inbox{NeedsGrant: []NeedsGrantRow{
		{Repo: "o/r", GrantState: "expired", LastExpiredAt: "2026-07-19T08:00:00Z", OpenPRs: &two,
			SuggestedMint: "gate grant -repo o/r -action merge -max-tier T1 -ttl 24h"},
	}}
	var buf bytes.Buffer
	renderInbox(&buf, in)
	for _, want := range []string{"needs a grant (1)", "o/r  expired", "2 open PR(s)", "last expired 2026-07-19T08:00:00Z", "→ gate grant -repo o/r"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("text render missing %q\n---\n%s", want, buf.String())
		}
	}
}

// subjectVerdict builds a verdict artifact carrying a PR subject — the shape the
// ready-to-merge projection recovers repo/number/title/head_sha from, since a
// would_merge action body carries only the outcome and merge command.
func subjectVerdict(run, id string, at time.Time, repo string, number int, title, headSHA string) state.Artifact {
	return art(state.KindVerdict, run, id, at, map[string]any{
		"subject": map[string]any{"repo": repo, "number": number, "head_sha": headSHA},
		"data":    map[string]any{"title": title},
	})
}

// wouldMerge builds the would_merge action body main.go writes: the outcome plus
// the paste-ready merge command.
func wouldMerge(command string) map[string]any {
	return map[string]any{"outcome": "would_merge", "verdict": "vrd_x", "grant": "grt_x", "command": command, "dry_run": true}
}

// TestReadyToMergeBase pins the base projection: a run whose latest terminal is a
// would_merge action surfaces exactly one ready row carrying the subject facts
// and the action body's own merge command.
func TestReadyToMergeBase(t *testing.T) {
	cmd := "gh pr merge 142 -R o/widget --squash --match-head-commit deadbeef"
	arts := []state.Artifact{
		subjectVerdict("run_a", "vrd_a", inboxBase, "o/widget", 142, "fix the docket", "deadbeef"),
		art(state.KindAction, "run_a", "act_a", inboxBase.Add(time.Minute), wouldMerge(cmd)),
	}
	in := buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: ""})
	if len(in.ReadyToMerge) != 1 {
		t.Fatalf("want one ready row, got %+v", in.ReadyToMerge)
	}
	r := in.ReadyToMerge[0]
	if r.Run != "run_a" || r.Repo != "o/widget" || r.Number != 142 || r.Title != "fix the docket" || r.HeadSHA != "deadbeef" {
		t.Fatalf("ready row facts wrong: %+v", r)
	}
	if r.MergeCommand != cmd {
		t.Fatalf("merge_command = %q, want %q", r.MergeCommand, cmd)
	}
	if r.URL != "https://github.com/o/widget/pull/142" {
		t.Fatalf("canonical PR URL = %q", r.URL)
	}
}

// TestReadyToMergeSupersededByLaterTerminal pins the freshness core: a would_merge
// followed by a later terminal for the SAME subject (here a re-park) must drop —
// a stale ready card with a merge command for a since-reopened decision is the
// exact failure to avoid.
func TestReadyToMergeSupersededByLaterTerminal(t *testing.T) {
	arts := []state.Artifact{
		subjectVerdict("run_old", "vrd_old", inboxBase, "o/r", 7, "t", "sha1"),
		art(state.KindAction, "run_old", "act_old", inboxBase.Add(time.Minute), wouldMerge("gh pr merge 7 -R o/r")),
		// A newer run for the same PR parks — supersedes the earlier would_merge.
		art(state.KindEscalation, "run_new", "esc_new", inboxBase.Add(2*time.Minute), esc("grt_a", "re-parked", "grant_tier_exceeded", "o/r", 7)),
	}
	in := buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: ""})
	if len(in.ReadyToMerge) != 0 {
		t.Fatalf("a later terminal for the subject must supersede the would_merge, got %+v", in.ReadyToMerge)
	}
	if len(in.Parked) != 1 {
		t.Fatalf("the re-park should show as parked, got %+v", in.Parked)
	}
}

// TestReadyToMergeNewerWouldMergeWins pins that a newer would_merge run for the
// same PR replaces the older, keeping one row keyed to the latest run.
func TestReadyToMergeNewerWouldMergeWins(t *testing.T) {
	arts := []state.Artifact{
		subjectVerdict("run_old", "vrd_old", inboxBase, "o/r", 7, "t", "old"),
		art(state.KindAction, "run_old", "act_old", inboxBase.Add(time.Minute), wouldMerge("gh pr merge 7 -R o/r --match-head-commit old")),
		subjectVerdict("run_new", "vrd_new", inboxBase.Add(2*time.Minute), "o/r", 7, "t", "new"),
		art(state.KindAction, "run_new", "act_new", inboxBase.Add(3*time.Minute), wouldMerge("gh pr merge 7 -R o/r --match-head-commit new")),
	}
	in := buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: ""})
	if len(in.ReadyToMerge) != 1 || in.ReadyToMerge[0].Run != "run_new" {
		t.Fatalf("newest would_merge must win, got %+v", in.ReadyToMerge)
	}
	if in.ReadyToMerge[0].HeadSHA != "new" {
		t.Fatalf("newest run facts must win, got %+v", in.ReadyToMerge[0])
	}
}

// TestReadyToMergeNonWouldMergeTerminalExcluded pins that a subject whose latest
// terminal is any action OTHER than would_merge (blocked here) never appears.
func TestReadyToMergeNonWouldMergeTerminalExcluded(t *testing.T) {
	arts := []state.Artifact{
		subjectVerdict("run_b", "vrd_b", inboxBase, "o/r", 9, "t", "sha"),
		art(state.KindAction, "run_b", "act_b", inboxBase.Add(time.Minute), map[string]any{"outcome": "blocked"}),
	}
	in := buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: ""})
	if len(in.ReadyToMerge) != 0 {
		t.Fatalf("a blocked terminal must not appear in ready_to_merge, got %+v", in.ReadyToMerge)
	}
}

// TestReconcileReadyLiveDropsClosedKeepsOpen mirrors the parked reconcile law for
// ready rows: a merged/closed PR drops, an open one stays (enriched), a failed
// lookup stays visible.
func TestReconcileReadyLiveDropsClosedKeepsOpen(t *testing.T) {
	rows := []ReadyRow{
		{Run: "run_open", Repo: "o/r", Number: 1, Title: "stale", HeadSHA: "abc"},
		{Run: "run_absent", Repo: "o/r", Number: 2},
		{Run: "run_broken", Repo: "o/broken", Number: 3},
	}
	live := snapshot(
		map[string]map[int]LivePR{
			// Present + head matches the recorded sha — still ready, enriched.
			"o/r": {1: {State: "OPEN", Title: "live", HeadSHA: "abc", URL: "https://github.com/o/r/pull/1"}},
		},
		map[string]error{"o/broken": fmt.Errorf("lookup unavailable")},
	)
	got, _ := reconcileReadyLive(rows, live)
	if len(got) != 2 || got[0].Run != "run_open" || got[1].Run != "run_broken" {
		t.Fatalf("absent (merged/closed) must drop, open + failed-fetch must stay: %+v", got)
	}
	if got[0].Title != "live" || got[0].HeadSHA != "abc" {
		t.Fatalf("open ready row was not enriched: %+v", got[0])
	}
}

// TestReconcileReadyLiveDropsOnHeadMove pins the stale-command guard: an OPEN PR
// whose live head SHA has moved past the sha the would_merge command pins is
// DROPPED (the new head was never gated). A matching live sha stays, an empty
// live sha stays (ambiguous), and a failed lookup stays.
func TestReconcileReadyLiveDropsOnHeadMove(t *testing.T) {
	rows := []ReadyRow{
		{Run: "run_moved", Repo: "o/r", Number: 1, HeadSHA: "gated1", MergeCommand: "gh pr merge 1 --match-head-commit gated1"},
		{Run: "run_same", Repo: "o/r", Number: 2, HeadSHA: "gated2"},
		{Run: "run_emptylive", Repo: "o/r", Number: 3, HeadSHA: "gated3"},
	}
	live := snapshot(map[string]map[int]LivePR{
		"o/r": {
			1: {State: "OPEN", HeadSHA: "pushed1"}, // head moved past gated1
			2: {State: "OPEN", HeadSHA: "gated2"},  // matches
			3: {State: "OPEN"},                     // empty live sha — not a confirmation
		},
	}, nil)
	got, _ := reconcileReadyLive(rows, live)
	if len(got) != 2 || got[0].Run != "run_same" || got[1].Run != "run_emptylive" {
		t.Fatalf("a confirmed head move must drop; match + empty-live-sha must stay: %+v", got)
	}
}

// TestReadyToMergeJSONShape pins the ready_to_merge JSON field name and shape the
// console feed consumes.
func TestReadyToMergeJSONShape(t *testing.T) {
	arts := []state.Artifact{
		subjectVerdict("run_a", "vrd_a", inboxBase, "o/widget", 142, "fix", "deadbeef"),
		art(state.KindAction, "run_a", "act_a", inboxBase.Add(time.Minute), wouldMerge("gh pr merge 142 -R o/widget")),
	}
	raw, err := json.Marshal(buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: ""}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"ready_to_merge"`) || !strings.Contains(string(raw), `"merge_command"`) {
		t.Fatalf("ready_to_merge JSON shape wrong:\n%s", raw)
	}
	var in Inbox
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	if len(in.ReadyToMerge) != 1 || in.ReadyToMerge[0].MergeCommand != "gh pr merge 142 -R o/widget" {
		t.Fatalf("ready row round-trip wrong: %+v", in.ReadyToMerge)
	}
}

// TestReadyToMergeRendersText pins the symmetric text section.
func TestReadyToMergeRendersText(t *testing.T) {
	in := Inbox{ReadyToMerge: []ReadyRow{
		{Run: "run_a", Repo: "o/widget", Number: 142, Title: "fix the docket", HeadSHA: "deadbeef", MergeCommand: "gh pr merge 142 -R o/widget"},
	}}
	var buf bytes.Buffer
	renderInbox(&buf, in)
	for _, want := range []string{"ready to merge (1)", "o/widget#142  run_a", `"fix the docket"`, "head deadbeef", "→ gh pr merge 142 -R o/widget"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("text render missing %q\n---\n%s", want, buf.String())
		}
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
	if err := NextText(&buf, st, func() time.Time { return inboxBase }, NextRequest{}); err != nil {
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
	renderInbox(&buf, buildInbox(arts, inboxBase.Add(time.Hour), NextRequest{StateArg: ""}))
	out := buf.String()
	t.Logf("\n%s", out)

	for _, want := range []string{
		"awaiting judgment (1)",
		"acme/widget#142  run_9f3a41c2  grant_tier_exceeded",
		`"verdict tier T2 exceeds grant ceiling T1; flake is known"`,
		// A ceiling park advertises the derived escape, never a judge command:
		// judging under the same grant re-applies the ceiling.
		"the operator must mint a wider grant",
		"→ gate explain -run run_9f3a41c2\n",
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
	if err := NextJSON(&buf, st, func() time.Time { return inboxBase }, NextRequest{}); err != nil {
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
