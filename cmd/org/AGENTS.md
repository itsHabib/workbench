# org — the Baton home

The runtime for role continuity chains. The kernel — record spine, kind set,
fold, every admission law — is `contracts/org` and is imported as types and
laws, never re-decided here. This tool owns what a pure kernel cannot: where
chains live, when records are stamped and locked, and how a fresh session
re-enters a role.

Design context: `docs/features/org/vision.md` (PR #245, the org TDD). The
system name there is Baton; this binary is its first runtime slice.

## What it is

- **State**: `$ORG_STATE` (default `~/dev/org/state`) holds one JSONL chain
  per role at `<tenant>/<role-with-colons-as-->/chain.jsonl`, plus
  content-addressed erasable bodies under `blobs/`. Appends are serialized by
  an flock over the fold→admit→append critical section; admission is
  `org.Advance`, so nothing reaches a chain that the kernel would refuse.
- **Receipts**: every write verb takes `-json` and emits a machine receipt
  (kind, seq, digest, phase, tip, holder, active, dangling, held, fence);
  `status`/`verify`/`boot` speak JSON too. Identity: `-incarnation` (or
  `ORG_INCARNATION`) presents the writer's id from attach; `-strict` (or
  `ORG_STRICT`) refuses the write-as-holder default.
- **Operator context**: files dropped in
  `$ORG_STATE/<tenant>/<role>/context.d/` ride the boot output, sorted,
  under `-context-bytes` (default 4096), truncating with a pointer to the
  directory. The dumbest mechanism that works: writing a file is publishing,
  deleting it is revocation.
- **Verbs** map one-to-one onto record kinds (charter, attach, claim, yield,
  complete, abandon, assign, takeover, revoke, seal, note, checkpoint, …) plus
  read verbs: `boot` (the byte-capped re-entry index), `status` (the board),
  `intake` (where does this work belong), `sweep`, `log`, `verify`, `blob`.
- **`intake`** is the routing reflex before assign: given `-work <uri>` it
  reports which chartered lanes' scopes cover it (the `contracts/org.InScope`
  predicate — prefix-at-a-boundary, never across schemes), which lanes
  already hold it (an out-of-scope hold is named as drift), and when nothing
  covers it, says so with the fix. Read-only; safe to run on sight of new
  work.
- **`sweep`** is the continuity instrument: it REPLAYS every chain in the
  configured tenant through the kernel and counts what happened — claims
  opened vs closed, obligations
  orphaned by a displaced holder vs discharged by a successor, and session
  ends that distilled a conclusion (checkpoint) vs ones only observed
  (mark). An orphan is not a kind, so only a replay can see it. A rate with
  no data renders `—`, never 0%: "no session has ended yet" and "every
  session ended undistilled" must not share a value. A chain that stops
  folding is a row with `BROKEN`, not a failed sweep. It also detects one work
  URI held by multiple role chains in the configured tenant as
  `assign_conflicts`: an honest detected-not-prevented finding, not a global
  lock or admission claim. The file-home scan is sequential, not an atomic
  snapshot across chains; rerun after reconciliation to confirm it converged.
- **Hooks** (`hooks/`): `sessionstart-boot.sh` injects `org boot` output into
  a session whose cwd maps to a role (`$ORG_STATE/roles.map`);
  `stop-mark.sh` appends a mechanical `mark` when a session stops. Both
  fail-open: no mapping, no binary, no chain — exit 0, empty output.

## Invariants

- The home adds no judgment. A record refused by the kernel is refused here
  with the kernel's reason on stderr; the chain does not grow.
- Checkpoints are distilled by a host, never demanded of the working agent.
  The Stop hook writes a `mark`; a mark at the tip renders the boot index
  `degraded`, which is the honest state.
- The boot index is an index: pointers plus hooks, byte-budgeted
  (`-max-bytes`, default 2048), shedding depth (last-word excerpt, held list)
  but never the headline, the charter line, or a dangling obligation.
- Liveness is derived from the writer's own declared `next_due`, never from
  self-report at read time.

## Exit codes (load-bearing seam)

0 ok · 1 kernel refusal (stderr carries the reason id, e.g. `dangling_claim`)
· 2 usage · 4 error. A refusal is the substrate working, not a failure.

## Checks

```
gofmt -l ./cmd/org && go vet ./cmd/org/...
golangci-lint run ./cmd/org/...
go test ./cmd/org/...
```
