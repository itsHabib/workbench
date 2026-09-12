# Inspect configured hook commands

`fleet inspect-hooks --config <harness-json>` reads one explicitly selected Claude
settings or Codex hooks JSON file. It emits JSON only, without executing any command,
resolving variables, calling Git, or migrating Fleet state. Missing/invalid hook
structure and files larger than 4 MiB refuse. An empty hooks object reports an empty
inventory, not a successful installation.

Rows identify event/group/hook indices, a command hash, environment variable names,
and a conservative command form: legacy Python, native Fleet, unknown or unsupported
hook type. Only simple literal invocations are recognized. Both slash styles and
simple quoted paths are supported. Wrappers, interpreter flags and shell control
syntax remain unknown. Command text and environment values are omitted. A recognized
basename is not verified executable identity or evidence that the command can run.

The source path and source-byte hash identify what was inspected. No default path
is assumed. Inspect all applicable configuration layers separately. Duplicate rows
remain visible by index/hash; this command does not decide precedence, enablement,
expected event coverage, project/hook trust or whether duplicate handlers execute.
It does not merge configuration layers or establish effective Fleet/Org roots.

Every result says `runtime: unverified` and `effective_state_roots: unknown`.
Runtime acceptance still requires observing a harmless real harness event against
known state roots. This command is the read-only first step toward fixing the
quoted/env-prefixed and Windows-backslash installer failures; it is not migration
plan/apply and does not make `install.sh --apply` safe on those configurations.

Exit 0 means inspection produced an inventory, including unknown rows. Exit 1 means
usage/configuration refusal; file I/O failures follow the CLI's ordinary error path.
Never use exit 0 alone as hook installation readiness.
