# Roles are prose; work and messages stand on their own

**Decision:** revised with the operator on 2026-09-10 after the headless failures in
[#304](https://github.com/itsHabib/workbench/issues/304) and
[#306](https://github.com/itsHabib/workbench/issues/306).

The earlier decision put all role definitions and continuity into Fleet. This
revision keeps the useful part of Org: a name, a prose card and optional parent
reference. It removes Org's chain protocol from normal work. This is the
operator's correction to that destination, not a claim that the previous
reviewers reviewed this implementation. Prior discussion remains in
[the review disposition](review-disposition.md).

## The ordinary experience

Write a Markdown file describing a role. Register it:

```sh
org charter -role lead:project -file ./lead.md -parent human:mh
org boot -role lead:project
org status
```

A role can describe a second repo, another child, or a different responsibility
by editing that prose. Registering the same name updates its file reference.
No retire/recharter sequence, incarnation, held claim, or scope delta is needed.
A parent is a descriptive reference; it need not become a delegation protocol.

Org does not send mail, assign work, own a process or demand checkpoints. Fleet
can operate without Org. Org can describe a team without Fleet. An agent can read
the Markdown directly; the registry and startup hook are conveniences.

## One owner for each fact

| Fact | Owner |
|---|---|
| Role instructions and responsibilities | The registered Markdown card |
| Role name, tenant, card path, optional parent | Org's small registry |
| Directory and seat bindings | Existing `roles.map` |
| Work and accountable lead | Fleet's existing assignment/dispatch path |
| Session identity, activity and process observations | Harness events and Go runtime observations |
| Mail and acknowledgement | Existing Fleet mail or native desktop messaging |
| A useful conclusion for the next session | Existing work handoff |
| Exclusive resource use | Existing leases |
| Completion evidence | Exact-head receipts and checks |
| Permission to merge | Gate |

Neither a parent link nor prose grants authority. A message does not require
charter traversal. A handoff does not require a chain checkpoint. Use the existing
mail and handoff mechanisms before inventing a new continuity product.

## The implementation stays small

Org has three normal CLI and MCP operations: register/update, read, list. Its
`roles.json` contains `{tenant, role, card, parent?}` rows. Prose stays in its
original file, and edits are visible on the next read. There is no synchronized
Fleet copy, hash-linked role definition, lifecycle reducer or inferred hierarchy
permission. Registry writes serialize and publish atomically; invalid registry
contents must not be overwritten as an empty registry.

The existing `roles.map` remains a directory/seat binding. Registration does not
create mailboxes or rebind workspaces. Distinct seats keep distinct addresses;
ambiguous address lookup must name the alternatives, not guess.

Fleet's supported headless scheduling, delivery and process observation live in
Go. Dispatch with a brief wakes the configured seat without a second order.
Recurring lead ticks are an optional schedule in that same runtime. A worker
continues authorized work without an arbitrary per-tick action or message quota.
New messages may obtain generated IDs; a deliberate same-message retry keeps
its returned ID and payload. A reply is a different message.

`fleet tail <seat|role> [-f]` reads observed transcript/output text and tool calls.
`fleet watch status` reads the launch, process, exit, output and latest harness
observations without running a tick. `--json` exposes the same facts to a future
UI. Watcher health and worker activity are separate. A process exit is not task
completion, and a quiet or present process is not proof of progress.

## Existing installations and history

The old Baton journal is retained under `org legacy <verb>`. Its kernel, records,
held work and open obligations are not rewritten. Default `org boot` reads a card
and never silently injects the old lifecycle protocol. The default MCP surface
no longer exposes work/journal verbs. The old Stop mark hook becomes a no-op;
the SessionStart hook can supply registered prose.

This is an explicit CLI/MCP cutover, not an in-place change to a running process.
Install matching Org and MCP binaries, update operating instructions, register
cards, and inspect legacy held work and unresolved questions before retiring
those callers. Put useful conclusions in the existing work/handoff path. Do not
claim that registering a card completed or migrated an old assignment. Retain
history and explicit legacy access until the remaining consumers are retired.
Do not automatically convert old authority terms into ignored metadata or delete
old leases/grants as part of role configuration.

No installed binaries, hook settings, live mappings, live state or merge controls
are changed by this patch. The working desktop loop/native-message baseline
remains available. Installation and a measured headless rerun remain separate
from unit and subprocess tests.

## Next runtime step, after this pass

The present command launcher still uses bounded command invocations such as
`claude -p`. Moving its poller into Go does not by itself supply session continuity.
Replace this boundary in a subsequent, focused change for **both Claude and Codex**:

- Keep the provider's durable session/thread ID and return to that exact session.
- Send a follow-up through the provider interface; do not reconstruct context from
  a new prompt or select an unrelated most-recent session by accident.
- Observe progress, waiting/input requests, completion, cancellation and errors.
  Token-by-token text streaming is optional; lifecycle visibility is required.
- Record explicit budget exhaustion and interruption separately from successful
  completion. Preserve working files and do not blindly replay uncertain effects.
- Keep scheduling and watcher ownership in Go. An SDK adapter, if needed by a
  provider, should translate that provider's interface rather than own another
  scheduler, mailbox or workflow engine.

Current official interfaces to investigate are Claude's
[streaming input](https://code.claude.com/docs/en/agent-sdk/streaming-vs-single-mode)
and [session resume](https://code.claude.com/docs/en/agent-sdk/sessions), and
[Codex app-server](https://learn.chatgpt.com/docs/app-server). The latter exposes
thread resume and turn start/steer/interrupt with structured events. The existing
Ship Claude SDK runner is useful prior art, but currently starts a fresh query
and rejects attach: merely substituting it would preserve the lifecycle problem.
This patch does not claim to implement either replacement runtime.

## Acceptance

Use isolated state, subprocesses and Git worktrees for mechanical checks:

- Create and edit a role, including its repo responsibilities, under the same
  name without a chain. Read the edited prose immediately.
- Keep tenant-filtered card lists and preserve old journal bytes.
- Verify three MCP operations and visible errors; no hidden work protocol.
- Dispatch across repos without changing caller identity; wake from assignment.
- Prevent a second launch before the first process emits its startup hook.
- Show running/failed workers and last hook activity between watcher ticks.
- Resume dirty same-branch work without discarding files; preserve live ownership.
- Retain mail and intentional retry semantics across replacement sessions.

Then repeat a real headless workload against the desktop baseline. Compare
completed draft PRs, coordination spend, idle assigned time and operator rescues.
Record source/binary versions, model settings and actual launch commands. Local
green mechanics do not prove improved real-task throughput.
