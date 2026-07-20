package local

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend"
	"github.com/itsHabib/workbench/cmd/runway/internal/claim"
	"github.com/itsHabib/workbench/contracts/execution"
)

// Allocation is the durable opaque handle written to private/backend.json so
// reconcile can probe and clean up without an in-memory Handle.
type Allocation struct {
	Backend    string                     `json:"backend,omitempty"`
	PID        int                        `json:"pid"`
	PGID       int                        `json:"pgid"`
	StartTicks uint64                     `json:"start_ticks"`
	Receipt    execution.PlacementReceipt `json:"receipt,omitempty"`
}

const backendFile = "backend.json"

// CleanupResult remains an alias for platform-specific helpers while the
// shared durable-cleanup outcome lives at the backend seam.
type CleanupResult = backend.CleanupResult

// writeAllocation persists the process-group identity under private/.
// An already-exited short-lived child makes StartTicks fail; record
// StartTicks 0 (degraded, unverifiable) and proceed so Wait can report the
// real exit. CleanupDurable fails closed on such allocations.
func writeAllocation(privateDir string, pid, pgid int) error {
	if privateDir == "" {
		return nil
	}
	ticks, err := claim.StartTicks(pid)
	if err != nil {
		ticks = 0
	}
	alloc := Allocation{
		Backend:    "local",
		PID:        pid,
		PGID:       pgid,
		StartTicks: ticks,
		Receipt: execution.PlacementReceipt{
			Backend:        "local",
			Profile:        "default",
			AllocationID:   fmt.Sprintf("pid:%d", pid),
			StreamDelivery: execution.StreamDeliveryNone,
		},
	}
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
func CleanupDurable(privateDir string) (backend.CleanupResult, error) {
	path := filepath.Join(privateDir, backendFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return backend.CleanupResult{}, nil
		}
		return backend.CleanupResult{}, fmt.Errorf("local: read backend.json: %w", err)
	}
	var alloc Allocation
	if err := json.Unmarshal(data, &alloc); err != nil {
		return backend.CleanupResult{}, fmt.Errorf("local: decode backend.json: %w", err)
	}
	if alloc.PID <= 0 {
		return backend.CleanupResult{}, nil
	}
	id := fmt.Sprintf("pid:%d", alloc.PID)
	switch probeAllocation(alloc) {
	case livenessLive:
		return cleanupLiveAllocation(alloc, id)
	case livenessUncertain:
		return backend.CleanupResult{Uncertain: true, AllocationID: id}, nil
	default:
		return cleanupDeadLeader(alloc, id)
	}
}

func cleanupLiveAllocation(alloc Allocation, id string) (backend.CleanupResult, error) {
	_ = killGroup(alloc.PGID)
	if probeAllocation(alloc) != livenessLive {
		return backend.CleanupResult{}, nil
	}
	return backend.CleanupResult{Uncertain: true, AllocationID: id}, nil
}

type liveness int

const (
	livenessDead liveness = iota
	livenessLive
	livenessUncertain
)

// probeAllocation classifies leader liveness. StartTicks errors that are not
// definitive process-gone, and StartTicks 0 with a still-existing PID, are
// uncertain — never clean.
func probeAllocation(alloc Allocation) liveness {
	if alloc.StartTicks == 0 {
		if pidExists(alloc.PID) {
			return livenessUncertain
		}
		return livenessDead
	}
	got, err := claim.StartTicks(alloc.PID)
	if err != nil {
		if pidExists(alloc.PID) {
			return livenessUncertain
		}
		return livenessDead
	}
	if got == alloc.StartTicks {
		return livenessLive
	}
	// PID reused under a different start identity — recorded leader is gone.
	return livenessDead
}
