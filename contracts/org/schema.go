package org

import _ "embed"

// Schema is the JSON Schema (draft 2020-12) for a record spine — the same
// document the Go types are conformance-tested against.
//
// There is ONE schema in this package, not one per kind, and that is the
// spine/body split showing up as a saving: the spine is the only wire shape,
// and a body is an opaque content-addressed blob whose shape the chain never
// interprets. Per-kind law — which subject members a claim or an assign
// requires — is contract law in validate.go, because it is conditional on kind
// and JSON Schema expressing it would put the same rule in two places that
// drift.
//
// It is embedded so a tool, or a second implementation in another language, can
// validate against the one source of truth instead of re-deriving it from
// prose. This stdlib-only package runs no runtime JSON-Schema validator; the
// conformance test keeps the document and the Go types in lockstep.
//
//go:embed schema/org-record-v1.json
var Schema []byte
