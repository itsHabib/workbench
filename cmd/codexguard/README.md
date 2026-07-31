# codexguard

`codexguard` is the deterministic policy floor for Codex tool calls. It
normalizes supported harness envelopes, classifies their semantic operation,
and emits a versioned
[`AutoDecisionV1`](../../contracts/automode/schema/auto-decision-v1.json).
It does not execute the call.

## Try it locally

```powershell
@'
{
  "envelope": {
    "kind": "shell",
    "shell": "powershell",
    "command": "go test ./cmd/codexguard/..."
  }
}
'@ | go run ./cmd/codexguard decide
```

That returns `pass` with rule `safe.read_or_test`. An authority-bearing action
returns a non-zero exit plus a decision artifact:

```powershell
@'
{
  "envelope": {
    "kind": "shell",
    "shell": "direct",
    "command": "git push --force origin main"
  }
}
'@ | go run ./cmd/codexguard decide
```

## Request

The emitted harness is always `codex`; request JSON cannot override it. Merge
evaluation reads the authoritative Gate state only from the process's
`GATE_STATE`; request JSON cannot select a state directory. `envelope.kind`
selects the explicit registry:

| Kind | Fields | Supported shape |
|---|---|---|
| `shell` | `shell`, `command` | direct, Bash/sh `-c`/`-lc`, PowerShell `-Command`/`-EncodedCommand`, `cmd /c` |
| `local` | `tool`, `arguments` | strict `shell_command` arguments |
| `mcp` | `tool`, `arguments` | the read-only registry plus the local shell seam |
| `code` | `code` | one static `tools.<name>(<JSON object>)` call |

Compound commands, substitutions, `Invoke-Expression`/`iex`, `Start-Process`,
unknown MCP tools, and non-static code-mode calls park. This is deliberate:
codexguard does not guess through an opaque wrapper.

## Merge rule

The only pass path for `gh pr merge` is:

1. the command includes repo, PR, and a 40-character
   `--match-head-commit`;
2. `gate next -state <configured> -json` contains exactly one current
   `ready_to_merge` row for that repo and PR;
3. the candidate inner command is byte-for-byte identical to the row's
   `merge_command`, and the row carries the same head;
4. an independent `gh pr view` reports `OPEN` at that exact head.

Any missing, stale, ambiguous, or differing fact refuses with a remedy to run
Gate again. codexguard never reconstructs a merge command and never imports Gate
decision logic.

## Native hook adapter

`codexguard hook` binds the same policy owner to Codex's native `PreToolUse`,
`PermissionRequest`, and `PostToolUse` envelopes. Pre-execution decisions are
synced to the AutoMode audit before a response is returned; permission requests
can never widen a non-pass; post-tool evidence can never grant authority.

See [`docs/hooks.md`](docs/hooks.md) for the response table, audit path, direct
offline fixtures, and honest hook-failure matrix. This slice deliberately ships
no installable hook definition: the projection layer must bind the command to
an absolute reviewed executable path, so a repository-local `codexguard`
binary can never replace the adapter.
