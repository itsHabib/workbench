// Package driverstate is the contract home for driver-state events: the typed
// event vocabulary a writer records as a driver run moves through its
// lifecycle, the kind-specific body payloads, the reducer output shapes, and
// the embedded JSON schema every emitter — Go or ship's TS — writes against.
//
// It is a leaf. It imports nothing else in the module and carries no decision
// logic: no Append, no Reduce, no lease/lock, no MCP surface. Those are the
// driverstate mechanism package (P2) and the workbench-mcp server (P3); this
// package is only the shared vocabulary, mirroring the contracts verdict-v0.3.0
// and contracts/execution pattern. Share contracts, not call stacks.
//
// The behavioral source of truth is docs/features/driver-state/spec.md §5
// (event schema + canonical encoding) and §6 (state machine / reducer output
// shapes). The types here are the ergonomic view of the embedded schema; the
// conformance tests keep the two in lockstep, and the pinned canonical-encoding
// reference vector (testdata/canonical-vector.json) is the cross-language chain
// anchor ship's independent TS emitter must reproduce byte-for-byte.
//
// Readers are tolerant: an unknown kind decodes without error and ReadLedger
// skips it with a warning rather than failing a listing (the driver list
// grok-4.5 lesson). Body types decode tolerantly too, so a field addition is
// never breaking.
package driverstate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Event is one driver-state lifecycle event: append-only, one file per run,
// hash-chained (Prev links the chain, Hash seals the line). Field declaration
// order IS the canonical-encoding order (see canonical.go). Body stays raw
// until a reader knows the Kind.
type Event struct {
	ID   string `json:"id"`
	Run  string `json:"run"`
	V    string `json:"v"`
	Kind Kind   `json:"kind"`
	// Stream is dss_<ulid>; empty for run-scoped kinds.
	Stream string `json:"stream,omitempty"`
	// Time is writer-supplied; append enforces per-run monotonicity (P2).
	// Writers must truncate to whole seconds: the canonical encoding uses
	// RFC 3339 as marshalled, so sub-second precision would have to be
	// reproduced bit-for-bit by every other-language emitter to keep the
	// chain stable. P2's Append enforces the truncation.
	Time time.Time `json:"time"`
	// Actor is "session:<id>" | "ship:<drv_id>" | "human:<who>".
	Actor string `json:"actor"`
	// ExtRef is the optional top-level external correlate (ship drv_id on
	// run_imported, PR URL on stream_pr_opened) — top-level, not body, so
	// cross-store queries don't parse every body (spec §10 Q2).
	ExtRef string          `json:"ext_ref,omitempty"`
	Body   json.RawMessage `json:"body"`
	Prev   string          `json:"prev"`
	Hash   string          `json:"hash"`
}

// Kind names one lifecycle transition. It is a string so an unknown future kind
// decodes without error; Known reports membership in the v0 set.
type Kind string

// The v0 event kinds. run_imported and run_finished are run-scoped (Stream
// empty); every other kind is stream-scoped.
const (
	KindRunImported      Kind = "run_imported"
	KindStreamDispatched Kind = "stream_dispatched"
	KindStreamAttempt    Kind = "stream_attempt"
	KindReviewCycle      Kind = "review_cycle"
	KindStreamPROpened   Kind = "stream_pr_opened"
	KindStreamLanded     Kind = "stream_landed"
	KindStreamFailed     Kind = "stream_failed"
	KindStreamSkipped    Kind = "stream_skipped"
	KindStreamMerged     Kind = "stream_merged"
	KindRunFinished      Kind = "run_finished"
)

// AllKinds is the known-v0 kind set, in schema-enum order. Enum↔const parity is
// conformance-tested against the embedded schema.
func AllKinds() []Kind {
	return []Kind{
		KindRunImported,
		KindStreamDispatched,
		KindStreamAttempt,
		KindReviewCycle,
		KindStreamPROpened,
		KindStreamLanded,
		KindStreamFailed,
		KindStreamSkipped,
		KindStreamMerged,
		KindRunFinished,
	}
}

var knownKinds = func() map[Kind]bool {
	m := make(map[Kind]bool, len(AllKinds()))
	for _, k := range AllKinds() {
		m[k] = true
	}
	return m
}()

// Known reports whether k is a kind this contract version defines. A reader
// uses it to skip unknown future kinds tolerantly.
func (k Kind) Known() bool { return knownKinds[k] }

