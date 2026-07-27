# `escalate serve` — remote resolution over Slack + a tunnel

**Status:** design note (scope for the next build increment on the escalation seam).
**Prereq:** the merged escalation seam (`contracts/escalation`, `gate resolve`,
`cmd/escalate resolve`, the replay guard). See `spec.md`.

## Goal

Close the escalation loop **from your phone**. Today `escalate resolve` is a CLI:
a human reads a Slack page, then goes to a terminal and types the decision. The
gap is the last mile — turning a **tap on the Slack card** into that decision,
with the operator never leaving Slack. This is spec §8 step 3, and it is the
increment that makes the seam something you actually *use*, not just something
that exists.

## The loop

```
gate parks ─▶ flare posts a Slack card WITH [Approve] [Block] buttons   ← flare change (render only)
                     │  operator taps Approve (phone, anywhere)
                     ▼
   Slack interactive-action callback (signed) ─▶ ngrok/cloudflared tunnel ─▶ escalate serve
                     │  verify Slack signature → who = the VERIFIED Slack user
                     │  map (escalation id, decision, who) → ingest.Decision
                     ▼
             gate resolve ─▶ judgment + KindResolution stamp ─▶ `gh pr merge` emitted
```

Every arrow that already exists is reused unchanged; only two things are new: a
few buttons on flare's card, and a small HTTP front door in `escalate`.

## The two new pieces

### 1. flare renders the buttons (Observability-safe) — **built (Phase 2)**

flare's briefed-escalation Slack card gains an `actions` block with **Approve** and
**Block** buttons. Each button's `value` carries the **escalation id** (already in
the event as `env.ID`); the callback URL points at **`escalate serve`, never at
flare**. This is a pure rendering change — flare still writes no decision, takes
no lock, owns no state. Amendment 3 holds: flare paints the button, `escalate`
handles the tap.

> Boundary check: flare must NOT call `escalate` or `gate`. It only sets Slack's
> `action_id`/`value` and lets Slack POST the callback to the configured URL. The
> decision path never re-enters flare.

**As built:** the button `action_id`s are the shared `contracts/escalation`
vocabulary (`ActionApprove` / `ActionBlock`) — flare renders them, `escalate serve`
parses them, neither imports the other (the boundary law). Rendering is gated on a
per-channel `resolve_actions` opt-in that is **off by default**: a rendered button
with no configured Slack Request URL is a dead tap, so the toggle *is* the
operator's "the ingress is wired" signal. And it fires only for a **resolvable
park** — event kind `escalation` carrying its artifact id — never for a
verdict-escalate or a cursor-alert, which `gate resolve` would refuse.

### 2. `escalate serve` — the HTTP ingress

A new verb on the existing binary: `escalate serve -addr :8099 -gate gate -state <dir>`.
It is a thin transport adapter over the *same* `ingest.Client` the `resolve` verb
already uses — the mechanism (validate → shell `gate resolve`) is unchanged; only
the *source* of the `Decision` differs (an HTTP callback instead of CLI flags).

Handler outline (one Slack interactive-action POST):

1. **Verify the Slack signature** — `X-Slack-Signature` / `X-Slack-Request-Timestamp`
   HMAC over the raw body with the signing secret, constant-time compare, and a
   ±5-min timestamp window. Reject anything unsigned or stale **before parsing**.
   This is what makes `who` *authenticated* rather than *asserted* (closes the one
   remaining ★ prereq in `spec.md` §7a).
2. **Map the payload** → `ingest.Decision{Escalation: value, Verdict: pass|block,
   Who: <verified slack user id/name>, Why: <"approved in Slack by @user">, Grant:
   <the run's grant>}`. `who` comes from the *verified* Slack identity in the
   payload, **never** from a field the client could set.
3. **Drive `ingest.Client.Resolve`** — exactly the path the CLI takes. The replay
   guard (`escalationIsOpen`, already merged) makes Slack's at-least-once callback
   retries and double-taps safe: the second one is refused, not double-applied.
