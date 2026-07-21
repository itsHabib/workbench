# Operator console actions — Technical Design Document

**Status:** draft / proposal — **NOT a build commitment.** Judgment is designed through an executable validation gate; browser-based minting is a no-go under the current threat model.
**Owner:** @itsHabib
**Date:** 2026-07-21
**Related:** [workbench charter](../../DESIGN.md), [operator-console brainstorm](brainstorm-2026-07-19.md), [actionable-docket correction](actionable-docket.md), [console design](../../../cmd/console/docs/DESIGN.md), [gate README](../../../cmd/gate/README.md), [auto-mode defaults](../../auto-mode-defaults.md)

> **Reviewers — focus areas:** (1) the local-process threat model and the narrow trust assumption that permits Judge but rejects Mint; (2) the Origin, Host, cookie, CSRF, preview, replay, and stale-state contracts in §§4–8; (3) the mint no-go criteria in §§4.3 and 11; (4) whether every failure maps closed without creating a second authority or audit record outside gate.

---

## 1. Problem & hypothesis

Console v0 is deliberately loopback-only and read-only. It renders `gate next -json -live` as the actionable docket, shells `gate explain -json` for a case file, and shells `gate audit` for the masthead integrity signal. Gate owns every judgment, grant, signature, ledger write, capability check, and command. The console imports none of gate's decision logic and reads none of gate's files.

The remaining operator friction is action-shaped:

- a parked case already has all of the context needed for a judgment, but the operator must copy a long `gate judge` command into a terminal;
- grant minting is an intentionally rarer, more powerful act with a similar flag-tail and expiry-calculation tax.

The hypothesis is deliberately split: a browser form can safely reduce **judgment** friction if gate still generates and executes the command contract, the browser first shows the exact argv, gate revalidates all state at commit time, and the action survives adversarial browser tests. That work does **not** prove browser **minting** safe. Minting creates authority and therefore needs a stronger human-presence property than an unauthenticated localhost HTTP service can provide.

This document designs judgment first and sets a binary validation gate. Under the current threat model, Mint is a **no-go HTTP surface** and remains CLI-only. Passing the Judge gate is necessary but not sufficient to revisit that decision.

### 1.1 Goals

- Add a pass/block judgment form to the case-file view with no preselected judgment.
- Require a non-empty, permanent rationale and show the exact argv before commit.
- Keep gate the only component that validates, decides, signs, writes, and generates semantic commands.
- Make submission idempotent and fail closed across double-clicks, retries, concurrent tabs, stale runs, stale grants, binary drift, and ledger tampering.
- Display gate's returned action, including its pinned merge command, without executing it.
- Establish a reusable browser-action security plane before the first mutating route.
- Prove the plane with executable JavaScript/browser tests and adversarial review before any Mint work is materialized.

### 1.2 Non-goals

- No merge endpoint, merge button, auto-copy-and-run behavior, or invocation of gate's returned merge command.
- No MCP verb, agent API, automation hook, or non-interactive fallback for minting.
- No console implementation of gate's reducer, grant rules, signing, ledger format, state projection, or command policy.
- No console access to signing-key or anchor-key bytes.
- No remote bind, authentication system, multi-user service, notification engine, or durable console database.
- No Judge and Mint implementation in one PR. P1, P2, validation, and any later P3 are independently reviewed changes.
- No commitment to P4 record/runs expansion or other dashboard work.

## 2. Functional & non-functional requirements

### 2.1 Functional requirements

1. The case file renders decision radios for exactly `pass` and `block`; neither is selected on load, refresh, back navigation, or failed submission.
2. The form requires a rationale that remains non-empty after `strings.TrimSpace`, is valid UTF-8, contains no NUL, and is at most 4,096 UTF-8 bytes. Interior whitespace and line breaks are preserved exactly.
3. The grant ID may be populated from gate's actionable-docket projection, but remains visible and editable before preview. The judgment is never populated or inferred.
4. Editing run, grant, decision, or rationale invalidates the current preview. A commit control is unavailable until a fresh preview has returned.
5. Gate generates the semantic argv for the preview. The console prepends its configured gate executable as argv element zero, shell-quotes only for display, stores the vector as an opaque intent, and later executes that exact vector with `exec.CommandContext`—never through a shell.
6. Gate rechecks audit integrity, unresolved-run state, request id, grant signature, scope, action, expiry, tier ceiling, and cycle ceiling at commit time. Preview is never authorization.
7. A successful judgment response includes gate's canonical result. If gate returns a merge command, the UI pins it beside the recorded outcome as copyable text only.
8. The browser refreshes the case file and docket after success. It never replays a POST on page refresh.
9. An audit failure or incompatible gate binary disables all action controls server-side and client-side while leaving read-only views available with a visible reason.
10. Mint has no HTTP request handler in committed scope. Attempts to call the reserved Mint paths receive the normal non-route `404`; the capability advertisement omits Mint.
11. Judge actions default disabled. `console serve` requires an explicit operator enablement flag, and P2 may enable it only after the deployment proves that governed agents cannot reach loopback or already possess the equivalent `gate judge` capability.

### 2.2 Non-functional requirements

