package reviewpanel

import _ "embed"

// Schema is the portable ReviewPanelV1 JSON Schema.
//
//go:embed schema/review-panel-v1.json
var Schema []byte
