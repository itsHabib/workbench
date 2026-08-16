package observe

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// fixedNow is the clock every preflight fixture reads, so grant expiry and
// coverage are evaluated against one instant.
func fixedNow() time.Time { return inboxBase.Add(time.Hour) }

// fetchFrom builds an OpenPRs seam over a static repo→PRs table, counting the
// calls per repo so the batching invariant (one fetch per repo, never per PR)
// can be asserted.
func fetchFrom(table map[string]map[int]LivePR, calls *sync.Map) OpenPRs {
	return func(repo string) (map[int]LivePR, error) {
		if calls != nil {
			n, _ := calls.LoadOrStore(repo, 0)
			calls.Store(repo, n.(int)+1)
		}
		prs, ok := table[repo]
		if !ok {
			return nil, errors.New("no such repo")
		}
		return prs, nil
	}
}

func protectFrom(table map[string]Protection) Protections {
	return func(repo string) (Protection, error) {
		p, ok := table[repo]
		if !ok {
			return Protection{}, errors.New("protection unreadable")
		}
		return p, nil
	}
}

func openPR(title string) LivePR {
	return LivePR{State: "OPEN", Title: title, HeadSHA: "sha", URL: "u"}
}

// req is a PreflightRequest with the fixture clock and no state arg.
func req(repos []string, fetch OpenPRs, protect Protections) PreflightRequest {
	return PreflightRequest{Repos: repos, Fetch: fetch, Protect: protect, Now: fixedNow}
}

