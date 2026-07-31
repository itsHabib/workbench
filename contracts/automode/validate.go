package automode

import (
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"
)

var (
	// ErrUnknownSchemaVersion identifies an unsupported AutoDecision major.
	ErrUnknownSchemaVersion = errors.New("unrecognized schema_version")
	// ErrMissingField identifies an absent required contract field.
	ErrMissingField = errors.New("required field missing")

	namePattern   = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)
	digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// ValidateDecision enforces artifact contract law without deciding policy.
func ValidateDecision(decision Decision) error {
	if err := checkVersion(decision.SchemaVersion); err != nil {
		return err
	}
	if err := require("rule_fired", decision.RuleFired); err != nil {
		return err
	}
	if err := require("rulebook_version", decision.RulebookVersion); err != nil {
		return err
	}
	if err := require("harness", decision.Harness); err != nil {
		return err
	}
	if !validOutcome(decision.Outcome) {
		return fmt.Errorf("automode: unsupported outcome %q", decision.Outcome)
	}
	if !utf8.ValidString(decision.Remedy) {
		return errors.New("automode: remedy must be valid UTF-8")
	}
	if decision.Outcome != OutcomePass && decision.Remedy == "" {
		return fmt.Errorf("automode: %w: remedy", ErrMissingField)
	}
	if err := validateInputs(decision.Inputs); err != nil {
		return err
	}
	if !digestPattern.MatchString(decision.ActionDigest) {
		return errors.New("automode: action_digest must be sha256:<64 lowercase hexadecimal characters>")
	}
	digest, err := Digest(decision.Inputs)
	if err != nil {
		return err
	}
	if decision.ActionDigest != digest {
		return errors.New("automode: action_digest does not match inputs")
	}
	return nil
}

// ValidateAuditEvent enforces the append-only event envelope.
func ValidateAuditEvent(event AuditEvent) error {
	if err := checkVersion(event.SchemaVersion); err != nil {
		return err
	}
	if err := require("event_id", event.EventID); err != nil {
		return err
	}
	if err := require("invocation_id", event.InvocationID); err != nil {
		return err
	}
	if event.RecordedAt.IsZero() {
		return fmt.Errorf("automode: %w: recorded_at", ErrMissingField)
	}
	if err := ValidateDecision(event.Decision); err != nil {
		return fmt.Errorf("automode: audit decision: %w", err)
	}
	switch event.Kind {
	case EventDecision:
		if event.Completion != nil {
			return errors.New("automode: decision event must not carry completion")
		}
		return nil
	case EventCompletion:
		return validateCompletion(event)
	default:
		return fmt.Errorf("automode: unsupported audit kind %q", event.Kind)
	}
}

func checkVersion(version string) error {
	if version == SchemaVersion {
		return nil
	}
	return fmt.Errorf("automode: %w: got %q, this reader accepts %q", ErrUnknownSchemaVersion, version, SchemaVersion)
}

func require(name, value string) error {
	if value == "" {
		return fmt.Errorf("automode: %w: %s", ErrMissingField, name)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("automode: %s must be valid UTF-8", name)
	}
	return nil
}

func validOutcome(outcome string) bool {
	switch outcome {
	case OutcomePass, OutcomePark, OutcomeBlock, OutcomeRefuse:
		return true
	}
	return false
}

func validateInputs(inputs InputProjection) error {
	if !utf8.ValidString(inputs.Action.Envelope) || !utf8.ValidString(inputs.Action.Operation) {
		return errors.New("automode: action names must be valid UTF-8")
	}
	if !namePattern.MatchString(inputs.Action.Envelope) {
		return errors.New("automode: action envelope must be a normalized name")
	}
	if !namePattern.MatchString(inputs.Action.Operation) {
		return errors.New("automode: action operation must be a normalized name")
	}
	if inputs.Action.Parameters == nil {
		return fmt.Errorf("automode: %w: inputs.action.parameters", ErrMissingField)
	}
	if inputs.Observables == nil {
		return fmt.Errorf("automode: %w: inputs.observables", ErrMissingField)
	}
	if err := validateValues("inputs.action.parameters", inputs.Action.Parameters); err != nil {
		return err
	}
	return validateValues("inputs.observables", inputs.Observables)
}

func validateValues(field string, values Values) error {
	for name, value := range values {
		if !utf8.ValidString(name) || !namePattern.MatchString(name) {
			return fmt.Errorf("automode: %s name %q is not normalized", field, name)
		}
		if !utf8.ValidString(value.Value) {
			return fmt.Errorf("automode: %s %q value must be valid UTF-8", field, name)
		}
		if value.Redacted != (value.Value == RedactionMarker) {
			return fmt.Errorf("automode: %s %q has inconsistent redaction", field, name)
		}
	}
	return nil
}

func validateCompletion(event AuditEvent) error {
	if event.Completion == nil {
		return fmt.Errorf("automode: %w: completion", ErrMissingField)
	}
	if event.Decision.Outcome != OutcomePass {
		return errors.New("automode: completion requires a pass decision")
	}
	if event.Completion.Status != StatusSucceeded && event.Completion.Status != StatusFailed {
		return fmt.Errorf("automode: unsupported completion status %q", event.Completion.Status)
	}
	if event.Completion.Observables == nil {
		return fmt.Errorf("automode: %w: completion.observables", ErrMissingField)
	}
	return validateValues("completion.observables", event.Completion.Observables)
}
