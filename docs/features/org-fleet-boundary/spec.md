# One Fleet workflow

**Status:** proposed decision, revised 2026-09-10 for final review.
**Scope:** the destination and migration contract. This PR changes documentation,
not installed binaries, hooks, state, or merge authority. The implementation in
[#295](https://github.com/itsHabib/workbench/pull/295) is independently useful.

## The decision

An agent uses Fleet to find its work, communicate, and recover context. There is
one authoritative assignment for a piece of work. Starting another session does
not require the operator to reconstruct the previous conversation or reconcile
an Org claim against a Fleet row.

Fleet is the destination for both runtime observations and continuity. Org may
remain an internal compatibility tool while its callers and history migrate. It
is not a second long-term workflow. Gate remains the separate merge-authority
boundary; continuity does not migrate into Gate's ledger.

This replaces the earlier versions of this document, which preserved two
command surfaces and added holder hooks, contact restrictions and automatic
launching to connect them. The operator chose a simpler mail contract and then
one Fleet workflow. The two architectural opinions now agree on that destination:
[Fable](https://github.com/itsHabib/workbench/pull/297#issuecomment-5618834137)
and [Astra](https://github.com/itsHabib/workbench/pull/297#issuecomment-5618834596).
The latter supersedes the earlier recommendation to merge the two-tool design.

## The normal experience

1. A lead assigns work once through Fleet. The assignment identifies the work,
   accountable lead, and intended result. A seat and a session identify where
   execution happens; neither creates another assignment.
2. On startup, the session receives its assignment, relevant role instructions,
   the latest useful handoff and unread mail. It does not run Org attach, claim,
   yield or release merely to become useful.
3. The worker sends a question to its lead or another identified address within
   the tenant. The recipient reads and acknowledges it. Reading and acknowledging
   do not complete work, and a message labelled an order grants no authority.
4. If a recipient is absent, mail waits. When a replacement session starts, the
   existing hook path presents it. This workflow does not require a launcher.
5. When work changes or stops, the agent leaves a concise handoff: conclusion,
   relevant evidence, and what remains. An idle tick need not manufacture a
   checkpoint. The system records provenance and supplies the handoff next time.
6. Existing receipts determine whether the requested result has evidence.
   Existing leases protect shared resources, and Gate authorizes a merge.

The first acceptance exercise is this entire exchange, including a replacement
sender retrying a message and a replacement recipient continuing the work. The
operator should neither relay messages nor repair bookkeeping.

## One owner for each fact

| Fact | Authoritative owner | What continuity may do |
|---|---|---|
| Work, accountable lead, intended result | Fleet assignment/dispatch record | Reference the record; never create a second claim |
| Session identity and liveness | Fleet harness observations | Identify the author of a handoff |
| Branch and resource ownership | Fleet leases | Display ownership; never acquire it by writing prose |
| Role instructions and scope | Fleet per-role definition after cutover; existing Org charter before it | Read the selected authority; never maintain two editable definitions |
| Handoff conclusion and remaining questions | One continuity record per handoff | Preserve authored text, evidence references and session provenance |
| Message and acknowledgement | Fleet mail | Reference the message; do not mirror each send into a second ledger |
| Evidence of completion | Existing exact-head receipts | Link the evidence; never turn a claim of success into a passing receipt |
| Permission to merge | Gate | Display the decision; never manufacture authority |

This is a destination, not a claim that today's Fleet already has one record.
Currently `cmd/fleet/internal/verbs/work.go` writes dispatch rows, while
`internal/verbs/views.go` writes slot placements with overlapping brief and
accountability fields. The target makes the dispatch/work record authoritative;
placement references that record and owns only placement. Request records carry
retry/admission identity, not another independently editable owner. The cutover
must also cover direct `fleet assign` callers before removing legacy fields.

An observation and an authored statement may appear in the same view or store,
but must remain distinguishable. A process exit is observed; “the work is done”
is an assertion until the required evidence exists. Fleet already contains
operator-authored dispatches and agent-authored mail. “Observed versus authored”
is a provenance distinction, not a reason to maintain separate products.

A role is a durable responsibility. A seat is a named execution location. A
session is a temporary process using that location. More than one seat can carry
the same role kind. An exclusive machine resource is a lease, not another role.

## What stays small

### Mail

Keep #295's simplified same-tenant send/read/ack contract, full message bodies,
retry identity across replacement sessions, and existing hook notifications.
Retain address validation, tenant isolation, and acknowledgement identity checks.
There is no contacts file, mandatory charter traversal, dispatch prerequisite,
intent wrapper or automatic launch in the mail path.

Topology can help discover an address. It does not grant permission to send.
A worker's brief supplies its accountable lead; useful communication outside a
reporting tree is not automatically a violation. Role-specific instructions may
name where a particular report belongs, without imposing a universal one-hop
restriction or one-send-per-tick quota.

Give individual seats distinct addresses before relying on concurrent workers
with the same role kind. The address identity includes tenant and address type
(role or seat), with collision-free storage encoding. An ambiguous legacy role
address must return the concrete alternatives instead of choosing a recipient.
A seat address remains stable across session replacement, but an outstanding
message remains tied to its work reference: reusing the seat must not silently
turn an old order into the next assignment. The recipient checks the referenced
work before acting; no send-time dispatch policy is reintroduced.

Existing mail and acknowledgements must survive the address transition. An
explicit, repeatable migration must map old identities to new ones or report an
ambiguity without moving data. Do not silently reinterpret a role mailbox as a
seat mailbox or transfer retained mail between tenants.

### Role definitions

Fleet's destination for a role-specific charter is one versioned definition at
`$FLEET_STATE/roles/<tenant-sha256>/<role-sha256>.json`, with lowercase SHA-256
hex path components and the full tenant/role identity checked in the record.
This is a proposed Fleet schema, not an already implemented store:

- `schema: fleet-role.v1`, `tenant`, `role`, and `kind` identify the definition.
- `instructions` contains the role-specific instruction text; `terms` preserves
  the effective scope, supervisors, and other operational terms from the
  validated source, including accepted recharters rather than just genesis.
- `source` records the legacy role identity and validated source revision/digest
  used for migration. It is provenance, not an alternate writable charter.

Fleet role configuration owns this record after cutover. Kind-wide lane manifests
remain reusable defaults; they are not a substitute for role-specific scope or
terms. `roles.map` remains a directory/seat binding to the tenant and role, not
another definition. Startup reads the selected per-role definition and references
its kind defaults. It must not separately inject a conflicting Org charter or
silently widen role terms through a kind default. Role terms do not mint Gate
merge authority.

Before a cohort switches, obtain its effective charter and retained instructions
through Org's validated output, stage the Fleet definition, and compare every
consumed term and instruction against that source. Each operational term must
have an explicit Fleet consumer/enforcement mapping; storing an ignored term as
metadata is not preservation. Unknown, missing, conflicting, or unsupported
terms keep that cohort on Org until resolved. This does not block mail or other
cohorts. Verify startup and the cohort's actual callers against the staged record
in scratch state before changing the live authority selection.

The migration report records one selected definition owner per tenant/role and
the source/destination revisions. Quiesce the cohort's definition writers, make
the verified Fleet record authoritative, switch its readers and writers, and
make the retained Org definition read-only history. Do not retire that Org
consumer until the readback matches and its old writer is disabled. Repeating
the cutover is a no-op. Rollback quiesces both writers and verifies that the latest
effective Fleet definition, including post-cutover edits, can be restored with
compatible Org consumer mappings before restoring its writer. If those changes
cannot be represented, rollback refuses rather than reverting to a stale charter.
There is no steady-state synchronization between these definitions.

### Continuity

Reuse existing startup context and last-word mechanisms before adding new
commands. The existing seams are `internal/fleet/hook.go` SessionStart,
`internal/fleet/session.go` handoff/last-word/assignment rendering, and
`internal/verbs/keys.go`'s `fleet handoff`. The `fleet assign` → `AssignmentLine`
seat notification is stamped as delivered and can disappear for a replacement session. Current assignment
visibility must not be consumed by the first session that reads it, and must be
checked against the seat's current work before display. The useful surface is the context a session receives and a way to leave
an intentional handoff. A captured final response is identified as captured text;
it is not silently promoted to an independently verified conclusion.

The initial implementation may obtain legacy context through Org's validated
CLI/JSON output. Select and byte-bound fields rather than injecting the entire
legacy boot text, which includes held-work instructions. It must not import Org's private decision logic or independently
fold its raw journal. Existing Workbench module boundaries still apply. A later
move of an implementation into Fleet removes the old decision owner instead of
creating a second reducer.

Do not make routine continuity writes depend on a lead holding an Org incarnation.
Do not change the Org kernel globally just to make those writes possible. Use the
Fleet continuity path for new work and keep existing Org work on its original
path until the migration for that work is explicit. A missing legacy reader can
be reported as unavailable context without blocking independent Fleet mail.

A question and answer normally live in mail. A handoff may reference a question
that still matters. If structured outstanding-question tracking is needed by a
real consumer, add one representation with a reply reference; do not require an
agent to write the same question as both mail and an Org escalation. Existing
unresolved Org obligations remain visible during migration and are not discarded.

A hash-linked journal can detect edits relative to a trusted retained digest; it
does not by itself fence a displaced writer or prevent an entire journal from
being rewritten. Keep existing history and integrity evidence. Do not introduce
new hash-chain, signing or fencing machinery without naming the consumer and
failure it protects against. Ordinary serialized publication and explicit
provenance remain separate from execution authority.

### Retry and recovery

Keep automatic replay confined to operations with a tested idempotency contract.
For one logical request, a retry reuses the same stable ID and payload and returns
the original result. A changed payload under that ID refuses. A replacement
session obtains that ID from the retained request or derives it deterministically
from the same work/event identity; generating a fresh ID is a new request.

Adding an `--id` flag alone does not establish restart safety. Any extension to
assignment/dispatch must exercise interruption before and after publication,
concurrent retries and changed-payload refusal. Successful publication and any
associated assignment effect need a documented recovery boundary. Do not remove
an existing recovery record until its replacement has that evidence.

### Directory configuration

Role configuration must not leak into unrelated nested worktrees. Managed harness
settings need an identifiable owned section so reconfiguration replaces that
section and unbinding removes only what Fleet owns. Preserve user-owned settings.
Validate both current parent configuration and nested worktree behavior in the
migration. Do not delete inherited denies indiscriminately or change installed
root bindings as a side effect of merging this document.

## Moving from two systems to one

This is an incremental retirement, not two permanent systems hidden behind a
wrapper. Each migrated fact has one writer and a verified cutover. A compatibility
reader is temporary and does not synchronize two writable authorities.

1. **Finish standalone mail (#295).** Review and merge the simplified head under
   ordinary checks and Gate. #296 is the superseded comparison, closed with credit.
   No other step in this document blocks mail.
2. **Add seat addressing and useful startup context.** Workbench implementation
   PRs extend the existing Fleet surfaces and prove the replacement-session
   exchange. Keep the additions separate if combining them obscures review.
3. **Inventory and migrate actual Org consumers.** Enumerate installed hooks,
   MCP/CLI callers, role definitions, held work, open intents/escalations and
   retained history. Separate live state from fixtures. Record identities and
   destinations in a migration report; do not publish secrets or private bodies.
   Existing Org-held work stays authoritative there until its explicit cutover;
   do not create a competing Fleet assignment during the transition.
4. **Cut over one bounded cohort.** In a scratch copy first, map the cohort's
   definitions to `fleet-role.v1` and obligations to the defined work/continuity
   records, preserve legacy
   source references and history, and verify before/after counts and contents.
   Quiesce the cohort's writers for the migration; after cutover only Fleet writes
   its migrated facts. Re-running the migration is a no-op. Unmapped or conflicting
   items remain reported, not dropped. Rollback must restore one writer, not turn
   dual writing on. Runtime installation is a distinct action from merging code.
5. **Retire migrated callers and finally Org.** Update instructions, hooks and MCP
   surfaces so agents use only Fleet for migrated work. Preserve the legacy
   journal as readable history. Remove the old command only when the inventory
   has no active consumer or unresolved obligation needing it. Keep unrelated
   Org users compatible until their own migration is verified.

Gate's state, grant custody and merge policy are outside this migration. Moving
continuity into Fleet explicitly replaces the earlier proposal to put it into
Gate's ledger. Existing Gate controls remain unchanged.

## Acceptance, using the existing sandbox

Run the existing sandbox with an isolated build and isolated state. Record the
exact binary/source head, harness adapter, scenario and resulting artifacts.
Keep the live sandbox owner in control of its running sessions; do not substitute
shared production state for a fixture or run competing orchestration loops.

| Exercise | Evidence required |
|---|---|
| Worker asks; lead session ends; replacement answers | Full message and handoff recovered; answer reaches worker; no operator relay |
| Sender ends after publication and retries from replacement | One message, original provenance and acknowledgement preserved |
| Two seats share a role kind | Distinct inboxes; neither can acknowledge the other's message |
| Seat is reused for another assignment | Old mail stays attributable to old work; no implicit reassignment or execution |
| Cross-tenant or ambiguous address | Explicit refusal; no read, ack or ownership transfer |
| Org reader missing or charter unavailable | Independent mail still works; missing continuity is visible |
| Continuity contains a claimed success | View preserves authored provenance; no fabricated completion receipt |
| Migration repeats or stops partway | No lost history/obligation, no duplicate assignment, one authoritative writer |
| Role binding changes or is removed | User settings preserved; no role restrictions inherited by unrelated worktrees |

Report operator interventions, missed context, duplicate effects and ambiguous
ownership. Passing store tests is not evidence of a successful agent exchange;
a successful exchange is not proof of unattended launch safety. Use the observed
failures to choose the next implementation, rather than making every future
mechanism a prerequisite for running the exercise.

## Deferred decisions

- Automatic launch: only after a demonstrated caller, in a separate contract.
  Run 3 exposed multiple launches per mailbox batch and a live-but-idle recipient
  that blocked progress. Those are acceptance cases, not proof that an idle
  process is dead. A launcher must coordinate competing starts and any existing
  occupant before claiming exclusive execution.
- Cross-machine mail transport and delivery to the human operator.
- A new topology reader, if existing role context does not meet a real discovery
  need. It is never a mail dependency.
- Structured question tracking or a stronger continuity integrity scheme, if a
  consumer needs more than retained mail, handoffs and existing legacy evidence.

## Evidence and review history

The workload evidence is the
[2026-09-09 rehearsal](https://github.com/itsHabib/fleet-demo-sandbox/blob/main/docs/REHEARSAL-2026-09-09.md),
its `docs/RUN-CONTRACT.md` and `docs/RUN-CONTRACT-v3.md`, and scorecards under
`runs/`. These demonstrate session-ID routing friction, duplicate seat identities,
root configuration leakage and the cost of lead ceremony. They do not establish
that every mechanism proposed by earlier drafts is necessary.

The implementation baseline is `cmd/fleet/README.md` and #295 at `60bbbf1`;
legacy behavior is described by `cmd/org/README.md`, its guide pair and
`contracts/org`. The related cc-skills proposals are
[#62](https://github.com/itsHabib/cc-skills/pull/62) and
[#65](https://github.com/itsHabib/cc-skills/pull/65). This document supersedes their
conflicting two-workflow, one-hop and mandatory-per-tick ceremony direction for
the migration described here; changing their installed callers is separate work.

The [review disposition](review-disposition.md) records the substantive prior
findings and what this rewrite removes, addresses or leaves to implementation.
The operator explicitly authorized a new review of the finished work on
2026-09-10. That does not turn earlier-head reviews into approval of this head or
change Gate grant authority.
