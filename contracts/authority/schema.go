package authority

import _ "embed"

// SchemaVersion is the one version this embedded schema ships. DecodeReceipt
// accepts exactly this value and rejects anything else.
const SchemaVersion = "authority-receipt.v1"

// Schema is the JSON Schema (draft 2020-12) for a room-authority receipt. It
// is embedded so a tool can serve or validate against the one source of
// truth instead of hand-rolling a copy. The Go types in this package are the
// ergonomic view of this document; conformance_test.go keeps the two in
// lockstep. This stdlib-only module runs no runtime JSON-Schema validator:
// structural laws the schema document states are enforced in Go by
// admission validation elsewhere (not in this leaf), same posture as
// contracts/execution.
//
//go:embed schema/authority-receipt-v1.json
var Schema []byte
