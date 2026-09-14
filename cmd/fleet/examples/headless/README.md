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

Requirements: Python 3.11+, Go, Git, an authenticated Codex CLI, and the existing
trusted Fleet hooks. Preparation installs nothing and does not copy credentials.
This example is stacked on the sandbox liveness fix in PR #342. Its default
build therefore includes the fix. `--fleet-source` can select another local
checkout explicitly; resolved.json records what was built.

From this repository:

```sh
python3 cmd/fleet/examples/headless/lab.py prepare
# Or: ... prepare --fleet-source /path/to/fixed/workbench --cards /path/to/cards
```

Preparation prints a new disposable lab directory first. Use it as `LAB` below.
It builds a private Fleet binary, clones a fixed Workbench base, seeds the task,
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
at launch. Provider choice is fixed to Codex in this bounded example.

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
tests and commits; a separate verifier process fetches that commit into its
own checkout, tests it and records a receipt. The supervisor exports the patch,
records an assessment and stops the addresses. The outer artifact audit requires
the actual patch, unchanged input/tests, the original assignment, observed draft
continuity by the original author, a different
supervisor conversation, clean same-head checkouts and independently attributed
passing receipts. PLAN.md may change when its author documents the answer and
finished work. This check observes file-change events; it does not prove absence
of writes outside those events. The outer audit executes no worker tests.
Failure and missing evidence prevent a passing audit.

Inspect `result/worker.patch`, `result/ASSESSMENT.md` and `result/audit.json`.
Actual provider state, traces and collected exits are under
`state/watch/delivery/`; Fleet owns mail, work and checkpoints under `state/`.
An assessment file alone is not completion.

## Boundaries and direction

The three agents are local processes with separate Git checkouts. They use
process-local Codex workspace-write policy with the lab as writable root,
network disabled, approval never, and MCP/plugins/desktop controls disabled.
They use the absolute private Fleet CLI because login shells can replace PATH.
Existing trusted host hooks and authentication remain in use. This is not
whole-process-tree isolation; agents share the lab and its local state.

Rooms is a separate optional execution backend: apply the same frozen patch
against the same base in the existing local Rooms host, run the same tests and
collect its result. That verifies the patch in Rooms. It does not put the
supervisor, author or verifier model inside a VM. Coordination to prepare that
backend occurs outside the demonstrated headless path.

This requires Rooms with PR #121's toolstore support. On an already prepared
Linux Rooms host, copy the frozen patch there and run:

```sh
python3 rooms-check.py --rooms /path/to/rooms --image /path/to/image.ext4 \
  --toolstore /path/to/python-toolstore --patch /path/to/worker.patch \
  --out /path/to/new-attempt-directory
```

This thin foreground adapter records input hashes, the exact invocation, CLI
exit and returned patch hash. Inspect `out/result.json` for command outcome and
`lifecycle.ndjson` for collection/cleanup. It does not start the host, install
tools, create a VM image, run a model or treat missing cleanup evidence as success.
The measured cold run used this same Rooms invocation through the owner's
host-specific probe; this portable adapter is a convenience, not a second
measured backend run.

Keep role cards as editable purpose/responsibility/context, slots as capacity
bindings, and assignments as work. Fleet owns launch/observe/mail/checkpoint/
stop where no desktop exists. A deployment manifest may reproduce these inputs
and compile them through existing operations; it does not need another runtime
or state store. A hierarchy is useful only when a concrete multi-project
responsibility/navigation problem appears. Reuse optional Org prose/parent
relationships then; no inherited permission or mandatory communication routing.

The earlier desktop recovery comparison in `../recovery/AGENT-TRIAL.md` found no
material Fleet advantage. This headless workload tests a different missing
service: scheduling and continuity without desktop task controls. It is not a
measured speed or cost comparison against that earlier run.

Offline operator tests (no model or watcher):

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s cmd/fleet/examples/headless/tests -v
```
