package driverstate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	dsc "github.com/itsHabib/workbench/contracts/driverstate"
)

// Append records e on run's ledger under dir. It requires the live lease l,
// seals the hash chain, and is idempotent by e.ID (a retried append returns the
// original committed event). The returned Event carries the sealed Prev/Hash and
// the truncated time actually persisted.
//
// The write window (all under the per-append lock): heal a torn tail → read the
// head → validate (contract grammar, state machine, monotonicity, seq) → seal →
// write + fsync. See the package doc for the chain rule.
func Append(dir string, l Lease, e Event) (Event, error) {
	if err := requireLease(l); err != nil {
		return Event{}, err
	}
	if e.Run != "" && e.Run != l.run {
		return Event{}, fmt.Errorf("driverstate: append: event run %q does not match lease run %q", e.Run, l.run)
	}
	e.Run = l.run
	if e.V == "" {
		e.V = dsc.Version
	}
	// Writer-supplied time, truncated to whole UTC seconds so the RFC 3339 form
	// is byte-stable across the Go and TS emitters (spec §5).
	e.Time = e.Time.UTC().Truncate(time.Second)

	rd := runDir(dir, l.run)
	lockPath := filepath.Join(rd, "append.lock")
	if err := acquireAppendLock(lockPath); err != nil {
		return Event{}, err
	}
	defer releaseAppendLock(lockPath)

	ledgerPath := filepath.Join(rd, "events.jsonl")
	events, validOffset, err := readAndHealLedger(ledgerPath)
	if err != nil {
		return Event{}, err
	}

	// Idempotency: a committed ID short-circuits before any validation, so a
	// retried terminal event is a no-op that returns the original.
	for _, prev := range events {
		if prev.ID == e.ID {
			return prev, nil
		}
	}

	if err := dsc.ValidateEvent(e); err != nil {
		return Event{}, err
	}
	if err := checkTransition(events, e); err != nil {
		return Event{}, err
	}
	if err := checkMonotonic(events, e); err != nil {
		return Event{}, err
	}

	e.Prev = headHash(events)
	e.Hash = dsc.ComputeHash(e)

	line := append(dsc.EncodeEvent(e), '\n')
	if err := appendSynced(ledgerPath, line, validOffset); err != nil {
		return Event{}, err
	}
	return e, nil
}

func headHash(events []Event) string {
	if len(events) == 0 {
		return ""
	}
	return events[len(events)-1].Hash
}

// checkMonotonic rejects an event older than the head — catches clock skew and
// replayed writes (spec §8).
func checkMonotonic(events []Event, e Event) error {
	if len(events) == 0 {
		return nil
	}
	head := events[len(events)-1]
	if e.Time.Before(head.Time) {
		return fmt.Errorf("driverstate: append: time %s is older than head %s (per-run monotonicity)", e.Time.Format(time.RFC3339), head.Time.Format(time.RFC3339))
	}
	return nil
}

// checkTransition enforces the state machine at write time (spec §5 table): the
// run-scoped kinds by their run-level rules, every other kind by folding its
// stream's prior events to a current status and applying the transition.
func checkTransition(events []Event, e Event) error {
	switch e.Kind {
	case dsc.KindRunImported:
		if len(events) > 0 {
			return ErrIllegalTransition{From: "run_open", Event: string(e.Kind)}
		}
		return nil
	case dsc.KindRunFinished:
		return checkRunFinished(events)
	}
	if len(events) == 0 {
		// A stream event with no run_imported yet: the run was never opened.
		return ErrIllegalTransition{From: "run_absent", Event: string(e.Kind)}
	}
	cur := streamStatus(events, e.Stream)
	if _, err := applyStream(cur, e); err != nil {
		return err
	}
	if e.Kind == dsc.KindStreamAttempt {
		return checkSeq(events, e)
	}
	return nil
}

// checkRunFinished allows run_finished only when every known stream is terminal
// ({merged, skipped, failed}); finishing is the no-retry declaration (spec §5).
func checkRunFinished(events []Event) error {
	if len(events) == 0 {
		return ErrIllegalTransition{From: "run_absent", Event: string(dsc.KindRunFinished)}
	}
	for _, s := range gatherStreams(events) {
		st := streamStatus(events, s)
		if !terminalStatus(st) {
			return ErrIllegalTransition{From: st, Event: string(dsc.KindRunFinished)}
		}
	}
	return nil
}

func terminalStatus(status string) bool {
	switch status {
	case dsc.StatusMerged, dsc.StatusSkipped, dsc.StatusFailed:
		return true
	default:
		return false
	}
}

// gatherStreams is the full stream set of a run: the manifest snapshot from
// run_imported plus any stream that later carried an event.
func gatherStreams(events []Event) []string {
	seen := make(map[string]bool)
	var streams []string
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		streams = append(streams, s)
	}
	for _, e := range events {
		if e.Kind == dsc.KindRunImported {
			var b dsc.RunImportedBody
			if err := json.Unmarshal(e.Body, &b); err == nil {
				for _, s := range b.Streams {
					add(s.Stream)
				}
			}
		}
		add(e.Stream)
	}
	return streams
}

