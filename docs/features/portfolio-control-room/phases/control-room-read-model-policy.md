**Status**: draft
**Owner**: Workbench maintainers
**Date**: 2026-07-13
**Related**: dossier task `control-room-read-model-policy` (id: `tsk_01KXDF5JX246XT4QK9A3WTXXTS`); [`../spec.md`](../spec.md); Phase 1 PR #16 (`e0477d08d76dc57f3f0f86724063a8e1e8eb52f0`)

# Lock the Control Room read model and deterministic attention policy

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Presentation model | `cmd/controlroom/internal/model/**` — versioned snapshot, normalized entities, availability, receipts, diagnoses, and safe links | ~190 | ~190 |
| Deterministic policy | `cmd/controlroom/internal/policy/**` — liveness, ranking, precedence, stale suppression, linkage, and friction score | ~230 | ~230 |
| Demo scenario | `cmd/controlroom/internal/demo/**` plus one deterministic golden snapshot under that package's `testdata/` | ~100 | ~75 |
| Model/policy tests | Tests colocated in the three `cmd/controlroom/internal/*` packages and JSON goldens | ~290 | ~145 |
| **Total** | | **~810** | **~640** |

Band: **ideal** per the repository PR-sizing convention and the accepted TDD's Phase 2 budget of 450–700 weighted LOC.

## Goal

Give every later UI, adapter, and orchestration task one private, presentation-owned contract. Phase 2 converts factual normalized records into deterministic liveness and attention conclusions without importing producer packages, reading sibling stores, or mixing source truth with Control Room policy. The same immutable `Snapshot` shape must support the Phase 3 demo and Phase 4 real adapters.

## Functional contract

Create three command-private packages: `cmd/controlroom/internal/model`, `cmd/controlroom/internal/policy`, and `cmd/controlroom/internal/demo`. Go's `internal` import rule must enforce the boundary; do not leave the model in an importable top-level `cmd/controlroom` library package and do not promote these types into Workbench `contracts`. `policy` may import `model`; `demo` may import `model` and `policy`; neither lower layer imports upward. The Phase 3 command/server may consume all three.

### Snapshot and source truth

Implement the TDD §5 model, including:

- `Snapshot`: `version`, `mode`, `generated_at`, source receipts, runs, tasks, pull requests, diagnoses/reliability, tool health, attention items, and stable repository options.
- `SourceReceipt`: source, `loading | ok | degraded | unavailable | stale`, observation time, duration, and sanitized typed error fields.
- Generic `Availability[T]`: `available | unknown | unavailable`; absent producer values never become useful zero values. The Go zero value (`State == ""`, `Value == nil`) must behave and serialize as `unknown`, never `available`.
- `Run`: stable workflow/driver ID and kind, repository/project/task/spec identity, explicit owner-issued `DocPath` and `SpecPath` availability, branch/status/phase, requested and actual runtime/provider/model availability, source timestamps, failure facts, evidence links, and derived liveness.
- `Task`: Dossier identity, project/phase/status/assignee, declared dependencies and reverse blockers, timestamps, artifact links, and derived liveness.
- `PullRequest`: repository/number/title/HTTPS URL/author/refs, draft and timestamps, visible checks, review decision, requested-reviewer count, unresolved-thread count, mergeability/state, `complete | truncated | unknown` detail state, and next factual condition.
- `Diagnosis`: the exact availability-bearing verdict, findings, report/evidence, token, cost, and latency shape in TDD §5. Raw traces never enter the model.
- `ToolHealth`: tool, worst severity, recurrence/session count, last occurrence, pain lines, and `accumulated_friction` kind. A live incident is a different source fact and must not be relabeled as accumulated friction.
- `AttentionItem`: stable ID, category, numeric score, stable rule ID, title, explanation/reason, repository/project, evidence links, supporting source names, newest factual update, and stale flag.
- `SafeLink`: allowlisted label plus HTTPS URL or a copyable repository-relative path. Do not add arbitrary filesystem paths or `file://` links. Phase 6 owns resolved workspace and `vscode://file/` validation.

