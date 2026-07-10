# Execution Runtime - Technical Design Document

**Status:** draft / proposal - NOT a build commitment. This is the artifact we decide from.
**Owner:** @itsHabib
**Date:** 2026-07-10
**Related:** [`docs/DESIGN.md`](../../DESIGN.md), [`agentic-workbench-closure`](../agentic-workbench-closure/spec.md), `pers/rooms/docs/runner-contract.md`, `pers/rooms/docs/experiments/multi-agent-rooms-2026-07-09.md`, `pers/ship/packages/agent-runner/src/runner.ts`, `pers/cortex/docs/compute-spawning/phase-1-foundation/plans/behaviour-architect.md`

> **Reviewers - focus areas:** (1) whether D1 keeps Runway a mechanism rather than another orchestrator, (2) whether the request/result schemas leak Rooms or AI-provider concepts, (3) whether the event and terminal-state laws are strong enough under cancellation, timeout, collection failure, and controller loss, and (4) whether the Phase 2 Rooms gate is the cheapest honest proof of the thesis.

## 1. Problem & hypothesis

The portfolio can execute the same broad category of work through several incompatible boundaries:

- Ship's provider-neutral `AgentRunner` accepts a rendered prompt, model, workspace path, provider configuration, and event callback.
- Rooms accepts CLI arguments and environment variables, boots a Firecracker microVM, and collects a terminal artifact directory.
- Cursor Cloud, Claude, Codex, local processes, browser sessions, and test harnesses each expose different start, event, cancellation, and result shapes.
- The parked Cortex design defined a focused `SpawnBackend` (`spawn`, `stream`, `stop`, `status`), but tied it to Cortex's orchestration model rather than a portable artifact contract.

This fragmentation has concrete costs. Ship's Rooms adapter knows `sudo -E rooms run`, temporary task files, image paths, output directories, and Rooms' terminal schema. Host absolute paths leaked into guest prompts. Room lifecycle logs and agent events are separate streams. A Firecracker `InstanceStart` was reported as "booted" even when the guest kernel immediately panicked. Two equivalent one-line agent tasks took 29.5 seconds and 159.9 seconds, while Ship passed no substrate deadline. Host hooks and subagent definitions did not enter the room because they were ambient state rather than declared inputs.

The missing primitive is not another agent framework. It is a small execution contract that says:

> one explicit work request, one placed run, one ordered event history, and one terminal receipt.

**Hypothesis:** if a caller can compile work into one versioned request and receive semantically equivalent events/results from both a local-process backend and Rooms, then Ship and future tools can stop owning placement-specific lifecycle code. If the contract cannot make those two backends equivalent without leaking their internals, it has not earned cloud backends, a daemon, or a standalone repository.

### Goals

- Define a language-neutral request, event, result, artifact, and error vocabulary under `workbench/contracts`.
- Add a focused `runway` executable inside Workbench.
- Make placement an explicit caller choice, not hidden routing policy.
- Make workspace, inputs, hooks, secrets, outputs, deadline, and cleanup policy explicit.
- Distinguish controller start, backend allocation, workload readiness, workload execution, collection, cleanup, and terminal completion.
- Produce hashable, shareable run directories that survive the execution process.
- Prove the contract first with local processes and Rooms.

### Non-goals

- A scheduler, queue, DAG engine, workflow engine, or multi-host control plane.
- A resident daemon in the first validation slice.
- Model selection, prompt rendering, reviewer policy, retries, or agent judgment.
- Replacing Ship's workflow state, Rooms' VM lifecycle, or Dossier's work index.
- Making Firecracker snapshots part of v0.
- Supporting arbitrary remote artifact stores before local content-addressed inputs prove necessary.
- A new standalone repository. Extraction is earned only by independent release/version pressure.

## 2. Functional & non-functional requirements

### Functional requirements