// Derived stream statuses (spec §6). Status is always a reducer OUTPUT, never
// stored on an event — there is no row to drift. These are vocabulary values;
// the reducer that computes them lives in the P2 mechanism package.
const (
	StatusPending    = "pending"
	StatusDispatched = "dispatched"
	StatusLanded     = "landed"
	StatusPROpen     = "pr_open"
	StatusMerged     = "merged"
	StatusFailed     = "failed"
	StatusSkipped    = "skipped"
)

// Done boundaries (session-orchestrator spec §4 D7): the per-run policy for how
// a stream reaches DONE. Merged is the pers/ default (drive through the gate to
// a merge); PROpen stops at an open PR (local-only / human-merge repos — no
// cloud dispatch, no bot reviewers, no gate merge); Green additionally waits for
// the local gate/CI to report green before stopping. An empty done_boundary
// reads as Merged (DoneBoundaryOrDefault). No boundary needs a new event or a
// state-machine change — it only governs the drive.
const (
	DoneBoundaryGreen  = "green"
	DoneBoundaryPROpen = "pr-open"
	DoneBoundaryMerged = "merged"
)

// DoneBoundaryOrDefault maps an empty boundary to the Merged default and passes
// any set value through unchanged — one place so readers never re-derive the
// default.
func DoneBoundaryOrDefault(b string) string {
	if b == "" {
		return DoneBoundaryMerged
	}
	return b
}

// RunImportedBody is the manifest snapshot at import. Manifest carries the
// driver.md frontmatter VERBATIM (render round-trips, and a run with only
// run_imported resumes from it — spec §7 F3); Repo/Source/Streams are the
// typed essentials readers index without parsing the snapshot. ShipRunRef is
// the optional ship drv_id correlate (spec §10 Q2 — field included, semantics
// deferred). Parent/ParentStream link a child sub-run up to the parent run and
// stream it implements (session-orchestrator spec §4 D1); both are empty on a
// parent or standalone run. DoneBoundary is the per-run done policy (§4 D7).
type RunImportedBody struct {
	Repo         string          `json:"repo"`
	Source       string          `json:"source"`
	GeneratedAt  string          `json:"generated_at,omitempty"`
	Manifest     json.RawMessage `json:"manifest"`
	Streams      []StreamSpec    `json:"streams"`
	ShipRunRef   string          `json:"ship_run_ref,omitempty"`
	Parent       string          `json:"parent,omitempty"`
	ParentStream string          `json:"parent_stream,omitempty"`
	DoneBoundary string          `json:"done_boundary,omitempty"`
}

// StreamSpec is one stream in the imported manifest snapshot.
type StreamSpec struct {
	Stream  string `json:"stream"`
	DocPath string `json:"doc_path"`
	Batch   int    `json:"batch,omitempty"`
}

// StreamDispatchedBody is the locator payload written when a stream is
// dispatched into a worktree. Every field is optional: older ledgers omit
// them, and a dispatch without a recovered locator still validates.
type StreamDispatchedBody struct {
	// Branch is the git branch the dispatch opened, when known.
	Branch string `json:"branch,omitempty"`
	// Worktree is the absolute path of the dispatch worktree, when known.
	Worktree string `json:"worktree,omitempty"`
	// Engine names the dispatching engine (e.g. "session") — live ledgers
	// already carry it, so the typed body must too.
	Engine string `json:"engine,omitempty"`
	// ChildRun is the child sub-run (dsr_…) this parent stream delegated its
	// impl to (session-orchestrator spec §4 D1) — the join link the rollup
	// reads. Empty on a non-delegating dispatch.
	ChildRun string `json:"child_run,omitempty"`
	// WorktreeConflict records that this dispatch hit (and resolved) a
	// worktree/branch collision — a per-child friction flag (spec §4 D4).
	WorktreeConflict bool `json:"worktree_conflict,omitempty"`
}

// StreamAttemptBody is one dispatch attempt on a stream. Seq is append-only
// monotone (P2 enforces increase). A terminal attempt with FailureCategory set
// lands the stream in failed; absent it, in landed.
type StreamAttemptBody struct {
	Seq             int    `json:"seq"`
	DocPath         string `json:"doc_path"`
	Terminal        bool   `json:"terminal"`
	FailureCategory string `json:"failure_category,omitempty"`
	// Commit is the head commit the attempt produced, when known — used by
	// resume to match ledger state to a PR head SHA.
	Commit string `json:"commit,omitempty"`
}

// StreamPROpenedBody records the PR a landed stream opened.
type StreamPROpenedBody struct {
	PR      int    `json:"pr"`
	URL     string `json:"url"`
	HeadSHA string `json:"head_sha"`
}

// StreamMergedBody records the merge that closed a stream's PR.
type StreamMergedBody struct {
	PR          int    `json:"pr"`
	MergeCommit string `json:"merge_commit"`
	MergedAt    string `json:"merged_at"`
}

