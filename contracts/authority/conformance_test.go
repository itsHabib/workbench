package authority

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// objSchema is the slice of JSON Schema this package asserts its Go types
// against: property names, which are required, nested object/array shapes,
// enums, and the schema_version const. It mirrors the contracts and
// contracts/execution walkers structurally.
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
// and a field is omitempty in Go exactly when it is optional in the schema —
// so a required field always marshals and an optional one never marshals
// empty. This receipt has no optional fields: every position is required.
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

// TestReceiptConformance is the acceptance gate: the schema and the Go types
// are one contract, checked structurally both ways so drift in either fails
// here.
func TestReceiptConformance(t *testing.T) {
	root := loadSchema(t)
	grant := root.itemsOf(t, "grants")
	evidence := root.child(t, "evidence")
	cases := []struct {
		name string
		typ  reflect.Type
		obj  objSchema
	}{
		{"Receipt", reflect.TypeOf(Receipt{}), root},
		{"Grant", reflect.TypeOf(Grant{}), grant},
		{"Delivery", reflect.TypeOf(Delivery{}), grant.child(t, "delivery")},
		{"Evidence", reflect.TypeOf(Evidence{}), evidence},
		{"EvidenceArtifact", reflect.TypeOf(EvidenceArtifact{}), evidence.itemsOf(t, "artifacts")},
		{"CustodyLogEntry", reflect.TypeOf(CustodyLogEntry{}), evidence.itemsOf(t, "custody_log")},
		{"Teardown", reflect.TypeOf(Teardown{}), root.child(t, "teardown")},
	}
	for _, c := range cases {
		assertObjectConforms(t, c.name, c.typ, c.obj)
	}
}

// TestTeardownEnumMatchesConstants ties the schema's closed teardown.status
// enum to the exported constants, so adding a status to one without the
// other fails.
func TestTeardownEnumMatchesConstants(t *testing.T) {
	root := loadSchema(t)
	assertSetEqual(t, "teardown.status.enum", root.child(t, "teardown").child(t, "status").Enum,
		[]string{TeardownDestroyed, TeardownFailed, TeardownUnknown})
}

func TestSchemaVersion(t *testing.T) {
	root := loadSchema(t)
	if root.XVersion != SchemaVersion {
		t.Errorf("schema x-version = %q, want %q", root.XVersion, SchemaVersion)
	}
	if c := root.child(t, "schema_version").Const; c != SchemaVersion {
		t.Errorf("schema_version const = %q, want %q — schema documents and the Go gate must agree on version validity", c, SchemaVersion)
	}
}

// TestNoProviderLeakage mirrors contracts/execution's FR2 guard: no provider
// vocabulary in the normative schema surface. Fixture VALUES are out of
// scope by the same rationale execution's test states — this is FR2 working
// as intended, not a gap.
func TestNoProviderLeakage(t *testing.T) {
	forbidden := []string{"cursor", "claude", "codex", "prompt", "mcp", "subagent"}
	low := strings.ToLower(string(Schema))
	for _, word := range forbidden {
		if strings.Contains(low, word) {
			t.Errorf("schema contains provider vocabulary %q (FR2)", word)
		}
	}
}
