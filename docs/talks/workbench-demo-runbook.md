# Workbench talk: run of show and demo runbook

**Target:** a 12-minute talk with a 5-minute live path.  
**Thesis:** agents become operationally useful when doing, judging, authority,
and explanation are separate, joined by inspectable artifacts rather than one
privileged call stack.

## The story in 90 seconds

The failure mode is not that agents cannot write code. It is that the usual
agent loop collapses five different responsibilities into one process: it does
the work, decides whether the work is good, assumes authority to act, records
its own success, and explains itself afterward.

Workbench separates those responsibilities:

| Plane | Job | Demo incumbent |
|---|---|---|
| State | remembers typed, attributable facts | Ship run/receipt plus Gate's hash-chained log |
| Execution | does work; does not judge itself | Work Driver |
| Verification | judges evidence through a deterministic floor and escalate-only ladder | Gate, triage, tracelens |
| Capability | bounds effects by scope, TTL, tier, and review cycles | operator-minted Gate grant; custody grant |
| Observability | explains and pushes derived state without becoming a decision-writer | Flare and Slack |

Composition is the thin policy layer that connects the planes. The important
boundary is visible in the demo: Flare may paint Approve and Block buttons, but
it cannot resolve a park. The signed callback enters through `escalate serve`,
which shells Gate's public `resolve` seam. The tools share contracts and
artifacts, not one another's decision code.

Talk line: **"Execution can propose. Verification can judge. Capability says
how far the judgment may act. State remembers why. Observability tells me when
I am needed."**

## Recommended order and timing

| Time | Beat |
|---:|---|
| 0:00–1:30 | Problem: one agent process is usually worker, judge, root, database, and narrator |
| 1:30–3:00 | Five-plane model and the artifact boundary |
| 3:00–3:45 | Work Driver proof: PR #162, normal engine, 94-step healthy trace, merged commit `3bde397` |
| 3:45–8:45 | Live park-to-resolution path |
| 8:45–10:15 | Custody/Jira: the same capability idea applied to credentials |
| 10:15–11:30 | What is proven versus still a POC |
| 11:30–12:00 | Close: authority is minted, never inferred |

Do not live-code. Start with the Work Driver run already complete, then spend
the live-demo budget on the human decision loop.

## Demo desk layout

Arrange four surfaces before the audience arrives:

1. **Terminal A — Execution/Verification:** driver status, then the Gate command.
2. **Slack — Observability:** the Flare card and Approve/Block buttons.
3. **Terminal B — ingress:** `escalate serve` log and ngrok request inspector.
4. **Terminal C — State:** `gate next` before and after, then `gate audit`.

Use a dedicated Slack channel with no employer names, ticket keys, URLs, or
message history visible. Disable desktop notification previews.

## Exact live sequence

All paths below are PowerShell paths. Set these once in every terminal:

```powershell
$workbench = '<absolute path to the clean Workbench checkout>'
$gateState = '<absolute path to the operator-managed Gate state>'
$gateKeys = '<absolute path to the sibling Gate key directory>'
$env:GATE_STATE = $gateState
$env:GATE_KEY = $gateKeys
```

### 1. Establish that Execution already worked

Show, do not rerun, the completed normal Work Driver proof:

```powershell
Set-Location '<absolute path to the clean Ship checkout>'
pnpm --filter @ship/cli exec tsx src/bin.ts driver status drv_01KYNDB4VDGDEG0N1006W9AG0C --json
tracelens ship -json wf_01KYNDJCAF4NT50K0HGYZN4Y22
gh pr view 162 -R itsHabib/workbench --json state,mergeCommit,title,url
```

Expected screen:

- driver terminal with the stream merged;
- tracelens healthy after 94 steps;
- PR #162 merged as `3bde397`.

Narration: **"That was the normal driver engine—not an orchestration shortcut.
Execution implemented, reviewers reviewed, Gate bounded the merge, and the
trace remained reconstructable."**

Fallback: show the GitHub PR page and the saved driver/workflow/commit identifiers
above. Never claim the driver is being rerun live.

### 2. Show the park in authoritative State

Use a real, already-parked content escalation for the demo PR:

```powershell
gate next -state $gateState
gate audit -state $gateState
```

Expected screen: one `awaiting judgment` row with the demo repository, PR,
question, run, and grant; then `chain intact`.

If no content park exists, do **not** pretend a clean Gate result is a park.
Pivot to the captured park-to-resolution transcript in
`docs/features/escalation-plane/EVIDENCE-escalation-plane-poc.md`. The
offline seed in `EVIDENCE-escalate-e2e-phase3.md` is a rehearsal-only fallback
that creates its own one-hour demo grant; the operator must explicitly choose
to run it. The talk agent must not run it or mint authority.

