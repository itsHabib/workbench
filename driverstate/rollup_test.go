package driverstate

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
)

// buildChild records a full or partial child sub-run under dir and returns its
// run id. attempts is the number of stream_attempt events (the last terminal, so
// attempts-1 is the retry count); cycles is the review_cycle count; merged
// carries the child through to stream_merged.
func buildChild(t *testing.T, dir, run, parent, stream string, conflict bool, attempts, cycles int, merged bool) string {
	t.Helper()
	l, err := Claim(dir, run, "session:"+run)
	if err != nil {
		t.Fatalf("claim %s: %v", run, err)
	}
	tm := baseTime
	next := func() time.Time { tm = tm.Add(time.Second); return tm }
	mustAppend(t, dir, l, ev("evt_"+run+"_imp", dsc.KindRunImported, "", "session:"+run, next(), dsc.RunImportedBody{
		Repo:         "itsHabib/workbench",
		Source:       "docs/driver.md",
		Manifest:     json.RawMessage(`{"v":1}`),
		Streams:      []dsc.StreamSpec{{Stream: stream, DocPath: "docs/x.md"}},
		Parent:       parent,
		ParentStream: stream,
	}))
	mustAppend(t, dir, l, ev("evt_"+run+"_disp", dsc.KindStreamDispatched, stream, "session:"+run, next(), dsc.StreamDispatchedBody{
		Engine: "session-orchestrator-child", Branch: "feat/" + run, WorktreeConflict: conflict,
	}))
	for seq := 1; seq <= attempts; seq++ {
		mustAppend(t, dir, l, ev("evt_"+run+"_att"+strconv.Itoa(seq), dsc.KindStreamAttempt, stream, "session:"+run, next(), dsc.StreamAttemptBody{
			Seq: seq, DocPath: "docs/x.md", Terminal: seq == attempts, Commit: "sha" + strconv.Itoa(seq),
		}))
	}
	mustAppend(t, dir, l, ev("evt_"+run+"_pr", dsc.KindStreamPROpened, stream, "session:"+run, next(), dsc.StreamPROpenedBody{
		PR: 100, URL: "https://x/100", HeadSHA: "head",
	}))
	for c := 1; c <= cycles; c++ {
		mustAppend(t, dir, l, ev("evt_"+run+"_rc"+strconv.Itoa(c), dsc.KindReviewCycle, stream, "session:"+run, next(), dsc.ReviewCycleBody{
			Cycle: c, PanelSettled: true, Findings: 0,
		}))
	}
	if merged {
		mustAppend(t, dir, l, ev("evt_"+run+"_mrg", dsc.KindStreamMerged, stream, "session:"+run, next(), dsc.StreamMergedBody{
			PR: 100, MergeCommit: "mc", MergedAt: "2026-07-16T12:30:00Z",
		}))
	}
	return run
}

// buildParentMirror records the parent's coarse stream mirror for one stream:
// dispatched{child_run} → pr_opened → optionally merged.
func buildParentMirror(t *testing.T, dir string, l Lease, tm *time.Time, stream, childRun string, pr int, merged bool) {
	t.Helper()
	next := func() time.Time { *tm = tm.Add(time.Second); return *tm }
	mustAppend(t, dir, l, ev("evt_p_disp_"+stream, dsc.KindStreamDispatched, stream, "session:parent", next(), dsc.StreamDispatchedBody{
		Engine: "session-orchestrator", ChildRun: childRun,
	}))
	// The mirror carries a single terminal attempt (the child's head commit)
	// to reach landed before pr_opened — the state machine requires it.
	mustAppend(t, dir, l, ev("evt_p_att_"+stream, dsc.KindStreamAttempt, stream, "session:parent", next(), dsc.StreamAttemptBody{
		Seq: 1, DocPath: "docs/x.md", Terminal: true, Commit: "childhead",
	}))
	mustAppend(t, dir, l, ev("evt_p_pr_"+stream, dsc.KindStreamPROpened, stream, "session:parent", next(), dsc.StreamPROpenedBody{
		PR: pr, URL: "https://x/100", HeadSHA: "head",
	}))
	if merged {
		mustAppend(t, dir, l, ev("evt_p_mrg_"+stream, dsc.KindStreamMerged, stream, "session:parent", next(), dsc.StreamMergedBody{
			PR: pr, MergeCommit: "mc", MergedAt: "2026-07-16T12:30:00Z",
		}))
	}
}

