# org

Editable role cards and a small registry. Give a role a name, write its prose,
and optionally name a parent. There is no role lifecycle to perform before work.

```sh
# Write lead.md in your editor: purpose, responsibilities, repos and useful context.
org charter -role lead:project -file ./lead.md -parent human:mh
org boot -role lead:project
org status

# A second repo or child is ordinary configuration.
# Edit lead.md; the next boot reads the change under the same role name.
org charter -role researcher:project -file ./researcher.md -parent lead:project
```

`charter` registers or updates the file reference. Omit `-parent` on an update to
retain it; use `-parent ""` to clear it. The Markdown file stays where you wrote it.
The registry is `$ORG_STATE/roles.json` (default `~/dev/org/state/roles.json`), a
JSON array of `tenant`, `role`, `card` and optional `parent`. Commands accept
`-state`, `-tenant` and `-json`. `boot -max-bytes N` optionally limits prose and
points to the full card when truncated.

Parent references help discovery. Scope, expectations and repo lists belong in
the prose; they do not become permission gates. Registering a role does not bind
a directory, launch a worker, or create a messaging protocol. `roles.map` retains
the existing directory/seat bindings. Fleet can work without this registry; Org
can describe roles without Fleet. Read a card directly or use the optional startup
hook to supply it to a session.

Mail belongs to mail. Useful handoffs belong with the work. Neither requires an
Org claim, incarnation or checkpoint. Actual resource leases and merge authority
remain with their existing owners.

## Upgrading existing installations

Normal CLI and MCP usage changes in this release. Install matching `org` and
`org-mcp` builds and replace old operating instructions with the card workflow.
The MCP server now exposes only `org_charter`, `org_boot` and `org_status`.

Existing chains stay on disk, readable and recoverable through `org legacy`:

```sh
org legacy status
org legacy boot -role lead:existing
org legacy verify -role lead:existing
```

Inspect existing held work and unresolved questions during cutover; registering
a card does not claim that this work was completed or moved. Preserve useful
conclusions in the existing work/handoff path and keep history available. Existing
Baton callers can explicitly use `org legacy <verb>` while migrating. Normal
`org boot` does not silently fall back to injecting the old lifecycle protocol.
See [LEGACY.md](LEGACY.md) for the preserved commands and [the boundary decision](../../docs/features/org-fleet-boundary/spec.md).

The SessionStart hook now reads the card. Remove the old Stop mark hook during
installation; its checked-in script is a no-op so older hook references do not
keep appending a journal. No installed binaries, mappings, hooks or live state
are changed merely by building this code.
