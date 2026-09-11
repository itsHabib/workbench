# Provider runtime validation

This is the evidence ledger for the provider lifecycle slice, recorded 2026-09-11.
Fixtures and real provider observations are separate. No production Fleet home was changed.

## Mechanism checks

- Go launch/reservation tests retain one launch per directory, delivered-mail reservation,
  nonzero exits, periodic wakeups, assignment wakeups and process-identity refusal.
- Eleven Node protocol fixtures cover Claude and Codex fresh start, explicit resume,
  mismatched resume ID, premature provider termination and interrupt. Codex additionally
  checks rejected resume without a fresh-thread fallback.
- A binary-level delivery scenario now uses a fake Claude SDK over the real embedded bridge,
  including a second delivery and lateness mail. It does not authenticate a provider.
- Go tests cover retained state tied to work/provider/attempt and cancellation refusing a
  recycled process identity. Race checks cover the Fleet subtree.

## Real provider evidence

The private run root is named `provider-20260911` under the operator's Fleet rehearsal
storage. Raw provider records and credentials are private and are not committed here.

Codex CLI `app-server` completed a fresh smoke turn and then resumed the same actual thread
with a distinct turn ID. Both transport exits were zero. The test used an isolated CODEX_HOME.
This proves connection and session continuation, not task throughput or cancellation.

The full Codex sandbox workflow completed: author dependency question, authored handoff,
retained dirty task file, resumed author, draft PR and independent exact-head receipt.
The result is [draft sandbox PR #17](https://github.com/itsHabib/fleet-demo-sandbox/pull/17)
at exact head `ef06b0b6a547ad7094ce394cb164be3d881fdcd4`, independently verified in a
separate session and detached checkout. The [verification comment](https://github.com/itsHabib/fleet-demo-sandbox/pull/17#issuecomment-5636514796)
records the one-file diff, retained marker, answered unit, same author session across resume,
distinct verifier session, clean checkout and passing Fleet receipt/`done` check.
The Go watcher was the scheduler. The first attempt exposed a clean-home setup prerequisite:
projecting hooks.json does not enable trusted hooks. The author retained its dirty file and
reported absent Fleet session identity instead of fabricating mail. The controller enabled
only the six exact sandbox Fleet hooks using `hooks/list` current hashes and
`config/value/write`, then explicitly woke the author again. Subsequent attempts emitted real
Fleet lifecycle hooks. This intervention must be counted when assessing the run. The controller also resumed the
watcher after its first hosting shell ended; the final provider exits were collected in a
persistent command session. One temporary recurrence produced an extra waiting turn before
mail arrived. These are visible run interventions, not a zero-rescue throughput result.

Claude's initial isolated-home smoke produced an actual SDK session and an authentication
failure (`is_error: true` despite `subtype: success`). Fleet correctly recorded failure and a
nonzero exit. That is failure-path evidence, not live Claude task qualification. Earlier
rehearsals used the configured default Claude login rather than an isolated provider home.

No claim of general production readiness, Windows provider qualification, controlled cost
advantage or both-provider full workflow success is established by these observations.
