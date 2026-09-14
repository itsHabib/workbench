# Headless Workbench example

An operator gives a supervisor one repository task. Fleet launches an author,
delivers a question, recovers an interrupted supervisor into a new conversation,
and launches an independent verifier at the author's exact result revision.
This example uses existing Fleet commands and records. It adds no scheduler or
permission hierarchy.

The task is a reusable Python readout of frozen GitHub component checks. It
preserves exact heads and observed labels, including skipped reviews and native
status contexts. Empty observations remain unknown; successful checks confer no
merge authority. The supplied input came from Workbench PRs 334, 337 and 340;
the supplied tests include synthetic boundary cases.

## Run

Requirements: Python 3.11.4+ (tarfile extraction filters), Go, Git and one authenticated provider for all three
agents: the Codex CLI with the existing trusted Fleet hooks (the default), or
Claude Code plus a directory whose `node_modules` holds the Claude Agent SDK
(`--provider claude --runtime-home DIR`). Preparation installs nothing and does
not copy credentials. The default build is this checkout, which includes the
macOS sandbox liveness fix from PR #342. `--fleet-source` can select another
local checkout explicitly; resolved.json records what was built.

From this repository:

```sh
python3 cmd/fleet/examples/headless/lab.py prepare
# Or: ... prepare --fleet-source /path/to/fixed/workbench --cards /path/to/cards
# Claude for all three agents, with the verifier running the patch in Rooms:
# ... prepare --provider claude --model sonnet --runtime-home /path/to/sdk-dir \
#       --rooms rooms-host /guest/rooms /guest/image.ext4 /guest/python-toolstore
```

Preparation prints a new disposable lab directory first. Use it as `LAB` below.
It builds a private Fleet binary, fetches only a fixed Workbench base (the
source repository's other refs hold this example's committed reference result),
seeds the task,
creates the role bindings and author slot through Fleet, and checks provider
configuration without a model turn. A failed preparation retains its directory
and diagnostics. `run` uses the existing provider account and consumes its usage.

```sh
LAB=/absolute/path/printed/by/prepare
python3 cmd/fleet/examples/headless/lab.py cards "$LAB"
python3 cmd/fleet/examples/headless/lab.py run "$LAB"
```

In another terminal:

```sh
python3 cmd/fleet/examples/headless/lab.py status "$LAB"
python3 cmd/fleet/examples/headless/lab.py stop "$LAB"
```

The run is bounded to 20 minutes. The stop entry point pauses its three addresses,
requests provider cancellation and asks the driver to collect exits before
stopping the watcher. A cancellation request is not proof of descendant
quiescence. Unknown exits are retained in `control/cleanup.json` and `status.json`.
Each prepared lab has one initial run and permits one bounded continuation:

```sh
python3 cmd/fleet/examples/headless/lab.py resume "$LAB"
```

Resume requires a stopped watcher and collected terminal attempts. It lifts the
address stops and lets pending mail or an eligible recurrence wake the agents;
it does not redispatch work, replay acknowledged mail or supply new task facts.
Earlier timeout/cleanup evidence is retained under `control/before-resume/`.
Nothing deletes the retained checkout, draft, messages or failure evidence.

## Define once, inspect, update

The default source is this example's `cards/` directory. `--cards DIR` imports
`supervisor.md`, `author.md` and `verifier.md` once. The active editable copies
are `LAB/lanes/<kind>/card.md`. Editing the original import directory after
preparation does not update these copies.

`resolved.json` records the import location and initial hashes, current editable
paths, derived roles and addresses, checkouts, task/base, private binary/source
revision, source dirty state and process-local provider overrides. `cards` prints
each current source hash, binding and diff against its installed projection.

Edit the local card, inspect the diff, then apply it while the lab is stopped:

```sh
python3 cmd/fleet/examples/headless/lab.py cards "$LAB"
python3 cmd/fleet/examples/headless/lab.py update "$LAB"
```

