# Fleet task coordination: first implementation increment

This adds retry-safe local assignments and a read-only observation view. It does
not yet implement the four-interaction product: launch/delivery, semantic worker
acceptance, correlated questions, safe stop and replacement remain adapter work.
Do not activate a live trial or present this as cross-harness lifecycle parity.

The approved direction is [cc-skills PR #60](https://github.com/itsHabib/cc-skills/pull/60):
one lead, one active worker, task-owned workspace and natural interaction through
the supervisor skill. The interfaces below are for the supervisor/adapter, not a
set of commands the operator should have to learn.

## Record once, retry safely

From the task's repository checkout, with a known worker:

```sh
fleet request my-branch --id navigation-fix-1 --worker SESSION \
  --for supervisor:ivy --brief 'Reproduce and fix the navigation failure; return focused checks.'
fleet status
fleet status --json
```

Use the discovered executable path if Fleet is not on PATH. The installed Mac
entrypoint during this build was `/Users/mh/.fleet/bin/fleet`; the new commands
are not available there until a reviewed release is installed.

Equivalent MCP tools are `fleet_request` (requires caller cwd) and `fleet_status`.
The supervisor chooses a stable request ID before calling. Same repo + same ID +
same branch/worker/lead/brief returns the existing assignment without renewing its
timestamp, resetting its initial head, posting a message or acquiring a lease.
Full-ID retries remain valid after branch deletion or session-record cleanup; a
short session prefix must still resolve uniquely. Supply a branch name, not a
numbered change. Changing the payload under that ID refuses. A second assignment for the same
branch refuses, as do unknown ownership, an unavailable worker and an applicable
stop flag. A recorded assignment is not an execution reservation; the ordinary
hook/lease guard still controls actual effects.

Records extend the existing `dispatch` row with `request_id` and `worker`.
One dispatch-store lock serializes decisions across processes, followed by the
existing branch lock for ownership inspection. Legacy dispatch/reassign/undispatch
cannot overwrite or delete these records, including with `--take`. They remain
retained until a correlated lifecycle operation is implemented. Do not remove
records manually to reuse IDs. Ordinary legacy records remain supported.

`request` is effectful and performs the existing lazy key migration before lease
inspection. Retained collisions refuse; failed requests can leave a migration
marker/lock but no new assignment. No GitHub write or worker launch occurs.

## Observe without claiming more than the evidence

`status` bypasses migration in both CLI and MCP and restores read-only mode after
rendering. JSON is `fleet-task-status-v1`, scoped to local request-bound assignments;
it is not the full portfolio inventory or permission to dispatch. `complete`
means the assignment sources were readable, not that every task is healthy.

- **Queued:** the assignment exists; delivery and acceptance are unconfirmed.
- **Activity observed:** the selected worker has a matching post-dispatch write-tool
  event on the task branch. This is not a claim of successful edits or acceptance.
- **Status needs checking:** conflicting/unreadable ownership, stop flag or missing
  worker liveness. A stopped/dead session does not establish command quiescence.

The existing hook-owned session record carries `last_writes`, keyed by branch,
with time and tool-use ID; `last_write` remains for compatibility. Writes on a
second branch do not erase the first branch observation. Read-only commands, old activity and another branch do not count.
JSON keeps IDs and evidence timestamps for debugging; terminal output does not
require the operator to interpret internal session IDs. No new agent-written
progress ledger, acceptance claim, done state or automatic takeover is introduced.

Generated role bindings subscribe Codex write events and supplement Claude
file-write post-tool events alongside its global Bash hook. Existing bindings
need regeneration and harness reload when this release is installed. This PR
does not edit installed hooks. Terminal observations include the activity age.

## Verification

Run `go test -race ./cmd/fleet/...`, `go vet ./cmd/fleet/...` and
`golangci-lint run ./cmd/fleet/...`, then both `testdata/run-suite.sh` variants
(default and `codex`). New tests cover real Git state, separate-process replay and
conflicts, immutable payloads, legacy-writer protection, damaged evidence,
post-tool provenance and non-migrating JSON-RPC observation. Harness event
fixtures are not proof of actual live Claude/Codex delivery or stop behavior.
