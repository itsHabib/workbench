# Control Room source configuration and privacy

**Status:** draft (Phase 1 contract)
**Owner:** Workbench maintainers
**Date:** 2026-07-13
**Related:** [`spec.md`](spec.md), [`phases/control-room-contract-fixtures-config.md`](phases/control-room-contract-fixtures-config.md)

Versioned configuration consumed by Phase 4 adapters. Readers are additive-field
tolerant: unknown fields may be ignored; missing required identity or command
fields fail closed at startup.

## Configuration schema (`v: 1`)

```json
{
  "v": 1,
  "mode": "demo",
  "workspace_root": "%USERPROFILE%/pers",
  "sources": {
    "ship": {
      "enabled": true,
      "executable": "ship",
      "discover_path": true,
      "argv": {
        "workflow_list": ["list", "--json"],
        "workflow_status": ["status", "{id}", "--json"],
        "driver_list": ["driver", "list", "--json"]
      },
      "timeout_ms": 10000
    },
    "dossier": {
      "enabled": true,
      "executable": "dossier",
      "discover_path": true,
      "argv": {
        "serve": ["serve", "--corpus", "{corpus}"]
      },
      "corpus": "%USERPROFILE%/pers/dossier-state",
      "timeout_ms": 10000
    },
    "github": {
      "enabled": true,
      "executable": "gh",
      "discover_path": true,
      "minimum_version": "2.90.0",
      "scopes": [
        "user:synthetic-author"
      ],
      "timeout_ms": 10000,
      "inventory_page_size": 50,
      "inventory_page_cap": 4
    },
    "tracelens": {
      "enabled": true,
      "executable": "tracelens",
      "discover_path": true,
      "argv": {
        "analyze": ["ship", "-json", "{run_ref}"]
      },
      "timeout_ms": 10000,
      "diagnose_timeout_ms": 10000,
      "enrichment_timeout_ms": 35000
    },
    "toolhealth": {
      "enabled": true,
      "executable": "toolhealth",
      "discover_path": true,
      "argv": [],
      "timeout_ms": 5000,
      "surface": "text"
    },
    "tower": {
      "enabled": false,
      "executable": "tower",
      "discover_path": true,
      "argv": {
        "ls": ["ls", "--json", "--no-reconcile"]
      },
      "timeout_ms": 5000
    }
  },
  "collection": {
    "core_timeout_ms": 15000,
    "enrichment_timeout_ms": 35000,
    "refresh_interval_ms": 60000
  }
}
```

### Required fields

| Field | Rule |
|---|---|
| `v` | Must be `1`. Unknown versions fail closed. |
| `mode` | `demo` or `real`. Demo ignores subprocess configuration. |
| `workspace_root` | Absolute or `%USERPROFILE%`-anchored workspace root for path validation. Never derived from sibling stores. |
| `sources.dossier.corpus` | The sole Dossier corpus setting. Required when Dossier is enabled; no root-level fallback or override exists. |
| `sources.<name>.enabled` | Boolean. Optional sources (`tower`) default `false`. |
| `sources.<name>.executable` | Command name or absolute path. When `discover_path` is true, search `PATH` only — never infer sibling package locations. |
| `sources.github.scopes` | Real mode: one to four entries, each `user:<login>`, `org:<login>`, or `repo:<owner/name>`. Zero or more than four is a startup error. |

### Executable discovery

Adapters may resolve executables on `PATH` when `discover_path` is true. They
must never:

- Read Ship SQLite, Dossier markdown corpus directly, or sibling artifact stores.
- Derive `dossier_corpus`, runs directories, or friction log paths from producer
  package layout.
- Guess GitHub scope from ambient repository metadata.

All argv templates, timeouts, corpus paths, and scopes are explicit configuration.

### GitHub scope validation

| Scope prefix | Example | Search qualifier |
|---|---|---|
| `user:` | `user:synthetic-author` | `is:pr is:open author:<login> archived:false` combined with scope |
| `org:` | `org:example-org` | Same author filter within org repos |
| `repo:` | `repo:example-org/example-repo` | Scoped to single repository |

Inventory paging: round-robin across scopes, at most four pages of 50 pull
requests (200 total) within the source timeout. Unvisited next page yields
`degraded` with `error_code = "inventory_truncated"`.

Detail saturation: when `statusCheckRollup.contexts.pageInfo.hasNextPage` or
`reviewThreads.pageInfo.hasNextPage` is true, the adapter sets
`detail_state = "truncated"`. Otherwise `complete` when all connections are
present; `unknown` when required fields are absent.

### Dossier MCP lifecycle

Real `serve` mode keeps one long-lived stdio child per process:

1. Start `dossier serve --corpus <configured-corpus>`.
2. Handshake MCP over stdin/stdout.
3. Issue read-only `tools/call` for `project.list`, `project.overview`,
   `phase.list`, `task.list`, `task.get`, `artifact.list`.

