# custody - Technical Design Document

**Status:** draft / proposal - NOT a build commitment. The artifact we decide from.
**Owner:** @itsHabib
**Date:** 2026-07-20
**Related:** [auto-mode defaults](../../auto-mode-defaults.md), [gate enforcement model](../../../cmd/gate/docs/enforcement.md), dossier project `workbench`

> **Reviewers - focus areas:** the grant-envelope reuse question (§4 D2), the
> rule-matching semantics and their bypass surface (§7, flows C-E), the manifest
> schema (§5), and whether the honesty section (§8.1) overclaims or underclaims.
> This is a design review, not a code review.

## 1. Problem & hypothesis

Business goals, in order:

1. **Secrets out of agent context.** Today agents read raw API credentials from a
   plaintext JSON file on disk. The secret enters the model transcript, from which it
   can leak anywhere a transcript goes, and it carries the operator's full identity:
   every scope the human has, forever, unaudited.
2. **Scoped, expiring, audited third-party access** - the precondition for turning
   agent autonomy up. Per [auto-mode defaults](../../auto-mode-defaults.md), autonomy
   is safe in proportion to how much of the decision surface is deterministic code the
   model cannot skip. Vendor API access currently has none.
3. **One broker for every key, near-zero policy cost for low-stakes keys.** The
   operator should not have to write a per-vendor broker app. A high-stakes key (a
   work issue tracker whose other projects may hold export-controlled data) gets tight
   rules; a hobby-project vendor gets one `all` action and a long-lived grant, and
   still picks up custody, audit, and token-out-of-context for free.

**Hypothesis:** a single local reverse proxy with per-key manifests and mintable,
scoped, expiring action grants converts "the agent holds my identity" into "the agent
holds a capability I minted", at a policy cost low enough that every static-header
HTTP credential the operator owns migrates behind it.

**Non-goals (v0):** OAuth flows and token refresh; request signing (SigV4-style
schemes sign the host and cannot be proxied this way); response-body filtering;
hash-chained logs; a plugin system; per-agent identities; remote or team mode.
Each is v2+ and evidence-driven only.

**Compatibility rule, stated plainly:** anything where the client lets you set a base
URL plus a static auth header goes through custody - MCP servers with configurable
endpoints, curl-based skills, SDKs with a base-URL knob. Clients that do their own
OAuth dance or their own request signing keep their existing path.

## 2. Functional & non-functional requirements

Functional:

- FR1: forward an HTTP request to a configured upstream with the real credential
  injected, iff a valid grant covers a matching action rule.
- FR2: grants are operator-minted only, HMAC-signed, key-scoped, action-scoped, and
  TTL-bounded; expired / out-of-scope / tampered grants refuse before any forwarding.
- FR3: every request (pass or refuse) appends one JSONL artifact line sufficient to
  *explain* the recorded verdict offline - artifact/schema version, the rule that
  fired, a manifest+rule digest, a grant digest, and the canonical forwarded target.
  (Full independent replay - re-running the matcher on logged inputs and reproducing
  the verdict - is a v1 goal via `explain`; v0 explains, it does not promise to
  replay, because some matched query values are too sensitive to log verbatim.)