// streamStatus folds a stream's prior events to its current derived status.
// Stored events were validated on write, so applyStream never errors here; a
// stray illegal fold is ignored rather than corrupting the status.
func streamStatus(events []Event, stream string) string {
	status := dsc.StatusPending
	for _, e := range events {
		if e.Stream != stream {
			continue
		}
		if ns, err := applyStream(status, e); err == nil {
			status = ns
		}
	}
	return status
}

// applyStream is the stream-scoped transition table (spec §5). It returns the
// next status or ErrIllegalTransition{From, Event}.
func applyStream(cur string, e Event) (string, error) {
	illegal := ErrIllegalTransition{From: cur, Event: string(e.Kind)}
	switch e.Kind {
	case dsc.KindStreamDispatched:
		if cur == dsc.StatusPending || cur == dsc.StatusFailed {
			return dsc.StatusDispatched, nil
		}
	case dsc.KindStreamAttempt:
		if cur != dsc.StatusDispatched {
			return "", illegal
		}
		var b dsc.StreamAttemptBody
		if err := json.Unmarshal(e.Body, &b); err != nil {
			return "", fmt.Errorf("driverstate: stream_attempt body: %w", err)
		}
		if !b.Terminal {
			return dsc.StatusDispatched, nil
		}
		if b.FailureCategory != "" {
			return dsc.StatusFailed, nil
		}
		return dsc.StatusLanded, nil
	case dsc.KindStreamFailed:
		if cur == dsc.StatusDispatched {
			return dsc.StatusFailed, nil
		}
	case dsc.KindStreamPROpened:
		if cur == dsc.StatusLanded {
			return dsc.StatusPROpen, nil
		}
	case dsc.KindReviewCycle:
		if cur == dsc.StatusPROpen {
			return dsc.StatusPROpen, nil
		}
	case dsc.KindStreamMerged:
		if cur == dsc.StatusPROpen {
			return dsc.StatusMerged, nil
		}
	case dsc.KindStreamSkipped:
		if cur == dsc.StatusPending || cur == dsc.StatusFailed {
			return dsc.StatusSkipped, nil
		}
	}
	return "", illegal
}

// checkSeq enforces append-only monotone stream_attempt seq per stream: a new
// attempt's seq must exceed every prior attempt's on the same stream.
func checkSeq(events []Event, e Event) error {
	var b dsc.StreamAttemptBody
	if err := json.Unmarshal(e.Body, &b); err != nil {
		return fmt.Errorf("driverstate: stream_attempt body: %w", err)
	}
	maxSeq := 0
	for _, prev := range events {
		if prev.Stream != e.Stream || prev.Kind != dsc.KindStreamAttempt {
			continue
		}
		var pb dsc.StreamAttemptBody
		if err := json.Unmarshal(prev.Body, &pb); err != nil {
			continue
		}
		if pb.Seq > maxSeq {
			maxSeq = pb.Seq
		}
	}
	if b.Seq <= maxSeq {
		return fmt.Errorf("driverstate: append: stream_attempt seq %d must exceed prior max %d", b.Seq, maxSeq)
	}
	return nil
}

// readAndHealLedger reads a run's ledger, healing a torn final line: bytes after
// the last newline are a crash's partial write and are truncated from the file
// before parsing. It returns the decoded events and the byte offset the next
// append must write at (the healed length).
func readAndHealLedger(path string) ([]Event, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("driverstate: read ledger: %w", err)
	}
	validLen := lastNewline(data)
	if validLen != int64(len(data)) {
		if err := os.Truncate(path, validLen); err != nil {
			return nil, 0, fmt.Errorf("driverstate: heal torn tail: %w", err)
		}
	}
	events, err := decodeLedger(data[:validLen])
	if err != nil {
		return nil, 0, err
	}
	return events, validLen, nil
}

// lastNewline returns the length of data up to and including its last newline —
// the boundary of the last complete line. Zero if there is none.
func lastNewline(data []byte) int64 {
	i := bytes.LastIndexByte(data, '\n')
	if i < 0 {
		return 0
	}
	return int64(i + 1)
}

// decodeLedger parses complete lines into events. A line that fails to decode is
// a mid-chain break (the torn FINAL line was already healed away): a loud
// ErrChainBroken, never a silent skip (spec §8).
func decodeLedger(data []byte) ([]Event, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var events []Event
	for _, line := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		e, err := dsc.DecodeEvent(line)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrChainBroken, err)
		}
		events = append(events, e)
	}
	return events, nil
}

// appendSynced appends line at offset (the healed length) and fsyncs. Opening
// with O_APPEND after a heal-truncate is race-free because the whole window runs
// under the append lock.
func appendSynced(path string, line []byte, offset int64) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("driverstate: open ledger: %w", err)
	}
	if _, err := f.WriteAt(line, offset); err != nil {
		f.Close()
		return fmt.Errorf("driverstate: write event: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("driverstate: fsync event: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("driverstate: close ledger: %w", err)
	}
	return nil
}

// acquireAppendLock takes the per-append byte-race lock: an O_EXCL create under
// the same retry discipline as the lease.
func acquireAppendLock(path string) error {
	return withRetry(func() error {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			if os.IsExist(err) {
				return errRetry
			}
			return fmt.Errorf("driverstate: acquire append lock: %w", err)
		}
		return f.Close()
	})
}

func releaseAppendLock(path string) {
	_ = os.Remove(path)
}
