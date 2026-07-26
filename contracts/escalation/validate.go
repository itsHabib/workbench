package escalation

import (
	"errors"
	"fmt"
)

// ErrUnknownSchemaVersion is the version-gate failure: the body declares a
// schema_version this reader does not understand. Callers branch with
// errors.Is.
var ErrUnknownSchemaVersion = errors.New("unrecognized schema_version")

// ErrMissingField is the required-field-law failure: a field the contract
// requires is absent. Callers branch with errors.Is.
var ErrMissingField = errors.New("required field missing")

// checkVersion accepts only the one version this schema ships. There is exactly
// one version today; a compatibility rule is decided if and when a v2 first
// exists, from evidence — mirroring contracts/authority's checkVersion.
func checkVersion(schemaVersion string) error {
	if schemaVersion == SchemaVersion {
		return nil
	}
	return fmt.Errorf("escalation: %w: got %q, this reader accepts %q", ErrUnknownSchemaVersion, schemaVersion, SchemaVersion)
}

// Validate is the strict contract law a consumer applies when it wants teeth:
// the version gate plus the required-field rule. It is deliberately NOT on the
// tolerant read path (DecodeBody) — flare and the inbox must still render older
// or unknown bodies. It is what a strict writer/admission checks, and what
// conformance_test.go pins. It is contract law, not a decision: it never
// chooses to park, route, or resolve — those stay in the owning tools.
func Validate(e V1) error {
	if err := checkVersion(e.SchemaVersion); err != nil {
		return err
	}
	return validateFields(e)
}

// validateFields enforces the required-field law JSON Schema states but a
// stdlib-only leaf cannot run a validator for: the always-present envelope of a
// park (outcome, verdict, grant, question) and, when a Resolution is present,
// its own required members. Optional fields (code, repo, number, brief) are not
// second-guessed.
func validateFields(e V1) error {
	for _, f := range []struct {
		name string
		val  string
	}{
		{"outcome", e.Outcome},
		{"verdict", e.Verdict},
		{"grant", e.Grant},
		{"question", e.Question},
	} {
		if f.val == "" {
			return fmt.Errorf("escalation: %w: %s", ErrMissingField, f.name)
		}
	}
	if e.Resolution == nil {
		return nil
	}
	return validateResolution(*e.Resolution)
}

// validateResolution enforces that a present resolution carries a full closed
// loop: every member is required, since a half-recorded resolution is worse
// than none — it claims the loop closed without the provenance to prove it.
func validateResolution(r Resolution) error {
	for _, f := range []struct {
		name string
		val  string
	}{
		{"resolution.decision", r.Decision},
		{"resolution.who", r.Who},
		{"resolution.at", r.At},
		{"resolution.judgment_id", r.JudgmentID},
	} {
		if f.val == "" {
			return fmt.Errorf("escalation: %w: %s", ErrMissingField, f.name)
		}
	}
	if r.Decision != DecisionPass && r.Decision != DecisionBlock {
		return fmt.Errorf("escalation: resolution.decision %q must be %q or %q", r.Decision, DecisionPass, DecisionBlock)
	}
	return nil
}
