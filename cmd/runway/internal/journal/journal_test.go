package journal_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/cmd/runway/internal/journal"
	"github.com/itsHabib/workbench/contracts/execution"
)

func TestJournalContiguousSeq(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	w, err := journal.Create(path, "run_test")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{
		execution.KindRunAccepted,
		execution.KindPlacementAllocated,
		execution.KindWorkloadStarted,
		execution.KindWorkloadExited,
	} {
		phase := execution.PhaseAdmission
		switch kind {
		case execution.KindPlacementAllocated:
			phase = execution.PhaseStartup
		case execution.KindWorkloadStarted, execution.KindWorkloadExited:
			phase = execution.PhaseWorkload
		}
		if _, err := w.Append(phase, kind, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	events, err := journal.ReadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := execution.Reduce(events)
	if err != nil {
		t.Fatal(err)
	}
	if state.Terminal || state.LastSeq != 4 || state.Phase != execution.PhaseWorkload {
		t.Fatalf("expected open contiguous history, got %+v", state)
	}
	for i, ev := range events {
		if ev.Seq != int64(i+1) {
			t.Fatalf("seq gap at %d: %+v", i, ev)
		}
	}
}

func TestJournalFlushBeforeNextTransition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	w, err := journal.Create(path, "run_flush")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(execution.PhaseAdmission, execution.KindRunAccepted, nil); err != nil {
		t.Fatal(err)
	}
	// A concurrent reader must see the flushed event before the next append.
	events, err := journal.ReadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != execution.KindRunAccepted {
		t.Fatalf("flushed event missing to reader: %+v", events)
	}
	if _, err := w.Append(execution.PhaseStartup, execution.KindPlacementAllocated, nil); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
}

func TestJournalCrashLosesOnlyUnflushedTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	w, err := journal.Create(path, "run_crash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(execution.PhaseAdmission, execution.KindRunAccepted, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(execution.PhaseStartup, execution.KindPlacementAllocated, nil); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	// Simulate a crash mid-write: a torn JSON line with no trailing newline
	// and no fsync of a complete event.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"schema_version":"0.1.0","run_id":"run_crash","seq":3,"time":"t","phase":"workload","kind":"workload_star`); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	events, err := journal.ReadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("crash must lose only the unflushed tail; got %d events", len(events))
	}
	if _, err := execution.Reduce(events); err != nil {
		t.Fatal(err)
	}
}