### 3. Start the signed ingress

The operator provides these values in the process environment. Do not paste
them into the repository, shell history, slides, or screen share:

```powershell
$env:SLACK_SIGNING_SECRET = '<operator-provided>'
$env:ESCALATE_ALLOWED_SLACK_USERS = '<operator Slack Uxxxx id>'
escalate serve -addr 127.0.0.1:8099 -gate gate -state $gateState
```

Expected screen:

```text
escalate serve: listening on 127.0.0.1:8099 (..., 1 authorized user(s))
```

Safety property worth saying aloud: the process refuses to start without both
callback authentication and an immutable-user-id allowlist.

### 4. Start the tunnel

In Terminal B, after the operator has authenticated ngrok:

```powershell
ngrok http 8099
```

Set the Slack app's Interactivity Request URL to the displayed HTTPS URL. Do
this before the talk if the URL is stable for the session.

Expected screen: ngrok online, forwarding HTTPS to
`http://localhost:8099`, with no request errors.

### 5. Route the Gate park through Flare

The demo Slack channel in Flare's routes file must contain:

```json
{
  "type": "slack",
  "token": "<operator-managed bot token>",
  "channel": "<demo channel id>",
  "resolve_actions": true
}
```

Run one bounded sweep:

```powershell
flare sweep -config '<operator routes.json>'
```

Expected screen in Slack:

- a current 2026 timestamp, never `Jan 1`;
- a brief beginning with the required human decision;
- `View PR`, `Approve`, and `Block` in one actions row;
- no buttons and no **Your call** headline for a Ship mechanism/runtime park.

If the Slack card is already present, show it and skip the sweep to avoid
dedupe theater. If delivery fails, show the local rendered-card test and the
captured evidence; do not weaken Flare's delivery checks.

### 6. Tap Approve or Block

Tap one button from the allowlisted Slack account.

Expected screens:

1. Slack immediately replaces the card with `Recording…` (the callback is
   acknowledged inside Slack's deadline).
2. `escalate serve` logs one accepted decision.
3. Slack updates to the final Gate outcome.

The callback is HMAC-verified with a five-minute replay window. `who` comes from
the verified Slack identity, not a client-settable field. A repeated or
concurrent tap resolves at most once.

### 7. Prove Gate, not Slack, resolved the park

```powershell
gate next -state $gateState
gate audit -state $gateState
```

Expected screen:

- the park has left `awaiting judgment`;
- Approve produces `ready to merge` and an exact commit-pinned `gh pr merge`
  command, or Block produces a blocked outcome;
- the hash chain is intact.

Gate resolution is a dry-run authorization result. It does not merge by itself.
Only run the exact emitted merge command if the operator supplied a live grant,
the outcome is pass, and the talk plan includes the merge.

## Supporting evidence: custody-proxied Jira

Keep this to 90 seconds and use generic labels only.

Narration:

1. **"The same separation applies to identity."** The agent called a loopback
   URL with no vendor credential.
2. Custody injected an operator-held static header and a real corporate Jira
   returned `200`.
3. A write and an out-of-scope API version were rejected `403` before forwarding.
4. A query parameter was initially denied by default; widening it was an
   explicit, logged capability decision.

Safe one-line claim:

> A general local credential broker read a real corporate Jira over the real
> corporate network while the agent never held the token.

Use `docs/talks/custody-demo/production-evidence.md` as the fallback. Never
show the employer host, user profile, ticket key, ticket body, token, or manifest
containing those values. Do not claim Confluence was proven: forwarding worked,
but the upstream returned `403`.

## Dependency audit

| Dependency | Classification | Evidence / action |
|---|---|---|
| Completed normal Work Driver proof | ready | PR #162 merged; driver/workflow ids captured; tracelens passed 94 steps |
| Ship unattended dispatch fix | ready | Ship PR #240 merged; use a clean checkout at or after its merge |
| Gate, Flare, tracelens binaries | ready | installed locally; rebuild from the exact demo head before rehearsal |
| `escalate serve` and Slack action contract | ready | real binary e2e passes signed Approve/Block, authn/authz, stale/forged/replay/double-tap cases |
| Flare receipt time and honest mechanism-park rendering | fixable autonomously | Work Driver task `flare-ship-receipt-truth`; do not rehearse the Ship-park card until its PR lands |
| `escalate` on PATH | fixable autonomously | `go install ./cmd/escalate` from the reviewed demo head |
| Listener on `127.0.0.1:8099` | fixable after operator input | start only after secret + allowlist are present |
| Slack signing secret | requires operator input | process environment only |
| Allowed Slack user ids | requires operator input | comma-separated immutable `U…` ids |
| Flare Slack token/channel/routes file | requires operator input | operator-managed local config; `resolve_actions: true` |
| ngrok authentication and Slack Request URL | requires operator input | config absent locally; operator authenticates and updates app |
| Live Gate grant | requires operator input | operator mints; agent never mints |
| Guaranteed content park on a fresh PR | unsuitable as the sole live plan | Gate may correctly pass; keep a real parked row ready or use captured evidence |
| Live custody corporate-Jira call on stage | unsuitable by default | leaks employer context and depends on corporate network; use genericized evidence |
| Ship database recovery | unsuitable for this talk | unrelated to the human-resolution narrative |

## Preflight checklist

Run this once the day before and again 30 minutes before the talk:

- [ ] Root checkout remains untouched; demo binaries come from clean reviewed heads.
- [ ] `go test ./cmd/flare/... ./cmd/escalate/... ./cmd/gate/...` passes.
- [ ] `go build` or `go install` refreshes `gate`, `flare`, and `escalate`.
- [ ] `Get-Command gate,flare,escalate,ngrok,tracelens` resolves expected binaries.
- [ ] `Get-NetTCPConnection -LocalPort 8099` is empty before serve, listening after.
- [ ] Operator exports `GATE_STATE` and sibling `GATE_KEY` before any Gate command.
- [ ] Operator supplies Slack secret, immutable user allowlist, bot token, and channel.
- [ ] Flare demo channel has `"resolve_actions": true`.
- [ ] ngrok config is valid; tunnel is online; Slack Request URL matches it.
- [ ] A real, unresolved, unexpired content park exists under the live grant.
- [ ] `gate next` shows exactly the park intended for the screen.
- [ ] Slack channel contains no employer or unrelated personal history.
- [ ] Approve and Block are visible only on the resolvable Gate park.
- [ ] Mechanism-failure Ship receipt renders without **Your call** or decision buttons.
- [ ] Fallback evidence files and PR #162 are open in tabs.
- [ ] Screen sharing excludes environment values, config files, and terminals with secrets.

## Recovery plan

| Failure on stage | Recovery |
|---|---|
| Work Driver status store is unavailable | show PR #162 + the captured ids and say the run is already complete |
| Gate passes instead of parking | celebrate the correct result; pivot to the captured real park transcript |
| Flare dedupes the event | show the existing Slack card; dedupe is the feature |
| Slack delivery fails | show the rendered-card test/capture, then continue with `gate next` and the resolution evidence |
| Port 8099 is occupied | identify the process; do not kill an unknown listener; use a rehearsed alternate port and retarget ngrok |
| ngrok is offline | run the signed loopback e2e and state precisely that the public transport hop is mocked |
| Tap is `401` | signing secret or timestamp; do not disable verification |
| Tap is `403` | wrong Slack user id; do not broaden the allowlist on stage |
| Grant expired/refused | stop; ask the operator for a newly minted grant |
| Resolution re-parks | explain that human approval cannot launder missing deterministic evidence; use the Block path or captured successful resolution |
| Merge command is unavailable | stop at `ready to merge`; resolution, not merging, is the demo objective |

## Claims ledger

### Safe to say

- A normal Work Driver run implemented, reviewed, gated, and merged Workbench
  PR #162; tracelens reported a healthy 94-step run.
- Flare is a read-only routing sink; Slack decisions return through a separate
  signed `escalate serve` ingress.
- The local HTTP e2e exercises the real serve binary and the exact Flare/Slack
  action vocabulary, including fail-closed authentication, authorization, and
  replay behavior.
- Gate resolution records provenance and preserves the audit chain.
- Custody brokered a real Jira read without placing the vendor token in the agent.

### Not yet safe to say

- The complete Slack → ngrok → real Gate path has been exercised in this
  rehearsal, until the operator supplies the live values and performs a tap.
- Every Ship `parked` receipt is a human decision. Some are mechanism failures.
- The Flare receipt timestamp/park classification fix is shipped, until its PR
  is reviewed, gated, and merged.
- Custody is a team gateway, compliance boundary, or proof that Confluence works.
- A Slack approval itself merges a PR. It resolves Gate, which emits a
  commit-pinned merge command after re-checking authority.

## Rehearsal record

Fill this after the final preflight:

- **Exercised end to end:** pending real-tap rehearsal.
- **Locally exercised:** real `escalate serve` binary over HTTP; signed
  Approve/Block; verified identity mapping; authn/authz rejection; replay and
  concurrent double-tap protection.
- **Still mocked:** Slack's real signing/POST, public ngrok transport, and the
  e2e harness's Gate implementation.
- **Operator inputs still needed:** Slack signing secret; immutable allowed
  Slack user id(s); Flare bot token/channel/routes path; authenticated ngrok
  config plus Slack Request URL update; one live Gate grant; the intended
  parked escalation.
