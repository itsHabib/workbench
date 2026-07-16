# dispatch — the placement-decision plane (phase 1)

**Status:** phase 1 (`dispatch-decide-core`), 2026-07-15
**Owner:** @itsHabib
**Shape:** one small stdlib-Go binary. Reads a policy + a task descriptor, emits a placement decision. Never dispatches, polls, lands, or talks to a provider.
**Contract:** [`docs/features/dispatch/spec.md`](../../../../docs/features/dispatch/spec.md) is the merged TDD and the binding design. This doc is the per-tool charter — read the TDD's §5 (data model), §6 (API), §7 (flows), §8 (concurrency) first.

## Glossary — one line, so it is never re-debated

**dispatch (this tool) decides placement; ship dispatches (the verb) executes it.** An airline dispatcher plans and authorizes the flight — aircraft, crew, fuel — and never touches the controls. Ship is the pilot. Policy vs mechanism, held as a name.

## What phase 1 is

`decide` + `validate` end-to-end: load a versioned, content-hashed policy file, validate it fail-closed, first-match-scan its rules against a task descriptor, emit a deterministic placement, and (when asked) append an append-only receipt. `validate` is the author pre-flight over the same loader. No `drift`, no scorecard, no `/work-driver-prep` integration — those are later phases.

## Laws

- **Pure decision, no execution.** dispatch reads a policy and a descriptor and emits a decision. It never dispatches, polls, lands, or calls a provider. Viability ("is this model reachable right now") stays in ship — that is a runtime capability check (mechanism); dispatch owns *preference* (policy).
- **Fail-closed everywhere.** An invalid/missing/empty policy, an unknown `task_class` in a match block, a bad descriptor, an unmatched descriptor, or a requested-but-failed receipt append are all non-zero exits — and **no non-zero exit ever emits a placement on stdout**. A descriptor that matches no rule is an *error* (exit 3), never a default placement: a silent default is how policy drifts back into mechanism, so the operator must write the catch-all into the file to get one.
- **Determinism is load-bearing (FR4).** Identical descriptor + identical policy → byte-identical placement stdout. No clock, no network, no randomness in the decide path. The receipt's `decided_at` is the only time read, and it is written to the receipt file, never to the stdout placement. Output is marshaled from fixed-field structs — never by ranging a Go map — so field order is stable.
- **The policy hash pins every decision.** The sha256 is computed over the exact on-disk policy bytes before parsing; every placement and receipt carries it, so a decision is replayable against the precise policy content that produced it, independent of the `version` field.
- **Boundary law.** `cmd/dispatch/internal/*` is private to this tool. It imports no other tool's decision logic. The `placement` type does **not** enter `contracts/` in phase 1 — that is the phase-4 trigger (a second consumer, `/work-driver-prep`).

## Frozen taxonomy (phase 1 freezes this — the phase-2 replay gate depends on it)

- **`task_class`** — complexity/novelty only, enumerated **exactly** `mechanical | analytical | generative`. Size is *not* encoded here; `weighted_loc` carries it continuously, so a large mechanical task and a small analytical one stay expressible. An unknown value fails loudly: exit 2 in a policy `match` block, exit 4 in a descriptor — never a silent never-match.
- **`risk_tier`** — imported from `/pr-risk`'s tier vocabulary (`T0/T1/T2/T3`-style strings) as *shared vocabulary* (contract, not call stack). Treated here as opaque strings; used as a proxy for model-selection appetite (novelty/judgment), not change-risk. `drift` (phase 3) will surface divergence if the proxy breaks down.

## Deterministic descriptor-derivation rules (FROZEN in phase 1)

The phase-2 replay gate is circular unless the rules for turning a task doc + manifest fields into a `Descriptor` are fixed *before* any descriptor is derived — hand-labeling descriptors to fit historical choices would prove nothing. These rules are the contract phase-2's harness derives against; they are frozen here and change only by a versioned amendment to this section.

A `Descriptor{repo, task_class, weighted_loc, risk_tier, budget?}` is derived as follows:

1. **`repo`** — the repository slug the task targets. Source, in order: the driver manifest's `repo`/`repository` stream field; else the dossier task's repo; else the git remote of the worktree the task doc lives in. Exactly one repo per descriptor — a cross-repo task is split before derivation.

