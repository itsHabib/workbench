package driverstate

import (
	"fmt"
	"sort"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
)

// ParentRollup is the join view of a parent run and its child sub-runs: the
// resume roster (which streams are where, which child produced which PR), the
// parent↔child cross-check (Agrees), and the per-child friction — all from the
// ledger, never from a child's raw impl context (session-orchestrator spec §7
// Resume, §4 D5). It is a mechanism read type, not a contract event type: like
// RunSummary it is derived, so it lives here beside the reducer, not in the leaf
// contract.
type ParentRollup struct {
	Run             string         `json:"run"`
	DoneBoundary    string         `json:"done_boundary"`
	BoundaryReached bool           `json:"boundary_reached"`
	Streams         []StreamRollup `json:"streams"`
}

// StreamRollup is one stream's row in a ParentRollup: the parent's mirrored
// status joined to its child sub-run's own ground-truth status, plus the PR the
// stream produced and the child's friction.
type StreamRollup struct {
	Stream       string `json:"stream"`
	ChildRun     string `json:"child_run,omitempty"`
	ParentStatus string `json:"parent_status"`
	ChildStatus  string `json:"child_status,omitempty"`
	PR           int    `json:"pr,omitempty"`
	URL          string `json:"url,omitempty"`
	MergeCommit  string `json:"merge_commit,omitempty"`
	// Agrees is false when the parent mirror ran AHEAD of the child's own record
	// (recorded ahead of facts — the failure this plane exists to catch) or the
	// child sub-run could not be read. A child that is further along than the
	// parent's mirror is normal mid-flight and agrees (spec §4 D5).
	Agrees   bool     `json:"agrees"`
	Friction Friction `json:"friction"`
}

// Friction is the per-child friction rollup (spec §4 D4): the gate-loop count,
// dispatch retries, and whether the dispatch hit a worktree conflict. All are
// derived from the child sub-run's own events — none is a stored field.
type Friction struct {
	GateCycles       int  `json:"gate_cycles"`
	Retries          int  `json:"retries"`
	WorktreeConflict bool `json:"worktree_conflict"`
}

// Rollup joins a parent run to its child sub-runs. It reduces the parent, then
// follows each stream's recorded child_run link and reduces that child, so the
// caller sees the whole fan-out in one read without touching any child's impl
// context. It is a pure read — no lease.
//
// A stream whose parent mirror has not yet recorded a child_run (the parent died
// between dispatching a child and mirroring it) shows an empty ChildRun here;
// driver_runs{parent} is the scan-based complement that still finds such an
// orphan child by its parent field, and the skill's git reconcile closes the
// gap. This function follows only the explicit links the parent recorded.
func Rollup(dir, parent string) (ParentRollup, error) {
	parentState, err := Reduce(dir, parent)
	if err != nil {
		return ParentRollup{}, fmt.Errorf("driverstate: rollup: %w", err)
	}
	boundary := dsc.DoneBoundaryOrDefault(parentState.Run.DoneBoundary)
	out := ParentRollup{
		Run:             parent,
		DoneBoundary:    boundary,
		BoundaryReached: true,
	}
	for stream, rec := range parentState.Streams {
		row := rollupStream(dir, stream, rec)
		if !reachedBoundary(rec.Status, boundary) {
			out.BoundaryReached = false
		}
		out.Streams = append(out.Streams, row)
	}
	// Deterministic order: parentState.Streams is a map, so sort by stream id so
	// the CLI rendering and any index-based assertion are stable across runs.
	sort.Slice(out.Streams, func(i, j int) bool { return out.Streams[i].Stream < out.Streams[j].Stream })
	return out, nil
}

// rollupStream builds one stream's row: the parent mirror facts plus, when the
// stream delegated to a child, the child's ground-truth status, friction, and
// the mirror-agreement verdict.
func rollupStream(dir, stream string, parentRec dsc.StreamRecord) StreamRollup {
	row := StreamRollup{
		Stream:       stream,
		ChildRun:     parentRec.ChildRun,
		ParentStatus: parentRec.Status,
		PR:           parentRec.PR,
		URL:          parentRec.URL,
		MergeCommit:  parentRec.MergeCommit,
		Agrees:       true, // no child to contradict the mirror
	}
	if parentRec.ChildRun == "" {
		return row
	}
	childState, err := Reduce(dir, parentRec.ChildRun)
	if err != nil {
		// Cannot read the child → cannot claim agreement.
		row.Agrees = false
		return row
	}
	childRec := childStreamRecord(childState, stream)
	row.ChildStatus = childRec.Status
	row.Friction = frictionOf(childRec)
	row.Agrees = mirrorAgrees(parentRec, childRec)
	// Surface the PR facts from whoever recorded them: the parent mirror when it
	// has them, else the child. A resume after the parent died between a child
	// opening its PR and mirroring it up still learns which PR the child produced
	// (spec §7 Resume) — the whole point of the rollup.
	if row.PR == 0 {
		row.PR = childRec.PR
		row.URL = childRec.URL
	}
	if row.MergeCommit == "" {
		row.MergeCommit = childRec.MergeCommit
	}
	return row
}

