# dispatch — Technical Design Document

**Status:** draft / proposal — NOT a build commitment. The artifact we decide from.
**Owner:** @itsHabib
**Date:** 2026-07-15
**Related:** [`docs/DESIGN.md`](../../DESIGN.md) (the boundary law this tool must obey), ship `docs/features/agent-runner-abstraction/spec.md` (the engines this routes across), `/provenance --by-model` (cc-skills — the scorecard that feeds §7's drift flow), `docs/features/model-lottery/` in ship (the experiment that motivated this).

> **Reviewers — focus areas:** §4.1 (CLI-vs-library seam), §4.5 (zero-ship-changes integration — is the manifest really a sufficient interface?), §7.2 (fail-closed semantics on policy miss), §10 (task-class taxonomy — the weakest part of the design).

## Glossary — one line, so it is never re-debated

**dispatch (this tool) decides placement; ship dispatches (the verb) executes it.** An airline dispatcher plans and authorizes the flight — aircraft, crew, fuel — and never touches the controls. Ship is the pilot. Policy vs mechanism, held as a name.

## 1. Problem & hypothesis

Placement policy — which engine, provider, model, and effort gets a task — is currently smeared across three places, none of them owned:

1. `tier-map.ts` inside ship (model↔tier cells, coupled to one engine),
2. ship's `assign` verb (pool round-robin, also inside the engine),
3. escalation rules living as **prose in skills** ("burns >2 correction rounds on the same defect class → escalate to opus").

Consequences, all observed in real runs: the policy can't route *across* engines (ship now has three runners — cursor, claude, codex — and `/goal`-style harnesses are coming); decisions leave no receipt (the runway model-override rationale lives in a manifest comment); and the `/provenance --by-model` scorecard — cost-per-useful-PR, correction rounds, interventions per model — has no consumer that can act on it.

**The bet:** engines are a depreciating asset — vendors will eat dispatch/poll/land mechanics. The durable layer is placement policy: which model, at what cost, with what escalation, recorded as evidence. Extract it as its own plane now that the second and third engines exist, and every engine (including ship's own driver, and eventually a vendor harness) becomes a swappable backend under it.

**Non-goals (v1):**

- **Not an engine.** dispatch never dispatches, polls, lands, or talks to a provider API. It reads a policy and a task descriptor and emits a decision.
- **No learned policy.** No bandit, no auto-tuning. The scorecard informs a *human* editing the policy file; `drift` is advisory.
- **No ship code changes.** The driver manifest is the integration point (§4.5).
- Viability preflight stays in ship — "is this model reachable right now" is a runtime capability check (mechanism); dispatch owns *preference* (policy).

## 2. Functional & non-functional requirements

**FR:**

- FR1 — `dispatch decide`: task descriptor in → placement out (`engine, provider, model, effort, runtime, escalation`), decision receipt appended.
- FR2 — policy is a versioned file, content-hashed; every receipt carries the policy hash it was decided under.
- FR3 — `dispatch drift`: scorecard export + policy in → report of where outcomes contradict policy (e.g. "policy sends small tasks to grok; measured corr-cost 4.0 vs opus 2.1").
- FR4 — deterministic: same descriptor + same policy → byte-identical placement.
- FR5 — every decision is explainable: the receipt names the matched rule.

| NFR | Target |
|---|---|
| Determinism | FR4; no clock, no randomness, no network in `decide` |
| Failure model | fail-closed: invalid policy or unmatched descriptor → non-zero exit, **no fallback placement** (§7.2) |
| Dependencies | stdlib-only Go, per workbench charter |
| Auditability | receipts are append-only JSONL; policy hash pins every decision |
| Composition | artifacts only (exit codes + JSONL); no other tool imports dispatch's decision logic (boundary law) |

## 3. Architecture overview

```
                 policy.json (versioned, sha256-pinned)
                        │
  task descriptor ──► dispatch decide ──► placement (stdout JSON)
                        │                     │
                        ▼                     ▼
              receipts.jsonl        /work-driver-prep fills
              (append-only)         driver-manifest stream fields
                                          │
                                          ▼
                              ship driver / claude-local / codex
                              (engines — unchanged, unaware)

  /provenance --by-model export ──► dispatch drift ──► drift report (advisory)
```

Layout follows the workbench convention — `cmd/dispatch/main.go` (flags, exit codes, JSONL emit) + `cmd/dispatch/internal/{policy, placement, receipt, scorecard}`. The placement schema joins `contracts` when a second tool needs the vocabulary, not before (§10).

New vs reused: everything under `cmd/dispatch/` is new; the frozen-policy discipline (version + content hash, validated fail-closed) is reused from gate and the roxiq gauntlet policy loader — same DNA, deliberately.

## 4. Key decisions & trade-offs

### 4.1 Data-coupled sibling, not a library

`/work-driver-prep` invokes the CLI and reads stdout JSON. The alternative — ship importing a placement library — was rejected: it would weld the policy layer to one engine's language and release cycle, which is the exact failure this tool exists to prevent. A format is a smaller promise than an API. Cost: a subprocess call per stream at prep time (negligible; prep is human-cadence).

### 4.2 Static policy file, human-edited

Declarative rules evaluated deterministically, in file order, first-match. The alternative (scorecard-driven auto-routing) is deferred until `drift` has produced enough reports that the human edits become mechanical — that's the earn-it signal for a learning loop, and it may never come. Matches gate's design DNA (deterministic floor) rather than the engine's.

### 4.3 Fail-closed on no match

A descriptor no rule matches is an **error**, not a default placement. The policy file must carry an explicit catch-all rule if the operator wants one. Rationale: a silent default is how policy drifts back into mechanism; forcing the catch-all into the file keeps the whole policy visible in one reviewable artifact.

### 4.4 Escalation is emitted, not enforced

`decide` includes the escalation rule (`max_rounds_per_defect_class`, `escalate_to`) in the placement it emits; the *seat driving the run* enforces it. dispatch has no runtime presence — enforcing would make it an engine. Trade-off accepted: an ignored escalation shows up in `drift`, not as a hard stop.

### 4.5 V1 integration: the manifest IS the interface — zero ship changes

`/work-driver-prep` currently hand-guesses `model`/`provider`/`effort` per stream; v1 has prep call `decide` to fill them. Ship consumes the manifest exactly as today, unaware dispatch exists. This defers any ship thin-down (`assign`, `tier-map`) to a post-gate phase — proven demand first. **Reviewer call requested:** is there placement state the manifest can't carry that would force an earlier ship touch? (Known candidate: per-stream escalation policy — today it's manifest-comment prose.)

### 4.6 Name: `dispatch`, disambiguated by glossary

Collides with ship's `dispatch` verb; accepted deliberately because the airline-dispatcher metaphor is exact and the glossary line resolves it (planner vs act — the policy/mechanism line restated). Rejected alternatives: `router`/`dispatcher`-as-generic (utils-grade), `helm` (Kubernetes owns the word), `billet`, `quartermaster`.

## 5. Data model

**Policy file** (`dispatch-policy.json`, lives in the consuming repo or `~/.config/dispatch/`):

```json
{
  "version": 1,
  "rules": [
    {
      "name": "small-mechanical-to-cheap",
      "match": { "task_class": "mechanical", "max_weighted_loc": 500, "risk_tier": ["T0", "T1"] },
      "place": { "engine": "ship-driver", "provider": "cursor", "model": "grok-4.5", "effort": "high", "runtime": "cloud" },
      "escalation": { "max_rounds_per_defect_class": 2, "escalate_to": "claude-opus-4-8" }
    },
    { "name": "catch-all", "match": {}, "place": { "engine": "ship-driver", "provider": "claude", "model": "claude-opus-4-8", "effort": "max", "runtime": "local" } }
  ]
}
```

**Task descriptor** (stdin or `--task` flag, JSON): `{ repo, task_class, weighted_loc, risk_tier, budget? }`.

**Placement** (stdout JSON): the matched rule's `place` + `escalation`, plus provenance: `{ rule, policy_version, policy_sha256 }`.

**Receipt** (one JSONL line, append-only): descriptor + placement + `decided_at` — the same evidence-record shape flare and gate already consume.

Versioning: `version` bumps on breaking schema change; the sha256 pins exact content per decision regardless.

## 6. API contract

```
dispatch decide --policy <path> [--task <json> | reads stdin] [--receipts <path>]
    stdout: placement JSON        exit 0
    exit 2: invalid/missing policy (schema, hash, version)
    exit 3: no rule matched (fail-closed; add a catch-all if you want one)
    exit 4: invalid task descriptor

dispatch drift --policy <path> --scorecard <json-path>
    stdout: drift report (JSON; --md for human form)
    exit 0: no drift; exit 1: drift found (grep-able in CI); exit 2/4 as above
```

Errors are values on stderr as single-line JSON `{code, message}`; no partial placement is ever emitted on a non-zero exit.

## 7. Key flows

### 7.1 decide (happy path)

1. Load policy → validate schema → compute sha256.
2. Parse descriptor → validate required fields.
3. First-match scan of `rules` in file order.
4. Emit placement to stdout; append receipt (descriptor, placement, rule name, policy hash, timestamp).
5. Exit 0. Steps 1–3 are pure; the only write is the receipt append.

### 7.2 decide (failure paths — the load-bearing ones)

- Policy unreadable/invalid → exit 2 **before** reading the descriptor. No placement.
- No rule matches → exit 3, stderr names the descriptor fields that matched nothing. No placement. The caller (prep) surfaces this to the operator instead of guessing — a placement hole is a policy bug, and the fix is a policy edit, not a runtime default.
- Receipt append fails after placement computed → placement still emitted, exit 0, warning on stderr. Receipts are best-effort evidence, not a transaction (flare precedent, not gate precedent — dispatch decisions are advisory until an engine acts on them).

### 7.3 drift

1. Load policy + scorecard export (per-model rows: streams, merged, correction rounds, interventions, corr-cost).
2. For each rule, find scorecard rows for the placed model within the rule's match window.
3. Report rules whose placed model is measurably dominated (worse corr-cost than an alternative the policy itself references) and models in the scorecard the policy never places.
4. Advisory always: exit 1 signals "drift found", nothing more.

## 8. Concurrency / consistency / failure model

Single-shot CLI; no daemon, no shared state beyond the receipts file. Concurrent `decide` invocations appending to one receipts file use O_APPEND single-line writes (< 4KB, atomic on POSIX; on Windows, best-effort — receipts are evidence, not a ledger, per §7.2). No retries anywhere: every failure is deterministic, so retrying is re-running.

## 9. Rollout / implementation plan

| Phase | Goal | High-level tasks | Depends on | Weighted est. | Gate |
|---|---|---|---|---|---|
| 1. `dispatch-decide-core` | `decide` end-to-end: policy load/validate/hash, first-match, placement emit, receipts | policy schema + loader (fail-closed), matcher, receipt writer, CLI + exit codes, table tests over §7.2 paths | — | ~550 (ideal band) | — |
| 2. `dispatch-replay-validation` | **VALIDATION GATE** — prove decisions match reality | replay harness: descriptors reconstructed from the runway + model-lottery manifests; policy file authored to encode the operator's actual choices; `decide` must reproduce every historical placement (or diverge with a defensible receipt) | 1 | ~250 (tests-heavy) | ✅ go/no-go |
| 3. `dispatch-drift` | `drift` over the provenance scorecard | scorecard ingest, dominance report, `--md` render | 1 | ~350 | post-gate |
| 4. `dispatch-prep-integration` | `/work-driver-prep` calls `decide` to fill manifest placement fields | skill edit (cc-skills), descriptor derivation from task docs, fallback-to-manual when exit 3 | 2 | skill-only | post-gate |
| 5. `dispatch-ship-thindown` | ship's `assign`/`tier-map` consume decisions | **stub — do not spec.** Earn with ≥1 month of phase-4 usage | 4 | — | post-gate |

Phases 1–2 are the commitment (~800 weighted total, two PRs). Phases 3–5 are gated: if replay shows the policy language can't express the operator's real choices, the design is wrong and we stop before building `drift`.

## 10. Open questions

1. **Task-class taxonomy.** `task_class` + `risk_tier` need a real enumeration. Candidates: reuse `/pr-risk`'s tiers (already deterministic-floor'd) vs a new size×novelty grid. Reusing pr-risk couples two planes' vocabularies — probably correct (shared *contract*, not call stack) but needs a deliberate call.
2. **Scorecard export shape.** `/provenance --by-model` renders markdown today; `drift` needs JSON. Add `--json` to the skill, or have drift parse the table? (Leaning: `--json` in the skill — parsing rendered markdown is a self-inflicted wound.)
3. **When does `placement` enter `contracts`?** Phase 4 makes prep a second consumer — that's the earn-it trigger per the charter. Confirm nothing needs it earlier.
4. **Budget semantics.** The descriptor carries `budget?` but v1 rules don't price anything (no cost telemetry exists — confirmed in ship's store). Keep the field reserved, or drop until telemetry exists?

## 11. Validation plan

The phase-2 gate, binary and baseline-free: **a policy file exists that (a) passes fail-closed validation, (b) reproduces 100% of the placements the operator actually chose across the runway phase-1 and model-lottery phase-3 manifests (6 streams, 3 models, 2 repos), and (c) every reproduction receipt names the rule that fired.** If the policy language needs per-stream special-casing to hit 100%, the language failed — that's a no-go, not a rounding error. Secondary signal (phase 4, post-gate): one real `/work-driver-prep` run ships with dispatch-filled placement fields and zero manual overrides.
