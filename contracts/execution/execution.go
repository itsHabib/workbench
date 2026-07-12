// Package execution is the Runway execution-contract vocabulary: the four
// wire shapes — portable work spec, placed run request, run event, terminal
// result — that let a caller compile domain intent into one explicit work
// bundle, one placement binding, one ordered lifecycle, and one terminal
// receipt.
//
// It is a leaf. It imports nothing else in the module and carries no decision
// logic: no routing, no lifecycle transitions, no backend resolution — those
// belong to cmd/runway. Types here are the ergonomic view of the embedded
// JSON Schemas; the conformance tests keep the two in lockstep.
//
// Readers are tolerant (FR14): unknown additive fields decode without error,
// and an unrecognized schema_version rejects loudly via the Decode functions.
// The contract is provider-neutral by law (FR2): no agent, provider, or
// host-path field exists in any shape, and placement backends are open
// vocabulary, never an enum.
package execution

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrUnknownSchemaVersion is the version-gate failure: the instance declares a
// schema_version this reader does not understand. Callers branch with
// errors.Is.
var ErrUnknownSchemaVersion = errors.New("unrecognized schema_version")

// checkVersion accepts only the one version these schemas ship. There is
// exactly one version today; a real compatibility rule is decided if and when
// a 0.2.0 first exists, from evidence.
func checkVersion(schemaVersion string) error {
	if schemaVersion == SchemaVersion {
		return nil
	}
	return fmt.Errorf("execution: %w: got %q, this reader accepts %q", ErrUnknownSchemaVersion, schemaVersion, SchemaVersion)
}

// WorkSpec is the portable work bundle: a structured command over logical
// roots, an immutable workspace, and declared inputs, secret references, and
// outputs. It carries no placement — the same work binds to any backend.
type WorkSpec struct {
	SchemaVersion string    `json:"schema_version"`
	Command       Command   `json:"command"`
	Cwd           PathRef   `json:"cwd"`
	Workspace     Workspace `json:"workspace"`
	Inputs        []Input   `json:"inputs,omitempty"`
	Secrets       []Secret  `json:"secrets,omitempty"`
	Outputs       []Output  `json:"outputs,omitempty"`
}

// Command is structured argv — never a shell line, never string interpolation.
type Command struct {
	Executable Executable `json:"executable"`
	Args       []Arg      `json:"args,omitempty"`
}

// Executable is a discriminated union: exactly one of Name (resolved through
// the placement profile's PATH) or Path (a structured reference into a logical
// root) is set. The XOR law is enforced by admission validation, not decode.
type Executable struct {
	Name *string  `json:"name,omitempty"`
	Path *PathRef `json:"path,omitempty"`
}

// Arg is a discriminated union: exactly one of Literal or Path is set.
// Literal is a pointer so an empty-string argument survives a round trip.
type Arg struct {
	Literal *string  `json:"literal,omitempty"`
	Path    *PathRef `json:"path,omitempty"`
}

// PathRef is a structured reference into a named logical root (FR3). The
// selected backend expands it to a native path; the contract never carries a
// host path.
type PathRef struct {
	Root  string `json:"root"`
	Value string `json:"value"`
}

// Workspace names the immutable code the work runs against.
type Workspace struct {
	Kind     string `json:"kind"`
	URL      string `json:"url"`
	Revision string `json:"revision"`
}

// Input declares one bundle file by logical source plus digest (FR15). Source
// and Target are bare relative paths beneath fixed roots, not PathRefs.
type Input struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Target string `json:"target"`
	SHA256 string `json:"sha256"`
}

// Secret is an opaque reference, never a value (D8, FR12).
type Secret struct {
	Name string `json:"name"`
	Ref  string `json:"ref"`
}

// Output declares one expected workload product beneath the fixed out root.
type Output struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Required bool   `json:"required"`
}

// Request is the placed run request: portable work plus policy plus an
// explicit placement binding (FR1).
type Request struct {
	SchemaVersion string    `json:"schema_version"`
	RequestID     string    `json:"request_id"`
	Work          Work      `json:"work"`
	Placement     Placement `json:"placement"`
	Policy        Policy    `json:"policy"`
}

// Work names the submitted work manifest and the digest of its exact bytes.
type Work struct {
	Manifest string `json:"manifest"`
	SHA256   string `json:"sha256"`
}

// Placement binds a run to a backend and a backend-local profile. Backend is
// open vocabulary resolved against installed adapters — never an enum (D4).
type Placement struct {
	Backend string `json:"backend"`
	Profile string `json:"profile"`
}

// Policy is the caller's execution policy for one run.
type Policy struct {
	DeadlineMS    int64 `json:"deadline_ms"`
	CancelGraceMS int64 `json:"cancel_grace_ms"`
}

// RunEvent is one canonical lifecycle event (FR6): contiguous seq from 1,
// monotone phase order, run_terminal always last. Kind is open vocabulary —
// kinds are additive within a major schema.
type RunEvent struct {
	SchemaVersion string         `json:"schema_version"`
	RunID         string         `json:"run_id"`
	Seq           int64          `json:"seq"`
	Time          string         `json:"time"`
	Phase         string         `json:"phase"`
	Kind          string         `json:"kind"`
	Message       string         `json:"message,omitempty"`
	Details       map[string]any `json:"details,omitempty"`
}

