**Status**: draft
**Owner**: @itsHabib
**Date**: 2026-07-05
**Related**: dossier task `align-triage-verdict-schema` (id: `tsk_01KWTQ0F9FR6KJ054TXDFW22KB`), gate project `schema-alignment` phase
**Repo**: pers/triage (local-only, no GitHub remote) — **runtime: local commit, no PR**

# Align triage to the gate verdict schema — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | new `internal/verify/verify.go` (Verdict, Producer, Decision, Reduce), adapter floor.Result→Verdict, `cmd/triage-floor` emit | ~230 | 230 |
| Tests | `internal/verify/verify_test.go` (ladder + fail-closed) | ~160 | 80 |
| **Total** | | | **~310** |

Band: **ideal/stretch** per gate's PR-sizing convention. If it runs long, the ladder/reducer + fail-closed tests are the must-ship core; the `cmd` emit change can follow.

## Goal

triage today types **only the deterministic floor** (`floor.Result`, `floor.Signal`, a `Tier` int enum T0–T3). The *composite* verdict — floor + agent advisory reconciled into a final tier + route — has **no Go type at all**; it exists only as untyped JSON emitted by the `/pr-risk` skill, in **three divergent shapes** (spec §7's `floor_tier/agent_tier/agent_escalation{from,to,why}/final_tier`, the runtime log's flat `floor/escalation:"T3"/final/why/signals`, and spec §6's `{floor,proposed,why}`). Escalation is a bare string tier, not a carried question.

Bring triage's verdict output onto the **gate contract**: a typed `Verdict` with `Decision` ⊥ `Tier`, a structured `Producer{class,impl}` on every verdict, a `Reduce` that composes floor + advisory under gate's ladder law, and escalations that carry the full question rather than a bare tier string. **Explicitly verify the reducer's fail-closed-on-unknown behavior holds after the change** (the task's named acceptance gate).

## Target schema (gate `verify.Verdict`) — mirror this

Same contract tracelens aligns to (kept in sync). Mirror in a new `internal/verify` package (gate's verify is `internal/`, not importable):

```go
type Producer struct {
	Class string `json:"class"`          // "code" | "local-model" | "judgment"
	Impl  string `json:"impl,omitempty"` // provenance only — Reduce must never branch on it
}
const ( ClassCode = "code"; ClassLocal = "local-model"; ClassJudgment = "judgment" )

// Decisions, worst to best: block > escalate > pass.
const ( DecisionBlock = "block"; DecisionEscalate = "escalate"; DecisionPass = "pass" )

type Verdict struct {
	Subject    Subject   `json:"subject"`
	Source     string    `json:"source"`
	Producer   Producer  `json:"producer"`
	Decision   string    `json:"decision"`
	Tier       string    `json:"tier"`     // "T0".."T3", unknown ranks highest (fail closed)
	Confidence float64   `json:"confidence"`
	Findings   []Finding `json:"findings,omitempty"`
	Why        string    `json:"why"`
}
type Finding struct { Title string `json:"title"`; Severity string `json:"severity,omitempty"`; Locus string `json:"locus,omitempty"`; Confidence float64 `json:"confidence,omitempty"` }
type Subject struct { Repo string `json:"repo"`; Number int `json:"number"`; HeadSHA string `json:"head_sha,omitempty"` }
```

gate's ladder (mirror in `Reduce`): worst decision wins, **max tier wins, min confidence carries**; a `local-model` producer may pass or escalate but **never block** (`ErrLocalBlock`); a `code` block is final (judgment can't override it); **unknown producer class is rejected** (`ErrUnknownProducer`) and **unknown tier ranks highest** — both fail closed.

## Current state (what exists today)

`internal/floor/floor.go` — the only place verdict types live:

```go
type Tier int
const ( T0 Tier = iota; T1; T2; T3 )        // String() → "T0".."T3" — already matches gate's tier strings

type Signal struct {                         // one fired rule
	Name  string `json:"signal"`
	Tier  Tier   `json:"-"`
	TierS string `json:"tier"`
	Why   string `json:"why"`
}
type Result struct {                         // "the floor classification"
	Floor   Tier     `json:"-"`
	FloorS  string   `json:"floor"`
	Signals []Signal `json:"signals"`
	Files, Added, Removed int
}
func Classify(d Diff) Result                 // reducer: floor = max over fired signals; fails closed
```