| Property | Target |
|---|---|
| Boundary | `cmd/console` shells a gate binary; `go list -deps ./cmd/console/...` contains no `cmd/gate` package. |
| Network | Loopback bind only; Host pinning remains mandatory on every response; mutating requests additionally require an exact same-origin `Origin`. |
| Browser security | Per-session, server-stored, single-use CSRF tokens; host-only `HttpOnly; SameSite=Strict` cookie; strict CSP; `nosniff`; no cached action response. |
| Authorization | A judgment requires a currently valid gate grant for the run's repo and `merge` action. Preview never reserves or extends a grant. |
| Consistency | At most one judgment resolves a given escalation. Same-request retry is idempotent; a different request against resolved state is stale. |
| Durability | Gate's canonical append-only ledger is the only durable action record; `gate audit` remains sufficient to reconstruct every successful action. |
| Compatibility | Console enables writes only when the gate binary advertises an overlapping, exact action-protocol version and all required features; preview and commit are bound to one gate binary identity. |
| Operability | Production console code remains Go stdlib-only. `chromedp` is test-only, build-tagged, and drives an installed Chrome/Chromium. |
| Latency | Preview and commit each use a 10-second subprocess deadline; timeout is visible and never interpreted as success. |
| Request bounds | Action JSON bodies are at most 16 KiB; unknown fields and trailing JSON values are rejected. |

## 3. Architecture overview

```text
browser
  │  loopback HTTP + Host/Origin/CSRF
  ▼
console web server
  ├─ read-only routes ──────────────┐
  ├─ in-memory sessions/tokens      │
  └─ bounded preview intents        │
             │ exact argv, no shell │
             ▼                      │
         gate binary ◄──────────────┘
          ├─ versioned action contract
          ├─ audit + stale/idempotency checks
          ├─ capability verification
          ├─ reducer + command generation
          └─ canonical append-only ledger
                    │
        key bytes stay inside gate's process
```

There is one new state class in console: bounded, in-memory **security state** (sessions, unused CSRF tokens, and short-lived preview intents). It is neither domain state nor an audit journal. Restarting console intentionally destroys it. Gate's ledger remains the only source of truth.

### 3.1 Components

- `cmd/gate`: adds a versioned compatibility projection with binary identity, a read-only Judge preview, structured action errors, request-id idempotency, and stale-escalation guards. It continues to own all policy and persistence.
- `cmd/console/internal/gatecli`: captures stdout, stderr, and numeric exit status; validates gate's versioned JSON envelopes; requests previews; and executes only the exact argv returned by gate.
- `cmd/console/internal/web`: owns Host/Origin/CSRF enforcement, session and intent lifetimes, strict JSON parsing, HTTP error mapping, and response headers. It does not interpret gate policy.
- `cmd/console/internal/web/static/app.html`: renders the form, command review, final outcome, and disabled-state reasons. It never constructs a gate command or executes a returned action.

### 3.2 Preserved boundary law

The console may validate transport schemas and identifiers needed to call gate. It may not copy gate's tier law, decide whether a grant is usable, infer a PR outcome, or generate a judgment/merge command. Gate publishes those facts as JSON artifacts. CI's existing hygiene check remains the mechanical backstop.

## 4. Threat model and key decisions

### 4.1 Threat actors

| Actor / failure | In scope? | Defense or consequence |
|---|---:|---|
| Remote malicious website in the operator's browser | Yes | Loopback bind, Host pinning, exact Origin, SameSite cookie, synchronizer CSRF token, CSP. |
| DNS rebinding page | Yes | Existing Host allowlist runs before routing; Origin must also match the request Host. |
| Malicious PR title, question, rationale, or gate output | Yes | JSON transport, text-only DOM insertion, HTML escaping, CSP, nosniff; never assign untrusted strings to `innerHTML`. |
| Accidental double click, refresh, retry, or concurrent tab | Yes | Single-use CSRF, intent state machine, gate request id, stale-escalation precondition, idempotent replay. |
| Stale/expired/wrong-scope/tampered grant | Yes | Gate checks again inside the committing invocation; HTTP maps the coded refusal. |
| Incompatible/malformed gate binary output | Yes | Exact version/features check, strict envelope decoder, exit-code/body agreement; actions disabled on failure. |
| Ledger edit, deletion, truncation, or anchor mismatch | Yes | `gate audit` before preview and inside commit; `423 audit_tampered`; controls disabled. |
| Plain-HTTP loopback cookie exposure | Yes, bounded | Cookie omits `Secure` because `http://127.0.0.1` must work. Loopback bind, Host/Origin checks, and the same-user residual bound this; any future HTTPS listener must use `Secure` and a `__Host-` cookie. |
| Local process under a different OS user | Partly | OS file/process/network permissions are the boundary; console adds no cross-user claim. |
| Local process or agent under the **same OS user** that can issue arbitrary HTTP | **Not distinguishable from the operator** | It can fetch the cookie and CSRF token and forge Origin. CSRF is not authentication. This is an accepted residual for Judge and a no-go for Mint. |
| Same-user process able to read memory, debug processes, or replace binaries | No | It already controls the console/gate trust base; no application-layer protocol can recover integrity. |

