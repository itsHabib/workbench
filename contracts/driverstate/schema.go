package driverstate

import _ "embed"

// SchemaVersion is the x-version the embedded schema carries — the bare version
// number. Version is the value the Event.V field and the schema $id name carry.
const (
	SchemaVersion = "0.1.0"
	Version       = "driver-state-v0.1.0"
	// AddressSchemaVersion is the additive authority-bearing address contract.
	AddressSchemaVersion = "0.2.0"
	// AddressVersion is written only for review_address_* events. Readers accept
	// it in the same chain as Version; an installed v0.1 writer refuses it.
	AddressVersion = "driver-state-v0.2.0"
)

// Schema is the JSON Schema (draft 2020-12) for a driver-state Event and its
// per-kind body payloads ($defs). It is embedded so a tool can serve or
// validate against the one source of truth instead of hand-rolling a copy. The
// Go types in this package are the ergonomic view of this document;
// conformance_test.go keeps the two in lockstep.
//
//go:embed schema/driver-state-v0.1.0.json
var Schema []byte

// AddressSchema is the v0.2 schema for authority-bearing address events.
//
//go:embed schema/driver-state-v0.2.0.json
var AddressSchema []byte
