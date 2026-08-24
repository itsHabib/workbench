package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/observe"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

var sweepBase = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

// sweepStore builds a store holding one park for widget#7 and one ready-to-merge
// authorization for api#12 — the two live shapes a sweep can close.
func sweepStore(t *testing.T) *state.Store {
	t.Helper()
	st, err := state.Open(t.TempDir(), func() time.Time { return sweepBase })
	if err != nil {
		t.Fatal(err)
	}
	park := map[string]any{
		"outcome": "parked_for_judgment", "verdict": "vrd_x", "grant": "grt_a",
		"question": "needs judgment", "repo": "o/widget", "number": 7,
	}
	if _, err := st.Append(state.KindEscalation, "run_park", nil, park); err != nil {
		t.Fatal(err)
	}
	ready := map[string]any{
		"outcome": "would_merge", "dry_run": true, "command": "gh pr merge 12",
		"subject": map[string]any{"repo": "o/api", "number": 12, "head_sha": "abc123"},
	}
	if _, err := st.Append(state.KindAction, "run_ready", nil, ready); err != nil {
		t.Fatal(err)
	}
	return st
}

// noneOpen is an open-PR seam reporting every repo as having no open PRs.
func noneOpen(string) (map[int]observe.LivePR, error) { return map[int]observe.LivePR{}, nil }

func clock() time.Time { return sweepBase }

