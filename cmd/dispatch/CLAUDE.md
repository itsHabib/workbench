# dispatch

The workbench's placement-decision plane: a small Go binary that turns a
versioned, content-hashed policy file + a task descriptor into a deterministic
placement (engine, provider, model, effort, runtime, escalation) plus an
append-only decision receipt. It **decides** placement; ship's `dispatch` verb
**executes** it. dispatch never dispatches, polls, lands, or calls a provider.

`docs/DESIGN.md` is the per-tool charter; `docs/features/dispatch/spec.md` (repo
root) is the binding TDD. Change behavior there first. The **frozen taxonomy**
and the **deterministic descriptor-derivation rules** in `docs/DESIGN.md` are a
contract the phase-2 replay gate keys on — amend them only by a versioned note,
never silently.

## Layout

- `cmd/dispatch` — verbs `decide`, `validate`. Owns the exit-code contract and
  the "no placement on any non-zero exit" invariant. Errors are single-line
  JSON `{code, message}` on stderr.
- `internal/policy` — the data model + fail-closed loader: schema validation,
  sha256 over the exact file bytes, the frozen `task_class` enum. The leaf.
- `internal/placement` — descriptor schema, the self-versioned placement shape,
  the first-match scan (`match.go`), provenance. Pure: no clock/I/O/randomness.
- `internal/receipt` — the append-only JSONL writer. Pure mechanism.

## Invariants (pinned by tests — keep them pinned)

- **Determinism (FR4):** identical descriptor + identical policy → byte-identical
  placement stdout. No clock, network, or randomness in the decide path; output
  is marshaled from fixed-field structs, never by ranging a map. The receipt's
  `decided_at` is the only time read, and it lands in the receipt file, never in
  the stdout placement.
- **Fail-closed exit codes, all reachable + asserted.** decide: 0 placed · 2
  bad/missing/empty policy or unknown `task_class` in a match block · 3 no rule
  matched (actual unmatched values on stderr) · 4 bad descriptor · 5 requested
  receipt append failed. validate: 0 valid · 1 valid-with-warnings · 2 invalid.
- **No placement on a non-zero exit.** In particular exit 5 leaves stdout empty:
  the receipt is appended *before* the placement reaches stdout, so a caller can
  never consume a placement from a failed invocation.
- **No default placement.** A descriptor matching no rule is exit 3, never a
  fallback — the operator must write an explicit `match: {}` catch-all to get one.
- **The policy hash pins every decision** (computed over exact file bytes before
  parse); every placement and receipt carries it.
- **Receipt completeness:** after N successful `decide --receipts` invocations the
  receipts file has exactly N lines (the phase-2 gate runs on this data).
- **Frozen `task_class` enum** `mechanical | analytical | generative` — an unknown
  value is exit 2 (policy match block) or exit 4 (descriptor), never a silent
  never-match. `risk_tier` is `/pr-risk`'s vocabulary, opaque strings here.

## Boundary law

`internal/*` is private to dispatch; it imports no other tool's decision logic.
The `placement` type does **not** enter `contracts/` yet — that is the phase-4
trigger (a second consumer, `/work-driver-prep`).

## Checks

```
gofmt -l . && go vet ./... && golangci-lint run ./... && go test ./...
```

No in-repo dependency (not even `contracts`) in phase 1.
