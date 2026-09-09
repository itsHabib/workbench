# fleet

The agent-fleet substrate: one Go binary that is the harness hook for Claude Code
and Codex, the operator's CLI, an MCP server, and a read-only per-machine watcher,
all over one store at `~/.fleet`. It exists so an operator running several agent
sessions can answer one question truthfully at any moment: who is working what,
what is stuck, and what is done.

It is a port of the Python reference in cc-skills
(`docs/features/agent-fleet-rules/ref/`), kept byte-compatible on purpose: store
shapes, filenames, exit codes and refusal texts are a contract the suite pins, and
a store the Python wrote must still satisfy. The design record is cc-skills
`docs/features/agent-fleet-rules/SECOND-LOOK-2026-09-04.md`.

## The idea in four rules

- **Facts come from the hook, never from an agent.** Every session's identity,
  branch, liveness, turn state and last word are derived from harness events.
  Nothing is declared. An agent does not know the substrate exists until it is
  refused or handed a `[fleet]` line.
- **One holder per key.** A branch (`repo:<id>:<branch>`) is leased on first
  write; a machine resource (`slot:<name>`) is taken on purpose. A rival is
  refused with the holder's name and the exact command that stands them down.
  A dead holder's branch is taken over; a dead holder's resource is orphaned
  and needs `--takeover`, because the machine it drives may still be running.
  Unreadable evidence is never death.
- **The substrate learns no domain word.** Roles are data: a lane is a
  `manifest.json` (what it requires, produces, denies; its cadence; whether it
  watches the board) plus a prose `card.md`. `fr1_test.go` fails the build if a
  domain word appears in the Go source.
- **Done is evidence.** A receipt is recorded only by a lane that produces that
  kind, from its own roled worktree, at the exact head, from a clean tree.
  `fleet done` answers from receipts and nothing else.

## The four faces

| face | entry | what it does |
|---|---|---|
| hook | `fleet hook claude` / `fleet hook codex` | reads one harness event on stdin; exit 0 allow, exit 2 deny with the reason on stderr; injects `[fleet]` context lines |
| CLI | `fleet <verb>` | the operator's side: stop, resume, revoke, take, drop, board, work, dispatch, … |
| MCP | `fleet mcp` | the same verbs as tools over stdio, for a hub agent to call from inside a session |
| watcher | `fleet watch` | one per machine, read-only: folds the store into `board.json`, `work.json`, `board.md`, records transitions in `observed.jsonl`, revived by any SessionStart |

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

- **control** — `stop`, `resume`, `revoke --to`, `decide`, `undecide`, `decisions`
- **resources** — `take [--takeover]`, `drop`
- **seats** — `pool`, `slots`, `assign`, `unassign`, `role`
- **ownership** — `dispatch`, `work`, `reassign`, `undispatch`, `sync`
- **evidence** — `receipt`, `receipts`, `done`, `ready`, `tier`
- **lookup** — `who`, `unowned`, `board`, `sessions`, `leases`, `costs`, `handoff`
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

## What is deliberately not here

- No daemon owns anything. The watcher is read-only; the hook is where facts
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
