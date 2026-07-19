# Foreign-agent friction log — Runway Rooms adapter

Agent: Codex. Started: 2026-07-18 (America/Los_Angeles).

## Worked as documented

- **Repo orientation.** Read `docs/DESIGN.md`, the parent `CLAUDE.md`, and the
  repo `CLAUDE.md`. The single-module boundary law and canonical checks were
  present and unambiguous.
- **Rooms prerequisite checkout.** The checkout at `~/pers/rooms` existed and
  contained the released `rooms run --lifecycle` implementation, its lifecycle
  spec, and host-gated tests. The event vocabulary in the kickoff matches the
  implementation.
- **Git baseline.** `git fetch origin --prune` succeeded; local `main` and
  `origin/main` were identical. Creating `runway-rooms-adapter` from that base
  succeeded without touching the pre-existing untracked planning files.
- **Dossier protocol.** The installed `dossier` executable serves standard MCP
  over stdio. After locating the corpus, a normal MCP `task.claim` call claimed
  `tsk_01KX7652WS78ZN6S7H4CW5HWDG` for `codex:michael`.

## Friction

### Kickoff points at sections that do not exist

- **What I tried:** read `cmd/runway/docs/DESIGN.md` §6 and §9, as directed by
  `kickoff-codex-runway-adapter.md`.
- **What happened:** that file is a short Phase-1 design note with no numbered
  sections. It links to `docs/features/execution-runtime/spec.md`, where §6 and
  §9 actually live. `origin/main` has the same mismatch.
- **Class:** `doc-lie`.
- **Smallest fix:** change the kickoff's source-of-truth path to
  `docs/features/execution-runtime/spec.md`; keep `cmd/runway/docs/DESIGN.md` as
  the implementation note it currently is.

### Foreign-agent task claiming is possible but not discoverable

- **What I tried:** followed the parent/repo `CLAUDE.md` files, ran
  `dossier --help`, and inspected the installed one-shot subcommands.
- **What happened:** neither CLAUDE file names the dossier corpus, the
  `DOSSIER_CORPUS` environment variable is unset, and the one-shot CLI exposes
  list/update/complete but not claim. A filesystem search found
  `~/pers/dossier-state/.dossier`; source/docs then showed that `task.claim` is
  available only through the MCP server. Sending the standard MCP initialize +
  `tools/call` exchange to `dossier serve --corpus ~/pers/dossier-state`
  succeeded.
- **Class:** `discoverability`.
- **Smallest fix:** document the canonical corpus path and a client-neutral MCP
  invocation in `~/pers/CLAUDE.md`, or add a `dossier task_claim` one-shot
  command alongside `task_update` and `task_complete`.

### The locked TDD does not define the `agent-cursor` profile resolution

- **What I tried:** read the Runway TDD, Rooms lifecycle/runner contracts,
  Rooms CLI flags, Ship's existing `RoomCursorRunner`, and queried the local
  portfolio RAG for a newer decision.
- **What happened:** the TDD requires a resolved `agent-cursor` profile and
  pins only the SendEnv secret allowlist. It does not define where image/model
  configuration comes from or how a portable command/task input becomes the
  `--runner cursor` flags. The local RAG returned 0.60 confidence and explicitly
  found no exact profile contract; its nearest source was
  `rooms/docs/features/poc-m4-anthropic-curl/spec.md`, which only establishes
  the substrate-level `--command <STRING>` convention.
- **Class:** `genuine-gap`.
- **Smallest fix:** add one profile table to the Runway TDD naming the config
  source, required work/input shape, exact Rooms argv, enforced receipt fields,
  and secret allowlist. Until then the adapter has to make and document that
  decision locally.

### The canonical test command has a Windows-only takeover-race flake

- **What I tried:** ran the required uncached suite with
  `go test -count=1 ./...` after the focused adapter tests passed.
- **What happened:** `TestClaimTakeoverRace_OneWinner` failed once because
  Windows rejected the takeover-file rename while another contender still had
  the destination open: `The process cannot access the file because it is
  being used by another process.` The adapter/controller packages passed in
  that run. `go test -count=1 ./cmd/runway/internal/claim` immediately passed,
  followed by a green `go test ./...`.
- **Class:** `flaky-test`.
- **Smallest fix:** make the Windows takeover publication primitive tolerate
  the expected sharing violation (bounded retry plus identity recheck), then
  run this race test repeatedly on a Windows CI lane.

## Implementation and validation

- The adapter stayed behind Runway's private backend registry; provider names
  did not enter the portable contracts or controller policy. It consumes the
  Rooms lifecycle stream across startup, workload, collection, and cleanup,
  and durable reconcile fails closed unless `rooms ls --json` proves the
  allocation absent under the known schema.
- The hermetic CLI double exercised success, pool exhaustion, collection and
  cleanup failures, context cancellation, durable recovery, and secret
  handling without requiring KVM or an installed Rooms binary. The separate
  `rooms_host` build-tag test compiles normally and runs only when explicitly
  enabled on the target host.
- Green checks: `gofmt -l .`, `go vet ./...`, `golangci-lint run ./...`,
  `go test ./...`, `go build ./...`,
  `go run ./cmd/tracelens eval ./cmd/tracelens/testdata/corpus`, and
  `go test -tags rooms_host ./cmd/runway/internal/backend/rooms`. Local
  `go test -race` is unavailable because this Windows Go environment has CGO
  disabled (`go: -race requires cgo`); the Linux CI race lane remains the
  authoritative run.

## Top three fixes

1. **Lock the `agent-cursor` profile table in the Runway TDD.** This removes
   the only substantive policy guess: image/model resolution, task mapping,
   receipt assertions, and secret names should be reviewable in one place.
2. **Make the kickoff self-contained and accurate.** Correct the §6/§9 path
   and include the dossier corpus/claim invocation so a foreign agent does not
   need repository archaeology before starting the requested work.
3. **Harden and continuously exercise claim takeover on Windows.** The
   canonical local check should be trustworthy on the operator's primary OS;
   a known sharing-violation race should not force humans to distinguish a
   product regression from an infrastructure retry.

## Verdict

The repository's architectural charter and Rooms lifecycle contract were
strong enough to support a clean adapter without importing decision code
across tools. The kickoff was less reliable than the code: it cited the wrong
design document and omitted both task-claim discovery and the one unresolved
profile decision. Tests gave good leverage once implementation began, though
the Windows takeover flake weakens confidence in the canonical one-command
check. Fixing the profile table and kickoff path would remove most of the
foreign-agent tax; hardening the Windows claim race would remove the remaining
false alarm.

## Delivery record

- Opened [workbench PR #68](https://github.com/itsHabib/workbench/pull/68)
  from `runway-rooms-adapter` to `main`, then marked it ready after the initial
  CI run passed. Both `check` (including the Linux race lane) and `hygiene`
  were green before the review patch.
- Requested Copilot and posted `@codex review` plus `@claude review`. Copilot's
  four inline findings and Claude's six actionable suggestions were all
  implemented. Codex raised two findings: context-cancellable image hashing
  was implemented; the claim that `pool_full` lacks `room_id` was rejected
  against current Rooms source, which mints the ID and binds the lifecycle
  writer before attempting the slot claim.
- Consulted Gate without creating or supplying a grant:
  `gate gate -repo itsHabib/workbench -pr 68 -state
  C:\Users\MichaelHabib\pers\gate\state`. It refused with exit code 4 and
  `gate: gate: -repo, -pr, -grant required`, as required by the kickoff's
  no-grant boundary.
- No grant was minted, no merge was attempted, and nothing was pushed to
  `main`.
