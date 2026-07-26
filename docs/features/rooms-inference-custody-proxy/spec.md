# rooms-inference-custody-proxy — Technical Design Document

**Status:** draft / proposal — NOT a build commitment. The artifact we decide from.
**Owner:** @itsHabib
**Date:** 2026-07-26
**Related:** [`docs/features/custody/spec.md`](../custody/spec.md) (the proxy this routes through) · workbench#125 (the egress wall this unblocks) · [`docs/features/rooms-inference-custody-proxy` dossier task `tsk_01KYEH96CP94P6FDJAHYS2AZTN`] · rooms `docs/features/vsock-secrets/spec.md` (the secret-delivery upgrade this pairs with) · memories `runway-custody-integration`, `rooms-egress-hostname-unreliable`

> **Reviewers — focus areas:**
> - **§4 D2** — how the grant reaches custody: the SDK/CLI can only set the *vendor* auth header, but custody v0 reads `X-Custody-Grant` (custody spec §6). This is the load-bearing fork; everything else follows from it.
> - **§4 D1 / §9** — claude-code first, cursor-SDK gated. Whether `@cursor/sdk@1.0.16` honors a base-URL override is *unverified in-repo* and gates phase 2.
> - **§7 flow B** — the streaming (SSE) inference request through custody's no-redirect, stream-verbatim engine.

## 1. Problem & hypothesis

