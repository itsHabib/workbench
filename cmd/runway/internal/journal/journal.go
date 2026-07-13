// Package journal is the append-only sole-writer events.ndjson for one run.
// The controller owns the writer; backends never touch it. Every Append
// flushes (File.Sync) before the controller takes the next externally
// visible step (TDD §8).
package journal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/itsHabib/workbench/contracts/execution"
)

// Writer appends canonical RunEvent lines for one run. Sole-writer by
// construction — construct one per controller, never share.
type Writer struct {
	f     *os.File
	runID string
	seq   int64
}

// Create truncates/creates events.ndjson and returns a sole writer starting
// at seq 1.
func Create(path, runID string) (*Writer, error) {
	if runID == "" {
		return nil, fmt.Errorf("journal: run id is empty")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("journal: create: %w", err)
	}
	return &Writer{f: f, runID: runID, seq: 0}, nil
}

// OpenAppend opens an existing journal for sole-writer append, continuing
// contiguous seq from the last durable event. Used by reconcile to repair a
// missing run_terminal or append after controller loss — never truncates.
func OpenAppend(path, runID string) (*Writer, error) {
	if runID == "" {
		return nil, fmt.Errorf("journal: run id is empty")
	}
	events, err := ReadHistory(path)
	if err != nil {
		return nil, err
	}
	var seq int64
	if n := len(events); n > 0 {
		seq = events[n-1].Seq
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("journal: open append: %w", err)
	}
	return &Writer{f: f, runID: runID, seq: seq}, nil
}

// Append assigns the next contiguous seq, writes one RunEvent JSON object
// per line, and Syncs before returning.
func (w *Writer) Append(phase, kind string, details map[string]any) (execution.RunEvent, error) {
	if w.f == nil {
		return execution.RunEvent{}, fmt.Errorf("journal: append on closed writer")
	}
	w.seq++
	ev := execution.RunEvent{
		SchemaVersion: execution.SchemaVersion,
		RunID:         w.runID,
		Seq:           w.seq,
		Time:          time.Now().UTC().Format(time.RFC3339Nano),
		Phase:         phase,
		Kind:          kind,
		Details:       details,
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return execution.RunEvent{}, fmt.Errorf("journal: encode seq %d: %w", w.seq, err)
	}
	if _, err := w.f.Write(append(line, '\n')); err != nil {
		return execution.RunEvent{}, fmt.Errorf("journal: write seq %d: %w", w.seq, err)
	}
	if err := w.f.Sync(); err != nil {
		return execution.RunEvent{}, fmt.Errorf("journal: sync seq %d: %w", w.seq, err)
	}
	return ev, nil
}

// Close releases the journal file.
func (w *Writer) Close() error {
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// ReadHistory loads a run's events and verifies them with execution.Reduce —
// ordering laws are never re-implemented here.
func ReadHistory(path string) ([]execution.RunEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("journal: open history: %w", err)
	}
	defer f.Close()

	var events []execution.RunEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		ev, err := execution.DecodeEvent(line)
		if err != nil {
			// More lines after a bad line => durable mid-journal corruption.
			// EOF after a bad line => torn unflushed tail (or empty history
			// when the first line itself was torn).
			if sc.Scan() {
				return nil, fmt.Errorf("journal: corrupt mid-journal: %w", err)
			}
			break
		}
		events = append(events, ev)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("journal: scan: %w", err)
	}
	if _, err := execution.Reduce(events); err != nil {
		return nil, fmt.Errorf("journal: reduce: %w", err)
	}
	return events, nil
}
