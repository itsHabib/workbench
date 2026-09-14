# Passage design

The [discussion design](../../../docs/features/passage/spec.md) owns the lifecycle
proposal and reconciliation with Rooms, Fleet and Gate. This document describes
the bounded command specimen.

`internal/passage/model.go` defines the private record and replay invariants.
`work.go` owns requirements, freshness, mutation and reopening decisions.
`files.go` handles bounded reads, input hashes, Git observations, locks and atomic
publication. `main.go` is the CLI. No other tool's code is imported.

One record contains the frozen selected contract, canonical local input root and
ordered events. Its revision is derived from event count. Current phase and active
receipts are replayed, not independently stored. Advance retains the receipts it
consumed. Reopen changes the active projection without deleting prior events.

This file format is intentionally private and provisional. It does not replace
Fleet receipts, Rooms result formats or the driver ledger. See the discussion
design for candidate seams and what a real composed trial needs to establish.