`update` calls existing `fleet role` only for changed projections and verifies
the resulting instructions. It refuses a running or uncertain watcher and
uncollected attempts. It does not restart a session or claim that a conversation
adopted the change. `control/run-inputs.json` freezes card and run-input hashes
at launch. One provider serves all three agents; `--provider` and `--model`
choose it at preparation.

Claude does not load Fleet's `CLAUDE.local.md` import of a card outside the
checkout without a per-project approval. With `--provider claude` the lab's
`claude` wrapper therefore appends the checkout's current `LAB/lanes/<kind>/card.md`
to the system prompt at every launch (`--append-system-prompt-file`) and refuses
to launch from a directory that has no card. An edited
card reaches the next launch without `update`, which projects only the Codex
instructions. `cards` reports both.

## What the demonstration does

The author deliberately writes a dirty plan and asks a policy question already
covered by the task's tests. The supervisor checkpoints and enters a short,
explicit interruption fixture. This creates a reproducible recovery point; it
is not a normal peer polling pattern or evidence of novel product reasoning.

The outer driver waits for the draft, stops/cancels only that supervisor, waits
for its exact provider terminal result and collected exit, then resumes the
address. The supervisor target has `fresh: true`: every wake starts a new
provider conversation. Pending author mail wakes it. The outer driver supplies
no task facts, policy answer, assignment or verifier order after interruption.
The new supervisor must recover those from Fleet records and files.

The supervisor retains the original assignment, answers through Fleet with a
stable message ID and repeats that exact send once. The author implements,
tests and commits. When it reports its head, the supervisor exports the patch
against the base and orders a separate verifier process. The verifier fetches
that commit into its own checkout, confirms the patch is byte-identical to its
own diff, tests it and records a receipt. With `--rooms` it then runs that same
patch file once in a cold Rooms room and records a separate `rooms` receipt at
the same head. The supervisor reads both receipts and the Rooms result, records
an assessment and stops the addresses. Agents wait for peers by ending their
turn; Fleet wakes them when mail arrives.

The outer artifact audit requires the actual patch, unchanged input/tests, the
original assignment, observed draft continuity by the original author, a
different supervisor conversation, clean same-head checkouts, independently
attributed passing receipts and no observed tool input naming this example's
committed reference result. With `--rooms` it also requires the `rooms` receipt,
a returned patch identical to the exported one, a succeeded command and
lifecycle collection and cleanup. PLAN.md may change when its author documents
the answer and finished work. Draft continuity reads Codex file-change events
(timestamped) or Claude Write/Edit tool calls (placed at their attempt's launch
time); it does not prove absence of writes outside those events. The outer audit
executes no worker tests. Failure and missing evidence prevent a passing audit.

Inspect `result/worker.patch`, `result/ASSESSMENT.md` and `result/audit.json`.
Actual provider state, traces and collected exits are under
`state/watch/delivery/`; Fleet owns mail, work and checkpoints under `state/`.
An assessment file alone is not completion.

## Boundaries and direction

The three agents are local processes with separate Git checkouts. They use the
absolute private Fleet CLI because login shells can replace PATH. This is not
whole-process-tree isolation; agents share the lab and its local state.

- **Codex**: process-local workspace-write policy with the lab as writable root,
  network disabled, approval never, and MCP/plugins/memories/desktop controls
  disabled. Existing trusted host hooks and authentication remain in use.
