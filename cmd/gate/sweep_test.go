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
	// The work list is the LIVE rows, so after the first sweep there is nothing
	// left to check — which is why this assertion cannot reach the duplicate
	// path and why TestSweepReportsAWrappedDuplicate exists.
	if second.Checked != 0 {
		t.Fatalf("a swept queue leaves nothing live to re-check, got %d", second.Checked)
	}
}

// TestSweepReportsAWrappedDuplicate reaches the path a repeated single-process
// sweep cannot: two sweeps racing on the SAME live terminal, where the second
// append is refused by the absent-parent guard.
//
// The store wraps its sentinel (`fmt.Errorf("%w: run %s kind %s", ...)`), so a
// direct `err == state.ErrAlreadyExists` silently never fires and a correctly
// deduplicated concurrent sweep reports as a hard failure instead. Calling
// recordClosed twice on one terminal is the smallest thing that exercises it.
func TestSweepReportsAWrappedDuplicate(t *testing.T) {
	st := sweepStore(t)
	subjects := observe.LiveSubjects(mustList(t, st))
	if len(subjects) == 0 {
		t.Fatal("fixture must have a live subject")
	}
	s := subjects[0]

	if _, err := recordClosed(st, s, clock, false); err != nil {
		t.Fatal(err)
	}
	again, err := recordClosed(st, s, clock, false)
	if err != nil {
		t.Fatalf("a duplicate closure must be reported, not returned as an error: %v", err)
	}
	if !again.Already {
		t.Fatalf("want the duplicate reported as already recorded, got %+v", again)
	}
}

// TestSweepDeclinesAClosureForARegatedSubject pins the read-to-append race the
// absent-parent guard cannot see. The GitHub fetch takes seconds; a PR reopened
// and re-gated inside that window gets a NEW terminal, and the sweep's closure
// would then land AFTER it in the log — mooting the fresh park, because a
// closing fact settles by log order. The absent-parent guard allows it, being
// keyed on the OLD terminal.
//
// The revalidation runs inside the store lock, so no terminal can land between
// the check and the append.
func TestSweepDeclinesAClosureForARegatedSubject(t *testing.T) {
	st := sweepStore(t)
	subjects := observe.LiveSubjects(mustList(t, st))
	var park observe.LiveSubject
	for _, s := range subjects {
		if s.Repo == "o/widget" {
			park = s
		}
	}
	if park.Terminal == "" {
		t.Fatal("fixture must have a live park for o/widget")
	}

	// The PR is reopened and re-gated while the fetch is in flight.
	regate := map[string]any{
		"outcome": "parked_for_judgment", "verdict": "vrd_y", "grant": "grt_a",
		"question": "re-gated", "repo": "o/widget", "number": 7,
	}
	if _, err := st.Append(state.KindEscalation, "run_regate", nil, regate); err != nil {
		t.Fatal(err)
	}

	got, err := recordClosed(st, park, clock, false)
	if err != nil {
		t.Fatalf("a re-gated subject is a normal outcome, not an error: %v", err)
	}
	if !got.Regated || got.Artifact != "" {
		t.Fatalf("want the closure declined and reported, got %+v", got)
	}

	// The whole point: the fresh park must still be live and still sweepable.
	in, err := observe.NextInbox(st, clock, observe.NextRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Parked) != 1 || in.Parked[0].Run != "run_regate" {
		t.Fatalf("the re-gated park must survive the in-flight sweep, got %+v (discharged %+v)",
			in.Parked, in.Discharged.Parked)
	}
}

func mustList(t *testing.T, st *state.Store) []state.Artifact {
	t.Helper()
	arts, err := st.List(nil)
	if err != nil {
		t.Fatal(err)
	}
	return arts
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
