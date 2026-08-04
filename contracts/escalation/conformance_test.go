package escalation

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// objSchema is the slice of JSON Schema this package asserts its Go types
// against: property names, which are required, nested object/array shapes,
// enums, and the schema_version const. It mirrors the contracts/authority and
// contracts/driverstate walkers structurally.
type objSchema struct {
	XVersion   string               `json:"x-version"`
	Type       string               `json:"type"`
	Required   []string             `json:"required"`
	Properties map[string]objSchema `json:"properties"`
	Items      *objSchema           `json:"items"`
	Enum       []string             `json:"enum"`
	Const      string               `json:"const"`
}

func loadSchema(t *testing.T) objSchema {
	t.Helper()
	var s objSchema
	if err := json.Unmarshal(Schema, &s); err != nil {
		t.Fatalf("parse embedded schema: %v", err)
	}
	return s
}

func (o objSchema) child(t *testing.T, prop string) objSchema {
	t.Helper()
	c, ok := o.Properties[prop]
	if !ok {
		t.Fatalf("schema has no property %q", prop)
	}
	return c
}

type goField struct {
	omitempty bool
}

// jsonFields maps a struct's json tag names to whether they carry omitempty.
func jsonFields(typ reflect.Type) map[string]goField {
	out := map[string]goField{}
	for i := 0; i < typ.NumField(); i++ {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		if parts[0] == "" {
			continue
		}
		out[parts[0]] = goField{omitempty: contains(parts[1:], "omitempty")}
	}
	return out
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// assertObjectConforms pins a Go struct to a schema object both ways: every
// schema property has a Go field and vice versa (additionalProperties:false),
// and a field is omitempty in Go exactly when it is optional in the schema — so
// a required field always marshals and an optional one never marshals empty.
func assertObjectConforms(t *testing.T, name string, typ reflect.Type, obj objSchema) {
	t.Helper()
	fields := jsonFields(typ)
	required := map[string]bool{}
	for _, r := range obj.Required {
		required[r] = true
	}
	for prop := range obj.Properties {
		f, ok := fields[prop]
		if !ok {
			t.Errorf("%s: schema property %q has no Go field", name, prop)
			continue
		}
		if required[prop] && f.omitempty {
			t.Errorf("%s: %q is required in schema but omitempty in Go", name, prop)
		}
		if !required[prop] && !f.omitempty {
			t.Errorf("%s: %q is optional in schema but not omitempty in Go", name, prop)
		}
	}
	for goName := range fields {
		if _, ok := obj.Properties[goName]; !ok {
			t.Errorf("%s: Go field %q is absent from the schema", name, goName)
		}
	}
}

func assertSetEqual(t *testing.T, name string, got, want []string) {
	t.Helper()
	set := func(ss []string) map[string]bool {
		m := map[string]bool{}
		for _, s := range ss {
			m[s] = true
		}
		return m
	}
	if !reflect.DeepEqual(set(got), set(want)) {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return b
}

// TestEscalationConformance is the acceptance gate: the schema and the Go types
// are one contract, checked structurally both ways so drift in either fails
// here.
func TestEscalationConformance(t *testing.T) {
	root := loadSchema(t)
	cases := []struct {
		name string
		typ  reflect.Type
		obj  objSchema
	}{
		{"V1", reflect.TypeOf(V1{}), root},
		{"Brief", reflect.TypeOf(Brief{}), root.child(t, "brief")},
		{"Escape", reflect.TypeOf(Escape{}), root.child(t, "escape")},
		{"Resolution", reflect.TypeOf(Resolution{}), root.child(t, "resolution")},
	}
	for _, c := range cases {
		assertObjectConforms(t, c.name, c.typ, c.obj)
	}
}

// TestCodeEnumMatchesConstants ties the schema's closed code enum to the
// exported constants, so adding a park code to one without the other fails. A
// content park's absent code is not an enum member by design — it is the
// omission of the field.
func TestCodeEnumMatchesConstants(t *testing.T) {
	root := loadSchema(t)
	assertSetEqual(t, "code.enum", root.child(t, "code").Enum,
		[]string{CodeTierExceeded, CodeCountUnreadable, CodeCycleExceeded})
}

// TestDecisionEnumMatchesConstants ties the schema's resolution.decision enum
// to the exported decision constants.
func TestDecisionEnumMatchesConstants(t *testing.T) {
	root := loadSchema(t)
	decision := root.child(t, "resolution").child(t, "decision")
	assertSetEqual(t, "resolution.decision.enum", decision.Enum,
		[]string{DecisionPass, DecisionBlock})
}

func TestSchemaVersion(t *testing.T) {
	root := loadSchema(t)
	if root.XVersion != SchemaVersion {
		t.Errorf("schema x-version = %q, want %q", root.XVersion, SchemaVersion)
	}
	if c := root.child(t, "schema_version").Const; c != SchemaVersion {
		t.Errorf("schema_version const = %q, want %q — the schema document and the Go gate must agree on version validity", c, SchemaVersion)
	}
}

// TestValidate_Golden proves each valid fixture passes the strict law: the
// version gate and the required-field rule both accept it.
func TestValidate_Golden(t *testing.T) {
	for _, f := range []string{"escalation-content-park.json", "escalation-tier-ceiling.json", "escalation-self-gated.json", "escalation-resolved.json"} {
		e, err := DecodeBody(readFixture(t, f))
		if err != nil {
			t.Fatalf("%s: decode: %v", f, err)
		}
		if err := Validate(e); err != nil {
			t.Errorf("%s: golden fixture must validate: %v", f, err)
		}
	}
}

// TestValidate_VersionGate pins the version gate: an otherwise-valid body that
// declares an unknown schema_version rejects via ErrUnknownSchemaVersion.
func TestValidate_VersionGate(t *testing.T) {
	e, err := DecodeBody(readFixture(t, "invalid-unknown-version.json"))
	if err != nil {
		t.Fatalf("tolerant decode must still read an unknown-version body: %v", err)
	}
	if err := Validate(e); !errors.Is(err, ErrUnknownSchemaVersion) {
		t.Fatalf("unknown schema_version must reject via the version gate, got %v", err)
	}
}

// TestValidate_RequiredFields pins the required-field law: a missing top-level
// required field and a half-recorded resolution both reject via ErrMissingField,
// and a resolution with an out-of-vocabulary decision rejects.
func TestValidate_RequiredFields(t *testing.T) {
	base := V1{
		SchemaVersion: SchemaVersion,
		Outcome:       "parked_for_judgment",
		Verdict:       "vrd_1",
		Grant:         "grt_1",
		Question:      "why",
	}
	if err := Validate(base); err != nil {
		t.Fatalf("a complete envelope must validate: %v", err)
	}

	missingQuestion := base
	missingQuestion.Question = ""
	if err := Validate(missingQuestion); !errors.Is(err, ErrMissingField) {
		t.Errorf("missing question must reject via ErrMissingField, got %v", err)
	}

	halfResolution := base
	halfResolution.Resolution = &Resolution{Decision: DecisionPass, Who: "operator"} // no at, no judgment_id
	if err := Validate(halfResolution); !errors.Is(err, ErrMissingField) {
		t.Errorf("a half-recorded resolution must reject via ErrMissingField, got %v", err)
	}

	badDecision := base
	badDecision.Resolution = &Resolution{Decision: "maybe", Who: "operator", At: "2026-07-25T00:00:00Z", JudgmentID: "jdg_1"}
	if err := Validate(badDecision); err == nil {
		t.Error("a resolution with an out-of-vocabulary decision must reject")
	}
}

// TestDecodeBody_Tolerant is the tolerant-reader guarantee the migration rests
// on: DecodeBody applies NO version gate and ignores unknown fields, so the
// routing and projection readers still render older or unknown bodies. A
// schema-only enum violation (a bad code) survives decode untouched — its
// rejection belongs to a future admission validator, never a claim this leaf
// enforces at read time. This mirrors contracts/authority's
// TestTolerantDecodeDefersToAdmission.
func TestDecodeBody_Tolerant(t *testing.T) {
	// A body carrying a field this reader predates still decodes, known fields
	// survive, and no version gate fires on the tolerant path.
	body := []byte(`{"schema_version":"escalation.v9","outcome":"parked_for_judgment","verdict":"vrd_1","grant":"grt_1","question":"an older or newer body","future_field":{"x":1}}`)
	e, err := DecodeBody(body)
	if err != nil {
		t.Fatalf("tolerant reader must ignore unknown fields and skip the version gate: %v", err)
	}
	if e.Question != "an older or newer body" || e.Outcome != "parked_for_judgment" {
		t.Fatalf("known fields did not survive an unknown-field body: %+v", e)
	}

	// A schema-only enum violation still decodes; the offending value survives.
	bad, err := DecodeBody(readFixture(t, "invalid-bad-code-enum.json"))
	if err != nil {
		t.Fatalf("a schema-only enum violation must still decode: %v", err)
	}
	if bad.Code != "vaporized" {
		t.Errorf("the violating value must survive decode untouched, got %q", bad.Code)
	}

	// A legacy body with no schema_version at all — the pre-contract shape — must
	// still decode on the tolerant path; that is the whole point of not gating
	// the reader flare and the inbox use.
	legacy := []byte(`{"outcome":"parked_for_judgment","question":"a pre-contract body","code":"grant_tier_exceeded","repo":"o/r","number":7}`)
	l, err := DecodeBody(legacy)
	if err != nil {
		t.Fatalf("a legacy body with no schema_version must still decode: %v", err)
	}
	if l.Code != CodeTierExceeded || l.Number != 7 {
		t.Fatalf("a legacy body's known fields must survive: %+v", l)
	}
}

// TestGoldenRoundTrip proves each valid fixture survives decode and re-encode
// losslessly: the re-encoded JSON is value-equal to the fixture (no field
// dropped or mutated) and a second decode is Go-value-equal to the first. This
// is where the required-fields-always-marshal / optional-fields-never-marshal-
// empty law is exercised end to end.
func TestGoldenRoundTrip(t *testing.T) {
	for _, f := range []string{"escalation-content-park.json", "escalation-tier-ceiling.json", "escalation-self-gated.json", "escalation-resolved.json"} {
		t.Run(f, func(t *testing.T) {
			raw := readFixture(t, f)
			first, err := DecodeBody(raw)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			out, err := json.Marshal(first)
			if err != nil {
				t.Fatalf("re-encode: %v", err)
			}
			assertJSONEqual(t, raw, out)
			second, err := DecodeBody(out)
			if err != nil {
				t.Fatalf("second decode: %v", err)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("round-trip mismatch:\n first=%+v\nsecond=%+v", first, second)
			}
		})
	}
}

// TestResolvedFixtureCarriesProvenance pins the closed-loop shape: the resolved
// fixture's resolution names a decision, who, when, and the judgment id it
// produced — the provenance a bare judgment artifact lacks.
func TestResolvedFixtureCarriesProvenance(t *testing.T) {
	e, err := DecodeBody(readFixture(t, "escalation-resolved.json"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if e.Resolution == nil {
		t.Fatal("the resolved fixture must carry a resolution")
	}
	r := e.Resolution
	if r.Decision != DecisionPass || r.Who == "" || r.At == "" || r.JudgmentID == "" {
		t.Fatalf("resolution must carry full provenance, got %+v", r)
	}
}

// assertJSONEqual compares two JSON documents as decoded values, so formatting
// differs but any dropped or mutated field fails.
func assertJSONEqual(t *testing.T, want, got []byte) {
	t.Helper()
	var w, g any
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("parse re-encoded: %v", err)
	}
	if !reflect.DeepEqual(w, g) {
		t.Fatalf("re-encoded JSON is not value-equal to the fixture:\n want=%s\n  got=%s", want, got)
	}
}