- **Claude**: the Agent SDK starts the installed CLI through a lab wrapper that
  loads only project and local settings (no user hooks or memory files), an
  empty strict MCP configuration and the tools Bash, Read, Write, Edit, Glob and
  Grep, and appends the role card. Auto memory is off. Fleet's projected hooks
  run from the private binary. Every agent runs under `dontAsk`, set by
  deliver.json and by each checkout's local settings, which refuses anything not
  allowed without prompting. The settings allow
  Bash, and file tools only by path, `Edit(//LAB/**)` and `Read(//LAB/**)`: a
  probe wrote inside the lab and was refused outside it. They also deny push,
  `gh`, `curl`, `wget`, `limactl` and web tools, but those are command-prefix
  rules that an absolute path or a wrapper script gets past. There is no OS
  sandbox: Bash can read and write outside the lab and reach the network. Run 2
  wrote temporary files under /tmp, so treat the Claude topology as trusted-local,
  not contained. Runs 2 and 3 predate the path rules and card injection: they
  allowed the file tools without a path and read their cards because RUN.md told
  them to.

Rooms is the optional execution backend. With `--rooms` the verifier runs the
exported patch through `LAB/bin/rooms-run PATCH OUT`, which applies it to the same
base in a cold room, runs the same tests and collects the result. That verifies
the patch in Rooms. It does not put the supervisor, author or verifier model
inside a VM. Starting the Rooms host and installing its once-per-host network
substrate (`scripts/setup-tap.sh --host` in the Rooms repository) happen outside
the demonstrated headless path.

This requires Rooms with PR #121's toolstore support. On an already prepared
Linux Rooms host, or from macOS through the local Lima Rooms host, run:

```sh
python3 rooms-check.py --rooms /path/to/rooms --image /path/to/image.ext4 \
  --toolstore /path/to/python-toolstore --patch /path/to/worker.patch \
  --out /path/to/new-attempt-directory [--lima rooms-host]
```

This thin foreground adapter records input hashes, the exact invocation, CLI
exit and returned patch hash. Inspect `out/result.json` for command outcome and
`lifecycle.ndjson` for collection/cleanup. With `--lima` the same file runs
inside the VM as root with a private HOME holding the Lima user's `id_rooms` key,
the attempt directory is copied back, the guest copy is removed without crossing
mounts, and `transport.json` adds the transport time. It does not start the host,
install tools, create a VM image, run a model or treat missing cleanup evidence
as success.

## Measured runs

These runs were on one macOS host, with the local Lima Rooms host for the patch
execution. Their evidence is retained outside the repository: RESULTS.md, the lab
archives and per-attempt measurements. PR #344 names where.

| Run | Provider | Outcome | Wall | Supervisor sessions | Rooms (in room / with transport) |
|---|---|---|---|---|---|
| Codex, 2026-09-13 | Codex app-server | patch `83da8be4` verified; Rooms ran separately outside the headless path | bound expired; one continuation | 3 (that lab's audit.json) | 18.29 s cold baseline (owner's probe) |
| Claude 1 | claude-sonnet-5 | fixture failed: Claude Code refuses a standalone `sleep`; the driver stopped instead of waiting | — | 1 | — |
| Claude 2 | claude-sonnet-5 | audit pass at `b288e388`, three receipts; see deviations below | 1,099 s | 2 | 14.53 s / 15.28 s |
| Claude 3 | claude-sonnet-5 | audit pass at `07ed940c`, 26/26 checks, three receipts, assessment reads Rooms directly | 784 s | 4 (interrupted + 3 mail wakes) | 17.30 s / 20.19 s |
| Claude 4 | claude-sonnet-5 | final code (path rules, card at launch): three receipts at `d3bf57b1`; the driver's audit failed only because it required receipts from the checkout root, and the verifier's came from a task subdirectory. The corrected rule passes 28/28 on the retained evidence | 628 s | 5 (interrupted + 4 mail wakes) | 14.72 s / 15.73 s |

Every Rooms run returned a byte-identical patch, reported `succeeded`, and
recorded `collection_done` and `cleanup_done`. In run 2 the recovered supervisor
polled `fleet status` in shell loops for 16 minutes instead of yielding. The
verifier also `cd`'d out of its checkout, and Fleet's guard then refused its
return, 5 times. It finished through a worktree under /tmp. Run 3's cards
removed all three. Provider-reported notional cost was $6.21 for run 2, $3.41
for run 3 and $3.84 for run 4. Run 4 exercised the final permission rules and
card delivery. The live author process carried the card flag after the SDK's
arguments, and no agent polled, touched /tmp or hit the cd guard.