- **FR1 - Explicit request:** a run request declares command, logical workspace, materialized inputs, secret references, expected outputs, placement, deadline, and cancellation grace.
- **FR2 - Provider neutrality:** the contract contains no Cursor, Claude, Codex, model, prompt, MCP, or subagent fields. An agent system compiles those into command, inputs, and secret references.
- **FR3 - Logical paths:** request paths are relative to named logical roots. Host paths and guest paths never appear as portable workspace identity.
- **FR4 - Explicit materialization:** hooks, skills, runner scripts, task files, and agent definitions enter a run only as declared inputs or as content baked into the selected image.
- **FR5 - Placement receipt:** the result records what backend and immutable inputs actually ran, including request digest, workspace revision, image digest when available, and backend allocation identity.
- **FR6 - Ordered lifecycle:** every durable event has a run ID and strictly increasing sequence number. A watcher can resume after a sequence without parsing logs.
- **FR7 - One terminal receipt:** every schema-valid accepted request produces at most one terminal `result.json`. Malformed requests fail before admission and produce no run.
- **FR8 - Distinct failure phase:** admission, preparation, startup, workload, collection, cleanup, timeout, cancellation, and controller-loss failures are distinguishable without message matching.
- **FR9 - Deadline enforcement:** the controller enforces one absolute work deadline independently of an agent SDK's own timeout.
- **FR10 - Cancellation:** cancellation is idempotent, records operator intent separately from timeout, and preserves partial events/artifacts.
- **FR11 - Durable observation:** `watch` and `result` read the run directory, not controller memory. Stdout is never the only copy of an event or result.
- **FR12 - Secret safety:** requests contain opaque secret references, never secret values. Events, diagnostics, receipts, and backend descriptors never contain resolved values.
- **FR13 - Backend isolation:** backend implementations are private to `cmd/runway`; callers consume contracts and process/artifact surfaces, not backend call stacks.
- **FR14 - Tolerant readers:** additive fields do not break older readers of the same major schema. Unknown major versions fail loudly.
- **FR15 - Portable references:** requests name bundle inputs and images by logical reference plus digest. Backend configuration, not the request, maps those references to host paths.

### Non-functional requirements

| Property | Target |
| --- | --- |
| Simplicity | Phase 0-2 add no daemon, queue, database, network listener, or third-party Go dependency. |
| Durability | Request and each event append are flushed before the next externally visible transition; `result.json` is written atomically. |
| Ordering | One active controller is the sole event writer. Sequence numbers are contiguous from 1 through terminal. |
| Cancellation | A cancel request reaches a live controller within 1 second locally; the terminal receipt appears within configured grace plus 5 seconds. |
| Timeout | A deadline overrun cannot remain `running`; it terminates as `timed_out` even if the underlying runner is still noisy. |
| Security | No resolved secret value is written to argv, request, events, result, backend state, or diagnostics. Backend-private state is mode `0600`. |
| Portability | The same fixture request runs through local and Rooms backends with the same terminal status, declared outputs, and phase vocabulary. |
| Operability | A run is diagnosable from its directory after the controller and backend processes exit. |
| Compatibility | JSON Schema draft 2020-12 is canonical; Go types are conformance-tested. TypeScript/Rust consumers use the schema, not Go imports. |
| Scope | Each implementation PR stays below roughly 700 weighted LOC; a larger slice needs a no-split argument. |

## 3. Architecture overview

```text
 Ship / browser tool / test harness / operator
                    |
          compile domain intent
                    |
                    v
        execution Request v0.1 (JSON)
                    |
                    v
         cmd/runway controller process
          |          |            |
          |          |            +--> durable run directory
          |          |                 request.json
          |          |                 events.ndjson
          |          |                 result.json
          |          |                 artifacts/
          |          |                 logs/
          |          |                 private/backend.json
          |          |
          v          v
       local       rooms          later, only after gate
      backend      backend        cloud/remote adapters
          |          |
          +---- explicit lifecycle events ----+
```

### Repository layout

```text
contracts/
  execution.go
  execution_conformance_test.go
  schema/execution-request-v0.1.0.json
  schema/execution-event-v0.1.0.json
  schema/execution-result-v0.1.0.json

cmd/runway/
  main.go
  README.md
  docs/DESIGN.md
  internal/
    controller/
    journal/
    state/
    backend/
      backend.go
      local/
      rooms/
```

`contracts` owns vocabulary only. `cmd/runway/internal/controller` owns lifecycle policy: validation, phase transitions, deadline, cancellation, terminal truth, and cleanup ordering. Backend packages own process/Rooms mechanisms and translate backend signals into controller observations. No other Workbench command imports Runway internals.

### What is reused

- Workbench's leaf contracts package and schema/type conformance pattern.
- Workbench's append-only artifact envelope where useful for event integrity.
- Rooms' `result.json`, logs, optional event file, artifact collection, pool-full signal, timeout, and cleanup behavior.
- Ship's provider-neutral agent compilation and event projection above this boundary.
- Cortex's useful lesson that compute lifecycle and provider protocol are orthogonal.

