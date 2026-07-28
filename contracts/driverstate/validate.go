package driverstate

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
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
	case KindStreamDispatched:
		return validateStreamDispatched(body)
	case KindStreamAttempt:
		return validateStreamAttempt(body)
	case KindStreamPROpened:
		return validateStreamPROpened(body)
	case KindStreamMerged:
		return validateStreamMerged(body)
	case KindReviewCycle:
		return validateReviewCycle(body)
	case KindClosureFacts:
		return validateClosureFacts(body)
	case KindIntervention:
		return validateIntervention(body)
	default:
		return nil
	}
}

func validateClosureFacts(body json.RawMessage) error {
	var b ClosureFactsBody
	if err := unmarshalBody(KindClosureFacts, body, &b); err != nil {
		return err
	}
	var rawFields map[string]json.RawMessage
	if err := unmarshalBody(KindClosureFacts, body, &rawFields); err != nil {
		return err
	}
	values := []struct {
		name  string
		value string
	}{
		{"task_ref", b.TaskRef}, {"seat", b.Seat}, {"harness", b.Harness},
		{"model", b.Model}, {"provider", b.Provider}, {"effort", b.Effort},
		{"review_producer", b.ReviewProducer}, {"catalog_revision", b.CatalogRevision},
		{"review_artifact_id", b.ReviewArtifactID},
		{"review_artifact_digest", b.ReviewArtifactDigest},
		{"review_head_sha", b.ReviewHeadSHA},
		{"final_reviewed_head_sha", b.FinalReviewedHeadSHA},
		{"gate_head_sha", b.GateHeadSHA},
		{"ship_run_ref", b.ShipRunRef},
		{"gate_run_ref", b.GateRunRef},
	}
	present := 0
	for _, field := range values {
		if _, exists := rawFields[field.name]; !exists {
			continue
		}
		present++
		if !validClosureString(field.value) {
			return fmt.Errorf("driverstate: closure_facts body: %s is malformed", field.name)
		}
	}
	if present == 0 {
		return errors.New("driverstate: closure_facts body: at least one fact is required")
	}
	return validateClosureFactFormats(b)
}

func validClosureString(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && trimmed == value && len(value) <= 256
}

func validateClosureFactFormats(b ClosureFactsBody) error {
	switch {
	case b.TaskRef != "" && !strings.HasPrefix(b.TaskRef, "tsk_"):
		return errors.New("driverstate: closure_facts body: task_ref must start with tsk_")
	case b.ShipRunRef != "" && !strings.HasPrefix(b.ShipRunRef, "drv_"):
		return errors.New("driverstate: closure_facts body: ship_run_ref must start with drv_")
	case b.GateRunRef != "" && !strings.HasPrefix(b.GateRunRef, "run_"):
		return errors.New("driverstate: closure_facts body: gate_run_ref must start with run_")
	case b.ReviewHeadSHA != "" && !validHex(b.ReviewHeadSHA, 40):
		return errors.New("driverstate: closure_facts body: review_head_sha must be 40 lowercase hexadecimal characters")
	case b.FinalReviewedHeadSHA != "" && !validHex(b.FinalReviewedHeadSHA, 40):
		return errors.New("driverstate: closure_facts body: final_reviewed_head_sha must be 40 lowercase hexadecimal characters")
	case b.GateHeadSHA != "" && !validHex(b.GateHeadSHA, 40):
		return errors.New("driverstate: closure_facts body: gate_head_sha must be 40 lowercase hexadecimal characters")
	case b.ReviewArtifactDigest != "" && !validDigest(b.ReviewArtifactDigest):
		return errors.New("driverstate: closure_facts body: review_artifact_digest must be 64 lowercase hexadecimal characters")
	case b.CatalogRevision != "" && !validCatalogRevision(b.CatalogRevision):
		return errors.New("driverstate: closure_facts body: catalog_revision must be a full source commit SHA or sha256:<64 lowercase hexadecimal characters>")
	}
	return nil
}