2. **`task_class`** — the complexity/novelty class, derived deterministically from the task doc, never from size:
   - **`mechanical`** — the change is mostly transcription with a known shape: rename/move, dependency bump, config/flag plumbing, mechanical test scaffolding, doc-only edits, codemod-style application of a stated pattern. Signal: the task doc describes *what to change* with no open design question.
   - **`analytical`** — the change requires reading and judgment over existing code but not new invention: bug diagnosis + fix, refactor to a named target, wiring a new case into an existing state machine, review-comment remediation. Signal: the task doc describes a *problem to solve* within an established design.
   - **`generative`** — the change invents a new shape: a new package/subsystem, a new schema or protocol, an unproven design, an open-ended "design and build X". Signal: the task doc leaves the shape to the implementer, or a TDD/spec is a precondition.
   - **Tie-break (deterministic):** when a task doc carries signals for more than one class, take the **most complex** present (`generative > analytical > mechanical`) — under-classifying complexity is the costlier error (it under-places the model). An explicit operator tag in the task doc (`task_class: <value>`) overrides the heuristic and is authoritative.

3. **`weighted_loc`** — the task's weighted line estimate. Source, in order: the driver manifest's weighted estimate for the stream; else the task doc's stated weighted/ideal-band estimate; else the sum over the task's planned files of (added + changed lines) with generated/vendored files excluded. Size lives here and only here — never folded into `task_class`.

4. **`risk_tier`** — `/pr-risk`'s tier for the change, reused verbatim as a string. Source, in order: an explicit `risk_tier` in the task doc or manifest; else the `/pr-risk` verdict for the change if one exists; else the deterministic floor `/pr-risk` would assign from the change's blast-radius signals. Never re-derived with a different vocabulary — the string is passed through opaquely.

5. **`budget`** — reserved. Accepted and echoed into the receipt, carried into no placement in v1 (no cost telemetry exists to price against). Derivation: if the task doc states an explicit budget, pass it through verbatim; otherwise omit. A later phase prices it once telemetry earns the field.

**Amendment discipline:** any change to rules 1–5 is a breaking change to the replay contract and must bump this section's own version note and be reconciled against the phase-2 harness — never edited silently.

## Data model (see TDD §5 for the authoritative shapes)

- **Policy** (`policy` package) — `{version, rules[]}`; each rule is `{name, match, place, escalation}`. `match` constrains `task_class` (exact enum), `max_weighted_loc` (`descriptor.weighted_loc <= this`), and `risk_tier` (membership in a list); an empty `match: {}` is the catch-all. Loaded fail-closed with the sha256 of the exact file bytes.
- **Descriptor** (`placement` package) — `{repo, task_class, weighted_loc, risk_tier, budget?}`, parsed with unknown fields refused; an unknown `task_class` → exit 4.
- **Placement** (stdout) — `{schema_version, place, escalation, provenance{rule, policy_version, policy_sha256}}`. `schema_version` is the placement shape's own version, distinct from `policy_version` (the CLI stdout is the contract, so it versions itself).
- **Receipt** (`receipt` package, JSONL) — `{decided_at, rule, policy_sha256, descriptor, placement}`, one append-only line per decision.

## Layout

- `cmd/dispatch/main.go` — verbs (`decide`, `validate`), flag parsing, the exit-code contract, stdout/stderr emit. Errors are single-line JSON `{code, message}` on stderr. Injects stdout/stderr/stdin as `io.Writer`/`io.Reader` so the exit-code and no-placement-on-error invariants are testable.
- `internal/policy` — the data model + fail-closed loader: schema validation, sha256 over exact bytes, the frozen `task_class` enum. The leaf; holds vocabulary and schema rules, no matching/receipt logic.
- `internal/placement` — the descriptor schema, the placement shape (self-versioned), the first-match scan (`match.go`), and provenance. Pure; no clock/I/O/randomness.
- `internal/receipt` — the append-only JSONL writer. Pure mechanism; the exit-5 ordering (receipt-before-stdout) lives in the caller.

## Exit codes

`decide`: **0** placed · **2** bad/missing/empty policy or unknown `task_class` in a match block · **3** no rule matched (actual unmatched values on stderr) · **4** bad descriptor · **5** `--receipts` given but the append failed (nothing on stdout).
`validate`: **0** valid · **1** valid-with-warnings (no catch-all rule) · **2** invalid.

## Non-goals (phase 1)

`drift` + scorecard ingest (phase 3); `/work-driver-prep` calling `decide` (phase 4); promoting `placement` into `contracts/` (phase 4 trigger); concurrent evidence-grade receipt writes (TDD §8 declares them unsupported — callers are serial).
