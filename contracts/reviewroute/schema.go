package reviewroute

import _ "embed"

// PolicySchema is the portable ReviewPolicyV1 JSON Schema.
//
//go:embed schema/review-policy-v1.json
var PolicySchema []byte

// PlanSchema is the portable ReviewPlanV1 JSON Schema.
//
//go:embed schema/review-plan-v1.json
var PlanSchema []byte

// RequestSchema is the portable ReviewRequestV1 JSON Schema.
//
//go:embed schema/review-request-v1.json
var RequestSchema []byte

// CycleInputSchema is the portable ReviewCycleInputV1 JSON Schema.
//
//go:embed schema/review-cycle-input-v1.json
var CycleInputSchema []byte

// DecisionSchema is the portable ReviewDecisionV1 JSON Schema.
//
//go:embed schema/review-decision-v1.json
var DecisionSchema []byte
