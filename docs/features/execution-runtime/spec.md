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

> one explicit work bundle, one placement binding, one ordered lifecycle, and one terminal receipt.

**Hypothesis:** if a caller can compile domain intent into one portable, versioned work bundle and bind it to either a local-process or Rooms placement while receiving the same lifecycle/result semantics, then Ship and future tools can stop owning placement-specific lifecycle code. Placement choice and profile remain caller policy; backend invocation, observation, cancellation, collection, and cleanup become Runway mechanism. Phase 2 proves that the contract can span both backends. Only Phase 3, where Ship deletes its direct placement lifecycle assembly, can close the portfolio thesis.

### Goals

- Define a language-neutral request, event, result, artifact, and error vocabulary under `workbench/contracts`.
- Add a focused `runway` executable inside Workbench.
- Make placement an explicit caller choice, not hidden routing policy.
- Make workspace, inputs, hooks, secrets, outputs, deadline, and cleanup policy explicit.
- Distinguish controller start, backend allocation, workload readiness, workload execution, collection, cleanup, and terminal completion.
- Produce hashable run directories that separate shareable Runway-authored metadata from workload-owned inputs/logs/artifacts whose sensitivity remains explicit.
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

- **FR1 - Explicit request:** a run request contains a portable work specification, execution policy, and a separate placement binding.
- **FR2 - Provider neutrality:** the contract contains no Cursor, Claude, Codex, model, prompt, MCP, or subagent fields. An agent system compiles those into command, inputs, and secret references.
- **FR3 - Logical paths:** cwd, argv path values, inputs, and outputs use structured references to named logical roots. The selected backend expands them to native paths.
- **FR4 - Explicit materialization:** hooks, skills, runner scripts, task files, and agent definitions enter a run only as declared inputs or as content baked into the selected image.
- **FR5 - Placement receipt:** the result records what backend profile and immutable inputs actually ran, including request/work digests, workspace revision, image digest when applicable, enforced constraints, and allocation identity.
- **FR6 - Ordered lifecycle:** every durable event has a run ID and strictly increasing sequence number. A watcher can resume after a sequence without parsing logs.
- **FR7 - One terminal receipt:** every schema-valid accepted request produces at most one terminal `result.json`. Malformed requests fail before admission and produce no run.
- **FR8 - Distinct failure phase:** admission, preparation, startup, workload, collection, cleanup, timeout, cancellation, and controller-loss failures are distinguishable without message matching.
- **FR9 - Deadline enforcement:** while the foreground controller is alive and responsive, it enforces one absolute work deadline independently of an agent SDK's own timeout.
- **FR10 - Cancellation:** cancellation is idempotent, records operator intent separately from timeout, and preserves partial events/artifacts.
- **FR11 - Durable observation:** `watch` and `result` read the run directory, not controller memory. Stdout is never the only copy of an event or result.
- **FR12 - Secret safety:** requests contain opaque secret references, never secret values. Events, diagnostics, receipts, and backend descriptors never contain resolved values.
- **FR13 - Backend isolation:** backend implementations are private to `cmd/runway`; callers consume contracts and process/artifact surfaces, not backend call stacks.
- **FR14 - Tolerant readers:** additive fields do not break older readers of the same major schema. Unknown major versions fail loudly.
- **FR15 - Portable references:** work specs name bundle inputs by logical source plus digest. Placement bindings name backend-local profiles; backend configuration maps profiles to images, network policy, resources, and host paths.

### Non-functional requirements

| Property | Target |
| --- | --- |
| Simplicity | Phase 0-2 add no daemon, queue, database, network listener, or third-party Go dependency. |
| Durability | Request and canonical lifecycle events are flushed before the next externally visible transition; `result.json` is written atomically. Stdout/stderr are buffered byte streams, not canonical events. |
| Ordering | One active controller is the sole event writer. Sequence numbers are contiguous from 1 through terminal. |
| Cancellation | A cancel request reaches a live controller within 1 second locally; the terminal receipt appears within configured grace plus 5 seconds. |
| Timeout | A healthy controller cannot leave a deadline overrun `running`; it terminates as `timed_out` even if the workload remains noisy. Controller loss requires explicit reconciliation. |
| Security | No resolved secret value is written to argv, request, events, result, backend state, or diagnostics. Backend-private state is mode `0600`. |
| Portability | The same work digest and policy run through local and Rooms placement bindings with the same terminal status, declared outputs, and phase vocabulary. |
| Operability | A run is diagnosable from its directory after the controller and backend processes exit. |
| Compatibility | JSON Schema draft 2020-12 defines wire shape; Go admission validation defines semantic laws. Conformance tests bind schema and Go types. TypeScript/Rust consumers use the schema plus published semantic fixtures, not Go imports. |
| Scope | Each implementation PR stays below roughly 700 weighted LOC; a larger slice needs a no-split argument. |

