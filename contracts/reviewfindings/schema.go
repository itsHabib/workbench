package reviewfindings

import _ "embed"

// Schema is the portable ReviewFindingsV1 JSON Schema.
//
//go:embed schema/review-findings-v1.json
var Schema []byte

// AddressWorkSchema is the portable AddressWorkV1 JSON Schema.
//
//go:embed schema/review-address-work-v1.json
var AddressWorkSchema []byte
