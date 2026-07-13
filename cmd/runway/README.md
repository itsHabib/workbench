# runway

Foreground execution-runtime controller for Workbench. One admitted request
becomes one run directory, one ordered lifecycle journal, and at most one
terminal `result.json`.

## Install

```bash
go install github.com/itsHabib/workbench/cmd/runway@latest
```

## CLI

```text
runway run --spec <request.json> --bundle <dir> [--state <dir>] [--json]
runway watch <run-id> [--state <dir>] [--after <seq>] [--follow] [--json]
runway logs <run-id> [--state <dir>] [--stream stdout|stderr] [--follow]
runway cancel <run-id> [--state <dir>] [--json]
runway result <run-id> [--state <dir>] [--wait --timeout <duration>] [--json]
```

State root defaults to `$RUNWAY_STATE`, otherwise `~/.runway`. Every
run-addressing command accepts `--state`.

`--json` puts machine output on stdout and diagnostics on stderr.

### Exit codes

| Code | Meaning |
| ---: | --- |
| 0 | terminal success, or successful read / cancel no-op |
| 2 | invalid request / CLI usage; no run admitted |
| 3 | terminal failed |
| 4 | placement unavailable / backpressure |
| 124 | timed out |
| 130 | cancelled |

The result schema is authoritative over process exit codes.

### Notes

- `watch` reads durable `events.ndjson` only — never backend stdout.
- `logs` tails buffered workload bytes; ordered per stream, may lose the
  unflushed tail on abrupt controller loss.
- `cancel` verifies recorded controller PID + process-start identity, writes
  an atomic cancel-request marker, and signals the controller. Repeat cancel
  after intent or terminal state is a successful no-op.
- `result --wait` requires `--timeout`. It watches durable state and never
  reconciles implicitly (`reconcile` is PR 3).

See `docs/DESIGN.md` for policy vs mechanism and the one-writer rule.
