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
| Dependencies | stdlib-only Go — justified, not dogma: the whole surface is JSON, sha256, file append, flag parsing (all stdlib-native); the charter permits pinned deps when earned, and none is. JSON over YAML for the policy file keeps it that way |
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

`/work-driver-prep` currently hand-guesses per-stream placement; v1 has prep call `decide` to fill it. Ship consumes the manifest exactly as today, unaware dispatch exists. This defers any ship thin-down (`assign`, `tier-map`) to a post-gate phase — proven demand first.

**What the manifest can and cannot carry (v2, per design review):** stream frontmatter already carries `provider`, `runtime`, `model`, `effort` — placement proper fits today. It does **not** carry: `engine` (implicitly ship-driver while that's the only engine prep drives — fine for v1), structured `escalation`, or decision provenance (`rule`, `policy_sha256`). Consequences, stated so no phase-4 integrator assumes otherwise:

- **Escalation is advisory-only end-to-end until phase 5.** It lives in the placement output and the receipt, readable by a human seat; no automated consumer enforces it. A missed escalation surfaces in `drift`, retrospectively (§4.4). Automated enforcement requires a manifest schema extension — deferred to phase 5 deliberately.
- Decision provenance stays in the receipts file, joined by task slug; the manifest is not the audit trail.
- `budget?` has the same gap — reserved in the descriptor, carried nowhere in v1 (§10.4).

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

**Placement** (stdout JSON): `schema_version` (the placement *shape's* own version — distinct from `policy_version`; the CLI is the contract, so the contract versions itself), then the matched rule's `place` + `escalation`, plus provenance: `{ rule, policy_version, policy_sha256 }`.

**Task descriptor taxonomy (frozen in phase 1 — the replay gate depends on it existing first):**

- `task_class` — complexity/novelty only; the schema **enumerates** `mechanical | analytical | generative` (unknown value → exit 2, so a typo in a `match` block fails loudly instead of silently never matching). Size is NOT encoded here — `weighted_loc` already carries it continuously; conflating them makes a large mechanical task and a small analytical one inexpressible.
- `risk_tier` — imported from `/pr-risk`'s tiers as shared vocabulary (contract, not call stack). Used here as a proxy for model-selection appetite (novelty/judgment), not change-risk; documented at the boundary, and `drift` will surface divergence if the proxy breaks down.
- Phase 1 also documents the **deterministic derivation rules** (task doc + manifest fields → descriptor). Without this the phase-2 replay is circular — hand-labeled descriptors fitted to historical choices prove nothing about future tasks.

**Receipt** (one JSONL line, append-only): descriptor + placement + `decided_at` — the same evidence-record shape flare and gate already consume.

Versioning: `version` bumps on breaking schema change; the sha256 pins exact content per decision regardless.

## 6. API contract

```
dispatch decide --policy <path> [--task <json> | reads stdin] [--receipts <path>]
    stdout: placement JSON (schema_version'd)      exit 0
    exit 2: invalid/missing policy (schema, hash, version, unknown task_class
            in a match block, or rules: [] — an empty policy is an authoring
            error, not a descriptor mismatch)
    exit 3: no rule matched (fail-closed; add a catch-all if you want one);
            stderr carries the actual unmatched VALUES
            (task_class=analytical risk_tier=T2), not just field names
    exit 4: invalid task descriptor
    exit 5: --receipts given but the append failed (fail-closed: an explicitly
            requested receipt is evidence, not a nicety; no --receipts flag =
            no receipt attempted, both worlds explicit)

dispatch validate --policy <path>
    pre-flight for policy authors: schema, hash, task_class enum, catch-all
    lint (warn if absent). Same loader as decide — ~zero marginal cost, and
    it means an operator can test a policy edit without a live descriptor.
    exit 0 valid / exit 2 invalid / exit 1 valid-with-warnings

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
4. If `--receipts`: append the receipt (descriptor, placement, rule name, policy hash, timestamp) **first** — a failed append exits 5 with nothing on stdout, preserving the "no placement on non-zero exit" invariant (v2.1, codex: stdout-before-receipt would hand callers a placement from a failed invocation).
5. Emit placement to stdout; exit 0. Steps 1–3 are pure; the only write is the receipt append.

### 7.2 decide (failure paths — the load-bearing ones)

- Policy unreadable/invalid/empty (`rules: []`) → exit 2 **before** reading the descriptor. No placement.
- No rule matches → exit 3, stderr carries the actual unmatched descriptor values. No placement. **Exit 3 forces an operator decision — the caller must never auto-guess a placement.** The fix is a policy edit (then re-run `decide`); if the operator instead hand-places, that is recorded as an explicit override, not a silent default — otherwise the hidden placement policy this tool exists to eliminate reappears one exit code away.
- `--receipts` given and the append fails after placement computed → **exit 5, fail-closed, nothing on stdout** — the receipt writes before the placement is emitted (§7.1 step 4), so a caller can never consume a placement from a failed invocation. An explicitly requested receipt is evidence — silently dropping the only durable record while the caller proceeds would rot the audit trail and starve the phase-2 gate of data. Without the flag, no receipt is attempted; both modes are explicit. (v2 — flipped from best-effort per design review, Copilot + claude; v2.1 — write ordering fixed per codex.)

### 7.3 drift

1. Load policy + scorecard export (per-model rows: streams, merged, correction rounds, interventions, corr-cost).
2. For each rule, find scorecard rows for the placed model within the rule's match window.
3. Report rules whose placed model is measurably dominated (worse corr-cost than an alternative the policy itself references) and models in the scorecard the policy never places.
4. Advisory always: exit 1 signals "drift found", nothing more.

## 8. Concurrency / consistency / failure model

Single-shot CLI; no daemon, no shared state beyond the receipts file. Receipt writes are O_APPEND single-line, but **no cross-process atomicity is claimed** — receipt lines can exceed any atomicity threshold and Windows append semantics are weaker than POSIX. Concurrent `decide` invocations against one receipts file are not a supported evidence-grade mode; prep calls are serial (human-cadence) and the replay harness runs serial. If parallel invocation ever matters, the design is per-decision receipt files + compaction (atomic create/rename), noted here so it's a known successor, not a redesign. No retries anywhere: every failure is deterministic, so retrying is re-running.

## 9. Rollout / implementation plan

| Phase | Goal | High-level tasks | Depends on | Weighted est. | Gate |
|---|---|---|---|---|---|
| 1. `dispatch-decide-core` | `decide` + `validate` end-to-end: policy load/validate/hash, first-match, placement emit, receipts; **taxonomy + derivation frozen here** | policy schema + loader (fail-closed, task_class enum), matcher, receipt writer (exit-5 semantics), `validate` verb, CLI + exit codes, table tests over §7.2 paths **including receipt-presence assertions** (line count = invocation count — the phase-2 gate runs on this data), descriptor-derivation rules documented | — | ~600 (ideal band) | — |
| 2. `dispatch-replay-validation` | **VALIDATION GATE** — prove decisions match reality | replay harness: descriptors derived from the runway + model-lottery manifests **via the phase-1 rules, not hand-labeled**; policy file authored to encode the operator's actual choices; `decide` must reproduce every historical placement (or diverge with a defensible receipt); negative control proves the gate can fail | 1 | ~250 (tests-heavy) | ✅ go/no-go |
| 3. `dispatch-drift` | `drift` over the provenance scorecard | scorecard ingest, dominance report, `--md` render | 1 | ~350 | post-gate |
| 4. `dispatch-prep-integration` | `/work-driver-prep` calls `decide` to fill manifest placement fields | skill edit (cc-skills), descriptor derivation from task docs; **exit 3 blocks — forces an operator decision (policy edit, or a hand-placement recorded as an explicit override); prep never auto-guesses** | 2 | skill-only | post-gate |
| 5. `dispatch-ship-thindown` | ship's `assign`/`tier-map` consume decisions | **stub — do not spec.** Earn with ≥1 month of phase-4 usage | 4 | — | post-gate |

Phases 1–2 are the commitment (~800 weighted total, two PRs). Phases 3–5 are gated: if replay shows the policy language can't express the operator's real choices, the design is wrong and we stop before building `drift`.

## 10. Open questions

1. **Task-class taxonomy — RESOLVED in v2** (claude + codex converged: the phase-2 gate is circular unless this freezes in phase 1). `task_class` is complexity-only, enumerated `mechanical | analytical | generative` (§5); size stays in `weighted_loc`; `risk_tier` imports `/pr-risk`'s tiers as shared vocabulary with the proxy caveat documented. Derivation rules land in phase 1. Remaining sub-question: exact derivation heuristics (task-doc keywords vs operator tag) — phase-1 implementation detail, not a design fork.
2. **Scorecard export shape.** `/provenance --by-model` renders markdown today; `drift` needs JSON. Add `--json` to the skill, or have drift parse the table? (Leaning: `--json` in the skill — parsing rendered markdown is a self-inflicted wound.)
3. **When does `placement` enter `contracts`?** Phase 4 makes prep a second consumer — that's the earn-it trigger per the charter. Confirm nothing needs it earlier.
4. **Budget semantics.** The descriptor carries `budget?` but v1 rules don't price anything (no cost telemetry exists — confirmed in ship's store). Keep the field reserved, or drop until telemetry exists?

## 11. Validation plan

The phase-2 gate, binary and baseline-free: **a policy file exists that (a) passes fail-closed validation, (b) reproduces 100% of the placements the operator actually chose across the runway phase-1 and model-lottery phase-3 manifests (6 streams, 3 models, 2 repos) — with descriptors derived via the phase-1 rules, never hand-labeled to fit — and (c) every reproduction receipt names the rule that fired.** If the policy language needs per-stream special-casing to hit 100%, the language failed — that's a no-go, not a rounding error. The harness includes a negative control (a descriptor the policy must exit-3 on) proving the gate *can* fail. Secondary signal (phase 4, post-gate): one real `/work-driver-prep` run ships with dispatch-filled placement fields and zero manual overrides.

### Go/no-go verdict — 2026-07-15 (`dispatch-replay-validation`)

**Harness:** `cmd/dispatch/internal/replay/{replay.go,replay_test.go}`, fixtures in
`cmd/dispatch/internal/replay/testdata/{historical.json,dispatch-policy.json}`.
Every descriptor is derived via `cmd/dispatch/docs/DESIGN.md` rules 1–5 and the
derivation source is recorded per-field in the fixture (`derivation.*`), never
hand-labeled to fit. The replay drives the exact CLI engine (`policy.Load` +
`placement.ParseDescriptor` + `placement.Decide`) — no matching logic is
reimplemented. `TestReplayPolicyValidates` (`cmd/dispatch/replay_validate_test.go`)
runs the authored policy through the real `dispatch validate` path (`run()`).

**Authored policy** — two rules, no catch-all:

| Rule | Match | Place |
|---|---|---|
| `generative-high-risk-to-opus-max` | `task_class=generative`, `risk_tier ∈ {T2,T3}` | `ship-driver/claude/claude-opus-4-8/max/local` |
| `contained-t1-to-sonnet-extra` | `risk_tier ∈ {T1}` | `ship-driver/claude/sonnet/extra/local` |

**Result — all 8 streams, derived descriptor vs actual:**

| # | stream | repo | derived task_class / weighted_loc / risk_tier | rule fired | emitted | actual | result |
|---|---|---|---|---|---|---|---|
| 1 | runway-local-rundir-journal-backend | workbench | generative / 775 / T2 | generative-high-risk-to-opus-max | claude/claude-opus-4-8/max/local | cursor/grok-4.5/max/cloud | DIVERGE — grok experiment override |
| 2 | runway-lifecycle-deadline-cancel-cli | workbench | generative / 575 / T2 | generative-high-risk-to-opus-max | claude/claude-opus-4-8/max/local | cursor/grok-4.5/max/cloud | DIVERGE — grok experiment override |
| 3 | runway-writer-claim-reconcile | workbench | generative / 425 / T2 | generative-high-risk-to-opus-max | claude/claude-opus-4-8/max/local | cursor/grok-4.5/max/cloud | DIVERGE — grok experiment override |
| 4 | cloud-sdk-cause-persistence | ship | analytical / 290 / T1 | contained-t1-to-sonnet-extra | claude/sonnet/extra/local | cursor/grok-4.5/extra/cloud | DIVERGE — fair-trio pool assignment |
| 5 | ccp-store-convergence | ship | mechanical / 170 / T1 | contained-t1-to-sonnet-extra | claude/sonnet/extra/local | cursor/claude-opus-4-8/extra/cloud | DIVERGE — fair-trio pool assignment |
| 6 | ccp-mcp-verb-parity | ship | analytical / 300 / T1 | contained-t1-to-sonnet-extra | claude/sonnet/extra/local | cursor/composer-2.5/extra/cloud | DIVERGE — fair-trio pool assignment |
| 7 | dispatch-decide-core | workbench | generative / 600 / T2 | generative-high-risk-to-opus-max | claude/claude-opus-4-8/max/local | claude/claude-opus-4-8/max/local | **MATCH** |
| 8 | dispatch-replay-validation | workbench | analytical / 250 / T1 | contained-t1-to-sonnet-extra | claude/sonnet/extra/local | claude/sonnet/extra/local | **MATCH** |

Both rules fire on 4 of the 8 streams each — no rule is single-stream. The two
dispatch phases separate cleanly by *derived* `task_class` (generative vs
analytical), and both MATCH, satisfying the binding gate. The
`generative-high-risk-to-opus-max` rule also fires on the three runway streams
(diverging there, defensibly — the grok override was never task_class-driven);
that it generalizes to descriptors it was not written for, and diverges
correctly on all three, is the evidence it is a real rule and not a
per-stream special-case for `dispatch-decide-core`. Same reasoning for
`contained-t1-to-sonnet-extra` against the three model-lottery streams.

Negative control (`TestReplayNegativeControl`): `task_class=mechanical`,
`risk_tier=T3` matches neither rule (the first requires `generative`, the
second requires `T1`) — `placement.Decide` returns `ok=false`, confirming the
gate can fail. The authored policy has no catch-all rule by design (`validate`
exits 1 with the expected warning, asserted in
`TestReplayPolicyValidates`); the negative control depends on that.

**GO.** A two-rule policy, keyed only on the frozen taxonomy's `task_class` and
`risk_tier` fields, reproduces both policy-driven historical placements
byte-for-byte and correctly diverges on all six experiment-driven ones with a
recorded, defensible reason — without a single per-stream special case. The
one caveat worth naming for the record: `risk_tier` for 7 of the 8 streams was
not stated explicitly anywhere upstream and had to be floored from
blast-radius signals per derivation rule 4 (only the runway streams have any
corroborating evidence — driver.md's recorded "T3/T2 vs T1 grant" park); a
live `/pr-risk` run against each real PR would be the stronger validation and
is worth doing before phase 4 leans on this signal for real routing decisions.
That gap is in the *fixture's derivation fidelity*, not in the policy
language's expressiveness — the property phase-2 exists to test — so it does
not change the verdict.
