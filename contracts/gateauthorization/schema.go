package gateauthorization

import _ "embed"

// Schema is the portable GateAuthorizationV1 JSON Schema.
//
//go:embed schema/gate-authorization-v1.json
var Schema []byte