## 4. Key decisions & trade-offs

### D1 - Workbench feature, not standalone project

**Choice:** execution schemas live in `contracts`; the executable lives at `cmd/runway`. This is a new infra primitive and pure mechanism, which Workbench explicitly accepts by default.

**Alternative:** create a standalone Runway repository now. Rejected: there is no independent consumer requiring a Go module/version, no separate release cadence, and no proven executable yet. Extract only when an outside Go consumer needs to pin it or Runway develops an independent public future.

### D2 - Generic command contract, not agent contract

**Choice:** Runway executes a command in a declared workspace with explicit inputs, secrets, outputs, placement, and policy. Ship remains responsible for turning model, prompt, MCP servers, and agent definitions into those mechanics.

**Alternative:** put `model`, `prompt`, `agents`, and provider runtime fields in `WorkSpec`. Rejected: this would make Runway another agent framework and prevent browsers, tests, code interpreters, and non-LLM tools from using the same primitive.

### D3 - Foreground controller before detached submission

**Choice:** v0 implements `runway run`, which owns one run until terminal. Other processes can call `watch`, `cancel`, and `result` through durable state. Callers may background `runway run`, but Runway does not create a resident service or detached supervisor in the pre-gate phases.

**Alternative:** start with `submit` backed by a daemon or self-detached controller. Rejected: daemon lifecycle, upgrades, orphan adoption, and authentication would dominate the contract proof. If callers repeatedly need reliable detached submission after Phase 2, design it from evidence.

### D4 - Caller chooses placement

**Choice:** `placement.backend` is explicit in the request. Runway validates capabilities and executes; it does not decide whether work deserves local, Rooms, or cloud compute.

**Alternative:** Runway routes by cost, risk, or resource availability. Rejected: those are caller policy and would turn the mechanism into an orchestrator.

### D5 - Exact submitted bytes are hashed

**Choice:** `request_sha256` hashes the exact accepted request bytes. Input and image digests are independently recorded. Semantic idempotency is not inferred from the request digest.

**Alternative:** mandate cross-language canonical JSON. Rejected for v0: RFC 8785 implementations add complexity and likely dependencies before any consumer needs semantic deduplication.

### D6 - One lifecycle stream, backend details as receipts

**Choice:** Runway emits canonical phases while preserving backend-specific allocation details in a non-authoritative receipt object. Consumers may display backend details but cannot use them to infer generic state.

**Alternative:** expose Rooms logs and Cursor events as two peer streams. Rejected: the experiment showed that callers then cannot distinguish `InstanceStart` from guest readiness without backend knowledge.

### D7 - Result truth is stricter than workload exit

**Choice:** a workload exit code of zero is not sufficient for overall success. Required-output collection or cleanup failure produces terminal `failed` with phase `collection` or `cleanup`, while preserving the workload exit code.

**Alternative:** report success and attach cleanup warnings. Rejected: leaked VM/tap/secret state is not successful execution.

### D8 - Secret references only

**Choice:** the request carries `{name, ref}`. The backend resolves `ref` at the latest safe boundary and injects the value without writing it to argv or durable state. v0 defines `env:NAME` as the first resolver convention but treats the reference string as opaque contract data.

**Alternative:** inline environment values with a `secret: true` flag. Rejected: redaction is not containment.

### D9 - No automatic retry in Runway

**Choice:** each invocation is one attempt and produces one receipt. Admission backpressure and failures are returned as values. Callers decide whether to retry with a new run ID.

**Alternative:** retry transient failures internally. Rejected: hidden retries distort cost, side effects, and provenance, and require workload idempotency policy Runway does not own.

### D10 - Snapshots are a placement capability, not v0 state

**Choice:** Firecracker snapshots and copy-on-write forks remain out of the initial schemas. A later version may add a content-addressed `resume_from` or `fork_from` input after a real replay/branching experiment.

**Alternative:** model snapshots now because Rooms can eventually support them. Rejected: no snapshot implementation or compatibility contract exists yet.

## 5. Data model

### Request v0.1

