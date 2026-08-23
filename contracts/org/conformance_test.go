package org

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// objSchema is the slice of JSON Schema this package asserts its Go types
// against. It follows the walk contracts/execution and contracts/authority use,
// minus the discriminated-union support this vocabulary does not need: the
// spine is one shape, not a union.
type objSchema struct {
	XVersion   string               `json:"x-version"`
	Type       json.RawMessage      `json:"type"`
	Required   []string             `json:"required"`
	Properties map[string]objSchema `json:"properties"`
	Items      *objSchema           `json:"items"`
	Enum       []string             `json:"enum"`
	Const      json.RawMessage      `json:"const"`
	Pattern    string               `json:"pattern"`
	MinItems   int                  `json:"minItems"`
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

// TestSchemaMatchesGoTypes keeps the embedded document and the Go types in
// lockstep in both directions. A property in one and not the other is a
// contract that means two different things depending on which language reads
// it, which is the exact failure a shared schema exists to prevent.
func TestSchemaMatchesGoTypes(t *testing.T) {
	s := loadSchema(t)
	if s.XVersion != SchemaVersion {
		t.Errorf("schema x-version %q, package SchemaVersion %q", s.XVersion, SchemaVersion)
	}
	cases := []struct {
		name   string
		schema objSchema
		fields []string
	}{
		{"Record", s, jsonFieldNames(t, typeOf(Record{}))},
		{"Ref", s.child(t, "refs").itemsOrFail(t), jsonFieldNames(t, typeOf(Ref{}))},
		{"Subject", s.child(t, "subject"), jsonFieldNames(t, typeOf(Subject{}))},
		{"Terms", s.child(t, "terms"), jsonFieldNames(t, typeOf(Terms{}))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, f := range tc.fields {
				if _, ok := tc.schema.Properties[f]; !ok {
					t.Errorf("Go field %q has no schema property", f)
				}
			}
			for p := range tc.schema.Properties {
				if !slices.Contains(tc.fields, p) {
					t.Errorf("schema property %q has no Go field", p)
				}
			}
		})
	}
}

func (o objSchema) itemsOrFail(t *testing.T) objSchema {
	t.Helper()
	if o.Items == nil {
		t.Fatal("schema property has no items")
	}
	return *o.Items
}

// TestSchemaPinsTheVersions proves the two version constants a reader gates on
// are stated in the document rather than only in Go, so a non-Go implementation
// reads the same values.
func TestSchemaPinsTheVersions(t *testing.T) {
	s := loadSchema(t)
	if got := unquote(s.child(t, "scheme").Const); got != Scheme {
		t.Errorf("schema pins scheme %q, package Scheme is %q", got, Scheme)
	}
	if got := unquote(s.child(t, "v").Const); got != "1" {
		t.Errorf("schema pins v %q, package Version is %d", got, Version)
	}
}

// TestSchemaEnumeratesTheKindClasses is the one enum that must not drift: the
// class is what an old reader dispatches on, so a value in the document that
// Go does not implement (or the reverse) would decide a takeover's fate.
func TestSchemaEnumeratesTheKindClasses(t *testing.T) {
	got := slices.Clone(loadSchema(t).child(t, "kind_class").Enum)
	want := []string{ClassAdvisory, ClassStructural}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("schema kind_class enum %v, package classes %v", got, want)
	}
}

// TestSchemaTrustEnumMatches pins the trust vocabulary for the same reason.
func TestSchemaTrustEnumMatches(t *testing.T) {
	s := loadSchema(t)
	got := slices.Clone(s.child(t, "refs").itemsOrFail(t).child(t, "trust").Enum)
	want := []string{TrustExternal, TrustInternal, TrustOperator}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("schema trust enum %v, package labels %v", got, want)
	}
}

// TestSchemaPatternsAgreeWithGo pins the doubling the package deliberately
// accepts: the grammars are stated in the schema for a second implementation
// AND compiled in Go for admission. Stating them twice is fine; letting them
// disagree is not, and only a test can tell the difference.
func TestSchemaPatternsAgreeWithGo(t *testing.T) {
	s := loadSchema(t)
	cases := []struct {
		what    string
		pattern string
		accept  []string
		reject  []string
	}{
		{"role", s.child(t, "role").Pattern,
			[]string{"human:mh", "ic:contracts-org", "lead:platform"},
			[]string{"IC:Thing", "ic::thing", ""}},
		{"incarnation", s.child(t, "incarnation").Pattern,
			[]string{digestOfString("i")},
			[]string{"session-4", "sha256:beef"}},
		{"work", s.child(t, "subject").child(t, "work").Pattern,
			[]string{"github:acme/api#88", "jira:PROJ-412", "dossier:p/ph/t"},
			[]string{"PROJ-412", "the api", ""}},
	}
	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			re := goPattern(t, tc.what)
			for _, s := range tc.accept {
				if !re(s) {
					t.Errorf("Go rejects %q, which the schema pattern %q accepts", s, tc.pattern)
				}
			}
			for _, s := range tc.reject {
				if re(s) {
					t.Errorf("Go accepts %q, which the schema pattern %q rejects", s, tc.pattern)
				}
			}
		})
	}
}

// goPattern returns the compiled grammar validate.go actually admits with, so
// the assertion above compares behavior rather than two copies of a string.
func goPattern(t *testing.T, what string) func(string) bool {
	t.Helper()
	switch what {
	case "role":
		return idPattern.MatchString
	case "incarnation", "digest":
		return digestPattern.MatchString
	case "work":
		return workURIPattern.MatchString
	}
	t.Fatalf("no Go grammar named %q", what)
	return nil
}

// TestSchemaRequiresTheSpine pins that the fields a fold cannot work without
// are required by the document, not merely conventional.
func TestSchemaRequiresTheSpine(t *testing.T) {
	required := loadSchema(t).Required
	for _, f := range []string{"v", "scheme", "seq", "prev", "tenant", "role", "kind", "kind_class", "fence", "at"} {
		if !slices.Contains(required, f) {
			t.Errorf("%q is not required by the schema, but the fold cannot proceed without it", f)
		}
	}
	// incarnation must NOT be required: charter, attach and takeover mint one
	// and so cannot carry it.
	if slices.Contains(required, "incarnation") {
		t.Error("schema requires incarnation, which charter, attach and takeover cannot carry")
	}
}

func unquote(raw json.RawMessage) string {
	return strings.Trim(string(raw), `"`)
}
