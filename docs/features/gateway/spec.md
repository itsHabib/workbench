# The AI gateway egress seam — Technical Design Document

**Status:** Implemented and validated; Gate A and Gate B passed.
**Owner:** @itsHabib
**Date:** 2026-07-28
**Related:** `docs/DESIGN.md`;
`cmd/gate/docs/features/cloud-model-backend/pluggable-model-backend.md`;
`cmd/gate/docs/features/ci-classify/eval/cloud-eval-results.md`

> **Reviewers — focus areas:** URL joining without losing a gateway path prefix
> (§4.7), the gateway-only ownership of `GATE_CLOUD_MODEL` (§4.4), endpoint
> redaction on transport errors (§4.6), and whether the live eval gate in §11
> is sufficient before rollout.

**Scope:** let workbench's cloud LLM
egress point at an AI gateway instead of a hardcoded `api.anthropic.com`, by
honoring the provider SDKs' standard base-URL environment variables. One touch
point: the cloud `Model` in `cmd/gate/internal/verify`. No new package, no new
backend flag value, and one gate-specific model selector.

**This repo is public and provider-agnostic on purpose.** No gateway hostname, no
vendor product name, no key format, and no absolute helper path appears anywhere
in the code or in this doc. All of that is *operator configuration* supplied at
runtime — see §2. A deployment-specific runbook lives outside this repo.

---

## 1. Problem & hypothesis

Workbench's cloud model path talks straight to `api.anthropic.com` with an
`ANTHROPIC_API_KEY` from the environment. That is one hardcoded egress and one
hardcoded credential shape, and it fails in a common situation: an organization
that fronts model providers with its own authenticated gateway and does not issue
personal API keys to engineers. In that setting `-model-backend cloud` is not
merely inconvenient, it is unusable —
`cmd/gate/docs/features/ci-classify/eval/cloud-eval-results.md` is the standing
evidence, an eval that could not run for want of a key.

An **AI gateway** is the general form of that setup: a single authenticated
endpoint fronting several model providers, where the caller holds a
gateway-issued token rather than a provider key. Many gateways (including the
common self-hosted ones) expose the provider's own wire protocol under a path
prefix, which means the transport workbench already has works against them
unmodified.

So the change needed is **not** a new credential story, **not** a new transport,
and **not** new configuration. Provider SDKs already standardize a base-URL
override next to the API key precisely for this case; the work is to honor those
existing variables and resolve the request URL against them (§2).

The payoff is concrete: gate's cloud rungs become runnable in environments where
they are currently dead, which unblocks the frozen ci-classify eval that has been
waiting on a key since 2026-07-13.

### 1.1 Functional requirements

- Preserve the direct Anthropic path when `ANTHROPIC_BASE_URL` is unset.
- Route `-model-backend cloud` through an absolute `ANTHROPIC_BASE_URL` when set,
  preserving any path prefix while appending `/v1/messages`.
- Keep the Anthropic-native request and response contract unchanged, including
  forced structured output and all existing fail-closed checks.
- Require an explicit `GATE_CLOUD_MODEL` when a gateway is configured; keep
  `cloudModelDefault` for the direct path.
- Fail legibly for invalid configuration and authentication failures without
  exposing the API key or resolved endpoint.
- Refuse gateway redirects so `x-api-key` cannot cross an origin boundary.
- Run the existing frozen ci-classify eval through the gateway with no change to
  the eval harness.

### 1.2 Non-functional requirements

| Quality | Target |
|---|---|
| Compatibility | With `ANTHROPIC_BASE_URL` unset, request URL, headers, model default, response parsing, and gate exit behavior remain unchanged. |
| Security | Neither credential nor resolved endpoint is emitted in logs, errors, verdicts, fixtures, or generated eval artifacts; gateway redirects are never followed. |
| Reliability | Invalid base URLs fail during model construction; 401/403 responses fail without retry; all existing truncation and malformed-response guards remain fail-closed. |
| Performance | No extra network round trip or subprocess; URL, key, and model are resolved once per short-lived gate process. |
| Operability | Configuration errors name the missing or invalid variable, and 401/403 errors direct the operator to re-authenticate. |
| Cost | Go standard library only; no module dependency or additional always-on service. |

