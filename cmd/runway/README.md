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
runway reconcile <run-id> [--state <dir>] [--json]
```

State root defaults to `$RUNWAY_STATE`, otherwise `~/.runway`. Every
run-addressing command accepts `--state`.

`--json` puts machine output on stdout and diagnostics on stderr.

### Exit codes

| Code | Meaning |
| ---: | --- |
| 0 | terminal success, or successful read / cancel / reconcile no-op |
| 2 | invalid request / CLI usage; no run admitted |
| 3 | terminal failed |
| 4 | placement unavailable / backpressure |
| 124 | timed out |
| 130 | cancelled |

The result schema is authoritative over process exit codes.

### Notes

- `watch` reads durable `events.ndjson` only — never backend stdout. It makes
  no controller-liveness claim.
- `logs` tails buffered workload bytes; ordered per stream, may lose the
  unflushed tail on abrupt controller loss.
- `cancel` verifies recorded controller PID + process-start identity, writes
  an atomic cancel-request marker, and best-effort wakes the controller. The
  marker is authoritative; on Windows there is no wake signal, so cancel
  latency is the controller's marker poll interval (~50ms). Repeat cancel
  after intent or terminal state is a successful no-op.
- `result --wait` requires `--timeout`. It watches durable state and never
  reconciles implicitly.
- `reconcile` is the explicit recovery verb for one known run ID after
  controller death. It takes over the writer claim, best-effort cleans the
  backend allocation, and either appends a missing `run_terminal` or writes a
  `controller_lost` receipt. Concurrent reconcilers: exactly one mutates.
  v0 does not scan for orphans or promise host-restart adoption.

See `docs/DESIGN.md` for policy vs mechanism, the writer-claim primitive, and
the one-writer rule.

## Rooms placement

`{"backend":"rooms","profile":"agent-cursor"}` installs the Rooms CLI
adapter. The profile resolves on the host; placed requests never contain host
paths or secret values:

- `RUNWAY_ROOMS_IMAGE` selects the cursor guest image (default
  `~/rooms/images/agent-alpine-cursor.ext4`).
- `RUNWAY_ROOMS_MODEL` selects the pinned Cursor model (default
  `composer-2.5`).
- `RUNWAY_ROOMS_BIN` selects the CLI name/path (default `rooms`). Production
  invokes `sudo -E <rooms-bin> run ...`; no shell is involved.
- The work bundle must declare an input named `task`. Its materialized target,
  the immutable workspace URL/revision, image, model, output directory, and
  lifecycle path become the `rooms run --runner cursor` argv.
- Only `CURSOR_API_KEY` and `ANTHROPIC_API_KEY` are accepted as work secret
  names. Values travel in the child environment for Rooms' SSH `SendEnv`
  allowlist; they never enter argv, lifecycle, receipts, or `backend.json`.

The adapter consumes Rooms lifecycle NDJSON in four boundaries: `Start`
through `workload_started`, `Wait` through `workload_exited`, `Collect` through
collection completion, and `Cleanup` through verified teardown. Structured
`pool_full {cap}` becomes `placement_unavailable` (exit 4) without a hidden
retry. The result receipt records the image digest, fixed resource/network
constraints, slot details, and `terminal_replay` stream delivery.

Unit tests use a hermetic CLI double and need no Rooms installation. The live
smoke is opt-in behind `-tags rooms_host` and `RUNWAY_ROOMS_HOST_TEST=1`; Gate C
placement equivalence remains a separate downstream task.
