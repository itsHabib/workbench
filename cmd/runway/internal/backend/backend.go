// Package backend is the Runway placement seam (TDD §6): backends propose
// observations; only the controller assigns seq and writes events.
package backend

import (
	"context"
	"errors"
	"fmt"
	"time"

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

// CustodyRequest asks a backend to resolve custody: secret refs into injectable
// child tokens at placement time (grant-materialized rooms §4). Deadline and
// Grace bound the derived child's TTL (D4); Now anchors the cap deterministically.
type CustodyRequest struct {
	Secrets  []execution.Secret
	Deadline time.Time
	Grace    time.Duration
	Now      time.Time
	// PrivateDir is the run's private dir. The resolver persists a token-free
	// copy of the derive records there so the room-authority receipt can still
	// assemble after controller death (§7 F). Empty disables persistence.
	PrivateDir string
}

// CustodyResolution is the outcome of resolving custody: refs — the environment
// additions the guest needs (CUSTODY_GRANT_<KEY> / CUSTODY_BASE_<KEY>, D6), the
// child-token bytes to redact from captured logs, and an opaque records handle
// the controller carries back to AssembleAuthorityReceipt at collection.
type CustodyResolution struct {
	Env     map[string]string
	Redact  [][]byte
	Records any
}

// CustodyResolver is an optional backend capability: resolve custody: secret
// refs (live parent-grant lookup + attenuated, source-bound derive). A backend
// that does not implement it cannot place custody-scoped authority — the
// controller refuses such a request at admission with a coded unsupported error.
type CustodyResolver interface {
	ResolveCustody(ctx context.Context, req CustodyRequest) (CustodyResolution, error)
}

// AuthorityReceiptInputs carries the durable, at-collection facts the receipt
// joins to the derive records: identity, the collected Result artifacts whose
// digests become evidence refs, and whether teardown destroyed the room.
type AuthorityReceiptInputs struct {
	RunID        string
	AllocationID string
	ArtifactsDir string
	Artifacts    []execution.Artifact
	TeardownOK   bool
	// TeardownAt is the teardown instant, supplied by the caller (never read from
	// the wall clock inside assembly) so re-collection from the same durable
	// inputs rewrites a byte-identical line (§8).
	TeardownAt time.Time
}

// AuthorityReceipter is an optional backend capability: assemble and persist the
// room-authority receipt (grant-materialized rooms §5) from records produced by
// ResolveCustody plus AuthorityReceiptInputs, returning the naming artifact so
// the controller lists it in Result.Artifacts. Assembly is idempotent from
// durable inputs.
type AuthorityReceipter interface {
	AssembleAuthorityReceipt(records any, in AuthorityReceiptInputs) (execution.Artifact, error)
}

// AuthorityUnresolved is a placement refusal: no live parent grant covers a
// custody: ref's key+actions, so no room boots and no secret falls back to a
// raw value (grant-materialized rooms §7 B, FR6). It carries the exact operator
// remedy for the run's diagnostics.
type AuthorityUnresolved struct {
	Ref    string
	Reason string
	Remedy string
}

func (e *AuthorityUnresolved) Error() string {
	return fmt.Sprintf("authority_unresolved: %s (ref %s); remedy: %s", e.Reason, e.Ref, e.Remedy)
}

// AsAuthorityUnresolved reports whether err is an authority-unresolved refusal.
func AsAuthorityUnresolved(err error) (*AuthorityUnresolved, bool) {
	var u *AuthorityUnresolved
	if errors.As(err, &u) {
		return u, true
	}
	return nil, false
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
