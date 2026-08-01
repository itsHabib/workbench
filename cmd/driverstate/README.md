# driverstate

The human/cron CLI over the driver-state event ledger: one append-only,
hash-chained `events.jsonl` per driver run, recording how a run and its streams
moved through their lifecycle (imported, dispatched, attempted, reviewed,
PR-opened, merged, finished). It appends one event at a time from stdin, folds
a run's events into a derived state view, renders a timeline, joins a parent
run to its child sub-runs, and verifies the chain. It is the 1:1 terminal twin
of the `workbench-mcp` driver verbs — same state root, same validation, same
ledger — differing only in lifecycle: no session, so `record` claims the run
lease, appends, and releases in one shot.

## Placement

Binary: `cmd/driverstate` (`main.go` + `record.go` / `read.go` / `render.go`,
all package `main` — this tenant has no `internal/` tree). It imports only the
top-level `driverstate` mechanism package (leases, `Append`, `Reduce`, `Runs`,
`Rollup`, `Verify`) and the leaf contract `contracts/driverstate` — no other
tool. The seam is the process: JSON on stdin/stdout, diagnostics on stderr.

Do not confuse the two `driverstate` packages. `contracts/driverstate` is the
leaf contract — `Event`, `Kind`, the body and reducer types, `DecodeEvent` /
`ReadLedger`, `ValidateEvent`, the canonical encoding, the embedded JSON
Schemas — importing nothing else in the module and carrying no decision logic.
`cmd/driverstate` is this binary, the face of that contract.

## Run it

```
driverstate record [--run <id>] [--json]  < event.json  # append one event, print it sealed
driverstate state  --run <id> [--json]                  # reduced RunState for one run
driverstate render --run <id>                           # timeline + per-stream blocks
driverstate runs   [--repo <r>] [--live] [--parent <id>] [--json]
driverstate rollup --run <parent> [--json]              # parent/child stream roster
driverstate verify --run <id> [--json]                  # re-walk the hash chain
```

State root resolves via `WORKBENCH_STATE_DIR` (must be absolute) else
`~/.workbench/driver-state`, exactly as the MCP server resolves it. The
resolved root prints to **stderr**, so `--json` stdout stays clean.

## Exit codes

| Code | Meaning |
| ---: | --- |
| 0 | the verb succeeded |
| 1 | any error — bad usage, missing `--run`, validation failure, broken chain |

That is the whole contract: `main` prints `driverstate: <err>` to stderr and
exits 1 on any returned error. There is no per-failure-class code today.

## How it works

Each run owns one `events.jsonl` under the state root. `record` fills the
client-minted defaults (run id — `--run`, else minted for `run_imported`; event
id; time), then the mechanism package seals the line: `prev` = the previous
event's hash, `hash` = SHA-256 over the contract's canonical encoding — field
declaration order, no insignificant whitespace, HTML escaping off, body spliced
in **verbatim** and never re-marshalled, so an independent (e.g. TypeScript)
emitter can reproduce the same bytes. The other verbs are pure reads; status is
always a reducer output, never a stored field.

## Constraints that are design decisions, not omissions

- **Tolerant reader, asymmetric.** An unknown *kind* decodes without error and
  `ReadLedger` skips it into a warning rather than failing a listing. An
  unknown *version* fails loudly. Kind tolerance buys additive growth within a
  version; it is never extended across one.
- **Mixed-version chain.** `KnownVersion` accepts both `driver-state-v0.1.0`
  and the additive `driver-state-v0.2.0` in one unchanged chain. Validation is
  strict about which is which: `review_address_*` kinds require v0.2.0, and
  v0.2.0 is reserved for them.
- **Append-only.** No verb edits or deletes an event. A correction is another
  event.
- **The contract carries no decision logic.** `contracts/driverstate` has no
  `Append`, no `Reduce`, no lease, no MCP surface — only vocabulary, validation
  law, and the canonical encoding.
- **No session.** `record` claims and releases the lease per invocation, so a
  cron run never leaves a run locked. A `run_imported` minted without `--run`
  is refused unless it carries `(repo, source, generated_at)`, so a retried
  import cannot mint a duplicate run.
- **Address mutation is not here.** `state --json` and `render` surface the
  folded `review_addresses`; changing them belongs to `reviewfindings address`.

## Status

The CLI, the mechanism package, and the contract are built and covered by tests
(`cmd/driverstate/cli_test.go`, `contracts/driverstate/*_test.go`). The feature
documents behind them still carry `Status: draft` — `docs/features/driver-state/spec.md`
is marked "draft / proposal — NOT a build commitment" — so treat the written
design as intent, not a frozen contract. See `cmd/driverstate/CLAUDE.md` for
scoped guidance and that spec (§5 event schema + canonical encoding, §6 state
machine / reducer output) for the behavioral source of truth.
