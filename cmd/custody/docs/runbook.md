# custody runbook — wire a key end to end

custody is a localhost credential broker. It holds a real vendor secret in the
OS credential store, forwards a narrowly-scoped set of requests to one upstream,
and injects the secret on the way out — so an agent can call an API it is never
handed the credential for. Every request is a **pass** (credential injected,
forwarded, logged) or a **fail-closed** refusal/denial that names the exact
command to unstick it.

This runbook wires one key from nothing to a working proxied call. The examples
are generic on purpose — swap `tracker`, `https://api.example.com`, and `PROJ-1`
for your own key name, upstream, and paths.

## The chain

```
keys set  →  manifest entry  →  serve  →  mint a grant  →  client call
 (secret)     (what's allowed)  (proxy)   (time-boxed cap)  (via the proxy)
```

## Trust domains (read once)

custody keeps two directories in **separate trust domains**, both OUTSIDE this
repo:

- **state dir** (`-state`, env `CUSTODY_STATE`, default `~/.custody`) — the
  manifest, grant records, and the request log.
- **mint-key dir** (`-mint-key-dir`, env `CUSTODY_KEY_DIR`, default
  `~/.custody-key`) — the HMAC mint key that signs grants.

The key dir must sit OUTSIDE the state dir; custody refuses to start if it is
equal to or nested under `-state`. Co-locating them would let anything that can
read state forge broader grants. Pick two sibling directories and keep them
consistent across every command below.

## 1. Store the secret

The secret is read from stdin and written to the OS credential store under a
reference. Nothing is echoed or logged. Pipe it in rather than typing it as an
argument (arguments land in shell history):

```sh
printf %s "$TRACKER_TOKEN" | custody keys set -name tracker-pat
```

`tracker-pat` is the reference; the manifest points at it as
`wincred:tracker-pat`. Re-run `keys set` any time the secret rotates — nothing
else changes.

## 2. Add a manifest key entry

The manifest lives at `<state>/manifest.json` and is hand-edited and operator
owned. It declares, per key: the secret reference, the single `https` upstream,
how the secret is injected, and the named **actions** — each a set of allowed
request shapes (methods + an anchored path glob + optional exact query
predicates). The action set is the ceiling a grant can scope to; nothing outside
it is ever forwarded. custody validates the whole manifest at startup and fails
closed on anything malformed.

```json
{
  "version": 1,
  "keys": {
    "tracker": {
      "secret": "wincred:tracker-pat",
      "upstream": "https://api.example.com",
      "inject": [
        { "kind": "header", "name": "Authorization", "template": "Bearer {secret}" }
      ],
      "actions": {
        "read": {
          "rules": [
            { "methods": ["GET"], "path": "/rest/api/2/issue/PROJ-*" }
          ]
        },
        "comment": {
          "rules": [
            { "methods": ["POST"], "path": "/rest/api/2/issue/PROJ-*/comment" }
          ]
        }
      },
      "note": "Example tracker key. PROJ-* only."
    }
  }
}
```

Notes on the shape:

- `secret` must be `wincred:<ref>` — the reference you stored in step 1.
- `upstream` must be `https`, with no userinfo, query, or fragment.
- `inject` is a list; v0 accepts exactly one `header` entry, and `{secret}` in
  the template is replaced with the stored secret at forward time.
- `path` globs are anchored; `*` matches within a path segment. Add a `query`
  map of exact-match predicates (`{"state": {"equals": "released", "occurs": "once"}}`)
  when a rule must pin a query parameter. Unlisted query params are denied by
  default.

## 3. Start the proxy

`serve` loads and validates the manifest (failing closed at startup), binds a
**loopback-only** listener, and serves the engine. A non-loopback `-addr` is
refused — the proxy holds real credentials and must never be reachable off the
box.

```sh
custody serve -addr 127.0.0.1:8127 \
  -state ~/.custody \
  -mint-key-dir ~/.custody-key
```

Leave it running. It logs one JSONL artifact line per request to
`<state>/log/requests.jsonl`.

## 4. Mint a scoped grant

A grant is an HMAC-signed, key-scoped, action-scoped, TTL-bounded capability. It
is the caller's bearer proof. Minting is a human act — it signs with the mint
key.

**First run only:** the mint-key dir is empty, so pass `-init` to bootstrap a
fresh mint key there. Without `-init`, an absent key is treated as a misdirected
`-mint-key-dir` and the mint is refused (`mint_key_missing`) — this guard is what
stops you from silently signing grants with an orphan key that `serve` later
rejects as a bad signature.

```sh
GRANT=$(custody grant \
  -state ~/.custody \
  -mint-key-dir ~/.custody-key \
  -key tracker \
  -actions read \
  -ttl 8h \
  -init)
```

**Every run after the first:** the key already exists — drop `-init`.

```sh
GRANT=$(custody grant \
  -state ~/.custody \
  -mint-key-dir ~/.custody-key \
  -key tracker \
  -actions read,comment \
  -ttl 8h)
```

`-actions` is a comma-separated subset of the key's manifest actions. Keep TTLs
short and re-mint; there is no revoke in v0, so a short lifetime is the bound.
The command prints the grant token to stdout — capture it (as above) and hand it
to the caller.

## 5. Point a client at the proxy

The caller talks to `127.0.0.1:<port>/<key>/<path>` and passes the grant in the
`X-Custody-Grant` header. custody resolves the `/<key>` prefix, validates the
grant, matches the action, injects the secret, and forwards the rest of the path
to the manifest upstream.

```sh
curl -H "X-Custody-Grant: $GRANT" \
  http://127.0.0.1:8127/tracker/rest/api/2/issue/PROJ-1
```

That request maps to `GET https://api.example.com/rest/api/2/issue/PROJ-1` with
the `Authorization: Bearer <secret>` header injected. The client never sees the
secret. Every response carries `X-Custody-Request-Id` for correlation with the
log.

## What a refusal looks like

Every fail-closed response is a JSON body `{code, reason, remedy, request_id}` —
the `remedy` names the exact command to unstick the work. Three you will meet:

- **403 `denied_no_action_match`** — the grant is valid but does not cover this
  request shape (e.g. a `POST .../comment` under a `read`-only grant). The remedy
  names the grant command for the action that would cover it, e.g.
  `... grant -key tracker -actions comment -ttl 1h`.
- **401 `refused_expired`** — the grant's TTL elapsed. Mint a fresh one (step 4,
  without `-init`).
- **401 `refused_no_grant`** — no `X-Custody-Grant` header, or a token that names
  no grant here.

Nothing is forwarded upstream on any refusal, and no secret or grant token
appears in the log.

## Flag reference

| Flag | Verbs | Meaning |
| --- | --- | --- |
| `-state` | `serve`, `grant` | state dir: manifest, grants, log (env `CUSTODY_STATE`) |
| `-mint-key-dir` | `serve`, `grant` | mint-key dir; a separate trust domain, must be outside `-state` (env `CUSTODY_KEY_DIR`) |
| `-init` | `grant` | first-run only: create a fresh mint key if none exists yet |
| `-key` | `grant` | manifest key name the grant is scoped to |
| `-actions` | `grant` | comma-separated subset of the key's actions (e.g. `read,comment`) |
| `-ttl` | `grant` | grant lifetime (e.g. `8h`) |
| `-addr` | `serve` | loopback listen address (default `127.0.0.1:8127`) |
| `-name` | `keys set` | secret reference to store (the ref after `wincred:`) |
