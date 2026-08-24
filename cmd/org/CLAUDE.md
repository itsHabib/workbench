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
- **Verbs** map one-to-one onto record kinds (charter, attach, claim, yield,
  complete, abandon, assign, takeover, revoke, seal, note, checkpoint, …) plus
  read verbs: `boot` (the byte-capped re-entry index), `status` (the board),
  `log`, `verify`, `blob`.
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
