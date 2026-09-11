# standup — the record and the compiler behind the standup

A conversation with the lead lane ends in one JSON record (`standup.v1`); the
record compiles, field by field, into Fleet verbs that already exist. This tool
owns the agenda, the record, and apply. It owns no launcher, no chain, and no
conversation. Design: `docs/features/standup/design.md`.

## Invariants

- **Confirm is code, never the model.** `confirm` stays `null` until
  `standup confirm` matches the configured phrase (trimmed, case-folded,
  exact). `apply` refuses a record with `confirm: null` before reading anything
  else, refuses a confirm whose stored phrase is not the configured one, and
  refuses a plan whose digest no longer matches the one confirm bound. A model
  relaying "sounds good" cannot commit the operator, and neither can an edit
  after the readback.
- **A record is pinned to one agenda.** `agenda_digest` is the hash of the
  agenda's projection (identity and outcome, never timestamps or liveness).
  `apply` rebuilds the projection live and refuses with a diff when the world
  moved; `--force-stale` is the operator's override and is recorded.
- **Refuse before writing.** Unknown kind, unknown seat, a seat that is a
  clone of another repository, a row already accountable to another role, and
  a ledger step whose arguments no longer match the plan are all found in
  planning, before any verb runs.
- **`applied[]` is the only ledger.** One entry per step, saved after each, so a
  crash leaves a truthful record and a re-run repeats nothing that exited 0. A
  forced apply writes its own entry, carrying the diff, before any verb runs.
- **Every verb is another tool read as an artifact.** `fleet`, `org` and `gh`
  are named by `FLEET_BIN`, `ORG_BIN`, `GH_BIN`; their JSON and exit codes are
  the contract. Nothing here imports their decision logic.

## Exit codes

`0` ok · `1` refused (unconfirmed, stale, phrase mismatch, changed payload,
unknown kind or seat) · `2` usage · `4` error. A refusal is not an error.

## Layout

- `main.go` — the verbs and their flags; positional-first so `confirm <id>
  --phrase …` reads naturally.
- `internal/standup/env.go` — Env, Config, the exec Runner, roles.map and lanes.
- `internal/standup/record.go` — the record types, validation (every failure at
  once), readback.
- `internal/standup/agenda.go` — sources, projection, digest, text render.
- `internal/standup/apply.go` — confirm, plan, world, apply.

## Checks

```
gofmt -l cmd/standup && go vet ./cmd/standup/...
golangci-lint run ./cmd/standup/...
go test ./cmd/standup/...
```

Tests are end to end against fake `fleet`/`org`/`gh` shell scripts (skipped on
Windows). Add a behavior by adding a case to the fake and asserting the call
log, not by mocking a Go interface.
