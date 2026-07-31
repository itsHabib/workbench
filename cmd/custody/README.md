# custody

A localhost credential broker. It holds a real vendor secret in the OS
credential store and forwards a narrowly-scoped set of requests to one upstream,
injecting the secret on the way out — so an agent can call an API it is never
handed the credential for. An operator-owned `manifest.json` declares, per key,
the secret reference, one `https` upstream, the injection template, and the
named *actions* — method + anchored path glob + optional exact-match query
predicates — that bound reach; a caller presents an HMAC-signed, key-scoped,
action-scoped, TTL-bounded grant. Every request is a pass (injected, forwarded,
logged) or a fail-closed refusal whose JSON body names the command to unstick it.

custody is a workbench tenant: one binary at `cmd/custody`, guts private under
`cmd/custody/internal/` (`grant`, `manifest`, `match`, `credstore`, `serve`,
`rollup`). Its load-bearing seam is the HTTP surface —
`http://127.0.0.1:8127/<key>/<vendor-path>` with an `X-Custody-Grant` header —
plus one JSONL artifact line per request at `<state>/log/requests.jsonl`.

## Run it

```
go build -o custody.exe ./cmd/custody
export CUSTODY_STATE=~/.custody CUSTODY_KEY_DIR=~/.custody-key  # -state/-mint-key-dir defaults
printf %s "<token>" | ./custody.exe keys set -name tracker-pat  # secret read from stdin, never argv
./custody.exe serve -addr 127.0.0.1:8127                        # loopback-only proxy; non-loopback -addr refused
./custody.exe grant -key tracker -actions read -ttl 8h -init    # first mint into a fresh key dir needs -init
./custody.exe grant -key tracker -actions read,comment -ttl 8h  # → cst2_<id>.<sig> on stdout
./custody.exe derive -grant cst2_... -actions read -ttl 2h      # child grant; may only narrow
./custody.exe derive -grant cst2_... -actions read -ttl 2h -bound-source 10.0.0.2
./custody.exe log rollup -since 24h [-json]                     # per-key verdict/latency summary
./custody.exe serve -tap-addr 10.0.0.1:8127 -tap-if-prefix tap  # second listener (Linux only, see Status)
```

`-state` and `-mint-key-dir` default to `$CUSTODY_STATE` / `$CUSTODY_KEY_DIR`.
custody refuses a key dir equal to or nested under the state dir: the mint key
is a separate trust domain from the grants and logs it signs.

### Exit codes

| Code | Meaning |
| ---: | --- |
| 0 | verb succeeded |
| 1 | verb failed (mint refused, bad manifest, store error, serve stopped) |
| 2 | usage: no verb given, or an unknown verb (`-h` prints usage and exits 0) |

The proxy's real contract is HTTP, not process exit: `401` for the grant layer
(`refused_no_grant` / `refused_expired` / `refused_wrong_key` /
`refused_bad_signature` / `refused_chain_depth`), `403 denied_no_action_match`
for the rule layer, `404 unknown_key`, `500 secret_unavailable`,
`502 upstream_unreachable`, `400 refused_bad_target`. Every response — pass or
refusal — carries `X-Custody-Request-Id`.

## How it works

One request, in order: canonicalize the whole origin-form target (ambiguous
encodings refuse rather than best-effort match); resolve the `/<key>` prefix;
validate the grant (signature, then chain depth, then key scope, then TTL);
match method + path glob + query predicates against the granted actions; read
the secret; build the outbound URL from the manifest authority plus the
canonical decoded path only, inject per the manifest template, forward with
redirects disabled; append one JSONL receipt. The receipt records
`schema_version`, verdict, key, grant id and a SHA-256 grant digest, manifest
digest, `rule_fired`, `matched_query`, canonical target and a raw-target hash,
upstream status and latency — never a secret, a bearer token, a header value, or
a body. The manifest is validated once at startup, so a malformed rule fails
`serve`, never a request.

## Constraints that are design decisions, not omissions

- **Same-user process identity is not a boundary.** The OS credential store is
  readable by any process in the operator's session, and the listener
  authenticates a bearer grant, not a caller. Anything running as the operator
  can read the secret directly or replay a grant it sees; custody buys
  secret-out-of-transcript, scoped reach, and a trail — not isolation.
- **A grant is a bearer capability with no revoke.** Any holder is any other
  holder for the whole TTL. Short TTLs are the only bound — nothing rejects a
  long-TTL write grant.
- **The audit log is not tamper-evident.** `requests.jsonl` is plain JSONL in a
  user-writable state dir, deliberately not hash-chained. A log-write failure
  after startup surfaces on stderr and does not fail the request.
- **Response bodies are streamed verbatim.** custody bounds *reach*, never
  content: a too-loose rule still returns whatever the upstream sends, and an
  upstream that echoes the injected header back is a body custody cannot scrub.
  `TRACE`/`CONNECT` are denied and redirects never followed, closing the
  reachable cases of that class.
- **Deny-by-default on anything unlisted**, query parameters included, with one
  per-rule `allowExtraParams` escape hatch. Substring/regex predicates
  (`mustMatch`) are rejected at manifest load, so endpoints carrying an embedded
  query language stay closed rather than looking bounded.
- **Windows Credential Manager is the only backend.** On any other OS the store
  is a fail-closed stub — nothing proxies without a credential.
- **Mint authority is key custody, not authentication.** `-minted-by` is a
  free-form, unauthenticated label; whoever can read the mint key can mint.

## Status

The proxy path is built and tested here — grant mint/derive/validate, manifest
load, canonicalization and matching, injection and forwarding, the log and the
rollup all carry unit, property, and fuzz tests plus an end-to-end smoke test
against a process-local `httptest` upstream. The feature spec
(`docs/features/custody/spec.md`) is still marked *draft / proposal*, and its
phase-1 validation gate (one high-stakes key for a week) is not recorded as
passed in this repo. What is claimed: a single-operator run against a real
third-party API in which reads passed and writes and unlisted API versions were
refused before forwarding. The `-tap-addr` second listener (source-bound grants
for a remote guest) is Linux-only, firewall-preflighted, and draft in
`docs/features/grant-materialized-rooms/custody-tap-listener.md` — `bound_source`
is signed into every grant but enforced only there. The spec's `explain` and
`keys import` verbs are not built.

Wire a first key end to end — secret, manifest entry, serve, grant, client call
— with [`docs/runbook.md`](docs/runbook.md).
