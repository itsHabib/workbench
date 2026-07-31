package automode

import (
	"encoding/hex"
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
		{"invalid-decision-redacted-raw.json", nil},
		{"invalid-decision-bad-name.json", nil},
		{"invalid-decision-nonpass-empty-remedy.json", ErrMissingField},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeDecision(readFixture(t, test.name))
			if test.want == nil && err == nil {
				t.Fatal("DecodeDecision() error = nil")
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("DecodeDecision() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestDigestIgnoresValueInsertionOrder(t *testing.T) {
	inputs := validInputs()
	want, err := Digest(inputs)
	if err != nil {
		t.Fatal(err)
	}
	reordered := inputs
	reordered.Action.Parameters = Values{
		"repo":  {Value: "itsHabib/workbench"},
		"pr":    {Value: "168"},
		"grant": {Value: RedactionMarker, Redacted: true},
	}
	reordered.Observables = Values{
		"gate_outcome": {Value: "ready_to_merge"},
		"head_sha":     {Value: "bce1322d926a541b23f68b9adf94823d55d5d699"},
	}
	got, err := Digest(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Digest(reordered) = %q, want %q", got, want)
	}
}

func TestDigestEmptyCollectionsGolden(t *testing.T) {
	inputs := InputProjection{
		Action:      Action{Envelope: "shell", Operation: "git.status", Parameters: Values{}},
		Observables: Values{},
	}
	got, err := Digest(inputs)
	if err != nil {
		t.Fatal(err)
	}
	const want = "sha256:cf5c3e8fe15a079f962b4256f0d4a0aa0391172a47a6c8793ac2e1674ba97579"
	if got != want {
		t.Fatalf("Digest(empty arrays) = %q, want %q", got, want)
	}
}

func TestCanonicalInputsGolden(t *testing.T) {
	got := hex.EncodeToString(canonicalInputs(validInputs()))
	const want = "776f726b62656e63682e6175746f6d6f64652e696e707574732e7631057368656c6c0f6769746875622e70722e6d6572676503056772616e74010a5b52454441435445445d0270720003313638047265706f001269747348616269622f776f726b62656e6368020c676174655f6f7574636f6d65000e72656164795f746f5f6d6572676508686561645f736861002862636531333232643932366135343162323366363862396164663934383233643535643564363939"
	if got != want {
		t.Fatalf("canonicalInputs() = %q, want %q", got, want)
	}
}

func TestValidationKeepsSecretsOutOfProjection(t *testing.T) {
	inputs := validInputs()
	inputs.Action.Parameters["grant"] = Value{Value: "super-secret-token", Redacted: true}
	if _, err := Digest(inputs); err == nil {
		t.Fatal("Digest() accepted a redacted raw value")
	}
	inputs.Action.Parameters["grant"] = Value{Value: RedactionMarker}
	if _, err := Digest(inputs); err == nil {
		t.Fatal("Digest() accepted an unflagged redaction marker")
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
		Observables: Values{"exit_code": {Value: "0"}},
	}
	if err := ValidateAuditEvent(completed); err != nil {
		t.Fatalf("completion event: %v", err)
	}

	missingCompletion := base
	missingCompletion.Kind = EventCompletion
	if err := ValidateAuditEvent(missingCompletion); err == nil {
		t.Fatal("completion event accepted no completion body")
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

	for _, name := range []string{
		"invalid-audit-decision-with-completion.json",
		"invalid-audit-completion-nonpass.json",
	} {
		if _, err := DecodeAuditEvent(readFixture(t, name)); err == nil {
			t.Fatalf("%s: DecodeAuditEvent() error = nil", name)
		}
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
	assertAuditSchemaBundlesDecisionDefs(t)

	assertObjectConforms(t, reflect.TypeOf(Decision{}), decisionSchema.Defs["decision"])
	assertObjectConforms(t, reflect.TypeOf(InputProjection{}), decisionSchema.Defs["inputs"])
	assertObjectConforms(t, reflect.TypeOf(Action{}), decisionSchema.Defs["action"])
	assertObjectConforms(t, reflect.TypeOf(Value{}), decisionSchema.Defs["value"])
	assertObjectConforms(t, reflect.TypeOf(AuditEvent{}), schemaNode{
		Required: auditSchema.Required, Properties: auditSchema.Properties,
	})
	assertObjectConforms(t, reflect.TypeOf(Completion{}), auditSchema.Properties["completion"])
}

func TestDecisionSchemaUsesNamesAsObjectKeys(t *testing.T) {
	decisionSchema := loadSchema(t, DecisionSchema)
	values := decisionSchema.Defs["values"]
	var additional schemaNode
	if err := json.Unmarshal(values.AdditionalProperties, &additional); err != nil {
		t.Fatal(err)
	}
	if values.Type != "object" || values.PropertyNames == nil ||
		values.PropertyNames.Ref != "#/$defs/name" ||
		additional.Ref != "#/$defs/value" {
		t.Fatal("decision schema does not structurally key values by name")
	}
}

func TestDecisionRejectsInvalidUTF8BeforePersistence(t *testing.T) {
	inputs := validInputs()
	inputs.Action.Parameters["path"] = Value{Value: string([]byte{0xff})}
	digest, err := Digest(inputs)
	if err == nil || digest != "" {
		t.Fatal("Digest() accepted invalid UTF-8")
	}

	decision := readDecisionFixture(t, "decision-pass.json")
	decision.Remedy = string([]byte{0xff})
	if err := ValidateDecision(decision); err == nil {
		t.Fatal("ValidateDecision() accepted invalid UTF-8 remedy")
	}
}

func TestSchemasResolveWithoutExternalResources(t *testing.T) {
	for name, data := range map[string][]byte{
		"decision": DecisionSchema,
		"audit":    AuditSchema,
	} {
		t.Run(name, func(t *testing.T) {
			var document any
			if err := json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			assertLocalRefsResolve(t, document, document)
		})
	}
}

func TestDecisionSchemaPinsRemedyLaw(t *testing.T) {
	decisionSchema := loadSchema(t, DecisionSchema)
	decision := decisionSchema.Defs["decision"]
	if len(decision.AllOf) != 1 {
		t.Fatalf("decision allOf rules = %d, want 1", len(decision.AllOf))
	}
	remedy := decision.AllOf[0]
	if remedy.If == nil || remedy.Else == nil ||
		remedy.If.Properties["outcome"].Const != OutcomePass ||
		remedy.Else.Properties["remedy"].MinLength != 1 {
		t.Fatal("decision schema does not require a non-empty non-pass remedy")
	}
}

func TestDecisionSchemaPinsRedactionLaw(t *testing.T) {
	decisionSchema := loadSchema(t, DecisionSchema)
	value := decisionSchema.Defs["value"]
	if len(value.AllOf) != 1 {
		t.Fatalf("value allOf rules = %d, want 1", len(value.AllOf))
	}
	redaction := value.AllOf[0]
	if redaction.If == nil || redaction.Then == nil || redaction.Else == nil ||
		redaction.If.Properties["redacted"].Const != true ||
		redaction.Then.Properties["value"].Const != RedactionMarker ||
		redaction.Else.Properties["value"].Not == nil ||
		redaction.Else.Properties["value"].Not.Const != RedactionMarker {
		t.Fatal("decision schema does not enforce redacted iff [REDACTED]")
	}
}

func TestAuditSchemaPinsEventLaws(t *testing.T) {
	auditSchema := loadSchema(t, AuditSchema)
	if len(auditSchema.AllOf) != 2 {
		t.Fatalf("audit allOf rules = %d, want 2", len(auditSchema.AllOf))
	}
	decisionEvent := auditSchema.AllOf[0]
	completionEvent := auditSchema.AllOf[1]
	if decisionEvent.If == nil || decisionEvent.Then == nil ||
		decisionEvent.If.Properties["kind"].Const != EventDecision ||
		decisionEvent.Then.Not == nil ||
		!reflect.DeepEqual(decisionEvent.Then.Not.Required, []string{"completion"}) {
		t.Fatal("audit schema does not forbid completion on decision events")
	}
	if completionEvent.If == nil || completionEvent.Then == nil ||
		completionEvent.If.Properties["kind"].Const != EventCompletion ||
		!reflect.DeepEqual(completionEvent.Then.Required, []string{"completion"}) ||
		completionEvent.Then.Properties["decision"].Properties["outcome"].Const != OutcomePass {
		t.Fatal("audit schema does not require a pass decision and completion body")
	}
}

func loadSchema(t *testing.T, data []byte) schemaDocument {
	t.Helper()
	var schema schemaDocument
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

type schemaDocument struct {
	XVersion   string                `json:"x-version"`
	Required   []string              `json:"required"`
	Properties map[string]schemaNode `json:"properties"`
	Defs       map[string]schemaNode `json:"$defs"`
	AllOf      []schemaNode          `json:"allOf"`
}

type schemaNode struct {
	Type                 string                `json:"type"`
	Ref                  string                `json:"$ref"`
	Required             []string              `json:"required"`
	Properties           map[string]schemaNode `json:"properties"`
	Const                any                   `json:"const"`
	Enum                 []string              `json:"enum"`
	MinLength            int                   `json:"minLength"`
	AllOf                []schemaNode          `json:"allOf"`
	If                   *schemaNode           `json:"if"`
	Then                 *schemaNode           `json:"then"`
	Else                 *schemaNode           `json:"else"`
	Not                  *schemaNode           `json:"not"`
	PropertyNames        *schemaNode           `json:"propertyNames"`
	AdditionalProperties json.RawMessage       `json:"additionalProperties"`
}

func assertAuditSchemaBundlesDecisionDefs(t *testing.T) {
	t.Helper()
	type rawSchema struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	var decision, audit rawSchema
	if err := json.Unmarshal(DecisionSchema, &decision); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(AuditSchema, &audit); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decision.Defs, audit.Defs) {
		t.Fatal("audit schema's bundled decision definitions drifted")
	}
}

func assertLocalRefsResolve(t *testing.T, root, node any) {
	t.Helper()
	switch value := node.(type) {
	case []any:
		for _, item := range value {
			assertLocalRefsResolve(t, root, item)
		}
	case map[string]any:
		for key, item := range value {
			if key != "$ref" {
				assertLocalRefsResolve(t, root, item)
				continue
			}
			ref, ok := item.(string)
			if !ok || !strings.HasPrefix(ref, "#/$defs/") {
				t.Fatalf("schema contains non-local reference %v", item)
			}
			name := strings.TrimPrefix(ref, "#/$defs/")
			defs := root.(map[string]any)["$defs"].(map[string]any)
			if _, ok := defs[name]; !ok {
				t.Fatalf("schema reference %q does not resolve", ref)
			}
		}
	}
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
			Parameters: Values{
				"grant": {Value: RedactionMarker, Redacted: true},
				"repo":  {Value: "itsHabib/workbench"},
				"pr":    {Value: "168"},
			},
		},
		Observables: Values{
			"head_sha":     {Value: "bce1322d926a541b23f68b9adf94823d55d5d699"},
			"gate_outcome": {Value: "ready_to_merge"},
		},
	}
}
