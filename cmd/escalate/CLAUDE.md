# escalate

The resolution back-channel of the **Escalation plane**: a small Go binary that
ingests the decision a notification carried back for a parked escalation and
drives gate's `resolve` verb to record it — closing the agent→human→agent loop
WITHOUT flare (the router) ever writing a decision.

It is the missing seam the plane formalizes. gate emits the park (push origin),
flare routes it (Observability, read-only per Amendment 3), and this component
owns the **resolution ingest** that no plane owned before — the human's decision
used to return only out-of-band via a hand-run `gate judge`.

Read `docs/features/escalation-plane/spec.md` first — it is the plane argument
and the contract; this binary is one of its two new pieces (the other is gate's
`resolve` verb, the effect side).

## The boundary it keeps

- **It shells gate; it never imports or reads gate.** Its only mechanism is
  `internal/ingest`, which runs `gate resolve` and returns gate's own JSON
  outcome + exit code. Only gate may write gate's log, so the *effect* lives in
  gate; escalate owns the *ingest*. Same composition pattern as `console`
  (artifacts + exit codes, not call stacks) — enforced by CI's `hygiene` job.
- **It is not flare.** The whole reason this is a separate binary: flare is a
  read-only sink (Amendment 3) and is architecturally forbidden from writing a
  decision. The resolution ingest therefore *cannot* be a flare change.
- **Transport-agnostic ingest.** `ingest.Decision` is the shape a Slack action
  ack, a webhook POST, or a future gate UI would carry. The `resolve` verb fills
  it from CLI flags; the `serve` verb fills it from a signed Slack callback —
  two transports over one contract, the same `ingest.Client` mechanism under
  both.

## Verbs

- `escalate resolve …` — the CLI transport: a human's decision as flags.
- `escalate serve [-addr] [-gate] [-state]` — the HTTP transport (Phase 1 of
  `docs/features/escalation-plane/escalate-serve.md`): a Slack interactive-action
  ingress. It VERIFIES the Slack signature (HMAC-SHA256 over the raw body,
  constant-time, ±5-min window) before trusting anything, derives `who` from the
  *verified* identity (never a client field), then AUTHORIZES that verified user
  against an allowlist (`ESCALATE_ALLOWED_SLACK_USERS`, Slack user ids) — an
  authentic but unlisted channel member is refused 403 before any resolve, because
  authentication is not authorization. It then reads the grant from the parked
  escalation via `gate next -json` and drives the same `ingest.Client.Resolve`.
  Both `SLACK_SIGNING_SECRET` and `ESCALATE_ALLOWED_SLACK_USERS` are **required** —
  it refuses to start unauthenticated OR with no authorized users (fail-closed).
  Like `resolve`, it shells `gate` with no `-key`, so set `GATE_KEY` (or use
  gate's default key dir) or gate refuses the grant with `grant_bad_signature`.
  Binds loopback by default; a tunnel (ngrok/cloudflared) exposes it to Slack.

## Exit codes

A faithful pass-through of `gate resolve`'s contract — **0** merge / **1**
blocked / **2** parked / **3** refused / **4** error. An ingest-side failure to
even reach gate (a malformed decision, gate not runnable) exits **5**, outside
gate's code space so the two never collide.

## Develop (from the module root)

```
go build -o escalate.exe ./cmd/escalate
go vet ./cmd/escalate/...
golangci-lint run ./cmd/escalate/...
go test ./cmd/escalate/...
```

The only in-repo dependency is `contracts/escalation` (the shared decision
vocabulary + the escalation-id shape it validates).