// Result is the terminal receipt — at most one per accepted request (FR7).
// Callers branch on Status, TerminalPhase, and ReasonCode, never on messages.
type Result struct {
	SchemaVersion    string           `json:"schema_version"`
	RunID            string           `json:"run_id"`
	RequestID        string           `json:"request_id"`
	RequestSHA256    string           `json:"request_sha256"`
	WorkSHA256       string           `json:"work_sha256"`
	Status           string           `json:"status"`
	TerminalPhase    string           `json:"terminal_phase"`
	ReasonCode       string           `json:"reason_code"`
	StartedAt        string           `json:"started_at"`
	EndedAt          string           `json:"ended_at"`
	WorkloadExitCode *int64           `json:"workload_exit_code,omitempty"`
	Placement        PlacementReceipt `json:"placement"`
	Causes           []Cause          `json:"causes"`
	Diagnostics      []Diagnostic     `json:"diagnostics"`
	Artifacts        []Artifact       `json:"artifacts"`
}

// PlacementReceipt records what backend profile and immutable inputs actually
// ran (FR5). It is non-authoritative (D6): consumers may display it but cannot
// infer generic state from it.
type PlacementReceipt struct {
	Backend        string         `json:"backend"`
	Profile        string         `json:"profile"`
	AllocationID   string         `json:"allocation_id"`
	ImageSHA256    string         `json:"image_sha256,omitempty"`
	StreamDelivery string         `json:"stream_delivery"`
	Enforced       map[string]any `json:"enforced,omitempty"`
	Details        map[string]any `json:"details,omitempty"`
}

// Cause is one prior (phase, reason_code) pair retained when a later failure
// became primary, such as cleanup failure after deadline expiry.
type Cause struct {
	Phase      string `json:"phase"`
	ReasonCode string `json:"reason_code"`
	Message    string `json:"message,omitempty"`
}

// Diagnostic is a structured record that may name an uncertain allocation
// without changing primary status.
type Diagnostic struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Artifact names one collected workload product with its exact-byte digest.
type Artifact struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Canonical lifecycle phases — a closed enum; phase order is monotone and the
// reducer's monotonicity law keys on this ordering.
const (
	PhaseAdmission   = "admission"
	PhasePreparation = "preparation"
	PhaseStartup     = "startup"
	PhaseWorkload    = "workload"
	PhaseCollection  = "collection"
	PhaseCleanup     = "cleanup"
	PhaseTerminal    = "terminal"
)

// Terminal statuses — a closed enum.
const (
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusTimedOut  = "timed_out"
	StatusCancelled = "cancelled"
)

// Stable reason codes — a closed enum.
const (
	ReasonCompleted            = "completed"
	ReasonPreparationFailed    = "preparation_failed"
	ReasonStartupFailed        = "startup_failed"
	ReasonWorkloadFailed       = "workload_failed"
	ReasonDeadlineExceeded     = "deadline_exceeded"
	ReasonCancelRequested      = "cancel_requested"
	ReasonCollectionFailed     = "collection_failed"
	ReasonCleanupFailed        = "cleanup_failed"
	ReasonControllerLost       = "controller_lost"
	ReasonPlacementUnavailable = "placement_unavailable"
)

// Required v0 event kinds. Kind is an OPEN vocabulary — additive within a
// major schema — so these are values only, deliberately not a schema enum.
const (
	KindRunAccepted        = "run_accepted"
	KindPlacementAllocated = "placement_allocated"
	KindWorkloadReady      = "workload_ready"
	KindWorkloadStarted    = "workload_started"
	KindWorkloadExited     = "workload_exited"
	KindArtifactCollected  = "artifact_collected"
	KindCleanupCompleted   = "cleanup_completed"
	KindRunTerminal        = "run_terminal"
)

// Logical roots a PathRef may name — a closed enum.
const (
	RootWorkspace = "workspace"
	RootInputs    = "inputs"
	RootOut       = "out"
)

// WorkspaceKindGit is the one workspace kind v0 defines.
const WorkspaceKindGit = "git"

// Stream-delivery modes for declared workload event artifacts. "live" is
// reserved and not valid in v0 (D11).
const (
	StreamDeliveryTerminalReplay = "terminal_replay"
	StreamDeliveryNone           = "none"
)

// DecodeWorkSpec is the tolerant reader for a work spec: unknown additive
// fields decode, an unrecognized schema_version rejects loudly (FR14).
func DecodeWorkSpec(data []byte) (WorkSpec, error) {
	var w WorkSpec
	if err := json.Unmarshal(data, &w); err != nil {
		return WorkSpec{}, fmt.Errorf("execution: decode work spec: %w", err)
	}
	if err := checkVersion(w.SchemaVersion); err != nil {
		return WorkSpec{}, err
	}
	return w, nil
}

// DecodeRequest is the tolerant reader for a placed run request.
func DecodeRequest(data []byte) (Request, error) {
	var r Request
	if err := json.Unmarshal(data, &r); err != nil {
		return Request{}, fmt.Errorf("execution: decode request: %w", err)
	}
	if err := checkVersion(r.SchemaVersion); err != nil {
		return Request{}, err
	}
	return r, nil
}

// DecodeEvent is the tolerant reader for a run event.
func DecodeEvent(data []byte) (RunEvent, error) {
	var e RunEvent
	if err := json.Unmarshal(data, &e); err != nil {
		return RunEvent{}, fmt.Errorf("execution: decode event: %w", err)
	}
	if err := checkVersion(e.SchemaVersion); err != nil {
		return RunEvent{}, err
	}
	return e, nil
}

// DecodeResult is the tolerant reader for a terminal result.
func DecodeResult(data []byte) (Result, error) {
	var r Result
	if err := json.Unmarshal(data, &r); err != nil {
		return Result{}, fmt.Errorf("execution: decode result: %w", err)
	}
	if err := checkVersion(r.SchemaVersion); err != nil {
		return Result{}, err
	}
	return r, nil
}
