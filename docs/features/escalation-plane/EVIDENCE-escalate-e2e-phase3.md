# EVIDENCE / RUNBOOK — phone-tap e2e (Phase 3)

Phase 3 of `escalate-serve.md`: the automated end-to-end proof of the
**ngrok-carried input path** — a signed Slack button tap → `escalate serve` →
gate's resolve seam — plus the fail-closed matrix, and the operator runbook for
the **real phone tap over a live tunnel** that only operator infra + a merge
still gate.

## What is automated (CI, deterministic) — `cmd/escalate/e2e`

The e2e builds the **real `escalate serve` binary** and fires signed Slack
interactive-action callbacks at it over loopback, asserting the whole path a real
tap traverses. Two stand-ins keep it deterministic and offline:

- **Loopback stands in for the ngrok tunnel** — the only hop not exercised is the
  public tunnel itself, which carries the identical bytes.
- **A recording stub gate (`e2e/stubgate`) stands in for the real gate** — it
  implements just the two verbs serve shells (`next -json`, `resolve`) with gate's
  JSON+exit-code contract, and journals every resolve so the test asserts exactly
  what serve drove gate with. The real gate's full resolve→stamp→audit thread was
  already captured against the real binary in
  `EVIDENCE-escalate-serve-phase1.md`; Phase 3 automates the *input* half that
  Phase 1 forged by hand and that Phase 2's flare buttons now generate.

The callback is built from the **shared `contracts/escalation` vocabulary**
(`ActionApprove` / `ActionBlock` + the escalation id as the button value) — the
exact payload flare renders (Phase 2) and Slack POSTs — so the e2e proves the
Phase-2↔serve contract composes, meeting at the contract, never a cross-import.

### Cases (all green — real serve binary, over HTTP)

| Test | Asserts |
|---|---|
| `TestApproveTapResolvesPark` | Approve → 200 `would_merge`; resolve driven with the joined grant, mapped `pass`, and `who` = the verified `@handle (id)` |
| `TestBlockTapRecordsBlock` | Block → 200 `would_block`, decision `block` |
| `TestForgedSignatureRejected` | wrong signing secret → 401, gate never driven |
| `TestStaleTimestampRejected` | valid HMAC but 10-min-old timestamp → 401 on the window |
| `TestUnsignedRejected` | no signature headers → 401 |
| `TestUnauthorizedUserForbidden` | an authentic tap from a user NOT on the allowlist → 403, gate never driven (authn ≠ authz) |
| `TestReplayRefused` | same tap twice → 200 then 409 (park left the inbox), exactly one resolve |
| `TestConcurrentDoubleTapResolvesOnce` | two Approves at once → one 200, one 409, exactly one resolve — never a double-apply |
| `TestForgedWhoIgnored` | a smuggled top-level `"who":"attacker"` is ignored; `who` = the verified identity |
| `TestUnknownActionRejected` | an unknown `action_id` → 400, never a silent resolve |

```
ok  github.com/itsHabib/workbench/cmd/escalate/e2e   (10/10 pass, ~10s)
```

## What remains — operator infra + the real tap

Everything below is the human boundary the run stops at: standing up the Slack app
and the tunnel, then doing a real tap. The **Slack-app + tunnel setup checklist**
lives in `escalate-serve.md` (steps 1–8). Once that is done, the real phone-tap
evidence-gather is:

1. **Point every command at the same keys + ledger — export these FIRST, before
   any `gate` command.** `gate grant` signs with whatever `GATE_KEY` is set *at
   mint time*, and serve must later open the ledger with the SAME keys or gate
   rejects the grant (`grant_bad_signature`); exporting after the mint would sign
   with `defaultKeyDir()` and then read with a different dir. The key dir must also
   be a **sibling** of the state dir — gate's `newEnv` refuses a key dir at or
   beneath `-state` (`cmd/gate/main.go`) — so `~/pers/gate/keys`, not
   `~/pers/gate/state/keys`:

   ```
   export GATE_KEY=~/pers/gate/keys      # sibling of state; set BEFORE any gate command
   export GATE_STATE=~/pers/gate/state   # so the bare gate commands below read this ledger
   ```

2. **Get a parked escalation to tap** — either path:

   **a) Offline demo seed** (no GitHub PR needed; a live `gate gate` needs a PR
   that is awkward to arrange on demand). `TestSeedDemoState` mints its OWN grant
   and park through gate's act-path — the same one Phase 1 used. **That grant
   carries a hard-coded 1-hour TTL**, so it is what the park resolves under, not a
   grant you mint separately; complete the tap within the hour or re-seed. It signs
   with `GATE_DEMO_KEY`, which must be the same sibling key dir:

   ```
   GATE_DEMO_STATE=~/pers/gate/state GATE_DEMO_KEY=~/pers/gate/keys \
     go test ./cmd/gate -run TestSeedDemoState -count=1 -v
   # → DEMO_SEEDED grant=grt_… run=run_… escalation=esc_… code=2   (grant TTL: 1h)
   ```

   **b) Real park** (no expiry pressure). Mint a longer-lived grant yourself and
   let a real gated PR park under it — flare then pages it with live buttons:

   ```
   gate grant -repo itsHabib/workbench -max-tier T2 -ttl 168h -state ~/pers/gate/state
   ```

3. **Run the ingress + tunnel** (from the setup checklist). serve requires both a
   signing secret (authn) and an allowlist of Slack user ids (authz) or it refuses
   to start:

   ```
   SLACK_SIGNING_SECRET=<app signing secret> \
   ESCALATE_ALLOWED_SLACK_USERS=<your Slack Uxxxx id> \
     escalate serve -addr 127.0.0.1:8099 -state ~/pers/gate/state &
   ngrok http 8099            # set the https URL as the Slack app's Request URL
   ```

4. **Tap Approve on your phone** in the Slack card flare posted. `gate resolve`
   follows the non-live path: it records **`would_merge`** and **emits the
   `gh pr merge` command** — it does not merge for you (this is the same outcome
   the automated e2e asserts). So expect the resolution to land and the park to
   move to `ready_to_merge`; then run the emitted merge command yourself to
   complete the merge. Capture (the `-state` flag is required so these read the
   seeded ledger, not a default/relative one):

   ```
   gate next  -state ~/pers/gate/state   # the park is gone (1 → 0), now ready_to_merge
   gate audit -state ~/pers/gate/state   # chain intact; a `resolution` artifact carries the verified who
   ```

5. **Paste the transcript here** under a new `## Real phone tap` section, mirroring
   `EVIDENCE-escalate-serve-phase1.md`'s format.

## Coverage boundary — automated vs. the real tap

| Hop | Automated e2e | Real phone tap |
|---|---|---|
| flare renders the button | proven by `notify` tests (Phase 2) | live Slack card |
| Slack signs + POSTs the callback | modeled: v0-signed callback fixture | real Slack |
| the public tunnel | loopback stand-in | real ngrok |
| serve authenticates + authorizes the tapper | **real serve binary** (403 on unlisted user pinned) | real serve binary |
| serve maps + looks up grant | **real serve binary** | real serve binary |
| gate records the resolution | recording stub (contract-faithful) | **real gate** (keys, chain) |

The only rows the real tap adds are the live Slack sign/POST, the public tunnel,
and the real gate's signed chain — all covered manually once, in Phase 1's
evidence and this runbook. No code stands between here and that tap; only infra.
