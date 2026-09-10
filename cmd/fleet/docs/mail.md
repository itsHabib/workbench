# Fleet mail

Mail is durable communication between identified roles and seats in the same
tenant. It grants no assignment, resource, or merge authority.

```sh
fleet send hub:b --id unit-question-1 --kind question --subject 'Which unit?' \
  --head abc123 --body 'Use milliseconds or seconds?'
fleet send worker-a --id unit-answer-1 --kind answer --subject 'Milliseconds' \
  --body 'Use milliseconds.'
fleet mail --unacked
fleet mail --for worker-a --json
fleet ack unit-answer-1
```

## Addresses

The session's launch directory resolves through `$ORG_STATE/roles.map`. A row
with a fourth column uses that seat name as its address. Otherwise it uses its
role name. Two worker seats of the same role kind have separate inboxes. Sending
to a pooled role kind refuses and lists the available seats; Fleet never guesses
which worker should receive it. Duplicate seat bindings and role/seat name
collisions must be resolved in the map.

Any identified session may send to or explicitly read another address in its
tenant, including seats without a dispatch row. Default reads and hook context
use its own address. Ack belongs only to the session's launch address; changing
cwd cannot acknowledge a sibling's mail. `--session <id8>` disambiguates live
sessions at the caller's cwd. There is no contact allowlist, charter traversal,
relationship inference, or task-acceptance requirement.

Kinds are `question`, `answer`, `escalation`, `report`, `order`. Subjects are limited
to 1024 bytes. `--body -` reads stdin, preserving full text. `mail` displays full
bodies and optional heads; `--json` returns records. MCP `fleet_send`, `fleet_mail`,
and `fleet_ack` require caller `cwd`; MCP bodies are literal text.

## Storage and retries

New records live under `$FLEET_STATE/mail/.v2/<tenant>/<kind>/<address>/<id>.json`
(default state root `~/.fleet`). Tenant and address components use lowercase SHA-256 hex digests, avoiding
case-folding aliases on macOS and Windows; kind distinguishes a role from a seat. IDs start with a letter or digit, followed
by letters, digits, `.`, `_`, `-`, up to 128 characters. The resolved tenant is
carried from authorization into storage, so a changed mapping cannot publish a
message into a different tenant.

Records retain `from_role` and the original `from_session` as provenance, with
`tenant`, `from_address`, and `from_kind` identifying the sender. Other fields are
`id`, `to`, `to_kind`, `kind`, `subject`, `head`, `body`, `at`, and optional `acked_at` /
`acked_by`. Times are epoch seconds.

The retry key is the typed recipient address within its tenant plus a
caller-chosen ID. The same sender address and kind/subject/head/body return the
original record even from a replacement session. Retry does not renew `at`,
overwrite the body, or reset acknowledgement. Changed payload or a different
sender under that ID refuses. IDs are recipient-scoped, so two senders choosing
the same ID for one inbox conflict. Send and ack serialize through the existing
mail lock and publish temp-then-rename. Repeated ack preserves the first reader.

## Retained role mail

Historical `mail/<role-safe>/<id>.json` records stay in place. Their immutable
`.address.json` must identify the original role and tenant. Unambiguous dedicated
role mail remains readable, acknowledgeable, and retryable by a replacement role
session without rewriting provenance. Typed paths keep a new tenant or a former
filename alias from adopting that history.

Historical shared-role mail remains explicitly readable with `fleet mail --for
<role>`, but it is never injected into a seat inbox or acknowledged as one. A
historical send without individual seat provenance cannot become a replacement
seat retry by inference. This preserves the record without guessing ownership.

Acknowledged messages remain retained. Listing currently scans retained records;
large mailboxes need measurement before indexing or retention machinery is added.

## Startup context and validation

SessionStart and UserPromptSubmit inject up to five unacked mail lines, then
`[fleet] and N more; fleet mail`. Subjects are flattened to one line and bounded,
including retained messages. The current launch-directory address is resolved at
each event. Hooks never auto-ack. An absent recipient keeps queued mail until a
session starts. There is no automatic launch or delivery stamp.

Both adapter suites use real CLI processes and hooks in temporary state. They
exercise replacement sender retries, queued recipients, distinct worker seats,
full-body reads, acknowledgement ownership, and cross-tenant refusal. The
continuity exchange also checks that the replacement worker sees its current
assignment, a replacement lead sees its authored handoff, and reused seats do
not receive a previous branch's brief. These are adapter-boundary checks, not
proof of a live model conversation or unattended launcher safety.
