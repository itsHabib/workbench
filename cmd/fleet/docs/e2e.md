# End-to-end test of a fleet, with a sandbox of your choosing

An agent guide for proving a Fleet build works with real sessions: two bucket leads under one
overall lead, two worker seats, one verifier seat, one contested resource, and a scorecard built
from records. It generalizes the four runs recorded in `itsHabib/fleet-demo-sandbox` (that
repository is one sandbox; any small private repository with a 40-second "bench" script works).
The scripts under `cmd/fleet/e2e/` are the ones those runs used.

## What a passing run shows

| observation | evidence |
|---|---|
| a fresh session in a seat knows its role, seat and assignment from the hook alone | its `[fleet]` lines; `sessions/<id>.json` |
| a worker asks its lead and a later session resumes with the answer, no operator | mail records with matching ids |
| two workers contend for one resource; the refused one reports up; the overall lead orders; no overlap | `fleet leases` over time; the refusal text in the report; the order and its relay |
| a verifier at the exact head emits a receipt; the row reads `done` | `receipts/<sha>.verify.json`; `fleet work` |
| escalation goes one hop up; nothing sideways; nothing to the operator | the scorecard's edge counts |
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
6. If sessions are made by mail: a delivery config (`deliver.json`: `{"<address>": {"cwd", "cmd"}}`,
   prompt placed right after `-p`; `--allowedTools` is variadic and swallows a trailing prompt)
   and a delivery process. Until the watcher's own launcher lands, use `e2e/mail-poll.sh`.
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

One headless overall-lead tick that sends one `order` per child:

```sh
(cd ~/dev/<repo>-lead && claude -p "<attach, claim the run item, send <run>-a-start-1 to supervisor:<name>-a and <run>-b-start-1 to supervisor:<name>-b, checkpoint, release; do not loop>" \
   --model opus --max-turns 40 --output-format json \
   --allowedTools 'Read,Skill,Bash(org *),Bash(<fleet binary> *),Bash(gh *),Bash(git *),Bash(cat *),Bash(ls *)' < /dev/null)
```

Then `e2e/mail-poll.sh <deliver.json> <fleet binary> <state dir> 10 45 <tenant>` in the
background, and watch `fleet mail --for <address>`, `fleet leases`, the PR list, and the poll
log. A loop printing each new file under `mail/` is the live view.


## Desktop `/loop` as the delivery process

If you would rather watch one visible session than run a background poller, make a desktop
session the launcher. Open it in a directory that carries no role (never a seat or a lead
directory), and give it `/loop 2m` over this instruction: read every configured address's
unread, not-yet-launched mail from the v2 store (`e2e/mail-poll.sh` is the reference; the
`launched.txt` set is the memory), and for each address with such mail and no live session in its
directory, start one headless `claude -p` from a subshell in that directory with the mail lines
in the prompt, log the launch, and end the turn. It sends no mail and takes no seat. Leads,
workers and the verifier stay headless and disposable; the loop session is the only long-lived
one, and it is not a role. On Windows the launch commands need a shell that runs
`scripts/bench.sh` (Git Bash), the hook path is the installed one, and paths in `deliver.json`
are that machine's; nothing from another machine's state applies.

## Stop and score

Stop when both tasks carry `verify pass`. Then:

```sh
python3 cmd/fleet/e2e/run-metrics.py <run-id> <since-iso-utc> --repo <owner>/<repo> --roles supervisor:<name>,supervisor:<name>-a,supervisor:<name>-b --seats <repo>-author-1,<repo>-author-2,<repo>-verifier-1
```

The scorecard counts mail by kind and edge, joins relays by id with per-hop latency, counts
watcher or poller launches and their latency, and sums sessions and tokens from the transcripts.
Commit it under `runs/` in the sandbox and append a section to its record with every friction:
reproducer, expected, actual, owner.

## Codex variant

Same contract, same directories, same scorecard. Change the launch commands in `deliver.json`
to the Codex launcher, and the hook path is `fleet hook codex`; `fleet role` already projected the
Codex config into each seat. Diff the two scorecards; differences in edges, relay depth or
refusals are the finding.

## Chaos layer

Deterministic faults that turn contract boundaries into tests: a CI workflow in the sandbox that
fails when the task file lacks the unit line (`checks`); a scripted review comment on each draft
(`reviews`); a worker killed while holding the bench (orphaned resources and `--takeover`); a push
after a verify row is dispatched (the verifier's exact-head refusal). Each becomes a scorecard row.
