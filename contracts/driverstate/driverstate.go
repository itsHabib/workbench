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

// RunImportedBody is the manifest snapshot at import: the driver.md frontmatter
// verbatim, so render round-trips. ShipRunRef is the optional ship drv_id
// correlate (spec §10 Q2 — field included, semantics deferred).
type RunImportedBody struct {
	Repo        string       `json:"repo"`
	Source      string       `json:"source"`
	GeneratedAt string       `json:"generated_at,omitempty"`
	Streams     []StreamSpec `json:"streams"`
	ShipRunRef  string       `json:"ship_run_ref,omitempty"`
}

// StreamSpec is one stream in the imported manifest snapshot.
type StreamSpec struct {
	Stream  string `json:"stream"`
	DocPath string `json:"doc_path"`
	Batch   int    `json:"batch,omitempty"`
}

// StreamAttemptBody is one dispatch attempt on a stream. Seq is append-only
// monotone (P2 enforces increase). A terminal attempt with FailureCategory set
// lands the stream in failed; absent it, in landed.
type StreamAttemptBody struct {
	Seq             int    `json:"seq"`
	DocPath         string `json:"doc_path"`
	Terminal        bool   `json:"terminal"`
	FailureCategory string `json:"failure_category,omitempty"`
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

// RunRecord is a run's derived summary.
type RunRecord struct {
	Repo       string    `json:"repo"`
	Source     string    `json:"source"`
	Status     string    `json:"status"`
	ImportedAt time.Time `json:"imported_at"`
}

// StreamRecord is a stream's derived summary: current status plus the facts
// folded from its events.
type StreamRecord struct {
	Status      string          `json:"status"`
	Attempts    []AttemptRecord `json:"attempts,omitempty"`
	PR          int             `json:"pr,omitempty"`
	URL         string          `json:"url,omitempty"`
	MergeCommit string          `json:"merge_commit,omitempty"`
}

// AttemptRecord is one folded attempt in a StreamRecord.
type AttemptRecord struct {
	Seq             int    `json:"seq"`
	Terminal        bool   `json:"terminal"`
	FailureCategory string `json:"failure_category,omitempty"`
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
