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

## Clean cutover

Only role-card commands are supported. Remove old hook and caller instructions;
do not add fallbacks or a parallel lifecycle. See README.md for the consumer inventory.

## Checks

```
gofmt -l ./cmd/org && go vet ./cmd/org/...
golangci-lint run ./cmd/org/...
go test ./cmd/org/...
```
