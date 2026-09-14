# Relay: evidence at a handoff

Status: bounded POC, 2026-09-14. No pipeline engine or required phase sequence.

## Decision

**Existing primitives plus a documented contract suffice.** Fleet already owns
assignments, receipt history, continuity and occupancy. The missing operation for
this slice is checking the receiving assignment's evidence requirements before
recording it. Add that check to `fleet request`, retaining the inputs in its
existing immutable, retry-safe assignment. Do not introduce a pipeline object,
another ledger, scheduler, role hierarchy or new binary.

This implements one boundary: implementation evidence → queued verification work.
It does not launch the worker or declare verification complete. A colleague can
inspect the recorded brief and evidence, deliver it using the current desktop
conversation, and work freely within the assignment. Gate still owns release.

The source is [Relay PR 1](https://github.com/itsHabib/relay/pull/1), especially
[the brief and raw appendices](https://github.com/itsHabib/relay/blob/abfae4f5c54d65fda92437137c86ca4ff6de98dc/briefs/2026-09-14-sdlc-as-pipeline.md).
Its head was rechecked as `abfae4f5c54d65fda92437137c86ca4ff6de98dc`.
Michael's raw intent governs: freedom within the work, useful receipts and
mechanical admission at consequential handoffs; no assumed graph.

## Grounding and corrections

Read against Workbench `1c0ba652dfc62ae4687d431581d96ece59b314cb`, Rooms
`b4c1efed18da9fe16583dea66580340d56b8467f`, and dossier
`88fb307a7349ec8144212ebbc5d61d97c3101b5c`.

| Concern | Current owner and implication |
| --- | --- |
| Roles/cards | [Org/Fleet decision](../org-fleet-boundary/spec.md): editable Org prose, optional parent. The brief's `org-work` lifecycle is retired. Roles do not confer authority. |
| Assignments | [Fleet request](../../../cmd/fleet/internal/verbs/request.go): repo-scoped request ID, immutable row, serialization, identical retry. Extend this existing consumer. |
| Receipts | [Fleet receipts](../../../cmd/fleet/internal/verbs/receipts.go): exact clean head, observed session provenance, pass/fail plus observable, append history before latest index. This is already an artifact a later phase can demand; the brief overstates the absence of early artifacts. |
| Continuity/mail | Fleet handoff and mail carry conclusions and requests. Neither is a completion receipt or permission. No new transport is needed. |
| Execution | Rooms `src/matrix.rs` and `src/main.rs`: matrix/case/command identity, result and failure envelopes, cleanup evidence. A Rooms adapter can supply a Fleet receipt after interpreting these outputs; a successful command alone does not establish the requested outcome. No Rooms/cloud execution in this POC. |
| Driver history | [driverstate](../../../driverstate/doc.go) gives leased, hash-chained appends; [its contract](../../../contracts/driverstate/driverstate.go) names driver events, not universal SDLC phases. Do not invent driver events just to journal a Fleet assignment twice. |
| Shared vocabulary | [Charter](../../DESIGN.md): share types/schemas, not another tool's decision code. This single Fleet consumer needs no new shared contract package. Extract one only when another consumer needs the same format. |
| Work catalog | dossier `PROTOCOL.md`: phases are ordered, linear subdivisions; tasks link artifacts, with append/supersede conventions. Link the resulting PR or receipt there when a dossier task exists. It need not orchestrate this boundary. |
| Release | Gate decides from current evidence and an operator grant. Hooks cover recognized commands, not arbitrary effects. No early-phase receipt is a merge grant, and a hook is not an OS sandbox. |

## The contract across the work

These are choices a work brief can make, not five mandatory steps. A small fix
can proceed directly to implementation and its relevant checks.

| Handoff | Useful input | Receiving condition / owner |
| --- | --- | --- |
| Discovery → design | source refs, examples, uncertainties, constraints | Designer has enough current evidence for the decisions at issue. Semantic acceptance stays with the designer. |
| Design → implementation | objective, decisions, API/data changes where needed, success/failure checks, escalation conditions | Assigned worker has a usable scope and permitted environment. Fleet owns placement/accountability; Rooms owns an execution environment when selected. |
| Implementation → verification | exact revision, relevant check receipts, reproduction brief | **This POC:** Fleet checks requested kinds at that revision before queuing the receiving assignment. |
| Verification → release | exact revision, configured reviews/checks, resolved or expressly deferred findings | Existing Gate evaluates evidence and operator authority. This POC cannot satisfy or bypass that decision. |
| Release → follow-up | pinned action and authoritative result | Existing Gate receipts, Fleet continuity, optional dossier links. Publishing or deployment may need its own authority. |

The unit here is one repository + branch + receiving relationship + request ID,
with its input bound to a full commit SHA. Designs shared across branches remain
ordinary linked documents. There is no project-wide mutable phase counter.

## First slice

```sh
fleet request task-branch --id check-1 --as verify --worker SESSION \
  --for lead:project --brief 'Verify the acceptance examples; report counterexamples.' \
  --head FULL_COMMIT_SHA --requires unit,integration
```

`--as` names the receiving relationship (default remains `implementation`).
`--head` and `--requires` must be supplied together. Receipt kinds are explicit
words chosen in the work brief; Fleet does not enumerate SDLC phases or judge
which tests are enough. Omitting these two flags preserves ordinary requests.
There is no automatic risk policy and no way for this packet to grant authority.
The requesting lead chooses this contract. An ordinary request without `entry`
is never evidence-admitted work; a receiver whose brief requires this boundary
must demand the packet. This opt-in POC does not prevent agents with unrestricted
tools from skipping the workflow or choosing weaker requirements.

Before writing the assignment, the receiver:

1. Resolves the branch's current local revision and checks the full expected SHA.
2. Reads each required kind's local receipt history strictly, with no damaged or
   torn tail. The final record must agree with its published latest index and
   name this repository, full head, kind, a clean tree, session and observable.
3. Requires pass for every kind. A later pass can supersede an earlier failure;
   the history retains both. Conflicting publication/history or ambiguous ordering
   is unavailable evidence, never a pass.
4. Under the existing dispatch and branch locks, and a receipt publication lock,
   records the assignment with the full evidence snapshots used at admission.
   Evidence and assignment have one publication point, not two recoverable writes.

The existing dispatch row gains an optional `entry` object with `head`, sorted
`requires`, and selected `receipts`. It retains the evidence even if later receipt
publication changes the latest result. The row is the historical admission
record, not a continuously valid permission token. `fleet status` exposes the
inputs and reports revision drift. An identical retry returns the original
assignment; it does not re-admit, renew, deliver or run anything. A changed
head/requirement/recipient/relationship under the same ID refuses. A new attempt
needs a new ID; an occupied receiving relationship remains protected by the
existing immutable-assignment rule.

Rows remain keyed by (repo, branch, relationship), as ordinary Fleet dispatch
already is. Another relationship may coexist, but the existing live-worker,
stop and branch-lease checks still run. This does not transfer a writer's lease.
The POC deliberately does not add automatic replacement or repair scheduling.

## Freshness, repair and authority

A new commit (including a rebase) invalidates revision-bound evidence for a new
admission. Historical assignments remain inspectable. A verification result that
requires edits produces new implementation evidence and a later assignment when
that receiving relationship can be safely retired by the existing owner; this POC
does not pretend the currently missing correlated request lifecycle is built.

Design/source freshness is dependency-specific: changed requirements or sources
call for reconsideration; time alone does not invalidate immutable code evidence.
There is no universal receipt TTL. A moving remote base, external service state,
review independence and source rights need checks appropriate to that work.
Local branch equality does not establish remote PR freshness.

This is a trusted local-store check. Fleet receipts are authored claims, not
signed attestations that tests ran. The assignment is protected by supported
verbs, not against an operator or process rewriting disk. Filesystem rename
provides process-crash publication, not a new power-loss durability guarantee.
Receipt publication and admission serialize when both use this build; an older
binary or arbitrary file writer is outside that lock. Git can move independently
of Fleet's locks, so consumers must recheck the pinned revision before working.
Mail, task delivery, execution containment and Gate remain separate boundaries.

## Validation and promotion

Use only isolated Git repositories and Fleet/Org state. Exercise actual receipt
production and request consumption; assert no assignment on stale head, missing,
failed, malformed or contradictory evidence. Preserve failed publication inputs;
retry after repair. Race competing requests and receipt publication, simulate a
lost response by replaying in another process, and reject conflicting replay.
Verify a recorded snapshot survives later verdict changes and status stays read-only.

The example must run with the Go toolchain, Python standard library and Git only,
with GitHub and watcher disabled. No model calls, paid hosts or real worker launches.
Publish the exact tested head and outputs on the POC PR. Configured model review
is deferred under Michael's explicit no-fan-out instruction; local checks are not
independent review or merge permission.

Promote only after a real receiving worker uses the packet and it reduces missed
evidence or operator intervention. A broader phase contract, shared schema or
driver integration needs a second concrete consumer and a measured missing behavior.