The custody egress wall (workbench#125, `rooms.go:382` `egressAllowlist`) drops **all** guest
egress except the custody proxy (the tap gateway) plus operator-configured
`RUNWAY_ROOMS_EGRESS_EXTRA` (git CIDR + guest DNS resolver IPs). The wall arms only when a
placement carries a `custody:` secret ref (`hasCustodyRef`, `rooms.go:405`) — additive infra
today, since nothing ships custody refs yet.

But the agent runner does its **model inference** with a *raw* vendor key —
`CURSOR_API_KEY` for the cursor SDK, `ANTHROPIC_API_KEY` / `CLAUDE_CODE_OAUTH_TOKEN` /
`ANTHROPIC_AUTH_TOKEN` for claude-code — forwarded verbatim over SSH SendEnv
(rooms `runner.rs:921-928`) and calling the vendor **directly**. The moment a placement
carries a custody ref, the wall arms and that direct vendor call is dropped: **a custody
workload cannot run its own inference.** Latent today (no custody workload exists);
a hard blocker the day the first one ships. Surfaced by codex P1 on #125.

**The bet:** route inference through the custody proxy — the same prefix-mapped reverse
proxy custody already defines (custody spec §3). The vendor key becomes a
custody-managed credential the *host* holds; the guest carries only a scoped, short-TTL
grant. Then the only egress a custody workload needs is proxy + git + DNS, which is
exactly what the wall already permits. Inference stops being the exception that forces a
hole in the wall.

**Non-goals:**
- **Not** allowlisting the inference CDN. Cursor/Anthropic are multi-IP CDN-backed; rooms
  pins an allowlist hostname to one host-resolved IP at launch (`egress.rs:161` `resolve`),
  the untrusted guest resolves a *different* IP, and the wall drops it. Host-validated
  2026-07-25 (memory `rooms-egress-hostname-unreliable`). Routing through the proxy makes
  the guest talk to **one stable host-local address** (the tap gateway) and lets the *host*
  proxy follow the vendor's real DNS — the multi-IP fragility disappears by construction.
- **Not** the vsock secret-delivery move (that's the sibling TDD; grant + base URL still
  ride SendEnv here, honestly recorded as `ssh_sendenv` in `receipt.go:64`). The two
  compose but ship independently.
- **Not** a new custody engine. Inference is a custody *key* + *action*, operator manifest
  state (custody spec D4), plus the guest-side plumbing to use it.

## 2. Functional & non-functional requirements

**FR1.** A placement whose inference credential is a `custody:` ref runs its agent's
inference entirely through the custody proxy — no direct vendor egress — and completes a
real task behind the armed egress wall.

**FR2.** The guest runner resolves its inference base URL from `CUSTODY_BASE_<KEY>` and its
inference credential from `CUSTODY_GRANT_<KEY>` (already delivered by
`secretEnvNames`, `rooms.go:606`), and passes **no** raw vendor key on that path.

**FR3.** A placement with a raw (non-custody) inference key keeps today's behavior exactly:
open network, direct vendor call, no wall. The two modes coexist; custody is opt-in per
placement.

**FR4.** Fail closed and legibly: if the base URL or grant is missing/empty, or the proxy
refuses/expires the grant, the run fails with an operator-facing reason — never silently
falls back to a raw vendor key or a direct call.

**FR5.** Every inference request is a custody log line (custody spec §5 artifact log):
grant id, rule fired, verdict, upstream status. "What did this agent's inference touch"
is answerable offline.

| NFR | Target |
|---|---|
| Streaming | SSE inference (POST, long-lived, chunked response) passes through custody unbuffered — first-token latency dominated by the vendor, proxy adds < ~5ms/hop on localhost |
| Security | Guest never holds a raw vendor key on the custody path; grant is scoped to an `inference` action + short TTL; grant leak ≤ one grant's TTL of vendor access, scoped to inference |
| Fail-closed | Missing base URL / grant / proxy = run fails, no direct-vendor fallback (FR4); egress wall already drops the vendor CDN |
| Compatibility | Additive — no change to non-custody placements (FR3), no change to the wall's git/DNS egress path (#125) |

## 3. Architecture overview

```
   ┌─────────────────── rooms guest (microVM, egress-walled) ───────────────────┐
   │  agent runner (claude-code  OR  cursor-runner.js)                           │
   │     base URL  = CUSTODY_BASE_<KEY>   (= <tap-gateway>/<key>)                 │
   │     auth      = CUSTODY_GRANT_<KEY>  (cst1_… bearer, scoped `inference`)     │
   └──────────────┬─────────────────────────────────────────────────────────────┘
                  │ POST http://<tap-gateway>/<key>/v1/messages   (only egress the wall permits)
                  ▼
   ┌─────────── host: custody serve (reverse proxy, custody spec §3) ────────────┐
   │  resolve /<key> → validate grant (from auth header, D2) → match `inference`  │
   │  action → CredRead real vendor secret → inject → forward upstream → stream   │
   │  back verbatim (no redirect follow) → append JSONL log line                  │
   └──────────────┬─────────────────────────────────────────────────────────────┘
                  │ real vendor secret, host follows real DNS (multi-IP fine)
                  ▼            api.anthropic.com / api.cursor.com
```

**Reused, unchanged:** the custody proxy engine, grant envelope, credential store, JSONL
log (custody spec §3–§8); the rooms egress wall + `CUSTODY_GRANT_/CUSTODY_BASE_` delivery
(`rooms.go:382`, `resolver.go`, `config.go:116`); rooms `--egress` iptables enforcer
(`egress.rs`).

**New, three seams:**
1. **custody engine** — an `inference` action shape + reading the grant from the *vendor
   auth header* (D2). Custody-repo change.
2. **placement/controller** — declare the inference credential as a `custody:` ref so the
   wall arms and the derived vars are delivered. Workbench-repo change (small).
3. **rooms guest runner** — consume `CUSTODY_BASE_<KEY>` / `CUSTODY_GRANT_<KEY>` and point
   the SDK/CLI at them. Rooms-repo change (the load-bearing one).

The seam that already exists and *almost* works: workbench delivers `CUSTODY_BASE_<KEY>`
and `CUSTODY_GRANT_<KEY>` to the guest today (`secretEnvNames`, `rooms.go:606`), but the
guest runner never reads them — `cursor-runner.js` calls `Agent.create({apiKey, model,
local})` with no base-URL field (`install-cursor.sh:172-180`) and claude-code's native
`ANTHROPIC_BASE_URL` is in neither the SendEnv list nor any image's `AcceptEnv`. Closing
seam 3 is what makes the delivered vars load-bearing.

## 4. Key decisions & trade-offs

**D1 — claude-code first, cursor-SDK gated on a spike. (DECIDED.)** claude-code honors
`ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN` natively — a proven, documented base-URL
override. `@cursor/sdk@1.0.16` (`install-cursor.sh:19`) exposes only
`Agent.create({apiKey, model, local, name})`; whether it honors *any* base-URL override
(env or option) is **unverified in-repo** — nothing sets one today. So phase 1 proves the
thesis on the claude-code `--command` path (the cheapest end-to-end proof), and the cursor
path is phase 2, gated on a one-task SDK spike. Rejected: block the whole feature on
cursor-SDK support we haven't confirmed exists. If the spike shows the cursor SDK cannot be
pointed at a proxy, cursor custody workloads stay deferred and claude-code is the custody
runner — an acceptable v0 posture, not a dead end.

**D2 — the grant travels in the vendor auth header, not `X-Custody-Grant`. (THE FORK — needs a reviewer call.)**
An inference SDK/CLI lets the caller set exactly two things: the base URL and the API
key/auth token. It will **not** add an arbitrary `X-Custody-Grant` header. But custody v0's
API (custody spec §6) documents the grant arriving in `X-Custody-Grant`, while custody's own
**D1** says clients "change exactly two things: base URL and **auth header value**." Those
two lines are in tension, and inference forces the resolution: on this path the grant
**must** arrive in the vendor's native auth slot —
`Authorization: Bearer <CUSTODY_GRANT>` (claude-code with `ANTHROPIC_AUTH_TOKEN`) or the
`x-api-key` / `Authorization` the cursor SDK sends. Concretely: the guest sets its "API key"
to the custody grant token; custody validates the grant from that slot, strips it, and
injects the real vendor secret from the credential store.

Two ways to land it, reviewer picks:
- **(a) per-key `grant_from` manifest field** — a key entry declares where its grant lives
  (`x_custody_header` default | `authorization_bearer` | `x_api_key`); the inference key
  uses an auth-header source. Keeps the generic proxy default intact, makes the exception
  explicit and per-key. **Recommended** — additive, legible, matches custody D5 (manifest is
  the policy surface).
- **(b) custody always also accepts the grant from the standard auth header.** Simpler, but
  widens the grant-acceptance surface for *every* key and muddies the "grant is a distinct
  header" story. Rejected unless (a) proves fiddly.

Security note this fork raises (→ security review, phase 3): when the grant rides
`Authorization`, custody must **replace** that header with the injected secret before
forwarding (never append), and must still refuse `TRACE`/redirects (custody spec §7C/§7 method
policy) so the grant-bearing header is never echoed back. The custody engine already does
header replacement + redirect refusal; the change is *where it reads the grant*, not the
forwarding invariants.

**D3 — the inference key is operator manifest state, action = `inference`. (DECIDED.)**
Per custody D4, the vendor key + upstream + action rules live in the operator's manifest,
not the repo. The repo carries the schema + a generic example. The `inference` action is a
POST allowlist for the vendor's inference endpoint(s) (`/v1/messages`, `/v1/chat/completions`
and their streaming variants). These are **not** embedded-query-language endpoints, so
custody's deny-by-default query rule (custody spec §7D) does not bite; a method+path rule
suffices. TTL follows a write action's short-TTL posture (custody D6) since inference is a
POST.

