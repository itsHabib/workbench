# fleet

## What this is for

An operator running a team of coding agents wants one thing from the machinery
underneath them: to always know **who is working what, what is stuck, and what
is done** — and to be told that truthfully, at the moment it changes, without
any agent having to stop and report it. `fleet` is that machinery. It is not a
scheduler or a workflow engine; it is the part that makes
ownership visible and exclusive so that leads can lead.

The shape it serves is a hub and spokes. The operator talks to a **lead** (a
supervisor session); the lead dispatches work to **workers** in seats it
controls; workers talk to their lead, never to the operator. A lead is
accountable for every row it dispatched until each is done. When the lead has
too much, the hierarchy grows by one command: bind a second directory to a
second lead role and `fleet reassign --for` the rows that move. Accountability
is a column on the row, not a tree in configuration, so two leads, or a lead
of leads, cost nothing new.

## The idea in five rules

- **Location is identity.** A session's role is decided by the directory it was
  launched in, resolved against `roles.map` (path → tenant, role, seat name).
  Nothing is registered and nothing is declared: open a tab in a roled directory
  and you are that role. A **seat** (a pooled worktree with a name) is a roled
  directory prepared in advance, so the lead can say "put this branch in
  `mono-finisher-2`" and the next session that opens there reads the brief,
  gets the seat's card and denies projected into its harness settings, and
  simply is that worker. Behavior is driven by where the agent sits, not by
  what it is told to remember.
- **Facts come from the hook, never from an agent.** Identity, branch,
  liveness, turn state and last word are derived from harness events. An
  agent does not know the substrate exists until it is refused or handed a
  `[fleet]` line. The day-one failure this design replaces was agents being
  asked to check in and checkpoint; they didn't, and the board lied.
- **One holder per key.** A branch (`repo:<id>:<branch>`) is leased on first
  write; a machine resource (`slot:<name>`) is taken on purpose. A rival is
  refused with the holder's name and the exact command that stands them down.
  A dead holder's branch is taken over; a dead holder's resource is orphaned
  and needs `--takeover`, because the machine it drives may still be running.
  Unreadable evidence is never death.
- **The substrate learns no domain word.** Roles are data: a lane is a
  `manifest.json` (what it requires, produces, denies; its cadence; whether it
  watches the board) plus a prose `card.md` the agent reads. `fr1_test.go`
  fails the build if a domain word appears in the Go source. Adding a kind of
  agent is one directory in cc-skills, not a code change here.
- **Done is evidence.** A receipt is recorded only by a lane that produces that
  kind, from its own roled worktree, at the exact head, from a clean tree.
  `fleet done` answers from receipts and nothing else; a message saying "done"
  is not done.

It is a port of the Python reference in cc-skills
(`docs/features/agent-fleet-rules/ref/`), kept byte-compatible on purpose: store
shapes, filenames, exit codes and refusal texts are a contract the suite pins, and
a store the Python wrote must still satisfy. The design record is cc-skills
`docs/features/agent-fleet-rules/SECOND-LOOK-2026-09-04.md`.

## The four faces

| face | entry | what it does |
|---|---|---|
| hook | `fleet hook claude` / `fleet hook codex` | reads one harness event on stdin; exit 0 allow, exit 2 deny with the reason on stderr; injects `[fleet]` context lines |
| CLI | `fleet <verb>` | the operator's side: stop, resume, revoke, take, drop, board, work, dispatch, … |
| MCP | `fleet mcp` | the same verbs as tools over stdio, for a hub agent to call from inside a session |
| watcher | `fleet watch` | one per machine; writes only under `watch/` (never a lease, session or row): folds the store into `board.json`, `work.json`, `board.md`, records transitions in `observed.jsonl`, revived by any SessionStart |

Exit codes are a load-bearing seam. Hook: 0 allow, 2 deny. Verb: 0 ok, 1 refused
with the reason on stderr (a refusal is the substrate doing its job), 2 usage;
`fleet done` adds 3 for failed evidence.

## The store

