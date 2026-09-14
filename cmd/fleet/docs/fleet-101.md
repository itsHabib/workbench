# Fleet 101

Fleet helps agents work together without making the operator relay every question.
It records who is working where, who is accountable for each task, messages between
agents, and evidence attached to a commit. A lead uses those records to answer questions,
notice stalled work and decide what should happen next.

Fleet is part of [Workbench](../../../docs/workbench-101.md): a family of tools for
agent work. You can use Fleet without adopting the whole family. Start with one
accountable lead and one worker in separate worktrees. Add an independent verifier
when the task needs one. More leads, special roles and exclusive resources are choices
for the work you have, not setup prerequisites.

## Choose your path

| You want to… | Read or do this |
|---|---|
| Understand one task from assignment to evidence | Follow the example below |
| Install on a fresh machine | [Install Fleet](install.md), then [run a fleet](run-a-fleet.md) |
| Use agents already open on your desktop | Follow the desktop path in [run a fleet](run-a-fleet.md) |
| Start agents from assignments or mail | Configure the Go watcher in [headless](headless.md) |
| Investigate leases, delivery and formal models | [Runtime and model reference](fleet-reference.md), a dated source snapshot |
| Check what has actually been exercised | [Provider validation](provider-runtime-validation.md) and [e2e](e2e.md) |

## The few things you need to name

A **role** describes responsibilities: task owner, reviewer or lead, for example.
Its card is editable prose. **Org** is an optional registry for reading those cards
and displaying parent relationships; Fleet does not need Org's executable or hierarchy.
Fleet's directory bindings happen to live in `$ORG_STATE/roles.map`. That shared path
name does not make Org registration a prerequisite.

A **seat** is a named worktree prepared for a worker. Its address identifies a mailbox;
two workers with the same role still have different seat addresses. A **work row** names
a branch, requested result and accountable lead (`--for`). A **handoff** says what the
agent learned and what comes next. **Mail** asks someone to act or answer. A **receipt**
records a checker's conclusion at an exact commit.

Hooks provide observed session activity. Agents author assignments, messages, handoffs
and receipts. A row reading `done` means the required receipt says pass at that head;
it does not mean Fleet ran the tests or proved the checker independent. The lead still
compares the evidence with the task's acceptance.

## One useful task

Suppose a parser reports a timeout in seconds while its caller expects milliseconds.
The outcome is a tested fix and a draft PR. The lead is `supervisor:demo`; the prepared
owner seat is `example-author-1`. These names illustrate the example: substitute the
names printed by your own setup. The root checkout stays on `main` and has no Fleet role.

First use [installation](install.md#configure-one-repository) to prepare directories,
then start a supported session in the lead directory. Confirm its actual `[fleet]`
startup identity. For a first run, desktop sessions are sufficient. Headless delivery
additionally needs a configured address and provider; dispatch alone does not install
or authenticate a provider.

Write the brief where the worker will read it:

> Fix the seconds/milliseconds mismatch. Reproduce it, add a focused regression,
> and run repository checks. Lead: supervisor:demo. Produce a draft PR with the exact
> tested commit and test result. Ask the caller's owner about ambiguous behavior.
> Use the agreed model budget. Stop at the draft and verification; no merge or deployment.

From the lead's own session, create the task branch at the intended base first
(`main` in this example). Dispatch requires an existing local or fetched branch:

```sh
git branch fix/timeout-units main
fleet dispatch fix/timeout-units --as implementation --for supervisor:demo \
  --slot example-author-1 --brief 'Fix the timeout unit mismatch; regression and repo checks; draft PR only. Ask about ambiguous caller behavior.'
```

The seat identifies the repository. The brief accompanies the assignment. Open the
worker session in that seat for desktop execution; a configured headless seat becomes
eligible on the next watcher fold. Confirm that startup names the assigned branch and
brief before editing. The lead can inspect another tree with `git -C`; it should not
enter that directory and act as its occupant.

### A question is ordinary work

The worker finds that the caller contract is unclear. From its own live session it sends:

```sh
fleet send supervisor:demo --kind question --subject 'Timeout units' \
  --body 'For fix/timeout-units: should the parser return milliseconds? The caller currently multiplies by 1000.'
```

Fleet returns a message ID. The lead reads and answers from its own session:

```sh
fleet mail --unacked
fleet send example-author-1 --kind answer --subject 'Keep milliseconds' \
  --body 'For fix/timeout-units: return milliseconds and remove the caller conversion. Cover both with the regression.'
fleet ack QUESTION_ID
```

Replace `QUESTION_ID` with the ID read from mail. The worker likewise reads and acknowledges
the answer. In real work reply to the message's `from_address` (or the assignment's
`reply_to`), rather than guessing an address from a role name. Acknowledgement means
received, not resolved. New messages get new IDs; an intentional retry reuses the returned
ID and exactly the same payload. See [mail](mail.md).

Workers may ask relevant peers directly within their tenant. The named lead retains
accountability; scope, spend and authority questions go to whoever can decide them.
An Org parent does not restrict communication. For overdue unacknowledged questions,
the watcher chooses the latest matching dispatch's `for`, otherwise configured `LATE_TO`.
With neither it records `late-no-recipient`; configuring an Org parent does not create
that route.

### Leave evidence that survives the session

The owner implements the fix, runs the agreed checks and opens the authorized draft.
Before handing it off, it records the actual result:

