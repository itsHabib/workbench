# Native Codex hooks

`codexguard hook` is the thin lifecycle adapter for Codex's native
`PreToolUse`, `PermissionRequest`, and `PostToolUse` command hooks. It reads one
current Codex envelope from stdin and delegates every policy decision to
`internal/policy`.

The reviewed but uninstalled hook source is
[`../assets/hooks.json`](../assets/hooks.json). Installation and trust are a
separate projection step. This slice does not edit a Codex profile.

## Ordering and responses

`PreToolUse` and `PermissionRequest` first append and sync an
`AutoModeAuditEventV1` decision. Only then may they write a native hook response.
If persistence fails, the adapter emits no allow or deny response and exits as a
hook failure.

| Event | Policy outcome | Native response |
|---|---|---|
| `PreToolUse` | `pass` | no response; normal Codex permissions still apply |
| `PreToolUse` | `park`, `block`, `refuse` | `permissionDecision: deny` with rule and remedy |
| `PermissionRequest` | `pass` | `behavior: allow` |
| `PermissionRequest` | `block`, `refuse` | `behavior: deny` with rule and remedy |
| `PermissionRequest` | `park` | no decision; normal approval flow remains |
| `PostToolUse` | prior persisted `pass` plus an unambiguous result signal | append and sync completion evidence |
| `PostToolUse` | no prior pass or ambiguous result | no authority and no completion event |

Permission handling cannot widen policy: `allow` is representable only for a
validated policy `pass`. `PostToolUse` is evidence after the fact. It cannot
grant, deny retroactively, or undo a side effect.

The adapter stores JSONL at
`$CODEX_HOME/state/codexguard/audit.jsonl` (or
`~/.codex/state/codexguard/audit.jsonl`). Trusted process configuration may set
`CODEXGUARD_AUDIT` to another path. Writes use an OS lock and file sync. Raw
commands, working directories, model names, transcripts, and tool responses are
not stored; the policy artifact and completion carry digests.

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
| executable missing | reports hook failure and may continue | restrictive `.rules` is the backstop | workflow parks |
| adapter crash | reports hook failure and may continue | restrictive `.rules` is the backstop | workflow parks |
| timeout | reports hook failure and may continue | restrictive `.rules` is the backstop | workflow parks |
| malformed stdout | reports hook failure and may continue | restrictive `.rules` is the backstop | workflow parks |
| changed/untrusted hook hash | skips until reviewed and trusted | restrictive `.rules` is the backstop | workflow parks |

None of those rows is described as fail-closed. A successfully invoked, valid
deny/refuse response stops the supported tool call. Restrictive exec-policy
rules remain the independent shell-command backstop. Tool paths that policy
cannot normalize remain `park`, not silently allowed.

## Offline fixture check

No Codex task or model call is needed:

```powershell
$env:CODEXGUARD_AUDIT = Join-Path $env:TEMP "codexguard-audit.jsonl"
Get-Content -Raw cmd/codexguard/testdata/hooks/pre-bash-block.json |
  go run ./cmd/codexguard hook
```

The response is a native `PreToolUse` deny and the decision exists in the audit
before stdout is written. Remove the temporary audit after inspection.
