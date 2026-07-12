package execution

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// objSchema is the slice of JSON Schema this package asserts its Go types
// against: property names, which are required, nested object/array shapes,
// enums, and — the execution vocabulary's novelty — oneOf discriminated
// unions. It extends the contracts package's walk structurally: without the
// OneOf field a decode would drop the union branches and the walk would pass
// vacuously on the exact shapes this domain exists to pin.
type objSchema struct {
	XVersion   string               `json:"x-version"`
	Type       string               `json:"type"`
	Required   []string             `json:"required"`
	Properties map[string]objSchema `json:"properties"`
	Items      *objSchema           `json:"items"`
	Enum       []string             `json:"enum"`
	OneOf      []objSchema          `json:"oneOf"`
}

func loadSchemaDoc(t *testing.T, raw []byte) objSchema {
	t.Helper()
	var s objSchema
	if err := json.Unmarshal(raw, &s); err != nil {
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

// unionBranch returns the oneOf member whose required property is the given
// discriminant.
func (o objSchema) unionBranch(t *testing.T, discriminant string) objSchema {
	t.Helper()
	for _, b := range o.OneOf {
		if contains(b.Required, discriminant) {
			return b
		}
	}
	t.Fatalf("schema has no oneOf branch requiring %q", discriminant)
	return objSchema{}
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
// so a required field always marshals and an optional one never marshals empty.
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

// assertUnionConforms pins a Go pointer-branch union struct to a schema oneOf.
// It fails loudly when the schema carries no branches — the guard against a
// vacuous pass — then checks each branch is an object with exactly one
// required discriminant that maps to a Go field, every Go field is omitempty
// (each is absent in the other branches), and the union of branch properties
// equals the Go field set exactly: one pointer per oneOf member.
func assertUnionConforms(t *testing.T, name string, typ reflect.Type, obj objSchema) {
	t.Helper()
	if len(obj.OneOf) == 0 {
		t.Fatalf("%s: schema has no oneOf branches — the union walk would pass vacuously", name)
	}
	fields := jsonFields(typ)
	if len(obj.OneOf) != len(fields) {
		t.Errorf("%s: %d oneOf branches but %d Go fields — each pointer branch maps to one member", name, len(obj.OneOf), len(fields))
	}
	seen := map[string]bool{}
	for i, branch := range obj.OneOf {
		assertUnionBranch(t, fmt.Sprintf("%s.oneOf[%d]", name, i), branch, fields, seen)
	}
	for f := range fields {
		if !seen[f] {
			t.Errorf("%s: Go field %q maps to no oneOf branch", name, f)
		}
	}
}

func assertUnionBranch(t *testing.T, name string, branch objSchema, fields map[string]goField, seen map[string]bool) {
	t.Helper()
	if branch.Type != "object" {
		t.Errorf("%s: branch type = %q, want object", name, branch.Type)
	}
	if len(branch.Required) != 1 {
		t.Errorf("%s: branch requires %v, want exactly one discriminant", name, branch.Required)
	}
	for prop := range branch.Properties {
		seen[prop] = true
		f, ok := fields[prop]
		if !ok {
			t.Errorf("%s: schema property %q has no Go field", name, prop)
			continue
		}
		if !f.omitempty {
			t.Errorf("%s: union field %q must be omitempty in Go", name, prop)
		}
	}
}

// assertPathRef pins one structured-reference position: the {root, value}
// shape and the closed root enum. Applying it to every position (cwd, the
// executable path branch, the args path branch) makes drift between positions
// fail.
func assertPathRef(t *testing.T, name string, obj objSchema) {
	t.Helper()
	assertObjectConforms(t, name, reflect.TypeOf(PathRef{}), obj)
	assertSetEqual(t, name+".root.enum", obj.child(t, "root").Enum,
		[]string{RootWorkspace, RootInputs, RootOut})
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

func TestWorkSpecConformance(t *testing.T) {
	root := loadSchemaDoc(t, WorkSpecSchema)
	command := root.child(t, "command")
	cases := []struct {
		name string
		typ  reflect.Type
		obj  objSchema
	}{
		{"WorkSpec", reflect.TypeOf(WorkSpec{}), root},
		{"Command", reflect.TypeOf(Command{}), command},
		{"Workspace", reflect.TypeOf(Workspace{}), root.child(t, "workspace")},
		{"Input", reflect.TypeOf(Input{}), root.itemsOf(t, "inputs")},
		{"Secret", reflect.TypeOf(Secret{}), root.itemsOf(t, "secrets")},
		{"Output", reflect.TypeOf(Output{}), root.itemsOf(t, "outputs")},
	}
	for _, c := range cases {
		assertObjectConforms(t, c.name, c.typ, c.obj)
	}

	executable := command.child(t, "executable")
	arg := command.itemsOf(t, "args")
	assertUnionConforms(t, "Executable", reflect.TypeOf(Executable{}), executable)
	assertUnionConforms(t, "Arg", reflect.TypeOf(Arg{}), arg)

	assertPathRef(t, "cwd", root.child(t, "cwd"))
	assertPathRef(t, "executable.path", executable.unionBranch(t, "path").child(t, "path"))
	assertPathRef(t, "arg.path", arg.unionBranch(t, "path").child(t, "path"))
}

func TestRequestConformance(t *testing.T) {
	root := loadSchemaDoc(t, RequestSchema)
	cases := []struct {
		name string
		typ  reflect.Type
		obj  objSchema
	}{
		{"Request", reflect.TypeOf(Request{}), root},
		{"Work", reflect.TypeOf(Work{}), root.child(t, "work")},
		{"Placement", reflect.TypeOf(Placement{}), root.child(t, "placement")},
		{"Policy", reflect.TypeOf(Policy{}), root.child(t, "policy")},
	}
	for _, c := range cases {
		assertObjectConforms(t, c.name, c.typ, c.obj)
	}
}

func TestEventConformance(t *testing.T) {
	root := loadSchemaDoc(t, EventSchema)
	assertObjectConforms(t, "RunEvent", reflect.TypeOf(RunEvent{}), root)
}

func TestResultConformance(t *testing.T) {
	root := loadSchemaDoc(t, ResultSchema)
	cases := []struct {
		name string
		typ  reflect.Type
		obj  objSchema
	}{
		{"Result", reflect.TypeOf(Result{}), root},
		{"PlacementReceipt", reflect.TypeOf(PlacementReceipt{}), root.child(t, "placement")},
		{"Cause", reflect.TypeOf(Cause{}), root.itemsOf(t, "causes")},
		{"Diagnostic", reflect.TypeOf(Diagnostic{}), root.itemsOf(t, "diagnostics")},
		{"Artifact", reflect.TypeOf(Artifact{}), root.itemsOf(t, "artifacts")},
	}
	for _, c := range cases {
		assertObjectConforms(t, c.name, c.typ, c.obj)
	}
}

// TestEnumsMatchConstants ties the exported constants to the closed schema
// enums — phase, status, reason_code, stream_delivery, workspace kind — so
// adding a value to one without the other fails. Kind is deliberately absent:
// it is open vocabulary (see TestOpenVocabularies).
func TestEnumsMatchConstants(t *testing.T) {
	phases := []string{PhaseAdmission, PhasePreparation, PhaseStartup,
		PhaseWorkload, PhaseCollection, PhaseCleanup, PhaseTerminal}
	reasons := []string{ReasonCompleted, ReasonPreparationFailed, ReasonStartupFailed,
		ReasonWorkloadFailed, ReasonDeadlineExceeded, ReasonCancelRequested,
		ReasonCollectionFailed, ReasonCleanupFailed, ReasonControllerLost,
		ReasonPlacementUnavailable}

	event := loadSchemaDoc(t, EventSchema)
	assertSetEqual(t, "event.phase.enum", event.child(t, "phase").Enum, phases)

	result := loadSchemaDoc(t, ResultSchema)
	assertSetEqual(t, "result.status.enum", result.child(t, "status").Enum,
		[]string{StatusSucceeded, StatusFailed, StatusTimedOut, StatusCancelled})
	assertSetEqual(t, "result.terminal_phase.enum", result.child(t, "terminal_phase").Enum, phases)
	assertSetEqual(t, "result.reason_code.enum", result.child(t, "reason_code").Enum, reasons)

	cause := result.itemsOf(t, "causes")
	assertSetEqual(t, "cause.phase.enum", cause.child(t, "phase").Enum, phases)
	assertSetEqual(t, "cause.reason_code.enum", cause.child(t, "reason_code").Enum, reasons)

	receipt := result.child(t, "placement")
	assertSetEqual(t, "placement.stream_delivery.enum", receipt.child(t, "stream_delivery").Enum,
		[]string{StreamDeliveryTerminalReplay, StreamDeliveryNone})

	work := loadSchemaDoc(t, WorkSpecSchema)
	assertSetEqual(t, "workspace.kind.enum", work.child(t, "workspace").child(t, "kind").Enum,
		[]string{WorkspaceKindGit})
}

// TestOpenVocabularies pins the two deliberately OPEN strings: event kind
// (kinds are additive within a major schema) and placement backend (resolved
// against installed adapters, D4). An enum appearing on either is contract
// drift.
func TestOpenVocabularies(t *testing.T) {
	event := loadSchemaDoc(t, EventSchema)
	if enum := event.child(t, "kind").Enum; len(enum) != 0 {
		t.Errorf("event.kind must stay an open string, got enum %v", enum)
	}
	request := loadSchemaDoc(t, RequestSchema)
	if enum := request.child(t, "placement").child(t, "backend").Enum; len(enum) != 0 {
		t.Errorf("placement.backend must stay an open string (D4), got enum %v", enum)
	}
}

func TestSchemaVersions(t *testing.T) {
	docs := map[string][]byte{
		"work-spec": WorkSpecSchema,
		"request":   RequestSchema,
		"event":     EventSchema,
		"result":    ResultSchema,
	}
	for name, raw := range docs {
		if v := loadSchemaDoc(t, raw).XVersion; v != SchemaVersion {
			t.Errorf("%s schema x-version = %q, want %q", name, v, SchemaVersion)
		}
	}
}

// TestVersionGate is the FR14 loud half: the reader accepts exactly the one
// version these schemas ship and rejects anything else — across all four
// documents, plus the golden unknown-version and garbage-version fixtures.
func TestVersionGate(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		decode  func([]byte) error
	}{
		{"work spec", "work-spec-name.json", func(b []byte) error { _, err := DecodeWorkSpec(b); return err }},
		{"request", "request.json", func(b []byte) error { _, err := DecodeRequest(b); return err }},
		{"event", "event.json", func(b []byte) error { _, err := DecodeEvent(b); return err }},
		{"result", "result.json", func(b []byte) error { _, err := DecodeResult(b); return err }},
	}
	current := []byte(`"schema_version": "0.1.0"`)
	for _, c := range cases {
		raw := readFixture(t, c.fixture)
		if !bytes.Contains(raw, current) {
			t.Fatalf("%s: fixture carries no current schema_version to bump", c.name)
		}
		if err := c.decode(raw); err != nil {
			t.Errorf("%s: current version must decode: %v", c.name, err)
		}
		bumped := bytes.Replace(raw, current, []byte(`"schema_version": "0.2.0"`), 1)
		if err := c.decode(bumped); !errors.Is(err, ErrUnknownSchemaVersion) {
			t.Errorf("%s: unrecognized version must reject loudly, got %v", c.name, err)
		}
	}

	for _, fixture := range []string{"invalid-work-spec-unknown-version.json", "invalid-work-spec-garbage-version.json"} {
		if _, err := DecodeWorkSpec(readFixture(t, fixture)); !errors.Is(err, ErrUnknownSchemaVersion) {
			t.Errorf("%s: must reject via the version gate, got %v", fixture, err)
		}
	}
}

// TestGoldenRoundTrip proves each valid fixture survives decode and re-encode
// losslessly: the re-encoded JSON is value-equal to the fixture (no field
// dropped or mutated) and a second decode is Go-value-equal to the first. The
// two work-spec fixtures cover both executable variants and both arg variants,
// including an empty-string literal.
func TestGoldenRoundTrip(t *testing.T) {
	cases := []struct {
		fixture string
		decode  func([]byte) (any, error)
	}{
		{"work-spec-name.json", func(b []byte) (any, error) { return DecodeWorkSpec(b) }},
		{"work-spec-path.json", func(b []byte) (any, error) { return DecodeWorkSpec(b) }},
		{"request.json", func(b []byte) (any, error) { return DecodeRequest(b) }},
		{"event.json", func(b []byte) (any, error) { return DecodeEvent(b) }},
		{"result.json", func(b []byte) (any, error) { return DecodeResult(b) }},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			raw := readFixture(t, c.fixture)
			first, err := c.decode(raw)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			out, err := json.Marshal(first)
			if err != nil {
				t.Fatalf("re-encode: %v", err)
			}
			assertJSONEqual(t, raw, out)
			second, err := c.decode(out)
			if err != nil {
				t.Fatalf("second decode: %v", err)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("round-trip mismatch:\n first=%+v\nsecond=%+v", first, second)
			}
		})
	}
}

// assertJSONEqual compares two JSON documents as decoded values, so
// formatting differs but any dropped or mutated field fails.
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

// TestUnknownFieldsIgnored is the tolerant-reader guarantee (FR14): a body
// carrying a field this binary predates still decodes, and known fields
// survive.
func TestUnknownFieldsIgnored(t *testing.T) {
	body := []byte(`{"schema_version":"0.1.0","run_id":"run_1","seq":3,"time":"2026-07-10T16:40:13.585Z","phase":"workload","kind":"workload_started","future_field":{"x":1}}`)
	e, err := DecodeEvent(body)
	if err != nil {
		t.Fatalf("tolerant reader must ignore unknown fields: %v", err)
	}
	if e.Kind != KindWorkloadStarted || e.Seq != 3 {
		t.Fatalf("known fields did not survive an unknown-field body: %+v", e)
	}
}

// TestTolerantDecodeDefersToAdmission pins the honest layering (G6): this
// stdlib-only module runs no runtime JSON-Schema validator, so a
// missing-required or enum-violating instance decodes tolerantly here. Its
// rejection is Go admission validation's job — the sibling
// runway-semantic-validators-reducer task — never a claim the schema document
// enforces at runtime.
func TestTolerantDecodeDefersToAdmission(t *testing.T) {
	w, err := DecodeWorkSpec(readFixture(t, "invalid-work-spec-missing-workspace.json"))
	if err != nil {
		t.Fatalf("missing-required must still decode (admission rejects it later): %v", err)
	}
	if w.Workspace != (Workspace{}) {
		t.Errorf("missing workspace must decode to the zero value, got %+v", w.Workspace)
	}

	e, err := DecodeEvent(readFixture(t, "invalid-event-bad-phase.json"))
	if err != nil {
		t.Fatalf("enum violation must still decode (admission rejects it later): %v", err)
	}
	if e.Phase != "warmup" {
		t.Errorf("the violating value must survive decode untouched, got %q", e.Phase)
	}
}

// TestNoProviderLeakage is the FR2 guard: no provider vocabulary anywhere in
// the four schema documents. Backend openness is pinned by
// TestOpenVocabularies; host-path absence is structural (every path position
// is a logical-root reference or a fixed-root relative string).
func TestNoProviderLeakage(t *testing.T) {
	docs := map[string][]byte{
		"work-spec": WorkSpecSchema,
		"request":   RequestSchema,
		"event":     EventSchema,
		"result":    ResultSchema,
	}
	forbidden := []string{"cursor", "claude", "codex", "model", "prompt", "mcp", "subagent"}
	for name, raw := range docs {
		assertNoTokens(t, name, raw, forbidden)
	}
}

func assertNoTokens(t *testing.T, name string, raw []byte, forbidden []string) {
	t.Helper()
	low := strings.ToLower(string(raw))
	for _, word := range forbidden {
		if strings.Contains(low, word) {
			t.Errorf("%s: schema contains provider vocabulary %q (FR2)", name, word)
		}
	}
}
