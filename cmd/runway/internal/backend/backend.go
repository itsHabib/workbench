// Package backend is the Runway placement seam (TDD §6): backends propose
// observations; only the controller assigns seq and writes events.
package backend

import (
	"context"
	"errors"
	"fmt"

	"github.com/itsHabib/workbench/contracts/execution"
)

// Emit proposes one observation to the controller. The controller assigns
// the contiguous seq and writes the durable event; backends must not touch
// the journal.
type Emit func(phase, kind string, details map[string]any) error

// Exit is a workload process exit observation.
type Exit struct {
	Code int
}

// Handle is opaque outside its backend package.
type Handle any

// PreparedRun is the fully expanded, secret-resolved start surface. Argv is
// never a shell line; paths are already native.
type PreparedRun struct {
	RunID      string
	Work       execution.WorkSpec
	Cwd        string
	Argv       []string
	Env        []string // full child environment, including resolved secrets
	Workspace  string   // native workspace root
	Inputs     string   // native materialized-input root
	Out        string   // native artifact root
	StdoutPath string
	StderrPath string
	Secrets    [][]byte // resolved secret values for log redaction only
	PrivateDir string   // run private/ for durable backend.json (0600)
}

// PlacementUnavailable is normal startup backpressure, not a mechanism
// failure. Backends return it when a placement was valid but no capacity was
// available for this attempt.
type PlacementUnavailable struct {
	Backend string
	Cap     int
}

func (e *PlacementUnavailable) Error() string {
	if e.Cap > 0 {
		return fmt.Sprintf("%s: placement unavailable (capacity %d)", e.Backend, e.Cap)
	}
	return fmt.Sprintf("%s: placement unavailable", e.Backend)
}

// IsPlacementUnavailable reports structured placement backpressure.
func IsPlacementUnavailable(err error) bool {
	var unavailable *PlacementUnavailable
	return errors.As(err, &unavailable)
}

// CleanupResult is the durable best-effort cleanup outcome used by reconcile.
type CleanupResult struct {
	Uncertain    bool
	AllocationID string
}

// Admitter is an optional backend-local admission law. It validates a
// portable work spec against a resolved profile before a run directory is
// created (for example, supported secret names).
type Admitter interface {
	Admit(execution.WorkSpec) error
}

// Backend is the placement seam. Collect/Cleanup exist for later lifecycle
// policy; this package defines the contract only.
type Backend interface {
	Start(ctx context.Context, prep PreparedRun, emit Emit) (Handle, error)
	Wait(ctx context.Context, h Handle, emit Emit) (Exit, error)
	Cancel(ctx context.Context, h Handle) error
	Collect(ctx context.Context, h Handle, outDir string) ([]execution.Artifact, error)
	Cleanup(ctx context.Context, h Handle) error
}
