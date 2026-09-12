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
This proves connection and session continuation, not task throughput.
A separate real interruption test reached a foreground shell command, requested cancellation,
received an `interrupted` turn, collected bridge exit 130 and app-server exit 0, and observed
that the post-sleep marker was still absent after the command's original duration had elapsed.
The first cancellation probe attempted a write outside its configured workspace and did not
reach the command; the successful probe used its own working directory.

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

The workflow binary SHA-256 was
`184f706a7266361825470c816349d82f15ed8db5d7fd435ca6f9c8d04fe1c325`.
It was built during development before the final native-trace split and conservative orphan
reservation change. The later Codex interruption test exercised the committed bridge; protocol
and Go checks cover the final changes. This is mechanism-specific evidence, not a claim that
one full live workflow ran on the final reviewed Workbench head.

## Frozen-build follow-up

Both full workflows subsequently passed using a clean build of Workbench
`5b96aa0b9b7ffdbd6f71945028c0c71a202ac911`, binary SHA256
`b1fdc153f71d7a5e89c8a5d8326781f5542ec02dae6f61d06bbc89c3b7ef69d7`:

- Codex: [draft sandbox PR #18](https://github.com/itsHabib/fleet-demo-sandbox/pull/18),
  exact head `b35cdd5d5ea2bb43be6dbf41c1935a4850921238`, with
  [independent verification](https://github.com/itsHabib/fleet-demo-sandbox/pull/18#issuecomment-5636832461).
- Claude: [draft sandbox PR #19](https://github.com/itsHabib/fleet-demo-sandbox/pull/19),
  exact head `1de2da1dfd1ae6662e2f481acc1006448a2bfaaa`, with
  [independent verification](https://github.com/itsHabib/fleet-demo-sandbox/pull/19#issuecomment-5636972870).

Each run completed the dependency question, handoff, retained dirty document, resumed
author session, draft PR and independent clean-checkout Fleet verify/pass receipt.
Each used six Go-watcher-scheduled attempts without post-start intervention. All addresses
were stopped by the lead, provider terminal results and bridge exits collected, then the
watcher stopped. Codex hook trust was configured before startup using the exact sandbox
definitions. Claude used the existing provider login normally with disposable worktrees,
Fleet and Org roots. A test-only PATH adapter executed the installed Claude CLI with
`--setting-sources project,local`, empty strict MCP config and bounded tools. No credentials
were copied and no global settings were edited. The SDK control protocol was unchanged.

Normal foreground-command interruption passed for both providers on that bridge: actual
command start, provider terminal interruption, bridge exit 130 and no delayed finish marker
after the original command deadline. Codex also recorded app-server exit 0. Claude reported
`result/error_during_execution` on interruption; the bridge correctly retained the interrupted
state. This does not prove forced descendant quiescence.

The final panel then exposed two additional invariant gaps: a requested Claude resume ID
could stand in for missing observed identity, and relative state roots changed meaning in a
provider's working directory. The subsequent patch requires observed Claude identity and
refuses relative Fleet/Org roots before provider invocation. Fourteen protocol fixtures and
the state-root launch regression cover those changes; the full workflows above predate them.
Final-head live smoke/cancellation and CI evidence is recorded on PR #318 without another
panel round. Remaining review deferrals are in FOLLOWUPS.md.

No general production readiness, Windows provider qualification or controlled cost advantage
is established by these bounded document/Git/session-identity workflows.

## Bounded pre-turn recovery

A subsequent macOS probe used the ordinary `/opt/homebrew/bin/codex` npm installation
(Codex 0.153.4) without selecting a native executable manually. The Go watcher ran a
fresh turn, then the controller temporarily moved only that probe's private rollout
file. The next watcher attempt resumed the actual ID and received native
`thread/resume` rejection. Its state retained `provider_terminal: false`, recorded
`turn_may_have_been_sent: false`, and joined the owner's armed, no-fork, exec/exit proof.
An explicit fresh retry completed with a different actual session and preserved a
dirty marker file. The rollout was restored and the address/watcher stopped. This is
controlled failure injection, not a zero-intervention workload. Private evidence is
under `provider-20260911/quiescence-live`; the exact published-head repeats are on #318.

Kernel regressions include a detached child that survives its parent: observation
records the fork and refuses quiescence. Failed observation also refuses proof.
Protocol/watcher fixtures reject missing/ambiguous phase, possibly dispatched turns,
stale attempt evidence and arbitrary wrappers. The proof only establishes that an
observed native process exited without forking. Arbitrary wrappers, observed forks,
unsupported platforms and ambiguous process/turn evidence remain reserved; no general
descendant cleanup or Windows provider qualification is claimed.
