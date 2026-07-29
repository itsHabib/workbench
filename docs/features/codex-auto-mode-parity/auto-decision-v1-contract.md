**Status**: draft
**Owner**: @codex:michael
**Date**: 2026-07-29
**Related**: dossier task `auto-decision-v1-contract` (`tsk_01KYP78RWR1HJ0P8KWEZ0QJMA2`)

# AutoDecisionV1 contract

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Production | `contracts/automode/` | ~180 | 180 |
| Tests/schema | `contracts/automode/` | ~260 | 130 |
| **Total** | | | **~310** |

Band: **amazing**.

## Goal

Define the leaf, versioned artifact shared by Codex auto-mode policy and hook
adapters: outcome, rule-fired, remedy, harness, and a secret-safe stable action
digest.

## Behavior / fix

- Add an explicit v1 schema and Go types.
- Validate required fields and supported major versions.
- Tolerate unknown optional v1 fields without changing the routing projection.
- Define canonical digest inputs without persisting raw secrets or command text.
- Keep command classification and authorization policy out of `contracts`.

## Acceptance

Valid fixtures round-trip deterministically. Unknown major and malformed
required fields refuse. Leaf-package hygiene passes.

## Test plan

Focused example/property tests, `gofmt -l .`, `go vet ./...`,
`golangci-lint run ./...`, `go test ./...`, and hygiene.

## Non-goals

No Codex hook adapter, rules installation, Gate policy change, or generic rules
framework.
