# console — design

The operator console: a local web surface for the things that need a human in
the gate merge-authorization loop. This version is **read-only** — one page that
answers "what needs me?" (runs parked for judgment, the grant ledger) and lets
you drill into any run's decision trace. Judging and minting stay in the CLI.

It is the UI companion to gate's `next` verb: `gate next -json` is the feed,
the console is one renderer of it.

## The boundary — the console renders, gate decides

The single architectural rule: **the console is a pure renderer over gate's own
JSON output.** It shells the `gate` binary and displays what comes back. It does
not import `cmd/gate`, does not read `log.jsonl`, does not reimplement any
projection or check.

```
browser ──HTTP──> console ──exec──> gate ──reads──> state (log.jsonl + anchor)
         (renders)         (proxies JSON)  (owns the projection)
```

Why shell instead of import or parse:

- **No schema drift.** The console forwards `gate next -json` / `gate explain
  -json` bytes largely untouched. It cannot fall out of sync with a projection
  it never parses. When gate's schema changes, the console renders the new
  fields or ignores them — it never contradicts gate.
- **The workbench boundary law.** Tools compose through artifacts (exit codes +
  JSON), never by importing one another's decision code. CI's `hygiene` job
  fails the build on a cross-tenant import; `go list -deps ./cmd/console/...`
  carries nothing from `cmd/gate`.
- **gate stays the single owner of truth.** The hash chain, the anchor, the
  verdict/ladder law, "what is parked", "is a grant live" — all live in gate.
  The console has no opinion of its own to drift.

`internal/gatecli` is the whole seam: `Next`, `Explain(run)`, `Audit`, each a
thin exec of the gate binary with an injectable `Runner` for tests. A run id is
validated (`run_[0-9a-f]+`) before it is ever forwarded.

## Surfaces (one page, no tab chrome)

The UI is deliberately one plain page — not a multi-tab console. It has two
views, switched client-side:

- **docket** (`/`) — what needs you: one current parked run per open PR, with a
  direct PR link, title, question verbatim, and a paste-ready `gate judge` /
  `gate explain` command (copy button attached). `gate next -json -live` removes
  confirmed merged/closed subjects; failed live lookups stay visible as unknown,
  and truly unattributed legacy rows sit in a secondary diagnostic section. The
  page also shows the grant ledger (live soonest-expiry first, then recently
  expired). A quiet `chain intact` / `CHAIN TAMPERED` line sits in the
  masthead; a tamper finding raises a full-width banner.
- **run trace** (`/run/<id>`) — the run's full causal chain rendered in gate's
  trace-view idiom (timeline rail, kind-shaped markers, provenance arcs,
  pass/block/escalate badges), fed by `gate explain -json`. A "← what needs you"
  link returns to the docket.

## Security posture

The console can shell gate, which holds signing keys — so the surface is kept
tight:

- **Loopback only.** `serve` refuses any bind that is not localhost/loopback —
  an unauthenticated console must never be reachable off the machine.
- **Host-header pinning.** Every request's `Host` must match the serve address
  (or a loopback alias on the same port), defeating DNS-rebinding — a remote
  page that re-resolves its name to 127.0.0.1 arrives with a foreign `Host` and
  is refused.
- **Strict CSP + nosniff.** The one page is self-contained and inlined;
  `default-src 'self'` forbids any external fetch, so the page can neither pull
  a dependency nor exfiltrate.
- **No mutating routes.** GET only. There is nothing to CSRF-protect yet because
  nothing changes state; when action endpoints arrive they bring their own
  CSRF + confirm plane.

## Deliberately out of this version

- **Action endpoints.** No judgment form, no mint desk. The console shows the
  command; the operator runs it in the CLI. Wiring `gate judge` (then `gate
  grant`) behind a click is the next phase, and it carries real weight — a
  mint is a signed capability, a judgment is effectful — so it lands with its
  own CSRF/confirm plane and its own review, not as an afterthought here.
- **Authentication / multi-user.** It is a single-operator, on-machine tool.
- **Any state of its own.** The console persists nothing. Every byte on screen
  is a projection gate produced this second.

## Phased plan

1. **This version — read-only.** docket + run trace, copy-paste commands. ✅
2. **Judge.** A judgment form on the run view that shells `gate judge`; CSRF +
   command-echo consent + confirm.
3. **Mint.** A grant form that shells `gate grant`; two-step confirm; the
   "human act" property stays visible (no agent-reachable endpoint).
4. **Record / driver runs.** Broader ledger browsing and a read-only driver lane
   — only as friction justifies.