`~/.fleet` (override with `FLEET_STATE`). Everything is a JSON file published by
temp-then-rename, or an append-only JSONL. Nothing needs a server.

| path | written by | meaning |
|---|---|---|
| `sessions/<sid>.json` | hook | identity, role, branch, liveness, turn state |
| `leases/<key>.json` | hook, `take`, `drop` | one holder per key |
| `receipts/<sha>.<kind>.json` | `fleet receipt` | evidence of done at an exact head |
| `dispatch/<repo>__<branch>__<rel>.json` | `fleet dispatch` | the declared part of an ownership row |
| `assign/<slot>.json` | `assign`, `dispatch --slot` | what a seat's next session reads at start |
| `mail/.v2/<tenant>/<kind>/<address>/<id>.json` | `send`, `ack` | role/seat messages, retained after acknowledgement |
| `watch/` | watcher | `board.json`, `work.json`, `board.md`, `observed.jsonl`, `heartbeat.json`, `report.md` |
| `events.jsonl` | hook | every evaluation's verdict and latency (passive telemetry) |
| `lanes/<kind>/` | `install.sh` | manifests and cards, copied from cc-skills |
| `keylocks/` | `KeyLock` | advisory `flock` files, never removed, released by the kernel on death |

Identity is the launch directory: `$ORG_STATE/roles.map` binds a path to a
tenant, a role, and optionally a seat name; the hook resolves a session's role from
where it was launched, longest prefix wins.

## Install, shadow, switch

```sh
bash cmd/fleet/install.sh             # dry run: every change it would make
bash cmd/fleet/install.sh --shadow    # run the Go hook beside the installed one, writing only events.jsonl
fleet shadow-report --since 24h       # a day later: events, latency, would-refuse, proven divergences
bash cmd/fleet/install.sh --apply     # build, back up both hook configs, swap the hook lines, install lanes
bash cmd/fleet/install.sh --rollback  # put the previous configs back
```

The installer edits harness configuration; the operator runs it, not an agent.
Shadow mode is how a switch is earned: the same verdict from the same store, exit 0
whatever it decides, and a report that names every case where the two hooks would
have disagreed.

## Ownership rows

`fleet dispatch` is the one declared act in the whole system:

```sh
fleet dispatch feat/x --as finish --for supervisor:mono --due 45m --slot mono-finisher-1 \
  --brief "…" --reply-to <your session id>
```

That writes a row's declared part: change, relationship, accountable role, due,
seat, and the dispatcher's address. Everything else on the row is observed at read
time: hands from the branch lease and its holder's liveness, done from a passing
receipt whose kind is the relationship at the branch head. `fleet work` shows the
rows grouped by accountable role with computed states — `dispatched`, `working`,
`idle`, `late`, `abandoned`, `dead`, `failed`, `undeclared`, `remote`, `done` — and states are
never set by anyone. `reassign --for` moves accountability as a column, which is how
one hub becomes two. The row and its receipts are mirrored to the change's pull
request as marked comments, so another machine reads the same ownership; `fleet
sync` pulls them back.

A lane whose manifest says `"watch": true` gets the board's attention rows and
what changed since its last prompt injected at every UserPromptSubmit.

## Verbs

Run `fleet` with no arguments for the full list. The groups:

- **control** — `stop`, `resume`, `revoke --to`, `decide`, `undecide`, `decisions`, `handoff`
- **resources** — `take [--takeover]`, `drop`
- **seats** — `pool`, `slots`, `assign`, `unassign`, `role`
- **ownership** — `dispatch`, `work`, `reassign`, `undispatch`, `sync`; `request` / `status` (see below)
- **evidence** — `receipt`, `receipts`, `done`, `ready`, `tier`
- **lookup** — `who`, `unowned`, `board`, `sessions`, `leases`, `costs`
- **telemetry** — `report`, `shadow-report`, `inspect-hooks`

`fleet who <thing>` gives one answer or a loud reason there is none, never a
substitute. See `docs/report.md` and `docs/hook-inspection.md` for the two verbs
with their own notes.

