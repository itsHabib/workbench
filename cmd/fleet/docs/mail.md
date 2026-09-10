# Fleet mail

Mail is durable communication between identified roles in the same tenant.
It grants no assignment, resource, or merge authority. This smaller contract
supersedes the earlier contact-derivation and watcher-launch scope.

```sh
fleet send hub:b --id unit-question-1 --kind question --subject 'Which unit?' \
  --head abc123 --body 'Use milliseconds or seconds?'
fleet mail --unacked
fleet mail --for hub:b --json
fleet ack unit-question-1
```

The session's launch directory resolves through `$ORG_STATE/roles.map`. Any
identified role may send to or read another identified role in its tenant,
including seats without a dispatch row. There is no contact allowlist, charter
traversal, or relationship inference. Unidentified or cross-tenant callers refuse.
`--session <id8>` disambiguates live sessions at the caller's cwd. Ack requires
that session to hold the addressed role or be in its bound directory, within the
same tenant. Mail verbs do not acquire work or resource ownership.

Kinds are `question`, `answer`, `escalation`, `report`, `order`. Subjects are
limited to 1024 UTF-8 bytes (oversized CLI/MCP input is usage); bodies remain full.
Hook summaries also truncate oversized subjects retained from older versions. `--body -` reads
stdin, preserving the full text. `mail` displays full bodies and optional heads;
`--json` returns records. MCP `fleet_send`, `fleet_mail`, and `fleet_ack` require
caller `cwd`; MCP bodies are literal text and mailbox output is JSON.

Records live at `$FLEET_STATE/mail/<role-safe>/<id>.json` (default state root
`~/.fleet`). Fleet's existing `Safe` encoding maps `:` to `__`. IDs start with a
letter or digit, followed by letters, digits, `.`, `_`, `-`, up to 128 characters.
Fields are `id`, `to`, `from_role`, `from_session`, `kind`, `subject`, `head`,
`body`, `at`, and optional `acked_at` / `acked_by`. Times are epoch seconds.

The retry key is recipient role + caller-chosen ID. The same sender role and
same kind/subject/head/body return the original record even from a replacement
sender session. `from_session` remains the original sender session as provenance;
retry does not renew `at`, overwrite the body, or reset acknowledgement. Changed
payload or a different sender role under that ID refuses. Send and ack serialize
through the existing `keylocks/mail.lock` primitive and publish temp-then-rename.
Acknowledged messages remain retained; repeated ack preserves the first reader.

An immutable `.address.json` beside the messages pins the original role and
tenant. Storage operations carry the tenant captured before authorization and
refuse a changed binding; first publication never chooses a new tenant after
authorization. Reads and acknowledgements use the same captured-tenant fence.
Ambiguous role names across tenants, filename collisions, changed tenant
bindings, or missing/unreadable metadata on retained mail refuse. Fleet never
silently adopts an old tenant's mailbox into a new tenant. The operator must
reconcile a changed binding; mail does not rewrite its own address metadata.

SessionStart and UserPromptSubmit inject up to five unacked mail lines:
`[fleet] mail <id> from <from_role> (<kind>): <subject>`, then
`[fleet] and N more; fleet mail`. Subjects are flattened to one line, and the
current launch-directory role is resolved at each event. Hooks never auto-ack.
An absent recipient simply has queued mail until a session starts and sees it.
There is no process launch, delivery configuration, delivery stamp, or automatic
resume. No installed hooks or live state are changed by this implementation.

Validation uses real CLI processes and hook adapters in isolated temporary state:
a sender ends and its replacement retries the same ID, an absent recipient's
replacement sees and reads the queued full body, acknowledgements remain intact,
and cross-tenant sends/reads refuse. These events test the adapter boundary; they
do not claim a live Claude/Codex session has been launched.

Hook listing currently scans retained mail, so latency grows with mailbox history.
An unacknowledged index or archive is deferred pending measured workload evidence;
this slice makes no constant-time mailbox claim.