Receipts are now accepted from anywhere inside the recording agent's own
checkout (Fleet's `worktree` field), rather than only from its root. The
corrected audit also passes runs 2, 3 and the earlier Codex lab.

## Next milestone: agents in rooms, peer mail across rooms

The Claude runs above put a real Workbench patch through supervisor,
author, verifier and a cold Rooms execution with no desktop task tools. They
also show what stands between this and a fully headless peer-to-peer fleet:

- **Agents run on the host; only the patch runs in a room.** A cold room applied
  and tested the exact patch in 14.5–18.3 seconds, while the three model agents
  ran as host processes. The Claude agents had no OS sandbox and wrote under
  /tmp. Moving an agent into a room needs:
  - a room that holds a provider turn for minutes rather than a 120-second
    command;
  - provider credentials injected per room and scoped, instead of the host's
    login;
  - egress limited to the provider API and the repository;
  - the room's result patch returned as the agent's work product. Rooms already
    returns a byte-identical patch.
- **Fleet identity is a host directory and a host PID.** Delivery launches the
  bridge as a host process. Liveness reads host PIDs (the #342 sysctl fix), and
  a session's recorded directory follows its shell's cwd. After a verifier
  `cd`'d inside the lab, the PreToolUse guard judged its own checkout another
  seat and refused the way back, and it could not record a receipt until it
  worked around the guard. In a room, identity has to be the room and
  attempt, and liveness has to come from the room lifecycle
  (`vmm_started` … `cleanup_done`), not `kill`/`sysctl` on the host.
- **Mail and wakes need a host-side watcher.** Mail, receipts and handoffs live
  in one host `FLEET_STATE` directory, and only the host `fleet watch` turns mail
  into a launch. Peer mail across rooms without a host supervisor needs:
  - a mail and receipt store that in-room sessions can reach, over a narrow
    endpoint or a synced directory;
  - wake delivery that starts a room;
  - per-room session identity for acknowledgements and stable message IDs.
  The mail-and-wake contract itself held: the author woke on its assignment, a
  fresh supervisor woke on pending mail after the interruption and recovered from
  records alone, and the verifier woke on its order.
- **Waiting must be cheap and explicit.** Unprompted, the recovered Claude
  supervisor held its turn for 16 minutes, polling `fleet status` until peers
  finished. When every turn occupies a room, that is a room held idle. The cards
  now require yielding. A first-class "end turn until mail" signal would make
  that the default rather than a prompt rule.

Concretely, the next step is one agent in one room. Run the verifier inside a
Rooms room with an injected scoped credential. Give it a Fleet mail/receipt
endpoint it can reach from the guest. Keep the host watcher only as the waker,
then remove it once the in-room session can wake peers itself.

Keep role cards as editable purpose/responsibility/context, slots as capacity
bindings, and assignments as work. Fleet owns launch/observe/mail/checkpoint/
stop where no desktop exists. A deployment manifest may reproduce these inputs
and compile them through existing operations; it does not need another runtime
or state store. A hierarchy is useful only when a concrete multi-project
responsibility/navigation problem appears. Reuse optional Org prose/parent
relationships then; no inherited permission or mandatory communication routing.

The [earlier desktop recovery comparison](https://github.com/itsHabib/workbench/blob/b8e99094eee6587a7f2e511cd22db30b7a0ab56b/cmd/fleet/examples/recovery/AGENT-TRIAL.md) found no material Fleet advantage. This headless workload tests a different missing
service: scheduling and continuity without desktop task controls. It is not a
measured speed or cost comparison against that earlier run.

Offline operator tests (no model or watcher):

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s cmd/fleet/examples/headless/tests -v
```