## Testing

```sh
go build ./cmd/fleet/... && go vet ./cmd/fleet/... && go test ./cmd/fleet/...
bash cmd/fleet/testdata/run-suite.sh           # the reference suite, ~60s: python3 + git
bash cmd/fleet/testdata/run-suite.sh codex     # the Codex-adapter variant
golangci-lint run ./cmd/fleet/...              # the repo's complexity ceilings; must be 0
(cd cmd/fleet/model && ./judge.sh)             # the lease protocol, machine-checked
```

The suite is the Python reference's own scenarios driven through this binary via
the shims in `testdata/`. The bar for a fix is discrimination: revert it in a
scratch copy and exactly its scenario goes red. `model/` is a bounded Quint model of
the lease protocol: TLC exhausts the reference for both key kinds, and three mutants
reproduce frozen counterexamples byte for byte, one of them the review finding that
an unreadable session record must not be read as death.

`fr1_test.go` is the domain-word tripwire over the Go source.

## Mail

Durable communication between identified roles and individual worker seats in the same tenant.
A `roles.map` row with a fourth column uses that seat name as its address; a
dedicated role uses its role name. Two seats of the same kind have separate inboxes.

Subjects are limited to 1024 bytes; bodies remain full.

```sh
fleet send hub:b --id unit-question-1 --kind question --subject 'Which unit?' \
  --head abc123 --body 'Use milliseconds or seconds?'
fleet mail --unacked                 # full bodies; --for <address> selects a mailbox
fleet ack unit-question-1
```

`--body -` reads stdin. `--session <id8>` disambiguates callers; `mail --json`
returns records. MCP exposes `fleet_send`, `fleet_mail`, `fleet_ack`. A replacement
sender session can retry the same sender address, recipient, ID and payload without rewriting the original
record. Changed payloads and cross-tenant access refuse. Mail grants no assignment,
resource, or merge authority; it has no relationship allowlist or launch machinery.

Hooks inject up to five unacked lines at SessionStart and UserPromptSubmit, without
auto-ack. Absent recipients keep queued mail until a session starts. This smaller
contract supersedes the earlier contact-derivation and watcher-launch scope.
See [Mail semantics and validation](docs/mail.md) for identity, storage and retry rules.

### Delivery and lateness

`fleet watch` — never the hook — can start a session for waiting mail, and can say
out loud what the board already knew. Both are folds: derived every tick, written as
records, never held by the thing they start.

**Delivery.** `$FLEET_STATE/deliver.json` maps an address to what to run for it:

```json
{"hub:lead": {"cwd": "/path/to/dir",
              "cmd": ["claude", "-p", "{{prompt}}", "--model", "opus"],
              "LATE_TO": "hub:above"}}
```

`{{prompt}}` is substituted wherever the operator placed it — the substrate learns no
harness flags. Each fold, for every configured address with mail that is unacked and
never delivered, and with nobody present in its directory, the watcher runs the
command **once**, carrying every eligible message, and stamps each one
`delivered_at`/`delivered_by`. A stamped message is never carried again; a started
launch counts as present for the rest of the fold; a launch that fails to start leaves
its mail for the next fold. `attempt`, `started` and `failed` are recorded in
`watch/observed.jsonl`, with the command's output under `watch/delivery/`.

*Present* means a session record in that directory that has not ended and either has
an open turn or was touched within `FLEET_IDLE_GRACE` (default 5m) — an idle window
nobody is looking at counts as absent, which is what a stalled rehearsal cost.
`FLEET_MAIL_GRACE` (default 10s) holds a just-arrived message back so a burst travels
together. **Delivery latency is bounded by the fold interval** (`--interval`, default
60s): a message arriving just after a fold waits for the next one, so worst case is
one interval plus the grace. An address with no entry is never launched for; its mail
waits for a session, as before.

**Lateness as mail.** Each fold also derives, and sends once per deadline, a `report`
from `fleet:watch`:

- a row past its due date with no live hands and no passing receipt at its head →
  to the role accountable for it;