// TestPreflightBatchesEveryMintUpFront is the surface's whole reason to exist:
// one pass over every repo in the sweep emits every mint the sweep will need, in
// one block, BEFORE any work starts. The fixture reproduces the recorded
// friction exactly — one repo with no grant at all (a clean, green PR that sat
// unmerged purely for want of one) and one repo whose live grant's default
// cycle ceiling the PR's review history has already spent.
func TestPreflightBatchesEveryMintUpFront(t *testing.T) {
	arts := []state.Artifact{
		// acme/covered has a live grant; #7 has consumed no cycles.
		art(state.KindGrant, "run_mint", "grt_covered", inboxBase, grant("acme/covered", inboxBase.Add(5*time.Hour))),
		subjectVerdict("run_c1", "vrd_c1", inboxBase, "acme/covered", 7, "fine", "sha"),
		// acme/spent has a live grant capped at 3 cycles, and #9 has already
		// burned three — the next gate run would ceiling-park.
		art(state.KindGrant, "run_mint2", "grt_spent", inboxBase, tieredGrant("acme/spent", "T1", 3, inboxBase.Add(5*time.Hour))),
		subjectVerdict("run_s1", "vrd_s1", inboxBase, "acme/spent", 9, "long review", "sha"),
		outcome(state.KindAction, "run_s1", "act_s1", "vrd_s1", inboxBase, map[string]any{"outcome": "would_merge"}),
		subjectVerdict("run_s2", "vrd_s2", inboxBase.Add(time.Minute), "acme/spent", 9, "long review", "sha"),
		outcome(state.KindAction, "run_s2", "act_s2", "vrd_s2", inboxBase.Add(time.Minute), map[string]any{"outcome": "would_merge"}),
		subjectVerdict("run_s3", "vrd_s3", inboxBase.Add(2*time.Minute), "acme/spent", 9, "long review", "sha"),
		outcome(state.KindAction, "run_s3", "act_s3", "vrd_s3", inboxBase.Add(2*time.Minute), map[string]any{"outcome": "would_merge"}),
	}
	// acme/ungranted has never had a grant and is not in the log at all — the
	// case `gate next` could not surface, because it recommends one PR at a time.
	table := map[string]map[int]LivePR{
		"acme/covered":   {7: openPR("covered PR")},
		"acme/spent":     {9: openPR("much-reviewed PR")},
		"acme/ungranted": {24: openPR("clean and green, no grant")},
	}
	prot := map[string]Protection{
		"acme/covered":   {Protected: false},
		"acme/spent":     {Protected: true, Strict: true, Contexts: []string{"ubuntu-latest"}, ConversationResolution: true},
		"acme/ungranted": {Protected: true, Strict: false, Contexts: []string{"Go"}},
	}
	var calls sync.Map
	scope := []string{"acme/covered", "acme/spent", "acme/ungranted"}

	p := buildPreflight(arts, req(scope, fetchFrom(table, &calls), protectFrom(prot)))

	if len(p.Repos) != 3 {
		t.Fatalf("want 3 repo plans, got %d: %+v", len(p.Repos), p.Repos)
	}
	// One fetch per repo — the inventory cost is O(repos), never O(PRs).
	for _, repo := range scope {
		n, _ := calls.Load(repo)
		if n != 1 {
			t.Errorf("%s fetched %v times, want 1", repo, n)
		}
	}
	byRepo := make(map[string]RepoPlan, len(p.Repos))
	for _, plan := range p.Repos {
		byRepo[plan.Repo] = plan
	}

	// The covered repo asks for nothing: suggesting a re-mint over a covering
	// grant trains the operator to ignore the block.
	if got := byRepo["acme/covered"].Mint; got != "" {
		t.Errorf("covered repo should need no mint, got %q", got)
	}
	// The spent repo's mint must clear the cycle it would next consume (4), not
	// the house default of 3 that already ceiling-parks it.
	spent := byRepo["acme/spent"]
	if !strings.Contains(spent.Mint, "-max-cycles 4") {
		t.Errorf("spent repo mint must widen cycles past the spent ceiling, got %q", spent.Mint)
	}
	if !strings.Contains(spent.Why, "#9") {
		t.Errorf("spent repo should name the PR forcing the mint, got %q", spent.Why)
	}
	if spent.PRs[0].Coverage.CyclesUsed != 3 || spent.PRs[0].Coverage.NextCycle != 4 {
		t.Errorf("historical cycles miscounted: %+v", spent.PRs[0].Coverage)
	}
	// The ungranted repo — the friction that started this — surfaces as absent.
	ungranted := byRepo["acme/ungranted"]
	if ungranted.PRs[0].Coverage.State != "absent" {
		t.Errorf("never-granted repo should read absent, got %+v", ungranted.PRs[0].Coverage)
	}
	if !strings.Contains(ungranted.Mint, "gate grant -repo acme/ungranted -action merge") {
		t.Errorf("ungranted repo needs a paste-ready mint, got %q", ungranted.Mint)
	}

	// The batch: exactly the two repos that need one, in repo order.
	want := []string{spent.Mint, ungranted.Mint}
	if len(p.Mints) != len(want) {
		t.Fatalf("want %d mints, got %v", len(want), p.Mints)
	}
	for i, m := range want {
		if p.Mints[i] != m {
			t.Errorf("mint %d = %q, want %q", i, p.Mints[i], m)
		}
	}
}

// TestPreflightMintNeverNarrowsALiveGrant pins that a re-mint composed for one
// PR can never lower a ceiling the repo's live grant already carries — a
// narrower grant would park PRs today's grant admits.
func TestPreflightMintNeverNarrowsALiveGrant(t *testing.T) {
	arts := []state.Artifact{
		// A wide cycle ceiling paired with a narrow tier one: the PR's T3 verdict
		// forces a mint, and the composed mint must keep the 9 cycles.
		art(state.KindGrant, "run_mint", "grt_wide", inboxBase, tieredGrant("acme/wide", "T1", 9, inboxBase.Add(5*time.Hour))),
		tieredVerdict("run_x", "vrd_x", inboxBase, "acme/wide", 3, "T3"),
	}
	table := map[string]map[int]LivePR{"acme/wide": {3: openPR("hot PR")}}

	p := buildPreflight(arts, req([]string{"acme/wide"}, fetchFrom(table, nil), nil))

	mint := p.Repos[0].Mint
	if !strings.Contains(mint, "-max-tier T3") {
		t.Errorf("mint must admit the composed verdict tier, got %q", mint)
	}
	if !strings.Contains(mint, "-max-cycles 9") {
		t.Errorf("mint must not narrow the live cycle ceiling of 9, got %q", mint)
	}
}

