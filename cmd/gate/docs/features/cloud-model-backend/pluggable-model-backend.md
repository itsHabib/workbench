**Status**: draft
**Owner**: @michael
**Date**: 2026-07-13
**Related**: dossier task `pluggable-model-backend` (id: `tsk_01KXFJ62JDT7R56E7VQVPVVSHG`), phase `cloud-model-backend`, Phase 0 eval verdict (Haiku 92.2%/95.7% vs 7B 80.4%/90.2%)

# Pluggable Model backend + cloud (Anthropic) path for gate's rungs — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `internal/verify/model.go` (new), `reviews.go`, `ciclassify.go`, `cmd/gate/main.go` | ~180 | 180 |
| Tests | `internal/verify/model_test.go` (fake Anthropic httptest), backend-selection test | ~100 | 50 |
| **Total** | | | **~230** |

Band: **ideal / stretch** — this is an interface extraction + a new transport with a subtle response-shape adapter, not a one-liner; correctness-risk is real (a wrong adapter silently corrupts classifications), so it earns careful review.

## Goal

Make gate's two model-backed rungs call the model through a small `Model` interface, and add a cloud (Anthropic) backend behind a flag, so gate no longer requires a local Ollama and can run in stock hosted CI. This is the prerequisite to arming the dormant canary enforcement (`GATE_ENFORCE`, shipped dormant in #17). Phase 0 proved a cloud model (Haiku 4.5) *exceeds* the local qwen2.5:7b on the frozen ci-classify eval, so the swap is an improvement, not a regression.

## Behavior / fix

### The seam
Two rungs today POST to Ollama with byte-identical request/response skeletons:
- **Rung A — review consolidation** `internal/verify/reviews.go`: `extractOne` (reviews.go:127) POSTs at reviews.go:142; system `extractPrompt` (reviews.go:24), schema `extractSchema` (reviews.go:27), result `extraction` (reviews.go:38), parse `message.content` → `json.Unmarshal` (reviews.go:161).
- **Rung B — ci-classify advisory** `internal/verify/ciclassify.go`: `ciAdvise` (ciclassify.go:167) POSTs at ciclassify.go:182 (test seam `ciAdvisoryURL` ciclassify.go:158); system `ciPrompt` (ciclassify.go:140), schema `ciSchema` (ciclassify.go:146), result `ciAdvisory` (ciclassify.go:160).
- Shared consts (reviews.go:20-45): `ollamaURL`, `ollamaModel = "qwen2.5:7b"`, `ollamaClient` (http.Client, 3m timeout).

Extract the duplicated skeleton into:

```go
type Model interface {
    // chat sends system+user with a structured-output schema and returns the
    // model's JSON payload as a string, so callers keep their existing
    // json.Unmarshal([]byte(content), &target) unchanged.
    chat(ctx context.Context, system, user string, schema json.RawMessage) (content string, err error)
}
```

### Backends
- **`localModel`** — today's Ollama call, moved verbatim (build map → POST `ollamaClient` → decode `message.content`). Behavior byte-identical; it is the default so nothing changes unless the cloud backend is explicitly selected.
- **`cloudModel`** — raw `net/http` POST to the Anthropic Messages API (`https://api.anthropic.com/v1/messages`, headers `x-api-key`, `anthropic-version: 2023-06-01`, `content-type`), **forced tool-use**: one tool whose `input_schema` is the caller's `schema`, plus `tool_choice: {type:"tool", name:<tool>}`, `temperature: 0`, `max_tokens` sized for the schema. **Response-shape adapter (the load-bearing detail):** Anthropic returns the structured object inside a `content[]` block of `type:"tool_use"` as `.input` (an object) — NOT as a `message.content` string. `cloudModel.chat` must find that block and `json.Marshal(block.Input)` back into a string, so the rung-side unmarshal is identical for both backends. Key read from **env** `ANTHROPIC_API_KEY` (CI-secret shape), never a file baked into the image. Default model `claude-haiku-4-5-20251001`.