4. **Respond in-channel** — update the Slack message ("✅ merged / ⛔ blocked by
   @user") from gate's returned JSON + exit code.

Where does the **grant** come from? The escalation body carries its `grant` id
(`escalation.V1.Grant`), so `serve` can read it from the parked escalation rather
than requiring the operator to paste it. (Resolve still re-checks the grant is
live at resolve time — remote approval only works inside an unexpired grant
window, which is correct: an approval must not outrun the delegation. Mint before
you step away.)

## The tunnel (ngrok / cloudflared)

`escalate serve` binds loopback-or-LAN; a tunnel exposes it to Slack:

```
escalate serve -addr 127.0.0.1:8099 -state ~/pers/gate/state &
ngrok http 8099        # → https://<random>.ngrok.app  → set as the Slack app's interactivity Request URL
```

The tunnel is the only public surface, and it fronts an endpoint that (a) rejects
every unsigned request and (b) can only ever drive `gate resolve` for an *already
parked* escalation under an *already live* grant. The blast radius is exactly
"approve/block a PR that gate already parked for judgment" — nothing else.

## Security model (the whole reason the split exists)

| Threat | Mitigation | Status |
|---|---|---|
| Forged callback (anyone POSTs a decision) | Slack signature verification, constant-time, timestamp window | **new in `serve`** |
| `who` spoofed | `who` derived from the *verified* Slack identity, never the payload body | **new in `serve`** |
| **Unauthorized (but authentic) user resolves** — any channel member taps the button | `ESCALATE_ALLOWED_SLACK_USERS` allowlist of Slack user ids, checked *after* the signature; an unlisted user is refused 403 before any lookup/resolve. serve **refuses to start** with no allowlist (fail-closed). Authentication ≠ authorization. | **new in Phase 2** |
| Replay / double-tap / Slack retry | `escalationIsOpen` guard refuses a non-open park | ✅ merged |
| Approval outside delegation | `gate resolve` re-checks the grant is live at resolve time | ✅ merged |
| flare becoming a decision-writer | callback targets `escalate`, not flare; flare only renders | by construction |
| Tunnel abuse | endpoint only drives `resolve` for a parked esc under a live grant; everything else rejected | by construction |

## Phasing

1. **`escalate serve` + signature verification** (no flare change yet) — testable
   with a `curl` of a captured Slack payload against a seeded park. Proves the
   ingress + auth + the `who`-from-verified-identity path. ✅ **SHIPPED** — see
   `EVIDENCE-escalate-serve-phase1.md` for the five-callback transcript (forged /
   stale / valid / replay / wrong-secret) against the real binaries.
2. **flare button rendering** — add the `actions` block; the shared action-id
   vocabulary lives in `contracts/escalation`; rendering is gated on a per-channel
   `resolve_actions` opt-in. ✅ **SHIPPED** — flare renders Approve/Block on a
   resolvable park, pinned by `notify` tests. Operator infra remains: create the
   Slack app, enable interactivity, point its Request URL at the tunnel (setup
   checklist below). No ngrok wired here.
3. **End-to-end over ngrok** — a real tap on a phone → merge, captured as evidence
   (mirroring the CLI evidence in `EVIDENCE-escalation-plane-poc.md`). ⏳ **NEXT** —
   the fail-closed e2e harness + phone-tap runbook land in the following increment;
   only the operator infra setup (the checklist below) + the real tap remain.

## Slack-app + tunnel setup (operator infra — the human boundary)

The code for Phases 2–3 is complete; standing up the Slack app and the tunnel is
manual, one-time operator work. In order:

1. **Create a Slack app** (api.slack.com/apps → *From scratch*) in the workspace
   the escalation channel lives in.
2. **Bot token scopes** (OAuth & Permissions): `chat:write`. Install to the
   workspace; copy the bot token (`xoxb-…`) into flare's routes file as the
   channel `token`, and invite the bot to the channel.
3. **Signing secret** (Basic Information → App Credentials): copy it to the box
   running `escalate serve` as `SLACK_SIGNING_SECRET` — serve refuses to start
   without it. It must be the SAME secret for both flare's Slack app and serve, or
   every callback fails signature verification.
4. **Authorize yourself** (who may resolve): set `ESCALATE_ALLOWED_SLACK_USERS`
   to your Slack **user id** (the immutable `Uxxxx`, comma-separated for more than
   one) on the box running serve. serve **refuses to start** without it — a signed
   callback only proves Slack sent it, not that the tapper may move a merge gate,
   so without an allowlist any channel member could resolve. Find your id in Slack:
   profile → *Copy member ID*.
5. **Run the ingress under a live grant's key:** `escalate serve` shells
   `gate resolve` with no `-key`, so export `GATE_KEY` (or use gate's default key
   dir) and mint the grant *before* stepping away — a remote approval only works
   inside an unexpired grant window.
6. **Tunnel:** `escalate serve -addr 127.0.0.1:8099 -state ~/pers/gate/state &`
   then `ngrok http 8099` → copy the `https://<random>.ngrok.app` URL.
7. **Interactivity** (Interactivity & Shortcuts → toggle on): set the Request URL
   to the tunnel URL. Save.
8. **Arm flare:** set `"resolve_actions": true` on the escalation channel in the
   routes file. The next briefed park renders live Approve/Block buttons.

Until steps 1–8 are done the buttons stay dark (the opt-in is off), so nothing
renders a dead tap.

### Phase 1 as shipped — two notes for Phase 2/3

- **Grant lookup uses the console read seam.** `serve` reads the grant from
  `gate next -json` (which now projects each parked run's `escalation` id) and
  joins on it — never importing gate, never trusting a client-supplied grant. A
  resolved park drops out of that inbox, so a replayed tap is refused at the
  lookup (409 `ErrNotParked`) ahead of gate's `escalationIsOpen` backstop.
- **`serve` shells `gate resolve` with no `-key`.** So the process needs
  `GATE_KEY` (or gate's default key dir) pointed at the operator's signing keys,
  exactly as the `resolve` CLI does — otherwise gate refuses the grant with
  `grant_bad_signature`. `-state` still comes from the flag / `$GATE_STATE`.

## Out of scope (for this increment)

- Persisting Slack↔run mapping beyond what the escalation artifact already carries.
- A hosted/always-on ingress (ngrok is the POC tunnel; a stable ingress is later).
- Agent→agent resolution (a bot posting a decision) — rides the same
  `ingest.Decision` shape unchanged; build when a concrete resolver exists.
- Rendering the resolution back into `gate next` / console (spec §8 step 4).

## Why this is the increment that earns the plane conversation

Once a **Slack button** and the **CLI** both drive the same `ingest.Decision`,
"resolution ingest" has *two transports over one contract* — the first real
evidence that it might be a role multiple things serve, not just a one-off seam.
That is the usage signal `spec.md` §3 says to wait for before touching
`workbench-101.md`'s plane count. Build the transport; let it argue its own case.
