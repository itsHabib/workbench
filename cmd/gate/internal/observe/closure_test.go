package observe

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// action builds an action artifact body.
func closureAction(outcome string) map[string]any {
	return map[string]any{"outcome": outcome, "repo": "", "number": 0}
}

// wouldMergeFor builds a ready-to-merge action that carries its own subject, so
// a closure test needs no separate verdict artifact to attribute it.
func wouldMergeFor(repo string, number int, command string) map[string]any {
	return map[string]any{
		"outcome": "would_merge", "command": command, "dry_run": true,
		"subject": map[string]any{"repo": repo, "number": number, "head_sha": "abc123def456"},
	}
}

func closureInbox(t *testing.T, arts []state.Artifact, req NextRequest) Inbox {
	t.Helper()
	return buildInbox(arts, inboxBase.Add(time.Hour), req)
}

// TestSupersededParkIsDischargedNotDropped pins the first half of the closure
// law: when a PR is gated more than once, only the newest run's terminal is the
// subject's state — and the parks it displaced are RETAINED as discharged rows
// rather than vanishing. Before this, a superseded park was silently dropped, so
// a shrinking queue and a lost queue looked identical.
func TestSupersededParkIsDischargedNotDropped(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindEscalation, "run_1", "esc_1", inboxBase, esc("grt_a", "first", "", "o/widget", 7)),
		art(state.KindEscalation, "run_2", "esc_2", inboxBase.Add(time.Minute), esc("grt_a", "second", "", "o/widget", 7)),
	}

	in := closureInbox(t, arts, NextRequest{IncludeDischarged: true})

	if len(in.Parked) != 1 || in.Parked[0].Run != "run_2" {
		t.Fatalf("only the newest park is live, got %+v", in.Parked)
	}
	if in.Discharged.Parked.Superseded != 1 {
		t.Fatalf("want 1 superseded park counted, got %+v", in.Discharged.Parked)
	}
	rows := in.DischargedRows.Parked
	if len(rows) != 1 || rows[0].Run != "run_1" || rows[0].Discharge != DischargeSuperseded {
		t.Fatalf("the displaced park must be retained as superseded, got %+v", rows)
	}
	if !strings.Contains(rows[0].DischargeWhy, "run_2") {
		t.Fatalf("a discharged row must name what displaced it, got %q", rows[0].DischargeWhy)
	}
}

// TestDischargedParkOffersNoJudgeCommand pins the safety property that makes
// discharge worth having: a settled park must never hand back a paste-ready
// `gate judge`. The judgment it would spend is one-shot, and spending it on a
// question the log has already answered is the exact waste this closes.
func TestDischargedParkOffersNoJudgeCommand(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindEscalation, "run_1", "esc_1", inboxBase, esc("grt_a", "first", "", "o/widget", 7)),
		art(state.KindEscalation, "run_2", "esc_2", inboxBase.Add(time.Minute), esc("grt_a", "second", "", "o/widget", 7)),
	}

	in := closureInbox(t, arts, NextRequest{IncludeDischarged: true})

	row := in.DischargedRows.Parked[0]
	if row.JudgeCommand != "" || row.ResolveCommand != "" || row.Escape != nil {
		t.Fatalf("a discharged park must carry nothing to act on, got %+v", row)
	}
	if in.Parked[0].JudgeCommand == "" {
		t.Fatalf("a LIVE park must keep its judge command, got %+v", in.Parked[0])
	}
}

// TestAlreadyMergedRefusalMootsThePark pins the one closing fact gate already
// produces on its own: a run refused because the PR had merged.
func TestAlreadyMergedRefusalMootsThePark(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindEscalation, "run_1", "esc_1", inboxBase, esc("grt_a", "needs judgment", "", "o/widget", 7)),
		art(state.KindEvidence, "run_2", "evd_2", inboxBase.Add(time.Minute), map[string]any{"repo": "o/widget", "number": 7}),
		art(state.KindAction, "run_2", "act_2", inboxBase.Add(2*time.Minute), closureAction(outcomeAlreadyMerged)),
	}

	in := closureInbox(t, arts, NextRequest{IncludeDischarged: true})

	if len(in.Parked) != 0 {
		t.Fatalf("a merged PR's park is not actionable, got %+v", in.Parked)
	}
	if in.Discharged.Parked.Superseded != 1 {
		t.Fatalf("the park was displaced by the refusal run, so it is superseded: %+v", in.Discharged.Parked)
	}
}

