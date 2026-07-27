# Phase 3 — dry-observe validation log

`GATE_ENFORCE=true`, **no branch protection** — the workflow posts a `gate`
status but nothing requires it yet (zero merge impact, reversible with
`gh variable set GATE_ENFORCE --body false`).

Goal: prove the `gate` check posts the expected status on real PRs and that the
green path is reachable on a stock hosted runner with the funded
`ANTHROPIC_API_KEY` (the cloud rung is a real API call).

## Cases

1. **clean PR → `gate=success` ("would_merge")** — the green-path-reachable proof.
2. **ladder-block PR → `gate=failure`** — e.g. a red CI check.
3. **stale head (force-push mid-run) → `error`, never success** — the SHA-binding.

## Observations

_(filled in as runs land)_