- a question or escalation unacked past `FLEET_REPLY_GRACE` (default 15m) → to the
  addressee's parent: for a seat, the `--for` role on its dispatch row; for a lead,
  the `LATE_TO` in its delivery entry. With no parent recorded the fold logs
  `late-no-recipient` rather than inventing one.

The report carries evidence — what was due, when, who had hands, what the head was —
and is delivered like any other mail. It grants nothing and acknowledges nothing.
The watcher writes only under `watch/` plus those stamps on mail records.

## Task coordination: first implementation increment

This adds retry-safe local assignments and a read-only observation view. It does
not yet implement the four-interaction product: task launch/acceptance, correlated questions, safe stop and replacement remain adapter work.
Mail is independent of task acceptance.
Do not activate a live trial or present this as cross-harness lifecycle parity.

The approved direction is [cc-skills PR #60](https://github.com/itsHabib/cc-skills/pull/60):
one lead, one active worker, task-owned workspace and natural interaction through
the supervisor skill. The interfaces below are for the supervisor/adapter, not a
set of commands the operator should have to learn.

### Record once, retry safely

From the task's repository checkout, with a known worker:

```sh
fleet request my-branch --id navigation-fix-1 --worker SESSION \
  --for supervisor:ivy --brief 'Reproduce and fix the navigation failure; return focused checks.'
fleet status
fleet status --json
```

Use the discovered executable path if Fleet is not on PATH; the installed
binary lives under `$FLEET_HOME/bin` (default `~/.fleet/bin/fleet`).

Equivalent MCP tools are `fleet_request` (requires caller cwd) and `fleet_status`.
The supervisor chooses a stable request ID before calling. Same repo + same ID +
same branch/worker/lead/brief returns the existing assignment without renewing its
timestamp, resetting its initial head, posting a message or acquiring a lease.
Full-ID retries remain valid after branch deletion or session-record cleanup; a
short session prefix must still resolve uniquely. Supply a branch name, not a
numbered change. Changing the payload under that ID refuses. A second assignment for the same
branch refuses, as do unknown ownership, an unavailable worker and an applicable
stop flag. A recorded assignment is not an execution reservation; the ordinary
hook/lease guard still controls actual effects.

Records extend the existing `dispatch` row with `request_id` and `worker`.
One dispatch-store lock serializes decisions across processes, followed by the
existing branch lock for ownership inspection. Legacy dispatch/reassign/undispatch
cannot overwrite or delete these records, including with `--take`. They remain
retained until a correlated lifecycle operation is implemented. Do not remove
records manually to reuse IDs. Ordinary legacy records remain supported.

`request` is effectful and performs the existing lazy key migration before lease
inspection. Retained collisions refuse; failed requests can leave a migration
marker/lock but no new assignment. No GitHub write or worker launch occurs.

### Observe without claiming more than the evidence

`status` bypasses migration in both CLI and MCP and restores read-only mode after
rendering. JSON is `fleet-task-status-v1`, scoped to local request-bound assignments;
it is not the full portfolio inventory or permission to dispatch. `complete`
means the assignment sources were readable, not that every task is healthy.

- **Queued:** the assignment exists; delivery and acceptance are unconfirmed.
- **Activity observed:** the selected worker has a matching post-dispatch write-tool
  event on the task branch. This is not a claim of successful edits or acceptance.
- **Status needs checking:** conflicting/unreadable ownership, stop flag or missing
  worker liveness. A stopped/dead session does not establish command quiescence.

The existing hook-owned session record carries `last_writes`, keyed by branch,
with time and tool-use ID; `last_write` remains for compatibility. Writes on a
second branch do not erase the first branch observation. Read-only commands, old activity and another branch do not count.
JSON keeps IDs and evidence timestamps for debugging; terminal output does not
require the operator to interpret internal session IDs. No new agent-written
progress ledger, acceptance claim, done state or automatic takeover is introduced.

Generated role bindings subscribe Codex write events and supplement Claude
file-write post-tool events alongside its global Bash hook. Existing bindings
need regeneration and harness reload when this release is installed. This increment
does not edit installed hooks. Terminal observations include the activity age.

### Verification of this increment

Run `go test -race ./cmd/fleet/...`, `go vet ./cmd/fleet/...` and
`golangci-lint run ./cmd/fleet/...`, then both `testdata/run-suite.sh` variants
(default and `codex`). New tests cover real Git state, separate-process replay and
conflicts, immutable payloads, legacy-writer protection, damaged evidence,
post-tool provenance and non-migrating JSON-RPC observation. Harness event
fixtures are not proof of actual live Claude/Codex delivery or stop behavior.

## Guides

- [docs/OVERVIEW.md](docs/OVERVIEW.md): the problem, the four rules, the shape, what it has proved.
- [docs/ONBOARDING.md](docs/ONBOARDING.md): a working fleet over one repository in thirty minutes.
- [docs/MINIMUM.md](docs/MINIMUM.md): the five habits and two files that carry most of the value with none of the machinery.
- [docs/run-a-fleet.md](docs/run-a-fleet.md): stand up leads and seats over any repository, act as a
  lead, worker or verifier, read the fleet.
- [docs/e2e.md](docs/e2e.md): prove a build end to end with real sessions in a sandbox of your
  choosing; `e2e/mail-poll.sh` (delivery stand-in until the watcher launcher lands) and
  `e2e/run-metrics.py` (the scorecard, from records only).

## What is deliberately not here

- No daemon owns anything. The watcher writes only its own board files; the hook is where facts
  are written; leases live in files the kernel releases on death.
- No agent ceremony. There is no check-in, heartbeat, or status an agent must
  send. If the board needs a fact, the hook derives it from an action the agent
  was going to take anyway.
- No domain vocabulary. Kinds of agent, what they require and produce, and what
  "done" means for a relationship all live in lane data and cards in cc-skills.
- No cross-machine store. Two machines share nothing but the git remote; the
  pull request is the record both read.

## Layout

```
main.go             dispatch: hook | mcp | watch | <verb>; the fail-open law for the hook
install.sh          dry run / --shadow / --apply / --rollback
internal/fleet      the store, keys, leases, liveness, policy, the hook handlers
internal/verbs      every CLI verb; Out is swappable so MCP captures it
internal/mcp        the verbs as MCP tools over stdio
internal/codex      Codex's event shape mapped onto the hook
internal/watch      the watcher: fold, classify, render, revive
internal/report     passive telemetry folded into a daily report
model/              the Quint model and its judge
testdata/           the reference suite, shims, lanes and fixtures
docs/               notes for report and hook inspection
```

## Continue after a session changes

A replacement session receives the seat's current assignment at SessionStart,
including a brief already shown to its predecessor. The assignment is checked
against its recorded role and tenant and the seat's current directory, repository
and branch; rebinding or reusing a seat does not replay the previous assignment.
Legacy assignments without recorded identity remain retained but need a fresh
assignment before Fleet can safely replay their context. The original delivery stamp remains historical
notification evidence, not a claim that the replacement accepted or finished work.

A dedicated lead can leave context that follows its role across branches:

```sh
fleet handoff --role "The parser expects milliseconds" "Answer the worker's units question"
```

The next session launched in that tenant/role receives an authored, advisory
excerpt. `fleet_handoff` exposes the same operation through MCP, with `conclusion`,
optional `next` and `session`, and required caller `cwd`. No Org attach, claim,
checkpoint or release is involved. A handoff is context, not completion evidence.
The stored text is limited to 16 KiB and the startup excerpt to 1,024 UTF-8 bytes.

Pooled worker seats use the existing branch handoff instead:

```sh
fleet handoff work-one "Parser fixed; integration check remains" "Run the integration check"
```

This avoids sharing one role handoff between different worker seats of the same
kind. Captured last assistant text remains separate from the intentional handoff.
No old Org records, installed hooks, or live assignments are migrated by these
commands. The longer-term single-work-record migration remains separate work.