### Transport decision — stdlib net/http, no SDK
A single structured POST mirroring the existing Ollama client keeps gate dependency-free (no new dep for CI to vet) and consistent with how gate already calls a model. The stdlib-only constraint (`docs/DESIGN.md:24`, `CLAUDE.md:24`) therefore **stays intact** — no waiver needed. (The official SDK was considered and rejected on merits for a one-call surface, not out of dogma.)

### Selection + plumbing
- `-model-backend local|cloud` flag, default `local` (preserves current behavior). Register on the `gate` verb (`cmd/gate/main.go` cmdGate ~299) or in `commonFlags` (main.go:241) if it should apply to every verb.
- Plumb flag → env → the `verify.Reviews` / `verify.CIClassify` calls (main.go:359/364), either as a `Model` param on those funcs or as package-level state in `internal/verify` set once at startup. Prefer an explicit param (testable) over hidden package state if it doesn't balloon the signatures.

### Producer class
Keep `Class: ClassLocal`; only `Impl` changes (→ the model id, e.g. `claude-haiku-4-5-20251001`). The cloud model occupies the **same** escalate-only advisory rung — it may only pass/escalate, never block — so the ladder law is unchanged. Do **not** introduce `ClassCloud`: that would touch the reducer branches and their pinned tests in `verify.go` for zero semantic gain. `Impl` is provenance-only (nothing branches on it, verify.go:26), so the swap is free. **Open question for reviewers:** the literal class value `local-model` is now location-inaccurate when a cloud backend is active — flag it, but defer any rename to a follow-up (a rename touches every artifact + pinned test). Precedent that gate already talks to Anthropic: `judge.go:44` AutoJudge shells `claude -p` (Class=judgment, Impl=claude-cli).

### Frozen surfaces — do not touch
`ciPrompt` / `ciSchema` / `extractPrompt` / `extractSchema` ship byte-identical to the vendored eval bundle (`.gitattributes` pins the eval files `-text` for the byte-level verbatim-evidence check). The `Model` refactor must leave these string/schema constants unchanged.

## Acceptance

1. `go build ./... && go vet ./... && go test ./...` green; `golangci-lint` clean.
2. Default path unchanged: with no `-model-backend` (or `-model-backend local`), the two rungs behave byte-identically to today (existing `internal/verify` tests still pass untouched, including the `fakeOllama` httptest at ciclassify_test.go:373).
3. `-model-backend cloud` routes both rungs through `cloudModel`; a new `model_test.go` stands up a fake Anthropic endpoint (httptest) returning a `tool_use` block and asserts the adapter re-marshals `.input` so the rung-side `ciAdvisory` / `extraction` unmarshal identically to the Ollama path.
4. **Validation gate (the real bar):** re-run the frozen ci-classify eval through the cloud backend and score with `docs/features/ci-classify/eval/floor-score.ps1`. The runner is external (`itsHabib/local` `cmd/eval`) — reuse the proven Phase-0 runner or a thin `-model-backend cloud` driver to emit `{expected,meta,output}` JSONL over `ci-lines-v2.jsonl`. Result must hold **≥ Phase-0 Haiku (92.2% coverage / 95.7% on-handled)** and never below the shipping bars (≥60% coverage / ≥90% on-handled). Commit the raw scores + a one-paragraph result note under `docs/features/ci-classify/eval/`.

## Test plan

- `TestCloudModelAdaptsToolUseToContentString` — fake Anthropic returns `{content:[{type:"tool_use", input:{...}}]}`; assert `chat` returns the marshalled object string.
- `TestCloudModelSurfacesAPIError` — non-200 / error envelope → wrapped error (fail-closed; the rung escalates rather than trusting garbage).
- `TestReviewsBackendSelection` / `TestCIClassifyBackendSelection` — the selected `Model` is the one invoked; default is local.
- Existing `internal/verify` tests unchanged and green (proves the local path is byte-identical).

## Non-goals

- **Arming** the enforcement (setting `GATE_ENFORCE=true` + branch protection + provisioning a funded CI secret) — stays deferred per operator; this PR only makes the backend *available*.
- Migrating the review rung's per-comment fan-out or the ci-classify floor logic — untouched.
- Any prompt/schema change, or any `ClassCloud` reducer work — explicitly deferred.
- Making the cloud backend the *default* — default stays `local`.