Use named string types and constants for externally serialized enumerations. JSON remains readable and additive-field tolerant: marshaling emits the documented snake-case contract; unmarshaling ignores unknown fields. Constructors or validation helpers must make invalid availability combinations and receipt states difficult to create, while unknown future fields do not fail a whole fixture.

`Snapshot.Sources` contains exactly one effective receipt per configured source name. In Phase 2, a receipt “belongs to the evaluated snapshot” precisely when it is that source's receipt in the `Snapshot.Sources` slice passed to `ApplyPolicy`; no timestamp freshness window is inferred. It is current only when its state is `ok` or `degraded`. `degraded` is current-but-explicitly-partial. `loading`, `unavailable`, missing, duplicate, or `stale` receipts are not current and fail closed. Phase 5's publisher is responsible for placing only current-generation receipts into a newly published snapshot and for marking retained payload `stale`; a latest failed attempt may appear separately only in the later composition input, before it is reduced to the one effective snapshot receipt.

A general `degraded` receipt does not create a new attention rule: its partial state remains visible in `Snapshot.Sources`, and source-specific informational rules such as `pr.detail_truncated` explain known consequences. Do not invent `source.degraded`; the accepted ranking table is exhaustive.

### Pure policy seam

Expose one pure policy entry point equivalent to:

```go
func ApplyPolicy(snapshot Snapshot, now time.Time) Snapshot
```

It returns a value with derived liveness and a newly ranked attention slice; it does not mutate caller-owned slices, read the clock, run processes, touch disk, or fetch data. The injected `now` is the only time source.

The implementation may use smaller internal reducers, but keep factual normalization separate from derived policy. Every consequence item must name its rule ID and the factual evidence that made it fire. The UI must be able to display “Control Room policy” explanations without mistaking them for producer status.

## Liveness policy

Implement the accepted thresholds exactly, with boundary tests:

- `on_fire/retry_loop`: at least three failed runs in the preceding 72 hours, including the boundary. The exact grouping key is `(Run.Kind, input_document)`: workflows use a non-empty available `Run.DocPath`; drivers use a non-empty available `Run.SpecPath`. Missing/unknown identity suppresses the rule. Workflow and driver groups never mix. Explain count, window, and latest cause.
- `on_fire/stalled_active`: `pending | running | dispatching | dispatched` with no source movement for at least 15 minutes. Explain the 15-minute Control Room threshold.
- `live`: source movement no more than 72 hours old, any active run, or an explicitly linked open pull request.
- `idle`: open work with movement older than 72 hours and no more than 336 hours old.
- `stale_claim`: a Dossier `claimed | in_progress` task strictly older than 336 hours with neither an explicitly linked open PR nor a current Ship run updated within the preceding 336 hours (the 336-hour boundary is recent and suppresses staleness). Evaluate only when Dossier, GitHub inventory, and Ship inventory receipts are all `ok`; `degraded`, `stale`, missing, or unavailable suppresses the conclusion.
- `blocked_no_path`: a task whose exact Dossier status is `blocked` and which has no resolvable declared dependency or exact artifact link explaining a path forward.
- `done`: a task whose exact Dossier status is `done`, when its Dossier receipt is current.
- `unknown`: the required fallback for every run or task when no rule above qualifies, identity/time facts are missing, the entity's supporting receipt is not current, or a terminal status has no dedicated liveness rule. The JSON liveness field is never omitted.

All elapsed-time comparisons use `now.Sub(factual_timestamp)` in hours, not calendar days; future timestamps clamp to zero elapsed time. The exact partition is `live <= 72h`, `idle > 72h && <= 336h`, and stale-claim age `> 336h`.

Derived entity liveness is safe only with a current supporting source receipt. If that receipt is `loading`, `unavailable`, missing, duplicate, or `stale`, preserve the producer status/timestamps but set the entity liveness label to `unknown`. Stale receipts therefore suppress both consequence attention items and direct entity liveness labels; `source.stale` carries the explanation.

