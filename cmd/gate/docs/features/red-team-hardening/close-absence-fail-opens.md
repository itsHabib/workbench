**Status**: draft
**Owner**: @michael
**Date**: 2026-07-06
**Related**: dossier task `close-absence-fail-opens` (id: `tsk_01KWW33QAEYS58NWG0CF54JTZM`), [docs/FOLLOWUPS.md](../../FOLLOWUPS.md)
**Model/effort**: opus / max

# Close the absence-of-signal fail-opens in the verifier ladder — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `internal/verify/verify.go` (Reduce), `internal/verify/reviews.go`, `internal/verify/readiness.go` | ~30 | 30 |
| Tests | `internal/verify/verify_test.go` (+ reviews/readiness cases as needed) | ~90 | 45 |
| **Total** | | | **~75** |

Band: **amazing** per repo's PR sizing convention.

## Goal

Absence of signal must never read as green — the classic gate fail-open, the same shape as rooms#47's `overlay_active:false → exit 0` that the portfolio's adversarial-gate rule exists to catch. Three independent places in the ladder currently produce **pass** from **nothing observed**. Close all three, and move the load-bearing invariant (a code floor must be present) out of `cmd/gate/main.go`'s call *order* and into `Reduce` itself, so a caller that composes fewer rungs — or a bug that loads zero verdicts — cannot silently auto-pass.

## Behavior / fix

Three fixes. The first is the keystone.

### 1. Floor-presence invariant lives in `Reduce` (`internal/verify/verify.go`)

Today `Reduce(subject, nil)` returns `pass`/`T0`/"all verifiers pass", and floor-presence is enforced only by the order in which `runGate` calls `Readiness`/`Floor`/`Reviews`. Move the invariant into the reducer:

- **Require at least one code-class verdict** (`Producer.Class == ClassCode`) in the input set. If none is present, `Reduce` returns a **non-pass** decision — specifically `DecisionEscalate`, with a `Why` such as `"no code-floor verdict present — cannot verify readiness"`. This mirrors how `Readiness` already escalates on an empty CI rollup ("never-triggered checks must not read as green").
- **This check dominates the judgment carve-out.** A `judgment` verdict must **not** be able to substitute for a missing floor: if the set has a judgment-pass but no code verdict, the result is still escalate, never pass. Order the reducer so floor-absence is resolved before the `judged != nil → out.Decision = judged.Decision` step can turn it green.
- `Reduce(nil)` is the degenerate case of the above → **escalate** (not pass), `err == nil`.
- A code-class **block** must still dominate everything (the existing early `return` on `out.Decision == DecisionBlock` stays). Floor-presence only gates the *pass* path.

Keep `Reduce` order-independent (its docstring promises "Verdict order does not matter"): compute floor-presence by scanning the set, not by positional assumptions.

### 2. Empty review panel escalates (`internal/verify/reviews.go`)

In `Reviews`, the `processed == 0` branch currently leaves `Decision` at pass with `"no bot findings to consolidate"`. A PR opened minutes ago — CI green, no reviewer has run yet — must not read as reviewed. Change `processed == 0` to **escalate** (`DecisionEscalate`) with a `Why` naming the empty panel (e.g. `"no bot review comments yet — cannot consolidate a panel"`). This stays within the ladder law: the reviews rung is `ClassLocal`, which may pass or escalate but never block — escalate is the correct fail-closed.

### 3. Empty `reviewDecision` escalates (`internal/verify/readiness.go`)

`Readiness` treats an empty `reviewDecision` (`""`) as "the repo requires no reviews" and contributes nothing — so on an unprotected repo the GitHub-level review status silently reads as satisfied. Make an empty `reviewDecision` **escalate** for non-merged subjects, mirroring the existing empty-CI-rollup escalation:

- Preserve the `MERGED` exemption exactly as it is for the block checks — a historical/backtest PR has no live review decision and must not newly escalate on that basis.
- Hard blocks still dominate: if `Readiness` already accumulated `blocks` (draft, conflict, unknown mergeability, non-approved review decision, red checks), it returns `block` as today. The empty-`reviewDecision` escalate only fires when there are no hard blocks — same precedence as the empty-CI escalate. If both empty-CI and empty-`reviewDecision` hold, a single escalate verdict naming the reason(s) is sufficient.

## Acceptance

- `Reduce(nil)` returns a non-pass decision (`DecisionEscalate`), `err == nil`.
- `Reduce` over a set with verdicts but **no** `ClassCode` verdict returns non-pass (escalate), **even when a `ClassJudgment` pass verdict is present** — a judgment cannot launder a missing floor.
- `Reduce` over the normal healthy set (code readiness + code floor + local reviews, all pass) still returns `pass`.
- An empty review panel (`processed == 0`) makes `Reviews` produce `escalate`, not pass.
- An empty `reviewDecision` on a non-merged subject makes `Readiness` produce `escalate` (absent any hard block); a `MERGED` subject with empty `reviewDecision` does **not** newly escalate.
- The floor-presence invariant is enforced inside `Reduce`, pinned by a test that does not rely on `cmd/gate/main.go`'s call order.

## Test plan

`go test ./internal/verify/...` with new cases:
- `Reduce(nil)` → `Decision != pass` (escalate).
- `Reduce([judgment-pass only])` → `Decision != pass` (floor absent).
- `Reduce([code-pass, local-pass])` → `pass` (healthy floor present, unchanged).
- `Reduce([code-block, judgment-pass])` → `block` (code block still dominates).
- `Reviews` over comments evidence with zero bot comments → `escalate`.
- `Readiness` over a view with empty `reviewDecision`, state != MERGED, otherwise green → `escalate`; same view with state == MERGED → not escalate.

Then `go vet ./...`, `gofmt -l .` clean, `golangci-lint run ./...`, full `go test -race ./...`.

## Non-goals

- Driver wiring (separate phase, explicitly gated on this landing plus the adversarial pass).
- Capability/minting changes and the tamper-chain hardening — sibling tasks in this phase.
- **`cmd/gate/main.go` is intentionally left untouched.** The behavior change lives entirely in `internal/verify/*`; `act` already maps `escalate → exit 2 (parked)`, so no caller change is needed. Keeping this task out of `main.go` preserves parallel-safety with the tamper task (which edits `newEnv`). If a stale comment in `runGate` claims call-order enforces the floor, updating that *comment* is the only acceptable `main.go` edit.
