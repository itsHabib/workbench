**Status**: draft
**Owner**: @itsHabib
**Date**: 2026-07-05
**Related**: dossier task `align-tracelens-verdict-schema` (id: `tsk_01KWTQ0HEZQMFYXT1WCP4D42A3`), gate project `schema-alignment` phase
**Repo**: itsHabib/tracelens (private) — **runtime: cloud**

# Align tracelens to the gate verdict schema — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `analyze.go` (Verdict type, split Health→Decision+Tier, Producer), `cmd/tracelens/ship.go` (gate on Decision) | ~150 | 150 |
| Tests | `analyze_test.go`, `cmd/tracelens/ship_test.go` | ~110 | 55 |
| **Total** | | | **~205** |

Band: **ideal** per gate's PR-sizing convention.

## Goal

tracelens today emits a `Report` whose `Health` field (`"healthy"|"degraded"|"pathological"`) is *derived from* the maximum finding `Severity` — a single axis that conflates "may this proceed" with "how bad is it." Findings carry no producer identity (the detector name lives in the free-text `Kind`), and there is no escalation/question concept. Bring tracelens's verdict output onto the **gate contract**: `Decision` and `Tier` as orthogonal axes, a structured `Producer{class,impl}`, and an escalate decision that carries the full reasoning as a question — while keeping the `ship` exit-code gate behaving exactly as it does today (pathological trips the gate; healthy and degraded pass).

**gate's `verify.Verdict` is `internal/` (not importable across modules), so tracelens mirrors the contract in its own types.** The target schema is reproduced verbatim below; match field names, JSON tags, and semantics so the two are byte-comparable.

## Target schema (gate `verify.Verdict`) — mirror this exactly

```go
// Producer identifies who stands behind a verdict. Class carries the ladder
// semantics; Impl names the specific implementation for provenance only —
// nothing may branch on Impl.
type Producer struct {
	Class string `json:"class"`
	Impl  string `json:"impl,omitempty"`
}

const (
	ClassCode     = "code"
	ClassLocal    = "local-model"
	ClassJudgment = "judgment"
)

// Decisions, worst to best: block > escalate > pass.
const (
	DecisionBlock    = "block"
	DecisionEscalate = "escalate"
	DecisionPass     = "pass"
)

// Verdict — Decision and Tier are deliberately orthogonal axes: decision says
// who may proceed; tier says who must approve.
type Verdict struct {
	Subject    Subject   `json:"subject"`
	Source     string    `json:"source"`
	Producer   Producer  `json:"producer"`
	Decision   string    `json:"decision"`
	Tier       string    `json:"tier"`
	Confidence float64   `json:"confidence"`
	Findings   []Finding `json:"findings,omitempty"`
	Why        string    `json:"why"`
}

type Finding struct {
	Title      string  `json:"title"`
	Severity   string  `json:"severity,omitempty"`
	Locus      string  `json:"locus,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

type Subject struct {
	Repo    string `json:"repo"`
	Number  int    `json:"number"`
	HeadSHA string `json:"head_sha,omitempty"`
}
```

Tier values are the strings `"T0" "T1" "T2" "T3"`; **an unknown tier ranks highest (fail closed)**. Ladder law (from gate's `Reduce`): worst decision wins, max tier wins, min confidence carries; a local-model producer may pass or escalate but never block; unknown producer classes are rejected. tracelens's analyzer is deterministic `code`, so the local-can't-block rule doesn't bite here — but the type must still carry Producer so the verdict is comparable to gate's.

## Current state (what exists today)

From `analyze.go`:

```go
type Severity int
const ( Info Severity = iota; Warn; Critical )       // custom MarshalJSON → "INFO"/"WARN"/"CRITICAL"

type Finding struct {                                 // no JSON tags
	Kind      string      // "loop"|"redundancy"|"retry_storm"|"cost_hotspot"|"stuck"
	Severity  Severity
	Summary   string
	Steps     []int
	WastedUSD float64
	Repair    string
}