## 3. Architecture overview

```text
 Ship / browser tool / test harness / operator
                    |
          compile domain intent
                    |
                    v
      work bundle + placed Request v0.1
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
contracts/execution/
  execution.go
  execution_conformance_test.go
  schema/work-spec-v0.1.0.json
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

`contracts/execution` owns vocabulary only. `cmd/runway/internal/controller` owns lifecycle policy: validation, phase transitions, deadline, cancellation, terminal truth, and cleanup ordering. Backend packages own process/Rooms mechanisms and translate backend signals into controller observations. No other Workbench command imports Runway internals.

### What is reused

- Workbench's leaf contracts package and schema/type conformance pattern.
- Workbench's append-only artifact envelope where useful for event integrity.
- Rooms' `result.json`, logs, optional event file, artifact collection, pool-full signal, timeout, and cleanup behavior.
- Ship's provider-neutral agent compilation and event projection above this boundary.
- Cortex's useful lesson that compute lifecycle and provider protocol are orthogonal.

## 4. Key decisions & trade-offs

### D1 - Workbench feature, not standalone project

**Choice:** execution schemas live in their own `contracts/execution` domain; the executable lives at `cmd/runway`. This is a new infra primitive and pure mechanism, which Workbench explicitly accepts by default. Nothing else in Workbench composes with Runway yet; Workbench is its home for shared mechanism and contract conventions, not runtime composition.

**Alternative:** create a standalone Runway repository now. Rejected: there is no independent consumer requiring a Go module/version, no separate release cadence, and no proven executable yet. Extract when a non-Workbench implementation needs to version the execution contract independently, an outside Go consumer needs to pin it, or Runway develops an independent public future. Keeping execution schemas partitioned avoids coupling their release surface to verdict contracts in the meantime.

### D2 - Generic command contract, not agent contract

**Choice:** Runway executes a command in a declared workspace with explicit inputs, secrets, outputs, placement, and policy. Ship remains responsible for turning model, prompt, MCP servers, and agent definitions into those mechanics.

**Alternative:** put `model`, `prompt`, `agents`, and provider runtime fields in `WorkSpec`. Rejected: this would make Runway another agent framework and prevent browsers, tests, code interpreters, and non-LLM tools from using the same primitive.

### D3 - Foreground controller before detached submission

**Choice:** v0 implements `runway run`, which owns one run until terminal. Other processes can call `watch`, `cancel`, and `result` through durable state. Reliable detached execution is unsupported: backgrounding the process does not create adoption, host-restart, or orphan-cleanup guarantees. `reconcile` is an explicit operator/caller recovery action for one known run ID; v0 never scans for abandoned runs.

**Alternative:** start with `submit` backed by a daemon or self-detached controller. Rejected: daemon lifecycle, upgrades, orphan adoption, and authentication would dominate the contract proof. If callers repeatedly need reliable detached submission after Phase 2, design it from evidence.

### D4 - Caller chooses placement

**Choice:** `placement.backend` and a logical `placement.profile` are explicit in the request but separate from portable `work` and `policy`. Runway resolves the profile from backend-local configuration, validates it, and executes; it does not decide whether work deserves local, Rooms, or cloud compute. A backend name is an open string resolved against configured adapters, not a schema enum.

**Alternative:** Runway routes by cost, risk, or resource availability. Rejected: those are caller policy and would turn the mechanism into an orchestrator.

### D5 - Exact submitted bytes are hashed

**Choice:** `request_sha256` hashes the exact accepted request bytes; `work_sha256` hashes the exact submitted `work.json` bytes. Input and resolved placement/image digests are independently recorded. Cross-placement equivalence compares `work_sha256` plus policy fields, not whole-request identity. Semantic idempotency is not inferred from either digest.

**Alternative:** mandate cross-language canonical JSON. Rejected for v0: RFC 8785 implementations add complexity and likely dependencies before any consumer needs semantic deduplication.

### D6 - One lifecycle stream, backend details as receipts

**Choice:** Runway emits canonical lifecycle phases while preserving backend-specific allocation details in a non-authoritative receipt object. Consumers may display backend details but cannot use them to infer generic state. Workload stdout/stderr and provider-origin events are data streams, not lifecycle events; they do not share the fsync-per-transition guarantee.

**Alternative:** expose Rooms logs and Cursor events as two peer streams. Rejected: the experiment showed that callers then cannot distinguish `InstanceStart` from guest readiness without backend knowledge.

### D7 - Result truth is stricter than workload exit

**Choice:** a workload exit code of zero is not sufficient for overall success. Required-output collection or cleanup failure produces terminal `failed` with phase `collection` or `cleanup`, while preserving the workload exit code.

**Alternative:** report success and attach cleanup warnings. Rejected: leaked VM/tap/secret state is not successful execution.

### D8 - Secret references only

**Choice:** the request carries `{name, ref}`. The controller resolves `ref` at the latest safe boundary and passes the value to a backend-supported in-memory channel without writing it to argv or durable state. v0 accepts only `env:NAME`, enforced in JSON Schema with `^env:[A-Za-z_][A-Za-z0-9_]*$` and repeated by Go admission validation. Each placement profile declares allowed secret names and transport capability; admission rejects unsupported names. The initial Rooms profile supports only the existing SSH `SendEnv` allowlist (`CURSOR_API_KEY` and `ANTHROPIC_API_KEY`); generic guest secret transport is out of scope.

Captured stdout/stderr replace exact resolved secret byte sequences with `[REDACTED]` before persistence, including matches split across read chunks. This protects against accidental direct echo; it is not a claim that Runway can prevent a workload from transforming or exfiltrating a secret it is authorized to receive.

Collected workload artifacts remain exact bytes so their digests are truthful; Runway does not redact them. A workload authorized to receive a secret can therefore make its run directory sensitive by writing that secret into an artifact. Such a run must not be exported as shareable merely because Runway-authored metadata is clean.

**Alternative:** inline environment values with a `secret: true` flag. Rejected: redaction is not containment.

### D9 - No automatic retry in Runway

**Choice:** each invocation is one attempt and produces one receipt. Admission backpressure and failures are returned as values. Callers decide whether to retry with a new run ID.

**Alternative:** retry transient failures internally. Rejected: hidden retries distort cost, side effects, and provenance, and require workload idempotency policy Runway does not own.

### D10 - Snapshots are a placement capability, not v0 state

**Choice:** Firecracker snapshots and copy-on-write forks remain out of the initial schemas. A later version may add a content-addressed `resume_from` or `fork_from` input after a real replay/branching experiment.

**Alternative:** model snapshots now because Rooms can eventually support them. Rejected: no snapshot implementation or compatibility contract exists yet.

### D11 - Lifecycle journal and workload streams are distinct

**Choice:** `events.ndjson` contains low-volume canonical lifecycle transitions only. `stdout.log` and `stderr.log` are ordered, buffered byte records that `runway logs --follow` may tail with bounded delivery latency while the controller is healthy; that bound includes the rolling tail needed for cross-chunk secret redaction. Provider event files remain declared workload artifacts. v0 records only `terminal_replay` or `none`; `live` is reserved until a separate design defines where and how callers consume a live workload stream.

**Alternative:** journal every output line as `workload_output` with a durable flush. Rejected: agent streams can emit hundreds of events and would turn lifecycle correctness into an fsync-per-line bottleneck. Phase 3 must either tolerate the Rooms backend's existing terminal replay semantics or add a separate Rooms streaming capability before removing Ship's direct local runner.

## 5. Data model

### Portable work spec v0.1

```json
{
  "schema_version": "0.1.0",
  "command": {
    "executable": {"name": "node"},
    "args": [
      {"path": {"root": "inputs", "value": "runner.js"}},
      {"path": {"root": "inputs", "value": "task.md"}}
    ]
  },
  "cwd": {"root": "workspace", "value": "."},
  "workspace": {
    "kind": "git",
    "url": "https://github.com/itsHabib/agent-sandbox",
    "revision": "57aa8b2c7a9531d5d6ba060a77247f9bfca0470f"
  },
  "inputs": [
    {"name": "runner", "source": "runner.js", "target": "runner.js", "sha256": "hex"},
    {"name": "task", "source": "task.md", "target": "task.md", "sha256": "hex"}
  ],
  "secrets": [
    {"name": "CURSOR_API_KEY", "ref": "env:CURSOR_API_KEY"}
  ],
  "outputs": [
    {"name": "result", "path": "result.json", "required": true},
    {"name": "agent_events", "path": "events.ndjson", "required": false}
  ]
}
```

`work.json` and every `inputs[].source` are files beneath the submitted bundle root. `command.executable` is either `{name}` for placement-PATH lookup or a structured `{path}` reference; `command.args` is an ordered union of `{literal: string}` and `{path: {root, value}}`. Runway never invokes a shell or performs string interpolation. Roots are `workspace`, `inputs`, and `out`. The backend expands structured executable/path arguments and cwd to native Windows or Linux paths immediately before process start.

Every backend also sets `RUNWAY_WORKSPACE`, `RUNWAY_INPUTS`, and `RUNWAY_OUT` to those expanded native roots. Workloads may use these environment variables for root discovery; structured path arguments remain the preferred way for callers to pass known file operands.

### Placed run request v0.1

```json
{
  "schema_version": "0.1.0",
  "request_id": "req_opaque",
  "work": {
    "manifest": "work.json",
    "sha256": "hex"
  },
  "placement": {
    "backend": "rooms",
    "profile": "agent-cursor"
  },
  "policy": {
    "deadline_ms": 300000,
    "cancel_grace_ms": 5000
  }
}
```

Rules:

- `request_id` is caller correlation, not run identity and not an idempotency key.
- `work.manifest` and input sources resolve beneath `--bundle`; they are never arbitrary controller paths. Their declared digests are verified before admission completes.
- Cwd and argv path values are structured logical references. Input targets and output paths are relative to their fixed `inputs` and `out` roots. Traversal and absolute paths are rejected.
- A named executable resolves through the placement profile's PATH, never an arbitrary host path. A workload that requires a custom executable declares it as an input and uses a structured executable path.
- `workspace.revision` must be immutable for `git` workspaces; symbolic refs are rejected by the portable contract.
- `placement.backend` is open vocabulary resolved against installed adapters. `placement.profile` is backend-local configuration, not portable work identity.
- Profiles pin enforceable network/resource/image policy and allowed secret names. Their resolved values appear in the placement receipt; callers cannot override individual constraints in v0.
- The equivalent local binding is `{"backend":"local","profile":"default"}`. A local profile has no image identity and records that field as absent; it never pretends to enforce Rooms isolation or resource controls.

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
- `workload_exited`
- `artifact_collected`
- `cleanup_completed`
- `run_terminal`

Backends need not emit every informational kind, but phase ordering cannot move backward. `run_terminal` is always last. Output bytes never become canonical lifecycle events merely because they contain newlines; they are written to `logs/` and observed with `runway logs`.

### Result v0.1

```json
{
  "schema_version": "0.1.0",
  "run_id": "run_opaque",
  "request_id": "req_opaque",
  "request_sha256": "hex",
  "work_sha256": "hex",
  "status": "succeeded",
  "terminal_phase": "terminal",
  "reason_code": "completed",
  "started_at": "2026-07-10T16:40:10.268Z",
  "ended_at": "2026-07-10T16:40:47.089Z",
  "workload_exit_code": 0,
  "placement": {
    "backend": "rooms",
    "profile": "agent-cursor",
    "allocation_id": "01kx6ed7rze1sba5fm5sqy3rvz",
    "image_sha256": "hex",
    "stream_delivery": "terminal_replay",
    "enforced": {
      "network": "egress",
      "cpu": 1,
      "memory_mib": 512
    },
    "details": {"slot": 2}
  },
  "causes": [],
  "diagnostics": [],
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
- `preparation_failed`
- `startup_failed`
- `workload_failed`
- `deadline_exceeded`
- `cancel_requested`
- `collection_failed`
- `cleanup_failed`
- `controller_lost`
- `placement_unavailable`

`causes` is an ordered array of prior `{phase, reason_code, message?}` values when a later failure becomes primary, such as cleanup failure after deadline expiry. `diagnostics` is an array of structured `{code, message, details?}` records and may name an uncertain allocation without changing primary status. In v0, `stream_delivery` is `terminal_replay` or `none` for declared provider/workload event artifacts; reserved value `live` cannot be emitted until a live-stream access contract exists. It does not describe stdout/stderr log tailing. Neither field may contain resolved secret values. Human-readable messages are diagnostic only. Callers branch on status, phase, and reason code.

### Run directory

```text
<state>/runs/<run-id>/
  request.json              exact accepted bytes
  work.json                 exact verified portable work manifest
  inputs/                   exact verified declared bundle inputs
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

Runway-authored public metadata is shareable after terminal completion. Workload-owned `inputs/`, `logs/`, and `artifacts/` may make the directory sensitive; export tooling must treat them as untrusted content rather than infer safety from terminal status. `private/` is host-local and excluded from exported run bundles, but it remains subject to the no-secret-at-rest invariant and validation scan.

## 6. API contract

### CLI v0

```text
runway run --spec <request.json> --bundle <dir> [--state <dir>] [--json]
runway watch <run-id> [--state <dir>] [--after <seq>] [--follow] [--json]
runway logs <run-id> [--state <dir>] [--stream stdout|stderr] [--follow]
runway cancel <run-id> [--state <dir>] [--json]
runway result <run-id> [--state <dir>] [--wait --timeout <duration>] [--json]
runway reconcile <run-id> [--state <dir>] [--json]
```

- `run` validates the request and bundle, allocates a run ID, writes exact `request.json` and `work.json`, records `run_accepted`, and blocks until terminal.
- `watch` reads durable events and never attaches to backend stdout directly.
- `logs` tails buffered workload bytes. Delivery is ordered per stream but may lose the final unflushed tail on abrupt controller loss.
- `cancel` verifies controller PID plus process-start identity before signaling it. Repetition is a successful no-op once cancellation or terminal state is recorded.
- `result` returns the atomic terminal receipt. With `--wait`, `--timeout` is mandatory; it watches durable state rather than polling a backend and never reconciles implicitly.
- `reconcile` detects a dead/reused controller identity, atomically acquires the run's writer claim, then invokes backend-specific best-effort cleanup. It records `controller_lost` only while holding that claim. Concurrent reconcilers return the existing owner/result and do not touch the journal.

The state root defaults from one documented environment/config value, but every run-addressing command accepts `--state`. Reliable `nohup`, CI adoption, periodic reconciliation scans, and host-restart recovery are unsupported in v0.

Stdout is machine output only under `--json`; diagnostics go to stderr. The result schema is authoritative over process exit codes.

Suggested exit codes:

| Code | Meaning |
| ---: | --- |
| 0 | terminal success or successful read/cancel no-op |
| 2 | invalid request/CLI usage; no run admitted |
| 3 | terminal failed |
| 4 | placement unavailable/backpressure |
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

The local backend expands logical paths to native paths, starts one process group with explicit cwd/env, and captures stdout/stderr. The Rooms backend shells out to the Rooms CLI in Phase 2; it does not import Rooms code or duplicate Firecracker lifecycle logic.

Phase 2 cannot start against the current human-log-only surface. Rooms must first expose a machine-readable lifecycle stream that distinguishes allocation, VM start, guest/workload readiness, workload exit, collection, and cleanup, plus structured `pool_full`. The recommended surface is a host-side NDJSON file selected by `rooms run --lifecycle <path>`. This is an explicit Rooms prerequisite, separate from PR #67. The first Rooms placement profile may use the existing environment plus SSH `SendEnv` transport only for its allowlisted secret names; a generic secret channel is not implied.

## 7. Key flows

### Flow A - successful run

1. Validate schema shape, semantic path laws, exact bundle/work/input digests, immutable revision, placement profile, and supported secret names without resolving secret values.
2. Mint run ID; create the run directory with restrictive permissions.
3. Persist exact request bytes and emit `run_accepted`.
4. Resolve/materialize workspace and declared inputs; verify digests.
5. Expand native roots/path arguments and set `RUNWAY_WORKSPACE`, `RUNWAY_INPUTS`, and `RUNWAY_OUT`.
6. Resolve secrets at backend start and keep values in process memory only.
7. Start backend; record placement allocation.
8. Record workload readiness separately from backend process/VM start.
9. Run until exit, cancellation, or deadline.
10. Collect declared outputs and verify required files/digests without rewriting artifact bytes.
11. Cleanup backend allocation.
12. Atomically write `result.json`; append `run_terminal` as the final event. If the controller dies between those writes, `reconcile` treats the immutable result as authoritative and appends only the missing terminal event.

### Flow B - placement backpressure

1. Request is schema-valid, so a run ID and durable directory exist.
2. Work preparation may already have completed when Rooms returns structured `pool_full` from backend start.
3. Controller preserves monotone phase order: it writes status `failed`, terminal phase `startup`, reason `placement_unavailable`, and exits 4. It never emits a late `admission` event after `preparation`.
4. Runway does not retry. The caller decides whether and when to create another run.

Exit code 4 is a placement result, not a retry instruction. Callers may retry only as explicit new attempts in their own workflow state; Runway supplies no queue, sleep, retry budget, or eventual-admission guarantee.

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
3. It atomically acquires the per-run writer claim; failure means another controller/reconciler owns the run and this invocation exits without mutation.
4. Backend adapter probes only what it can prove and performs best-effort cleanup.
5. While holding the writer claim, reconcile atomically writes a failed terminal receipt with reason `controller_lost`, then appends `run_terminal`. Controller loss is a reason in the receipt/final event, not a separate pre-result event.
6. Uncertain backend liveness fails closed and names the remaining allocation in diagnostics; it is never reported clean.

## 8. Concurrency, consistency, and failure model

### One writer per run

The foreground controller holds an exclusive per-run writer claim and is the sole canonical event/result writer. `watch` is read-only. `cancel` writes only a cancellation request marker and signals the verified controller. `reconcile` may become writer only after proving the original controller identity is dead and atomically acquiring the same claim. Claim acquisition is tested under concurrent reconcilers; PID checks alone are never treated as exclusivity.

### Event guarantees

- Durable file order defines canonical order.
- `seq` is contiguous and unique within a run.
- Readers may reconnect with `--after N`; replay is at-least-once at the transport level but deduplicable by `(run_id, seq)`.
- Phase order is monotone. Backend observations arriving late are retained as diagnostics but cannot regress canonical phase.
- After normal completion or reconciliation, `run_terminal` occurs exactly once and is the final canonical event.

### Result guarantees

- `result.json` is created via temp file, flush, and atomic rename.
- Existing terminal result is immutable.
- A result without `run_terminal` is the only repairable partial terminal state. Reconciliation may append that event deterministically from the immutable result but may not rewrite the result or append any other event.
- A success result requires workload success, every required artifact, and proven cleanup.
- Backend uncertainty is failure, not success with a warning.

### Retry model

Runway never retries work. The caller may issue a new request with a new run ID and link attempts in its own workflow state. This keeps cost, side effects, and provenance visible.

### Crash model

v0 does not promise automatic adoption after host/process restart, automatic discovery of abandoned runs, or recovery from a live but wedged controller. It promises truthful detection via process-start identity and explicit `reconcile`, preservation of all flushed lifecycle events, and no false terminal success. A crash after successful workload/cleanup but before terminal rename may therefore reconcile as `controller_lost`; prior events distinguish that uncertainty from workload failure. Automatic per-run supervisors or a daemon are post-gate designs.

## 9. Rollout / implementation plan

| Phase | Goal | High-level tasks | Depends on | Scope | Gate |
| --- | --- | --- | --- | --- | --- |
| 0 - Contract | Freeze portable vocabulary before executable code. | Add four JSON Schemas, partitioned Go types, semantic admission validators, golden valid/invalid fixtures, and a pure history reducer/model test package. | None | 500-750 across 1-2 PRs | Schemas/types agree on shape; Go validation rejects path/secret/profile law violations; reducer tests prove valid transition/result combinations without claiming JSON Schema enforces history. |
| 1 - Local controller | Prove lifecycle truth without virtualization. | Implement bundle materialization, native path expansion, run directory, journal, local backend, logs, deadline, cancel, result, writer claim, watch, manual reconcile, and CLI docs. | Phase 0 | 1,100-1,600 across 3 PRs | Fixtures prove success, nonzero exit, missing output, timeout, cancel race, controller loss, concurrent reconcile, one terminal result, and zero orphan process groups. |
| 2 - Rooms adapter | Prove placement neutrality against the real microVM substrate. | First add structured lifecycle/pool-full output to Rooms; then translate the placed request to Rooms CLI, map lifecycle/artifacts/cleanup, preserve the resolved profile receipt, and add host-gated tests. | Phase 1; Rooms PR #67; Rooms structured-lifecycle prerequisite | 700-1,100 across repos | **PREREQUISITE GATE:** identical work digest/policy runs through local/default and rooms/agent-cursor with equivalent terminal semantics; run-at-capacity rejects N+1; timeout/cancel leave zero Rooms residue. |
| 3 - Ship compiler | Test the actual portfolio thesis without changing Ship policy. | Compile AgentRunInput into bundle/work/request; invoke Runway; map lifecycle/result and existing terminal event replay; keep feature flag/fallback; delete direct placement assembly only after parity. | Phase 2 GO | Cross-repo, 800-1,200 | **THESIS GATE:** local and Rooms Ship runs preserve required liveness/terminal semantics and branch artifacts; no Ship code invokes Rooms or interprets placement lifecycle; rollback path remains until parity corpus passes. |
| 4 - Reusable environment bundles | Make hooks/skills/agent definitions content-addressed and shareable across requests. | Define reusable bundle manifest/CAS convention, materializer, digest receipt, and workbench-hook fixture on top of the Phase 1 local bundle seam. | Phase 2 GO; demand from Phase 3 | 450-700 | Same reusable bundle digest materializes locally and in Rooms; missing/mutated content fails before workload start. |
| 5 - Earned extensions | Add only capabilities pulled by evidence. | Consider detached submit, remote artifact URIs, cloud adapter, snapshot/fork refs, or extraction to its own module/repo. | Measured demand | Unscoped | Separate TDD per capability. |

Phases 0-2 establish that the mechanism is plausible; they do not prove Ship can surrender lifecycle ownership. Phase 3 is the thesis test and remains uncommitted until the Rooms prerequisite gate passes. Phase 0 should split schema/type shape from semantic validator/reducer tests if it crosses one reviewable PR.

## 10. Open questions

1. **CLI name:** `runway` is the current recommendation. Does it fit the Workbench vocabulary, or should the executable be the literal `spawn`/`exec`?
2. **Envelope reuse:** should `RunEvent` be the body of Workbench's hash-chained `Envelope`, or remain a standalone line with `run_id`/`seq`? Reusing the envelope adds integrity and parent links but also verdict-era fields that may not help execution.
3. **Profile storage:** should backend profiles live in one Workbench config file or be resolved by each adapter from its existing substrate configuration? The request contract deliberately does not expose profile internals.
4. **Controller loss on Windows:** which process-start identity, process-group mechanism, and atomic writer-claim primitive provide PID-reuse and concurrent-reconcile safety without adding a service?
5. **Stream parity:** is terminal replay of provider event artifacts sufficient for the first Ship-on-Runway Rooms integration, or must a separate design add a live opaque byte-stream access surface before Phase 3 can remove the direct adapter? Reserved `stream_delivery: live` creates no v0 access mechanism.
6. **Schema publication:** should TypeScript and Rust consumers vendor the schema/semantic fixture corpus at a pinned commit, fetch release artifacts in CI, or consume a generated package? Direct cross-repo source imports are out.

## 11. Validation plan

### Gate A - contract laws (Phase 0)

Golden fixtures and generated cases prove:

- schemas and Go types agree on wire shape;
- Go admission validation rejects absolute/traversing bundle sources, cwd/path arguments, input targets, and outputs;
- structured path arguments expand to native Windows and Linux fixture paths without changing `work.json`;
- every backend sets the three root-discovery environment variables to the same native roots used for structured path expansion;
- profile names remain logical and contain no controller/backend host path;
- secret references must match `^env:[A-Za-z_][A-Za-z0-9_]*$` in both schema and Go validation; inline or malformed references reject;
- a request digest changes when any exact submitted byte changes;
- a work digest remains identical across local and Rooms placed requests;
- pure reducer/model tests enforce contiguous, phase-monotone histories and at most one terminal event/result;
- Go semantic validation enforces terminal status/reason/phase/cause combinations;
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
- race at least two reconcilers and prove only one acquires the writer claim or mutates events/result.

PASS requires truthful terminal receipts, contiguous event histories, no secret values in a recursive scan including `private/`, and no orphan process groups. Abrupt controller loss may require an explicit `reconcile`; no test assumes automatic discovery.

### Gate C - placement prerequisite (Phase 2)

Run identical `work.json` bytes and policy through separate `local/default` and `rooms/agent-cursor` placed requests. The test environment must keep the foreground controller alive except in explicit controller-loss cases; v0 does not claim automatic orphan adoption.

This gate compares lifecycle and result semantics, not isolation strength or resource policy. Those truthfully differ by placement profile and are reported, never normalized away.

1. `work_sha256`, statuses, reason codes, declared artifact names/digests, and phase order are equivalent;
2. placement receipts truthfully differ in profile, enforced constraints, image identity, stream-delivery mode, and backend details;
3. two Rooms requests overlap on distinct slots and complete independently;
4. with a pool of N, N runs are admitted and an overlapping N+1 request returns structured admission backpressure without a hidden retry;
5. cancellation and timeout preserve partial events/artifacts;
6. every terminal path leaves `rooms ls` empty and zero Firecracker/tap/slot residue;
7. no Runway-authored request, lifecycle event, result, redacted log, process argv, or `private/` file contains the fixture secret value;
8. controller and public contract packages contain no Rooms name checks or parsing; Rooms-specific lifecycle translation and residue assertions remain in the adapter/gate harness.

The Gate C fixture must not intentionally write its secret into a declared workload artifact. Artifact bytes are workload-owned and digest-preserved; a secret found there is fixture/workload leakage and makes that run unsafe to export, not evidence that Runway serialized the secret into its own contract surfaces.

**GO:** all eight pass on a fresh `rooms-host` rebuild, and neither backend requires a caller to branch on backend-specific text.

**NO-GO / reshape:** if equivalent terminal semantics require the controller/public contracts to understand Cursor, Ship, Firecracker slots, or provider events, keep the shared schemas smaller and leave orchestration in the existing adapters. If foreground control makes cancellation/recovery unusable under the stated lifetime precondition, design a per-run supervisor only after documenting the exact failed flow.

### Gate D - Ship thesis (Phase 3)

Run equivalent local and Rooms Ship workflows through Runway behind a feature flag. PASS requires existing cancellation, duration-cap, event-ordering, terminal classification, branch, and artifact behavior; Ship contains no `rooms run`, Rooms artifact parsing, slot/readiness logic, or placement-specific cleanup. Terminal replay is acceptable only if it matches the current Rooms runner contract; any local live-event regression blocks removal of the direct local runner. This is the first gate that can validate or kill the portfolio hypothesis.

## 12. Dossier seeding handoff

The existing Dossier connector is not exposed in this Codex session. Do not hand-edit the corpus as a substitute. A Dossier-capable follow-up should:

1. resolve the existing `workbench` project;
2. add six phase stubs mirroring §9;
3. materialize tasks only for Phases 0-2, through the placement prerequisite gate;
4. create and link a Rooms task for the structured lifecycle/pool-full surface required before Phase 2;
5. link this file as a `doc` artifact and the design PR as a `pr` artifact;
6. leave Phases 3-5 task-less until Gate C passes.

Recommended task tiers: Phase 0 schema/conformance tasks `sonnet/extra`; Phase 1 state machine, cancellation, and controller-loss work `opus/max`; Phase 2 Rooms adapter `opus/extra` with an `ultracode` adversarial validation pass.