// ReviewCycleBody records one review cycle on an open PR — first-class from
// v0.1.0 because cycles are unreconstructable from coarse attempt snapshots.
type ReviewCycleBody struct {
	Cycle        int  `json:"cycle"`
	PanelSettled bool `json:"panel_settled"`
	Findings     int  `json:"findings"`
}

// RunState is the reducer's output view of a run: the run record plus every
// stream's derived record. There is NO reducer here — this is the output shape
// the P2 Reduce fills. Status fields are always derived, never stored.
type RunState struct {
	Run     RunRecord               `json:"run"`
	Streams map[string]StreamRecord `json:"streams"`
}

// RunRecord is a run's derived summary. Parent/ParentStream/DoneBoundary are
// folded from run_imported (empty parent = a parent or standalone run;
// DoneBoundary reads through DoneBoundaryOrDefault at the call site).
type RunRecord struct {
	Repo         string    `json:"repo"`
	Source       string    `json:"source"`
	Status       string    `json:"status"`
	ImportedAt   time.Time `json:"imported_at"`
	Parent       string    `json:"parent,omitempty"`
	ParentStream string    `json:"parent_stream,omitempty"`
	DoneBoundary string    `json:"done_boundary,omitempty"`
}

// StreamRecord is a stream's derived summary: current status plus the facts
// folded from its events.
type StreamRecord struct {
	Status      string          `json:"status"`
	Attempts    []AttemptRecord `json:"attempts,omitempty"`
	PR          int             `json:"pr,omitempty"`
	URL         string          `json:"url,omitempty"`
	MergeCommit string          `json:"merge_commit,omitempty"`
	// Branch is folded from stream_dispatched — the dispatch branch locator.
	Branch string `json:"branch,omitempty"`
	// Worktree is folded from stream_dispatched — the dispatch worktree locator.
	Worktree string `json:"worktree,omitempty"`
	// ChildRun is folded from stream_dispatched.child_run — the sub-run this
	// stream delegated its impl to (session-orchestrator spec §4 D1).
	ChildRun string `json:"child_run,omitempty"`
	// ReviewCycles is the count of review_cycle events folded onto this stream —
	// the gate-loop count surfaced for friction (spec §4 D4). It is a derived
	// counter, not a stored field.
	ReviewCycles int `json:"review_cycles,omitempty"`
	// WorktreeConflict is folded from stream_dispatched.worktree_conflict.
	WorktreeConflict bool `json:"worktree_conflict,omitempty"`
}

// AttemptRecord is one folded attempt in a StreamRecord.
type AttemptRecord struct {
	Seq             int    `json:"seq"`
	Terminal        bool   `json:"terminal"`
	FailureCategory string `json:"failure_category,omitempty"`
	// Commit is folded from stream_attempt — the head commit the attempt produced.
	Commit string `json:"commit,omitempty"`
}

// DecodeEvent is the tolerant reader for a single event: unknown additive
// fields decode, and an unknown Kind decodes without error (Known reports it).
// A wrong contract version rejects loudly — a reader that predates a major bump
// must not silently mis-read.
func DecodeEvent(data []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(data, &e); err != nil {
		return Event{}, fmt.Errorf("driverstate: decode event: %w", err)
	}
	if e.V != Version {
		return Event{}, fmt.Errorf("driverstate: %w: got %q, this reader accepts %q", ErrUnknownVersion, e.V, Version)
	}
	return e, nil
}

// ReadLedger decodes a run's JSONL event stream tolerantly: known-kind events
// return in file order, unknown-kind events are SKIPPED with a warning (never
// an error), and a malformed line fails loudly. A wrong contract version also
// fails loudly — same law as DecodeEvent; kind tolerance is for additive growth
// within a version, never across one. It reduces nothing — it only partitions
// the vocabulary, so it stays decision-free and leaf-safe.
func ReadLedger(data []byte) ([]Event, []string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var events []Event
	var warnings []string
	for {
		var e Event
		err := dec.Decode(&e)
		if err == io.EOF {
			return events, warnings, nil
		}
		if err != nil {
			return nil, nil, fmt.Errorf("driverstate: read ledger: %w", err)
		}
		if e.V != Version {
			return nil, nil, fmt.Errorf("driverstate: read ledger: %w: got %q at event %q, this reader accepts %q", ErrUnknownVersion, e.V, e.ID, Version)
		}
		if !e.Kind.Known() {
			warnings = append(warnings, fmt.Sprintf("skipped unknown kind %q at event %q", e.Kind, e.ID))
			continue
		}
		events = append(events, e)
	}
}