Liveness is first-match with kind-specific precedence. Runs evaluate `retry_loop`, `stalled_active`, `live`, `idle`, then `unknown`. Tasks evaluate `done`, `blocked_no_path`, `stale_claim`, `live`, `idle`, then `unknown`. This makes terminal completion stable, lets blocked/stale policy outrank mere age, and prevents active or linked work from being labeled idle.

Linkage is exact and owner-issued. A run's task/spec identity may equal a task ID or slug, and an artifact may name the exact PR/run. Title/body substring guesses never link entities.

## Attention ranking

For each normalized run, task, or PR, evaluate non-informational rules in descending score order and emit only the first match for that entity. Informational source/detail items may coexist. Sort across entities by score descending, newest factual update descending, then stable attention ID ascending.

| Rule ID | Category | Score | Required facts |
|---|---|---:|---|
| `run.retry_loop` | urgent | 100 | Liveness retry-loop group qualifies. |
| `run.stalled_active` | urgent | 95 | Active/pending run reaches the no-update threshold. |
| `pr.ci_failed` | urgent | 90 | Current GitHub receipt and at least one visible completed failed check. This observed negative remains valid when another detail connection is truncated. |
| `pr.changes_requested` | urgent | 85 | Current GitHub data reports `CHANGES_REQUESTED`. |
| `task.blocked_no_path` | urgent | 80 | Blocked task has no resolvable path. |
| `pr.unresolved_threads` | actionable | 75 | Current data returns at least one unresolved thread; the observed negative remains valid if later pages are truncated. |
| `pr.review_needed` | actionable | 70 | Current GitHub receipt; complete detail; non-draft; at least one visible check and all visible checks completed successfully; review required or requested-reviewer count nonzero. |
| `pr.merge_ready` | actionable | 65 | Current receipt; complete detail; non-draft; non-empty all-success checks; approved; no requested reviewers; mergeable/clean; zero unresolved threads. |
| `task.stale_claim` | actionable | 55 | Exact liveness rule qualifies with all three required source inventories `ok`. |
| `task.ready` | actionable | 40 | Status `todo` and every declared dependency is present and terminal-done. Missing/unknown dependencies fail closed. |
| `pr.checks_running` | waiting | 30 | Current complete detail; a visible queued/pending/in-progress check; no visible failure; no changes request; zero unresolved threads. |
| `tool.accumulated_friction` | informational | 10–25 | Formula below; never a live incident. |
| `source.unavailable` | informational | 8 | Current-generation unavailable receipt with its sanitized owner/source error. |
| `source.stale` | informational | 7 | Retained records or diagnosis payload from an earlier generation. |
| `pr.detail_truncated` | informational | 6 | Current PR detail is truncated; name the saturated connection and suppress positive readiness. |

`tool.accumulated_friction = min(25, 10 + severity + recurrence + recency)`:

- severity: P1=8, P2=5, P3=2, unknown=0;
- recurrence: `min(4, max(0, session_count - 1))`;
- recency: 3 when elapsed is at most 72 hours, 1 when greater than 72 and at most 336 hours, otherwise 0.

Positive readiness/waiting conclusions fail closed. Empty or unknown checks never qualify as success. `detail_state != complete` suppresses `review_needed`, `merge_ready`, and `checks_running`; negative facts already returned may still produce `ci_failed`, `changes_requested`, or `unresolved_threads`.

An urgent, actionable, or waiting item is current only when every supporting receipt is current. If a supporting receipt is stale, suppress the consequence and emit exactly one `source.stale` item per stale source, with stable ID `source.stale:<source>`; never emit one per retained entity. Informational retained items may remain visible with `stale: true`, accompanied by that source-level item. Source unavailability yields exactly one `source.unavailable:<source>` item; it never fabricates a record-level problem or readiness conclusion.

Friction recency always uses `now - ToolHealth.LastOccurrence`, including for retained stale friction. It never uses receipt observation time; stale state changes visibility/currentness, not the historical occurrence timestamp or score inputs.

## Deterministic demo scenario

Add a production demo builder or fixture-backed loader using the Phase 1 sanitized contracts and the fixed clock `2026-07-13T12:00:00Z`. It must create, at minimum:

- one healthy/current run;
- one stalled active run;
- three same-kind/same-document failed runs that form a retry loop;
- one failed-CI PR;
- one review-needed or merge-ready PR with complete non-empty checks;
- one blocked task with no path;
- one ready task with terminal dependencies;
- one diagnosis with findings and explicitly unavailable telemetry;
- accumulated friction;
- at least one degraded/unavailable source and one stale retained informational record.

Apply the same policy function used by real snapshots. Produce a stable golden JSON snapshot whose timestamps, item order, scores, explanations, and IDs are reproducible byte-for-byte after standard JSON indentation. The demo builder performs no subprocess or network I/O.

## Validation

Add table-driven and golden tests, with names or equivalent coverage for:

- JSON round-trip of the snapshot contract, unknown additive fields, and `Availability` available/unknown/unavailable semantics.
- Every ranking rule and exact score, including all threshold boundaries.
- Liveness fallback: current Dossier `done` maps to `done`; unmatched, missing-fact, terminal-other, and non-current entities map to `unknown` without omitting the field.
- Receipt membership/currentness: unique in-snapshot `ok|degraded` receipts qualify; missing, duplicate, loading, unavailable, and stale receipts suppress dependent liveness and consequence items.
- Per-entity precedence/exclusivity (failed CI beats review/merge/waiting; changes-requested beats later PR rules; retry-loop beats stalled-active where both could describe a run).
- Stable order by score, newest factual update, then ID.
- Workflow/driver retry groups never combine; different `docPath`/`specPath` identities never combine.
- `stale_claim` requires all three inventories `ok`, exact linkage, and a truly absent recent linked PR/run, including exact 336-hour boundary cases.
- Missing dependency state prevents `task.ready`; all known terminal dependencies allow it.
- Empty/unknown checks and truncated/unknown detail never create positive PR readiness.
- Truncated details retain returned negative evidence and add `pr.detail_truncated`.
- Stale supporting receipts suppress urgent/actionable/waiting items and add `source.stale`.
- Multiple retained entities from one source emit one stable source-level stale item; two stale sources emit two.
- Unavailable sources create only `source.unavailable`; unrelated healthy source items survive.
- Friction scoring for P1/P2/P3/unknown, recurrence cap, 72-hour and 336-hour boundaries, `now - last occurrence` under stale receipts, and stale informational visibility.
- Demo snapshot equals the committed golden and contains the required story beats.

Run:

```text
gofmt -l .
go vet ./...
golangci-lint run ./...
go test -race ./...
go build ./...
git diff --check
```

## Tradeoffs and risks

- Keep the model explicit even if it is verbose. Availability-bearing fields and stable rule IDs cost lines but prevent dangerous zero-value inference and UI/adapter drift.
- Do not create a generic rules engine. Straight-line, table-tested Go policy is easier to audit and change.
- Do not parse all producer envelopes in production here. Phase 4 owns per-source adapters; Phase 2 may use small test helpers or the deterministic demo builder to prove the model.
- Do not introduce `atomic.Pointer`, goroutines, HTTP, embedded UI assets, CLI flags, subprocesses, MCP, GitHub pagination, or path resolution. Those belong to later serialized phases.
- Do not import Ship, Dossier, Tracelens, Tower, or toolhealth source packages. The presentation model owns normalized facts only.

## Non-goals

- HTTP handlers, server security headers, CSRF, refresh coordination, and snapshot publication.
- Static UI, responsive behavior, filters, drawers, browser automation, or screenshots.
- Real source adapters, executable discovery, Dossier child lifecycle/breaker, GitHub queries, and Tracelens invocation.
- Workspace deep-link resolution or `vscode://file/` generation.
- Durable cache or writing back to any producer.

## Implementation plan

1. Define versioned presentation types, enumerations, availability helpers, and JSON contract tests in `cmd/controlroom`.
2. Implement pure liveness/linkage reducers and table-test every boundary.
3. Implement per-entity precedence, current-source gating, informational rules, friction scoring, and deterministic ordering.
4. Build the fixed-clock demo snapshot from sanitized facts and commit its golden JSON.
5. Run the full repository gates and review the golden diff for accidental source inference, raw paths, or unstable ordering.