```json
{
  "schema_version": "0.1.0",
  "request_id": "req_opaque",
  "work": {
    "command": ["node", "/runway/inputs/runner.js"],
    "cwd": "workspace",
    "workspace": {
      "kind": "git",
      "url": "https://github.com/itsHabib/agent-sandbox",
      "revision": "57aa8b2c7a9531d5d6ba060a77247f9bfca0470f"
    },
    "inputs": [
      {
        "name": "runner",
        "source": "bundle/runner.js",
        "target": "inputs/runner.js",
        "sha256": "hex"
      },
      {
        "name": "task",
        "source": "bundle/task.md",
        "target": "inputs/task.md",
        "sha256": "hex"
      }
    ],
    "secrets": [
      {"name": "CURSOR_API_KEY", "ref": "env:CURSOR_API_KEY"}
    ],
    "outputs": [
      {"name": "result", "path": "out/result.json", "required": true},
      {"name": "events", "path": "out/events.ndjson", "required": false}
    ]
  },
  "placement": {
    "backend": "rooms",
    "image_ref": "agent-alpine-cursor",
    "image_sha256": "16ce6e5dca8ebbc60b08bd5a2ef0d805f459c93c37158eb6ee06ec11304719b6",
    "network": "egress",
    "resources": {"cpu": 1, "memory_mib": 512}
  },
  "policy": {
    "deadline_ms": 300000,
    "cancel_grace_ms": 5000
  }
}
```

Rules:

- `request_id` is caller correlation, not run identity and not an idempotency key.
- `cwd`, input sources/targets, and output paths are logical-root-relative and traversal-safe. `source` resolves beneath the submitted request-bundle root; it is never an arbitrary controller path.
- Backends expose the same runtime roots (`/runway/workspace`, `/runway/inputs`, and `/runway/out`) and set `RUNWAY_WORKSPACE`, `RUNWAY_INPUTS`, and `RUNWAY_OUT`. These are contract paths inside the workload, not host or allocation identity.
- `workspace.revision` must be immutable for `git` workspaces; symbolic refs are rejected by the portable contract.
- A backend may reject unsupported placement fields but may not silently ignore a requested security/resource constraint.
- `image_ref` is a logical name resolved by backend-local configuration; `image_sha256` records the required immutable identity. Neither field reveals the backing host path.

### Run event v0.1

```json
{
  "schema_version": "0.1.0",
  "run_id": "run_opaque",
  "seq": 6,
  "time": "2026-07-10T16:40:13.585Z",
  "phase": "startup",
  "kind": "workload_ready",
  "message": "guest transport ready",
  "details": {}
}
```

Canonical phases are `admission`, `preparation`, `startup`, `workload`, `collection`, `cleanup`, and `terminal`. Kinds are additive within a major schema; required v0 kinds are:

- `run_accepted`
- `placement_allocated`
- `workload_ready`
- `workload_started`
- `workload_output`
- `workload_exited`
- `artifact_collected`
- `cleanup_completed`
- `run_terminal`

Backends need not emit every informational kind, but phase ordering cannot move backward. `run_terminal` is always last.

### Result v0.1

```json
{
  "schema_version": "0.1.0",
  "run_id": "run_opaque",
  "request_id": "req_opaque",
  "request_sha256": "hex",
  "status": "succeeded",
  "terminal_phase": "terminal",
  "reason_code": "completed",
  "started_at": "2026-07-10T16:40:10.268Z",
  "ended_at": "2026-07-10T16:40:47.089Z",
  "workload_exit_code": 0,
  "placement": {
    "backend": "rooms",
    "allocation_id": "01kx6ed7rze1sba5fm5sqy3rvz",
    "image_sha256": "hex",
    "details": {"slot": 2}
  },
  "artifacts": [
    {
      "name": "result",
      "path": "artifacts/result.json",
      "sha256": "hex",
      "size": 412
    }
  ]
}
```

Terminal statuses are `succeeded`, `failed`, `timed_out`, and `cancelled`. Stable reason codes include:

- `completed`
- `admission_unavailable`
- `preparation_failed`
- `startup_failed`
- `workload_failed`
- `deadline_exceeded`
- `cancel_requested`
- `collection_failed`
- `cleanup_failed`
- `controller_lost`

Human-readable messages are diagnostic only. Callers branch on status, phase, and reason code.

### Run directory

```text
<state>/runs/<run-id>/
  request.json              exact accepted bytes
  events.ndjson             append-only canonical events
  result.json               atomic terminal singleton
  logs/
    controller.log
    stdout.log
    stderr.log
  artifacts/
  private/
    controller.json         PID + process-start identity, mode 0600
    backend.json            opaque backend handle, mode 0600
```