## 2. The environment contract

**Use the provider SDKs' own standard names for transport and credentials. Keep
the unavoidable model choice in gate's namespace.**

Every major provider SDK already accepts a base-URL override next to its API key,
precisely so a proxy or gateway can be dropped in without code changes. A gateway
is exactly that case. The transport contract honors those existing names. Gate
also needs one model selector because model IDs are request data, not an SDK
environment standard:

| Variable | Role | Required |
|---|---|---|
| `ANTHROPIC_API_KEY` | Credential sent as `x-api-key` on the Anthropic-native path. Holds a gateway-issued token when a gateway is configured; a provider key otherwise. | for the Anthropic path |
| `ANTHROPIC_BASE_URL` | Base URL override. **Unset = direct to the provider** (today's behavior, unchanged). Set = route through that origin. | no |
| `GATE_CLOUD_MODEL` | Model ID for gate's structured-output cloud backend. It does not configure the separate local CLI judgment path. | when `ANTHROPIC_BASE_URL` is set |
| `OPENAI_API_KEY` | Same role, OpenAI-compatible path. | for the OpenAI path |
| `OPENAI_BASE_URL` | Same role, OpenAI-compatible path. | no |

Only the Anthropic pair plus `GATE_CLOUD_MODEL` are in scope for this change
(§3.1); the OpenAI pair is listed because it is the same transport contract for
the other protocol, and any later call site should follow it rather than invent
something.

The payoff is that **a gateway needs no new configuration surface at all** — a
key and a base URL per protocol, both already standardized. A tool honoring them
works against a direct provider, a gateway, or a local mock with no code change,
and third-party tools that already read these names come along for free.

`<base>` is an **origin plus optional prefix**, and the client appends the
provider's own path exactly as the SDK would. Verified against a live gateway:
setting the Anthropic base to `<host>/<provider-prefix>` and appending the
standard `/v1/messages` returns 200. So the client must not hardcode a full URL —
it must join base + the provider's canonical path.

> **Do not strip or rewrite the base's path component.** A gateway commonly serves
> the provider protocol under a prefix, and `405 Method Not Allowed` on the bare
> provider path is the usual symptom of a client that dropped it. This is the most
> common first-run failure and the error is unhelpful.

**Auth header.** Send what the provider SDK sends: `x-api-key` +
`anthropic-version` on the Anthropic path, `Authorization: Bearer` on the OpenAI
path. Gateways generally accept `Authorization: Bearer` on both — verified — but
there is no reason to deviate from the provider shape, and matching it keeps the
direct path and the gateway path on one code route.

**Model naming.** Gateways commonly namespace model IDs by provider
(`<provider>/<model>`) rather than the provider's bare name, and the served model
set is the deployment's choice — it will not match the provider's public
catalogue. So the model ID must be configurable, and **the existing
`cloudModelDefault` cannot be assumed to exist** behind a gateway. See §4.4.

**Token acquisition is out of band.** How the token gets into `ANTHROPIC_API_KEY`
is the operator's problem, not the client's. A gateway fronted by an identity
provider typically ships a CLI that mints and caches a token and prints it to
stdout, making the whole thing a one-liner in a shell profile:

```bash
export ANTHROPIC_BASE_URL=<gateway-origin-and-prefix>
export ANTHROPIC_API_KEY=$(<token-command>)
export GATE_CLOUD_MODEL=<served-model-id>
```

This is why §4.2 chose an env var over an exec'd helper: the env var is the
standard, and the helper composes into it trivially. The tradeoff is rotation —
see §4.2.

**Protocol variants.** Gateways typically offer both the native provider protocol
and an OpenAI-compatible one (`/v1/chat/completions`, `/v1/models`). Only the
native path is needed here (§7).

## 3. Where the code needs the seam

Surveyed every tenant under `cmd/` plus the top-level `local/` and `contracts/`
packages, grepping for provider hostnames, the Ollama port, and provider key
names. Result: `console`, `custody`, `dispatch`, `driverstate`, `escalate`,
`eval`, `flare`, `tracelens`, `triage`, and `workbench-mcp` make **no** LLM call
at all. Three places matter, and only the first is in scope.

### 3.1 `cmd/gate/internal/verify/model.go` — the real one

The shape is already right. There is a `Model` interface, a `localModel`
(Ollama) and a `cloudModel` (Anthropic), and `ModelBackend(backend string)`
selects between them off gate's `-model-backend local|cloud` flag
(`cmd/gate/main.go:376`, threaded to `verify.ModelBackend` at `main.go:417`).
`cloudModel` already carries its own `url` and `apiKey` **fields** — and
`model_test.go` already exercises it by pointing `url` at an `httptest` server.

At design time this was not surgery. Two things were hardcoded, and only two:

- `model.go` used a full `anthropicURL` package constant, assigned to the field
  in `newCloudModel`.
- `ModelBackend` read only `os.Getenv("ANTHROPIC_API_KEY")`.

Everything downstream transfers **unchanged** against a gateway serving the
Anthropic-native protocol: the request body is already Anthropic-native with a
`structured_output` tool and `tool_choice`, the headers are already `x-api-key` +
`anthropic-version`, and the response adapter already walks `content[]` for the
`tool_use` block. The existing fail-closed guards (`max_tokens` truncation, empty
`tool_use` input, no-`tool_use`-block) all stay meaningful.

### 3.2 `local/local.go` — optional, second

`ollamaURL = "http://localhost:11434/api/chat"` is hardcoded at `local.go:24`.
`local` is the *free* path and should stay pointed at Ollama — do **not** move it
to a gateway. But `local.Ask` already takes an injected `Escalate` func
(`local/README.md`), and today nothing is wired into it, so a low-confidence
result is merely *flagged*. A gateway-backed escalate target is the natural
filler, and gateways usually expose a small cheap model that makes escalation
routine rather than precious.

Doing that is a separate change from this one, and it does **not** need a shared
package: `local`'s escalate hook is an injected func, so a caller that already has
gate's cloud path can supply it. `local/` stays a leaf (CI's `hygiene` job enforces
it imports nothing beyond `contracts`), and nothing here threatens that. See §4.1.

