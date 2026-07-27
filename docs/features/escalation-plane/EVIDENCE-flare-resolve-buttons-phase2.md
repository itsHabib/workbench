# EVIDENCE — flare resolve buttons (Phase 2)

Phase 2 of `escalate-serve.md`: flare renders **Approve/Block** interactive
buttons on a resolvable parked-escalation card, carrying the shared
`contracts/escalation` action-id vocabulary and the escalation artifact id as the
button value — the exact payload `escalate serve` (Phase 1) already parses. No
ngrok is wired here; that is operator infra (see the setup checklist in
`escalate-serve.md`).

## What changed

- `contracts/escalation` — exported the button action-id vocabulary
  (`ActionApprove` = `"approve"`, `ActionBlock` = `"block"`). Vocabulary only; the
  action→decision mapping stays with the parser (`serve`), keeping the contract a
  pure leaf.
- `cmd/escalate/internal/serve` — `verdictFor` now reads the shared constants
  instead of private copies. No behavior change; the two sides can no longer drift.
- `cmd/flare/internal/config` — new per-channel `resolve_actions` opt-in
  (off by default).
- `cmd/flare/internal/notify` — `actionElements` renders the `View PR` link and,
  when the channel opts in AND the event is a resolvable park (`resolvablePark`:
  kind `escalation` + a non-empty artifact id), the Approve/Block buttons.

## The rendered card (captured, opted-in resolvable park)

A briefed park on a `"resolve_actions": true` channel renders `View PR · Approve ·
Block` in one actions row. Approve carries `action_id:"approve"`, Block
`action_id:"block"`, both with the escalation id as `value` and no `url` (Slack
routes an interactive button to the app's Request URL, not a link):

```json
{
  "type": "actions",
  "elements": [
    { "type": "button", "text": {"type":"plain_text","text":"View PR #137","emoji":true},
      "url": "https://github.com/itsHabib/workbench/pull/137", "style": "primary" },
    { "type": "button", "text": {"type":"plain_text","text":"Approve","emoji":true},
      "action_id": "approve", "value": "esc_9f2a1c", "style": "primary" },
    { "type": "button", "text": {"type":"plain_text","text":"Block","emoji":true},
      "action_id": "block", "value": "esc_9f2a1c", "style": "danger" }
  ]
}
```

The `value` (`esc_9f2a1c`) is the escalation artifact id. `serve`
(`decisionFromPayload`) reads it straight into `ingest.Decision.Escalation`, and
`findGrant` joins it against the parked run's `escalation` id (projected into
`gate next -json` in Phase 1) to read the grant — so a verified tap resolves the
right park with nothing pasted.

## Fail-closed / correctness guards (pinned by tests)

```
=== RUN   TestSlackResolveButtonsRenderOnOptIn
--- PASS: TestSlackResolveButtonsRenderOnOptIn
=== RUN   TestSlackResolveButtonsRequireOptIn
--- PASS: TestSlackResolveButtonsRequireOptIn
=== RUN   TestSlackResolveButtonsOnlyOnResolvableParks
=== RUN   TestSlackResolveButtonsOnlyOnResolvableParks/verdict-escalate
=== RUN   TestSlackResolveButtonsOnlyOnResolvableParks/cursor-alert
=== RUN   TestSlackResolveButtonsOnlyOnResolvableParks/park-missing-id
--- PASS: TestSlackResolveButtonsOnlyOnResolvableParks
ok  github.com/itsHabib/workbench/cmd/flare/internal/notify
```

- **Dark by default.** No buttons unless the channel sets `resolve_actions`, so a
  card never renders a dead tap before the Slack app's Request URL is wired.
- **Resolvable parks only.** A verdict with an escalate decision, a cursor-alert,
  and a park missing its id all reach `SevEscalate` but render NO resolve buttons —
  `gate resolve` would refuse a tap on any of them.
- **Render-only.** flare sets `action_id`/`value` and nothing else; the callback
  targets `escalate serve`, never flare (Amendment 3). flare writes no decision,
  takes no lock, owns no state.

## Boundary law

`contracts/escalation` is a leaf; flare and `escalate` both import it and neither
imports the other. The shared action-id vocabulary is exactly the "share
contracts, not call stacks" pattern — a literal `"approve"`/`"block"` duplicated
on each side would be the implicit, drift-prone contract the package retires. CI's
`hygiene` job enforces the no-cross-import rule.