### 4.2 What “human-only” means here

An unauthenticated localhost service cannot prove a human gesture. A same-user agent can perform the same GET/POST sequence as a browser, including acquiring a session cookie and CSRF token. `Origin`, `Sec-Fetch-*`, button clicks, typed phrases, delays, and pointer movement are forgeable by such a process.

For **Judge**, the design makes a narrower claim: the route resists remote-web confused-deputy attacks and accidental browser actions, while a same-user local process is inside the accepted trust boundary. This is tolerable only because Judge consumes an already-minted, repo/action/TTL-scoped grant and exposes no capability that the same process could not exercise with `gate judge` under the same OS identity. Command echo proves which argv the browser is consenting to; it does not prove who is behind the browser.

This assumption is a **hard precondition**, not an open deployment question. Judge defaults disabled. P2 may enable the route only after the validation evidence positively shows one of two conditions: governed agents cannot reach the listener, or they already have the equivalent ability to invoke `gate judge` under the same OS identity. If neither is proven, Judge is a no-go and remains copy-paste CLI. An explicit `console serve -enable-judge` operator flag records acceptance of the proven boundary, but the flag alone is not proof.

### 4.3 D1 — Mint is CLI-only under the current model

Minting creates fresh authority. An HTTP handler would make console a confused deputy over the gate process and its signing-key access: a local agent blocked from `gate grant` could simply call the handler. CSRF and a two-step browser flow do not change that fact.

Therefore:

- no `/api/actions/mint/preview` or `/api/actions/mint/commit` route is registered;
- no Mint capability is advertised by `/api/action-session`;
- console never invokes `gate grant` in P1 or P2;
- the existing CLI and external key-custody/tool-guard discipline remain the mint boundary;
- P3 stays task-less until this TDD is reviewed and the Judge validation gate passes; even then, implementation requires a separate design amendment proving an OS-mediated, unforgeable user-verification step.

A future two-step Mint UX would otherwise be only **frictional**: review all fields and absolute expiry, then type a random challenge or click “mint grant.” A local process can synthesize both gestures, so that flow is explicitly insufficient. A possible go criterion is platform WebAuthn with `userVerification: "required"`, gate-bound mint parameters, enrollment/recovery rules, and no programmatic fallback. This TDD does not choose or build it.

### 4.4 D2 — Preview then commit for Judge

Judge uses two HTTP POSTs:

1. **Preview:** validate the form, ask gate for a canonical command and stale-state preconditions, then show the exact argv.
2. **Commit:** accept only the opaque preview intent id; execute the stored argv exactly; return gate's result.

Any field edit discards the preview. Preview does not append to the ledger, reserve a grant, or make commit safer by itself. Commit revalidates everything.

### 4.5 D3 — Gate owns compatibility and semantic command generation

Gate adds `gate version -json` with an action protocol advertisement. P1 uses protocol version `1`; console supports exactly version `1` initially and requires these features:

```json
{
  "schema": "gate.compat.v1",
  "binary_version": "<build version or devel>",
  "binary_id": "sha256:<digest of the running executable>",
  "protocols": { "console_actions": 1 },
  "features": [
    "judge_preview_v1",
    "judge_binary_identity_v1",
    "judge_request_id_v1",
    "judge_stale_guard_v1",
    "structured_action_errors_v1"
  ]
}
```

Missing command, malformed JSON, missing feature, non-overlapping version, or unavailable binary identity leaves read-only console working but sets `actions_enabled:false` with `gate_incompatible`. Commit rechecks compatibility rather than trusting startup state.

Gate's read-only preview returns the canonical argument vector **after** argv element zero. Console prepends its configured executable path, stores that complete vector, displays that exact vector with platform-correct quoting, and executes it without a shell. The vector includes `-expect-binary-id <preview binary_id>`; the binary that actually receives commit computes its own identity and refuses before any state read/write if it differs. A PATH/symlink change or upgrade therefore expires the preview even if the replacement advertises the same protocol version. Console never derives flags from form fields after preview.

### 4.6 D4 — No separate console journal

A second append-only log would introduce reconciliation, retention, tamper, and “which record is canonical?” problems without adding authority evidence. Gate already records the judgment, reduced verdict, outcome, request id, and any returned command in its anchored ledger. That is sufficient to reconstruct success.

Console may emit ordinary process diagnostics for failed HTTP/subprocess calls, but it persists no action journal. Security intents and cached results expire from memory and are explicitly non-authoritative.

### 4.7 D5 — Test-only chromedp

Action-bearing inline JavaScript must execute in a real browser. `httptest` and string-contract tests remain Tier 0; a build-tagged `chromedp` suite becomes required Tier 1. `chromedp` is a test dependency only and drives an installed system Chrome/Chromium; production console code remains stdlib-only. Playwright stays an optional manual visual tier, not a committed Node toolchain.

## 5. Data model

### 5.1 Console session (memory only)

```go
type actionSession struct {
    ID        [32]byte
    CreatedAt time.Time
    ExpiresAt time.Time
    CSRF      map[[32]byte]time.Time // token -> absolute expiry; unused only
    Intents   map[string]*judgeIntent
}
```

