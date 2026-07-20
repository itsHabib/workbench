package driverstate

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RunSummary is the per-run row returned by Runs: a run identifier and its
// derived status, plus the headline fields from the run_imported event.
// A broken-chain ledger yields Status RunStatusCorrupt so one bad run never
// poisons the whole listing (spec §7 F5).
type RunSummary struct {
	Run        string    `json:"run"`
	Status     string    `json:"status"`
	Repo       string    `json:"repo,omitempty"`
	Source     string    `json:"source,omitempty"`
	ImportedAt time.Time `json:"imported_at,omitempty"`
	// Parent is the parent run id when this run is a child sub-run — empty for a
	// parent or standalone run. It lets a listing filter a run's children
	// (session-orchestrator spec §4 D1).
	Parent string `json:"parent,omitempty"`
}

// Runs lists every started run under dir. It never hard-fails on a single bad
// run: a broken-chain or otherwise unreadable ledger produces a RunSummary
// with Status RunStatusCorrupt while all other runs list normally (spec §7 F5
// — the direct fix for ship's driver list grok-4.5 failure class).
//
// An absent directory is not an error — it returns a nil slice (callers treat
// nil as an empty listing). Directories
// without an events.jsonl (runs that were claimed but never appended to) are
// skipped.
func Runs(dir string) ([]RunSummary, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("driverstate: runs: %w", err)
	}
	var out []RunSummary
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		run := entry.Name()
		if _, statErr := os.Stat(filepath.Join(dir, run, "events.jsonl")); os.IsNotExist(statErr) {
			continue // claimed but never appended; not a started run
		}
		out = append(out, summariseRun(dir, run))
	}
	return out, nil
}

// summariseRun reduces one run into a RunSummary. Any read or chain failure
// yields RunStatusCorrupt so the caller can include this run in the listing
// without propagating the error.
func summariseRun(dir, run string) RunSummary {
	state, err := Reduce(dir, run)
	if err != nil {
		return RunSummary{Run: run, Status: RunStatusCorrupt}
	}
	return RunSummary{
		Run:        run,
		Status:     state.Run.Status,
		Repo:       state.Run.Repo,
		Source:     state.Run.Source,
		ImportedAt: state.Run.ImportedAt,
		Parent:     state.Run.Parent,
	}
}
