package driverstate

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrUnknownVersion is the version-gate failure: the event declares a contract
// version this reader does not understand. Callers branch with errors.Is.
var ErrUnknownVersion = errors.New("unrecognized driver-state version")

// runScoped is the set of kinds that carry no stream (Stream must be empty);
// every other known kind is stream-scoped (Stream must be set).
var runScoped = map[Kind]bool{
	KindRunImported: true,
	KindRunFinished: true,
}

// ValidateEvent enforces the event-envelope laws JSON Schema cannot express at
// runtime in a stdlib-only module: the version gate, identity presence, the
// run-scoped/stream-scoped rule, and the per-kind body grammar. It is contract
// law, not a decision — it never chooses a transition (that is the P2 reducer's
// state machine). An unknown Kind is tolerated: presence rules still apply, but
// its body is not second-guessed.
func ValidateEvent(e Event) error {
	if e.V != Version {
		return fmt.Errorf("driverstate: %w: got %q, this reader accepts %q", ErrUnknownVersion, e.V, Version)
	}
	if e.ID == "" {
		return fmt.Errorf("driverstate: id is empty; a client-minted idempotency key is required")
	}
	if e.Run == "" {
		return fmt.Errorf("driverstate: run is empty; an event must name its run")
	}
	if e.Actor == "" {
		return fmt.Errorf("driverstate: actor is empty; an event must name who wrote it")
	}
	if err := checkScope(e.Kind, e.Stream); err != nil {
		return err
	}
	return ValidateBody(e.Kind, e.Body)
}

// checkScope enforces the run-scoped/stream-scoped rule for known kinds. An
// unknown kind is left alone — a reader tolerates it rather than guessing its
// scope.
func checkScope(kind Kind, stream string) error {
	if !kind.Known() {
		return nil
	}
	if runScoped[kind] && stream != "" {
		return fmt.Errorf("driverstate: kind %q is run-scoped but carries stream %q", kind, stream)
	}
	if !runScoped[kind] && stream == "" {
		return fmt.Errorf("driverstate: kind %q is stream-scoped but carries no stream", kind)
	}
	return nil
}

// ValidateBody decodes a kind's body tolerantly and enforces its payload
// grammar — the shapes spec §5 pins. An unknown kind, or a known kind whose
// body the spec leaves open, validates permissively. A malformed body for a
// pinned kind rejects loudly.
func ValidateBody(kind Kind, body json.RawMessage) error {
	switch kind {
	case KindRunImported:
		return validateRunImported(body)
	case KindStreamAttempt:
		return validateStreamAttempt(body)
	case KindStreamPROpened:
		return validateStreamPROpened(body)
	case KindStreamMerged:
		return validateStreamMerged(body)
	case KindReviewCycle:
		return validateReviewCycle(body)
	default:
		return nil
	}
}

func unmarshalBody(kind Kind, body json.RawMessage, into any) error {
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("driverstate: %s body: %w", kind, err)
	}
	return nil
}

// Presence probes: decoding a required boolean/array/int into a value struct
// loses whether the member was present at all, so the pinned-grammar validators
// decode into pointer-field shadows and reject nil — the schema's `required`
// list, enforced at runtime.
type runImportedProbe struct {
	Streams *[]StreamSpec `json:"streams"`
}

type streamAttemptProbe struct {
	Terminal *bool `json:"terminal"`
}

type reviewCycleProbe struct {
	PanelSettled *bool `json:"panel_settled"`
	Findings     *int  `json:"findings"`
}

func validateRunImported(body json.RawMessage) error {
	var b RunImportedBody
	if err := unmarshalBody(KindRunImported, body, &b); err != nil {
		return err
	}
	var probe runImportedProbe
	if err := unmarshalBody(KindRunImported, body, &probe); err != nil {
		return err
	}
	if probe.Streams == nil {
		return fmt.Errorf("driverstate: run_imported body: streams is missing")
	}
	if b.Repo == "" {
		return fmt.Errorf("driverstate: run_imported body: repo is empty")
	}
	if b.Source == "" {
		return fmt.Errorf("driverstate: run_imported body: source is empty")
	}
	for i, s := range b.Streams {
		if s.Stream == "" {
			return fmt.Errorf("driverstate: run_imported body: streams[%d].stream is empty", i)
		}
		if s.DocPath == "" {
			return fmt.Errorf("driverstate: run_imported body: streams[%d].doc_path is empty", i)
		}
	}
	return nil
}

func validateStreamAttempt(body json.RawMessage) error {
	var b StreamAttemptBody
	if err := unmarshalBody(KindStreamAttempt, body, &b); err != nil {
		return err
	}
	var probe streamAttemptProbe
	if err := unmarshalBody(KindStreamAttempt, body, &probe); err != nil {
		return err
	}
	if probe.Terminal == nil {
		return fmt.Errorf("driverstate: stream_attempt body: terminal is missing")
	}
	if b.Seq < 1 {
		return fmt.Errorf("driverstate: stream_attempt body: seq %d must be at least 1", b.Seq)
	}
	if b.DocPath == "" {
		return fmt.Errorf("driverstate: stream_attempt body: doc_path is empty")
	}
	if b.FailureCategory != "" && !b.Terminal {
		return fmt.Errorf("driverstate: stream_attempt body: failure_category is set on a non-terminal attempt")
	}
	return nil
}

func validateStreamPROpened(body json.RawMessage) error {
	var b StreamPROpenedBody
	if err := unmarshalBody(KindStreamPROpened, body, &b); err != nil {
		return err
	}
	if b.PR < 1 {
		return fmt.Errorf("driverstate: stream_pr_opened body: pr %d must be at least 1", b.PR)
	}
	if b.URL == "" {
		return fmt.Errorf("driverstate: stream_pr_opened body: url is empty")
	}
	if b.HeadSHA == "" {
		return fmt.Errorf("driverstate: stream_pr_opened body: head_sha is empty")
	}
	return nil
}

func validateStreamMerged(body json.RawMessage) error {
	var b StreamMergedBody
	if err := unmarshalBody(KindStreamMerged, body, &b); err != nil {
		return err
	}
	if b.PR < 1 {
		return fmt.Errorf("driverstate: stream_merged body: pr %d must be at least 1", b.PR)
	}
	if b.MergeCommit == "" {
		return fmt.Errorf("driverstate: stream_merged body: merge_commit is empty")
	}
	if b.MergedAt == "" {
		return fmt.Errorf("driverstate: stream_merged body: merged_at is empty")
	}
	return nil
}

func validateReviewCycle(body json.RawMessage) error {
	var b ReviewCycleBody
	if err := unmarshalBody(KindReviewCycle, body, &b); err != nil {
		return err
	}
	var probe reviewCycleProbe
	if err := unmarshalBody(KindReviewCycle, body, &probe); err != nil {
		return err
	}
	if probe.PanelSettled == nil {
		return fmt.Errorf("driverstate: review_cycle body: panel_settled is missing")
	}
	if probe.Findings == nil {
		return fmt.Errorf("driverstate: review_cycle body: findings is missing")
	}
	if b.Cycle < 1 {
		return fmt.Errorf("driverstate: review_cycle body: cycle %d must be at least 1", b.Cycle)
	}
	if b.Findings < 0 {
		return fmt.Errorf("driverstate: review_cycle body: findings %d must not be negative", b.Findings)
	}
	return nil
}
