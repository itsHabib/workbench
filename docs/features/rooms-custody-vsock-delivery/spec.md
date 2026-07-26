# rooms-custody-vsock-delivery — Technical Design Document

**Status:** draft / proposal — NOT a build commitment. The artifact we decide from.
**Owner:** @itsHabib
**Date:** 2026-07-26
**Related:** rooms `docs/features/vsock-secrets/spec.md` (the channel this rides — already built, #79/#82) · [`docs/features/custody/spec.md`](../custody/spec.md) · [`docs/features/rooms-inference-custody-proxy/spec.md`](../rooms-inference-custody-proxy/spec.md) (sibling; delivers the same vars over SendEnv in v0, this flips the channel) · workbench#127 (made the receipt honest about the *current* SendEnv channel) · memory `runway-custody-integration`

> **Reviewers — focus areas:**
> - **§4 D1** — what actually needs building: rooms' vsock `--secret` channel is *done*. This is workbench wiring + receipt honesty, not a new mechanism. Confirm the seam is that thin.
> - **§4 D2** — does `CUSTODY_BASE_<KEY>` (a URL, not a secret) ride the vsock channel too, or stay SendEnv?
> - **§7 flow A / §8** — the fail-closed invariant: no `workload_started` unless the grant was confirmed staged, and the grant value never lands in the guest process environment.

## 1. Problem & hypothesis

The rooms custody integration derives a scoped child grant on the host and delivers it to
the guest as `CUSTODY_GRANT_<KEY>` (resolver.go) — but over **SSH SendEnv**. An env var
**persists in the guest process environment for the whole session**: it is readable via
`/proc/<pid>/environ`, inherited by every child of the agent, and is not one-shot. workbench#127
made the receipt tell the truth about this — `grantFromRecord` reports
`Channel: "ssh_sendenv"`, `OneShot: false` (receipt.go:64-89), with a comment stating plainly
that the vsock first-read-then-delete path "the grant-materialized-rooms spec (D6/§6)
ultimately intends" is a *tracked cross-repo follow-up*. **This TDD is that follow-up.**

The good news: rooms already shipped the hard part. `rooms --secret <NAME>` reads the value
from the launching process's env, removes it so SendEnv cannot also forward it, delivers it
over a per-room one-shot virtio-vsock channel, and the guest fetch hook stages it at
`/run/rooms/secrets.env` (tmpfs, `0600`) then the runner reads-and-`unlink`s it — never
assigning it back into `process.env` (rooms vsock-secrets spec §5). The workload is gated on
delivery: no ack → no `workload_started` (fail closed). What's missing is purely the
**workbench-side wiring**: route the custody grant through `--secret` instead of SendEnv, and
make the receipt report the channel that then actually runs.

**The bet:** flip the custody grant's delivery from SendEnv to the existing vsock channel, so
the child grant is **memory-only, first-read-then-delete, never in the guest env** — the D6
intent — and `grantFromRecord` honestly reports `vsock` / `OneShot: true` because the run
genuinely performs a one-shot delivery.

**Non-goals:**
- **Not** building a vsock mechanism — rooms owns it and it's merged. If this TDD grows a
  rooms `src/` change, it's a small guest-side read for the claude-code path, not new transport.
- **Not** the inference-proxy feature (sibling TDD). That decides *what* the guest does with
  the grant (points inference at the proxy); this decides *how* the grant arrives. They share
  the `CUSTODY_GRANT_<KEY>` / `CUSTODY_BASE_<KEY>` var names, so the runner-side read is the
  one place they touch — sequenced in §9.
- **Not** migrating `GH_TOKEN` — it rides a separate post-workload SSH session
  (`push_branch_in_guest`), is never exposed to the agent, and rooms vsock-secrets §8 already
  parks its migration as its own follow-up.

## 2. Functional & non-functional requirements

**FR1.** The custody child grant reaches the guest over the rooms vsock `--secret` channel,
not SSH SendEnv.

**FR2.** After the workload starts, the grant value is absent from the guest process
environment (`/proc/<pid>/environ`), absent from `/proc/cmdline` and host `ps`, and
`/run/rooms/secrets.env` no longer exists.

**FR3.** `grantFromRecord` reports `Channel: "vsock"` and `OneShot: true`, and the receipt's
`Enforced["secret_transport"]` matches. The receipt stops carrying the SendEnv claim for
custody grants.

**FR4.** Fail closed, reusing rooms' gate: if the grant is requested but not confirmed staged
(no ack within the timeout), the room emits `secrets_failed` and never reaches
`workload_started`. Admission still refuses a custody placement whose gateway/source/egress
config is unset (`requireCustodyConfig`, `requireEgressConfig`).

**FR5.** Non-custody placements are unchanged — raw allowlisted keys keep their current
SendEnv path until their own migration (out of scope here).

| NFR | Target |
|---|---|
| Secret exposure | Grant is memory-only in the guest for the window between stage and first read; never in env, cmdline, or any collected artifact (rooms vsock-secrets §10 T1–T4) |
| Fail-closed | No `workload_started` without a confirmed-staged grant (rooms vsock-secrets §6 invariant) — inherited, not re-implemented |
| Honesty | Receipt channel == the channel that ran; no aspirational claim (the #127 discipline, now satisfiable) |
| Blast radius | Additive: rooms transport unchanged; workbench change is runArgs + receipt; guest change is at most a claude-code read hook |

## 3. Architecture overview

```
  host: runway rooms backend
     resolver derives child grant  → value into the `rooms` process env as CUSTODY_GRANT_<KEY>
     runArgs adds:  --secret CUSTODY_GRANT_<KEY>            (NEW — was: rely on SendEnv)
        │
        ▼
  rooms (unchanged, #79/#82):
     harvest_secrets reads CUSTODY_GRANT_<KEY> from env, REMOVES it from env (no SendEnv leak)
     one-shot vsock listener → guest fetch hook stages /run/rooms/secrets.env (tmpfs 0600)
     gate: ack ⇒ secrets_delivered ⇒ workload_started;  no ack ⇒ secrets_failed ⇒ no workload
        │
        ▼
  guest runner:
     cursor-runner.js   — already reads+unlinks /run/rooms/secrets.env (vsock-secrets §5.5)
     claude-code --command — reads the same file, exports the vars, then the file is gone   (NEW seam, small)
        │
        ▼
  host: grantFromRecord → Delivery{Channel:"vsock", OneShot:true}     (NEW — honest)
```

**Reused, unchanged:** the entire rooms vsock mechanism — device config, jail-scoped UDS,
one-shot listener, ack gate, guest fetch hook, atomic staging, the fail-closed matrix (rooms
vsock-secrets §5–§6). The custody resolver + child-grant derivation (resolver.go). The egress
wall + admission gates (#125).

**New, three small seams:**
1. **workbench runArgs** — emit `--secret CUSTODY_GRANT_<KEY>` (and per D2, maybe
   `CUSTODY_BASE_<KEY>`) so rooms harvests the value into the vsock blob and strips it from
   SendEnv. Drop the custody vars from the SendEnv delivery path (`secretEnvNames` /
   `roomsEnv`).
2. **workbench receipt** — `grantFromRecord` → `vsock` / `OneShot: true`; retire
   `deliveryChannelSendEnv` for custody; `Enforced["secret_transport"]` follows.
3. **guest claude-code read** — the `--command` path reads `/run/rooms/secrets.env` and
   exports the custody vars (cursor already does this; rooms vsock-secrets §8 notes `--command`
   consumption is per-command). Small rooms-repo bake/wrapper change, or a shared read helper.

## 4. Key decisions & trade-offs

**D1 — this is wiring, not a mechanism. (DECIDED.)** rooms `--secret` + vsock is merged and
e2e-proven (vsock-secrets §10). The temptation to "design the vsock delivery" is already
spent effort; the honest scope is: workbench passes `--secret`, workbench tells the truth in
the receipt, and the claude-code guest path learns to read the staged file. Rejected:
re-opening any transport design. If the seam turns out thicker than three small changes, that's
a signal the rooms mechanism doesn't fit and belongs back in rooms — not a reason to grow this
doc.

**D2 — does `CUSTODY_BASE_<KEY>` ride vsock too? (RECOMMEND: yes.)** The grant is the secret
and **must** move to vsock (FR1). `CUSTODY_BASE_<KEY>` is a URL (`<gateway>/<key>`,
config.go:116) — not sensitive, but it reveals the proxy topology and pairs with the grant.
Options:
- **(a) both ride vsock** — the guest reads one file for everything custody-related, nothing
  custody sits in the env, and delivery is uniformly one-shot. `--secret` is name-agnostic
  mechanism, so a URL value is fine. **Recommended** — single channel, cleanest guest read.
- **(b) grant vsock, base SendEnv** — smaller change, but splits the guest read across two
  sources and keeps a custody var in the persistent env for no security benefit. Rejected
  unless (a) complicates the fetch-hook/`--secret` validation (e.g. rooms' `--secret` rejects a
  value with a `/` or `:` — needs a one-line check; §10 Q1).

**D3 — keep SendEnv as a migration fallback, delete it only after a real run. (DECIDED.)**
Mirror rooms vsock-secrets' own P4 discipline: land the vsock path with the SendEnv path still
present as a fallback, prove one real custody placement end-to-end on vsock (the gate), *then*
remove the SendEnv `AcceptEnv`/`SendEnv` lines and the custody branch of `secretEnvNames`.
Deleting the fallback in the same PR that adds the path risks a green unit suite over a guest
that can't actually read the file. Cheap-to-keep-closed over big-bang.

**D4 — receipt honesty is not optional and ships *with* the wiring, not after. (DECIDED.)**
The #127 comment is explicit that claiming vsock while running SendEnv would make the receipt
lie. The inverse is equally true: the moment delivery moves to vsock, `grantFromRecord` MUST
flip to `vsock`/`OneShot:true` in the same change, or the receipt now *understates* the posture
and the honesty invariant breaks the other way. Delivery-channel and receipt-channel change
together, always.

## 5. Data model

No new persistent types. Field-level changes only:

- **`authority.Delivery`** (receipt) — `Channel` value moves from `"ssh_sendenv"` to
  `"vsock"` for custody grants; `OneShot` `false → true`. Existing struct, existing fields.
- **`Enforced["secret_transport"]`** (receipt map, rooms.go) — `"ssh_sendenv"` →
  `"vsock"` for custody placements.
- **rooms `--secret` blob** — one more `NAME=value` line (`CUSTODY_GRANT_<KEY>`, and per D2
  `CUSTODY_BASE_<KEY>`). No format change; the blob is already an arbitrary `NAME=value`
  carrier (vsock-secrets §5.2).

## 6. API / config contract

**workbench runArgs (`rooms.go`):** for each custody grant, append
`--secret CUSTODY_GRANT_<KEY>` (and per D2 `--secret CUSTODY_BASE_<KEY>`). The value must be
present in the `rooms` child process env at spawn — it already is (the resolver populates
`prep.Env`, `roomsEnv` forwards it; with `--secret`, rooms harvests-and-strips it).

**workbench `secretEnvNames` / `roomsEnv`:** stop scheduling the custody vars for SendEnv once
the fallback is retired (D3 phase 2). During migration both may be set; rooms' `harvest_secrets`
removing the var from env is what prevents a double-delivery (SendEnv can't forward what's been
removed).

**workbench `validateSecrets`:** its comment already *claims* custody refs "deliver a derived
child token over vsock (D6), not through SSH SendEnv" (rooms.go:568-572) — this TDD makes the
code match the comment. No allowlist logic change.

**rooms guest (claude-code `--command` path):** read `/run/rooms/secrets.env`, export the
custody vars into the command's environment, rely on the runner having `unlink`ed the file
(vsock-secrets §5.5 semantics). Mechanism is runner-agnostic (vsock-secrets §8); the wiring is
per-command.

**rooms `--secret` value constraints:** values may not contain a newline (blob integrity,
vsock-secrets §6). A URL (D2) is newline-free; confirm no other char constraint bites (§10 Q1).

## 7. Key flows

**A — happy path (the whole point).** Custody placement admitted → resolver derives the child
grant, value set in the `rooms` env as `CUSTODY_GRANT_ANTHROPIC_INFERENCE` → runArgs adds
`--secret CUSTODY_GRANT_ANTHROPIC_INFERENCE` → rooms harvests it, strips it from env, binds the
one-shot listener → guest fetch hook stages `/run/rooms/secrets.env` → ack →
`secrets_delivered` → runner reads the grant, `unlink`s the file, uses it (points inference at
the proxy, sibling TDD) → workload runs with the grant in memory only → receipt records
`Channel: vsock, OneShot: true`. `/proc/<pid>/environ` never held the grant.

**B — fail closed on no ack.** Old image without the fetch hook, or a vsock-less guest → no ack
within the timeout → `secrets_failed`, no `workload_started`, `cleanup_done`, zero leaks
(vsock-secrets §6). The minted child grant is unused; operator sees why in `result.json`.

**C — migration window (D3).** Fallback present: both `--secret` and the SendEnv name are set.
rooms harvests-and-strips → the guest reads the file → SendEnv has nothing to forward (var was
removed host-side). If the guest image predates the fetch hook, delivery fails closed rather
than silently falling back to SendEnv — the fallback is for *code*-path safety during rollout,
not a silent-degrade at runtime.

**D — receipt honesty holds both ways (D4).** Whichever channel actually ran is what the
receipt reports. Post-migration a custody grant reports `vsock`; a placement that somehow ran
SendEnv (fallback exercised) must still report `ssh_sendenv` — the channel is read from what the
run did, never assumed.

## 8. Failure model

Inherited from rooms vsock-secrets §6 — this TDD adds no new failure surface, it changes which
channel a custody grant uses:
- Listener bind fails → `boot_failed`. Listener errors post-bind → `secrets_failed` immediately.
- Guest fetch/stage fails mid-write → no ack (guest acks only after atomic stage) →
  `secrets_failed`.
- Second connect to the port → connection refused (socket unlinked).
- Workload crashes before deleting `secrets.env` → tmpfs in a disposable VM, torn down, never
  collected.
- The invariant reviewers should try to break: **no `workload_started` in which the custody
  grant was not confirmed staged** — and, added here, **no path in which the grant value reaches
  the guest process environment.**

## 9. Rollout / implementation plan

| Phase | Goal | High-level tasks | Depends on | Gate |
|---|---|---|---|---|
| 1. `vsock-deliver-custody` | the custody grant rides vsock; the receipt tells the truth | (1a) workbench runArgs: `--secret CUSTODY_GRANT_<KEY>` (+ base per D2), value kept in the rooms env, custody vars dropped from the SendEnv *delivery* [sonnet]; (1b) workbench receipt: `grantFromRecord` → `vsock`/`OneShot:true` + `Enforced["secret_transport"]`, retire `deliveryChannelSendEnv` for custody, make `validateSecrets` code match its comment [sonnet]; (1c) rooms guest: claude-code `--command` reads `/run/rooms/secrets.env` and exports the custody vars [opus, cross-repo] | rooms vsock-secrets (merged), #127 | **VALIDATION GATE** below |
| 2. `vsock-drain-sendenv` | the SendEnv fallback for custody vars stops existing | remove the custody branch of `secretEnvNames`, the custody `AcceptEnv`/`SendEnv` lines, and any migration fallback; one dogfood run on vsock-only | phase 1 + gate | - |
| 3. `vsock-drain-rest` | remaining secrets + GH_TOKEN onto vsock (stub) | migrate `GH_TOKEN` (separate SSH session today) and any other raw secret; converge with the inference-proxy-harden phase | phase 2 | each needs its own evidence; may fold into inference-proxy-harden or a rooms follow-up |

Rough scope: phase 1 is three small tasks across two repos (1c, the claude-code guest read, is
the only non-trivial one — cursor already reads the file). Phase 2 is one cleanup task gated on
the real run. Phase 3 is a deliberately-unsized stub.

**VALIDATION GATE (after phase 1):** one real custody placement on the rooms-host, grant
delivered over vsock, and (reusing rooms vsock-secrets §10 checks scoped to the grant value):
- (a) the agent process tree's `/proc/<pid>/environ` does **not** contain the grant;
- (b) `/run/rooms/secrets.env` is gone once the workload is running;
- (c) `/proc/cmdline`, host `ps auxww`, and the collected artifacts do not contain the grant;
- (d) the receipt reports `Channel: vsock, OneShot: true`;
- (e) `--witness` composes (the same run yields witness evidence, and — with the sibling
  inference-proxy feature — zero direct-vendor egress).
Phases 2–3 are not committed until this passes.

## 10. Open questions

1. **Does rooms `--secret` accept a URL value?** (D2) — values are newline-rejected
   (vsock-secrets §6); confirm no `/`/`:`/length constraint rejects `CUSTODY_BASE_<KEY>`. If it
   does, keep the base on SendEnv (D2 (b)) — a one-line rooms relaxation or a documented split.
2. **Shared guest read helper vs per-runner?** cursor-runner.js reads the file inline; the
   claude-code `--command` path needs the same. A tiny shared shell/JS helper baked into the
   image (`rooms-load-secrets`) vs duplicating the read in each runner. Lean shared, decide in
   code (rooms convention).
3. **Sequencing with the inference-proxy TDD.** That feature ships the guest runner *using* the
   grant (over SendEnv, its D5); this flips the channel. Cleanest order: inference-proxy phase 1
   first (proves inference through the proxy at all), then this (hardens delivery) — but 1c here
   and inference-proxy's 1c are the *same* guest-runner file. Do they merge into one task? (§11.)

## 11. Validation plan

The §9 gate is the plan; its signal is binary and baseline-free: run one real custody placement
and grep the guest env / cmdline / host ps / artifacts for the grant value (zero hits), confirm
`/run/rooms/secrets.env` is gone post-workload, and confirm the receipt says
`vsock`/`OneShot:true`. If the grant appears in `/proc/<pid>/environ`, the SendEnv path is still
live and the feature is not done regardless of what the receipt claims. The honesty check is
part of the gate, not a separate step: receipt channel must equal the channel the grep proves
ran.
