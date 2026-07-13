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
		execution.KindWorkloadReady,
		execution.KindWorkloadStarted,
		execution.KindWorkloadExited,
	} {
		phase := execution.PhaseAdmission
		switch kind {
		case execution.KindPlacementAllocated, execution.KindWorkloadReady:
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
	if state.Terminal || state.LastSeq != 5 || state.Phase != execution.PhaseWorkload {
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

func TestJournalAppendAfterCloseErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	w, err := journal.Create(path, "run_closed")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(execution.PhaseAdmission, execution.KindRunAccepted, nil); err == nil {
		t.Fatal("Append after Close must return an error, not panic")
	}
}

func TestJournalTornFirstLineYieldsEmptyHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	// Torn first line at EOF — no durable events yet.
	if err := os.WriteFile(path, []byte(`{"schema_version":"0.1.0","run_id":"run_torn","seq":1,"time":"t","phase":"admission","kind":"run_accep`), 0o600); err != nil {
		t.Fatal(err)
	}
	events, err := journal.ReadHistory(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("torn first line must yield empty history, got %d events", len(events))
	}
}

func TestJournalCorruptMiddleLineErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	w, err := journal.Create(path, "run_mid")
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

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Insert a corrupt complete line between the two durable events, then
	// keep the second durable event so Scan finds more lines after the bad one.
	lines := splitLines(raw)
	if len(lines) < 2 {
		t.Fatalf("need 2 lines, got %d", len(lines))
	}
	corrupt := append([]byte(nil), lines[0]...)
	corrupt = append(corrupt, '\n')
	corrupt = append(corrupt, []byte(`{"not":"an-event"`)...)
	corrupt = append(corrupt, '\n')
	corrupt = append(corrupt, lines[1]...)
	corrupt = append(corrupt, '\n')
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := journal.ReadHistory(path); err == nil {
		t.Fatal("corrupt middle line with trailing durable events must error")
	}
}

func splitLines(raw []byte) [][]byte {
	var lines [][]byte
	start := 0
	for i, b := range raw {
		if b != '\n' {
			continue
		}
		if i > start {
			lines = append(lines, raw[start:i])
		}
		start = i + 1
	}
	if start < len(raw) {
		lines = append(lines, raw[start:])
	}
	return lines
}
