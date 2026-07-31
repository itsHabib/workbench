package automode

import _ "embed"

// DecisionSchema is the portable AutoDecisionV1 JSON Schema.
//
//go:embed schema/auto-decision-v1.json
var DecisionSchema []byte

// AuditSchema is the portable AutoModeAuditEventV1 JSON Schema.
//
//go:embed schema/auto-audit-event-v1.json
var AuditSchema []byte