type Report struct {                                  // no JSON tags
	Steps     int
	TotalUSD  float64
	WastedUSD float64
	Health    string       // "healthy"|"degraded"|"pathological", DERIVED from max severity
	Headline  string
	Findings  []Finding
}
```

- `buildReport` (`analyze.go`) is the policy layer that assigns `Health`: any `Critical` finding ⇒ `"pathological"`; else any `Warn` ⇒ `"degraded"`; else `"healthy"`.
- `gateCode` (`cmd/tracelens/ship.go`): `Health == "pathological"` ⇒ exit 1, else exit 0. Contract on `shipMain`: "0 healthy or degraded, 1 pathological, 2 error." Pinned by `TestGateCode`.
- Detector→severity: Loop=Critical, RetryStorm=Critical, Redundancy=Warn, Stuck=Warn, CostHotspot=Info. (RetryStorm + CostHotspot are dormant on cursor ship traces — no cost/token telemetry.)

## Behavior / what to build

### 1. Add the mirrored verdict types
Introduce `Producer`, the `Decision`/`Class` const blocks, and a `Verdict` type in `analyze.go` matching the target schema above (JSON tags included). Keep `Report` as the internal analyzer result if convenient, but the **emitted** verdict (the `-json` surface and the value `gateCode` consumes) must be a `Verdict`.

### 2. Split the derived `Health` into orthogonal `Decision` + `Tier`
Replace the single `Health` axis with two independent fields, both computed in the policy layer (rename/extend `buildReport`):

| tracelens today (max severity) | → `Decision` | → `Tier` |
|---|---|---|
| pathological (a Critical finding) | `block` | `T3` |
| degraded (a Warn, no Critical) | `escalate` | `T2` |
| healthy (Info only, or none) | `pass` | Info present → `T1`, else `T0` |

Decision and Tier are stored as separate fields — do **not** re-derive one from the other at read time. (They correlate in tracelens because one analyzer sets both, but the contract is that the *type* has orthogonal axes, as gate's does.)

### 3. Structured `Producer` on the verdict
The run-level verdict is produced by deterministic detectors, so `Producer{Class: "code", Impl: "tracelens"}` (or `Impl: "tracelens/cursor"` to record the dialect). Per-finding, map the detector `Kind` into `Finding.Title`/`Finding.Locus` (gate's `Finding` has no producer field — producer lives on the `Verdict`). Suggested finding mapping: `Summary`→`Title`, `Severity.String()`→`Severity`, the step indices→`Locus` (e.g. `"steps 3,4,5"`). Preserve `WastedUSD`/`Repair` as extra fields on tracelens's own finding type if you want to keep them — but the gate-shaped `Finding` slice on the verdict is the aligned surface.

### 4. Escalate carries the full question
When `Decision == escalate` (degraded), `Verdict.Why` must hold the **full aggregated reasoning** — the headline plus each contributing finding's summary — not a bare flag. This mirrors gate, where the escalation's `question` field carries `reduced.Why`. A downstream reader must be able to act on `Why` alone.

### 5. Keep the exit-code gate semantics identical
Rekey `gateCode` on the new axis **without changing behavior**: `Decision == DecisionBlock` ⇒ exit 1, else exit 0. This is exactly today's mapping (only pathological/block trips the gate; escalate/degraded and pass/healthy both exit 0). The `2` (error) path from arg/parse/IO failure is unchanged.

## Acceptance

1. tracelens emits a `Verdict` (mirrored gate shape, correct JSON tags) from `-json` and from the `ship` path; `Decision` and `Tier` are independent fields.
2. `gateCode` (or its renamed equivalent) trips the gate **iff** `Decision == block`; healthy→pass and degraded→escalate both exit 0. Existing gate behavior is preserved bit-for-bit on the current detector set.
3. Every emitted verdict carries `Producer{Class:"code", Impl:…}`.
4. An escalate verdict's `Why` carries the full aggregated reasoning (headline + finding summaries).
5. An unknown/garbage tier value ranks as most-restrictive if any tier-composition helper is added (fail closed) — match gate's `tier.Rank` default.

## Test plan

- `TestGateCode` (rewritten): `{block:1, escalate:0, pass:0}` — the direct analogue of today's `{pathological:1, degraded:0, healthy:0}`.
- `TestBuildVerdictDecisionTierOrthogonal`: a Critical finding ⇒ `Decision==block && Tier=="T3"`; a Warn-only run ⇒ `Decision==escalate && Tier=="T2"`; Info-only ⇒ `Decision==pass && Tier=="T1"`; empty ⇒ `Decision==pass && Tier=="T0"`.
- `TestVerdictCarriesProducer`: every produced verdict has `Producer.Class=="code"`.
- `TestEscalateCarriesQuestion`: a degraded run's `Why` contains the headline and each finding summary (non-empty, not a flag).
- Loop-detected trace still gates to exit 1 (regression: the one detector that fires on cursor ship traces and produces a block).

## Non-goals

- **Do not** build gate's artifact log, hash chain, `Reduce` over multiple producers, or the escalation *artifact*. tracelens has one deterministic producer; this task aligns the **verdict type + exit gate**, not gate's whole state machine.
- **Do not** import gate (its verify package is `internal/`); mirror the contract in tracelens's own types.
- **Do not** revive the dormant RetryStorm/CostHotspot detectors or change detector thresholds — telemetry unlock is a separate parked task (`docs/NEXT.md`).
- **Do not** add the claude/codex dialects.