- Session ids and CSRF tokens come from `crypto/rand` with 256 bits of entropy.
- Sessions expire 30 minutes after creation; there is no sliding extension.
- Each CSRF token expires after 10 minutes or with the session, whichever is sooner, and is consumed on the first syntactically valid action POST regardless of its outcome.
- `GET /api/action-session` creates or inspects the session but does not issue a token. `GET /api/action-csrf` issues exactly one token immediately before an action POST.
- Each session holds at most 16 unused CSRF tokens and 8 live intents. Expired/used entries are collected; a full live set returns `429 csrf_token_limit` rather than silently evicting a token another tab is about to use.
- Server restart invalidates every session, token, preview, and cached HTTP result.

### 5.2 Judge intent (memory only)

```go
type judgeIntent struct {
    ID          string
    SessionID   [32]byte
    CreatedAt   time.Time
    ExpiresAt   time.Time
    Argv        []string // complete argv including configured gate binary
    Command     string   // lossless display quoting of Argv
    BinaryID    string
    Run         string
    RequestID   string
    State       intentState // previewed | executing | timed_out | complete
    Result      *judgeResponse
}
```

- Preview intents expire after two minutes.
- The server never accepts replacement argv or form fields on commit.
- `executing` prevents concurrent execution. After `CommandContext` has terminated and reaped a timed-out subprocess, the intent moves to `timed_out`; the same intent may transition back to `executing` on retry. Gate's request id makes the unknown outcome idempotent. `complete` returns the cached response with `replayed:true` until session expiry.
- Cached results are convenience only. On restart, the case file reconstructs success from gate's ledger.

### 5.3 Gate ledger additions

Gate assigns `request_id` during preview and carries it in the canonical argv. The judgment and final action artifacts record it additively. The final action artifact also records the canonical output command, if any. A retry with an existing request id:

- returns the prior result without appending when the recorded semantic inputs match;
- resumes reduction/action if a crash left a matching judgment without its final action;
- fails `request_conflict` if the id exists with different inputs.

No signing key, CSRF token, session id, or preview intent enters the ledger.

### 5.4 Conditional Mint inputs (P3 stub only)

If a later design satisfies the human-presence gate, Mint must present all of these explicitly and pass them unchanged to gate's preview contract:

| Input | Contract |
|---|---|
| `repo` | Required `owner/repo`; gate validates and canonicalizes. |
| `action` | Required enum advertised by gate; v1 is `merge` only. |
| `max_tier` | Required `T0`–`T3`; no hidden default. |
| `max_cycles` | Required non-negative integer; `0` is displayed as **unbounded**, never silently defaulted. |
| `ttl` | Required positive Go duration accepted by gate; no browser-owned policy/default. |
| absolute expiry | Gate preview returns UTC `expires_at`; browser renders that instant in the operator's local timezone with offset. It is display-only, not a second input. |
| `init` | Omitted normally. Shown and accepted only when gate preview says the canonical state has no `log.jsonl`; hidden and rejected otherwise. |

The eventual preview must echo all fields plus the absolute local expiry. None of this creates an HTTP contract today.

## 6. Gate CLI action contract

### 6.1 Read-only Judge preview

Console invokes a new read-only mode:

```text
gate judge -preview -json -run run_… -grant grt_… -decision pass|block -why <text>
```

The successful stdout envelope is:

```json
{
  "schema": "gate.judge-preview.v1",
  "request_id": "gact_…",
  "binary_id": "sha256:…",
  "expires_at": "2026-07-21T19:02:00Z",
  "run": "run_…",
  "escalation_id": "art_…",
  "argv": [
    "judge", "-json", "-run", "run_…", "-grant", "grt_…",
    "-decision", "pass", "-why", "…", "-request-id", "gact_…",
    "-expect-binary-id", "sha256:…",
    "-expect-escalation", "art_…"
  ]
}
```

The real vector may also contain the configured `-state` argument. It must never contain key bytes. Preview runs a clean audit, validates the run/escalation and form, and checks the grant at preview time, but it writes nothing. `expires_at` bounds the console intent; commit still uses current wall-clock grant expiry.

### 6.2 Judge commit

The exact preview argv invokes normal `gate judge` with four additions:

- `-json` requests versioned result/error envelopes;
- `-request-id` supplies idempotency identity;
- `-expect-escalation` supplies the state precondition;
- `-expect-binary-id` binds consent to the executable that produced the preview.

Gate performs an audited read, verifies that the named escalation is still the one unresolved target, checks request-id history, and rechecks the grant before the first new append. Concurrent writers use an optimistic head compare under gate's existing interprocess state lock: unrelated writes may trigger a bounded re-read/retry, but a changed target returns `stale_run`. The first authoritative append carries `request_id`; a second request cannot resolve the same escalation.

Successful stdout:

```json
{
  "schema": "gate.action-result.v1",
  "request_id": "gact_…",
  "action": "judge",
  "recorded": true,
  "replayed": false,
  "run": "run_…",
  "pr": "owner/repo#85",
  "decision": "pass",
  "tier": "T1",
  "outcome": "would_merge",
  "why": "…",
  "command": {
    "argv": ["gh", "pr", "merge", "85", "--repo", "owner/repo", "--match-head-commit", "…"],
    "display": "gh pr merge …"
  }
}
```

