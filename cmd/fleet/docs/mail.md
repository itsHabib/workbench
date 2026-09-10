# Fleet mail

A role is the address; sessions remain disposable. All paths below are under
`FLEET_STATE` (default `~/.fleet`). No installed hooks are edited by these verbs.

```sh
fleet send hub:parent --id unit-question-1 --kind question --subject 'Which unit?' \
  --head abc123 --body 'Use milliseconds or seconds?'
fleet mail --unacked                 # caller's role, including full bodies
fleet mail --for hub:parent --json   # explicit read-only mailbox
fleet ack unit-question-1
```

Kinds are `question`, `answer`, `escalation`, `report`, `order`. `--body -` reads
stdin; MCP `body` is always literal text. `--session <id8>` disambiguates live
sessions in the calling directory on all three verbs. Send derives its role from
the session's launch directory (recorded cwd for older sessions). Ack requires a
session holding the addressed role or in that role's bound directory. Lists do not
acknowledge. MCP tools `fleet_send`, `fleet_mail`, `fleet_ack` require caller `cwd`.

Configure `contacts.json` as an explicit adjacency list, including each role's
parent, children and siblings, not ancestors or cousins:

```json
{"hub:parent": ["hub:a", "hub:b"], "hub:a": ["hub:parent", "hub:b"], "hub:b": ["hub:parent", "hub:a"]}
```

This chooses the operator-written fallback: Org has append-only charter/recharter
chains, but no cheap published `contacts` relation. Fleet does not duplicate Org's
chain validation and folding. Contacts are directed and not transitively expanded;
`roles.map` must bind every address to the caller's tenant. An unlisted recipient
is refused with the allowed set. Since the requested mailbox path has no tenant
component, names reused across tenants (or colliding after filename sanitizing)
refuse. Seat senders ignore the static list: their only contact is `for` on the
current dispatch matching their seat, repo and checked-out branch. Missing or
conflicting accountability refuses. Reassignment changes that contact immediately.

Mail lives at `mail/<role-safe>/<id>.json`; role-safe uses Fleet's existing `Safe`
encoding (`:` becomes `__`). IDs start with a letter or digit, then letters,
digits, `.`, `_`, `-`, at most 128 characters. The fields are `id`, `to`,
`from_role`, `from_session`, `kind`, `subject`, `head`, `body`, `at`, with optional
`acked_at`, `acked_by`, `delivered_at`, `delivered_by`. Times are epoch seconds.
Same recipient + ID + identical payload returns the original record, including
its timestamps/stamps; any changed payload field (including sender session)
refuses. Records remain after ack. Send, ack and delivery serialize on
`watch/mail.lock` and publish JSON by temp-then-rename.

SessionStart and UserPromptSubmit inject up to five unacked messages:
`[fleet] mail <id> from <from_role> (<kind>): <subject>`, then
`[fleet] and N more; fleet mail`. Subjects are flattened to one line. No auto-ack.

Configure `deliver.json` to enable watcher delivery for otherwise absent roles:

```json
{"hub:parent": {"cwd": "/path/to/parent", "cmd": ["claude", "-p", "--model", "opus", "{{prompt}}"]}}
```

The cwd must be bound to that role. Each fold launches one command per configured
role with eligible mail and no live session. `FLEET_MAIL_GRACE=10s` is the default;
set another duration (or `0s` for tests). Delivery occurs on a watcher tick, whose
default interval is 60s. Arguments substitute `{{prompt}}` directly, without a
shell; the prompt carries the mail summary and instructions to read/ack. Child
output goes to `watch/watch-<pid>-<time>.log`. Unreadable liveness evidence suppresses
launches. A live session receives mail at its next hook event instead.

`delivered_at`/`delivered_by` reserve an **at-most-once launch attempt** before
starting the process. `watch/observed.jsonl` records attempt and started/failed
outcomes. A crash between reservation and start can leave mail unlaunched; a failed
command is also not retried under that ID. This is the deliberate cost of never
launching twice: a stamp is not proof of reading or successful execution. Ack is
independent, and the original unacked mail remains visible. No automatic session
resume, delivery retry, cross-machine transport, or task lifecycle change is added.