// TestPreflightDenylistExcludes pins the explicit denylist: a whole repo is
// dropped from scope before a network call is spent on it, a single PR is
// dropped from its repo's rows, and both are echoed back — a silently-skipped PR
// would read as a clean sweep.
func TestPreflightDenylistExcludes(t *testing.T) {
	table := map[string]map[int]LivePR{
		"acme/keep": {1: openPR("keep me"), 2: openPR("skip me")},
		"acme/drop": {5: openPR("whole repo excluded")},
	}
	var calls sync.Map
	r := req([]string{"acme/keep", "acme/drop"}, fetchFrom(table, &calls), nil)
	r.Deny = []string{"acme/drop", "acme/keep#2"}

	p := buildPreflight(nil, r)

	if len(p.Repos) != 1 || p.Repos[0].Repo != "acme/keep" {
		t.Fatalf("denylisted repo should leave scope: %+v", p.Repos)
	}
	if _, fetched := calls.Load("acme/drop"); fetched {
		t.Error("a denylisted repo must not be fetched at all")
	}
	if len(p.Repos[0].PRs) != 1 || p.Repos[0].PRs[0].Number != 1 {
		t.Fatalf("denylisted PR should be dropped: %+v", p.Repos[0].PRs)
	}
	if strings.Join(p.Excluded, ",") != "acme/drop,acme/keep#2" {
		t.Errorf("exclusions must be echoed back, got %v", p.Excluded)
	}
}

// TestPreflightDegradesOnUnreachableRepo pins the best-effort law observe holds
// everywhere: a repo whose PR list or protection cannot be read records the
// failure and keeps its row. The projection never fails as a whole — a sweep
// blinded by one unreachable repo is the state this surface exists to prevent.
func TestPreflightDegradesOnUnreachableRepo(t *testing.T) {
	table := map[string]map[int]LivePR{"acme/ok": {1: openPR("fine")}}
	p := buildPreflight(nil, req([]string{"acme/ok", "acme/gone"}, fetchFrom(table, nil), protectFrom(nil)))

	if len(p.Repos) != 2 {
		t.Fatalf("an unreachable repo must keep its row: %+v", p.Repos)
	}
	byRepo := make(map[string]RepoPlan, len(p.Repos))
	for _, plan := range p.Repos {
		byRepo[plan.Repo] = plan
	}
	if gone := byRepo["acme/gone"]; gone.PRsError == "" {
		t.Fatalf("unreachable repo should record its fetch error: %+v", gone)
	}
	if byRepo["acme/ok"].ProtectionError == "" {
		t.Error("an unreadable protection should be recorded, not silently dropped")
	}
}

// TestPreflightScopeDefaultsToKnownRepos pins the no-flag path: with no explicit
// -repo, the sweep's scope is every repo the log already names — grants minted,
// refusals recorded, PRs gated.
func TestPreflightScopeDefaultsToKnownRepos(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindGrant, "run_mint", "grt_a", inboxBase, grant("acme/granted", inboxBase.Add(5*time.Hour))),
		art(state.KindGrantNeeded, "run_r", "gnd_a", inboxBase, map[string]any{"repo": "acme/refused", "reason": "grant_absent"}),
		subjectVerdict("run_v", "vrd_v", inboxBase, "acme/gated", 4, "t", "sha"),
	}
	table := map[string]map[int]LivePR{
		"acme/granted": {1: openPR("a")},
		"acme/refused": {2: openPR("b")},
		"acme/gated":   {4: openPR("c")},
	}

	p := buildPreflight(arts, req(nil, fetchFrom(table, nil), nil))

	var got []string
	for _, plan := range p.Repos {
		got = append(got, plan.Repo)
	}
	want := "acme/gated,acme/granted,acme/refused"
	if strings.Join(got, ",") != want {
		t.Errorf("default scope = %v, want %s", got, want)
	}
}

