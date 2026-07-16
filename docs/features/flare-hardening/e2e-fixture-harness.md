**Status**: draft
**Owner**: @michael
**Date**: 2026-07-15
**Related**: dossier task `e2e-fixture-harness` (id: `tsk_01KXGG2F5Q6V0658M1MNJW6WAJ`)

# Workbench cross-plane e2e harness + config-contract tests — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Test source | `cmd/flare/e2e/` (new pkg: harness + e2e test), `cmd/flare/internal/config/config_test.go` (contract test) | ~260 | 130 |
| Fixtures | `cmd/flare/e2e/testdata/` (gate `log.jsonl`, ship `receipts.jsonl`, routes config), `cmd/flare/internal/config/testdata/routes.golden.json` | ~60 | 30 |
| CI | `.github/workflows/ci.yml` (note only if a dedicated e2e job is added) | ~5 | 5 |
| **Total** | | | **~165** |

Band: **ideal**. Test-only + fixtures + a small CI note; **no production source changes** (see mechanism note).

## Goal

Motivated by a real silent failure: flare's shipped `~/.flare/routes.json` drifted ahead of the binary (staged a `type:"slack"` channel with `token`+`channel` fields the then-current binary's `Channel` struct lacked), so `config.Load` — which sets `DisallowUnknownFields()` — rejected it and flare could not start. The notification plane was silently dead ~17h, undetected, and the breaking change was mergeable.

flare has good unit invariants but nothing tests (a) the *actual shipped config shape* against the *current binary*, nor (b) the full producer→sweep→sink path end to end. Build both. The workbench couples planes only through typed artifacts on disk (`evidence → verdict → action → receipt → notify`), so every boundary is a file you can fixture or a delivery you can capture — e2e falls out of the architecture.

## Layer 1 — config-contract test (cheap, catches THIS bug)

Commit a canonical routes fixture that mirrors the **shipped** shape — including the field set the live `~/.flare/routes.json` uses today: `version: 1`, a `gate-log` source, a `ship-receipts` source, a `slack` channel (`type`,`token`,`channel`), a `toast` channel, `routes`, and a non-drop `catch_all`. Use placeholder secrets (e.g. `"xoxb-REPLACE"`) — never a real token.

- `cmd/flare/internal/config/testdata/routes.golden.json` — the fixture.
- A test in `config_test.go` asserts `config.Load(golden)` succeeds and returns the expected version/source-kinds/channel-types. If a future field the shipped config uses is missing from the binary's structs, `DisallowUnknownFields` makes this go **red immediately** — the config schema can never silently lead the binary again.
- The fixture is the frozen contract: when the shipped shape legitimately gains a field, the binary's struct and this fixture move together in one PR.

## Layer 2 — cross-plane e2e with fixture artifacts + capture sink

An e2e test in a new `cmd/flare/e2e` package that drives the **real** producer→sweep→sink path — not a re-implementation of `cycle`:

1. **Seed fixture producer artifacts** into a temp dir:
   - a gate `log.jsonl` containing at least one escalation and one block/escalate verdict, built from `contracts` envelope/verdict types with a valid `prev`/`hash` chain (importing `contracts` is allowed — it is the shared vocabulary; importing another tool's decision logic is not).
   - a ship `receipts.jsonl` containing a `failed`/`cancelled` (park) receipt and a non-page-worthy one, so the harness proves selectivity.
2. **Capture sink (mechanism):** point the routes config's channels at a **`webhook` channel whose URL is an in-process `net/http/httptest.Server`** that records every delivered payload. This captures exactly what would hit Slack/toast with **zero production surface change** — no new channel type in `config`/`notify`. (Rationale: the task floated a dedicated capture channel type; the httptest-webhook route achieves the same capture using only stdlib and keeps the production `notify.Send` switch untouched. Do it this way unless you find a concrete reason a webhook can't observe what you must assert — if so, surface it, don't invent a production `capture` type silently.)
3. **Run the real sweep:** build the flare binary once (e.g. `go build` to a temp path in `TestMain`) and exec `flare sweep -config <fixture-routes.json> -state <tempdir>` as a subprocess, with sources pointed at the seeded fixtures and channels at the httptest server. The `-config` and `-state` flags already exist — no HOME dependency, fully isolated.
4. **Assert deliveries:** exactly the expected page-worthy events reach the capture sink (the escalation, the block/escalate verdict, the park receipt), the non-page-worthy receipt does not, and each lands on the channel its route selects.
5. **Assert idempotence:** run `flare sweep` a second time against the same `-state` dir (unchanged fixtures) → **zero** new deliveries (the journal dedupe / seen-set holds). This is the resweep-never-re-pages invariant, end to end.

## CI / merge-gating

The new tests live under the standard `go test ./...` surface, so the existing CI job (`go test`, `go test -race`, `hygiene`) already gates them — a config/route/channel drift that breaks `config.Load` or the sweep path now fails the required check and **cannot merge**. If the e2e build-and-exec is too slow for the default job, gate it behind a short-mode skip (`testing.Short()`) and add a dedicated non-short e2e step; note the choice in the PR. No new external dependency — stdlib only (`os/exec`, `net/http/httptest`, `encoding/json`, `testing`).

## Acceptance

- `routes.golden.json` mirrors the shipped shape and `config.Load` accepts it; adding a field to the shipped fixture the binary doesn't know makes the contract test fail.
- The e2e seeds fixture gate + ship artifacts, runs the real `flare sweep`, and asserts exactly the expected captured deliveries on the expected channels, with the non-page-worthy receipt excluded.
- A second sweep produces zero new deliveries (idempotent — dedupe holds).
- All of it runs under `go test ./...` (and `-race`); `gofmt`/`go vet`/`golangci-lint`/`hygiene` clean.
- Boundary law respected: the e2e imports `contracts` (shared vocabulary) but no other tool's decision code; it exercises flare through its binary + artifacts, not by importing gate/ship internals.

## Test plan

The deliverable *is* tests. Name them intention-revealingly, e.g. `config_load_accepts_shipped_golden_routes`, `config_load_rejects_unknown_field`, `e2e_sweep_delivers_expected_pages_to_capture_sink`, `e2e_resweep_is_idempotent`.

## Non-goals

- A shared cross-family harness package for gate/driver/tracelens — this proves the pattern on flare first; generalizing is a later task.
- A `gate.yml` external check (future; the in-repo required job is the gate for now).
- Validating the operator's live `~/.flare/routes.json` in CI (it isn't in the repo); the golden fixture is the committed contract. A local smoke check against the live file is optional and must skip cleanly when the file is absent.
- Any change to production `config`/`notify`/`source` code.
