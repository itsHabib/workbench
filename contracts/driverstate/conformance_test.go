package driverstate

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

// objSchema is the slice of JSON Schema this package asserts its Go types
// against: property names, which are required, nested object/array shapes,
// enums, the v const, and the $defs the per-kind body payloads live in. It
// mirrors the contracts and contracts/execution walkers structurally.
type objSchema struct {
	XVersion   string               `json:"x-version"`
	Type       string               `json:"type"`
	Required   []string             `json:"required"`
	Properties map[string]objSchema `json:"properties"`
	Items      *objSchema           `json:"items"`
	Enum       []string             `json:"enum"`
	Const      string               `json:"const"`
	Defs       map[string]objSchema `json:"$defs"`
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

func (o objSchema) def(t *testing.T, name string) objSchema {
	t.Helper()
	d, ok := o.Defs[name]
	if !ok {
		t.Fatalf("schema has no $defs/%q", name)
	}
	return d
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

// TestSchemaMatchesGoTypes is the acceptance-1 gate: the Event envelope and
// every pinned body payload conform to the schema both ways. Drift in either
// the Go types or the schema fails here.
func TestSchemaMatchesGoTypes(t *testing.T) {
	root := loadSchema(t)
	cases := []struct {
		name string
		typ  reflect.Type
		obj  objSchema
	}{
		{"Event", reflect.TypeOf(Event{}), root},
		{"RunImportedBody", reflect.TypeOf(RunImportedBody{}), root.def(t, "run_imported_body")},
		{"StreamSpec", reflect.TypeOf(StreamSpec{}), root.def(t, "stream_spec")},
		{"StreamAttemptBody", reflect.TypeOf(StreamAttemptBody{}), root.def(t, "stream_attempt_body")},
		{"StreamPROpenedBody", reflect.TypeOf(StreamPROpenedBody{}), root.def(t, "stream_pr_opened_body")},
		{"StreamMergedBody", reflect.TypeOf(StreamMergedBody{}), root.def(t, "stream_merged_body")},
		{"ReviewCycleBody", reflect.TypeOf(ReviewCycleBody{}), root.def(t, "review_cycle_body")},
	}
	for _, c := range cases {
		assertObjectConforms(t, c.name, c.typ, c.obj)
	}
}

// TestEnumsMatchConstants is acceptance-2: the schema kind enum ties to the Kind
// constants (AllKinds), so adding a kind to one without the other fails. The v
// const is pinned to Version the same way.
func TestEnumsMatchConstants(t *testing.T) {
	root := loadSchema(t)
	want := make([]string, 0, len(AllKinds()))
	for _, k := range AllKinds() {
		want = append(want, string(k))
	}
	assertSetEqual(t, "kind.enum", root.child(t, "kind").Enum, want)

	if c := root.child(t, "v").Const; c != Version {
		t.Errorf("schema v const = %q, want %q", c, Version)
	}
}

func TestSchemaVersion(t *testing.T) {
	if v := loadSchema(t).XVersion; v != SchemaVersion {
		t.Errorf("schema x-version = %q, want %q", v, SchemaVersion)
	}
}

// TestUnknownFieldsIgnored is the tolerant-reader guarantee: an event body
// carrying a field this binary predates still decodes, and known fields survive.
func TestUnknownFieldsIgnored(t *testing.T) {
	line := []byte(`{"id":"evt_1","run":"dsr_1","v":"driver-state-v0.1.0","kind":"stream_pr_opened","stream":"dss_1","time":"2026-07-16T12:00:00Z","actor":"session:demo","body":{"pr":12,"url":"u","head_sha":"abc"},"prev":"","hash":"h","future_field":{"x":1}}`)
	e, err := DecodeEvent(line)
	if err != nil {
		t.Fatalf("tolerant reader must ignore unknown fields: %v", err)
	}
	if e.Kind != KindStreamPROpened || e.Stream != "dss_1" {
		t.Fatalf("known fields did not survive an unknown-field body: %+v", e)
	}
}

// TestTolerantReaderSkipsUnknownKind is acceptance-3: a ledger with an unknown
// future kind lists every known event and skips the unknown one with a warning,
// never a hard error (the driver list grok-4.5 lesson).
func TestTolerantReaderSkipsUnknownKind(t *testing.T) {
	data := readFixture(t, "ledger-unknown-kind.jsonl")
	events, warnings, err := ReadLedger(data)
	if err != nil {
		t.Fatalf("tolerant reader must not error on an unknown kind: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("want 2 known-kind events, got %d: %+v", len(events), events)
	}
	if events[0].Kind != KindRunImported || events[1].Kind != KindRunFinished {
		t.Fatalf("known events are wrong or out of order: %+v", events)
	}
	if len(warnings) != 1 {
		t.Fatalf("want exactly one skip warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "stream_teleported") {
		t.Errorf("warning must name the skipped kind, got %q", warnings[0])
	}
}

// TestPayloadValidationPerKind exercises the per-kind body grammar: a good body
// admits, a malformed one rejects at unmarshal-validate — including the bad
// stream_attempt seq the task plan calls out.
func TestPayloadValidationPerKind(t *testing.T) {
	cases := []struct {
		name    string
		kind    Kind
		body    string
		wantErr bool
	}{
		{"run_imported ok", KindRunImported, `{"repo":"r","source":"s","streams":[{"stream":"dss_1","doc_path":"d"}]}`, false},
		{"run_imported empty repo", KindRunImported, `{"repo":"","source":"s","streams":[]}`, true},
		{"run_imported stream missing doc_path", KindRunImported, `{"repo":"r","source":"s","streams":[{"stream":"dss_1"}]}`, true},
		{"stream_attempt ok", KindStreamAttempt, `{"seq":1,"doc_path":"d","terminal":false}`, false},
		{"stream_attempt bad seq", KindStreamAttempt, `{"seq":0,"doc_path":"d","terminal":false}`, true},
		{"stream_attempt failure on non-terminal", KindStreamAttempt, `{"seq":2,"doc_path":"d","terminal":false,"failure_category":"flake"}`, true},
		{"stream_attempt terminal failure ok", KindStreamAttempt, `{"seq":2,"doc_path":"d","terminal":true,"failure_category":"flake"}`, false},
		{"stream_pr_opened ok", KindStreamPROpened, `{"pr":12,"url":"u","head_sha":"abc"}`, false},
		{"stream_pr_opened bad pr", KindStreamPROpened, `{"pr":0,"url":"u","head_sha":"abc"}`, true},
		{"stream_merged ok", KindStreamMerged, `{"pr":12,"merge_commit":"abc","merged_at":"2026-07-16T12:00:00Z"}`, false},
		{"stream_merged empty merge_commit", KindStreamMerged, `{"pr":12,"merge_commit":"","merged_at":"t"}`, true},
		{"review_cycle ok", KindReviewCycle, `{"cycle":1,"panel_settled":true,"findings":0}`, false},
		{"review_cycle bad cycle", KindReviewCycle, `{"cycle":0,"panel_settled":true,"findings":0}`, true},
		{"malformed json", KindStreamAttempt, `{"seq":`, true},
		{"open kind tolerated", KindStreamDispatched, `{"anything":true}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateBody(c.kind, json.RawMessage(c.body))
			if c.wantErr && err == nil {
				t.Fatalf("want error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
		})
	}
}

// TestValidateEventScope pins the run-scoped/stream-scoped law and the identity
// and version gates ValidateEvent enforces.
func TestValidateEventScope(t *testing.T) {
	base := func() Event {
		return Event{ID: "evt_1", Run: "dsr_1", V: Version, Actor: "session:demo"}
	}
	cases := []struct {
		name    string
		mutate  func(*Event)
		wantErr bool
	}{
		{"run-scoped no stream ok", func(e *Event) { e.Kind = KindRunFinished }, false},
		{"run-scoped with stream rejects", func(e *Event) { e.Kind = KindRunFinished; e.Stream = "dss_1" }, true},
		{"stream-scoped with stream ok", func(e *Event) {
			e.Kind = KindStreamPROpened
			e.Stream = "dss_1"
			e.Body = json.RawMessage(`{"pr":1,"url":"u","head_sha":"a"}`)
		}, false},
		{"stream-scoped no stream rejects", func(e *Event) { e.Kind = KindStreamPROpened; e.Stream = "" }, true},
		{"empty id rejects", func(e *Event) { e.Kind = KindRunFinished; e.ID = "" }, true},
		{"wrong version rejects", func(e *Event) { e.Kind = KindRunFinished; e.V = "driver-state-v9.9.9" }, true},
		{"unknown kind tolerated", func(e *Event) { e.Kind = "stream_teleported"; e.Stream = "dss_1" }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := base()
			c.mutate(&e)
			err := ValidateEvent(e)
			if c.wantErr && err == nil {
				t.Fatalf("want error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
		})
	}
}

// vectorEvent is the reference event the canonical vector pins. Its body is a
// compact literal so the canonical bytes are deterministic and a TS emitter can
// reproduce them.
func vectorEvent() Event {
	body := `{"repo":"itsHabib/workbench","source":"docs/driver/driver.md","streams":[{"stream":"dss_01JQSTREAM0000000000000001","doc_path":"docs/features/driver-state/spec.md"}],"ship_run_ref":"drv_01JQSHIP000000000000000001"}`
	return Event{
		ID:     "evt_01JQEVENT00000000000000IMP0",
		Run:    "dsr_01JQRUN0000000000000000RUN0",
		V:      Version,
		Kind:   KindRunImported,
		Stream: "",
		Time:   time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
		Actor:  "session:demo-01",
		Body:   json.RawMessage(body),
		Prev:   "",
		Hash:   "",
	}
}

// canonicalVector is the pinned fixture: one full event, its expected canonical
// bytes, and its expected hash.
type canonicalVector struct {
	Canonical string `json:"canonical"`
	Hash      string `json:"hash"`
}

// TestCanonicalVector is the P1 canonical-encoding reference vector (spec §5,
// review M3): the fixed event must reproduce the pinned canonical bytes and
// hash. This is the cross-language chain anchor — if it drifts, the Go package
// and ship's TS emitter would silently disagree, so the suite fails loudly.
func TestCanonicalVector(t *testing.T) {
	var want canonicalVector
	if err := json.Unmarshal(readFixture(t, "canonical-vector.json"), &want); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	e := vectorEvent()
	gotCanonical := string(Canonical(e))
	if gotCanonical != want.Canonical {
		t.Errorf("canonical bytes drifted:\n got=%s\nwant=%s", gotCanonical, want.Canonical)
	}
	gotHash := ComputeHash(e)
	if gotHash != want.Hash {
		t.Errorf("canonical hash drifted:\n got=%s\nwant=%s", gotHash, want.Hash)
	}
}

// TestCanonicalIgnoresHashField proves the hash seals everything but itself: a
// stored Hash value does not change the canonical bytes or the recomputed hash,
// so a chain reader can recompute and compare.
func TestCanonicalIgnoresHashField(t *testing.T) {
	e := vectorEvent()
	withHash := e
	withHash.Hash = "deadbeef"
	if !reflect.DeepEqual(Canonical(e), Canonical(withHash)) {
		t.Fatal("canonical bytes must not depend on the hash field")
	}
	if ComputeHash(e) != ComputeHash(withHash) {
		t.Fatal("computed hash must not depend on the stored hash field")
	}
}

// TestRequiredFieldsAlwaysMarshal locks the on-the-wire shape a consumer relies
// on: every required key is present even on a zero event, and the optional
// stream is omitted when empty.
func TestRequiredFieldsAlwaysMarshal(t *testing.T) {
	out, err := json.Marshal(Event{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, key := range []string{`"id"`, `"run"`, `"v"`, `"kind"`, `"time"`, `"actor"`, `"body"`, `"prev"`, `"hash"`} {
		if !strings.Contains(s, key) {
			t.Errorf("required key %s missing from a marshaled zero Event: %s", key, s)
		}
	}
	if strings.Contains(s, `"stream"`) {
		t.Errorf("optional stream must be omitted when empty: %s", s)
	}
}

func TestRoundTrip(t *testing.T) {
	e := vectorEvent()
	e.Hash = ComputeHash(e)
	out, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeEvent(out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e, got) {
		t.Fatalf("round-trip mismatch:\n in=%+v\nout=%+v", e, got)
	}
}

// TestDecodeEventVersionGate is the loud half of tolerance: the reader accepts
// exactly the version this contract ships and rejects anything else via
// ErrUnknownVersion.
func TestDecodeEventVersionGate(t *testing.T) {
	good := `{"id":"e","run":"r","v":"driver-state-v0.1.0","kind":"run_finished","time":"2026-07-16T12:00:00Z","actor":"a","body":null,"prev":"","hash":"h"}`
	if _, err := DecodeEvent([]byte(good)); err != nil {
		t.Fatalf("current version must decode: %v", err)
	}
	bad := `{"id":"e","run":"r","v":"driver-state-v0.2.0","kind":"run_finished","time":"2026-07-16T12:00:00Z","actor":"a","body":null,"prev":"","hash":"h"}`
	if _, err := DecodeEvent([]byte(bad)); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("a future version must reject via the version gate, got %v", err)
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