// TestSubjectClosedMootsTheCurrentPark pins the sweep's own closing fact against
// a park that nothing superseded — the shape that produced the operator's ghost
// queue, where the newest terminal IS the park and the PR is simply gone.
func TestSubjectClosedMootsTheCurrentPark(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindEscalation, "run_1", "esc_1", inboxBase, esc("grt_a", "needs judgment", "", "o/widget", 7)),
		art(state.KindSubjectClosed, "run_1", "sbc_1", inboxBase.Add(time.Hour), map[string]any{
			"repo": "o/widget", "number": 7, "state": ClosedNotOpen,
			"observed_at": inboxBase.Add(time.Hour).Format(time.RFC3339), "source": "gh-open-pr-list",
		}),
	}

	in := closureInbox(t, arts, NextRequest{IncludeDischarged: true})

	if len(in.Parked) != 0 {
		t.Fatalf("a closed PR's park is not actionable, got %+v", in.Parked)
	}
	if in.Discharged.Parked.Moot != 1 || in.Discharged.Parked.Superseded != 0 {
		t.Fatalf("want exactly one MOOT park, got %+v", in.Discharged.Parked)
	}
	if why := in.DischargedRows.Parked[0].DischargeWhy; !strings.Contains(why, "no longer open") {
		t.Fatalf("the row must say what was observed, got %q", why)
	}
}

// TestReadyRowSurvivesItsOwnMergeUntilAClosingFact is the operator's actual bug,
// pinned. Every action gate writes is dry_run/would_merge, so a ready row has no
// way to learn the PR landed: it stood forever recommending a merge command for
// a dead PR. 150 of the 164 rows in the live inbox were this.
func TestReadyRowSurvivesItsOwnMergeUntilAClosingFact(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindAction, "run_1", "act_1", inboxBase, wouldMergeFor("o/widget", 7, "gh pr merge 7 --match-head-commit abc123def456")),
	}

	before := closureInbox(t, arts, NextRequest{})
	if len(before.ReadyToMerge) != 1 {
		t.Fatalf("a fresh authorization is ready to merge, got %+v", before.ReadyToMerge)
	}

	arts = append(arts, art(state.KindSubjectClosed, "run_1", "sbc_1", inboxBase.Add(time.Hour), map[string]any{
		"repo": "o/widget", "number": 7, "state": ClosedNotOpen,
		"observed_at": inboxBase.Add(time.Hour).Format(time.RFC3339), "source": "gh-open-pr-list",
	}))

	after := closureInbox(t, arts, NextRequest{IncludeDischarged: true})
	if len(after.ReadyToMerge) != 0 {
		t.Fatalf("a landed PR is not still ready to merge, got %+v", after.ReadyToMerge)
	}
	if after.Discharged.ReadyToMerge.Moot != 1 {
		t.Fatalf("want the ready row counted moot, got %+v", after.Discharged.ReadyToMerge)
	}
}

