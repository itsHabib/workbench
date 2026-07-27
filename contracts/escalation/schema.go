package escalation

import _ "embed"

// SchemaVersion is the one version this embedded schema ships. Validate accepts
// exactly this value and rejects anything else; the tolerant DecodeBody applies
// no version gate at all. There is exactly one version today — a compatibility
// rule is decided if and when a v2 first exists, from evidence, mirroring
// contracts/authority's posture.
const SchemaVersion = "escalation.v1"

// Schema is the JSON Schema (draft 2020-12) for an escalation body. It is
// embedded so a tool — including cross-language readers like ship and dossier,
// which read schema files, not Go types — can serve or validate against the one
// source of truth instead of hand-rolling a copy. The Go types in this package
// are the ergonomic view of this document; conformance_test.go keeps the two in
// lockstep. This stdlib-only leaf runs no runtime JSON-Schema validator:
// structural laws the schema document states (the closed Code enum, the nested
// required lists) are admission-validator input, not runtime-enforced by decode
// — the same honest layering contracts/authority and contracts/execution keep.
//
//go:embed schema/escalation-v1.json
var Schema []byte
