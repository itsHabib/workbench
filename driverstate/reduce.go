package driverstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
)

// Run-level status strings produced by the reducer. These are mechanism-level
// constants; contracts/driverstate tracks stream statuses (pending, dispatched,
// landed, …, merged). RunStatusCorrupt is produced only by Runs when Reduce
// fails — never by Reduce itself, which returns an error instead.
const (
	RunStatusOpen     = "open"
	RunStatusFinished = "finished"
	RunStatusCorrupt  = "corrupt"
)

// Reduce folds a run's hash-chained ledger into a RunState. It is a pure
// read: no lease required, no state modification. The ledger is read with
// torn-tail tolerance — a partial line left by a crash is discarded without
// error — and the hash chain is verified across every complete line. A
// mid-chain break returns ErrChainBroken; a torn final line is discarded
// with a stderr warning (lock-free readers may see a mid-append file; spec §8).
//
// Manifest-seeded streams: the run_imported body carries the full stream list.
// Reduce initialises every stream to pending before folding later events, so a
// resuming session always sees the complete stream set even when some streams
// have received no events yet (spec §7 F3).
//
// Unknown event kinds are tolerated: the chain is verified across them, but
// they are skipped in the fold and a warning is printed to stderr (spec §8).
func Reduce(dir, run string) (dsc.RunState, error) {
	if err := validateRunID(run); err != nil {
		return dsc.RunState{}, fmt.Errorf("driverstate: reduce: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(runDir(dir, run), "events.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return dsc.RunState{}, fmt.Errorf("driverstate: reduce: run %q not found: %w", run, err)
		}
		return dsc.RunState{}, fmt.Errorf("driverstate: reduce: %w", err)
	}
	events, err := decodeLedger(trimWithWarning(data, "reduce", run))
	if err != nil {
		return dsc.RunState{}, fmt.Errorf("driverstate: reduce: %w", err)
	}
	return foldEvents(events), nil
}

// trimWithWarning discards a torn final line and, when it actually trimmed,
// says so on stderr — so a lock-free reader can distinguish a benign
// mid-append read from a real chain break (spec §8).
func trimWithWarning(data []byte, verb, run string) []byte {
	trimmed := trimTornTail(data)
	if len(trimmed) != len(data) {
		fmt.Fprintf(os.Stderr, "driverstate: %s: discarded torn final line in run %q\n", verb, run)
	}
	return trimmed
}

// foldEvents builds a RunState from a decoded, chain-verified event slice.
// Unknown kinds are skipped with a stderr warning; known kinds each apply
// their own fold rule. The function is pure: no I/O beyond the warning.
func foldEvents(events []Event) dsc.RunState {
	state := dsc.RunState{
		Streams: make(map[string]dsc.StreamRecord),
	}
	finished := false
	for _, e := range events {
		if !e.Kind.Known() {
			fmt.Fprintf(os.Stderr, "driverstate: reduce: skipping unknown kind %q at event %q\n", e.Kind, e.ID)
			continue
		}
		applyEventToState(&state, &finished, e)
	}
	if finished {
		state.Run.Status = RunStatusFinished
		return state
	}
	state.Run.Status = RunStatusOpen
	return state
}

// applyEventToState applies one known event to the accumulator in place.
// Each case delegates to a helper so the switch stays shallow.
func applyEventToState(state *dsc.RunState, finished *bool, e Event) {
	switch e.Kind {
	case dsc.KindRunImported:
		applyRunImported(state, e)
	case dsc.KindStreamDispatched:
		applyStreamDispatched(state, e)
	case dsc.KindStreamAttempt:
		applyStreamAttempt(state, e)
	case dsc.KindStreamPROpened:
		applyStreamPROpened(state, e)
	case dsc.KindReviewCycle:
		// Status stays pr_open; no fold needed.
	case dsc.KindStreamLanded:
		setStreamStatus(state, e.Stream, dsc.StatusLanded)
	case dsc.KindStreamFailed:
		setStreamStatus(state, e.Stream, dsc.StatusFailed)
	case dsc.KindStreamSkipped:
		setStreamStatus(state, e.Stream, dsc.StatusSkipped)
	case dsc.KindStreamMerged:
		applyStreamMerged(state, e)
	case dsc.KindRunFinished:
		*finished = true
	}
}

// applyRunImported seeds the RunRecord and initialises every manifest stream
// to pending. Streams that later receive events have their status overlaid by
// the subsequent fold steps.
func applyRunImported(state *dsc.RunState, e Event) {
	var b dsc.RunImportedBody
	if err := json.Unmarshal(e.Body, &b); err != nil {
		return
	}
	state.Run.Repo = b.Repo
	state.Run.Source = b.Source
	state.Run.ImportedAt = e.Time
	for _, s := range b.Streams {
		if _, exists := state.Streams[s.Stream]; !exists {
			state.Streams[s.Stream] = dsc.StreamRecord{Status: dsc.StatusPending}
		}
	}
}

// applyStreamDispatched advances the stream to dispatched and folds optional
// branch/worktree locators when the body carries them. A null or empty body
// still sets status — older ledgers omit the locators.
func applyStreamDispatched(state *dsc.RunState, e Event) {
	rec := state.Streams[e.Stream]
	rec.Status = dsc.StatusDispatched
	var b dsc.StreamDispatchedBody
	if err := json.Unmarshal(e.Body, &b); err == nil {
		rec.Branch = b.Branch
		rec.Worktree = b.Worktree
	}
	state.Streams[e.Stream] = rec
}

// applyStreamAttempt records the attempt and advances the stream status for a
// terminal attempt (landed unless FailureCategory is set, in which case failed).
func applyStreamAttempt(state *dsc.RunState, e Event) {
	var b dsc.StreamAttemptBody
	if err := json.Unmarshal(e.Body, &b); err != nil {
		return
	}
	rec := state.Streams[e.Stream]
	rec.Attempts = append(rec.Attempts, dsc.AttemptRecord{
		Seq:             b.Seq,
		Terminal:        b.Terminal,
		FailureCategory: b.FailureCategory,
		Commit:          b.Commit,
	})
	if b.Terminal {
		rec.Status = dsc.StatusLanded
		if b.FailureCategory != "" {
			rec.Status = dsc.StatusFailed
		}
	}
	state.Streams[e.Stream] = rec
}

// applyStreamPROpened records the PR number and URL and advances the status to
// pr_open.
func applyStreamPROpened(state *dsc.RunState, e Event) {
	var b dsc.StreamPROpenedBody
	if err := json.Unmarshal(e.Body, &b); err != nil {
		return
	}
	rec := state.Streams[e.Stream]
	rec.Status = dsc.StatusPROpen
	rec.PR = b.PR
	rec.URL = b.URL
	state.Streams[e.Stream] = rec
}

// applyStreamMerged records the merge commit and PR number and advances the
// status to merged.
func applyStreamMerged(state *dsc.RunState, e Event) {
	var b dsc.StreamMergedBody
	if err := json.Unmarshal(e.Body, &b); err != nil {
		return
	}
	rec := state.Streams[e.Stream]
	rec.Status = dsc.StatusMerged
	rec.PR = b.PR
	rec.MergeCommit = b.MergeCommit
	state.Streams[e.Stream] = rec
}

// setStreamStatus is the common single-field update for kinds that only change
// the stream's status (dispatched, landed, failed, skipped).
func setStreamStatus(state *dsc.RunState, stream, status string) {
	rec := state.Streams[stream]
	rec.Status = status
	state.Streams[stream] = rec
}
