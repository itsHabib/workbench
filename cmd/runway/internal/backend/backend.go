// Package backend is the Runway placement seam (TDD §6): backends propose
// observations; only the controller assigns seq and writes events.
package backend

import (
	"context"

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
	Cwd        string
	Argv       []string
	Env        []string // full child environment, including resolved secrets
	StdoutPath string
	StderrPath string
	Secrets    [][]byte // resolved secret values for log redaction only
	PrivateDir string   // run private/ for durable backend.json (0600)
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