### 3.3 `CURSOR_API_KEY` — the gateway-incompatible runtime

`cmd/runway/internal/backend/rooms/` runs an agent-cursor profile and carries
`CURSOR_API_KEY` in two places: the `allowedSecrets` allowlist
(`rooms.go:34`) and the declared `secret_names` in the lifecycle record
(`rooms.go:172`).

Cursor's runtime authenticates to its own vendor service and takes no base-URL
override, so **it cannot be pointed at a gateway.** In an environment where the
gateway is the only sanctioned egress, that runtime is unusable — not
misconfigured. This is a real capability loss to state plainly rather than a
detail to route around.

**Not part of this change.** Removing a runtime is a separate decision from adding
a base-URL seam, and bundling them would mix an additive change with a
subtractive one. Two things belong here:

- A `FOLLOWUPS.md` entry recording that the cursor runtime has no gateway path, so
  the reason is on the record when someone hits a 401 there.
- No new work that deepens the dependency on it.

When it is retired, the removal is `allowedSecrets`, the `secret_names` array, the
`agent-cursor` profile plumbing, and the runtime's own docs — and the
`secret_names` array in particular is a lifecycle-record field, so check whether
existing records need to stay readable before changing its shape.

### 3.4 Not a target

The archived standalone `gate` repo (migration banner, 2026-07-17, pointing here)
carries a byte-identical `model.go` modulo CRLF line endings. Do not edit it; do
not treat the pair as a dup needing reconciliation.

## 4. Design decisions

### 4.1 No new package

**Decision: the change lives in `cmd/gate/internal/verify/`. Do not add a
top-level `gateway/` package.**