The public directory is shareable after terminal completion. `private/` is host-local and excluded from exported run bundles.

## 6. API contract

### CLI v0

```text
runway run --spec <request.json> [--state <dir>] [--json]
runway watch <run-id> [--after <seq>] [--follow] [--json]
runway cancel <run-id> [--json]
runway result <run-id> [--wait] [--json]
runway reconcile <run-id> [--json]
```

- `run` validates the request, allocates a run ID, writes `request.json`, records `run_accepted`, and blocks until terminal.
- `watch` reads durable events and never attaches to backend stdout directly.
- `cancel` verifies controller PID plus process-start identity before signaling it. Repetition is a successful no-op once cancellation or terminal state is recorded.
- `result` returns the atomic terminal receipt. With `--wait`, it watches durable state rather than polling a backend.
- `reconcile` detects a dead/reused controller identity. It writes `controller_lost` only after proving no active writer remains, then invokes backend-specific best-effort cleanup.

Stdout is machine output only under `--json`; diagnostics go to stderr. The result schema is authoritative over process exit codes.

Suggested exit codes:

| Code | Meaning |
| ---: | --- |
| 0 | terminal success or successful read/cancel no-op |
| 2 | invalid request/CLI usage; no run admitted |
| 3 | terminal failed |
| 4 | admission unavailable/backpressure |
| 124 | timed out |
| 130 | cancelled |

### Internal backend seam

```go
type Backend interface {
    Start(context.Context, PreparedRun, Emit) (Handle, error)
    Wait(context.Context, Handle, Emit) (Exit, error)
    Cancel(context.Context, Handle) error
    Collect(context.Context, Handle, string) ([]Artifact, error)
    Cleanup(context.Context, Handle) error
}
```

`Handle` is opaque outside its backend package. `Emit` proposes observations to the controller; only the controller assigns canonical sequence numbers and writes events. Backend errors are mechanism values mapped by controller policy into stable phase/reason codes.

The local backend starts one process group with explicit cwd/env and captures stdout/stderr. The Rooms backend shells out to the existing Rooms CLI in Phase 2; it does not import Rooms code or duplicate Firecracker lifecycle logic.

## 7. Key flows

### Flow A - successful run

1. Validate schema, paths, immutable revision, supported placement, and secret references without resolving secret values.
2. Mint run ID; create the run directory with restrictive permissions.
3. Persist exact request bytes and emit `run_accepted`.
4. Resolve/materialize workspace and declared inputs; verify digests.
5. Resolve secrets at backend start and keep values in process memory only.
6. Start backend; record placement allocation.
7. Record workload readiness separately from backend process/VM start.
8. Run until exit, cancellation, or deadline.
9. Collect declared outputs and verify required files/digests.
10. Cleanup backend allocation.
11. Atomically write `result.json`; append `run_terminal` as the final event. If the controller dies between those writes, `reconcile` treats the immutable result as authoritative and appends only the missing terminal event.

### Flow B - admission backpressure

1. Request is schema-valid, so a run ID and durable directory exist.
2. Rooms returns `pool_full` or another explicit admission refusal.
3. Controller emits phase `admission`, writes status `failed`, reason `admission_unavailable`, and exits 4.
4. Runway does not retry. The caller decides whether and when to create another run.

### Flow C - deadline

1. Controller starts one absolute timer before backend preparation.
2. At expiry, controller records `deadline_exceeded` intent before signaling cancellation.
3. Backend receives cancel; after grace, controller escalates termination.
4. Partial events and outputs are collected best-effort.
5. Cleanup runs even if collection fails.
6. Terminal status remains `timed_out`; collection/cleanup failures are attached diagnostics unless cleanup cannot prove isolation, in which case overall status becomes `failed`, phase `cleanup`, while preserving `deadline_exceeded` as prior cause.

### Flow D - user cancellation race

1. `cancel` verifies the recorded controller identity and writes a cancel request atomically.
2. If workload exit already won and terminal receipt exists, cancel returns a terminal no-op.
3. Otherwise controller records cancel intent and calls backend cancel once.
4. Exactly one of normal completion or cancellation writes the terminal receipt under an exclusive terminal transition.
5. Repeated cancellation never changes an existing terminal result.

### Flow E - required artifact missing

