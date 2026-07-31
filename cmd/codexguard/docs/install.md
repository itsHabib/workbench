# Projecting codexguard into a Codex profile

Workbench projects two reviewed profile assets:

- `hooks.json` is rendered with the resolved absolute path of the
  `codexguard` executable running projection.
- `assets/rules/workbench-codexguard.rules` is the restrictive shell backstop
  when a hook is missing, skipped, or fails.

The projection command never edits an existing divergent file. In particular,
it will not merge or replace a user-owned `hooks.json` or rules file.

## Inspect without changing anything

Build or install `codexguard` at its final trusted location, invoke that exact
binary, then point status at the intended Codex home:

```powershell
codexguard projection status -home "$env:USERPROFILE\.codex"
```

The JSON plan reports each target as `missing`, `current`, or `divergent` and
prints a SHA-256 `digest`. Status does not create the home or any directories.
The hook bytes and plan digest include the executable's resolved absolute path;
moving the binary requires a fresh status/apply and hook trust review.
Review the paths and hashes before applying.

## Apply the exact observed plan

Pass the exact digest returned by status:

```powershell
codexguard projection apply `
  -home "$env:USERPROFILE\.codex" `
  -expect "<status digest>"
```

Apply re-reads every target. A stale digest, divergent file, symlinked target
parent, or create-time collision refuses without overwriting the target.
Reviewed bytes are staged and synced before an exclusive publish. Repeating
status/apply on current files is a no-op.

Rollback removes a published target only while it is still the same hard link
as the private staged file. A concurrent replacement is user-owned and remains
untouched.

Projection holds and revalidates each target-directory identity across publish.
Unix publishes and rolls back relative to that stable directory handle. Windows
has no Go-level `linkat` equivalent, so it validates the held handle immediately
before and after the exclusive hard-link operation and refuses any identity
change. This protects against accidental path replacement; it is not a sandbox
against another process running as the same OS user.

For a repository test or demo, use a temporary directory instead of the real
profile:

```powershell
$demoHome = Join-Path $env:TEMP "codexguard-demo"
codexguard projection status -home $demoHome
```

## Activation is a separate operator action

Projection does not register, enable, or trust hooks, restart Codex, modify
managed policy, or install the `codexguard` executable. The operator must:

1. place the reviewed executable at its final operator-controlled path and run
   projection from that binary;
2. resolve any reported file collision deliberately;
3. restart Codex so it discovers the active config layer;
4. use `/hooks` to review and trust the exact non-managed hook hash.

Codex loads `hooks.json` and `rules/*.rules` beside active config layers.
Non-managed command hooks are skipped until their exact definition is trusted.
Rules are experimental and govern commands outside the sandbox. These native
semantics are documented in the official
[hooks](https://developers.openai.com/codex/hooks) and
[rules](https://developers.openai.com/codex/agent-configuration/rules)
manuals.

The rules are not the policy owner and cannot dynamically validate a Gate
artifact. They forbid only exact operator-only/destructive prefixes and prompt
on direct merge. They deliberately do not prompt whole shell or Git
executables: a rules prompt cannot be canceled by a healthy hook and would make
normal delivery approval-heavy. Opaque wrappers, Git global-option forms, and a
force flag after arbitrary push arguments remain hook-owned and are not
fail-closed when the hook is missing or skipped. Only the hook delegates a
supported tool call to codexguard's deterministic policy engine; Gate remains
the merge authority.