- `Classify` already **fails closed**: an unmatched code file floors at `T1` (`internal-change`), an unmatched non-code path floors at `T1` (`unknown-path`) — never auto-`T0`.
- Producer is **implicit in the package** (`internal/floor` is the deterministic engine); the agent advisory has no code home (it's the `/pr-risk` skill reading the diff + `RUBRIC.md`).
- Composition `final = max(floor, escalation)` is done **in skill prose**, not code. Escalation semantics: the agent **may only raise, never lower**.
- Module `github.com/itsHabib/triage`, `go 1.26`. Tier→route (prose only): T0 auto / T1 peer / T2 owner / T3 owner+adversarial+defense.

## Behavior / what to build

### 1. New `internal/verify` package mirroring gate
Add `Verdict`, `Producer`, `Finding`, `Subject`, the `Decision`/`Class` const blocks, and the sentinel errors `ErrLocalBlock` / `ErrUnknownProducer`. Reuse the existing tier strings (`"T0".."T3"`); add a `tierRank` with `default → highest` (unknown fails closed), mirroring gate's `tier.Rank`.

### 2. Adapter: `floor.Result` → `Verdict`
The deterministic floor is a **`code`** producer emitting **tier-with-pass** (per gate's DESIGN.md example — a floor classifies risk, it does not block):

```
Verdict{
  Producer: {Class: "code", Impl: "triage-floor"},
  Decision: pass,
  Tier:     result.FloorS,               // T0..T3
  Findings: []Finding from result.Signals (Name→Title, TierS→Severity; Locus is for location, so a floor signal sets none, and each signal's Why joins the verdict-level Why below — gate's Finding carries no rationale field),
  Why:      "deterministic floor: <joined signal reasons>",
  Confidence: 1.0,
}
```

### 3. Advisory verdict shape (the agent producer)
Define the Go type the `/pr-risk` skill's advisory output deserializes into — a **`local-model`** producer that may raise the tier and escalate, never block (matching triage's "agent may only raise"):

```
Verdict{ Producer: {Class:"local-model", Impl:"<model, e.g. qwen2.5:7b>"},
         Decision: pass | escalate,      // escalate iff it raises the tier
         Tier: <proposed T0..T3>, Why: "<full reasoning>", Confidence: <0..1> }
```

### 4. `Reduce([]Verdict) (Verdict, error)` — the ladder
Replace the skill's prose `final = max(floor, escalation)` with gate's reducer semantics:
- monotone-max tier, min confidence;
- `code` block ⇒ final block (triage's producers won't emit block today, but the rule holds);
- a `local-model` verdict with `Decision==block` ⇒ return `ErrLocalBlock` (structural refusal);
- an unknown producer class ⇒ return `ErrUnknownProducer` (do **not** silently drop — that's a fail-open);
- an unknown tier ⇒ ranks highest (`T3`) via `tierRank` default;
- if any producer escalated (and nothing blocked), composed `Decision = escalate`.

This is where triage's "final = max(floor, escalation)" becomes a typed, fail-closed reducer.

### 5. Escalation carries the full question
The composed verdict's `Why` aggregates every escalating producer's reasoning (`source: why`, joined) — the same content gate writes into an escalation artifact's `question` field. Replace the bare `escalation: "T3"` string with `Decision==escalate` + a `Why` a reader can act on alone.

### 6. Emit the aligned shape
`cmd/triage-floor` emits a `Verdict` (gate-aligned JSON) instead of the raw `Result`. The `/pr-risk` skill's composite log should adopt the same shape going forward (one canonical schema, replacing the three divergent ones).

## Acceptance

1. A new `internal/verify` package defines the mirrored `Verdict`/`Producer`/`Decision`/`Tier` contract and a `Reduce([]Verdict)(Verdict,error)`.
2. `floor.Result` maps to a `Verdict{Producer:{code,triage-floor}, Decision:pass, Tier:floor}`; `triage-floor` emits gate-aligned JSON.
3. **Fail-closed holds (named gate):** `Reduce` returns `ErrUnknownProducer` on an unknown class and ranks an unknown tier as `T3`; a `local-model` block returns `ErrLocalBlock`. Pinned by tests.
4. Escalation is a `Decision==escalate` verdict whose `Why` carries the full reasoning — no bare tier-string escalation remains on the emitted surface.
5. `Decision` and `Tier` are independent fields; a passing floor still carries its tier.

## Test plan

Mirror gate's `verify_test.go` invariants against triage's reducer:
- `TestReduceUnknownProducerClassFailsClosed` — unknown class ⇒ `ErrUnknownProducer`.
- `TestReduceUnknownTierFailsClosed` — `Tier:"garbage"` ranks as T3.
- `TestReduceLocalCannotBlock` — a `local-model` block ⇒ `ErrLocalBlock`.
- `TestReduceAdvisoryRaisesTierAndEscalates` — floor `pass@T1` + advisory `escalate@T3` ⇒ composed `escalate@T3`, `Why` contains both reasons.
- `TestReduceClassIgnoresImpl` — an `impl` suffix can't smuggle a local block past the ban.
- `TestFloorResultToVerdict` — adapter maps floor tier + signals faithfully; Decision is `pass`.

## Non-goals

- **Do not** build gate's artifact log, hash chain, grants, or escalation *artifacts* — triage aligns the **verdict + reducer types**, not gate's state machine.
- **Do not** import gate (verify is `internal/`); mirror the contract.
- **Do not** rewrite historical `labels/mismatches.jsonl` entries — new verdicts emit the aligned shape; the append-only oracle keeps its history.
- **Do not** change `RUBRIC.md`, the signal→tier rules, or the tier→route table — this is a schema/reducer alignment, not a policy change.
- **Do not** wire the agent advisory into Go (it stays skill-side); this task only defines the typed shape its output lands in.
