package grantrequest

import _ "embed"

// Schema is the portable JSON Schema for RequestArtifact.
//
//go:embed schema/grant-request-v1.json
var Schema []byte

// DenialSchema is the portable JSON Schema for Denial.
//
//go:embed schema/grant-denial-v1.json
var DenialSchema []byte