1. Workload exits zero.
2. Collection cannot find a required output.
3. Controller records `collection_failed`; cleanup still runs.
4. Terminal status is `failed`, phase `collection`, reason `collection_failed`, with workload exit code retained as zero.

### Flow F - controller loss

1. `watch` observes no result and no new events but makes no liveness claim.
2. `reconcile` verifies controller PID/start identity is absent or reused.
3. Backend adapter probes only what it can prove and performs best-effort cleanup.
4. With no active writer, reconcile appends `controller_lost` and atomically writes a failed terminal receipt.
5. Uncertain backend liveness fails closed and names the remaining allocation in diagnostics; it is never reported clean.

## 8. Concurrency, consistency, and failure model

### One writer per run

The foreground controller is the sole canonical event/result writer. `watch` is read-only. `cancel` writes only a cancellation request marker and signals the verified controller. `reconcile` may become writer only after proving the original controller identity is dead.

### Event guarantees

- Durable file order defines canonical order.
- `seq` is contiguous and unique within a run.
- Readers may reconnect with `--after N`; replay is at-least-once at the transport level but deduplicable by `(run_id, seq)`.
- Phase order is monotone. Backend observations arriving late are retained as diagnostics but cannot regress canonical phase.
- After normal completion or reconciliation, `run_terminal` occurs exactly once and is the final canonical event.

### Result guarantees

- `result.json` is created via temp file, flush, and atomic rename.
- Existing terminal result is immutable.
- A result without `run_terminal` is the only repairable partial terminal state. Reconciliation may append that event from the immutable result but may not rewrite the result or append any other event.
- A success result requires workload success, every required artifact, and proven cleanup.
- Backend uncertainty is failure, not success with a warning.

### Retry model

Runway never retries work. The caller may issue a new request with a new run ID and link attempts in its own workflow state. This keeps cost, side effects, and provenance visible.

### Crash model

v0 does not promise automatic adoption after host/process restart. It promises truthful detection via process-start identity and `reconcile`, preservation of all flushed events, and no false terminal success. Automatic per-run supervisors or a daemon are post-gate designs.

## 9. Rollout / implementation plan

| Phase | Goal | High-level tasks | Depends on | Scope | Gate |
| --- | --- | --- | --- | --- | --- |
| 0 - Contract | Freeze the portable vocabulary before executable code. | Add three JSON Schemas, Go types, schema/type conformance, golden valid/invalid fixtures, path/secret/state invariants. | None | 350-550 weighted LOC | Schemas reject host-path leaks, inline secrets, invalid phase/status combinations, and traversal; Go round-trips golden fixtures. |
| 1 - Local controller | Prove lifecycle truth without virtualization. | Implement run directory, journal, local backend, deadline, cancel, result, watch, reconcile, CLI docs. | Phase 0 | 900-1,300 across 2-3 PRs | Fixture commands prove success, nonzero exit, missing required output, timeout, cancel race, controller loss, one terminal result, zero orphan process groups. |
| 2 - Rooms adapter | Prove placement neutrality against the real microVM substrate. | Translate Request to Rooms CLI, map pool-full/readiness/artifacts/cleanup, preserve backend receipt, add live gated tests. | Phase 1; Rooms PR #67 | 500-800 across 1-2 PRs | **VALIDATION GATE:** the same two fixture requests run local and Rooms with equivalent statuses/outputs/phase laws; N=2 concurrent Rooms runs; timeout/cancel leave zero rooms, Firecracker processes, taps, or slots. |
| 3 - Ship compiler | Remove placement-specific execution assembly from Ship without changing Ship policy. | Compile AgentRunInput to Request; invoke Runway process surface; map RunEvents/Result to AgentRunHandle/Result; keep feature flag/fallback. | Phase 2 GO | Cross-repo, 700-1,100 | One local and one Rooms Ship run produce existing Ship terminal semantics and branch artifacts; old direct adapter remains rollback path until parity corpus passes. |
| 4 - Explicit environment bundles | Make hooks/skills/agent definitions portable inputs. | Define content-addressed bundle convention, materializer, digest receipt, fixture for workbench hooks. | Phase 2 GO; demand from Phase 3 | 450-700 | Same bundle digest materializes locally and in Rooms; missing/mutated input fails before workload start. |
| 5 - Earned extensions | Add only capabilities pulled by evidence. | Consider detached submit, remote artifact URIs, cloud adapter, snapshot/fork refs, or extraction to its own module/repo. | Measured demand | Unscoped | Separate TDD per capability. |

