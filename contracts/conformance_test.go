package contracts

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// objSchema is the slice of JSON Schema this package asserts its Go types
// against: property names, which are required, nested object/array shapes, and
// enums. It is recursive so subject/producer/findings.items are walked too.
type objSchema struct {
	XVersion   string               `json:"x-version"`
	Type       string               `json:"type"`
	Required   []string             `json:"required"`
	Properties map[string]objSchema `json:"properties"`
	Items      *objSchema           `json:"items"`
	Enum       []string             `json:"enum"`
}

func loadSchema(t *testing.T) objSchema {
	t.Helper()
	var s objSchema
	if err := json.Unmarshal(VerdictSchema, &s); err != nil {
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

func (o objSchema) itemsOf(t *testing.T, prop string) objSchema {
	t.Helper()
	c := o.child(t, prop)
	if c.Items == nil {
		t.Fatalf("schema property %q has no items", prop)
	}
	return *c.Items
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

// TestSchemaMatchesGoTypes is the conformance gate: the schema and the Go types
// are one contract, checked structurally so drift in either fails here.
func TestSchemaMatchesGoTypes(t *testing.T) {
	root := loadSchema(t)
	cases := []struct {
		name string
		typ  reflect.Type
		obj  objSchema
	}{
		{"Verdict", reflect.TypeOf(Verdict{}), root},
		{"Subject", reflect.TypeOf(Subject{}), root.child(t, "subject")},
		{"Producer", reflect.TypeOf(Producer{}), root.child(t, "producer")},
		{"Finding", reflect.TypeOf(Finding{}), root.itemsOf(t, "findings")},
	}
	for _, c := range cases {
		assertObjectConforms(t, c.name, c.typ, c.obj)
	}
}

// TestEnumsMatchConstants ties the exported constants to the schema enums, so
// adding a producer class or decision to one without the other fails.
func TestEnumsMatchConstants(t *testing.T) {
	root := loadSchema(t)
	assertSetEqual(t, "decision.enum",
		root.child(t, "decision").Enum,
		[]string{DecisionBlock, DecisionEscalate, DecisionPass})
	assertSetEqual(t, "producer.class.enum",
		root.child(t, "producer").child(t, "class").Enum,
		[]string{ClassCode, ClassLocal, ClassJudgment})
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

func TestSchemaVersion(t *testing.T) {
	if v := loadSchema(t).XVersion; v != VerdictSchemaVersion {
		t.Errorf("schema x-version = %q, want %q", v, VerdictSchemaVersion)
	}
}

// TestUnknownFieldsIgnored is the tolerant-reader guarantee: a body carrying a
// field this binary predates still decodes, and the known fields survive.
func TestUnknownFieldsIgnored(t *testing.T) {
	body := []byte(`{"subject":{"repo":"itsHabib/ship","number":181},"source":"triage-floor","producer":{"class":"code"},"decision":"block","tier":"T3","confidence":1,"why":"nope","future_field":{"x":1}}`)
	var v Verdict
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("tolerant reader must ignore unknown fields: %v", err)
	}
	if v.Decision != DecisionBlock || v.Subject.Number != 181 {
		t.Fatalf("known fields did not survive an unknown-field body: %+v", v)
	}
}

// TestNonVerdictEnvelopeYieldsNoVerdict pins the graceful handling of artifacts
// that are not verdicts — an escalation body must not be forced to parse as one.
func TestNonVerdictEnvelopeYieldsNoVerdict(t *testing.T) {
	env := Envelope{Kind: KindEscalation, Body: []byte(`{"outcome":"parked_for_judgment","question":"?"}`)}
	v, ok, err := env.Verdict()
	if ok || err != nil {
		t.Fatalf("a non-verdict envelope must yield ok=false, no error; got ok=%v err=%v", ok, err)
	}
	if !reflect.DeepEqual(v, Verdict{}) {
		t.Fatalf("a non-verdict envelope must yield the zero verdict, got %+v", v)
	}
}

func TestVerdictEnvelopeDecodes(t *testing.T) {
	env := Envelope{Kind: KindVerdict, Body: []byte(`{"subject":{"repo":"r","number":1},"decision":"escalate","tier":"T2","why":"x"}`)}
	v, ok, err := env.Verdict()
	if !ok || err != nil {
		t.Fatalf("a verdict envelope must decode: ok=%v err=%v", ok, err)
	}
	if v.Decision != DecisionEscalate || v.Tier != "T2" || v.Subject.Repo != "r" {
		t.Fatalf("decoded verdict is wrong: %+v", v)
	}
}

// TestRequiredFieldsAlwaysMarshal locks the on-the-wire shape a consumer relies
// on: every required key is present even on a zero verdict, and the optional
// findings array is omitted when empty.
func TestRequiredFieldsAlwaysMarshal(t *testing.T) {
	out, err := json.Marshal(Verdict{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, key := range []string{`"subject"`, `"source"`, `"producer"`, `"decision"`, `"tier"`, `"confidence"`, `"why"`} {
		if !strings.Contains(s, key) {
			t.Errorf("required key %s missing from a marshaled zero Verdict: %s", key, s)
		}
	}
	if strings.Contains(s, `"findings"`) {
		t.Errorf("optional findings must be omitted when empty: %s", s)
	}
}

func TestRoundTrip(t *testing.T) {
	in := Verdict{
		Subject:    Subject{Repo: "itsHabib/ship", Number: 181, HeadSHA: "abc123"},
		Source:     "review-consolidation",
		Producer:   Producer{Class: ClassJudgment, Impl: "opus-4.8"},
		Decision:   DecisionEscalate,
		Tier:       "T3",
		Confidence: 0.5,
		Findings:   []Finding{{Title: "risky path", Severity: "high", Locus: "main.go:10", Confidence: 0.9, Evidence: "rm -rf /"}},
		Why:        "needs a human",
	}
	out, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var got Verdict
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, got) {
		t.Fatalf("round-trip mismatch:\n in=%+v\nout=%+v", in, got)
	}
}