// TestSweepClosesTheGhostQueue is the end-to-end shape of the operator's bug: an
// inbox holding rows whose pull requests are long gone. One sweep records the
// closures, and the OFFLINE projection — no network, the path escalate serve
// shells under a hard budget — is correct from then on.
func TestSweepClosesTheGhostQueue(t *testing.T) {
	st := sweepStore(t)

	before, err := observe.NextInbox(st, clock, observe.NextRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Parked) != 1 || len(before.ReadyToMerge) != 1 {
		t.Fatalf("fixture must start with one live row on each surface, got %d parked / %d ready",
			len(before.Parked), len(before.ReadyToMerge))
	}

	res, err := runSweep(st, noneOpen, clock, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Checked != 2 || len(res.Closed) != 2 {
		t.Fatalf("want 2 subjects checked and closed, got %+v", res)
	}

	after, err := observe.NextInbox(st, clock, observe.NextRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Parked) != 0 || len(after.ReadyToMerge) != 0 {
		t.Fatalf("the offline inbox must be empty after the sweep, got %d parked / %d ready",
			len(after.Parked), len(after.ReadyToMerge))
	}
	if after.Discharged.Parked.Moot != 1 || after.Discharged.ReadyToMerge.Moot != 1 {
		t.Fatalf("both rows must be counted moot, got %+v", after.Discharged)
	}
}

// TestSweepIsIdempotent pins that the closure is keyed on the terminal it
// discharges, so re-running a sweep records nothing new. The store's
// absent-parent guard is what makes that structural rather than a convention,
// and an append-only hash-chained log has no other way to be re-runnable.
func TestSweepIsIdempotent(t *testing.T) {
	st := sweepStore(t)

	if _, err := runSweep(st, noneOpen, clock, false); err != nil {
		t.Fatal(err)
	}
	firstLen := countArtifacts(t, st)

	second, err := runSweep(st, noneOpen, clock, false)
	if err != nil {
		t.Fatal(err)
	}
	if n := countArtifacts(t, st); n != firstLen {
		t.Fatalf("a repeated sweep wrote %d new artifact(s)", n-firstLen)
	}
	for _, c := range second.Closed {
		if !c.Already {
			t.Fatalf("a repeated sweep must report each closure as already recorded, got %+v", c)
		}
	}
}

// TestSweepLeavesAnUnreadRepoAlone pins the safe direction. A failed read is not
// evidence of closure, and treating it as one would delete the operator's queue
// on a network blip — the same failure this change exists to fix, inverted.
func TestSweepLeavesAnUnreadRepoAlone(t *testing.T) {
	st := sweepStore(t)
	failWidget := func(repo string) (map[int]observe.LivePR, error) {
		if repo == "o/widget" {
			return nil, errors.New("gh: network unreachable")
		}
		return map[int]observe.LivePR{}, nil
	}

	res, err := runSweep(st, failWidget, clock, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Closed) != 1 || res.Closed[0].Repo != "o/api" {
		t.Fatalf("only the readable repo may be closed, got %+v", res.Closed)
	}
	if len(res.Unreadable) != 1 || res.Unreadable[0].Repo != "o/widget" {
		t.Fatalf("the unread repo must be reported, got %+v", res.Unreadable)
	}

	after, err := observe.NextInbox(st, clock, observe.NextRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Parked) != 1 {
		t.Fatalf("the unread repo's park must survive, got %+v", after.Parked)
	}
}

// TestSweepDryRunWritesNothing pins that -dry-run reports without recording.
func TestSweepDryRunWritesNothing(t *testing.T) {
	st := sweepStore(t)
	before := countArtifacts(t, st)

	res, err := runSweep(st, noneOpen, clock, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Closed) != 2 || !res.DryRun {
		t.Fatalf("a dry run still reports what it would close, got %+v", res)
	}
	if n := countArtifacts(t, st); n != before {
		t.Fatalf("a dry run wrote %d artifact(s)", n-before)
	}
}

// TestSweepSkipsAnOpenPR pins the obvious negative: a PR still on the open list
// is left exactly as it was.
func TestSweepSkipsAnOpenPR(t *testing.T) {
	st := sweepStore(t)
	widgetOpen := func(repo string) (map[int]observe.LivePR, error) {
		if repo == "o/widget" {
			return map[int]observe.LivePR{7: {State: "OPEN", HeadSHA: "abc"}}, nil
		}
		return map[int]observe.LivePR{}, nil
	}

	res, err := runSweep(st, widgetOpen, clock, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Closed) != 1 || res.Closed[0].Repo != "o/api" {
		t.Fatalf("an open PR must not be closed, got %+v", res.Closed)
	}
}

// TestSweepRecordsOnlyWhatItObserved pins the honesty of the artifact: an
// open-PR list proves a PR is NOT OPEN and cannot say whether it merged or was
// abandoned. Recording a merge gate never read would invent the very fact the
// receipt/reconcile path exists to establish properly, with the platform's own
// clock and actor.
func TestSweepRecordsOnlyWhatItObserved(t *testing.T) {
	st := sweepStore(t)
	if _, err := runSweep(st, noneOpen, clock, false); err != nil {
		t.Fatal(err)
	}

	arts, err := st.List(nil)
	if err != nil {
		t.Fatal(err)
	}
	var found int
	for _, a := range arts {
		if a.Kind != state.KindSubjectClosed {
			continue
		}
		found++
		assertObservationOnly(t, string(a.Body))
	}
	if found != 2 {
		t.Fatalf("want 2 closure artifacts, got %d", found)
	}
}

func assertObservationOnly(t *testing.T, body string) {
	t.Helper()
	if !strings.Contains(body, `"state":"`+observe.ClosedNotOpen+`"`) {
		t.Fatalf("closure must record the observed state, got %s", body)
	}
	if !strings.Contains(body, `"source":"`+sweepSource+`"`) {
		t.Fatalf("closure must name its basis, got %s", body)
	}
	for _, claim := range []string{`"merge_commit"`, `"actor"`, `"merged_at"`} {
		if strings.Contains(body, claim) {
			t.Fatalf("a sweep must not claim %s, a landing fact it never read: %s", claim, body)
		}
	}
}

func countArtifacts(t *testing.T, st *state.Store) int {
	t.Helper()
	arts, err := st.List(nil)
	if err != nil {
		t.Fatal(err)
	}
	return len(arts)
}