```sh
fleet handoff fix/timeout-units 'Draft PR and tested commit: insert actual links/SHA. Regression result: insert observed result. Next: independent verification against the brief.'
```

That command writes a checkpoint. Read checkpoints with `fleet inspect <address>`;
`handoff` has no read mode. A replacement first reads the current branch, dirty files,
assignment, mail and handoff, then continues the same work. It preserves unfinished files.

When independence is part of acceptance, a separate verifier checks the exact proposed
head and records the agreed receipt from its own live session and clean checkout using
[the receipt history](../README.md#receipts-keep-their-history). The lead checks both evidence and provenance,
then queries the agreed kind. Here the row uses `--as implementation`, so its completion
receipt must use `implementation` too. A `verify` receipt would be a different kind and
would not complete this row. The kind names the evidence, not who is allowed to record it.
After actually running the checks, the verifier records
its observed result (replace the SHA and description with the real evidence):

```sh
fleet receipt ACTUAL_COMMIT_SHA implementation pass "Actual checks run and observed results"
fleet done ACTUAL_COMMIT_SHA --kind implementation
```

A receiving assignment can optionally demand named receipts before it is queued:
`fleet request ... --as verify --head FULL_SHA --requires unit,integration`.
See the [Relay example](../examples/relay-boundary/README.md) for missing, failed,
stale and contradictory evidence, plus restart recovery. This retains a handoff's
input evidence; ordinary work does not acquire mandatory phases.

A missing receipt is not pass. Nor is an exit code from the provider, an agent's “done”
message, or an old receipt for a different commit. Fleet does not authorize a merge;
repository review rules and Gate govern that next step. The example ends at a checked draft.

## When work stops moving

Start with observations:

```sh
fleet work
fleet board
fleet watch status
fleet inspect example-author-1
```

`unoccupied` means no current branch holder; read the handoff before deciding it was
abandoned. `late` means a due time elapsed, not that a process died. A refusal names the
condition to investigate. Do useful independent work while waiting, leave a handoff when
needed, and let the existing desktop loop or Go watcher provide the next wakeup.

Do not erase a launch record to make an address appear free. At the source revision
checked for this guide (`10b066cac0bb959ca3dfa7dc2d77886eed277e78`, 2026-09-13), provider-command
construction can fail after publishing `starting`, stranding the address before spawn.
This remains a runtime defect for the reliability owner. Ambiguous attempts that might
have started must remain reserved until supported evidence permits recovery. The
[headless recovery guide](headless.md) explains the available commands and their refusals.

The old lost-stdin-request race was fixed by #330: Fleet now writes a private request
file before starting Node and passes its path in argv. A bridge exit alone still does
not establish release. Matching provider-terminal evidence, matching never-started evidence,
or the supported bounded quiescence proof matters. It is possible for a bridge to exit
successfully while cleanup remains pending.

## What the protection means

Fleet gates recognized tool admissions. It does not lock the filesystem. At the checked
revision, named file-edit tools and recognized Git/GitHub shell operations receive branch
lease checks. Ordinary shell writes such as `printf x > file.txt`, scripts and generators
can run without that branch-lease check. Tools outside the hook matcher bypass it entirely.
A lease also does not stop a child process that was already running. Keep separate worktrees
and respect live occupants and exclusive resources.

A configured or trusted hook is not proof that it ran. Inspect configuration, then check
actual events in the harness and build you intend to use. [Provider validation](provider-runtime-validation.md)
records bounded macOS runs with Claude and Codex, including limitations and interventions.
[Windows hook validation](https://github.com/itsHabib/workbench/issues/324) remains a separate
qualification question. Fixture tests and a completed demonstration do not establish all
platforms, arbitrary write protection, or unattended reliability.

## Check your understanding

- A worker needs an API fact. Must it ask the operator? No: ask the relevant peer; keep
  the lead informed when the answer changes the task's scope or acceptance.
- The provider exited zero. Is the task verified? No: check the exact-head evidence.
- A receipt says pass. Did Fleet prove the tests ran? No: it records the author's claim
  and provenance; the checker and lead establish that the evidence supports acceptance.
- The branch holder died. Can an old child still write? Yes; admission checks do not
  contain existing processes.
- Do you need Org and a lead-of-leads to begin? No. Use responsibilities and contacts
  suited to one real task, then add structure when it helps.

To try it, continue with [install](install.md) and [run a fleet](run-a-fleet.md).
To understand why the safeguards are bounded, use the optional
[runtime and model reference](fleet-reference.md).

A subsequent private check on 2026-09-13 used installed `10b066c` with all six Codex
hooks trusted. A holder's `apply_patch` was admitted; a contender's `apply_patch`
was refused with `lease-held` while the holder was alive, and the file stayed unchanged
by the contender. Both sessions ended terminally. This demonstrates that specific
live admission path, not generic shell-write protection or surviving-child containment.
The runtime owner retains its raw evidence; see the [teaching validation record](fleet-teaching-validation.md).

## Source and evidence map

Commands and boundaries were checked against source at the revision named above:
[dispatch](../internal/verbs/work.go), [mail](../internal/verbs/mail.go),
[handoffs](../internal/verbs/keys.go), [receipts](../internal/verbs/receipts.go),
[admission](../internal/fleet/policy.go), [launch and release](../internal/watch/runtime.go),
and [lateness routing](../internal/watch/late.go). These links follow the checkout;
the dated [runtime reference](fleet-reference.md) retains the old source locations.
