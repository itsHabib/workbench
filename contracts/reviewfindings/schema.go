package reviewfindings

import _ "embed"

// Schema is the portable ReviewFindingsV1 JSON Schema.
//
//go:embed schema/review-findings-v1.json
var Schema []byte