func validateIntervention(body json.RawMessage) error {
	var b InterventionBody
	if err := unmarshalBody(KindIntervention, body, &b); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339, b.Time); err != nil {
		return errors.New("driverstate: intervention body: time must be RFC3339")
	}
	if b.Kind != InterventionGenuineJudgment && b.Kind != InterventionMechanismRepair {
		return fmt.Errorf("driverstate: intervention body: unknown kind %q", b.Kind)
	}
	if !validReasonCode(b.ReasonCode) {
		return errors.New("driverstate: intervention body: reason_code must be a lowercase hyphenated identifier")
	}
	if strings.TrimSpace(b.Actor) == "" || len(b.Actor) > 256 {
		return errors.New("driverstate: intervention body: actor is required")
	}
	if b.Kind == InterventionGenuineJudgment && strings.TrimSpace(b.QuestionRef) == "" {
		return errors.New("driverstate: intervention body: genuine-judgment requires question_ref")
	}
	return nil
}

func validReasonCode(value string) bool {
	if value == "" {
		return false
	}
	afterHyphen := true
	for _, r := range value {
		if r == '-' {
			if afterHyphen {
				return false
			}
			afterHyphen = true
			continue
		}
		if r < 'a' || r > 'z' {
			if r < '0' || r > '9' {
				return false
			}
		}
		afterHyphen = false
	}
	return !afterHyphen
}

func validDigest(value string) bool {
	return validHex(value, 64)
}

func validCatalogRevision(value string) bool {
	if strings.HasPrefix(value, "sha256:") {
		return validHex(value[len("sha256:"):], 64)
	}
	return validHex(value, 40) || validHex(value, 64)
}

func validHex(value string, size int) bool {
	if len(value) != size || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func unmarshalBody(kind Kind, body json.RawMessage, into any) error {
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("driverstate: %s body: %w", kind, err)
	}
	return nil
}

// validateStreamDispatched enforces only that the body, when present, decodes
// as the locator shape — every field is optional, and legacy ledgers carry an
// absent or null body (EncodeEvent emits null for an empty RawMessage).
func validateStreamDispatched(body json.RawMessage) error {
	if len(body) == 0 {
		return nil
	}
	var b StreamDispatchedBody
	return unmarshalBody(KindStreamDispatched, body, &b)
}

// Presence probes: decoding a required boolean/array/int into a value struct
// loses whether the member was present at all, so the pinned-grammar validators
// decode into pointer-field shadows and reject nil — the schema's `required`
// list, enforced at runtime.
type runImportedProbe struct {
	Manifest *json.RawMessage `json:"manifest"`
	Streams  *[]StreamSpec    `json:"streams"`
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
	if probe.Manifest == nil {
		return fmt.Errorf("driverstate: run_imported body: manifest is missing; the verbatim driver.md frontmatter snapshot is what a bare run resumes from")
	}
	if b.Repo == "" {
		return fmt.Errorf("driverstate: run_imported body: repo is empty")
	}
	if b.Source == "" {
		return fmt.Errorf("driverstate: run_imported body: source is empty")
	}
	var rawFields map[string]json.RawMessage
	if err := unmarshalBody(KindRunImported, body, &rawFields); err != nil {
		return err
	}
	if _, present := rawFields["ship_run_ref"]; present {
		if strings.TrimSpace(b.ShipRunRef) == "" || !strings.HasPrefix(b.ShipRunRef, "drv_") {
			return fmt.Errorf("driverstate: run_imported body: ship_run_ref must start with drv_")
		}
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
	var rawFields map[string]json.RawMessage
	if err := unmarshalBody(KindStreamMerged, body, &rawFields); err != nil {
		return err
	}
	if _, present := rawFields["head_sha"]; present && !validHex(b.HeadSHA, 40) {
		return fmt.Errorf("driverstate: stream_merged body: head_sha must be 40 lowercase hexadecimal characters")
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
