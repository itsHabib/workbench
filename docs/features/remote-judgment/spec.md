# Remote judgment — unblock gate parks from a phone — Technical Design Document

**Status:** draft / proposal — NOT a build commitment. The artifact we decide from.
**Owner:** @itsHabib
**Date:** 2026-08-21
**Related:** `docs/features/escalation-plane/spec.md` (the seam this extends),
`docs/features/escalation-plane/escalate-serve.md` (the shipped ingress this
hardens), `docs/features/escalation-live-2026-08-19/` (darwin ops + resolve-line
surfaces), `docs/auto-mode-defaults.md` (#3 fail closed, #5 authority is minted,
#6 move the boundary), `docs/workbench-101.md` §4 (planes + Amendments 2/3),
`FOLLOWUPS.md` ("resolve open-check→append", "durable resolution across a HARD
crash"), workbench PRs #130 #140 #150 #210 #238 #239 #240. Dossier project
`workbench`, phases `remote-judgment-*`.

> **Reviewers — focus areas:**
> 1. §4.1 — *this is a gap-close on a shipped path, not a new component.* Is the
>    inventory of what already exists (and what doesn't) accurate? The worst
>    outcome of this doc is building `escalate serve` a second time.
> 2. §4.3 / §7.2 — the **confirm-modal token**: a serve-signed, short-lived,
>    user-bound claim carried in Slack `private_metadata`. Is binding
>    `(escalation, decision, tapper, message, exp)` sufficient, and is the
>    Slack-signature + token + allowlist + grant stack layered in the right order?
> 3. §4.5 — where the **thread post-back** state lives. The design keeps flare
>    storing nothing new by reading `container.message_ts` off the interaction
>    payload; check that Amendment 3 survives and that no authoritative state
>    leaks into `escalate`.
> 4. §9 — phase 3 (batch) is **blocked on two open FOLLOWUPS gate races**, not
>    on product appetite. Agree that a batch over a subject-scoped-incorrect
>    open-park check is unsafe to ship?

## 1. Problem & hypothesis

The merge pipeline's terminating step is a human stamp: `gate judge` /
`gate resolve` (or `escalate resolve`, which shells it). Only the operator may
stamp, and every stamp today is either a terminal command or a Slack tap that
requires the ingress to be up, wired, and inside an unexpired grant window.
With 20+ parked runs the operator is the bottleneck for hours — not because
deciding is slow, but because *reaching the keyboard* is.

**Hypothesis.** The decision itself takes seconds and needs only the park's
brief, the tier, and a pass/block call. If the Slack card a park already produces
can carry the decision back through an authenticated, tunnelled local endpoint —
replaying the human's stamp under a grant the operator pre-minted with an explicit
TTL and tier ceiling — the operator clears a backlog from a phone in minutes, and
*nothing about gate's authority model changes*: same grant, same one-shot judgment,
same hash-chained artifacts, same pinned merge command.

**What this is.** A hardening + extension of the shipped escalation seam
(`escalate serve`) into something the operator actually uses away from the desk:
confirm-before-stamp, outcome-in-thread with the pinned merge command, written
reasons, and eventually a batch view.

**Non-goals (and why).**

- **Not a new plane, not a new binary.** `escalate` is already the resolution
  ingress; `flare` already renders the card. Adding a third component would
  re-split a seam that was deliberately shipped as one. (`escalation-plane/spec.md`
  §3.)
- **Not "remote gate."** No remote `gate gate`, no remote mint, no remote
  `executor` verbs, no remote merge. The only verbs reachable from a phone are
  the two human-stamp verbs (`resolve`, and `judge` only as a *model-judge
  request* in P2b), and only against a park that already exists.
- **Not a decision-maker.** The ingress never derives a pass/block; it
  transports one. A "smart" default (auto-approve T1) is a policy change that
  belongs in gate's floor, not in a transport (default #6).
- **Not a hosted service.** ngrok/cloudflared quick tunnels are the POC
  ingress; a stable named tunnel is a P4 stub. Multi-operator is out of scope.
- **Not the `gate executor` bridge.** `gate executor *` is the GitHub-App merge
  bridge (`cmd/gate/executor.go`, `contracts/gateauthorization`). This doc avoids
  the word "executor" for the phone path to keep the two apart.

## 2. Functional & non-functional requirements

**Functional**

- FR1. A park that carries a grant renders a card with **Approve** / **Block**
  actions; tapping one opens a **confirm modal** (P1) that names the PR, tier,
  grant id + expiry, and the exact decision; only the modal's **Submit** stamps.
- FR2. The stamp runs `gate resolve` with `-who` = the *verified* Slack identity,
  `-why` = operator text (P2) or a synthesized line (P1), under the grant the
  run parked with — never a client-supplied grant.
- FR3. The outcome (gate exit code + `outcome`) is posted **in the card's
  thread**, and on pass the post carries the **pinned `gh pr merge
  --match-head-commit` command** from the judged run's `action` artifact.
- FR4. Replays, double-taps, stale cards, expired grants, and out-of-tier
  decisions are refused with a human-readable reason on the card — never
  double-applied, never silently dropped.
- FR5. When the ingress is unreachable (tunnel down, serve not running) the card
  **says so** and the paste-ready CLI line remains the documented fallback.
- FR6. (P2) The operator can request the configured model judge (`gate judge
  -auto -provider …`) from the card; its verdict is posted to the thread and
  still goes through the human stamp before anything merges. *(Open — §10 Q3.)*
- FR7. (P3) A digest lists every open park under live grants; the operator can
  stamp several in one modal; each lands as its own one-shot judgment.

**Non-functional**

| Dimension | Target |
|---|---|
| Authority | Zero new authority. Every phone action is `gate resolve`/`gate judge` under an operator-minted grant; gate enforces tier ceiling, TTL, cycles, signature. A judge can never exceed the grant ceiling (existing law). |
| Integrity | Every stamp lands as `judgment` + `resolution` artifacts on the hash-chained log, parented to the escalation + grant. No artifact kinds added for P0–P2. |
| Idempotency | At-most-once stamp per escalation: `AppendIfAbsentParentWhereAfterAudit` + `requireOpenEscalation` (existing). Retries/replays → `judgment_duplicate` / `errStaleEscalation` → "☑️ already resolved". |
| Durability | An acked tap survives a SIGKILL of `escalate serve`: persist-before-ack, replay-on-start (P0). Today: graceful drain only. |
| Latency | Ack ≤ 3 s (Slack's window); outcome in thread ≤ 30 s p95 (resolve 25 s cap + deliver 10 s cap, existing). |
| Availability | Best effort. Down = nothing happens + the card says so (FR5). Never fail open. |
| Auth surface | Slack request signing (±5 min) → serve-signed modal token (≤10 min, user-bound) → Slack user-id allowlist → gate grant check. Every layer fails closed. |
| Boundary law | flare never calls escalate/gate; escalate never imports gate (shells `gate resolve`/`gate judge`/`gate next -json`); gate unchanged except where FOLLOWUPS already owes a fix. CI `hygiene` still passes. |
| Operability | One env file (`~/.flare/env`), two launchd agents (#239), one tunnel. Heartbeat file + `flare status` exit non-zero when the ingress is stale. |

## 3. Architecture overview

```
                 (Observability — renders, never gates)           (State)
 gate parks ──▶ flare sweep ──▶ Slack card [Approve][Block]       log.jsonl
                 │  reads ingress heartbeat → "ingress live / DOWN"    ▲
                 ▼                                                     │ judgment
  phone tap ─▶ Slack ─signed POST─▶ tunnel ─▶ escalate serve ──────────┤ resolution
               ▲        │ block_actions: verify sig → allowlist →      │ (+ action on pass)
               │        │   open confirm modal (views.open, token)     │
               │        │ view_submission: verify sig → verify token → │
               │        │   persist intent → ack → gate resolve ───────┘
               │        ▼
               └── thread reply: outcome + pinned merge command (chat.postMessage, thread_ts)
                    + card update via response_url (replace_original)
```

**Reused unchanged:** gate's grant/judgment/resolution machinery; `escalate`'s
`ingest.Decision` + `execRunner`; serve's signature verification, allowlist,
per-escalation lock, `response_url` delivery; flare's card, `resolve_actions`
opt-in, the `ActionApprove`/`ActionBlock` vocabulary in `contracts/escalation`;
the launchd plists and `~/.flare/env`.

**New:**

| Piece | Where | Plane |
|---|---|---|
| Interaction-type dispatch (`block_actions` vs `view_submission`) | `cmd/escalate/internal/serve` | ingress mechanism |
| Confirm modal + serve-signed `private_metadata` token | `cmd/escalate/internal/serve/modal.go` | ingress mechanism |
| Intent journal (persist-before-ack, replay on start) | `cmd/escalate/internal/intent` | ingress mechanism (non-authoritative) |
| Thread post-back with pinned merge command | `cmd/escalate/internal/serve/thread.go` | ingress mechanism |
| Heartbeat file + card "ingress" line | serve writes `<flare-state>/ingress.heartbeat`; flare reads it | Observability |
| `ActionJudgeAsk`, `ActionBatch*` action ids | `contracts/escalation` | contract |
| `escalate judge` verb (P2b) | `cmd/escalate` | ingress → `gate judge -auto` |
| Batch digest + multi-stamp modal (P3) | flare render + serve | render / ingress |

**Seams named.** Slack → serve: HTTP + Slack signing. serve → gate: argv + exit
code + stdout JSON (`gate resolve`, `gate judge`), `gate next -json` for grant
and `merge_command` lookup. serve → flare: one file (heartbeat), read-only.
Nothing else crosses.

## 4. Key decisions & trade-offs

### 4.1 Build on `escalate serve`; do not build a new component — **decided**

| | |
|---|---|
| Choice | Extend `cmd/escalate serve` (signed Slack ingress → `ingest.Client` → `gate resolve`). |
| Alternative | A new `cmd/remote` "executor" daemon with its own auth, queue, and verb table. |
| Why | The alternative already exists under the name `escalate serve` and has three evidence files. What is *missing* is: a confirm step (today one tap stamps), an outcome in the thread (today only the card is replaced), crash durability (`FOLLOWUPS.md` open), `-state` on the paste line (#240 open), a judge path, batch, and a real-tunnel capture. Those are increments on one binary, not a fourth tenant. A new verb table is also new attack surface with no new authority to justify it. |

### 4.2 Only human-stamp verbs are remotely reachable — **decided**

| | |
|---|---|
| Choice | Reachable from Slack: `gate resolve` (P1+), `gate judge -auto -provider` as a *request* whose verdict still needs the human stamp (P2b). Nothing else. |
| Alternative | A generic "run verb X with args Y" endpoint, allow-listed per verb. |
| Why | Default #5: authority is minted, not inferred. The phone never mints, never runs `gate gate`, never merges. `gate resolve` is the one verb whose *purpose* is recording a named human decision, and it already takes the open-park guard. A generic endpoint would turn the tunnel URL into a remote shell for whoever holds the signing secret. |

### 4.3 Confirm modal with a serve-signed, short-lived, user-bound token — **decided; reviewers weigh the binding**

| | |
|---|---|
| Choice | Tap → `views.open` confirm modal. `private_metadata` = `base64(json{esc, decision, uid, channel, ts, grant, exp}) . hmac_sha256(serve_key, …)`; `exp` = now+10 min. On `view_submission`: Slack sig → token sig + exp → `uid == payload.user.id` → allowlist → proceed. |
| Alternative A | No modal; keep one-tap (today). |
| Alternative B | Slack's built-in button `confirm` dialog (client-side only). |
| Alternative C | Store pending taps server-side keyed by a nonce; modal carries only the nonce. |
| Why | One-tap on a phone is a pocket-merge. Slack's `confirm` is cosmetic — the POST is the same, unsigned by us. C is sound but adds a store with expiry to a process whose law is to hold no authoritative state; the signed token is stateless, and its only secret is a key serve already must hold (`ESCALATE_TOKEN_KEY`, generated if absent, chmod 600). Binding `uid` defeats "tap on my phone, submit from yours"; binding `esc`+`decision` defeats modal-swap; `exp` bounds theft. The token is **not** authority — gate still checks the grant. |

### 4.4 `who` and `why` — **decided**

`who` = verified Slack identity (`@handle (Uxxxx)`, existing `slackWho`). P1 keeps
the synthesized `why`; P2 adds a required text input on the modal (min 8 chars)
and passes it verbatim as `-why`. Free text is a stamp, not a command: it is
recorded in the `resolution` artifact and never parsed.

### 4.5 Thread post-back reads `container.message_ts` off the payload; flare stores nothing — **decided**

| | |
|---|---|
| Choice | Slack's `block_actions` payload carries `channel.id` + `container.message_ts` of the card. serve copies them into the modal token; on outcome it calls `chat.postMessage` with `thread_ts` using `SLACK_BOT_TOKEN` (new env for serve; same app as flare). |
| Alternative | flare records the `ts` it got back from `chat.postMessage` in its journal; serve looks it up. |
| Why | The payload already has it — no new flare state, no lookup, no coupling. Amendment 3 carve-out for flare (cursors/dedupe) stays exactly as wide as it is. Cost: serve now holds a bot token, which it must for `views.open` anyway. |

### 4.6 Pinned merge command comes from `gate next -json`, not from parsing the log — **decided**

After a pass, serve reads `gate next -json` and joins `ready_to_merge[]` on the
run id (the ingress already shells `gate next -json` for the grant lookup). That
is the console read seam; serve never opens `log.jsonl`. If the run is absent
(ceiling, block, error) the thread post carries gate's `outcome` + `why` and no
command.

### 4.7 Persist-before-ack for durability — **decided**

An accepted `view_submission` is appended to `<escalate-state>/intents.jsonl`
*before* the 200 ack; `process` marks it done after delivery; on start serve
replays undone intents (gate's guards make replay safe: a second apply is
`judgment_duplicate`). This is non-authoritative transport state — the
decision of record is still gate's log. Closes the open FOLLOWUPS item.

### 4.8 "Tunnel down → card says so" via a heartbeat file flare reads — **decided; cheap**

serve touches `<flare-state>/ingress.heartbeat` (JSON: `{at, addr, tunnel_url?}`)
every 30 s. flare's card renders one context line: `ingress: live 12s ago` or
`⚠️ ingress stale 7m — buttons may be dead; use the line below`. flare reads a
file; it does not probe, call, or gate. `flare status` exits non-zero when the
heartbeat is older than 3× the interval so the existing launchd runbook surfaces
it. *Alternative:* flare HEADs the tunnel URL — rejected: flare would need the
URL and would be making outbound calls to the control path.

### 4.9 Batch waits on two gate FOLLOWUPS — **decided; gates P3**

FOLLOWUPS "Still open (1)" and "(2)": the open-park check is run-scoped while the
inbox is subject-scoped, and the terminal `act` append is not linearized with the
judgment. One stamp at a time, a human can notice a stale card. A batch of 20
cannot. P3 therefore depends on closing both (owner: gate) and is a gated stub.

### 4.10 `cmdJudge` and the open-park guard — **open fork (§10 Q2)**

Today `judge` deliberately skips `requireOpenEscalation` (FOLLOWUPS "Still open
(3)"). Once a second writer (`escalate serve` replaying intents, or P2b's model
judge) exists, the stated revisit condition is met. Proposal: set it (one line)
and accept the retry. Reviewers: agree?

## 5. Data model

**No new artifact kinds in gate's log for P0–P2.** Each remote stamp produces
exactly what a CLI stamp produces: `judgment` (jdg_) parented `[esc, grt]`,
`resolution` (res_) parented `[esc, jdg]` with body
`{decision, who, at, judgment_id}`, and on pass the `action` (act_) with
`{command, argv}`. The `who` string is the audit's proof of remote origin
(`@handle (Uxxxx)`); P2 adds nothing to the schema — `why` is already a field.

**New non-authoritative transport state (all under `escalate`'s own dir, default
`~/.escalate/`):**

```
intents.jsonl      # {id, at, esc, decision, who, why, uid, channel, ts, token_exp, state: accepted|done|failed, gate_exit?}
token.key          # 32 random bytes, 0600 — signs private_metadata (4.3)
```

**Heartbeat (under flare's state dir, written by serve, read by flare):**

```
ingress.heartbeat  # {"at":"2026-08-21T14:02:11Z","addr":"127.0.0.1:8099","tunnel":"https://x.ngrok.app"?}
```

**Contract additions (`contracts/escalation`):**

```go
ActionJudgeAsk   = "judge_ask"    // P2b: request the model judge; not a decision
ActionBatchOpen  = "batch_open"   // P3: open the multi-stamp modal
ActionBatchStamp = "batch_stamp"  // P3: view_submission callback id
```

`V1` is unchanged. Phase 3 may add an optional `thread_ts`-free digest shape;
deferred until the phase unblocks.

## 6. API contract

### 6.1 Slack → `escalate serve` (one Request URL, any path)

| `payload.type` | Action | Sync response |
|---|---|---|
| `block_actions`, `action_id ∈ {approve, block}` | verify sig → allowlist → build token → `views.open(trigger_id, confirm modal)` | 200 empty (modal opens) |
| `view_submission`, `callback_id = confirm_stamp` | verify sig → verify token (sig, exp, uid) → allowlist → append intent → ack | 200 `{"response_action":"clear"}` |
| `block_actions`, `action_id = judge_ask` (P2b) | verify sig → allowlist → run `escalate judge` async → thread post | 200 (card → "⏳ asking judge") |
| `view_submission`, `callback_id = batch_stamp` (P3) | as confirm, N intents | 200 clear |
| anything else | 400 / 401 / 403 / 405 | — |

Errors shown *in the modal* via `response_action: errors` when the token is
expired (`"This confirmation expired — tap Approve again"`).

### 6.2 `escalate serve` config surface (env, via `~/.flare/env`)

| Var | Req | Purpose |
|---|---|---|
| `SLACK_SIGNING_SECRET` | ✓ | request signing (existing) |
| `ESCALATE_ALLOWED_SLACK_USERS` | ✓ | user-id allowlist (existing) |
| `SLACK_BOT_TOKEN` | ✓ (P0+) | `views.open`, thread `chat.postMessage` |
| `ESCALATE_STATE` | – | intents + token key dir; default `~/.escalate` |
| `ESCALATE_TOKEN_KEY` | – | override token key path |
| `FLARE_STATE` | – | where to write `ingress.heartbeat`; default `~/.flare` |
| `GATE_STATE`, `GATE_KEY` | ✓ | as today — serve shells gate with no `-key` |

New flags: `-heartbeat 30s`, `-intents <path>`. Refuses to start without
`SLACK_BOT_TOKEN` once P0 lands (a modal that cannot open is a dead tap).

### 6.3 `escalate judge` (P2b)

```
escalate judge -run run_x -grant grt_x -provider claude|codex [-gate gate] [-state DIR]
  → argv: gate judge -run run_x -grant grt_x -auto -provider <p>
  exit: gate's 0/1/2/3/4 pass-through; 5 ingest error (same law as resolve)
```

### 6.4 Thread post (what the operator sees)

```
✅ @mh (U0123) approved esc_9f… — gate: would_merge (exit 0)
gh pr merge 241 --repo itsHabib/workbench --squash --match-head-commit 7e43bc2…
```
```
⛔ @mh (U0123) blocked esc_9f… — "flaky e2e, not a real pass"
```
```
☑️ Already resolved — esc_9f… was stamped at 14:01 by @mh (U0123); nothing to do.
```
```
🚫 Refused — grant grt_ab… expired 12m ago. Mint a new one at the keyboard:
gate grant -repo itsHabib/workbench -max-tier T2 -ttl 8h -state …
```

The last line is default #3: every refusal prints its own remedy. The remedy for
an expired grant is *always* a keyboard command — the phone cannot mint.

### 6.5 Invariants (asserted by tests, not prose)

1. No request path reaches `gate` without passing `verify()` first.
2. `view_submission` without a valid token is 401 even with a valid Slack signature.
3. `token.uid ≠ payload.user.id` → 403.
4. The grant passed to `gate resolve` equals `gate next -json` `parked[].grant` for that escalation — never a payload field.
5. A replayed `view_submission` (same token) yields exactly one `judgment` artifact.
6. serve never writes under `GATE_STATE`.
7. flare's `notify` package has no import of `net/http` client code aimed at escalate, and no reference to the tunnel URL.

## 7. Key flows

### 7.1 Happy path (P1): tap → confirm → stamp → thread

1. gate parks `run_x` with `esc_y` under `grt_z`; flare's sweep renders the card
   (buttons on because `resolve_actions: true` and the park is resolvable).
2. Operator taps **Approve** on the phone. Slack POSTs `block_actions` to the
   tunnel. serve: `verify()` ✓ → allowlist ✓ → builds token
   `{esc_y, pass, U0123, C0ESC, 1724250131.001, grt_z, exp=+10m}` → `views.open`
   with the PR title, tier, grant id + `expires in 6h`, and "You are about to
   **approve** esc_y".
3. Operator hits **Submit**. Slack POSTs `view_submission`. serve: `verify()` ✓ →
   token sig ✓, `exp` ✓, `uid` ✓ → allowlist ✓ → append intent `accepted` →
   respond `clear` (≤ 3 s).
4. Background, under the per-escalation lock: `gate next -json` → grant for
   `esc_y` (must equal token's; mismatch = refuse, the run re-parked) →
   `gate resolve -escalation esc_y -grant grt_z -decision pass -who "@mh (U0123)"
   -why "approved in Slack by @mh (U0123)"` → exit 0.
5. `gate next -json` again → `ready_to_merge[].merge_command` for `run_x`.
6. `chat.postMessage(thread_ts=1724250131.001)` with the ✅ line + command;
   `response_url` `replace_original` drops the buttons. Intent → `done`.

### 7.2 Token misuse

- **Expired token** (operator leaves the modal open 11 min): step 3 fails `exp`
  → `response_action: errors` on the modal. No intent, no gate call.
- **Forged `view_submission`** (attacker has the Slack signing secret but not
  serve's token key): Slack sig ✓, token sig ✗ → 401. Logged with the Slack user
  id it claimed.
- **Token replay** (same payload twice): first lands; second passes all
  verification, appends an intent, hits `gate resolve` → `errStaleEscalation`
  (not open) → "☑️ already resolved" in thread. Exactly one judgment exists.
- **Cross-user submit** (token minted for U0123, submitted by U0456 who is also
  on the allowlist): `uid` mismatch → 403. Defence in depth; both users are
  trusted, but a decision must be attributable to the one who read the brief.

### 7.3 Stale card / re-park race

The card for `esc_y` is 40 min old; meanwhile the PR was re-gated and re-parked as
`esc_w`. Operator approves `esc_y`. Step 4: `gate next -json` no longer lists
`esc_y` → `ErrNotParked` → thread: "☑️ This park was superseded by esc_w — open
the newer card." No gate write. (Backstop if the inbox is stale: gate's own
`requireOpenEscalation` refuses under the lock.)

### 7.4 Grant expired between tap and submit

Token still valid; `gate resolve` → exit 3 `capability_refused: grant_expired`.
Thread: 🚫 + the mint command (§6.4). The remedy is keyboard-only by design.

### 7.5 Tunnel down

Slack's POST fails; Slack shows its own "⚠️ failed to send" on the button. Nothing
reached serve → nothing happened. Because serve's heartbeat stops, the *next*
sweep renders "⚠️ ingress stale" on new cards and `flare status` goes red; the
paste-ready line is the fallback. Existing cards are not retro-edited (flare
keeps no message ids).

### 7.6 Serve SIGKILLed after ack

Intent `accepted` is on disk. On restart, serve replays: same path as step 4.
If the first attempt had actually reached gate before the kill, replay gets
`judgment_duplicate` → intent `done`, thread gets "☑️ already resolved" (the
thread may have missed the original ✅ — acceptable, and it prints the command
again on a pass via `gate next -json`).

### 7.7 Model-judge request (P2b)

Tap **Ask judge** → serve: verify → allowlist → card "⏳ asking claude" →
`escalate judge -run run_x -grant grt_z -provider claude` → gate writes the
judgment *if the provider passes/blocks within the ceiling* (this is the existing
`-auto` path — it **is** a real judgment, one-shot). Thread: the verdict + why +
either the merge command or the block reason. **Open question Q3:** whether the
phone may trigger a model judgment at all, or only a *dry* advisory. The
conservative P2b is advisory-only via `-judgment -` dry mode if gate grows one;
otherwise P2b ships as "run the same command the operator would type."

## 8. Concurrency / consistency / failure model

- **Serialization.** Per-escalation mutex in serve (existing) + gate's locked
  `AppendIfAbsentParentWhereAfterAudit` (existing). Two serve processes on one
  state dir are *not* supported in P0–P2 (documented; launchd `KeepAlive` runs
  one). P3 requires FOLLOWUPS (1)(2) closed.
- **Retry.** serve never retries `gate resolve` on its own; a non-zero exit is a
  result. Slack's at-least-once delivery and the intent replay are the only
  retry sources, both made safe by gate's guards.
- **Consistency.** The decision of record is gate's log. intents.jsonl and the
  Slack thread are projections; if they disagree with the log, the log wins and
  `gate explain` is the tie-breaker.
- **Time.** Three clocks matter: Slack sig window (±5 min), token `exp` (10 min),
  grant TTL (hours). Each is checked by the layer that owns it; none is trusted
  from the client.
- **Dependencies down.** Slack API down → modal cannot open → tap does nothing,
  serve logs it (fail closed). gate binary missing → exit 5, thread gets the
  error. `gate next -json` failing → refuse the stamp (cannot prove the grant).

## 9. Rollout / implementation plan

Critical path: **P0 → P1 → VALIDATION GATE → P2a → (P2b) → P3**. P0 is
no-regret hardening of the already-shipped path; P1 is the smallest increment
that makes the phone safe to use daily. Everything after the gate is earned by
the gate.

| Phase | Goal | High-level tasks | Depends on | Gate | ~wLOC |
|---|---|---|---|---|---|
| **P0** — harden the shipped path | The one-tap path that exists today becomes trustworthy enough to extend | Merge #240 (`-state` on the paste line); `SLACK_BOT_TOKEN` + thread post-back with `merge_command` via `gate next -json` (4.5, 4.6); persist-before-ack intent journal + replay-on-start (4.7); heartbeat file + flare "ingress" line + `flare status` red (4.8); payload struct gains `channel`, `container.message_ts`, `trigger_id`; real-tunnel evidence capture (the un-captured phone tap) | — | none — **ships now (no-regret)** | 400–600 |
| **P1** — confirm modal | No pocket-merges: a stamp needs a second, explicit, attributable step | Interaction dispatch (`block_actions` / `view_submission`); `views.open` confirm modal; serve-signed `private_metadata` token (4.3) + `token.key` mgmt; modal error surfaces; invariant tests §6.5 1–6; runbook: Slack app needs `interactivity` + `views`; EVIDENCE file | P0 | pre-gate | 400–600 |
| **GATE** | | Validation plan §11 | P1 | **GO/NO-GO** | — |
| **P2a** — written why | The operator's reason is the operator's words | Required `why` text input on the confirm modal; pass verbatim as `-why`; length + control-char guard; thread shows it | P1 + gate | post-gate | ≤200 |
| **P2b** — model-judge request | Ask the configured judge from the phone; human still stamps | `ActionJudgeAsk`; `escalate judge` verb (§6.3); `cmdJudge` takes `requireOpenEscalation` (4.10, owner gate); thread renders verdict; **Q3 must be answered first** | P2a | post-gate, **demand-gated** | ~250 |
| **P3** — batch | Clear a 20-park backlog in one modal | Digest card (flare render); multi-select modal; N intents → N sequential locked resolves; per-row thread outcomes; per-batch cap (≤10) + per-grant cycle awareness; `ActionBatch*` | P2a + **FOLLOWUPS (1)(2) closed in gate** | post-gate, **blocked on gate races** | 400–600 |
| **P4** — stable ingress | Named tunnel, always-on, rotation | cloudflared named tunnel in the plist; secret rotation runbook; second-operator allowlist semantics | P1 | demand-gated stub | — |

Commitment boundary: **P0 + P1.** P2–P4 are stubs in dossier with tasks
materialized only when the gate passes.

## 10. Open questions

- **Q1 — Token key custody.** `ESCALATE_TOKEN_KEY` is a second secret on the box.
  Should it live under the `custody` plane (`docs/features/custody/spec.md`) from
  day one, or is a 0600 file acceptable until custody v0 lands? *Proposal:* 0600
  file now; FOLLOWUPS entry to migrate.
- **Q2 — `cmdJudge` open-park guard.** Flip it in P2b (one line + accept the
  retry), or keep the recorded trade-off? The stated revisit trigger ("a second
  writer") is met by P0's replay. *Proposal:* flip in P0, not P2b.
- **Q3 — May the phone trigger a model judgment?** `gate judge -auto` writes a
  real one-shot judgment. Remote-triggering it is still "replaying an operator
  decision" (the operator decided to ask the judge) but the *verdict* is the
  model's. Options: (a) allow, it's the same command the operator would type;
  (b) advisory-only — needs a dry mode gate doesn't have; (c) drop P2b.
  *Proposal:* (a), because the grant ceiling bounds it identically either way,
  but this is the operator's call.
- **Q4 — Who sees the buttons.** The allowlist is serve-side; a channel member
  off the list sees live buttons that 403. Render-side gating would need flare to
  know the allowlist (coupling). *Proposal:* accept; the 403 is cheap and logged.
- **Q5 — Card retro-edit on ingress down.** FR5 is satisfied for *new* cards
  only. Retro-editing old cards needs flare to keep `ts` (Amendment 3 carve-out
  widening). *Proposal:* no; `flare status` + the paste line cover it.

## 11. Validation plan

Binary, baseline-free, one week after P1 lands:

1. **≥ 10 real phone stamps** (not loopback) across ≥ 3 repos, each captured as
   a `who`-bearing `resolution` artifact whose `who` matches
   `@handle (Uxxxx)` — proves the path is used, not just built.
2. **Zero double-applies**: `gate audit` shows exactly one `judgment` per
   escalation touched remotely.
3. **Zero off-grant stamps**: every remote `judgment`'s grant parent was minted
   by the operator with `-ttl ≤ 24h`; no remote action produced a `grant`.
4. **One induced tunnel outage** during the week: `flare status` went red within
   2 sweeps, the card line said so, the CLI fallback was used once.
5. **One induced SIGKILL** mid-resolve: the intent replayed on restart and the
   log shows one judgment.

GO → materialize P2a. NO-GO on (2) or (3) → stop, this is an authority bug.
NO-GO on (1) → the keyboard wasn't the bottleneck; close the initiative.