`command` is absent when no follow-on command exists. Gate's existing outcome exit codes remain load-bearing: `0` for `would_merge`, `1` for a recorded block, `2` for a recorded park. All three are successful **judgment HTTP** results because the requested judgment was durably recorded. Console requires exit code and JSON outcome to agree.

Structured failure stdout:

```json
{
  "schema": "gate.action-error.v1",
  "request_id": "gact_…",
  "code": "stale_run",
  "message": "run … was already resolved",
  "retryable": false
}
```

Capability refusals use gate exit `3` and a coded body. Invalid/stale/integrity/engine failures use exit `4` and a coded body. Plain stderr, malformed JSON, or a code/body mismatch is `invalid_gate_response`, never guessed from prose.

### 6.3 Required gate error codes

- `invalid_request`
- `invalid_run`
- `invalid_grant`
- `invalid_decision`
- `invalid_rationale`
- `stale_run`
- `request_conflict`
- `grant_expired`
- `grant_scope_mismatch`
- `grant_bad_signature`
- `grant_key_missing`
- `log_integrity_failed`
- `action_protocol_unsupported`
- `action_internal`

Error prose is for humans; callers branch only on these codes.

`request_conflict` is a defensive gate invariant: normal browser flow cannot create it because gate generates the request id and console stores immutable argv. Its HTTP mapping still fails closed at `409`; it is an operator-visible integrity fault, not a prompt to retry with changed fields.

## 7. HTTP contract

All `/api/action-*` and `/api/actions/*` responses set `Content-Type: application/json`, `Cache-Control: no-store`, and `X-Content-Type-Options: nosniff`. Action POSTs require exact same-origin `Origin`, a valid Host, `Content-Type: application/json`, the host-only session cookie, and `X-CSRF-Token`.

### 7.1 `GET /api/action-session`

Creates or reuses a session and reports whether actions are available. It does not issue a CSRF token.

```json
{
  "schema": "console.action-session.v1",
  "session_expires_at": "2026-07-21T19:30:00Z",
  "actions_enabled": true,
  "disabled_reason": null,
  "gate": { "binary_version": "…", "binary_id": "sha256:…", "protocol": 1 },
  "actions": ["judge"]
}
```

Cookie:

```text
console_session=<opaque>; Path=/; HttpOnly; SameSite=Strict; Max-Age=1800
```

`Domain` is omitted, making it host-only. `Secure` is intentionally omitted because console serves plain HTTP on loopback; adding `Secure` would make the cookie unreliable on `http://127.0.0.1`. Loopback/Host/Origin controls and the stated same-user residual are the compensating boundary. A future HTTPS listener must add `Secure` and use a `__Host-` cookie.

### 7.2 `GET /api/action-csrf`

Requires a valid action session and returns one fresh single-use token for the next POST:

```json
{
  "schema": "console.csrf.v1",
  "csrf_token": "base64url…",
  "csrf_expires_at": "2026-07-21T19:10:00Z"
}
```

The UI calls this only immediately before preview or commit, never speculatively on page load. At the 16-live-token cap it returns `429 csrf_token_limit`; the UI surfaces the condition and waits for tokens to be used/expire rather than evicting or silently retrying.

### 7.3 `POST /api/actions/judge/preview`

Request:

```json
{
  "run": "run_…",
  "grant": "grt_…",
  "decision": "pass",
  "rationale": "The permanent rationale."
}
```

Response `200`:

```json
{
  "schema": "console.judge-preview.v1",
  "intent_id": "jint_…",
  "expires_at": "2026-07-21T19:02:00Z",
  "command": {
    "argv": ["C:\\path\\gate.exe", "judge", "…"],
    "display": "C:\\path\\gate.exe judge …"
  }
}
```

The browser displays `command.display` as text. It does not receive a field that can replace the stored argv. Preview consumes its CSRF token even when gate rejects the form; the UI obtains a new token for the next POST.

### 7.4 `POST /api/actions/judge/commit`

Request:

```json
{ "intent_id": "jint_…" }
```

Response `200` wraps gate's result without reinterpreting the outcome:

```json
{
  "schema": "console.judge-result.v1",
  "intent_id": "jint_…",
  "replayed": false,
  "gate_exit": 0,
  "result": { "schema": "gate.action-result.v1", "outcome": "would_merge", "command": { "display": "gh pr merge …" } }
}
```

A retry of the same completed intent returns the same body with `replayed:true`. A request while the intent is executing returns `409 action_in_progress` plus `Retry-After: 1`; it never starts a second subprocess.

### 7.5 Mint and merge contracts

There are deliberately no routes for Mint or Merge:

```text
POST /api/actions/mint/preview  -> 404
POST /api/actions/mint/commit  -> 404
POST /api/actions/merge         -> 404
```

The static app contains no fetch call targeting them. Gate's returned merge command is text/copy data only.

### 7.6 HTTP error envelope and mapping