- FR4: refusals fail closed and print a remedy - the exact `custody grant` command a
  human types to unstick the work (gate's park-with-remedy pattern).
- FR5: secrets live in the OS credential store, never in the manifest, the log, the
  grant, or any response custody emits.
- FR6: a per-key prose `note` rides back to the caller on a grant's first use, so the
  calling skill can surface operator intent into agent context (advisory ceiling on
  top of the deterministic floor).

Non-functional:

| Dimension | Target |
|---|---|
| Latency | < 10 ms added per request on localhost (excl. upstream) |
| Availability | proxy down = calls fail closed with connection refused; nothing falls back to raw secrets |
| Security | secret bytes never appear in agent-visible output, logs, or error messages; listener binds 127.0.0.1 only |
| Operability | one binary, one state dir, `custody log` answers "what did agents touch" without tooling |
| Dependencies | Go standard library plus `golang.org/x/sys` (Credential Manager syscalls); no third-party deps, matching the monorepo bar |

## 3. Architecture overview

New tenant `cmd/custody` in the workbench monorepo, reusing the `contracts` grant
substrate the same way `cmd/gate` does.

```
agent ──► http://127.0.0.1:8127/<key>/<vendor-path>
             │  X-Custody-Grant: cst_...
             ▼
         custody serve
             1. resolve prefix /<key> → key manifest        (unknown key: 404 refuse)
             2. validate grant                               (HMAC, TTL, key match)
             3. match request against granted actions        (method + path + query rules)
             4. read secret from OS credential store
             5. inject per manifest template, forward upstream
             6. append JSONL artifact line
```

What is reused: the grant envelope shape (HMAC signing, TTL, refuse-before-evidence)
proven in gate. What is new: the proxy engine, the key manifest, the action-rule
matcher, the credential-store reader.

The seam: custody decides *reach* (which requests a grant may make). It does not
decide *placement* (dispatch), *merge* (gate), or *risk tier* (triage). It shares
their contract shape - `(action, observables, rulebook, grant) -> verdict + rule-fired
+ artifact` - not their call stacks.

## 4. Key decisions & trade-offs

**D1 - prefix-mapped reverse proxy, not a forward proxy.** A forward proxy
(`HTTP_PROXY`) cannot see paths inside TLS without interception certs; rejected in
one line. The prefix map means clients change exactly two things: base URL and auth
header value.

**D2 - grant envelope: copy the mechanism, version it, converge deliberately. (DECIDED.)**
Ground truth from the code: there is no shared signed-grant type today.
`contracts.Envelope` is the append-only JSONL *wrapper*; the real signed grant is
`capability.Grant`, internal to gate, HMAC over (repo, action, tier, cycles, expiry,
mintedBy), and its own comment notes the scheme carries no version and migrates by
"mint fresh." Two things are separable: the *mechanism* (HMAC-sign-a-struct, TTL
check, coded errors, loud-on-missing-key) which is identical across tools, and the
*scope body* which differs (gate: repo/tier/cycles; custody: key/action-set) and
always will. Decision: custody copies the mechanism into its own package now and
stamps every grant with a `version` and `domain` field from the first commit; gate is
not touched. Convergence - lifting the proven-identical mechanism into `contracts` and
re-pointing gate through it (gate's short TTLs mint-fresh across the change) - is a
deliberate later PR, taken when two real consumers have shaped the seam, not a
prerequisite that puts a refactor of a live, merge-guarding component in front of code
that does not exist yet. The reviewer's objection to plain copy ("two unversioned
contracts to reconcile") is answered by versioning custody's grant from day one:
convergence stays mechanical and non-breaking. Token prefix is visibly versioned
(`cst1_...`). Cost accepted: for the pre-convergence window, custody grants are
versioned and gate grants are not - a cosmetic inconsistency across two independent
keys signing two independent state dirs, not a functional one.

**D3 - the action set IS the ceiling.** No mapping of gate's T1/T2/T3 tiers onto
custody; forcing a shared tier vocabulary generalizes two systems that only share an
envelope. `read` vs `comment` vs `all` is all the tiering v0 carries.

**D4 - manifests are operator state, never repo content.** This repo is public. Key
manifests name real internal hosts and real project scopes; they live in the state
dir. The repo carries the schema, generic examples, and tests only.

**D5 - deterministic floor, prose ceiling.** Route rules are the floor: observables a
unit test can check (method, path glob, query regex). The manifest `note` is the
advisory ceiling: prose returned in a response header on a grant's first use, which
the calling skill surfaces into context. Agents demonstrably follow surfaced intent;
custody makes that channel cheap without ever relying on it.

**D6 - TTL policy tuned against re-mint fatigue.** Long-TTL grants are acceptable for
read actions (reads are scoped and logged anyway); write actions get short TTLs. The
failure mode this guards: nagging the operator into `-ttl 365d -actions all`, which
turns the whole system into theater. Cheap-to-stay-closed beats strict-and-bypassed.

**D7 - secret backend is Windows Credential Manager for v0**, read via
`golang.org/x/sys/windows` (CredRead), behind a two-method interface (`Get`, `Set`) so
a future keychain/secret-service backend is a file, not a refactor. No abstraction
beyond that - one implementation, one interface, per the no-generic-engine rule.

## 5. Data model

**Key manifest** (`<state>/manifest.json`, operator-owned, out of repo). Two seams are
locked additively now so a later feature is a new field, not a shape change: a
top-level `version`, and `inject` as a tagged list (`{kind, name, template}`) even
though v0 accepts exactly one header entry. Unknown fields are rejected at load. The
examples below show the loose (`hobbyvendor`) and tight (`tracker`) ends of the dial;
`upstream` must be HTTPS with no userinfo/query/fragment, and injected header names may
not be `Host`, a hop-by-hop/forwarding header, or `X-Custody-*`.

```jsonc
{
  "version": 1,
  "keys": {
    // high-stakes: a work issue tracker behind a personal access token,
    // where projects outside PROJ may hold export-controlled data
    "tracker": {
      "secret": "wincred:tracker-pat",
      "upstream": "https://issues.example.com",
      "inject": [ { "kind": "header", "name": "Authorization", "template": "Bearer {secret}" } ],
      "actions": {
        "read": {
          "rules": [
            { "methods": ["GET"], "path": "/rest/api/2/issue/PROJ-*" },
            { "methods": ["GET"], "path": "/rest/api/2/search",
              "query": { "jql": { "mustMatch": "project *= *PROJ" } } }
          ]
        },
        "comment": {
          "rules": [
            { "methods": ["POST"], "path": "/rest/api/2/issue/PROJ-*/comment" }
          ]
        }
      },
      "note": "Work tracker. PROJ only; other projects may hold export-controlled data. If a task needs another project, stop and ask the operator."
    },

    // low-stakes: the strictness dial at its loose end
    "hobbyvendor": {
      "secret": "wincred:hobbyvendor-key",
      "upstream": "https://api.hobbyvendor.example",
      "inject": [ { "kind": "header", "name": "Authorization", "template": "Bearer {secret}" } ],
      "actions": { "all": { "rules": [ { "methods": ["*"], "path": "/**" } ] } }
    }
  }
}
```

**Grant** (HMAC-signed token `cst_<id>.<sig>`, record persisted in `<state>/grants/`):
grant id, key name, action names, minted-at, TTL, minted-by (free-form,
unauthenticated - same custody caveat as gate: human-mint is a key-custody
precondition, not a property of the record).

**Artifact log** (`<state>/log/requests.jsonl`, append-only): `schema_version`, `ts`,
`request_id`, `key`, `grant_id` + `grant_digest`, `manifest_digest` + `rule_fired`
(action name + rule index; the digest pins *which* manifest revision decided, since a
later edit would otherwise re-point the index), `verdict` (`pass` | `refused` |
`denied` | `upstream_error`), `method`, `canonical_target` + `raw_target_hash`,
`query_keys`, `upstream_status`, `latency_ms`. Enough to *explain* the verdict offline
(FR3); never bodies, never header values, never secrets. The exact final field set is
settled while writing the logger (§8.2). Plain JSONL, not hash-chained: a read log is
tuning evidence, not a merge-authority record; revisit if custody grants ever gate an
effectful verb chain.

**State dir**: `%USERPROFILE%\.custody\` - `manifest.json`, `mint.key` (HMAC key),
`grants/`, `log/`. Same custody caveat as gate state: the mint key staying outside
governed sessions' reach is what makes "operator-minted" mean anything.

## 6. API contract

**HTTP surface** (localhost only):

```
ANY /<key>/<vendor-path>[?query]     header: X-Custody-Grant: cst_...
```

- Pass: upstream response streamed back verbatim; custody adds
  `X-Custody-Request-Id` always and `X-Custody-Note` on a grant's first use.
- Refuse/deny: JSON body `{ "code", "reason", "remedy", "request_id" }` with
  - `401 refused_no_grant` / `refused_expired` / `refused_bad_signature` /
    `refused_wrong_key` - the *request* is unauthorized (grant layer),
  - `403 denied_no_action_match` - judged and denied (rule layer),
  - `404 unknown_key` - no manifest prefix,
  - `502 upstream_unreachable`, `500 secret_unavailable` (secret missing from the
    credential store; remedy: `custody keys set -name <ref>`).
- Every remedy names the exact command, e.g.
  `remedy: custody grant -key tracker -actions comment -ttl 1h`.

**CLI verbs**:

```
custody serve [-addr 127.0.0.1:8127] [-state <dir>]
custody keys set -name <ref>            # secret read from stdin, written to credential store
custody grant -key K -actions a,b -ttl 8h   →  cst_...
custody log [-key K] [-since 24h]
custody explain -req req_...            # v1: replay one decision from the log
custody keys import -from <file>        # v1: drain a plaintext keys file, then delete it
```

Grant handoff to agents: the minted token travels via env var (e.g.
`CUSTODY_GRANT_TRACKER`) or settings. Losing one leaks a scoped, expiring capability
instead of an identity.

## 7. Key flows

**A - happy path.** Agent GETs `/tracker/rest/api/2/issue/PROJ-123` with a valid
`read` grant → prefix resolves → HMAC + TTL pass → rule `read[0]` matches → CredRead
→ `Authorization: Bearer <secret>` injected → forwarded → 200 streamed back → JSONL
line with `verdict: pass, rule_fired: read[0]`.

**B - denial with remedy.** Same grant, POST to `.../comment` → grant valid but no
granted action matches POST → `403 denied_no_action_match`, remedy names the mint
command, JSONL `verdict: denied`. The agent surfaces the remedy to the operator;
nothing falls back to a raw secret.

**C - canonical target identity (the load-bearing invariant).** The one rule the
whole authorization rests on: **the exact semantic target that was matched is the
target that is sent.** Matching a normalized path while forwarding the original bytes
is the core hole (a full-width or `%2F`/`%252F`-encoded separator can match one
segment yet resolve elsewhere once an upstream re-normalizes). So:

- accept origin-form request targets only; refuse absolute-form, authority-form,
  `CONNECT`, `*`, fragments, and malformed targets - a request line may never
  influence the outbound host;
- the outbound URL is built exclusively from the manifest's scheme+authority plus the
  canonical decoded path (Go: set `URL.Path`, clear `RawPath`, let the stdlib
  re-escape; parse and re-encode the query, never restore the original `RawQuery`);
- decode once, remove dot segments, then anchored whole-path match; reject if decoding
  reveals another escape, or reveals an encoded `/` or `\`, controls/NUL, or - for v0,
  conservatively - non-ASCII / compatibility-normalizing characters;
- segment globs range over a constrained character alphabet, not arbitrary
  reserved/routing characters, so `PROJ-*` does not also admit `;` or vendor path
  syntax.

`/rest/api/2/issue/PROJ-*` therefore matches neither `.../PROJ-1/../OTHER-9` nor its
encoded or full-width spellings; ambiguous input refuses rather than best-effort
matches. The *invariant* is fixed here; the exhaustive list of rejected encoding
classes is settled in implementation, against a table-driven adversarial test suite -
not enumerated further in prose.

**D - query rule, and the v0 scope line it forces.** A `mustMatch` regex cannot
enforce result-set containment on an embedded query language: `jql=project = PROJ OR
project = SECRET` satisfies a `project *= *PROJ` substring check yet returns a
forbidden project, and repeated `?jql=...&jql=...` params create a matcher/upstream
differential. So the v0 rule is: **endpoints carrying an embedded query language
(search/JQL and kin) are deny-by-default** - a real parser is required before they
open, and that is v2 work gated on evidence. Regex rules remain valid only for scalar
query parameters, and only with full-value (anchored) semantics on a parameter
constrained to occur exactly once. This narrows what v0 promises rather than pretending
a regex closes the hole.

**E - grant expiry mid-session.** TTL lapses between two calls → second call returns
`401 refused_expired` + remedy → operator re-mints → work resumes. No grace period:
fail closed, make closed cheap.

**F - secret missing.** Manifest references `wincred:tracker-pat` but the store has
no such entry → `500 secret_unavailable` + remedy → nothing forwarded, nothing logged
beyond the refusal line.

## 8. Concurrency / consistency / failure model

Single process, single box. Grant validation is stateless per request. The JSONL log
is append-only behind a mutex; one writer. Upstream failures pass through as-is (no
retries - retry policy belongs to the caller, custody only records the outcome).
Proxy not running = connection refused = fail closed; no client falls back to reading
a secret directly, because after v1 the plaintext file no longer exists.

### 8.1 Threat model honesty

What custody does **not** claim, stated the way gate's enforcement doc states its
boundary:

- **Single-box custody is not cryptographic.** The OS credential store is readable by
  any process in the operator's user session; a determined local agent could dig the
  secret out. What custody actually buys: the secret is out of plaintext files and
  out of transcripts, every custody-mediated request is associated with a bearer grant
  id and logged, and overreach becomes a deliberate, loud act of going around a
  guardrail instead of an accidental step past one. Note the precise claim: there is
  no *actor* attribution (grants are bearer tokens - any holder is any other holder),
  and credential-store use that bypasses custody is neither visible nor logged.
  Discipline plus an audit trail, until custody of the mint key and the credential
  store hardens. The "deliberate-and-loud" property holds only once the canonical-target
  (§7 C) and query-language (§7 D) rules are in place - before them, an ordinary
  request can log as a normal pass while exceeding intended scope.
- **A grant is replayable by any holder until it expires.** It is a bearer capability,
  not a per-request or per-actor authorization; any process holding the token makes any
  request its action set covers, for the token's whole TTL. This is why write actions
  get short TTLs (§4 D6).
- **Path and query rules cannot see response bodies**, so FR5's "no secret in any
  response" is scoped to responses and errors *custody itself* generates - upstream
  bodies are streamed back verbatim and unfiltered by design. A scoped GET can still
  return text the operator would rather agents not read if the rule is too loose. For
  regulated data the actual control remains the execution boundary (local-only,
  approved environment); custody narrows reach and produces the audit trail that
  proves what was reached.
- **Mint authority is key custody, not authentication.** Anyone who can run
  `custody grant` with the mint key can mint. Same precondition as gate: the mint key
  and the grant verb stay outside governed sessions' reach.

### 8.2 Settled in implementation, not prose

This design fixes the load-bearing invariants (§7 C canonical identity, §7 D
deny-by-default, §4 D2 grant shape) and deliberately leaves the following to be worked
out in code and tests rather than pre-specified here: the exact enumeration of rejected
encoding classes (§7 C), the final artifact field list beyond the digests FR3 names,
the manifest injection-seam details (tagged `inject` list, HTTPS-only upstream, header
denylist, single-placeholder no-CRLF templates - all additive, so a wrong first guess
is cheap to correct), and the guard's command-normalization depth (§ hooks). Each is a
place where trying it is faster and more honest than theorizing it.

## 9. Rollout / implementation plan

| Phase | Goal | High-level tasks | Depends on | Gate |
|---|---|---|---|---|
| 1. `custody-v0` | one real key end-to-end through the proxy | grant envelope (mint/validate); manifest loader + credential store (`keys set`); proxy engine (resolve, validate, match, inject, forward, log, remedies); wire first key + generic runbook | - | **VALIDATION GATE** below |
| 2. `custody-drain` | the plaintext keys file stops existing | `keys import -from` migration verb; move every remaining key behind custody; PreToolUse guard rule refusing reads of keys files / credential stores; `explain` verb | phase 1 + gate | - |
| 3. `custody-evidence` | expressiveness earned by evidence | query-rule upgrades driven by `rule_fired` stats; response-side filtering if a real leak class shows up; per-agent grant identities if attribution needs it | phase 2 | each item needs a logged incident or a weekly-review finding first |

Rough scope: phase 1 is four PR-sized tasks (the proxy engine is the largest);
phase 2 is two to three; phase 3 is deliberately unsized - it may never run.

**VALIDATION GATE (after phase 1):** one real high-stakes key wired through custody
for one week of normal agent use, with (a) zero raw-secret occurrences in session
transcripts over the week, (b) that key's entry deleted from the plaintext file, and
(c) `custody log` answering "what did agents touch this week" in one command.
Phases after the gate are not committed until it passes.

## 10. Open questions

1. **D2** - lift a domain-scoped grant envelope into `contracts` now, or copy the
   shape and converge later? (Reviewer input wanted; leaning copy-then-converge.)
2. Grant delivery ergonomics - one env var per key works today; does a session-start
   hook that mints tier-appropriate read grants automatically cross the line into
   self-minting, or is it fine because the hook runs under operator config?
3. Log rotation / retention - JSONL grows unbounded; probably a `-since`-aware
   compaction verb later, not v0.
4. Port and prefix collisions - fixed default port with a `-addr` flag is assumed
   fine for a single operator; anything more is speculative.

## 11. Validation plan

The gate in §9 is the plan, and its signal is binary and baseline-free: grep the
week's transcripts for the secret (zero hits), confirm the plaintext entry is gone,
and confirm `custody explain` reconstructs each recorded verdict from the log +
manifest/grant digests (the v0 promise is explain, not independent replay - see FR3).
