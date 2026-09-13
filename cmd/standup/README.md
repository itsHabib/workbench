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

## Desktop conversation POC

Use the desktop's existing voice conversation. `standup mcp` exposes the same
compiler as local stdio tools; no audio server, API key, HTTP listener or second
planner is involved. Voice itself still needs a human trial in the desktop.

Build from this checkout and register the binary with the desktop/Codex host:

```sh
go build -o "$HOME/.fleet/bin/standup-poc" ./cmd/standup
codex mcp add standup-poc -- "$HOME/.fleet/bin/standup-poc" mcp
```

Start a **new desktop task in the lead's directory**, e.g. `~/dev/lead-mh`.
The server inherits the task's environment and working directory. Fleet still
requires a real lead session at that launch directory: setting an MCP `cwd` or
walking into the directory does not impersonate the lead. Check that the task
can read the lead's mail before trying apply. `FLEET_BIN`, `ORG_BIN`, `GH_BIN`,
`FLEET_STATE`, `ORG_STATE`, and `STANDUP_DIR` keep their CLI meanings. Use matching
Fleet and standup builds. Remove the POC connection with `codex mcp remove standup-poc`.

The tools are:

| Tool | Result / effect |
|---|---|
| `standup_agenda` | Saves an agenda with per-source availability. No Fleet writes. |
| `standup_new` | Saves an empty draft; optional `from` carries an old plan to a fresh agenda. Returns its path, record, readback and `plan_digest`. |
| `standup_draft` | Replaces **all** editable fields with typed `plan`, using `expect` from the last view. Retain the entries you still want. Invalid cards/unknown fields are refused without saving; add complete cards as the conversation settles them. |
| `standup_prepare` | Checks live planning without confirmation. Returns `plan_valid`, exact steps, errors and delivery warnings. No branch push, pool, dispatch, send, decision or ledger write. |
| `standup_show` | Returns the persisted record, file path, complete readback and digest. Open this file in the desktop to see the shared plan. |
| `standup_confirm` | Relays the actual user's phrase with the displayed digest and `surface: voice` or `text`. Saves confirmation only. |
| `standup_apply` | Requires the confirmed digest. Runs existing apply, with no MCP stale override. May create remote branches and launch work indirectly through mail. |
| `standup_status` | Reads the apply ledger, scoped runtime observations and each required receipt through `fleet done`. Worker activity and task completion stay separate. |

`prepare`, `status`, `show --json`, and `draft <id> --expect <digest> --file
<path|->` also work at the terminal. Draft JSON contains only `roles`, `cards`,
`decisions`, `deferred`, and optional `next`. Confirmation and apply accept an
optional `--expect` in the CLI; the MCP requires it. MCP record references are
ids inside the configured store, never arbitrary paths. The CLI retains its
existing path support.

Changed drafts clear confirmation. An edit based on an old digest refuses. A
record with execution history cannot be rewritten through draft; use a new
agenda and `new --from`. CLI/MCP writes are serialized with one advisory store
lock; a busy store refuses instead of overwriting another call. Manual file
editors do not participate in that lock.

This POC **trusts the desktop agent to relay the user's actual words after
readback**. A phrase comparison and `surface: voice` do not authenticate audio
or prove a readback was heard. Never present them as such. No confirmation
phrase is provided by agenda/new/prepare as an instruction to execute.

`plan_valid` means compiler checks passed at collection time. Delivery warnings
are separate because recording an assignment can be intentional even when its
mail will wait. Runtime observations preserve their collection time, Fleet exit
code and errors. Receipt status queries the change as Fleet resolves it now;
its returned SHA is the evidence scope, not necessarily the head of a checked-out
worker. A successful apply is not evidence that a worker started or completed.
`next` remains descriptive; this POC adds no scheduling or per-role store.

### First voice trial

Say: “Read the agenda and help me draft one small sandbox task. Keep the saved
plan visible, prepare it, and tell me what would happen before we apply it.”
Correct one card aloud and check the file changed. Ask for readback. A vague
“sounds good” must not apply anything. After the actual configured phrase,
inspect apply's results and `standup_status`. If delivery is absent, expect
waiting mail and a warning, not a launch claim. Capture the record id and the
receipt SHA if work completes. No live voice or production dispatch is covered
by the automated fixture tests.

Preparation repeats unavailable sources from the pinned agenda and distinguishes
steps already recorded (`done`), effects already present (`skip`), and remaining
steps (`plan`). A refused prepare still returns useful JSON alongside its nonzero
exit. Missing role maps preserve structured status: seat-backed cards become
unknown while checkout-only cards and collected runtime evidence remain visible.
Delivery status currently has no tenant field in Fleet; the adapter scopes it by
address and directory from the plan tenant's role map and rejects any explicit
mismatched tenant. Runtime status requires an explicit matching tenant.
