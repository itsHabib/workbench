package driverstate

import _ "embed"

// SchemaVersion is the x-version the embedded schema carries — the bare version
// number. Version is the value the Event.V field and the schema $id name carry.
const (
	SchemaVersion = "0.1.0"
	Version       = "driver-state-v0.1.0"
)

// Schema is the JSON Schema (draft 2020-12) for a driver-state Event and its
// per-kind body payloads ($defs). It is embedded so a tool can serve or
// validate against the one source of truth instead of hand-rolling a copy. The
// Go types in this package are the ergonomic view of this document;
// conformance_test.go keeps the two in lockstep.
//
//go:embed schema/driver-state-v0.1.0.json
var Schema []byte
