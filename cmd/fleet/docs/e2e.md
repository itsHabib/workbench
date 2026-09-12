# End-to-end test of a fleet, with a sandbox of your choosing

An agent guide for proving a Fleet build works with real sessions: two bucket leads under one
overall lead, two worker seats, one verifier seat, one contested resource, and a scorecard built
from records. It generalizes the four runs recorded in `itsHabib/fleet-demo-sandbox` (that
repository is one sandbox; any small private repository with a 40-second "bench" script works).
The scripts under `cmd/fleet/e2e/` are the ones those runs used.

## Start with one complete path

For a runtime or continuity change, begin with one lead, one worker and one verifier in
private state and fresh worktrees. A useful bounded task is:

1. The lead dispatches a brief once, then checkpoints and yields.
2. The worker leaves a dirty draft and a handoff, asks for a missing fact, then yields.
3. The lead answers and yields. Mail wakes a replacement worker, which preserves the draft,
   completes the task and opens a draft PR.
4. The lead forwards the exact commit to the verifier and yields. The verifier checks that
   head in its own worktree, correlates retained draft/session evidence, and emits a receipt.
5. Remove delivery entries to stop new launches; let active sessions finish, then stop the
   Go watcher. Record wall time, reported model cost, manual rescues and any shell polling.

Use [run-a-fleet.md](run-a-fleet.md) for roles, messages and checkpoints, and
[headless.md](headless.md) for the runtime. Save the first handoff/draft before replacement
updates them: handoffs retain the latest context, not a history. A deliberate yield/resume
proves continuity across sessions; it is not a crash-recovery or contention test. Expand to
the multi-lead/resource scenario below only when testing those behaviors.

## What a passing run shows

| observation | evidence |
|---|---|
| a fresh session in a seat knows its role, seat and assignment from the hook alone | its `[fleet]` lines; `sessions/<id>.json` |
| a worker asks its lead and a later session resumes with the answer, no operator | mail records with matching ids |
| two workers contend for one resource; the refused one reports up; the overall lead orders; no overlap | `fleet leases` over time; the refusal text in the report; the order and its relay |
| a verifier at the exact head emits a receipt; the row reads `done` | `receipts/<sha>.verify.json`; `fleet work` |
| questions reach the relevant peer without operator relay | message edges and operator rescues |
| a stale order is refused on the evidence of the row | the lead's report back |

## Choose the sandbox

A private repository with: a `scripts/bench.sh` that prints a start line, a result line and an
end line and sleeps 40 seconds (the exclusive resource stand-in), a `tasks/` directory, and a
`docs/RUN-CONTRACT.md` you write per run. Seven directories beside its checkout: `-lead`,
`-lead-a`, `-lead-b`, `-author-1`, `-author-2`, `-verifier-1` (plus `-finisher-1` if you test
handoffs). Stand them up per `run-a-fleet.md`.

## Preconditions

1. One installed hook build; for verbs not yet on `main`, a side binary named explicitly in every
   prompt (`go build -o ~/.fleet/bin/fleet.<tag> ./cmd/fleet`).
2. Charters for the three leads; `roles.map` lines for all seven directories; **no repository root
   roled**.
3. The `task-supervisor` skill and the lane cards at the contract revision.
4. **No idle holders**: `fleet sessions` shows no live session in any lead directory or seat.
   A desktop session idle in a roled directory reads as live and blocks delivery.
5. Exactly one `fleet watch` on the machine.
6. If sessions are made by mail: a delivery config (`deliver.json`: `{"<address>": {"cwd", "provider"}}`,
   optional `model`, `prompt` and positive `every` duration per the provider guide)
   in `$FLEET_STATE`. Use the current Go watcher; see [headless.md](headless.md).
7. The slow-command gate: after five 40-second runs, `bash scripts/bench.sh` needs the
   `FLEET_ALLOW_SLOW=<rule-slug>` prefix (the slug of the rule it trips) and the seat's allow
   list needs that rule's own `Bash(FLEET_ALLOW_SLOW=<rule-slug>:*)` — never a wildcard.

## Reset between runs

From a session that will not act as a lead, in subshells, never `cd`ing into a seat:

```sh
(cd ~/dev/<repo> && for b in $(git branch --list 'task/*' | tr -d ' '); do fleet undispatch $b; done
   for s in author-1 author-2 verifier-1; do fleet unassign <repo>-$s; done
   git branch task/<run>-a main && git branch task/<run>-b main && git push origin task/<run>-a task/<run>-b)
for d in lead lead-a lead-b author-1 author-2 verifier-1; do
  git -C ~/dev/<repo>-$d fetch -q origin main && git -C ~/dev/<repo>-$d checkout -q --detach origin/main
done
```

Commit the run's contract so every worktree sees it. Force the contention: both bucket leads
dispatch in their first tick, and each worker must ask its lead a question by mail before it may
touch the bench.

## Kick off and watch

Use the Go runtime from [headless.md](headless.md). Configure a recurring lead `every` and
`prompt` when reproducing the desktop loop behavior. Workers wake from their current
assignment with a brief or from mail; no second order after dispatch is required. One Go
watcher handles both paths, with no default lifetime or worker turn cap. Keep the operator's
explicit budget in the selected harness/run configuration.

Watch `fleet work`, `fleet leases`, the PR list, `watch/observed.jsonl` and the per-attempt exit
records in `watch/delivery/`. A process start is not proof that its task progressed; an exit
is not a receipt. Exercise a worker that exits before emitting hooks, one that exits nonzero,
and two overlapping folds receiving work for the same address. Verify retained files and
that a lead gets its next periodic tick without an operator wakeup.

## Desktop comparison

Keep desktop loops and native messaging as the desktop baseline. Do not make a desktop agent
scan mailbox files or maintain a `launched.txt` set to emulate the headless runtime. Compare
that baseline with periodic lead wakeups in Go, then with mail-only wakeups in the same Go
runtime. Use comparable real work and acceptance. Historical sandbox runs changed several
variables at once and do not establish that mail-only execution is cheaper or more reliable.

## Stop and score

Stop when both tasks carry `verify pass`. Then:

```sh
python3 cmd/fleet/e2e/run-metrics.py <run-id> <since-iso-utc> --repo <owner>/<repo> --roles supervisor:<name>,supervisor:<name>-a,supervisor:<name>-b --seats <repo>-author-1,<repo>-author-2,<repo>-verifier-1
```

The scorecard counts mail by kind and edge, joins relays by id with per-hop latency, counts
watcher launches and their latency (retained historical poller logs are still readable), and sums sessions and tokens from the transcripts.
Commit it under `runs/` in the sandbox and append a section to its record with every friction:
reproducer, expected, actual, owner.

## Codex variant

Keep the contract and directories, set `provider: "codex"` in `deliver.json` (the hook
path is `fleet hook codex`; `fleet role` already projected the Codex config into each seat), and
verify a fresh session there sees its role and hooks before dispatching. The scorecard's mail,
receipt and launch counts apply as they are; its session and token reader is Claude-only and it
counts no refusals, so collect Codex sessions, usage and refusals separately before comparing.
This is an unproved variant until it has run: nothing so far is Windows or Codex acceptance.

## Chaos layer

Deterministic faults that turn contract boundaries into tests: a CI workflow in the sandbox that
fails when the task file lacks the unit line (`checks`); a scripted review comment on each draft
(`reviews`); a worker killed while holding the bench (orphaned resources and `--takeover`); a push
after a verify row is dispatched (the verifier's exact-head refusal). Each becomes a scorecard row.
