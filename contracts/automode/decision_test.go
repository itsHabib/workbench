package automode

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDecisionFixturesReplaySameRouting(t *testing.T) {
	base := readDecisionFixture(t, "decision-pass.json")
	future := readDecisionFixture(t, "decision-pass-future-fields.json")

	if !reflect.DeepEqual(base.Routing(), future.Routing()) {
		t.Fatalf("routing changed for additive v1 fields: base=%+v future=%+v", base.Routing(), future.Routing())
	}
	if base.ActionDigest != future.ActionDigest {
		t.Fatalf("digest changed for additive v1 fields: %q != %q", base.ActionDigest, future.ActionDigest)
	}
}

func TestDecisionFixturesRefuseMalformedContracts(t *testing.T) {
	tests := []struct {
		name string
		want error
	}{
		{"invalid-decision-unknown-version.json", ErrUnknownSchemaVersion},
		{"invalid-decision-missing-rulebook.json", ErrMissingField},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeDecision(readFixture(t, test.name))
			if !errors.Is(err, test.want) {
				t.Fatalf("DecodeDecision() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestDigestIgnoresNamedValueOrder(t *testing.T) {
	inputs := validInputs()
	want, err := Digest(inputs)
	if err != nil {
		t.Fatal(err)
	}
	reordered := inputs
	reordered.Action.Parameters = reverse(inputs.Action.Parameters)
	reordered.Observables = reverse(inputs.Observables)
	got, err := Digest(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Digest(reordered) = %q, want %q", got, want)
	}
}

func TestValidationKeepsSecretsOutOfProjection(t *testing.T) {
	inputs := validInputs()
	inputs.Action.Parameters[0] = NamedValue{Name: "grant", Value: "super-secret-token", Redacted: true}
	if _, err := Digest(inputs); err == nil {
		t.Fatal("Digest() accepted a redacted raw value")
	}

	for _, name := range []string{"decision-pass.json", "decision-pass-future-fields.json"} {
		if strings.Contains(string(readFixture(t, name)), "super-secret-token") {
			t.Fatalf("%s contains fixture secret", name)
		}
	}
}

func TestValidateAuditEvent(t *testing.T) {
	decision := readDecisionFixture(t, "decision-pass.json")
	base := AuditEvent{
		SchemaVersion: SchemaVersion,
		EventID:       "evt_1",
		InvocationID:  "inv_1",
		RecordedAt:    time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC),
		Kind:          EventDecision,
		Decision:      decision,
	}
	if err := ValidateAuditEvent(base); err != nil {
		t.Fatalf("decision event: %v", err)
	}

	completed := base
	completed.Kind = EventCompletion
	completed.Completion = &Completion{
		Status:      StatusSucceeded,
		Observables: []NamedValue{{Name: "exit_code", Value: "0"}},
	}
	if err := ValidateAuditEvent(completed); err != nil {
		t.Fatalf("completion event: %v", err)
	}

	refused := base
	refused.Decision.Outcome = OutcomeRefuse
	refused.Decision.Remedy = "run gate again"
	refused.Decision.ActionDigest, _ = Digest(refused.Decision.Inputs)
	refused.Kind = EventCompletion
	refused.Completion = completed.Completion
	if err := ValidateAuditEvent(refused); err == nil {
		t.Fatal("completion event accepted a refused decision")
	}
}

func TestSchemasCarryVersionAndEnums(t *testing.T) {
	var decisionSchema schemaDocument
	if err := json.Unmarshal(DecisionSchema, &decisionSchema); err != nil {
		t.Fatal(err)
	}
	decision := decisionSchema.Defs["decision"].Properties
	if decisionSchema.XVersion != SchemaVersion || decision["schema_version"].Const != SchemaVersion {
		t.Fatal("decision schema version drift")
	}
	want := []string{OutcomePass, OutcomePark, OutcomeBlock, OutcomeRefuse}
	if !reflect.DeepEqual(decision["outcome"].Enum, want) {
		t.Fatalf("outcome enum = %v, want %v", decision["outcome"].Enum, want)
	}

	var auditSchema schemaDocument
	if err := json.Unmarshal(AuditSchema, &auditSchema); err != nil {
		t.Fatal(err)
	}
	if auditSchema.XVersion != SchemaVersion {
		t.Fatalf("audit schema version = %q, want %q", auditSchema.XVersion, SchemaVersion)
	}

	assertObjectConforms(t, reflect.TypeOf(Decision{}), decisionSchema.Defs["decision"])
	assertObjectConforms(t, reflect.TypeOf(InputProjection{}), decisionSchema.Defs["inputs"])
	assertObjectConforms(t, reflect.TypeOf(Action{}), decisionSchema.Defs["action"])
	assertObjectConforms(t, reflect.TypeOf(NamedValue{}), decisionSchema.Defs["value"])
	assertObjectConforms(t, reflect.TypeOf(AuditEvent{}), schemaNode{
		Required: auditSchema.Required, Properties: auditSchema.Properties,
	})
	assertObjectConforms(t, reflect.TypeOf(Completion{}), auditSchema.Properties["completion"])
}

type schemaDocument struct {
	XVersion   string                `json:"x-version"`
	Required   []string              `json:"required"`
	Properties map[string]schemaNode `json:"properties"`
	Defs       map[string]schemaNode `json:"$defs"`
}

type schemaNode struct {
	Required   []string              `json:"required"`
	Properties map[string]schemaNode `json:"properties"`
	Const      string                `json:"const"`
	Enum       []string              `json:"enum"`
}

func assertObjectConforms(t *testing.T, typ reflect.Type, schema schemaNode) {
	t.Helper()
	required := make(map[string]struct{}, len(schema.Required))
	for _, name := range schema.Required {
		required[name] = struct{}{}
	}
	fields := make(map[string]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		tag := strings.Split(typ.Field(i).Tag.Get("json"), ",")
		if tag[0] == "" || tag[0] == "-" {
			continue
		}
		fields[tag[0]] = len(tag) > 1 && tag[1] == "omitempty"
	}
	for name := range schema.Properties {
		omitempty, ok := fields[name]
		if !ok {
			t.Errorf("%s: schema property %q has no Go field", typ.Name(), name)
			continue
		}
		_, isRequired := required[name]
		if isRequired == omitempty {
			t.Errorf("%s.%s required=%t omitempty=%t", typ.Name(), name, isRequired, omitempty)
		}
	}
	for name := range fields {
		if _, ok := schema.Properties[name]; !ok {
			t.Errorf("%s: Go field %q has no schema property", typ.Name(), name)
		}
	}
}

func readDecisionFixture(t *testing.T, name string) Decision {
	t.Helper()
	decision, err := DecodeDecision(readFixture(t, name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return decision
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func validInputs() InputProjection {
	return InputProjection{
		Action: Action{
			Envelope:  "shell",
			Operation: "github.pr.merge",
			Parameters: []NamedValue{
				{Name: "grant", Value: RedactionMarker, Redacted: true},
				{Name: "repo", Value: "itsHabib/workbench"},
				{Name: "pr", Value: "168"},
			},
		},
		Observables: []NamedValue{
			{Name: "head_sha", Value: "bce1322d926a541b23f68b9adf94823d55d5d699"},
			{Name: "gate_outcome", Value: "ready_to_merge"},
		},
	}
}

func reverse[T any](values []T) []T {
	out := append([]T(nil), values...)
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}