Session failure (EOF, exit, call error) marks Dossier unavailable. After three
consecutive start/handshake/first-call failures, automatic probes pause for five
minutes; one manual refresh may perform a single half-open probe.

### Tracelens bounds

- Enrichment lane: analyze up to five eligible traces per generation.
- Diagnose POST: 10-second synchronous bound per run.
- Eligible traces: terminal workflow runs with evidence available, `updated_at`
  within 14 days, sorted `updated_at` descending then stable run ID.

### Toolhealth provisional seam

v1 consumes the existing human text board (`toolhealth` with no flags). Control
Room applies a tolerant fixture-backed parser and never reimplements local-model
bucketing. If the text contract drifts, the source degrades. A future
`toolhealth -json` at the owner seam replaces text parsing without changing the
normalized `ToolHealth` presentation shape.

### Tower optional enablement

`tower.enabled` defaults to `false`. Missing executable or disabled config
yields `unavailable` — never a startup failure. When enabled, only
`tower ls --json --no-reconcile` supplies supplemental branch/path context.

## Privacy and redaction rules

These rules apply to fixtures, adapter error messages surfaced to the browser,
and configuration examples committed to the repository.

### Never commit or emit

| Class | Rule |
|---|---|
| Credentials | GitHub tokens (`ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_`, `github_pat_`), Cursor API keys, OpenAI/Anthropic keys (`sk-`, `sk-ant-`), or environment assignments where the variable name contains `TOKEN`, `KEY`, `SECRET`, `PASSWORD`, or `CREDENTIAL` followed by a real value |
| Private keys | PEM headers matching `BEGIN ... PRIVATE KEY` |
| Operator identity | Real display names, emails, or GitHub logins from the operator environment |
| Absolute operator paths | Expanded Windows `C:\Users\...` or Unix `/home/<name>/`, `/Users/<name>/` paths observed from a real machine |
| Raw prompts and traces | Unredacted agent prompts, full trace payloads, or stderr dumps from child processes |

### Allowed placeholders

| Placeholder | Usage |
|---|---|
| `%USERPROFILE%` | Documentation and configuration examples only — never as an observed absolute path in fixtures |
| `synthetic-author`, `synthetic-agent` | Fixture identity stand-ins |
| `docs/features/example-feature/spec.md` | Neutral owner-issued `docPath` / `specPath` linkage |
| `worktrees/<name>`, `runs/<id>/` | Relative paths within a repository workspace |

### Child-process error sanitization

Raw child stderr never enters a snapshot or browser-facing fixture. Adapters emit
typed `SourceReceipt` records with `error_code` and a sanitized `message` after:

1. Removing absolute paths, usernames, and credential-like substrings.
2. Truncating to 200 characters.

Unavailable and degraded fixtures model these receipts — see
`cmd/controlroom/testdata/contracts/*/source-unavailable.json` and GitHub
`receipt-inventory-truncated.json`.

### Path validation (real mode)

Deep links and `vscode://file/` targets require:

1. Existing absolute path.
2. `filepath.Abs`, `filepath.Clean`, `filepath.EvalSymlinks` on path and configured `workspace_root`.
3. `filepath.Rel` with rejection of different volumes, `..`, and relative paths beginning with `..` plus separator.

## Fixture provenance commands

Recorded for review; not executed during `go test`.

```text
# Ship workflow observability (PR #194)
ship list --json
ship status wf_demo_01 --json

# Ship driver list (PR #193)
ship driver list --json

# Dossier MCP session
dossier serve --corpus %USERPROFILE%/pers/dossier-state
# MCP tools/call: task.list, task.get

# GitHub inventory (gh >= 2.90.0)
gh api user --jq .login
gh api graphql -f query='...' -f cursor=

# Tracelens
tracelens ship -json wf_demo_01

# Tool health (provisional text)
toolhealth

# Tower (optional)
tower ls --json --no-reconcile
```

Fixture inventory: [`cmd/controlroom/testdata/contracts/README.md`](../../cmd/controlroom/testdata/contracts/README.md).

## Demo vs real mode

| Mode | Behavior |
|---|---|
| `demo` | Load fixtures from `cmd/controlroom/testdata/contracts/` and `testdata/demo/` (Phase 3). No subprocesses. |
| `real` | Execute configured adapters with explicit timeouts. Partial failure retains last-successful records as stale. |

## Validation

`cmd/controlroom/fixtures_test.go` enforces:

- JSON/JSONL syntax for every fixture document.
- Inventory coverage for all six source directories.
- Healthy and degraded/unavailable coverage per source.
- Ship forbidden keys (`sourceJson`, `manifestPath`, `artifactsDir`, absolute paths).
- Credential and operator-path sanitization scans.

Run: `go test -race ./cmd/controlroom/...`
