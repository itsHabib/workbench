# Fleet teaching path: validation and review dispositions

2026-09-13. Documentation follow-up to [#329](https://github.com/itsHabib/workbench/pull/329),
whose operator-read and merge hold remains in force. Runtime source checked:
`10b066cac0bb959ca3dfa7dc2d77886eed277e78`. No runtime implementation changes in this slice.

## Reader journey

Workbench 101 supplies purpose, responsibilities and a reading map. Fleet 101 remains
a focused linked chapter: one task → question/answer → handoff → exact-head evidence.
Installation and run-a-fleet retain the operational commands. The former 1,331-line
Fleet 101 becomes a dated runtime/model reference, with historical transport and current
limitations called out before its source trace. No reader must traverse that reference
before running a task. No claim of a successful newcomer learning trial is made.

## Reconciliation of both independent reviews

The [first review](https://github.com/itsHabib/workbench/pull/329#issuecomment-5647690980)
and [second review](https://github.com/itsHabib/workbench/pull/329#issuecomment-5648028645)
reviewed `d82d65464de303abdebf15ece1095e78a10a83c1`.

| Concern | Disposition |
|---|---|
| Long compulsory first read; no worked task | Short Fleet path and Workbench reading map; old detail retained as optional snapshot |
| Default hierarchy in README/onboarding | One accountable lead and worker; peers communicate directly; extra leads and Org optional |
| Ownership wording omits shell writes | Explicit admission/classifier boundary in Fleet 101, README, overview, minimum and reference |
| Receipts confused with observed truth | Introduce authored records separately; receipt provenance is not proof of tests or independence |
| Old stdin transport and missing request file | Current guide explains #330; historical reference marks old store/trace/self-test as historical |
| Pre-spawn `starting` failure kept for future harness | Name defect and route repair to runtime owner; no routine manual JSON recovery teaching |
| Org parent confused with lateness recipient | Explain newest dispatch `for` / `LATE_TO`, no Org lookup, no-recipient and ack semantics |
| Self-test 7's false release answer | Corrected: insufficient information; other matching release proofs matter |
| Blanket no-Codex qualification and #324 callback plan | Link dated provider ledger; Windows rerun is separate, callbacks conditional |
| Formal counterexamples and model limits | Retain the dated model reference; no model or runtime repair mixed into teaching |

The three panel cycles on #329 are not restarted. Its resolved findings remain resolved;
this is the operator-requested teaching follow-up with independent final review.

## Command validation

A fresh build of `cmd/fleet` was exercised in a disposable repository and private
`FLEET_STATE`, `ORG_STATE` and `CODEX_HOME`. Watcher auto-start and GitHub publication
were disabled. Hook events were supplied synthetically through the real CLI.

The first attempt exposed a missing prerequisite: dispatch refused a nonexistent
`fix/timeout-units` branch. The teaching and run guides now explicitly create or fetch
the intended branch. A complete rerun passed:

1. Pool one author seat; create and bind the lead worktree; register fixture sessions.
2. Create the task branch, dispatch it, and observe the worker's assignment.
3. Send a unit question, read it, answer it, and acknowledge both actual message IDs.
4. Observe the fixture's failing `timeout_ms(2) == 2000` assertion, fix the fixture,
   rerun successfully, and commit it.
5. Write and inspect a handoff; record a fixture receipt and query `done` at that commit.
6. Read work, board and watcher status.

This checks commands and local state transitions. The synthetic checker is not an
independent agent, no real provider was launched by this walkthrough, and no draft PR
was published for the fixture. It does not prove a real learner can complete the path.
The implementing task retains the script and both failed/successful transcripts.

`go test ./cmd/fleet/... ./cmd/org/...` passed on this source. Both `bash cmd/fleet/testdata/run-suite.sh` and its `codex` variant passed all scenarios.
Final independent-review results are recorded on the follow-up PR at its exact head.
No formal model changed, so the earlier model evidence is retained as dated evidence
rather than relabeled as a new run.

## Separate live hook observation

The runtime owner ran a private macOS Codex probe on installed `10b066c` on 2026-09-13:
all six hooks trusted; native Codex execution with `workspace-write`. In two sessions,
the live holder's `apply_patch` was admitted (Pre/Post exit 0); the contender's
`apply_patch` was refused (Pre exit 2, `lease-held`). The marker stayed `holder`.
Both sessions exited 0; elapsed time was 77.68 seconds. A Bash sleep also emitted
Pre/Post events, which proves shell hook events, not shell-write protection.

The teaching owner read the retained result/event record. Raw artifacts remain local
with the runtime owner's task; this summary is not an independently repeated provider run.
It qualifies this admission path, not arbitrary commands, Windows, descendant containment
or unattended operation. The earlier read-only turn likewise did not qualify write protection.

## Residuals and stop boundary

- Known constructor failure can strand `starting` at the checked source revision.
  The runtime owner is implementing its repair separately. Do not claim it fixed until
  the actual implementation/evidence is adopted; genuine crash ambiguity remains distinct.
- Ordinary shell-write coverage and surviving effects remain outside the branch admission
  guarantee. No broader safety assertion is inferred from trusted hooks.
- Windows #324, a real newcomer trial, and comparative usefulness/cost evidence remain
  unestablished by this documentation change.
- Wider Workbench architecture chapters retain their own dates and live-configuration
  qualifications. This slice does not requalify every tool or former experiment.
- Operator read and merge hold remain. No Gate call, merge or live installation is part
  of this teaching task.

## Independent review correction

Review of the first teaching commit (`92becf3`) found that the worked dispatch used
`--as implementation` while its receipt used `verify`. A `done --kind verify` query
could pass while the assigned implementation row stayed dispatched. The revised
example uses `implementation` consistently and explains why the kind must match.
The original transcript is retained; the corrected walkthrough additionally asserts
that `fleet work` reports `done fix/timeout-units/implementation`. That rerun passed.
