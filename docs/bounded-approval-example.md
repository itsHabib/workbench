# Bounded approval example

An approval should name an already-recorded action, not carry enough authority
to invent a new one.

For a parked Gate run, the notification therefore carries only two pieces of
decision vocabulary:

```json
{
  "action_id": "approve",
  "value": "esc_<parked-escalation-id>"
}
```

The resolution service verifies the callback identity, finds the live grant
already attached to that escalation, and asks Gate to resolve that exact park.
Gate then rechecks that the park is still open and that the grant remains live
before recording the judgment. A stale or replayed approval cannot select a new
run, widen the grant, or change the proposed action.

This keeps the button intentionally small: it is a human decision over durable
evidence, not a portable merge credential.