// TestPreflightTextRendersTheOperatorBlock pins the human layout: per-repo
// protection and cycles, then the mint batch last, labelled operator-only. The
// batch block is the deliverable — it is what replaces being interrupted per
// repo mid-sweep.
func TestPreflightTextRendersTheOperatorBlock(t *testing.T) {
	arts := []state.Artifact{
		tieredVerdict("run_x", "vrd_x", inboxBase, "acme/strict", 12, "T2"),
	}
	table := map[string]map[int]LivePR{"acme/strict": {12: openPR("needs a grant")}}
	prot := map[string]Protection{
		"acme/strict": {Protected: true, Strict: true, Contexts: []string{"ubuntu-latest", "windows-latest"}, ConversationResolution: true},
	}

	var buf bytes.Buffer
	renderPreflight(&buf, buildPreflight(arts, req([]string{"acme/strict"}, fetchFrom(table, nil), protectFrom(prot))))
	out := buf.String()
	t.Logf("\n%s", out)

	for _, want := range []string{
		"acme/strict (1 open PR(s))",
		"protection: STRICT — a BEHIND PR costs refresh + CI re-run + a fresh gate judgment",
		"checks ubuntu-latest, windows-latest",
		"conversation resolution required",
		`#12  "needs a grant"`,
		"cycles used 0, next 1",
		"grant absent:",
		"mint before the sweep (1) — operator only; gate never runs these",
		"gate grant -repo acme/strict -action merge -max-tier T2 -max-cycles 3 -ttl 24h",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered preflight missing %q\n---\n%s", want, out)
		}
	}
}

// TestPreflightTextCoveredSweepSaysSo pins the quiet path: a fully covered sweep
// prints an explicit all-clear rather than an empty block the operator has to
// interpret.
func TestPreflightTextCoveredSweepSaysSo(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindGrant, "run_mint", "grt_a", inboxBase, grant("acme/ok", inboxBase.Add(5*time.Hour))),
	}
	table := map[string]map[int]LivePR{"acme/ok": {1: openPR("fine")}}

	var buf bytes.Buffer
	renderPreflight(&buf, buildPreflight(arts, req([]string{"acme/ok"}, fetchFrom(table, nil), nil)))

	if !strings.Contains(buf.String(), "every repo in scope is covered — no mints needed.") {
		t.Errorf("a covered sweep should say so:\n%s", buf.String())
	}
}

// TestPreflightJSONOverStore drives the full read path — store scan →
// projection → JSON — over a store built with the real state API, the shape a
// sweep driver would branch on.
func TestPreflightJSONOverStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	st, err := state.Open(dir, func() time.Time { return inboxBase })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(state.KindGrantNeeded, "run_r", nil, map[string]any{"repo": "o/r", "reason": "grant_absent"}); err != nil {
		t.Fatal(err)
	}
	table := map[string]map[int]LivePR{"o/r": {42: openPR("needs a grant")}}
	r := req(nil, fetchFrom(table, nil), nil)
	r.StateArg = " -state /mnt/gate"

	var buf bytes.Buffer
	if err := PreflightJSON(&buf, st, r); err != nil {
		t.Fatal(err)
	}
	var p Preflight
	if err := json.Unmarshal(buf.Bytes(), &p); err != nil {
		t.Fatalf("preflight -json is not valid Preflight JSON: %v\n%s", err, buf.Bytes())
	}
	if len(p.Mints) != 1 || !strings.Contains(p.Mints[0], "gate grant -state /mnt/gate -repo o/r") {
		t.Fatalf("mint must target the state dir this preflight read: %v", p.Mints)
	}
	if len(p.Repos) != 1 || len(p.Repos[0].PRs) != 1 || p.Repos[0].PRs[0].Number != 42 {
		t.Fatalf("inventory wrong: %+v", p.Repos)
	}
}
