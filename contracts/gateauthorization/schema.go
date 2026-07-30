package gateauthorization

import _ "embed"

// AuthorizationSchema is the portable GateAuthorizationV1 JSON Schema.
//
//go:embed schema/gate-authorization-v1.json
var AuthorizationSchema []byte

// AuthorizationRequestSchema is the pre-approval GateAuthorizationRequestV1 schema.
//
//go:embed schema/gate-authorization-request-v1.json
var AuthorizationRequestSchema []byte

// PreparationRequestSchema is the protected hosted Gate preparation request.
//
//go:embed schema/gate-preparation-request-v1.json
var PreparationRequestSchema []byte

// ExecutionClaimSchema is the portable GateExecutionClaimV1 JSON Schema.
//
//go:embed schema/gate-execution-claim-v1.json
var ExecutionClaimSchema []byte

// ExecutionResultSchema is the portable GateExecutionResultV1 JSON Schema.
//
//go:embed schema/gate-execution-result-v1.json
var ExecutionResultSchema []byte

// Schema remains an alias for the original exported authorization schema.
var Schema = AuthorizationSchema