// TestClosureReadsReceiptAndCoverage pins the exact PR #249 body shapes this
// projection decodes. It is the seam that catches drift: if #249's receipt or
// coverage body changes before it lands, this fails loudly rather than silently
// emptying the moot class and restoring the ghost queue.
func TestClosureReadsReceiptAndCoverage(t *testing.T) {
	cases := []struct {
		name string
		art  state.Artifact
		want bool
	}{
		{"receipt merged", art(kindReceipt, "run_r", "rcp_1", inboxBase, map[string]any{
			"schema_version": "receipt.v1", "repo": "o/widget", "number": 7,
			"action": "act_1", "outcome": "merged", "merge_commit": "deadbeef", "actor": "someone",
		}), true},
		{"receipt superseded", art(kindReceipt, "run_r", "rcp_2", inboxBase, map[string]any{
			"repo": "o/widget", "number": 7, "outcome": "superseded",
		}), true},
		{"receipt abandoned", art(kindReceipt, "run_r", "rcp_3", inboxBase, map[string]any{
			"repo": "o/widget", "number": 7, "outcome": "abandoned",
		}), true},
		// A merge command that ran and did not land leaves the PR OPEN. Treating
		// it as closed would hide a row that still needs the operator.
		{"receipt failed leaves the PR open", art(kindReceipt, "run_r", "rcp_4", inboxBase, map[string]any{
			"repo": "o/widget", "number": 7, "outcome": "failed", "why": "merge refused",
		}), false},
		{"coverage authorized_and_landed", art(kindCoverage, "run_c", "cov_1", inboxBase, map[string]any{
			"schema_version": "coverage.v1", "repo": "o/widget", "basis": "merged-pull-requests",
			"authorized_and_landed": []map[string]any{{"number": 7, "merged_at": "2026-08-01T00:00:00Z"}},
		}), true},
		{"coverage landed_without_authorization", art(kindCoverage, "run_c", "cov_2", inboxBase, map[string]any{
			"repo": "o/widget", "landed_without_authorization": []map[string]any{{"number": 7}},
		}), true},
		{"coverage pre_adoption", art(kindCoverage, "run_c", "cov_3", inboxBase, map[string]any{
			"repo": "o/widget", "pre_adoption": []map[string]any{{"number": 7}},
		}), true},
		// authorized_never_landed is a list of AUTHORIZATIONS, not of merges: the
		// PR on it may well still be open and still need the operator.
		{"coverage authorized_never_landed is not a closing fact", art(kindCoverage, "run_c", "cov_4", inboxBase, map[string]any{
			"repo": "o/widget", "authorized_never_landed": []map[string]any{{"number": 7}},
		}), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			arts := []state.Artifact{
				art(state.KindEscalation, "run_1", "esc_1", inboxBase, esc("grt_a", "q", "", "o/widget", 7)),
				tc.art,
			}
			in := closureInbox(t, arts, NextRequest{})
			moot := in.Discharged.Parked.Moot == 1
			if moot != tc.want {
				t.Fatalf("closing fact recognised=%v, want %v (parked=%d discharged=%+v)",
					moot, tc.want, len(in.Parked), in.Discharged.Parked)
			}
		})
	}
}

