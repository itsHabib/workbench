**Status**: draft
**Owner**: @mh
**Date**: 2026-07-16
**Related**: dossier task `workbench-mcp-v0-server` (id: `tsk_01KXQ54XP7RGAX9B0G79WEPMVN`), locked design [docs/features/driver-state/spec.md](../driver-state/spec.md) §6, §9 P3, §11 (PR #47)

# cmd/workbench-mcp stdio server + cmd/driverstate CLI — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | cmd/workbench-mcp/*, cmd/driverstate/* | ~350 | 350 |
| Tests | handler + CLI tests | ~400 | 200 |
| **Total** | | | **~550** |

Band: **ideal** per workbench PR sizing.

## Goal

P3 of the driver-state plane: the unified MCP surface v0 exposing the four driver verbs over stdio, and the 1:1 CLI mirror. This is the client boundary of the plane — the spec §11 validation gate runs against it.

## Behavior

Per spec §6 (the locked contract — do not relitigate D1–D6 or resolved §10 questions):

| Verb | In | Out |
|---|---|---|
| `driver_record` | `{run?, event}` (run omitted on `run_imported` → minted) | the appended event (id, hash) or structured error |
| `driver_state` | `{run}` | `RunState` |
| `driver_runs` | `{repo?, live?}` | `[]RunSummary` |
| `driver_verify` | `{run}` | ok / `ErrChainBroken` detail |

- Structured errors: `ErrIllegalTransition`/`ErrChainBroken`/`ErrLocked` codes surface to the client (F2 is the plane's whole value — the validator corrects the agent).
- **Lease lifecycle (review M2):** the server holds the active `Lease` and auto-renews it on a background goroutine at interval = staleness-threshold / 2 for as long as the client session is connected. There is deliberately NO `driver_renew` verb. Server exit (stdio close) stops renewal; an orphaned lease self-expires within one threshold window.
- **State-root resolution is canonical, not ambient (review P2):** resolve once at startup — explicit `WORKBENCH_STATE_DIR`, else the real (non-virtualized) user profile — and PRINT the resolved path at startup. Two MCP instances resolving different roots is the ship/MSIX failure mode this exists to kill; the §11 gate includes a cross-client check.
- Verb exposure is **compile-time registration** in `cmd/workbench-mcp` (opt-in per tenant; capability-mutating verbs excluded by construction). Unknown verbs return MCP `MethodNotFound`.
- `cmd/driverstate` CLI mirrors 1:1 (`workbench driverstate record|state|runs|verify`, `--json`).
- Registration: `.mcp.json` (project or user scope), `WORKBENCH_STATE_DIR` flowing through the server env.
- Dependencies: `cmd/workbench-mcp` and `cmd/driverstate` may import `driverstate` + `contracts` (mechanism + leaf); nothing cross-tool. Stdlib only.

## Acceptance

Spec §11 validation-gate preconditions all satisfiable: every transition recordable through the verbs; killed session's lease self-clears; fresh session resumes via `driver_runs {live:true}` → `driver_state`; terminal and Desktop-connector clients resolve (and print) the same state root. CI hygiene green.

## Test plan

- Verb handlers over a temp state dir: happy paths + each structured-error path.
- Unknown verb → `MethodNotFound`.
- Auto-renew goroutine renews at threshold/2 (fake clock); renewal stops on server exit.
- Startup prints the resolved state root (explicit env and fallback paths).
- CLI mirror: golden `--json` outputs per verb.

## Non-goals

- P4 `/work-driver --engine session` skill.
- P5 ship emitter.
- Any verb beyond the four (no `driver_renew`, no capability-mutating verbs).
