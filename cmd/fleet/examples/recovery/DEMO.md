# Recover the checkpoint already written

From the repository root, with Go, Python 3 and Git:

```sh
go build -o /tmp/fleet-recovery ./cmd/fleet && python3 cmd/fleet/examples/recovery/demo.py /tmp/fleet-recovery
```

The demo takes a few seconds and prints a retained disposable artifact directory.
It creates local Git repositories and isolated Fleet/Org state, runs deterministic
local processes, and kills only its own supervisor child. It uses no model,
watcher service, network, installed configuration, or real worker assignment.

1. An author commits input; a separate process checks exact bytes and emits a
   verify receipt at the same head. These are distinct fixture processes, not
   an independent model review or a proof that Fleet enforces independence.
2. A transform worker takes a branch lease through a hook, writes a dirty draft
   and remains alive. The supervisor saves an ordinary 1,563-byte checkpoint;
   the driver kills that supervisor after publication.
3. A fresh reader process receives only the root and binary path. Existing
   `inspect`, `work`, `receipts` and Git reads reveal the completed input, exact
   receipt provenance, live writer, dirty draft, remaining work and authority
   context. The driver asserts all retained non-Git files and mtimes survived
   the reads. `observed.json` contains the actual observations.
4. The same worker finishes when signaled. A fresh direct caller verifies the
   output and creates a local summary; another invocation keeps it without
   rewriting. A tiny desired-data caller performs the same operation in a
   separate summary directory and prints its proposal. Both refuse an external
   edit and preserve its bytes. The completed input is never rewritten.

The driver supplies the continuation signal and deterministic acceptance
assertions. This is a mechanism probe, not an autonomous-agent benchmark.
Harness hook registration is real; PID provenance is deliberately replaced with
the known Python child's PID so this does not borrow an enclosing desktop
session's liveness. The fixture driver owns and reaps both children. It does not
demonstrate tracking arbitrary descendants, provider crash recovery, distributed
leases, atomic snapshots, exactly-once effects or crash-durable disk flushes.

## Reproduce the old reader

Build Fleet from base `10b066cac0bb959ca3dfa7dc2d77886eed277e78` in a separate
checkout, then run this script with that binary:

```sh
python3 cmd/fleet/examples/recovery/demo.py /absolute/path/to/old-fleet --expect-missing
```

The assertion succeeds only if the documented reader loses the next step while
the full checkpoint remains on disk. The old reader returns the 1,024-byte
SessionStart excerpt. The data was never lost: inspecting the hashed JSON file
directly was already a workable fallback. This PR removes that retrieval detail
from the caller; it introduces no new persistent store.

## What Fleet earns here

Desktop task listing, waiting, messages and retained conversations already
provide useful coordination and continuity. Git gives exact heads and dirty
work; GitHub supplies reviews, checks and PR discussions. A capable agent can
compose those directly. This POC does not justify recreating them in Fleet.

Fleet contributes existing local records that can be read outside one desktop
conversation: work relationships, cooperative branch ownership, role checkpoints
and receipt provenance. The demonstrated missing piece is a complete checkpoint
read through the documented operation. Recovery still depends on someone having
written useful context before interruption and retaining the role binding and
state. A stale or mistaken checkpoint stays stale or mistaken.

An authored authority section is a pointer to prior instructions, not authority.
The demo's authority comes from its caller authorizing this disposable exercise.
Real merge work must consult existing authenticated grants and the exact-head
Gate action; PR #339's grant discovery is an optional lookup, not that action.
This demo never inspects or mints a real grant.

## What this says about the language bakeoff

The direct and desired-data clients use the same `ensure_file` operation and
current file contents. Each creates one summary, keeps it on retry and refuses
conflicting external contents. The desired-data client adds a readable proposal;
it adds no recovery guarantee in this scenario. The direct baseline suffices.
This small comparison does not test parser design, arbitrary effects, plugins
or a general planner, and does not choose a winning representation.

A general language/compiler for desired systems and work can remain an optional
client. Fleet and Rooms are possible adapters, not the language's scope. Syntax
and plugins can describe dependencies and inspection without owning another
authority, ownership or completion registry. The prepared bakeoff should keep
its own richer common workload and earn value against direct use of the same
operations; it has not been launched by this POC.

Useful foundations to depend on, with their present limits:

| Existing primitive | What a client can use | What it must not infer |
| --- | --- | --- |
| Git head/status and artifact bytes | Current revision and observed contents | A cross-resource snapshot or semantic success |
| Work rows and leases | Declared accountability and cooperative local ownership | OS isolation, forced quiescence or cross-machine exclusion |
| `inspect` and handoffs | Retained authored context plus separate activity observations | That activity is completion or context is permission |
| Receipts | Verdict, kind, exact head and session provenance | Enforced independence or merge authority |
| `request --id` | Same-payload replay while the canonical row survives | Exactly-once execution or deduplication after deletion |
| Gate | Existing grant assessment and a pinned authorized action | That discovery alone authorizes merging |

PR #337 separately explores unseated plan/apply replay using existing work rows.
It does not cover seated assignment publication or worker execution. The known
gap where seated dispatch can publish a wakeable assignment before its work row
is committed remains outside this PR, as do runtime #334 and review #338. A
compiler must disclose those limits rather than promise them through syntax.

Assessment question: after trying the demo, does the complete `inspect` result
provide enough continuity to keep direct tools as the supervisor's foundation,
and which concrete change in the richer workload would make an inspectable
declarative proposal worth its added concepts?
