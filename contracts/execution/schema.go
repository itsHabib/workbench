package execution

import _ "embed"

// SchemaVersion is the one version all four embedded schemas ship. The Decode
// functions accept exactly this value and reject anything else (FR14).
const SchemaVersion = "0.1.0"

// The four JSON Schemas (draft 2020-12) are embedded so a tool can serve or
// validate against the one source of truth instead of hand-rolling a copy.
// The Go types in this package are the ergonomic view of these documents;
// execution_conformance_test.go keeps the two in lockstep. This stdlib-only
// module runs no runtime JSON-Schema validator: structural laws the schema
// documents state are enforced in Go by admission validation, not by decode.

// WorkSpecSchema is the JSON Schema for a portable work spec.
//
//go:embed schema/work-spec-v0.1.0.json
var WorkSpecSchema []byte

// RequestSchema is the JSON Schema for a placed run request.
//
//go:embed schema/execution-request-v0.1.0.json
var RequestSchema []byte

// EventSchema is the JSON Schema for a run event.
//
//go:embed schema/execution-event-v0.1.0.json
var EventSchema []byte

// ResultSchema is the JSON Schema for a terminal result.
//
//go:embed schema/execution-result-v0.1.0.json
var ResultSchema []byte
