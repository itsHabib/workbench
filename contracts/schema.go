package contracts

import _ "embed"

// VerdictSchemaVersion is the version of the schema VerdictSchema carries.
const VerdictSchemaVersion = "0.3.0"

// VerdictSchema is the JSON Schema (draft 2020-12) for a Verdict body — the
// same document the Go types are conformance-tested against. It is embedded so
// a tool can serve or validate against the one source of truth instead of
// re-parsing prose or hand-rolling a copy. The Go types in this package are the
// ergonomic view of this schema; conformance_test.go keeps the two in lockstep.
//
//go:embed schema/verdict-v0.3.0.json
var VerdictSchema []byte
