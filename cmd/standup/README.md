# standup

The compiler behind the standup. A conversation with the lead lane ends in one
JSON record; the record compiles, field by field, into Fleet verbs that already
exist. Nothing new holds state between standups except the record itself.
Design: [`docs/features/standup/design.md`](../../docs/features/standup/design.md).

```text
standup agenda            derive today's agenda from records; write and digest it
standup new --agenda <id> scaffold an empty record pinned to that agenda
standup show <id>         the readback: the record as the lead reads it aloud
standup confirm <id> --phrase "<words>"
                          set confirm iff the words are the configured phrase
standup apply <id> [--dry-run] [--force-stale]
                          refuse unconfirmed or stale; otherwise run the verbs
```

Exit codes: `0` ok · `1` refused (unconfirmed, stale agenda, phrase mismatch,
changed payload, unknown kind) · `2` usage · `4` error. A refusal is the record
not being ready; stderr says what would make it ready.

## Where it runs

In the lead's own directory, from the lead's session. `fleet mail` and `fleet
send` resolve the caller from the directory they run in, so the standup runs
where the lead session runs. Binaries and state come from the environment:

| Variable | Default | Meaning |
|---|---|---|
| `FLEET_BIN`, `ORG_BIN`, `GH_BIN` | `$FLEET_STATE/bin/fleet` when installed, else `fleet`; `org`; `gh` | the tools it reads and drives |
| `FLEET_STATE` | `~/.fleet` | lanes live at `$FLEET_STATE/lanes/<kind>/` |
| `STANDUP_DIR` | `$FLEET_STATE/standup` | `config.json`, `agenda/`, `records/` |
| `ORG_STATE` | `~/dev/org/state` | `roles.map` (seat name → directory) |

`config.json` is written once by the operator:

```json
{"lead": "lead:mh", "tenant": "mh", "phrase": "ship it", "repos": ["itsHabib/ivy"]}
```

## The agenda

`standup agenda` reads six things and refuses none of them: `fleet work --json`,
`fleet receipts --since 24h --json`, `fleet mail --for <lead> --unacked --json`,
`org status -json` (the registered role cards: role, parent, card), `gh pr list`
per configured repository, and the previous record's deferrals. A source that cannot be read is recorded as unavailable with
the tool's own words. The file carries every row; the text render is bounded.

The agenda's **projection** is the stable view of those sources (what exists and
how it ended, never timestamps or whether hands are live this second) and its
**digest** pins the record to the world it was planned against. `apply` rebuilds
the projection live and refuses with a diff when the world moved; `--force-stale`
proceeds anyway and says so in `applied[]`.

## The record (`standup.v1`)

| Field | Compiles to | Idempotency |
|---|---|---|
| `roles[]` | `fleet pool <checkout> <kind> <seats>` | pool is a top-up; a kind with no manifest under `$FLEET_STATE/lanes` refuses before any write. A card may name a seat the pool will create (`<basename>-<kind>-<i>`); its origin is checked on the checkout it is pooled beside |
| `cards[]` | `fleet dispatch <change> --as --for --due --brief --slot` in the seat's checkout, then `fleet send <seat> --id <card id> --kind order` from the lead's directory | a `#<n>` change is resolved to its head branch first, because Fleet keys rows by branch; a row already declared for that branch and relationship is skipped when it names the same accountable role and refused when it names another; two cards for one row refuse; rows in two repositories of the same name refuse as ambiguous; mail is retry-safe by id |
| `decisions[]` | `fleet decide <kind> <subject> "<text>"` | skipped when `fleet decisions` already lists it |
| `deferred[]` | nothing | re-raised verbatim in the next agenda |
| `confirm` | precondition | `null` until `standup confirm` matches the phrase; the model cannot fill it in. It binds a digest of the plan as read back, so an edit after confirm is refused until confirmed again |
| `applied[]` | written by apply | one entry per step; a re-run repeats nothing that exited 0, and refuses when a recorded step's directory or arguments no longer match the plan. `--force-stale` writes its own entry carrying the diff |

An unreadable `fleet work` at apply time is an error, not an empty world: planning
against nothing would let a dispatch replace a row nobody could see. The readout
labels every step with what happened to it: `plan`, `done` (on the ledger from an
earlier apply), `skip`, `ran`, `failed`, or `not reached`.

Launch is not a verb here. `fleet watch` delivers the card's mail to an absent
seat by starting that seat's configured provider session (`deliver.json`:
`cwd`, `provider`, optional `permission_mode`, `every` and `prompt`); delivery
is the launcher.

## Checks

```sh
gofmt -l cmd/standup && go vet ./cmd/standup/...
golangci-lint run ./cmd/standup/...
go test ./cmd/standup/...
```

The tests drive the CLI end to end against fake `fleet`, `org` and `gh` scripts
that answer from files and log every write verb with the directory it ran in.