// TestRollupJoinsChildren builds a parent run with two delegated streams — one
// merged child with friction, one mid-flight child — and asserts the rollup
// joins the parent mirror to each child's ground truth, derives friction, and
// reports the merged boundary unreached because a stream is still mid-flight.
func TestRollupJoinsChildren(t *testing.T) {
	dir := t.TempDir()
	// A merged child: 2 attempts (1 retry), 2 review cycles, a worktree conflict.
	buildChild(t, dir, "dsr_ca", "dsr_parent", "dss_a", true, 2, 2, true)
	// A mid-flight child: 1 attempt, no cycles yet, PR open, not merged.
	buildChild(t, dir, "dsr_cb", "dsr_parent", "dss_b", false, 1, 0, false)

	pl, err := Claim(dir, "dsr_parent", "session:parent")
	if err != nil {
		t.Fatalf("claim parent: %v", err)
	}
	tm := baseTime.Add(time.Hour) // parent mirror strictly after children
	mustAppend(t, dir, pl, ev("evt_p_imp", dsc.KindRunImported, "", "session:parent", tm, dsc.RunImportedBody{
		Repo:         "itsHabib/workbench",
		Source:       "docs/driver.md",
		Manifest:     json.RawMessage(`{"v":1}`),
		Streams:      []dsc.StreamSpec{{Stream: "dss_a", DocPath: "docs/a.md"}, {Stream: "dss_b", DocPath: "docs/b.md"}},
		DoneBoundary: dsc.DoneBoundaryMerged,
	}))
	buildParentMirror(t, dir, pl, &tm, "dss_a", "dsr_ca", 100, true)
	buildParentMirror(t, dir, pl, &tm, "dss_b", "dsr_cb", 100, false)

	r, err := Rollup(dir, "dsr_parent")
	if err != nil {
		t.Fatalf("rollup: %v", err)
	}
	if r.DoneBoundary != dsc.DoneBoundaryMerged {
		t.Errorf("done_boundary = %q, want merged", r.DoneBoundary)
	}
	if r.BoundaryReached {
		t.Errorf("boundary_reached = true, want false (dss_b mid-flight)")
	}
	byStream := map[string]StreamRollup{}
	for _, s := range r.Streams {
		byStream[s.Stream] = s
	}
	a := byStream["dss_a"]
	if a.ChildRun != "dsr_ca" || a.ParentStatus != dsc.StatusMerged || a.ChildStatus != dsc.StatusMerged {
		t.Errorf("dss_a join wrong: %+v", a)
	}
	if !a.Agrees {
		t.Errorf("dss_a should agree (parent merged, child merged): %+v", a)
	}
	if a.Friction != (Friction{GateCycles: 2, Retries: 1, WorktreeConflict: true}) {
		t.Errorf("dss_a friction = %+v, want {2,1,true}", a.Friction)
	}
	b := byStream["dss_b"]
	if b.ChildRun != "dsr_cb" || b.ParentStatus != dsc.StatusPROpen || b.ChildStatus != dsc.StatusPROpen {
		t.Errorf("dss_b join wrong: %+v", b)
	}
	if !b.Agrees {
		t.Errorf("dss_b should agree (both pr_open): %+v", b)
	}
	if b.Friction != (Friction{GateCycles: 0, Retries: 0, WorktreeConflict: false}) {
		t.Errorf("dss_b friction = %+v, want zero", b.Friction)
	}
}

// TestChildImportsWithSharedKeyGetDistinctRuns is the sub-run isolation guard:
// two children derived from one manifest share (repo, source, generated_at) but
// name different parent streams, and must NOT dedupe into one run — the parent
// linkage discriminates them (spec §4 D1).
func TestChildImportsWithSharedKeyGetDistinctRuns(t *testing.T) {
	dir := t.TempDir()
	imp := func(run, stream string) Event {
		return ev("evt_"+run, dsc.KindRunImported, "", "session:"+run, baseTime, dsc.RunImportedBody{
			Repo: "itsHabib/workbench", Source: "driver.md", GeneratedAt: "2026-07-20T00:00:00Z",
			Manifest: json.RawMessage(`{}`), Streams: []dsc.StreamSpec{{Stream: stream, DocPath: "d"}},
			Parent: "dsr_parent", ParentStream: stream,
		})
	}
	la, err := Claim(dir, "dsr_ca", "session:ca")
	if err != nil {
		t.Fatalf("claim ca: %v", err)
	}
	outA := mustAppend(t, dir, la, imp("dsr_ca", "dss_a"))
	lb, err := Claim(dir, "dsr_cb", "session:cb")
	if err != nil {
		t.Fatalf("claim cb: %v", err)
	}
	outB := mustAppend(t, dir, lb, imp("dsr_cb", "dss_b"))
	if outA.Run == outB.Run {
		t.Fatalf("distinct children collapsed into one run: %s", outA.Run)
	}
	// And a genuine retry of child A's SAME import still dedupes to A.
	retry := mustAppend(t, dir, la, imp("dsr_ca", "dss_a"))
	if retry.Run != outA.Run {
		t.Errorf("child A retry did not dedupe: %s vs %s", retry.Run, outA.Run)
	}
}