// TestDischargedRowsHiddenUnlessAsked pins the default: the counts always
// project, the rows only on request. Both halves matter — hiding the count is
// how a 164-row queue got quoted as real work, and showing 161 dead rows by
// default is the surface it replaces.
func TestDischargedRowsHiddenUnlessAsked(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindEscalation, "run_1", "esc_1", inboxBase, esc("grt_a", "first", "", "o/widget", 7)),
		art(state.KindEscalation, "run_2", "esc_2", inboxBase.Add(time.Minute), esc("grt_a", "second", "", "o/widget", 7)),
	}

	in := closureInbox(t, arts, NextRequest{})
	if in.DischargedRows != nil {
		t.Fatalf("discharged rows must stay hidden by default, got %+v", in.DischargedRows)
	}
	if in.Discharged.Parked.Superseded != 1 {
		t.Fatalf("the COUNT must project regardless, got %+v", in.Discharged.Parked)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"discharged"`) {
		t.Fatalf("the discharged summary must always be on the wire: %s", raw)
	}
	if strings.Contains(string(raw), `"discharged_rows"`) {
		t.Fatalf("the rows must be omitted when not asked for: %s", raw)
	}
}

// TestLiveSubjectsIsTheSweepWorkList pins that the sweep's work list is exactly
// the rows the inbox still considers live — never the discharged ones, whose
// cost would otherwise grow with history rather than with the queue.
func TestLiveSubjectsIsTheSweepWorkList(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindEscalation, "run_1", "esc_1", inboxBase, esc("grt_a", "first", "", "o/widget", 7)),
		art(state.KindEscalation, "run_2", "esc_2", inboxBase.Add(time.Minute), esc("grt_a", "second", "", "o/widget", 7)),
		art(state.KindAction, "run_3", "act_3", inboxBase.Add(2*time.Minute), wouldMergeFor("o/api", 12, "gh pr merge 12")),
		// Already discharged: nothing to sweep here.
		art(state.KindAction, "run_4", "act_4", inboxBase.Add(3*time.Minute), wouldMergeFor("o/api", 13, "gh pr merge 13")),
		art(state.KindSubjectClosed, "run_4", "sbc_4", inboxBase.Add(4*time.Minute), map[string]any{
			"repo": "o/api", "number": 13, "state": ClosedNotOpen,
		}),
	}

	got := LiveSubjects(arts)

	want := []LiveSubject{
		{Repo: "o/api", Number: 12, Run: "run_3", Terminal: "act_3"},
		{Repo: "o/widget", Number: 7, Run: "run_2", Terminal: "esc_2"},
	}
	if len(got) != len(want) {
		t.Fatalf("want %d live subjects, got %d: %+v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("live subject %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestLiveReadDistinguishesMootFromStale pins that the live reconcile's two
// drop reasons are not the same fact. A PR absent from the open set is finished;
// a PR whose head moved past the authorized SHA is still open and still needs a
// re-gate. Counting the second as moot would report owed work as done.
func TestLiveReadDistinguishesMootFromStale(t *testing.T) {
	rows := []ReadyRow{
		{Run: "run_1", Repo: "o/widget", Number: 7, HeadSHA: "aaaa", MergeCommand: "gh pr merge 7"},
		{Run: "run_2", Repo: "o/widget", Number: 8, HeadSHA: "bbbb", MergeCommand: "gh pr merge 8"},
		{Run: "run_3", Repo: "o/widget", Number: 9, HeadSHA: "cccc", MergeCommand: "gh pr merge 9"},
	}
	live := liveRepos{open: map[string]map[int]LivePR{"o/widget": {
		8: {State: "OPEN", HeadSHA: "zzzz"}, // head moved → stale
		9: {State: "OPEN", HeadSHA: "cccc"}, // unchanged → live
	}}, errs: map[string]error{}}

	out, discharged := reconcileReadyLive(rows, live)

	if len(out) != 1 || out[0].Run != "run_3" {
		t.Fatalf("only the unchanged head stays ready, got %+v", out)
	}
	byRun := map[string]ReadyRow{}
	for _, r := range discharged {
		byRun[r.Run] = r
	}
	if byRun["run_1"].Discharge != DischargeMoot {
		t.Fatalf("a PR absent from the open set is moot, got %q", byRun["run_1"].Discharge)
	}
	if byRun["run_2"].Discharge != DischargeStale {
		t.Fatalf("a moved head is stale, not moot, got %q", byRun["run_2"].Discharge)
	}
	if !strings.Contains(byRun["run_2"].DischargeWhy, "zzzz") {
		t.Fatalf("a stale row must name the head it found, got %q", byRun["run_2"].DischargeWhy)
	}
}

// TestUnreadRepoKeepsItsRows pins the safe direction of the live read: a repo
// whose fetch failed is UNKNOWN, never closed. Assuming closure on a failed read
// would delete the operator's queue on a network blip.
func TestUnreadRepoKeepsItsRows(t *testing.T) {
	parked := []ParkedRun{{Run: "run_1", Repo: "o/widget", Number: 7}}
	live := liveRepos{open: map[string]map[int]LivePR{}, errs: map[string]error{"o/widget": errFetch}}

	out, discharged := reconcileLive(parked, live)

	if len(out) != 1 || out[0].PRState != "unknown" {
		t.Fatalf("an unread repo's rows stay visible as unknown, got %+v", out)
	}
	if len(discharged) != 0 {
		t.Fatalf("an unread repo discharges nothing, got %+v", discharged)
	}
}

// errFetch stands in for a failed open-PR read.
var errFetch = errors.New("gh: network unreachable")

// TestReopenedPRIsNotMootedByItsOldClosure pins the ordering rule, and it is the
// bug this reduction would otherwise have introduced while claiming to fix its
// mirror image. A pull request can be closed and REOPENED — this repo's own
// review-cycle rule says a PR past its cap is "closed and re-opened fresh" — and
// the re-gated PR parks again AFTER the closure was recorded. A closing fact
// that settled a terminal it predates would hide that live park forever.
func TestReopenedPRIsNotMootedByItsOldClosure(t *testing.T) {
	closure := art(state.KindSubjectClosed, "run_1", "sbc_1", inboxBase.Add(time.Hour), map[string]any{
		"repo": "o/widget", "number": 7, "state": ClosedNotOpen,
	})

	t.Run("park after the closure stays live", func(t *testing.T) {
		arts := []state.Artifact{
			art(state.KindAction, "run_1", "act_1", inboxBase, wouldMergeFor("o/widget", 7, "x")),
			closure,
			art(state.KindEscalation, "run_2", "esc_2", inboxBase.Add(2*time.Hour), esc("grt_a", "needs judgment", "", "o/widget", 7)),
		}
		in := closureInbox(t, arts, NextRequest{})
		if len(in.Parked) != 1 {
			t.Fatalf("a reopened PR's fresh park must be live, got %d parked (%+v)", len(in.Parked), in.Discharged.Parked)
		}
	})

	t.Run("authorization after the closure stays ready", func(t *testing.T) {
		arts := []state.Artifact{
			art(state.KindEscalation, "run_1", "esc_1", inboxBase, esc("grt_a", "q", "", "o/widget", 7)),
			closure,
			art(state.KindAction, "run_2", "act_2", inboxBase.Add(2*time.Hour), wouldMergeFor("o/widget", 7, "x")),
		}
		in := closureInbox(t, arts, NextRequest{})
		if len(in.ReadyToMerge) != 1 {
			t.Fatalf("a reopened PR re-authorized after its closure must be ready, got %d (%+v)",
				len(in.ReadyToMerge), in.Discharged.ReadyToMerge)
		}
	})

	t.Run("audit does not call the fresh park moot", func(t *testing.T) {
		arts := []state.Artifact{
			art(state.KindAction, "run_1", "act_1", inboxBase, wouldMergeFor("o/widget", 7, "x")),
			closure,
			art(state.KindEscalation, "run_2", "esc_2", inboxBase.Add(2*time.Hour), esc("grt_a", "q", "", "o/widget", 7)),
		}
		rep := ParkDischargeReport(arts)
		if rep.Live != 1 || rep.Moot != 0 {
			t.Fatalf("want the reopened park counted live, got %+v", rep)
		}
	})
}

// TestClosureStillSettlesAnOlderTerminal is the ordering rule's other direction:
// a closure recorded AFTER the terminal it settles must still work, or the fix
// above would simply disable the moot class.
func TestClosureStillSettlesAnOlderTerminal(t *testing.T) {
	arts := []state.Artifact{
		art(state.KindEscalation, "run_1", "esc_1", inboxBase, esc("grt_a", "q", "", "o/widget", 7)),
		art(state.KindSubjectClosed, "run_1", "sbc_1", inboxBase.Add(time.Hour), map[string]any{
			"repo": "o/widget", "number": 7, "state": ClosedNotOpen,
		}),
	}

	in := closureInbox(t, arts, NextRequest{})

	if len(in.Parked) != 0 || in.Discharged.Parked.Moot != 1 {
		t.Fatalf("a closure postdating its terminal must still moot it, got %d parked / %+v",
			len(in.Parked), in.Discharged.Parked)
	}
}
