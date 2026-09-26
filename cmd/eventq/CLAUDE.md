# eventq

Local metadata queries over immutable JSONL projections. Read README.md and
docs/DESIGN.md. Library code stays private under internal/query; consumers use
the CLI/JSON boundary. No cross-tool decision imports.

Preserve exact int64 and null semantics, bounded ingestion, private output modes,
no-clobber publication, source-line provenance, and the Codex metadata allowlist.
Never add command/output/prompt payloads to the default projection. Indexes and
real traces stay untracked. Nonzero exit does not imply a defect; summed duration
does not imply elapsed time. Snapshot queries never claim current Fleet status.

Run go test -race ./cmd/eventq/..., go vet ./cmd/eventq/... and golangci-lint run
./cmd/eventq/... from the module root. Also run the Go 1.27 SIMD and forced-
emulation commands in README.md for kernel changes. The scalar implementation
is the reference; tail lengths, signed extrema, nulls, and prior masks must agree.
Keep this file and CLAUDE.md byte-identical. Follow the repository-wide checks
and delivery policy. Do not change the module's default Go version for SIMD.