// TestRollupRejectsMismatchedChildLink guards the join: a parent stream whose
// child_run points at a sub-run that names a DIFFERENT parent must not have that
// child's PR reported here with agrees:true (spec §4 D5, medium finding).
func TestRollupRejectsMismatchedChildLink(t *testing.T) {
	dir := t.TempDir()
	// A child that belongs to a DIFFERENT parent, carrying its own PR + friction.
	buildChild(t, dir, "dsr_stray", "dsr_other_parent", "dss_a", true, 1, 0, false)

	pl, err := Claim(dir, "dsr_parent", "session:parent")
	if err != nil {
		t.Fatalf("claim parent: %v", err)
	}
	tm := baseTime.Add(time.Hour)
	mustAppend(t, dir, pl, ev("evt_p_imp", dsc.KindRunImported, "", "session:parent", tm, dsc.RunImportedBody{
		Repo: "itsHabib/workbench", Source: "docs/driver.md", Manifest: json.RawMessage(`{"v":1}`),
		Streams: []dsc.StreamSpec{{Stream: "dss_a", DocPath: "docs/a.md"}},
	}))
	// Parent mis-links this stream to the stray child.
	buildParentMirror(t, dir, pl, &tm, "dss_a", "dsr_stray", 100, false)

	r, err := Rollup(dir, "dsr_parent")
	if err != nil {
		t.Fatalf("rollup: %v", err)
	}
	s := r.Streams[0]
	if s.Agrees {
		t.Errorf("mismatched child link must not agree: %+v", s)
	}
	if s.Friction.WorktreeConflict {
		t.Errorf("stray child's facts must not be adopted: %+v", s)
	}
}

// TestMirrorAgrees pins the parent↔child agreement cross-check, especially the
// terminal-status cases a happy-path-only rank would mask (merged vs skipped).
func TestMirrorAgrees(t *testing.T) {
	rec := func(status string, pr int) dsc.StreamRecord { return dsc.StreamRecord{Status: status, PR: pr} }
	cases := []struct {
		name          string
		parent, child dsc.StreamRecord
		want          bool
	}{
		{"both merged", rec(dsc.StatusMerged, 5), rec(dsc.StatusMerged, 5), true},
		{"parent merged, child skipped — contradiction", rec(dsc.StatusMerged, 0), rec(dsc.StatusSkipped, 0), false},
		{"parent merged, child pr_open — parent leads", rec(dsc.StatusMerged, 0), rec(dsc.StatusPROpen, 0), false},
		{"parent failed, child pending — parent leads a terminal", rec(dsc.StatusFailed, 0), rec(dsc.StatusPending, 0), false},
		{"both skipped", rec(dsc.StatusSkipped, 0), rec(dsc.StatusSkipped, 0), true},
		{"parent pr_open, child merged — child ahead", rec(dsc.StatusPROpen, 5), rec(dsc.StatusMerged, 5), true},
		{"parent dispatched, child pr_open — child ahead", rec(dsc.StatusDispatched, 0), rec(dsc.StatusPROpen, 0), true},
		{"parent pr_open, child dispatched — parent leads", rec(dsc.StatusPROpen, 0), rec(dsc.StatusDispatched, 0), false},
		{"pr mismatch", rec(dsc.StatusPROpen, 5), rec(dsc.StatusPROpen, 6), false},
		{"parent dispatched, child failed — parent not ahead", rec(dsc.StatusDispatched, 0), rec(dsc.StatusFailed, 0), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mirrorAgrees(c.parent, c.child); got != c.want {
				t.Errorf("mirrorAgrees(%s, %s) = %t, want %t", c.parent.Status, c.child.Status, got, c.want)
			}
		})
	}
}

// TestRollupCatchesMirrorAheadOfChild is the recorded-ahead-of-facts guard: the
// parent mirror says merged but the child's own record only reached pr_open, so
// the cross-check must flag disagreement (spec §4 D5).
func TestRollupCatchesMirrorAheadOfChild(t *testing.T) {
	dir := t.TempDir()
	// Child only reached PR open — NOT merged.
	buildChild(t, dir, "dsr_cx", "dsr_parent2", "dss_a", false, 1, 0, false)

	pl, err := Claim(dir, "dsr_parent2", "session:parent")
	if err != nil {
		t.Fatalf("claim parent: %v", err)
	}
	tm := baseTime.Add(time.Hour)
	mustAppend(t, dir, pl, ev("evt_p2_imp", dsc.KindRunImported, "", "session:parent", tm, dsc.RunImportedBody{
		Repo:     "itsHabib/workbench",
		Source:   "docs/driver.md",
		Manifest: json.RawMessage(`{"v":1}`),
		Streams:  []dsc.StreamSpec{{Stream: "dss_a", DocPath: "docs/a.md"}},
	}))
	// Parent mirror runs AHEAD: it records merged.
	buildParentMirror(t, dir, pl, &tm, "dss_a", "dsr_cx", 100, true)

	r, err := Rollup(dir, "dsr_parent2")
	if err != nil {
		t.Fatalf("rollup: %v", err)
	}
	if len(r.Streams) != 1 {
		t.Fatalf("want 1 stream, got %d", len(r.Streams))
	}
	s := r.Streams[0]
	if s.Agrees {
		t.Errorf("mirror is ahead of child (merged vs pr_open) — agrees must be false: %+v", s)
	}
	// Default (empty) boundary reads as merged; parent status IS merged, so the
	// orchestration boundary is reached even though the child disagrees — the two
	// signals are distinct on purpose.
	if !r.BoundaryReached {
		t.Errorf("parent mirror merged → boundary_reached true, got false")
	}
}