An earlier draft of this spec called for one, reasoning that `local/` is a leaf
package (CI's `hygiene` job enforces it imports nothing beyond `contracts`), so a
client both `local` and `gate` could use needed a top-level home peer to `local/`.

That reasoning is now moot. Honoring a standard env var adds **no shared
component** — there is no client for `local` to import, just URL resolution inside
gate's existing `cloudModel`. Adding a package to hold two dozen lines of `net/url`
joining would be structure for its own sake.

If a second consumer ever needs the same resolution, extract then, with a real
second call site to shape it.

### 4.2 Standard env vars, read at construction

**Decision: honor `ANTHROPIC_BASE_URL` / `ANTHROPIC_API_KEY` (and the OpenAI
pair for any future transport), add only the gate-owned `GATE_CLOUD_MODEL`
selector, and compile in no deployment defaults.**

Two things follow, and both matter for a public repo:

- **No hostname, prefix, or helper path in the tree.** A hardcoded origin is a
  single-deployment assumption that any second environment invalidates.
- **No bespoke config surface.** Earlier drafts of this spec invented
  `GATEWAY_BASE_URL` / `GATEWAY_KEY_COMMAND` and an exec'd `KeySource`. That was
  strictly worse: it duplicated a standard the SDK ecosystem already has, and it
  put a subprocess in the request path for something an env var expresses.

The base URL is read **once at model construction**, not per request. It is
configuration, not a credential.

The key is also read at construction, which is the one real tradeoff. A
gateway-issued token can rotate, and a long-lived process that cached it at
startup will 401 until restart. Accepted, because:

- Gate is a short-lived CLI. A run is minutes; the window is negligible.
- The alternative — exec a helper per request — puts a subprocess in the hot path
  to close a window this process shape barely has.
- The failure is legible and self-correcting: a 401 with a "re-authenticate and
  retry" error, not silent corruption.

Record it as a known limitation in the package doc comment. If a long-running
daemon ever consumes this package, revisit with a `KeySource` func — the field
should be typed so that swap is additive, not a rewrite.

For tests, the client takes its base URL and key as **struct fields**, exactly as
`cloudModel` already does. Env reading happens in the constructor only, so the
`httptest` pattern already in `model_test.go` keeps working with no new
machinery, and no test needs to mutate the environment.

### 4.3 No new backend value — `cloud` becomes gateway-aware

**Decision: `-model-backend` stays `local|cloud`. `cloud` honors
`ANTHROPIC_BASE_URL` when it is set.**

This reverses an earlier draft that added a third `gateway` backend value. Once
the base URL is a standard env var, a third backend is redundant: "direct" and
"through a gateway" are the *same* transport differing only in origin. A separate
enum value would force every caller and every doc to choose between two paths
that share one code route.

So: `newCloudModel` reads `ANTHROPIC_BASE_URL`, falling back to the provider
default when unset. **Unset behavior is byte-for-byte what it is today** — that is
what keeps the frozen Phase-0 eval baseline and the hosted-CI path valid.

`impl()` keeps returning **the model ID and nothing else** — no host, no origin,
no direct-vs-gateway marker. The model ID is what identifies the judge; the route
it took to get there is deployment topology, and topology does not belong in an
artifact log that lives in a public repo.

This is a deliberate narrowing. An earlier draft had `impl()` record the resolved
host so a reader could tell which egress produced a judgment. That leaks an
internal hostname into every verdict artifact, which is exactly what §6 exists to
prevent, and it buys little: if two runs need distinguishing, configure distinct
model IDs. Keep the blast radius at zero.

Consequence to accept honestly: the artifact does not say whether a gateway was in
the path. If a run's provenance is ever genuinely in question, the answer comes
from the environment that produced it, not from the log.

### 4.4 The default model

`cloudModelDefault` is a specific dated provider model, and a gateway's served set
is the deployment's choice — that exact ID may not exist there. So when
`ANTHROPIC_BASE_URL` is set, `GATE_CLOUD_MODEL` becomes **required**, with a
legible error if absent. Do not silently fall back to the provider default: that
surfaces as a confusing 404 from the gateway instead of a config error naming the
fix.

When the base URL is unset, `cloudModelDefault` still applies. Same rule as §4.3:
direct behavior does not change.

`GATE_CLOUD_MODEL` configures only the Anthropic-native `cloudModel` used by
advisory rungs and the eval harness. The separate local CLI judgment path owns
its provider invocation and does not read a `GATE_JUDGE_MODEL` variable. This
keeps transports with different supported catalogues from silently changing
one another. The name follows the existing `GATE_*` convention while making
the narrower ownership explicit.

This has a real consequence for the eval: the Phase-0 bars (coverage ≥ 60%,
on-handled ≥ 90%, against a 92.2% / 95.7% reference) were set against one
specific model. A re-run through a different model must **name the model in the
results doc** and state that the comparison is indicative, not apples-to-apples.

### 4.5 Fail fast and legibly on a missing or stale key

The likely failures are a missing key, a stale key, and a base URL pointing
somewhere unreachable. All three must fail fast with an error that names the fix:

- Missing key with a base URL set — *"cloud model: ANTHROPIC_API_KEY not set"*
  is the existing direct-path error; keep it.
- A 401/403 from a configured gateway must say the token may be expired and needs
  re-issuing. Do not retry silently: a gateway token is often minted by an
  interactive flow, so a retry cannot fix it and only delays the real message.
- Never attempt to drive an authentication flow programmatically. Auth is an
  operator action.

### 4.6 Never emit the token or the endpoint

Neither the token **nor the resolved URL** may reach logs, error strings, or
verdict artifacts. `impl()` returns the model ID only (§4.3).

The endpoint matters as much as the token here, for a different reason: a leaked
token is revocable, but a hostname committed to a public repo's history is not
retractable. And URLs leak by accident more easily than keys, because the obvious
way to write a transport error is to include the URL that failed:

- Wrap request errors without the URL. Go's `*url.Error` **embeds the URL in its
  `Error()` string**, so returning a bare `client.Do` error leaks the endpoint.
  Extract the status and a short reason instead.
- On a non-2xx, keep the existing capped error-body read, but echo no request
  header and no request URL.
- Same rule for anything written to an artifact: a resolution or config error must
  name *which variable* is wrong, never the value it held.

This is the one place where a careless error message defeats §6 at runtime rather
than at review time, which is why it is a design decision and not a style note.

### 4.7 Architecture, data, API, and failure model

The architecture remains one call path:

```text
gate -model-backend cloud
        |
        v
ModelBackend("cloud") -- reads configuration once
        |
        v
cloudModel { model, apiKey, url, gateway, client }
        |
        v
Anthropic Messages request -- direct provider or gateway
        |
        v
existing tool_use response adapter and fail-closed guards
```

No persistent data model changes. The `cloudModel` fields are process-local
configuration; `gateway` is a boolean used only to select the redacted error
path. Verdict artifacts continue to record only the model ID in `Producer.Impl`.
The resolved URL and route topology are intentionally absent.

The internal construction contract is:

```go
func resolveAnthropicURL(base string) (string, error)
func newCloudModel(apiKey, model, base string) (Model, error)
func ModelBackend(backend string) (Model, error)
```

`ModelBackend("cloud")` reads `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL`, and
`GATE_CLOUD_MODEL`, then delegates pure validation and URL resolution to the
constructor. It ignores `GATE_CLOUD_MODEL` in direct mode so the existing default
cannot be changed accidentally. `resolveAnthropicURL` accepts an empty string
(direct fallback) or an absolute `http`/`https` base, rejects userinfo, query, and
fragment components, and joins the path prefix with `v1/messages` without
discarding the prefix.
Restricting schemes avoids accidentally treating another URL form as an HTTP
endpoint; rejecting userinfo prevents credentials from entering the URL.

The outbound API remains the Anthropic Messages protocol already implemented:
POST JSON, `x-api-key`, `anthropic-version`, forced `structured_output` tool use,
and a `tool_use.input` response. There is no new public Go API and no artifact
schema change.

Failure behavior is explicit:

1. Missing key, gateway model, or invalid base fails at construction before any
   request.
2. DNS, connection, and timeout errors return a stable transport category without
   wrapping `*url.Error`, because its string includes the endpoint.
3. 401/403 fails once with a re-authentication hint. Other non-2xx responses keep
   the current capped body only when direct mode is active; gateway mode returns
   status plus a stable category so a reflected body cannot leak routing details.
4. Gateway 3xx responses are returned to the caller and handled through the same
   redacted status path. The client never follows a redirect with `x-api-key`.
5. A successful HTTP response still fails closed on decode errors, truncation,
   empty tool input, or missing tool-use blocks exactly as today.
6. There is no automatic fallback from gateway to direct provider; that would
   bypass the configured egress boundary.

## 5. Implementation

Work from the module root. Go standard library only — no new module dependencies.

This is a **smaller change than a new package**. Once the base URL is an env var,
`cloudModel` already has the right fields — the work is resolution and joining,
not a new transport. Do not create a `gateway/` package; an earlier draft called
for one, and it is unjustified now (see §4.3). `local/`'s leaf constraint stops
mattering because there is nothing new for it to import.

### Step 1 — base-URL resolution in `cmd/gate/internal/verify/model.go`

- Keep the direct Anthropic URL as the **fallback**, renamed to make its role
  explicit (`anthropicDirectURL`).
- Add a small resolver: read `ANTHROPIC_BASE_URL`; if empty, return the direct
  URL; otherwise join the base's origin **and path prefix** with the provider's
  canonical `/v1/messages`.
- The join is the one place to be careful, and it earns unit tests: preserve the
  base's path prefix, tolerate a trailing slash, and reject a base that is not a
  valid absolute URL with a legible error. Use `net/url`; do not string-concat.
- Comment that the prefix must survive the join, citing the `405` symptom (§2), so
  nobody "simplifies" it into an origin-only rewrite later.
- `newCloudModel` takes the resolved URL. Its existing `url` field is unchanged,
  which is what keeps the tests working.

### Step 2 — model and audit trail

- Apply §4.4: when `ANTHROPIC_BASE_URL` is set, require
  `GATE_CLOUD_MODEL` and error legibly if absent; when unset,
  `cloudModelDefault` still applies.
- `impl()` is **unchanged** — model ID only. Do not add the host, the origin, or a
  direct-vs-gateway marker (§4.3).
- Do **not** add a `-model-backend` value. The flag stays `local|cloud`
  (`main.go:376` unchanged).

Leave the capability-check-before-model-construction ordering at `main.go:409`
alone. The comment there is load-bearing: a missing grant must refuse (exit 3)
*before* a missing key can hard-error the backend. Same reasoning now applies to a
missing base URL or model.

### Step 3 — tests

Follow `model_test.go`'s existing shape: `httptest` server plus a struct literal
with `url` pointed at it. Add:

- URL resolution as a table test: unset base → direct URL; origin-only base →
  `<origin>/v1/messages`; base **with a path prefix** → prefix preserved;
  trailing-slash base → no doubled slash; relative URL, unsupported scheme,
  userinfo, query, fragment, and malformed base → error.
- The outbound request carries `x-api-key` and `anthropic-version` (**not**
  `Authorization: Bearer` — that is the OpenAI-compat shape, and mixing them up is
  an easy, silent mistake).
- A 401 from the server produces an error mentioning re-authentication and **does
  not** contain the key (§4.5, §4.6).
- A gateway non-2xx response that reflects its request URL does not expose that
  URL, while the direct-mode error contract stays unchanged.
- A cross-origin redirect is not followed and the redirect target receives no
  request or `x-api-key`.
- Base URL set with no model configured → legible error, not a request.
- `impl()` returns the model ID and contains **no** host, origin, or key — assert
  on the absence, so a later "helpful" addition trips a test.

Resolution tests must not read the real environment — pass the base URL in as a
parameter rather than calling `os.Setenv`, so the tests stay parallel-safe.

Preserve every existing fail-closed assertion (truncation, empty input, missing
block): the gateway path shares one code route with direct, so they cover both by
construction.

Tests must not contain a real hostname, token, or helper path.

### Step 4 — validate

Build and check module-wide, per `cmd/gate/CLAUDE.md`:

```bash
go build ./... && go vet ./... && golangci-lint run ./... && go test ./...
```

Known local quirk to expect and ignore: `observe.TestExplainGolden` fails on a
Windows checkout (CRLF golden, no `.gitattributes`); it passes on Linux CI.

Then the real acceptance — the eval blocked since 2026-07-13. `ci-classify-eval`
(`cmd/gate/tools/ci-classify-eval/main.go`) already calls
`verify.ModelBackend("cloud")`, so **it needs no gateway-specific code change**:
exporting the three variables is enough. During validation, its fixture decoder
was corrected to preserve the frozen dataset's string metadata as opaque JSON;
that change is transport-independent. This remains the clearest evidence the
configuration approach is the right shape.

```bash
export ANTHROPIC_BASE_URL=...   # gateway origin + provider prefix
export ANTHROPIC_API_KEY=...    # gateway-issued token
export GATE_CLOUD_MODEL=...      # model ID served by the gateway
go run ./cmd/gate/tools/ci-classify-eval \
  -out cmd/gate/docs/features/ci-classify/eval/ci-eval-raw.gateway.jsonl
pwsh cmd/gate/docs/features/ci-classify/eval/floor-score.ps1 -raw ci-eval-raw.gateway.jsonl
```

Bars: coverage ≥ 60%, on-handled accuracy ≥ 90%. Phase-0 reference: 92.2% / 95.7%.

Replace `cloud-eval-results.md` (currently a "pending operator run" placeholder)
with the real percentages, naming the model used and noting that it differs from
the Phase-0 reference model (§4.4).

### Step 5 — docs

- `cmd/gate/CLAUDE.md` — the provider-variable contract from §2, plus
  `GATE_CLOUD_MODEL`, with placeholder values only. This is the primary
  operator-facing surface.
- Root `CLAUDE.md` — one line noting that cloud egress honors the standard
  provider base-URL variables, so a gateway needs no bespoke config.
- `FOLLOWUPS.md` — the construction-time key read and its rotation limitation
  (§4.2); the `local` escalate target (§3.2) if not done in this pass.

## 6. Public-repo review gate

Before pushing, confirm no deployment specifics leaked. This is an acceptance
criterion, not a nicety.

- No gateway hostname or internal domain in code, tests, docs, or fixtures.
- No vendor gateway product name in identifiers, comments, or docs.
- No absolute path to an internal helper binary.
- No token or token prefix, including in test fixtures.
- No internal group, account, or org identifier.
- No gateway-specific model ID (a provider-prefixed name reveals the deployment's
  catalogue). Tests use placeholders.
- No error string that would print the resolved URL at runtime (§4.6) — the review
  catches the literal, not the interpolation, so this one has to be read for.

Run a grep for the specifics of your own deployment across the diff before the
push, and check **generated** files too, not just source: eval JSONL, verdict
artifacts, and golden files are the easy misses.

## 7. Out of scope

- Moving `local/` off Ollama. It stays the free path.
- The OpenAI-compat transport (`/v1/chat/completions`) and non-Anthropic model
  families. The native path covers gate's needs; add the second transport when a
  call site actually wants a non-Anthropic model.
- Any gateway-specific MCP transport.
- Budget or spend-cap enforcement in the decision path. Some gateways expose a
  per-key cap; a pre-flight check before a long eval may be worth it later. Note
  it in `FOLLOWUPS.md`.
- Driving an authentication flow programmatically (§4.5).
- Streaming. Gate's structured-output calls are single-shot.

## 8. Acceptance

1. `go build ./... && go vet ./... && golangci-lint run ./... && go test ./...`
   clean from the module root (modulo the known `TestExplainGolden` Windows quirk),
   and `hygiene` passes.
2. **With `ANTHROPIC_BASE_URL` unset, behavior is byte-for-byte what it is today.**
   The single most important criterion: it is what preserves the hosted-CI path and
   the frozen eval baseline.
3. With `ANTHROPIC_BASE_URL`, `ANTHROPIC_API_KEY`, and `GATE_CLOUD_MODEL` set,
   `gate -model-backend cloud` completes a real run against a gateway.
4. **Neither the token nor the gateway host appears anywhere** — not in the
   artifact log, not in `impl()`, not in any error string. Grep the emitted log for
   both the key and the hostname to confirm.
5. A base URL carrying a path prefix keeps that prefix in the request. This is
   the regression that produces a bare-path `405`, so it is a pinned test, not a
   manual check.
6. Base URL set with no model configured fails with an error naming the missing
   config, not a 404 from the gateway.
7. A 401 produces an error mentioning re-authentication, with no silent retry.
8. A gateway redirect is refused before the key can be forwarded to its target.
9. The frozen ci-classify eval runs with the three variables exported and **no
   gateway-specific code in `ci-classify-eval`**, clearing coverage ≥ 60% / on-handled
   ≥ 90%, with `cloud-eval-results.md` updated to real numbers and the model
   named.
10. `FOLLOWUPS.md` records the cursor-runtime gateway incompatibility (§3.3) and
   the construction-time key read (§4.2).
11. The §6 review gate passes.

## 9. Rollout / implementation plan

| Phase | Goal | High-level tasks | Depends on | Scope | Gate |
|---|---|---|---|---|---|
| 1. Configuration seam | Make the existing cloud backend gateway-aware without changing direct behavior. | Add pure URL resolution; read the three Anthropic/gate variables at construction; require a gateway model; sanitize transport and auth errors; add focused unit tests. | none | ~150–300 weighted LOC | Unit tests pin direct compatibility, prefix preservation, validation, redaction, and 401/403 behavior. |
| 2. Documentation and static review | Make the operator contract discoverable and prove no deployment detail entered the repo. | Update gate/root guidance and `FOLLOWUPS.md`; inspect the diff and generated artifacts using the §6 checklist. | Phase 1 | ~40–100 weighted LOC | **VALIDATION GATE A:** module checks and public-repo review pass before any live credential is used. |
| 3. Live gateway validation | Prove the native protocol works through the configured deployment. | Run the frozen ci-classify eval unchanged; score it; record model and results; inspect emitted JSONL for endpoint/key leakage. | Gate A and operator-provided runtime configuration | generated eval result + short results note | **VALIDATION GATE B / go-no-go:** coverage ≥60%, on-handled accuracy ≥90%, no secret/endpoint leakage, and no change to `ci-classify-eval`. |
| 4. Follow-on consumers | Consider the `local.Ask` escalation hook and OpenAI-compatible transport only after a real caller exists. | Materialize separate task(s) from `FOLLOWUPS.md` if demand appears. | Gate B plus concrete consumer | unestimated; intentionally uncommitted | Post-gate stub; requires a separate design decision. |

Only Phases 1–3 are part of this initiative. Phase 4 is recorded to make the
direction visible without designing speculative code.

## 10. Open questions

1. **Resolved — accept `http` bases.** Supporting `http` enables local mocks and
   development gateways; production guidance requires `https`. The resolver
   accepts both and leaves transport policy to deployment.
2. **Where should the live eval output live?** The existing results directory is
   authoritative, but raw gateway output must be inspected before commit.
   Proposed resolution: write to the existing eval directory, scan it, then
   commit only the sanitized raw JSONL and results note if they contain no
   deployment details.

Neither question changes the Phase 1 API seam.

## 11. Validation plan

The initiative is a **go** because both gates below passed:

- **Gate A — deterministic:** all module checks from §5 pass (apart from the
  documented Windows-only golden quirk), the focused gateway tests cover every
  failure branch in §4.7, direct-mode request behavior is pinned, and a review of
  the complete diff finds no deployment detail.
- **Gate B — live (passed):** with operator-supplied `ANTHROPIC_BASE_URL`,
  `ANTHROPIC_API_KEY`, and `GATE_CLOUD_MODEL`, the unchanged frozen eval reaches
  coverage ≥60% and on-handled accuracy ≥90%. The recorded result names the model
  but not the endpoint, and scans of stderr, verdict artifacts, and output JSONL
  find neither the exact key nor the exact configured host.

Any credential/endpoint leak is an immediate no-go regardless of score. A score
below either bar is also a no-go; the implementation does not ship on the theory
that routing alone is sufficient. Direct-provider regression is a no-go even if
the gateway eval passes.
