package controller

import (
	"os"
	"testing"

	"github.com/itsHabib/workbench/cmd/runway/internal/journal"
	"github.com/itsHabib/workbench/cmd/runway/internal/state"
	"github.com/itsHabib/workbench/contracts/execution"
)

// TestReconcileAlreadyTerminalReadsJournalFirst pins the read order that keeps
// the pre-claim terminal check race-free. The writer commits result.json (temp
// + Sync + rename) before appending run_terminal, so a reader wanting both
// facts must read them in reverse; reading result.json first can straddle a
// concurrent reconciler's commit and report a terminal-without-result state
// that never existed on disk (the TestReconcileRace_OneWinner flake).
//
// result.json is planted as a directory: unreadable for every user including
// root, on both unix and Windows. It is a probe, not a reachable state — a
// pre-check that touches it before deciding the journal is non-terminal
// surfaces the read error and fails this test.
func TestReconcileAlreadyTerminalReadsJournalFirst(t *testing.T) {
	run, err := state.Create(t.TempDir(), "run_order_probe")
	if err != nil {
		t.Fatal(err)
	}
	j, err := journal.Create(run.EventsPath(), "run_order_probe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Append(execution.PhaseAdmission, execution.KindRunAccepted, map[string]any{"request_id": "req_probe"}); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(run.ResultPath(), 0o700); err != nil {
		t.Fatal(err)
	}

	out, done, err := reconcileAlreadyTerminal(run)
	if err != nil {
		t.Fatalf("non-terminal journal must decide without reading result.json: %v", err)
	}
	if done {
		t.Fatalf("want reconcile to continue past the pre-check, got %+v", out)
	}
}