// childStreamRecord returns the child sub-run's record for the delegated stream.
// The child reuses the parent's stream id (spec §3), so the lookup is by id; a
// child that carried exactly one stream under a different id falls back to that
// one, so the rollup still joins.
func childStreamRecord(childState dsc.RunState, stream string) dsc.StreamRecord {
	if rec, ok := childState.Streams[stream]; ok {
		return rec
	}
	if len(childState.Streams) == 1 {
		for _, rec := range childState.Streams {
			return rec
		}
	}
	return dsc.StreamRecord{}
}

// frictionOf derives a stream's friction from its folded record: the gate-loop
// count is the review-cycle count, retries are the attempts beyond the first,
// and the worktree-conflict flag rides the dispatch (spec §4 D4).
func frictionOf(rec dsc.StreamRecord) Friction {
	retries := len(rec.Attempts) - 1
	if retries < 0 {
		retries = 0
	}
	return Friction{
		GateCycles:       rec.ReviewCycles,
		Retries:          retries,
		WorktreeConflict: rec.WorktreeConflict,
	}
}

// mirrorAgrees reports whether the parent's mirrored stream status is consistent
// with the child's own record. It disagrees when the parent recorded ahead of
// what the child can back up:
//   - a PR the mirror names contradicts the child's PR;
//   - the parent reached a TERMINAL status (merged/skipped/failed) the child did
//     not reach identically — merged-vs-skipped is a contradiction, not a lead,
//     and equal happy-path rank must not mask it;
//   - the parent leads a still-in-flight child up the happy path.
//
// A child that is further along than the parent's mirror is normal mid-flight
// and agrees; a terminal child under a non-terminal parent is the parent simply
// not having caught up, and also agrees.
func mirrorAgrees(parentRec, childRec dsc.StreamRecord) bool {
	if parentRec.PR != 0 && childRec.PR != 0 && parentRec.PR != childRec.PR {
		return false
	}
	if terminalStatus(parentRec.Status) {
		return parentRec.Status == childRec.Status
	}
	if terminalStatus(childRec.Status) {
		return true
	}
	return happyRank(parentRec.Status) <= happyRank(childRec.Status)
}

// happyRank orders the NON-terminal happy path (pending → dispatched → landed →
// pr_open → merged) so a mirror leading a still-in-flight child is detectable.
// merged is the happy terminus (rank 4); the off-path terminals (skipped,
// failed) and any unknown value rank 0 — callers gate terminal statuses through
// terminalStatus first, so those never rely on this rank.
func happyRank(status string) int {
	switch status {
	case dsc.StatusDispatched:
		return 1
	case dsc.StatusLanded:
		return 2
	case dsc.StatusPROpen:
		return 3
	case dsc.StatusMerged:
		return 4
	}
	return 0
}

// reachedBoundary reports whether a stream has reached the run's done boundary —
// either its status is at least the boundary's rank, or it is terminal
// (merged/skipped/failed: nothing left for the orchestrator to push). It is the
// per-stream input to ParentRollup.BoundaryReached (spec §4 D7).
func reachedBoundary(status, boundary string) bool {
	if terminalStatus(status) {
		return true
	}
	return happyRank(status) >= boundaryRank(boundary)
}

// boundaryRank maps a done boundary to the happy-path rank a stream must reach.
// An unrecognized boundary requires merged (the strictest) so a typo never
// reports a run done early.
func boundaryRank(boundary string) int {
	switch dsc.DoneBoundaryOrDefault(boundary) {
	case dsc.DoneBoundaryPROpen, dsc.DoneBoundaryGreen:
		return 3
	case dsc.DoneBoundaryMerged:
		return 4
	}
	return 4
}
