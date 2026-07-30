# Codex ReviewFindingsV1 producer

**Status:** implemented
**Date:** 2026-07-27
**Scope:** the smallest Phase 0/1 closure slice

## Audit against current main

The closure TDD remains the program charter, but several implementation claims
are stale:

- shared verdict convergence and the Tracelens/Gate/Triage migration have
  already landed in Workbench;
- Ship's `driver address` no longer accepts opaque prose. Ship owns a
  `ReviewFindingsV1` parser, exact-head validation, transactional consumption,
  canonical-digest replay protection, cycle exhaustion, and recovery;
- Gate already understands Codex exact-head review evidence and treats absent
  review evidence fail-closed;
- PR #155 adds Codex discovery pointers for scoped tool guidance. This slice
  follows its pointer shape for the new tool and does not duplicate existing
  guidance.

Still open:

- a fresh Codex task had no producer for Ship's landed artifact;
- `gate judge -auto` still shells the Claude CLI;
- Gate B's two real Codex/Claude closure runs, closure receipts, later
  `panel.missing` Gate behavior, and catalog integrity gate remain unproven.

The smallest end-to-end gap is therefore production, not consumption:
Codex needs to turn exact-head GitHub inline findings into Ship's already
validated artifact without hand-authoring JSON.

## Capability

`reviewfindings github` reads the live PR head and paginated inline review
comments through `gh`, selects only completed-panel comments attached to the
requested exact head, and writes one `ReviewFindingsV1` artifact atomically.

It refuses before replacing the output when:

- the requested reviewed head differs from the live PR head;
- the PR is not open;
- no sourced exact-head inline findings remain;
- the generated artifact violates the shared contract.

Ship remains authoritative for duplicate consumption, cycle capacity, live-head
revalidation at address time, and dispatch. The producer does not import or
reimplement Ship decision code.

## Fresh Codex task invocation

After reviewers have completed on the exact head:

```powershell
$head = gh pr view 155 -R itsHabib/workbench --json headRefOid --jq .headRefOid
reviewfindings github `
  -repo itsHabib/workbench `
  -pr 155 `
  -head $head `
  -requested codex,claude,cursor,copilot `
  -completed codex,copilot `
  -out .artifacts/review-findings-pr155.json
```

Then hand the artifact to the existing boundary:

```powershell
ship driver address <driver-run-id> `
  --stream <stream-id> `
  --findings .artifacts/review-findings-pr155.json
```

The completed set is an explicit Codex judgment over settled reviewers. Missing
reviewers are derived as `requested - completed` and remain visible. The CLI
does not infer that silence means clean.

## Contract paths

- Go type and contract law:
  `contracts/reviewfindings/reviewfindings.go`
- portable JSON Schema:
  `contracts/reviewfindings/schema/review-findings-v1.json`
- Codex/GitHub producer:
  `cmd/reviewfindings/main.go`
- Ship consumer:
  `packages/driver/src/review-findings.ts` in `itsHabib/ship`
- Ship durable consumption:
  `packages/store/src/review-artifacts.ts` in `itsHabib/ship`

JSON Schema counts Unicode characters, while Ship's contract deliberately
bounds UTF-8 bytes. The schema marks those limits with `x-maxBytes`; enforcing
readers use the Go validator or Ship parser for byte-exact validation.

## Remaining phases

This slice does not claim Gate B. It leaves:

1. install/call this producer from the canonical Codex review skill catalog;
2. run one real Codex address cycle and one independent Claude cycle;
3. add complete closure receipts and prove `panel.missing` cannot become a
   later clean Gate verdict;
4. decouple `gate judge -auto` from the Claude CLI;
5. complete the catalog integrity and later enforcement/reliability gates.