Phases 0-2 are the thesis test. Phase 3 and later are not committed until the Rooms validation gate passes. Phase 0 should split schemas/types from state-machine property tests if it crosses one reviewable PR.

## 10. Open questions

1. **CLI name:** `runway` is the current recommendation. Does it fit the Workbench vocabulary, or should the executable be the literal `spawn`/`exec`?
2. **Envelope reuse:** should `RunEvent` be the body of Workbench's hash-chained `Envelope`, or remain a standalone line with `run_id`/`seq`? Reusing the envelope adds integrity and parent links but also verdict-era fields that may not help execution.
3. **Network policy vocabulary:** is `none | egress` sufficient for the Rooms gate, or must the first schema distinguish allowlisted egress and host-service access?
4. **Resource constraints:** should an unsupported CPU/memory constraint reject admission in v0, or may local placement report it as unenforced? The recommended answer is reject any requested constraint the backend cannot prove.
5. **Controller loss on Windows:** which process-start identity and process-group mechanism provide the same PID-reuse safety as Linux `/proc/<pid>/stat` without adding a service?
6. **Rooms readiness:** should Phase 2 consume new structured Rooms lifecycle events, or infer readiness from current stderr plus SSH acceptance? The contract should not force Rooms to expose an API it has not earned, but parsing human logs is not acceptable production behavior.
7. **Schema publication:** should TypeScript and Rust consumers vendor the schema at a pinned commit, fetch it in CI, or consume a release artifact? Direct cross-repo source imports are out.

## 11. Validation plan

### Gate A - contract laws (Phase 0)

Golden fixtures and generated cases prove:

- accepted requests contain no absolute cwd/output/target paths;
- bundle input sources and targets cannot be absolute or traverse logical roots;
- image references remain logical and contain no controller or backend host path;
- secret values cannot be represented, only references;
- a request digest changes when any exact submitted byte changes;
- event sequence is contiguous and phase-monotone;
- one run has at most one terminal event/result;
- terminal status/reason/phase combinations are valid;
- unknown additive fields decode, unknown major versions reject;
- schema and Go types remain structurally identical.

### Gate B - local lifecycle (Phase 1)

A deterministic fixture suite runs commands that:

- succeed with required outputs;
- exit nonzero;
- exit zero without a required output;
- exceed deadline while continuously emitting output;
- race normal completion against cancellation;
- ignore graceful termination and require escalation;
- lose the controller and reconcile without PID-reuse mistakes.

PASS requires truthful terminal receipts, contiguous event histories, no secret values in a recursive run-directory scan, and no orphan process groups.

### Gate C - placement equivalence (Phase 2, program gate)

Run the same success and timeout requests through `local` and `rooms`:

1. statuses, reason codes, declared artifact names/digests, and phase order are equivalent;
2. placement receipts differ only in backend-specific details;
3. two Rooms requests overlap on distinct slots and complete independently;
4. pool-full returns admission backpressure without a hidden retry;
5. cancellation and timeout preserve partial events/artifacts;
6. every terminal path leaves `rooms ls` empty and zero Firecracker/tap/slot residue;
7. no request, event, result, log, process argv, or exported bundle contains resolved secret values.

**GO:** all seven pass on a fresh `rooms-host` rebuild, and neither backend requires a caller to branch on backend-specific text.

**NO-GO / reshape:** if equivalent semantics require Runway to understand Cursor, Ship, Firecracker slots, or provider events, keep the shared schemas smaller and leave orchestration in the existing adapters. If foreground control makes cancellation/recovery unusable, design a per-run supervisor only after documenting the exact failed flow.

## 12. Dossier seeding handoff

The existing Dossier connector is not exposed in this Codex session. Do not hand-edit the corpus as a substitute. A Dossier-capable follow-up should:

1. resolve the existing `workbench` project;
2. add five phase stubs mirroring §9;
3. materialize tasks only for Phases 0-2, through the validation gate;
4. link this file as a `doc` artifact and the design PR as a `pr` artifact;
5. leave Phases 3-5 task-less until Gate C passes.

Recommended task tiers: Phase 0 schema/conformance tasks `sonnet/extra`; Phase 1 state machine, cancellation, and controller-loss work `opus/max`; Phase 2 Rooms adapter `opus/extra` with an `ultracode` adversarial validation pass.
