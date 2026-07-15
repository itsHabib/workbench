**Status**: draft
**Owner**: @michael
**Date**: 2026-07-13
**Related**: dossier task `local-usage-report` (id: `tsk_01KXDYG9NZ1T98R00FST9DSV7S`, workbench project, offload-adoption phase)

# local usage: adoption report over the offload usage ledger — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `cmd/local/report.go`, `cmd/local/main.go`, `cmd/local/usage.go` | ~130 | 130 |
| Tests | `cmd/local/report_test.go` | ~160 | 80 |
| **Total** | | | **~210** |

Band: **amazing** per repo's PR sizing convention.

## Goal

PR #11 gave `local` a write-side usage ledger (`cmd/local/usage.go` appends one JSONL record per invocation: ts/cwd/prompt/source/flagged), but there is no read side. The "measured local-first offloading" claim needs adoption numbers — calls by repo, local-vs-cloud split, flagged rate over time — and today the only way to get them is hand-grepping `usage.jsonl`. Add a `usage` subcommand that rolls the ledger up.

## Behavior / fix

- Resolve the log via the same path logic as `logUsage` (`LOCAL_USAGE_LOG` → `XDG_STATE_HOME` → `~/.local/state/local/usage.jsonl`) — reuse the existing `usageLogPath` function, do not duplicate it.
- Report: total invocations, span (first→last ts), per-repo counts (last path segment of `cwd`; full path in `-json`), per-day counts, source split (`local` vs `cloud`), flagged count + rate.
- `-json` flag emits the same rollup as one JSON document (stable field names) for reporting tooling.
- Missing or empty log → a friendly zero report, exit 0 (absence of data is an answer, not an error). Malformed lines are skipped and counted; the count is reported, never a crash — mirror the best-effort posture of the write side.
- House style applies: line-of-sight, no `else`, ≤2 nesting levels per scope, stdlib only.

## Acceptance

- `local usage` over a fixture ledger prints totals, per-repo, per-day, source split, and flagged rate; `local usage -json` round-trips the same numbers.
- Missing log → zero report, exit 0. A ledger with malformed lines reports N skipped and correct stats for the rest.
- Path resolution is shared with `logUsage` (one function, not two copies).

## Test plan

`go test ./cmd/local/...` — table tests over fixture ledgers (empty, missing, mixed sources, malformed lines, multi-repo/multi-day). `gofmt -l`, `go vet ./...`, `golangci-lint run ./...`.

## Non-goals

Changing the record shape or write side; charts/rendering (reporting tooling consumes `-json`); pruning/rotation of the ledger.
