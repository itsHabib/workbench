# console e2e (Playwright)

Browser coverage for `cmd/console`, driving the **real** `console` binary against
committed, deterministic gate-state fixtures. This pins the class of regression
the console has actually shipped: a full-red **TAMPERED** banner that was pure
misconfiguration (audit couldn't resolve the anchor), and a stale `gate` binary
that made the docket error out.

Node/Playwright is **test-only and confined to this directory** — its own
`package.json`, never part of the Go module or the production build. `go build
./...`, `go vet ./...`, and `go test ./...` do not see it (there are no Go files
here).

## Run

```
cd cmd/console/e2e
npm install
npx playwright install chromium
npx playwright test
```

`globalSetup` builds `gate` + `console` from source into `.bin/` (gitignored)
before any spec, so a stale binary can never be silently reused.

## What it asserts

| spec | guards |
| --- | --- |
| `docket.spec.ts` | the parked run renders (repo#number, title, question); judge/explain commands **carry `-state`** (fails if a rendered command omits it) |
| `audit-intact.spec.ts` | the good chain shows **chain intact**, no tamper banner |
| `audit-tampered.spec.ts` | a mutated chain flips to **CHAIN TAMPERED** + banner — fails loudly if the console ever renders "intact" over a broken chain |
| `empty.spec.ts` | an inbox with nothing parked renders a clean empty state, not an error |
| `security.spec.ts` | strict CSP + nosniff on served responses, a spoofed `Host` is refused (403), a non-loopback bind is refused at startup |

## Fixtures

Committed under `fixtures/{good,tampered,empty}/state/log.jsonl`. Each is a
genuine gate hash-chained log — every line's `hash` is computed exactly as
`state.hashArtifact` does, so the real `gate audit` replays it.

- **good** — one grant + one parked run (evidence → reduced verdict → escalation)
  for `example/console-e2e#42`.
- **tampered** — the good log with one escalation-body byte mutated and its hash
  left stale (a body-hash mismatch on replay).
- **empty** — a lone grant, no parked run.

### Why the fixtures ship *without* an anchor

Gate's anchor record is keyed by an HMAC secret held outside the state dir, and
its filename embeds a hash of the state dir's **absolute path**. Both are only
knowable at run time. So the harness (`helpers/harness.ts`) copies a fixture into
a fresh temp dir and **binds the anchor there** by minting one grant through the
real `gate` binary — an append rebinds the anchor over the whole prior log at
that dir's real path. The committed fixture is therefore path-independent: it
audits intact wherever the repo is checked out. The tampered fixture is anchored
the same way, so its broken chain is caught by replay, not by a missing anchor.

## Regenerating the fixtures

The log chains are produced by a pure-Node generator (no gate import) that
mirrors `state.hashArtifact` byte-for-byte:

```
npm run gen-fixtures
```

Edit `scripts/gen-fixtures.mjs` to change subjects, questions, or the tamper
mutation. If you change gate's `hashArtifact` or artifact serialization, re-run
this and the anchoring step will keep the fixtures audit-clean.

## Not covered here

- No CI wiring (a follow-up adds the e2e job); this suite lands runnable locally.
- No judge/mint actions — the console is read-only in this version.
- No production changes to `cmd/console`; this is coverage, not a feature.
