**Status**: draft
**Owner**: @michael
**Date**: 2026-07-22
**Related**: dossier task `console-p0-actionable-docket` (id: `tsk_01KY5KABJA6J1A64FYBVYB9M80`, workbench project, phase `operator-console-p0-docket`); binding contract: [docs/features/operator-console/spec.md](../operator-console/spec.md) §10.1 (P0)

# Gate `next -json` projects by PR subject; console renders a clickable docket — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | gate `next`/projection code under `cmd/gate/`, console rendering under `cmd/console/` | ~280 | 280 |
| Tests | projection + rendering suites | ~350 | 175 |
| **Total** | | | **~455** |

Band: **ideal** per repo's PR sizing convention (spec's own estimate: 300–500 wLOC).

## Goal

Gate's parked-run inbox reduces by run ID, so one PR judged across several runs shows stale duplicate "needs attention" rows keyed by opaque `run_…` ids, and actionable parks are indistinguishable from unattributable legacy runs. Make "what needs me?" true before any action sits on it. Read-only, pre-gate — no mutation, no security-proof dependency; every action phase (P1+) sits on this.

**The full P0 contract is the merged TDD — `docs/features/operator-console/spec.md` §10.1. That section is binding; this spec scopes and pins its two review-settled decisions but does not replace it. Read §10.1 before writing code.**

## Behavior / fix

**Gate projection** (`cmd/gate`, the `next -json` path) — §10.1 "Gate projection" items 1–7 verbatim, with two decisions pinned:

- **"Newest terminal artifact" = latest position in the append-only log — chain order, never artifact timestamps.** Clocks skew between writers; the hash chain is the total order gate authenticates. Ties cannot exist.
- **Live-read mode does one lookup per distinct `repo#number`** (bounded by docket size, never per run); rate-limited/failed lookups fail visible as `unknown`, never silently dropped. Default projection stays deterministic and offline.

Plus: subject recovery from every artifact shape gate has emitted; per-run reduction preserved before subject reduction; subject-less legacy runs in a separate inspectable collection, excluded from "needs attention"; JSON fields additive; text output leads with `repo#number`.

**Console** (`cmd/console`) — §10.1 "Console docket" items 1–5: prominent clickable `repo#number` + title, run ID demoted to diagnostic; **copyable, fully-resolved judge/explain commands** (every id and `-state` filled in); main count from actionable PR-backed rows only; collapsed secondary section for legacy rows; escaping/loopback/host-pin/CSP posture and read-only routes preserved. Console still shells the gate binary — no gate imports (boundary law; hygiene CI enforces).

## Acceptance

Two parked runs for one PR collapse to the newest (chain order); an action in a newer run suppresses an older parked run for the same PR; subject recovery works from legacy verdict/evidence shapes; unattributable runs are counted separately and never inflate "needs attention"; console shows clickable `repo#number` rows with copyable resolved commands; live mode does ≤1 lookup per subject and renders failures as `unknown`.

## Test plan

§10.1's test list verbatim: projection collapse/suppression/recovery/separation/deterministic-ordering; console link/title/secondary-ID/legacy-section rendering; `gofmt -l . && go vet ./... && golangci-lint run ./... && go test ./...`. Dogfood: run the built `gate next -json` against the operator's live state dir — already-resolved duplicate PRs disappear, genuinely-latest parked PRs remain.

## Non-goals (P0 scope walls)

Any mutating route or action UI (P1+), Judge enablement machinery, live-read as the default, schema breaks to existing JSON consumers, gate imports in console.