**D4 — no fallback, ever. (DECIDED.)** If `CUSTODY_BASE_<KEY>` or `CUSTODY_GRANT_<KEY>` is
empty, or the proxy refuses, the runner fails the workload. It must not read a raw vendor key
(there won't be one on this path — the placement declares only the custody ref) and must not
attempt a direct call (the wall drops it anyway). This mirrors rooms' existing custody
fail-closed at admission (`requireCustodyConfig`, `rooms.go:530`; `requireEgressConfig`,
`rooms.go:556`). Cheap-to-stay-closed over silent-degrade.

**D5 — base URL + grant still ride SendEnv in v0; vsock is the sibling TDD.** The honest
delivery channel today is SSH SendEnv (`receipt.go:64` `ssh_sendenv`, `OneShot:false`). This
TDD does **not** move to vsock; it only adds two env names to the SendEnv/AcceptEnv
allowlists. When the vsock secret-delivery TDD lands, the *same* `CUSTODY_BASE_/CUSTODY_GRANT`
vars move to the vsock blob (rooms `docs/features/vsock-secrets/spec.md`; the blob is an
arbitrary `NAME=value` carrier, `install-cursor.sh:79-84`) with no change to this feature's
contract — the runner reads the same names from a different source.

## 5. Data model

No new persistent types on the rooms/workbench side. Two additions elsewhere, both additive:

- **Custody manifest — an inference key entry** (operator state, custody spec §5 shape):
  ```jsonc
  "anthropic-inference": {
    "secret": "wincred:anthropic-api-key",
    "upstream": "https://api.anthropic.com",
    "grant_from": "authorization_bearer",          // D2 (a): where the grant is read
    "inject": [ { "kind": "header", "name": "x-api-key", "template": "{secret}" } ],
    "actions": {
      "inference": { "rules": [
        { "methods": ["POST"], "path": "/v1/messages" },
        { "methods": ["POST"], "path": "/v1/messages/count_tokens" }
      ] }
    },
    "note": "Model inference only. POST /v1/messages. No other reach."
  }
  ```
  The `grant_from` field is the D2 seam; the repo ships this as a schema field + example,
  never a live manifest (custody D4).

- **rooms images — two new `AcceptEnv` names** (`ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`;
  cursor equivalents in phase 2) in `build-rootfs-alpine.sh` / `install-cursor.sh`. Build-time
  config, not runtime state.

## 6. API / config contract

**rooms guest env (delivered, already):** `CUSTODY_BASE_<KEY>` (`= <TapGateway>/<key>`,
`config.go:116`), `CUSTODY_GRANT_<KEY>` (child bearer token, `resolver.go`), where `<KEY>` is
the custody key upper-cased with `-`→`_` (`envKey`, `resolver.go`).

**rooms guest runner — new mapping (this feature):**
- claude-code `--command` path: `ANTHROPIC_BASE_URL := CUSTODY_BASE_<KEY>`,
  `ANTHROPIC_AUTH_TOKEN := CUSTODY_GRANT_<KEY>`; unset the raw `ANTHROPIC_API_KEY`.
- cursor path (phase 2, gated): `Agent.create({ apiKey: CUSTODY_GRANT_<KEY>, baseUrl:
  CUSTODY_BASE_<KEY>, … })` **iff** the SDK supports a base-URL option (D1 spike).

**rooms transport allowlists — new names:** add `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`
to the SendEnv list (`runner.rs:921-928`) and to each image's sshd `AcceptEnv`
(`build-rootfs-alpine.sh:239`, `install-cursor.sh:261`).

**workbench controller — placement secret declaration:** a custody placement declares its
inference credential as a `custody:<key>/inference` ref (so `hasCustodyRef` fires) instead of
a raw `allowedSecrets` name (`rooms.go:32`). The rooms backend already resolves the ref to the
two guest vars; no rooms-backend change is needed for delivery — only the runner-side
consumption (rooms repo) and the manifest/engine work (custody repo).

**custody engine — new surface:** honor `grant_from` per key (D2); an `inference` action is
just method+path rules (no new engine primitive). No CLI change beyond `custody keys set` for
the new key + `custody grant -key <k> -actions inference`.

## 7. Key flows

**A — admission (host, workbench).** Placement carries `custody:anthropic-inference/inference`.
`hasCustodyRef` → true → `requireCustodyConfig` checks the tap gateway + source are set
(`rooms.go:530`), `requireEgressConfig` checks `EgressExtra` carries git CIDR + DNS
(`rooms.go:556`). Resolver derives a source-bound child grant; `secretEnvNames` schedules
`CUSTODY_GRANT_ANTHROPIC_INFERENCE` + `CUSTODY_BASE_ANTHROPIC_INFERENCE` for SendEnv
(`rooms.go:606`). `egressAllowlist` = `[tap-gateway-host, <git CIDR>, <DNS IPs>]`
(`rooms.go:382`); `runArgs` emits `--egress allowlist:…` (`rooms.go:364`). Wall armed.

**B — inference through the proxy (the load-bearing path).** Runner in the guest sets
`ANTHROPIC_BASE_URL=http://<tap-gateway>/anthropic-inference` and
`ANTHROPIC_AUTH_TOKEN=<grant>` → claude-code POSTs `…/v1/messages` (streaming) → the only
egress the wall permits (tap gateway) → custody resolves `/anthropic-inference`, validates the
grant **from `Authorization: Bearer`** (D2), matches `inference[0]`, CredReads the real key,
**replaces** the auth header with the injected `x-api-key`, forwards to `api.anthropic.com`
(host follows real DNS — multi-IP fine), streams the SSE response back verbatim, no redirect
follow → JSONL `verdict: pass, rule_fired: inference[0]`. Agent gets its tokens; the raw key
never entered the guest.

**C — grant refused / expired mid-run.** Grant TTL lapses between turns → custody returns
`401 refused_expired` (custody spec §7E) → the SDK/CLI surfaces an auth error → the runner
fails the workload with the custody remedy in the log. No direct-vendor retry (wall drops it;
D4). Operator re-mints, replays the placement.

**D — non-custody placement (unchanged, FR3).** Raw `ANTHROPIC_API_KEY` in `allowedSecrets`,
no custody ref → `hasCustodyRef` false → `egressAllowlist` nil → no `--egress` flag → open
network → direct vendor call. Byte-for-byte today's behavior.

**E — misconfig fail-closed (FR4).** Base URL empty (gateway unset) → admission already
refuses (`requireCustodyConfig`). Grant var empty at runtime → runner refuses to start
inference with a named error, does not fall back.

## 8. Failure model

Single proxy, single host. Grant validation stateless per request (custody spec §8). Proxy
down = connection refused = fail closed (no fallback path exists on the custody placement).
Streaming: custody forwards the SSE stream unbuffered and follows no redirect — a vendor 3xx
is streamed back, not chased with the credential (custody spec §7C). Grant is a bearer
capability (custody spec §8.1): a leaked grant is replayable for its TTL, scoped to the
`inference` action — which is why inference grants get a short TTL (custody D6). The raw
vendor secret stays host-side in the credential store; a compromised guest reaches only the
proxy, only the `inference` action, only for the grant's TTL.

## 9. Rollout / implementation plan

| Phase | Goal | High-level tasks | Depends on | Gate |
|---|---|---|---|---|
| 1. `inference-proxy-claude` | claude-code inference runs end-to-end through the custody proxy behind the armed wall | (1a) custody engine: `grant_from` per-key (D2) + `inference` action shape + SSE/no-redirect verified [opus]; (1b) workbench controller: declare the inference credential as a `custody:` ref on custody placements [sonnet]; (1c) rooms guest: add `ANTHROPIC_BASE_URL`/`ANTHROPIC_AUTH_TOKEN` to SendEnv + AcceptEnv and map `CUSTODY_BASE_/CUSTODY_GRANT_` → claude-code base-url + auth, drop raw key [opus] | #125 (merged), custody v0 | **VALIDATION GATE** below |
| 2. `inference-proxy-cursor` | cursor-SDK inference through the proxy | SDK base-URL spike (does `@cursor/sdk@1.0.16` honor an override? — the D1 unknown); if yes, pass `baseUrl` + grant into `Agent.create` + cursor `AcceptEnv`; if no, document cursor custody as deferred | phase 1 + gate + spike outcome | each task needs the spike result first |
| 3. `inference-proxy-harden` | security-review the auth-header grant path + converge with vsock | security review of D2 (auth-header replacement, TRACE/redirect, no echo); converge `CUSTODY_BASE_/CUSTODY_GRANT_` onto vsock delivery (sibling TDD); per-agent grant identity if attribution needs it | phase 2 + vsock TDD | logged incident / review finding first |

Rough scope: phase 1 is three PR-sized tasks across two repos (1c, the guest runner + image,
is the largest and the load-bearing one). Phase 2 is a spike + one task. Phase 3 is
deliberately unsized and may fold into the vsock TDD.

**VALIDATION GATE (after phase 1):** one custody-ref'd **claude-code** placement runs a real
task on the rooms-host behind the armed egress wall (`allowlist:` = tap gateway + git CIDR +
DNS only), and:
- (a) the agent completes its inference — tokens flow — with **no** raw vendor key delivered
  to the guest (verify the guest env carries only `CUSTODY_GRANT_/CUSTODY_BASE_`);
- (b) `witness.json` / `witness.pcap` (#123) show **zero** direct-vendor egress — all
  inference traffic went to the tap gateway;
- (c) `custody log` shows the `inference` requests with `verdict: pass`.
Phases 2–3 are not committed until this passes.

## 10. Open questions

1. **Does `@cursor/sdk@1.0.16` honor any base-URL override?** (D1) — unverified in-repo; the
   phase-2 spike answers it. If not, is a vendored SDK patch acceptable, or does cursor custody
   simply wait?
2. **D2 (a) vs (b)** — per-key `grant_from` field vs custody universally accepting the grant
   from the auth header. Reviewer call; (a) recommended.
3. **Key naming** — one inference key per vendor (`anthropic-inference`, `cursor-inference`)
   vs a shared logical `inference` key. Per-vendor is cleaner for the manifest `upstream` +
   `inject` template; confirm.
4. **claude-code auth token vs api key** — `ANTHROPIC_AUTH_TOKEN` (→ `Authorization: Bearer`)
   is the natural grant slot; confirm claude-code sends Bearer (not `x-api-key`) when
   `ANTHROPIC_AUTH_TOKEN` is set, so custody reads the right header (D2).

## 11. Validation plan

The §9 gate is the plan; its signal is binary and baseline-free: run one real claude-code
custody placement behind the wall and confirm (a) no raw key in the guest env, (b) witness
shows zero direct-vendor egress, (c) custody log records the inference requests. If all three
hold, the thesis — inference runs through the proxy behind the wall — is proven and phase 2
unlocks. If (b) fails (any direct-vendor packet), the wall or the runner mapping is wrong and
the feature is not done regardless of whether tokens flowed.
