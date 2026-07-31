# Native Codex hooks

`codexguard hook` is the thin lifecycle adapter for Codex's native
`PreToolUse`, `PermissionRequest`, and `PostToolUse` command hooks. It reads one
current Codex envelope from stdin and delegates every policy decision to
`internal/policy`.

The separate [`projection command`](install.md) renders the installable hook
definition with the resolved absolute path of the executable running
projection. It can place those exact hash-bound bytes after an explicit apply.
Resolving a bare `codexguard` name from a repository working directory is never
trusted. Repository changes do not edit a Codex profile, activate the hook, or
trust its hash.

## Ordering and responses

`PreToolUse` and `PermissionRequest` first append and sync an
`AutoModeAuditEventV1` decision. Only then may they write a native hook response.
If persistence fails, the adapter emits no allow or deny response and exits as a
hook failure.

| Event | Policy outcome | Native response |
|---|---|---|
| `PreToolUse` | `pass` | no response; normal Codex permissions still apply |
| `PreToolUse` | `park`, `block`, `refuse` | `permissionDecision: deny` with rule and remedy |
| `PermissionRequest` | `pass`, `park` | no decision; normal approval flow remains |
| `PermissionRequest` | `block`, `refuse` | `behavior: deny` with rule and remedy |
| `PostToolUse` | prior persisted `pass` plus an unambiguous result signal | append and sync completion evidence |
| `PostToolUse` | no prior pass or ambiguous result | no authority and no completion event |

Permission handling cannot widen policy. The native request does not identify
the capability that caused the approval prompt, so even a validated command
`pass` defers to Codex's normal approval flow rather than emitting `allow`.
`PostToolUse` is evidence after the fact. It cannot grant, deny retroactively,
or undo a side effect.

Current native Bash/PowerShell responses contain truncated command output but
no exit status. They are therefore status-unknown and do not produce completion
events. Structured local/MCP responses with an explicit `is_error`, `success`,
or exit-code field may produce best-effort completion evidence.

The adapter stores JSONL at
`$CODEX_HOME/state/codexguard/audit.jsonl` (or
`~/.codex/state/codexguard/audit.jsonl`). Trusted process configuration may set
`CODEXGUARD_AUDIT` to another path. Writes use an OS lock and file sync. Raw
commands, working directories, model names, transcripts, and tool responses are
not stored; the policy artifact and completion carry digests. A failed or short
append is truncated back to its prior offset before the adapter returns failure.

For shell events, a tool-specific `workdir` is authoritative when present.
Current native Bash hook input exposes only `command`; the envelope's `cwd` is
the session directory, not proof of a per-call execution directory. Such calls
therefore park instead of claiming a replayable execution context. If a future
native envelope supplies `workdir`, the policy binds its digest without
persisting the raw path.

Pre/Post correlation also binds the exact tool-input bytes. If another
concurrent hook rewrites the call after codexguard evaluates it, the completion
cannot attach to the earlier decision.

## Honest failure matrix

Codex command hooks are a useful guardrail, not a complete enforcement
boundary. The current Codex behavior is documented in the
[official hook manual](https://developers.openai.com/codex/hooks): matching
commands can run concurrently, changed non-managed hook hashes are skipped
until trusted, unsupported output is reported as a hook failure, and some tool
paths may bypass the default hook path.

The pinned machine-readable matrix is
[`../testdata/hooks/failure-matrix.json`](../testdata/hooks/failure-matrix.json).

| Failure | Codex behavior | Covered shell action | Unsupported local/MCP action |
|---|---|---|---|
| executable missing | reports hook failure and may continue | exact matching `.rules` prefixes only; other shapes are not fail-closed | workflow parks |
| adapter crash | reports hook failure and may continue | exact matching `.rules` prefixes only; other shapes are not fail-closed | workflow parks |
| timeout | reports hook failure and may continue | exact matching `.rules` prefixes only; other shapes are not fail-closed | workflow parks |
| malformed stdout | reports hook failure and may continue | exact matching `.rules` prefixes only; other shapes are not fail-closed | workflow parks |
| changed/untrusted hook hash | skips until reviewed and trusted | exact matching `.rules` prefixes only; other shapes are not fail-closed | workflow parks |

None of those rows is described as fail-closed. A successfully invoked, valid
deny/refuse response stops the supported tool call. Narrow exec-policy rules
remain an independent backstop only for their exact static prefixes; they do
not claim semantic coverage of opaque wrappers or later arguments. Tool paths
that policy cannot normalize remain `park`, not silently allowed.

## Offline fixture check

No Codex task or model call is needed:

```powershell
$env:CODEXGUARD_AUDIT = Join-Path $env:TEMP "codexguard-audit.jsonl"
Get-Content -Raw cmd/codexguard/testdata/hooks/pre-bash-block.json |
  go run ./cmd/codexguard hook
```

The response is a native `PreToolUse` deny and the decision exists in the audit
before stdout is written. Remove the temporary audit after inspection.