```json
{
  "schema": "console.error.v1",
  "error": {
    "code": "stale_run",
    "message": "The case changed; refresh before judging.",
    "retryable": false,
    "field": null
  }
}
```

| HTTP | Code(s) | Meaning / next behavior |
|---:|---|---|
| `400` | `invalid_json`, `invalid_request` | Malformed shape, unknown/trailing field, invalid id shape. No subprocess for transport-invalid input. |
| `403` | `forbidden_host`, `forbidden_origin`, `csrf_invalid`, `session_invalid`, `grant_expired`, `grant_scope_mismatch`, `grant_bad_signature`, `grant_key_missing` | Request is not permitted. A `401` is not used because console has no authentication challenge. |
| `409` | `stale_run`, `request_conflict`, `action_in_progress` | Refresh/review state. Never silently re-preview and submit. |
| `410` | `session_expired`, `csrf_expired`, `intent_expired` | Obtain a new session/token or preview. |
| `413` | `request_too_large` | Body exceeded 16 KiB. |
| `415` | `unsupported_media_type` | JSON content type required. |
| `422` | `invalid_run`, `invalid_grant`, `invalid_decision`, `invalid_rationale` | Well-formed body with rejected field value. |
| `423` | `audit_tampered` | All action controls lock; reason remains visible. |
| `429` | `csrf_token_limit` | Too many unused tokens exist for this session; no token is evicted. |
| `502` | `gate_unavailable`, `invalid_gate_response`, `action_internal` | Gate failed or violated its contract; no success is inferred. |
| `503` | `gate_incompatible`, `actions_disabled` | Read-only console remains available; action controls stay disabled. |
| `504` | `gate_timeout` | Outcome is unknown. Retry the **same commit intent**; request-id idempotency resolves it. |

Decision outcomes `blocked` and `parked_for_judgment` do not become HTTP errors when the judgment append succeeded. They return `200` with the gate result and its original exit code.

## 8. Key flows and failure semantics

### 8.1 Page load and control enablement

1. Existing read-only case-file and audit requests run.
2. Browser requests `/api/action-session`; server checks explicit Judge enablement, the live gate compatibility/binary-identity contract, and a clean audit. No CSRF token is allocated on load.
3. If any check fails, the response carries `actions_enabled:false`; the radios, rationale, preview, and commit controls are disabled, and the exact reason is visible.
4. If enabled, decision radios remain empty. The grant field may use gate's current suggestion.

### 8.2 Preview and consent

1. Operator selects pass/block and writes a rationale.
2. Browser fetches a fresh token from `/api/action-csrf`, then POSTs preview with exact same-origin `Origin` automatically supplied by the browser.
3. Console consumes the token, strictly decodes the body, rechecks compatibility/audit, and shells gate preview.
4. Gate validates current run/grant state and returns a request id, escalation precondition, and canonical argv.
5. Console stores the complete argv in a session-bound, two-minute intent and displays it.
6. Only now does “record judgment” enable. Any form edit deletes the intent from the browser and requires a new preview.

The command echo is a consent artifact in the UI, not an identity proof. The test suite asserts the displayed argv and executed argv are byte-for-byte equal by element.

### 8.3 Commit and returned action

1. Browser fetches a new token from `/api/action-csrf` and POSTs only `intent_id`.
2. Server atomically moves the intent from `previewed` or `timed_out` to `executing`.
3. Gate verifies its expected binary identity, re-audits, checks compatibility version, request-id history, unresolved escalation, and current grant, then records the judgment/reduction/action.
4. Console validates exit/body agreement and caches the response under the intent.
5. UI renders the permanent rationale and outcome. A returned merge command is pinned as copyable text; no handler can execute it.
6. UI performs GET refreshes of case file, docket, and audit. Browser history receives only GET state, never a form-resubmittable document.

### 8.4 Double submit, retry, refresh, and restart

- **Double click / same intent:** one request wins `executing`; the other gets `409 action_in_progress` or the cached `200 replayed:true`.
- **Network/subprocess timeout after commit:** once the timed-out process is terminated and reaped, the intent enters `timed_out`. Retry the same intent; it may execute again with the same request id, and console cache or gate replay/resume returns the original result without a second append.
- **Refresh before commit:** preview is not auto-committed. The UI may recover the still-live intent from `sessionStorage`, but must show the command again and require the explicit commit click.
- **Refresh after commit:** GET projections render the ledger result. No POST repeats.
- **Console restart before commit:** cookie and intent are invalid; commit returns `410 session_expired`/`intent_expired`; re-preview from current state.
- **Console restart after commit but before response:** refresh the case file. Gate's ledger is canonical; a new attempt against the resolved escalation returns `stale_run`, while the recorded result remains visible.

### 8.5 Concurrent tabs and stale runs

- Tabs sharing a session obtain distinct single-use CSRF tokens.
- Two previews for the same escalation may coexist. The first successful commit resolves it; the second returns `409 stale_run` with zero new append.
- A newer escalation or terminal action between preview and commit invalidates `-expect-escalation`.
- Unrelated ledger traffic may cause gate's optimistic head compare to retry internally, but never causes it to skip re-audit or target-state validation.

### 8.6 Stale or changed grants

