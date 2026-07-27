# Evidence — `escalate serve` Phase 1 (signed Slack ingress → resolve)

**Date:** 2026-07-26 · **Host:** local Windows checkout (worktree `escalate-serve-phase1`)
**Binaries:** `gate` and `escalate` (with the new `serve` verb), built from this branch.

## What ran

The Phase-1 loop from `escalate-serve.md`: a **signed HTTP callback** — the shape
a Slack interactive-action POST takes — driven against the real `escalate serve`
binary, which verifies the signature, maps the payload to an `ingest.Decision`
with `who` taken from the *verified* identity, reads the grant from the parked
escalation (`gate next -json`), and drives the real `gate resolve`.

```
escalate serve -addr 127.0.0.1:8099 -gate gate -state <seeded>
   (SLACK_SIGNING_SECRET set; GATE_KEY points at the seed's signing keys)
```

**Honesty note on the seed.** Same as the POC: a live `gate gate` needs a GitHub
PR that is not available offline, so the **park** is seeded through gate's own
`act()` path (`TestSeedDemoState`). Everything downstream — the signature check,
the payload→decision map, the grant lookup, the resolve, the judgment, the
resolution stamp — is the real binary, end to end. The client that forges the
signed callbacks is a throwaway (`scratchpad/slackpost`, not in the repo); it
computes the Slack v0 signature exactly as Slack does.

Seed: `DEMO_SEEDED grant=grt_270ec013f45569d3 run=run_b2637869b4cd6d18 escalation=esc_93b8e2b0c7be74f9 code=2`

## Transcript — five callbacks against the running ingress

### (1) forged signature — rejected before parsing
```
HTTP 401
signature verification failed
```

### (2) stale timestamp (10 minutes old) — rejected on the window
```
HTTP 401
signature verification failed
```

### (3) valid, freshly signed **Approve** — drives `gate resolve` → would_merge
```
HTTP 200
{
  "run": "run_b2637869b4cd6d18",
  "pr": "itsHabib/ship#126",
  "decision": "pass",
  "tier": "T0",
  "outcome": "would_merge",
  "why": "judgment: approved in Slack by @michael (U1)",
  "action": "gh pr merge 126 -R itsHabib/ship --squash --delete-branch --match-head-commit abc123",
  "head_sha": "abc123"
}
```
`who` = `@michael (U1)` came from the **verified** Slack identity in the signed
payload — the mutable handle *and* the immutable Slack user id, never a
client-settable field. A top-level `"who":"attacker"` smuggled into the JSON is
ignored by construction (see the unit test).

### (4) REPLAY the same tap — refused, not double-applied
```
HTTP 409
serve: escalation is not currently parked: esc_93b8e2b0c7be74f9 not in gate inbox
```
The first Approve resolved the park, so it left the inbox `serve` reads the grant
from — the replay is refused at that first line of defense. gate's own
`escalationIsOpen` guard is the backstop behind it.

### (5) wrong signing secret — rejected
```
HTTP 401
signature verification failed
```

### (6) CONCURRENT double-tap (two Approves fired at once) — exactly once
```
tap A → HTTP 200  would_merge
tap B → HTTP 409  escalation is not currently parked
```
The per-escalation lock serializes the two; the winner resolves and the loser
finds the park already closed. The log recorded **exactly one** `resolution`
(`who:"@michael (U1)"`) — the concurrency the HTTP transport introduces cannot
double-apply. (Added after codex's P1 review of the first push.)

## The loop closed with provenance

After the Approve, the inbox went from 1 parked to **0**, and a `resolution`
artifact was appended, carrying the verified `who`:

```json
{"kind":"resolution","run":"run_b2637869b4cd6d18",
 "parents":["esc_93b8e2b0c7be74f9","jdg_37a5c41119365220"],
 "body":{"decision":"pass","who":"@michael (U1)","at":"2026-07-27T04:41:29Z","judgment_id":"jdg_37a5c41119365220"}}
```

`gate audit` → **chain intact**.

## What this proves (and what it doesn't)

Proven: the ingress, the signature gate (valid / forged / stale / wrong-secret),
the `who`-from-verified-identity path, the grant-from-parked-escalation lookup,
and replay safety — all through the real binaries. This is spec §7a's remaining
★ prereq (`who` authenticated, not asserted).

Not yet: flare rendering the buttons (Phase 2) and a real phone tap over an
ngrok tunnel (Phase 3). Phase 1 has no flare change.
