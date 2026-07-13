package local

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/itsHabib/workbench/cmd/runway/internal/claim"
)

// Allocation is the durable opaque handle written to private/backend.json so
// reconcile can probe and clean up without an in-memory Handle.
type Allocation struct {
	PID        int    `json:"pid"`
	PGID       int    `json:"pgid"`
	StartTicks uint64 `json:"start_ticks"`
}

// CleanupResult is the best-effort outcome of durable allocation cleanup.
type CleanupResult struct {
	// Uncertain is true when a surviving allocation could not be proven gone.
	Uncertain bool
	// AllocationID names the remaining allocation for diagnostics (pid:N).
	AllocationID string
}

const backendFile = "backend.json"

// writeAllocation persists the process-group identity under private/.
func writeAllocation(privateDir string, pid, pgid int) error {
	if privateDir == "" {
		return nil
	}
	ticks, err := claim.StartTicks(pid)
	if err != nil {
		return fmt.Errorf("local: allocation start ticks: %w", err)
	}
	alloc := Allocation{PID: pid, PGID: pgid, StartTicks: ticks}
	data, err := json.Marshal(alloc)
	if err != nil {
		return fmt.Errorf("local: encode allocation: %w", err)
	}
	path := filepath.Join(privateDir, backendFile)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("local: write backend.json: %w", err)
	}
	return nil
}

// CleanupDurable probes private/backend.json and kills the process group only
// when the recorded identity still matches. Uncertain liveness fails closed:
// callers must name the allocation in diagnostics and never report clean.
func CleanupDurable(privateDir string) (CleanupResult, error) {
	path := filepath.Join(privateDir, backendFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CleanupResult{}, nil
		}
		return CleanupResult{}, fmt.Errorf("local: read backend.json: %w", err)
	}
	var alloc Allocation
	if err := json.Unmarshal(data, &alloc); err != nil {
		return CleanupResult{}, fmt.Errorf("local: decode backend.json: %w", err)
	}
	if alloc.PID <= 0 {
		return CleanupResult{}, nil
	}
	id := fmt.Sprintf("pid:%d", alloc.PID)
	if !allocationLive(alloc) {
		return CleanupResult{}, nil
	}
	_ = killGroup(alloc.PGID)
	if !allocationLive(alloc) {
		return CleanupResult{}, nil
	}
	return CleanupResult{Uncertain: true, AllocationID: id}, nil
}

func allocationLive(alloc Allocation) bool {
	if alloc.StartTicks == 0 {
		// Unverifiable start identity — treat as uncertain live if the PID
		// exists at all (fail closed for reconcile diagnostics).
		return pidExists(alloc.PID)
	}
	got, err := claim.StartTicks(alloc.PID)
	if err != nil {
		return false
	}
	return got == alloc.StartTicks
}