- A grant valid at preview but expired at commit returns `403 grant_expired`; nothing is appended.
- Wrong repo/action scope returns `403 grant_scope_mismatch`.
- Bad signature or missing key returns `403` with its coded reason; console never attempts key repair.
- A newly minted replacement grant does not rewrite an old preview. The operator edits the grant field and obtains a new preview.

### 8.7 Audit tampering

- Client-side: masthead tamper banner remains; all action controls disable.
- Server-side: preview and commit each invoke a gate path that audits before acting. A forged browser request cannot bypass the disabled UI.
- Gate-side: tampering is a hard `log_integrity_failed`; no judgment, reduction, action, or console journal entry is written.
- Recovery is operational and out of band. The console offers no repair, reseal, truncate, or override button.

### 8.8 Gate incompatibility or malformed output

- Read-only `next`, `explain`, and `audit` may continue if their existing contracts work.
- The action-session response names the mismatch; action POSTs return `503 gate_incompatible`.
- Preview stores gate's `binary_id`; commit's exact argv requires it. A changed PATH target, symlink target, or upgraded binary returns `503 gate_incompatible`/`action_protocol_unsupported` before mutation.
- Unknown fields in a compatible newer envelope may be tolerated only where the versioned schema declares forward compatibility; missing required fields, unknown schema names, or exit/body mismatch fail closed.
- Console never falls back to constructing a legacy `gate judge` command itself.

## 9. Security and browser validation gate

Mint may not begin merely because Judge “seems fine.” The validation gate is binary and evidence-based.

### 9.1 Required test tiers

**Tier 0 — Go unit/integration, always on**

- Host aliases/port pinning and rejection of foreign/malformed Host.
- Origin exact-match matrix, including missing, `null`, foreign, scheme/port/alias mismatch.
- Cookie attributes, session absolute expiry, token expiry/use-once semantics, and restart invalidation.
- No token on action-session load; just-in-time token issuance; 16-token cap returns `429` without eviction; concurrent tabs retain their own valid tokens.
- Strict JSON decoder, size limit, rationale constraints, and no subprocess on transport-invalid requests.
- Preview intent binding, edit invalidation contract, exact displayed/executed argv equivalence, binary-identity drift refusal, `timed_out` retry, and all error mappings.
- Fake gate exit/body compatibility, audit tamper, incompatible version, malformed JSON, and missing features.
- Gate tests for request-id idempotency, partial-write resume, two-request same-escalation race, stale escalation, stale/expired/wrong-scope grant, and audit tamper with zero unauthorized append.
- Hygiene assertion that console imports no gate package.

**Tier 1 — executable browser JavaScript, required for the gate**

A build-tagged `go test -tags=browser ./cmd/console/...` suite launches the real embedded page under system Chrome/Chromium with `chromedp` and a fake gate binary/server fixture. It proves:

1. neither decision radio is selected on load or refresh;
2. preview is disabled until decision and trimmed rationale exist;
3. exact argv is visible before commit and an edit invalidates it;
4. success displays gate's returned merge command but issues no merge request or subprocess;
5. missing/bad/replayed CSRF, foreign Origin, and bad Host produce the coded failures and no gate commit;
6. double click produces one gate commit; retry returns the same result;
7. two tabs racing one escalation produce one success and one stale result;
8. refresh never repeats a POST;
9. expired intent and expired grant fail visibly;
10. audit tamper and incompatible gate disable controls with the reason visible;
11. replacing/upgrading the configured gate binary after preview expires the intent without a commit;
12. a subprocess timeout can retry the same intent and converges on one gate result.

The browser job is a required CI check for P2 and validation changes. It may use the Chrome already installed on GitHub-hosted runners; it must not download a browser at test time.

**Tier 2 — manual visual/accessibility check**

Inspect the case file at narrow/wide widths, keyboard-only through radios/rationale/review/commit, focus movement on errors, copy affordances, and tamper/incompatibility banners. This is required evidence in the validation PR but is not the security signal by itself.

### 9.2 Adversarial review

Request Codex, Claude, Cursor, and Copilot on the design PR and again on the validation PR. Review instructions target:

- the same-user local-process trust assumption;
- CSRF/Origin/Host/cookie bypasses;
- exact-argv time-of-check/time-of-use gaps;
- request-id races and crash recovery;
- mint confused-deputy risk and no-go criteria;
- any failure that appends before authorization or reports success without a canonical ledger artifact.

### 9.3 Binary GO/NO-GO signal

Judge validation is **GO** only when all of the following are true on the final review head:

1. Tier 0 and Tier 1 commands pass in CI and locally on the operator platform.
2. Deployment evidence proves governed agents cannot reach loopback or already possess equivalent `gate judge` capability; absent that proof, Judge remains disabled and the gate is NO-GO.
3. A scripted 50-way same-escalation commit race records exactly one judgment path, every other request resolves as replay/in-progress/stale, and `gate audit` remains intact.
4. The attack matrix records zero ledger mutations for bad Host, bad/missing Origin, bad/replayed CSRF, expired intent, stale run, expired/wrong-scope/bad-signature grant, binary drift, incompatible gate, malformed gate output, and tampered audit.
5. No unresolved critical/high finding remains from the four requested reviewers; accepted residuals are written into this threat model.
6. Manual evidence shows the exact displayed argv equals the executed argv and the returned merge command is never executed.

