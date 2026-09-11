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

## Clean cutover

This release removes the Baton CLI and its Stop shim. There is no fallback or legacy
command. Install matching `org` and `org-mcp`; the only operations are charter, boot,
and status. Existing journal files are inert historical data, not inputs to this runtime.

Before activating the new binaries, stop the old watchers and remove the old Org hook
entries, including installed copies of `sessionstart-boot.sh`, `pretool-lane-guard.sh`,
`lane-resolve.sh` and `stop-mark.sh` under `~/dev/org/hooks/`. Remove attach, intent,
claim, chain and incarnation instructions from global harness guidance, installed
supervisor/worker cards, task-owner/verifier/supervisor skills and deliver.json prompts.
Replace the Org MCP entry with the matching three-tool build and start fresh sessions.
Register the role prose you intend to use and bind directories in roles.map as needed.

Use the optional checked-in SessionStart card reader for new sessions; no Org write guard
or Stop hook belongs in the new setup. Fleet owns runtime occupancy and authored handoffs.
Old files need not be deleted or migrated to start this workflow. Installing this build
does not assert that historical work completed. See [the boundary decision](../../docs/features/org-fleet-boundary/spec.md).
