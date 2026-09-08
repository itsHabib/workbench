<!-- reaper-work:v1 -->
# Work: Retry-safe Fleet assignments and truthful observation

Work-ID: fleet-task-coordination
Status: active
Subject: git:f1cea9a96ef77680ab3a6e05ed1614d6b8048e40
Stop-at: reviewed-change

## Outcome

A supervisor can retry an assignment without overwriting or duplicating work and
inspect hook-observed activity without claiming delivery, acceptance or termination.

## Preserve

- Existing branch leases, hooks, Gate authority and legacy dispatch behavior except
  refusing uncorrelated replacement of a request-bound or unreadable assignment.
- Root cmd/triage/labels/mismatches.jsonl and earlier worktree friction logs are unrelated.
- One existing dispatch store and hook-owned facts; no second editable ledger.

## Change

- `cmd/fleet/internal/verbs/request.go`: immutable request IDs and serialized replay.
- `cmd/fleet/internal/verbs/status.go`: read-only plain-language request observations.
- `cmd/fleet/internal/verbs/work.go`: protect request records from legacy mutations.
- `cmd/fleet/internal/verbs/verbs.go`: command entrypoints before lazy migration.
- `cmd/fleet/internal/fleet/hook.go`: completed write-tool observation in session record.
- `cmd/fleet/internal/mcp/mcp.go`: equivalent request/status tool entrypoints.
- `cmd/fleet/internal/verbs/request_test.go`: replay, conflicts, races and read-only proof.
- `cmd/fleet/internal/fleet/task_activity_test.go`: post-tool activity provenance.
- `cmd/fleet/internal/mcp/task_test.go`: non-migrating observation through JSON-RPC.
- `cmd/fleet/README.md`: interface, current limits and next adapter proof.

## Prove

- Green: go test -race ./cmd/fleet/...; go vet ./cmd/fleet/...; golangci-lint run ./cmd/fleet/...
- Green: both cmd/fleet/testdata/run-suite.sh harness suites; focused real Git CLI exercise.
- Red: competing requests, changed replay, damaged evidence and late unrelated activity never yield false acceptance.

## Stop

- No live worker launches, stops, lease transfers or installed hook changes from this PR.
- Delivery, semantic acceptance, correlated answers and effect-safe replacement remain
  follow-on implementation under tsk_01M1ZJVZ1ZDHJC1PR1AZGE47TC, not claimed complete.

## Evidence

- Verified: focused Fleet race tests, root Go vet/lint, and Claude regression scenarios pass.
- Verified: separate-process replay/conflict tests and Codex regression scenarios pass.
- Verified: incomplete MCP status remains parseable JSON; compiled-binary fixture smoke passes for both adapter shapes.
- Pending: exact-head PR review and full-module CI.

## Handoff

- Last: CLI/MCP request and status paths implemented; initial existing package tests pass.
- Next: add negative concurrency/read-only tests and run the documented checks.
