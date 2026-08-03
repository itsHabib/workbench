# escalate

One Go binary that ingests a human's decision for a **parked** gate escalation
and drives `gate resolve` to record it. It takes that decision from either of
two transports — CLI flags (`escalate resolve`) or a signed Slack
interactive-action callback (`escalate serve`) — validates it into one
`ingest.Decision` (`{escalation, verdict, who, why, grant}`), and shells `gate`,
passing gate's own JSON outcome and exit code back. escalate is the **inbound**
arrow of the escalation seam (human → system); `flare` is the **outbound** arrow
and read-only per Amendment 3 — exactly why this could not be a flare change.

Tenant layout: `cmd/escalate/main.go` is the binary; its guts stay private under
`cmd/escalate/internal/` (`ingest` — validate + shell gate; `serve` — the HTTP
transport adapter), plus an offline harness in `cmd/escalate/e2e/`. Its one
in-repo dependency is the leaf `contracts/escalation`. The load-bearing seam:
**it shells the gate binary and never imports gate** — only gate may write
gate's log, so the *effect* lives in gate and escalate owns the *ingest*.

## Run it

```
go build -o escalate.exe ./cmd/escalate
# CLI transport — a human's decision as flags
./escalate.exe resolve -escalation esc_… -decision pass -grant grt_… \
  -who "operator" -why "reviewed the diff" -state ~/dev/gate/state

# HTTP transport — Slack interactive-action ingress; both env vars are REQUIRED
SLACK_SIGNING_SECRET=<app signing secret> \
ESCALATE_ALLOWED_SLACK_USERS=<Uxxxxxxxx>[,<Uxxxxxxxx>] \
  ./escalate.exe serve -addr 127.0.0.1:8099 -gate gate -state ~/dev/gate/state
```

`-gate` defaults to `gate` on PATH and `-state` to `$GATE_STATE`. Both verbs
shell `gate resolve` with no `-key`, so the process needs `GATE_KEY` (or gate's
default key dir), or gate refuses the grant with `grant_bad_signature`.

## Exit codes — a faithful pass-through of `gate resolve`'s contract

| Code | Meaning |
| ---: | --- |
| 0 | pass — gate authorized the merge |
| 1 | blocked |
| 2 | parked |
| 3 | refused |
| 4 | gate error |
| 5 | **escalate-side** ingest failure — bad decision, gate not runnable |

5 sits outside gate's 0–4 space so a caller can tell it apart from a decision.

## How it works

`resolve` validates before it spawns anything: the escalation id must match
`^esc_[0-9a-f]+$`, the verdict must be `pass` or `block` (vocabulary from
`contracts/escalation`, not a re-spelled literal), grant/who/why must be
non-empty. It then runs `gate resolve …`, re-emits gate's stdout, and streams
gate's stderr through so a hard error shows gate's own diagnostic.

`serve` puts a transport in front of that same `ingest.Client`. Per callback,
synchronously: read the raw body (capped at 1 MiB) → verify the Slack signature
(`v0=HMAC-SHA256(secret, "v0:{ts}:{body}")`, constant-time, ±5-minute window) →
parse the payload → authorize the *verified* Slack user id against the allowlist
(401 unsigned/stale, 400 malformed or unknown `action_id`, 403 unlisted). Only
then does it ack 200 inside Slack's ~3s window, replacing the card with a
working state that drops the buttons. The rest runs in a background goroutine
under a per-escalation lock: read the grant from the parked escalation via `gate
next -json`, drive the same `Resolve`, POST the outcome to `response_url`.

## Constraints that are design decisions, not omissions

- **No unauthenticated ingress.** `serve` refuses to start without
  `SLACK_SIGNING_SECRET`, and a `serve.Server` with no `Authorize` denies
  everyone. Authentication is not authorization: an authentic but unlisted
  channel member is refused before any lookup or resolve.
- **`who` is never client-settable** — derived from the verified Slack identity
  (`@handle (Uxxxx)`); a smuggled `who` field is ignored. The grant likewise
  comes from the parked escalation, not the button payload, so an approval
  cannot outrun the delegation (gate re-checks it is live at resolve time).
- **It cannot merge, judge, or write gate's log.** The blast radius is exactly
  "approve/block a PR gate already parked for judgment"; outbound POSTs are
  host-guarded to https on a `slack.com` host.
- **Loopback by default**; a tunnel (ngrok / cloudflared) is what exposes it to
  Slack. Honest limits: the process-local lock does **not** serialize a second
  `serve` on the same `-state`, nor a CLI `resolve` racing a callback; and a
  hard crash after the ack can lose a decision Slack will not retry. Both are in
  `FOLLOWUPS.md`; gate's `escalationIsOpen` guard is the double-apply backstop.

## Status

Both transports are proven through the real `gate` and `escalate` binaries
(`EVIDENCE-escalation-plane-poc.md`, `EVIDENCE-escalate-serve-phase1.md`) — in
both cases against a park seeded through gate's own `act()` path, since a live
`gate gate` run needs a GitHub PR unavailable offline. `cmd/escalate/e2e` drives
the real `serve` binary over loopback with signed callbacks and a recording stub
gate: approve, block, forged, stale, unsigned, unauthorized, replay, concurrent
double-tap, forged-`who`, unknown-action. **Not yet done:** the real phone tap
over a live tunnel against the real gate — only one-time Slack-app + tunnel
operator setup stands in the way. Per the spec this ships as **a contract plus a
seam, not a sixth plane**.

Read next, both under `docs/features/escalation-plane/`: `spec.md` (the charter
— the seam, the contract, the not-a-plane decision) and `escalate-serve.md` (the
serve runbook — security model, phasing, Slack-app + tunnel checklist).
