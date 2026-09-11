# org — role cards

Org is a small directory of editable Markdown role cards. It owns role definitions
and optional parent references. It does not own sessions, work assignments,
messages, handoffs, checkpoints, liveness, or authority grants.

## Normal surface

- `org charter -role <name> -file <card.md> [-parent <name>]` registers or updates
  a card. Reusing the name is an ordinary configuration edit, not a refusal.
- `org boot -role <name>` reads the file as it exists now. `-max-bytes` optionally
  bounds prose and reports truncation with the source path; default is full text.
- `org status` lists the configured tenant's registered cards.
- The three commands take `-state`, `-tenant`, and `-json`; no incarnation flags.

`$ORG_STATE/roles.json` is a JSON array of `{tenant, role, card, parent?}`. Card
paths are absolute. Prose stays in the original Markdown file: one source, no
copied or synchronized definition. Registry updates serialize and publish by
rename; malformed or duplicate entries refuse writes without replacing the file.
`roles.map` is the independent cwd/seat binding already used by Fleet and the
startup hook. Registering prose does not rebind a checkout or create a mailbox.
Parents are descriptive references. No recursive admission, inheritance,
mandatory hierarchy, or scope enforcement is inferred from prose.

## Compatibility

`org legacy <verb>` retains the Baton CLI for existing chains, held work and
obligations. Its kernel and journal format are unchanged. Default commands never
fold or append those chains. See [LEGACY.md](LEGACY.md) for that protocol.
Do not put legacy attach/claim/checkpoint requirements into new role cards.
Existing history is not silently migrated, completed, deleted or granted new
permissions by registering a card. Retire remaining callers deliberately.

`hooks/sessionstart-boot.sh` optionally injects the selected role's card.
`hooks/stop-mark.sh` is a no-op compatibility shim; remove it from installed hook
configuration when upgrading. Role definitions do not need activity records.
The MCP surface exposes just charter, boot and status over the same CLI.

## Checks

```
gofmt -l ./cmd/org && go vet ./cmd/org/...
golangci-lint run ./cmd/org/...
go test ./cmd/org/...
```