Any failure is **NO-GO**: Judge remains copy-paste CLI, Mint remains CLI-only, and P3 stays task-less.

Passing this gate validates the shared action plane for bounded judgment. It does **not** prove human presence for Mint. P3 remains no-go unless a separately reviewed design also satisfies §11.

## 10. Rollout / implementation plan

| Phase | Goal | High-level tasks | Depends on | Gate | Rough scope |
|---|---|---|---|---|---:|
| **P1 — action security plane + compatibility contract** | Build the fail-closed substrate before any route mutates | Gate `version -json` + binary identity; Judge preview/result/error schemas; request-id + stale-escalation guards; exact argv generation; gatecli exit/envelope validation; default-disabled Judge flag; session/cookie/Origin/just-in-time CSRF/intent mechanism; timeout retry; audit/incompatibility lockout; Tier 0 tests | — | pre-gate, no browser mutation yet | 500–800 wLOC |
| **P2 — judgment form + end-to-end action** | Add the first bounded authority-bearing browser action | Case-file form; no default decision; rationale validation; preview/review/commit UX; result/merge-command rendering; refresh behavior; server routes; gate commit wiring; Tier 0 + chromedp scenarios | P1 | pre-gate | 400–700 wLOC |
| **VALIDATION — adversarial proof** | Decide whether the action plane is safe enough to retain | Four-agent review; attack matrix; 50-way race; final-head browser run; manual exact-argv/no-merge evidence; fold fixes into P1/P2 contracts | P2 | **GO/NO-GO** | 100–250 wLOC/tests + evidence |
| **P3 — Mint reconsideration (stub)** | Decide whether browser mint can ever prove human presence | Default result is CLI-only. Only after Judge GO: separate threat-model amendment; evaluate OS-mediated WebAuthn/user verification; registration/recovery; gate-bound parameters; new independent validation gate. No Judge code folded into this PR. | VALIDATION GO + separate human-presence proof | **currently NO-GO; task-less** | uncommitted |
| **P4 — optional hardening/follow-ups (stub)** | Respond to observed friction only | Possible secure-loopback HTTPS, broader action-result query, record/runs panes, accessibility hardening, operational telemetry—each demand-gated | prior evidence | not committed | uncommitted |

P1, P2, validation, and any future P3 are separate PRs. Judge and Mint are never implemented together.

## 11. Mint go/no-go criteria

Mint stays CLI-only unless a later reviewed design proves all of these:

1. **Unforgeable user verification:** an OS/platform authenticator performs user verification and produces a cryptographic assertion bound to the exact repo, action, tier, cycles, TTL, expiry, and `init` choice. A click, typed phrase, delay, CAPTCHA, pointer gesture, Origin, or CSRF token does not qualify.
2. **Gate verification:** gate—not console—verifies the assertion and binds it to the mint. Console never receives or handles signing-key bytes.
3. **No automation fallback:** no MCP verb, API token, “remember me,” headless path, recovery bypass, or CLI flag can convert the browser flow into agent automation. The existing operator CLI may remain, governed by external key custody/tool guards.
4. **Enrollment and recovery:** authenticator registration, replacement, loss, and reset have an explicit trust root that cannot be exercised by the same local agent being constrained, including through any endpoint on the loopback service.
5. **Two-step consent:** gate returns the exact canonical mint argv/parameters and absolute local expiry for review; after review, a fresh user-verification assertion is bound to that immutable preview. Editing any field invalidates both.
6. **Fresh-state safety:** `-init` is offered only on gate-attested fresh state and is included in the signed assertion; wrong-directory mint remains fail-closed.
7. **Independent proof:** a dedicated Mint browser/adversarial suite demonstrates no grant artifact for forged/replayed/mismatched/expired assertions, concurrent tabs, restart, or tampered audit.

If any item is unavailable or disproportionate, retaining CLI-only mint is the successful outcome, not unfinished work.

## 12. Open questions

The Judge trust boundary is no longer an open default: absent positive deployment evidence, actions stay disabled and P2 is NO-GO. Reviewers should challenge these deliberately deferred forks:

1. If Mint is ever reconsidered, is platform WebAuthn with required user verification supportable on the operator's browsers/OS without an automation fallback? Until proven, the answer is no.
2. Does gate's request-id crash recovery need a general state batch/CAS primitive, or can a minimal guarded append plus idempotent resume preserve the existing state mechanism cleanly? P1 must settle this in gate's design before implementation.

## 13. Validation plan summary

1. Review and lock this TDD; no action code ships from the design PR.
2. Land P1 as a non-UI action substrate with unit/integration proof.
3. Land P2 as the Judge form and chromedp end-to-end tier.
4. Run the §9 adversarial gate on the final P2 head.
5. **GO:** retain Judge and close the validation phase. **NO-GO:** disable/remove the mutating route and keep the existing copy-paste command.
6. In either outcome, Mint remains CLI-only unless a separate P3 design satisfies every criterion in §11.
