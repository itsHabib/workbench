# Bounded approval example

An approval should name an already-recorded action, not carry enough authority
to invent a new one.

For a parked Gate run, the notification therefore carries only two pieces of
decision vocabulary. Within Slack's `actions` array, the only decision-relevant
fields are:

```json
{
  "action_id": "approve",
  "value": "esc_<parked-escalation-id>"
}
```

That is one element of the enclosing `block_actions` event, not the whole
callback body — everything around it (`type`, `user`, `response_url`) is
transport metadata the decision does not draw on. A Block button substitutes
`"block"` as the `action_id`; the two are the `ActionApprove` / `ActionBlock`
constants in [`contracts/escalation`](../contracts/escalation/escalation.go).

The resolution service verifies the callback identity **and** that the caller is
authorized to resolve — authentication and authorization are separate checks, so
an authentic but unlisted user is refused before any lookup. It then finds the
live grant already attached to that escalation and asks Gate to resolve that
exact park. Gate rechecks that the park is still open and that the grant remains
live before recording the judgment. A stale or replayed approval cannot select a
new run, widen the grant, or change the proposed action.

This keeps the button intentionally small: it is a human decision over durable
evidence, not a portable merge credential.

See also: [features/escalation-plane/escalate-serve.md](features/escalation-plane/escalate-serve.md)
for the resolution service's security model.
