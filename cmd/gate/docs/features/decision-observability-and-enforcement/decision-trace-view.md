**Status**: draft
**Owner**: @michael
**Date**: 2026-07-13
**Related**: dossier task `decision-trace-view` (id: `tsk_01KXDYJEYB6FCN8KV2ZCBZVZD1`, workbench project, talk-readiness phase)

# Decision-trace view: static rendering of a frozen run's decision graph — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `docs/demo/trace-view.html` | ~400 | 400 |
| Docs/fixture | `docs/demo/README.md`, `docs/demo/fixture.json` | ~50 | 50 |
| **Total** | | | **~450** |

Band: **ideal** per repo's PR sizing convention.

**Depends on**: `explain-json-projection` (must land first — this consumes its JSON shape).

## Goal

The strongest visual explanation of gate is a run’s causal chain — evidence → verifier verdicts → reduced verdict → judgment → action, especially the case where the judge passes but the grant ceiling still parks the action. Today that story only exists as `explain` CLI text. Build a self-contained static HTML decision-trace view a room can read.

## Behavior / fix

A static view under `docs/demo/`, renderer only — no new Go code, no gate verbs, storeless/read-only:

- One HTML file (inline CSS/JS, no external requests, no server) that renders an `explain -json` document as a top-to-bottom decision graph: nodes grouped by kind, parent edges drawn, verdict nodes showing producer/decision/tier/why, findings expandable, the final action/park highlighted with the grant ceiling that produced it.
- Loads its data from an embedded JSON fixture (a frozen real run pasted in at build/freeze time) — deterministic replay, works with Wi-Fi off; also accepts a pasted/dropped JSON document to render any other run.
- A short `docs/demo/README.md`: how to freeze a run (`gate explain -run X -json > fixture`) and re-embed.

Keep it sharp and small; this is a demo asset with a durable life, not a UI platform (the multi-source dashboard job was axed with controlroom, 2026-07-17; this static view is the only render surface).

## Acceptance

- Opening `trace-view.html` from disk, offline, renders the embedded frozen run as a decision graph with parent edges and a highlighted terminal action/park.
- The ceiling-park story is legible at a glance: judgment says pass, action node shows parked with the grant tier that refused it (given a fixture containing that case).
- Rendering a different `explain -json` document via paste/drop works without editing the file.

## Test plan

Manual: open from disk offline, render the fixture + one other frozen run; verify no network requests (devtools). If a Playwright harness is trivial to point at a file URL, one smoke assert on node count — do not build test infra for this.

## Non-goals

Live reads of the state dir; any server; control-room integration; audit visualization; slide styling (deck work is ops, not this repo).
