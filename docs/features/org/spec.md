# org — Technical Design Document

**Status:** draft / proposal — NOT a build commitment. The artifact we decide from.
**Owner:** @mh
**Date:** 2026-08-21
**Related:** [drive-v0](../../../../drive/docs/features/drive-v0/spec.md) (session classes, scope-bound caps, FR6 "liveness is derived"); [session-claims](../session-claims/spec.md); [execution-runtime](../execution-runtime/spec.md) and `contracts/execution` (the leaf-package shape this copies); `contracts/envelope.go` (the hash-chained record this reuses); [gate DESIGN — tamper model](../../../../gate/docs/DESIGN.md); [agents-as-processes-gleam](../../../../agents-as-processes-gleam/README.md) (the ownership finding); [fm-epoch-replay-laws](../../../../fm-epoch-replay-laws/README.md) (the fold laws to port); [parley](../../../../parley/README.md) (role protocols); dossier project `org`.

> **Reviewers — focus areas:** §4.2 (the tip of a role's chain *is* ownership — the load-bearing claim), §4.6 (grants bind to incarnations, not chain heads), §4.7 (who writes the distilled state, and what a mechanical `mark` may stand in for), §7.3 (crash → takeover → the stale incarnation's next write), §9 (what is committed before the validation gate vs. after).

## 1. Problem & hypothesis

The operator's portfolio is already run by agents; what it lacks is an
**organization**. The shape we want is known and published: two leads that
keep each other honest, a lead per project, five to ten ICs per project each
holding one unit of work for days, everyone messaging directly, the human
talking to the leads — thirty to fifty prompts a day, five percent of them
"something went off the rails."

Every attempt at that shape so far has died at the same point: **an agent
that wakes up does not know it was ever here.** A session gets `CLAUDE.md`,
a memory index keyed by its working directory, and tool signatures. The
state of the work it owns — what it decided, what it promised whom, what is
half-done — exists only in a transcript it cannot see. Evidence on this
machine: 79 worktree-keyed project directories under `~/.claude/projects`
hold zero memory files between them; the hand-off verbs that would fix this
(`/claim`, `/release`, `/continue`) are opt-in, and the drive spec records
two claim events in the two days after they shipped. Instructions are
advice; nobody types the verb.

Two prior experiments fix the frame:

- **switchboard** asked whether a long-lived context needs a long-lived
  process. It does not: a durable-but-unowned baseline recovers the same
  conversation from disk. What residency uniquely supplies is **serialized
  ownership** — the second of two racing turns is refused instead of
  corrupting the journal.
- **drive** made work the first-class object and sessions attachments to it,
  with scope authority as a ledger fact: a driver's cap is stored as a hash,
  re-minting a scope invalidates prior caps, and contention is recorded and
  surfaced rather than silently resolved.

**Hypothesis.** Continuity and ownership are the same fact, and both are a
property of a data structure, not of a process or a prompt. Give every
**role** (lead, project lead, IC) one append-only, hash-chained journal of
distilled context. Define *being* the role as *holding the tip of its chain*.
Then: an incarnation that has not folded the chain cannot exist; two
incarnations cannot both be the role; a dead incarnation is replaced by a
`takeover` that makes the old one's next write illegal; and every grant,
assignment, and message in the org hangs off a chain position that can be
audited. "Wake up as if you were never away" stops being a quality of the
boot prompt and becomes a theorem about a reducer.

**Non-goals.**

- Not an always-on runtime. Roles are durable; incarnations are disposable
  sessions on any host (Claude Code, Agent SDK, `codex exec`).
- Not a new work store. dossier holds tasks; assignments point at them.
- Not a chat surface. The operator's interface is a conversation with the
  leads, hosted wherever they already talk to agents.
- Not consensus. One writer per chain, a supervisor that may take over, a
  keyed anchor for tamper evidence. Ledger, not blockchain.
- Not merge authorization. gate is unchanged and uninteresting here.

## 2. Functional & non-functional requirements

**Functional.**

- FR1 — A **role** is a durable identity with a charter (scope, decides /
  never-decides, capability manifest, supervisor, escalation target) recorded
  as the genesis record of its chain.
- FR2 — A role has exactly one **chain**: append-only, `seq` contiguous from
  1, each record `prev`-linked to the last, sealed by a keyed tip anchor.
- FR3 — **Incarnating** a role is appending a `resume` record whose `prev`
  equals the chain's tip. Any other way of claiming a role is refused.
- FR4 — The runtime writes **checkpoints** on a cadence (pre-compaction,
  stop, every N tool calls, on every outbound `delegate`/`report`/`escalate`)
  without a verb being typed.
- FR5 — **Takeover** by the role's supervisor appends a record that makes the
  displaced incarnation's next append fail with a named refusal.
- FR6 — **Assignments** (role ↔ dossier work unit) and **messages** (role ↔
  role, typed) are chain records; a message carries the counterpart chain's
  `(role, seq, hash)` so both sides can be cross-verified.
- FR7 — **Liveness is derived at read time, never recorded** (drive FR6
  inherited verbatim): tip age, transcript mtime, PR state, process checks.
- FR8 — **Grants** (gate, custody) minted for a role name the incarnation
  they were minted to; a displaced incarnation cannot spend them.
- FR9 — A **fold** of any chain prefix yields a role state good enough to
  act on: goal, current work, decisions with their why, open threads, next
  actions, refs to evidence.

**Non-functional.**

| Dimension | Target |
| --- | --- |
| Size | A checkpoint's `state` ≤ 4 KB; a fold to the tip ≤ 16 KB injected. Bounded by contract law, not convention. |
| Latency | Fold to tip < 100 ms for a chain of 10k records (pure Go over JSONL; snapshot every 256 records). |
| Durability | Append-only JSONL, one `write(2)` per record, torn-tail truncation on read (switchboard's journal). |
| Integrity | Tamper-evident against a state-dir-only writer: `HMAC(key, head ‖ count)` anchor with the key outside the state dir — gate's bounded claim, not non-repudiation. |
| Determinism | The write path invokes no model. The distilled `state` is authored by the incarnation itself at checkpoint time; a mechanical `mark` stands in when it didn't (§4.7). |
| Portability | Chains are files; a role can be incarnated on any machine that has the state dir and the anchor key. No server. |
| Cost | No new subscriptions; agent spend unchanged. Checkpoint authoring costs one short tool call per cadence tick. |

## 3. Architecture overview

```
                    operator  ──talks to──►  lead A  ◄──supervise──►  lead B
                                               │  delegate / report / escalate
                                               ▼
                                    project lead (one per live repo)
                                               │
                                 ┌─────────────┼─────────────┐
                                 ▼             ▼             ▼
                                IC            IC            IC        ← one dossier task each
                                                                         2–3 days unattended

   every node above is a ROLE = one chain:   genesis ─► … ─► checkpoint ─► tip
                                                                            ▲
                                              incarnate = append resume with prev == tip

   ┌─────────────────────────── contracts/org (leaf, no decisions) ───────────────────────────┐
   │  role · assignment · continuity · message · supervision  — types, schema, validate, fold  │
   └──────────────────────────────────────────────────────────────────────────────────────────┘
                 ▲                      ▲                        ▲                    ▲
     drive (runtime: incarnate,   hooks (Claude Code        parley (compiles      custody / gate
     supervise, takeover,         host adapter: checkpoint   role protocols;       (grants name the
     tree-from-chains)            / mark / resume)           observe audits)       incarnation)
                 │
     dossier (work) · channel / SendMessage (transport) · runway (execution) · gate log (receipts)
```

**New:** the five contracts and the fold; the chain as an `Envelope` kind;
the role layer and supervision reducer in drive; the host adapter in hooks;
the `org-delegate` protocol in parley. **Reused unchanged:** `Envelope`'s
`prev`/`hash`/`parents`, gate's anchor, drive's scope caps and liveness
joins, dossier, channel, runway, custody, gate.

The seam that matters: **contracts know nothing about hosts.** A Claude Code
session, an Agent SDK worker, and `codex exec` all produce the same records
through the same verbs. The host adapter is the only per-harness code, and it
is mechanism (when to checkpoint), never policy (what a valid chain is).

## 4. Key decisions & trade-offs

### 4.1 The chain is an `Envelope` kind, not a new ledger format

`contracts.Envelope` already carries `prev`, `hash`, `parents`, `kind`, and a
raw `body`. A continuity record is `kind: "org.<record>"` with a
`contracts/org` body. gate's keyed tip anchor (`HMAC(key, head ‖ count)`,
key outside the state dir) is reused as-is for truncation and rewrite
detection. *Alternative:* a bespoke journal per role (switchboard's
`Envelope(version, sequence, event)`) — rejected: two hash-chain formats in
one portfolio, and the gate log already proved this one under audit.
*Trade-off:* `Envelope.run` is a gate-ism; for org records it carries the
role id.

### 4.2 The tip of a role's chain *is* the role (ownership as data)

To act as a role you append to its chain, and the only legal first append is
a `resume` whose `prev` is the current tip. Two incarnations cannot both hold
the tip: the second append has a stale `prev` and is refused with
`prev_mismatch`. This is switchboard's serialized-ownership result with the
process removed — the refusal comes from the reducer, not a mailbox.

Relationship to drive's caps: they are the same event seen from two planes.
drive's cap says *this session has authority over scope S*; the chain says
*this incarnation is role R and knows what R knows*. A `takeover` record on
R's chain is what mints the successor's cap and revokes the predecessor's
(§7.3). *Alternative:* leases/heartbeats — rejected: liveness is never
recorded (FR7); a lease is recorded liveness by another name.

### 4.3 Roles are durable; incarnations are disposable

A role's identity is its genesis record (the charter) plus its chain. An
incarnation is `(role, incarnation_id, host, session_ref)` — a session on any
harness that currently holds the tip. Roles outlive machines; incarnations
don't outlive a crash. *Consequence:* the org chart is the set of genesis
records; "who is the ivy lead" is a fold, not a config file.

### 4.4 Messages are typed, transport-agnostic, and cross-referenced

`delegate`, `report`, `escalate`, `ask`, `answer`, `takeover_notice` are
record kinds with a fixed body shape. Transport is whatever is at hand —
`channel`, `SendMessage`, a file — but a message exists *as a fact* only once
both chains carry it: the sender's `message.sent` at `(R1, seq_a)` and the
receiver's `message.received` at `(R2, seq_b)`, each naming the other's
`(role, seq, hash)`. An auditor (parley `observe`) can then check that what
the IC says it was asked matches what the lead says it asked, trusting
neither. *Alternative:* treat the bus as the record — rejected: channel is
untyped and unanchored by design; it stays the nimble transport.

### 4.5 Supervision is derived; `takeover` is its only write; leads supervise each other

A supervisor is a role whose charter names the roles it watches. It derives
liveness (tip age + host signals), and its only privileged write is a
`takeover` on a watched role's chain. The two top leads name each other as
supervisor so there is no singleton whose death orphans the org.
*Alternative:* a daemon supervisor — rejected: it is the always-on process
the non-goals exclude, and it is exactly the single point of failure the
two-leads shape exists to avoid.

### 4.6 Grants bind to incarnations, not chain heads

A gate grant or custody grant minted for a role carries `incarnation_id`.
*Alternative considered:* bind to the exact `(seq, hash)` at mint time —
rejected: every checkpoint would invalidate every grant. Binding to the
incarnation means grants survive checkpoints and die on `takeover`, which is
the intended semantics: authority follows continuity, and a displaced
incarnation that still holds a token cannot spend it. **Reviewer call:** this
is additive to custody's grant shape (`cst2_…`) and gate's; confirm the field
lands in both or in a shared `contracts/authority` extension.

### 4.7 Who writes the distilled state — and what a `mark` may stand in for

The `state` body of a `checkpoint` or `handoff` is written by the
incarnation itself: at each cadence tick the host adapter prompts for (or,
on hosts that support it, requires) a short structured `org checkpoint`
call. No model runs inside the hook. When the incarnation didn't author one
— it crashed, hit the context ceiling, or ignored the prompt — the adapter
appends a mechanical **`mark`**: session ref, git state, last N tool calls,
transcript offset. A `mark` keeps the chain continuous and resumable but is
*degraded*: the fold reports `state.degraded = true` and the resumed
incarnation's first act is to reconstruct from refs. Contract law: a
`checkpoint`/`handoff` with empty `state.next` is malformed
(`empty_next`); a chain whose tip is a `mark` is legal; a role with no chain
is not a role (`chain_missing`).

### 4.8 Where it lives

`contracts/org` is a leaf: types, embedded JSON schema, `validate.go`
(contract law), `reduce.go` (the fold and refusals), conformance + fuzz
tests, no decision logic — identical in shape to `contracts/execution`. The
runtime (incarnate, supervise, takeover, tree) is drive, which already owns
session classes and scope caps. The Claude Code host adapter is hooks. The
role protocols are parley. No new repository.

## 5. Data model

**Role id.** `org/<role-slug>` — e.g. `org/lead-a`, `org/ivy-lead`,
`org/ivy-ic-3`. Lowercase, stable, never reused.

**Chain layout.** `<state>/org/<role-slug>/chain.jsonl` plus the anchor
record under the key dir (per gate). Optional `snapshot-<seq>.json` every
256 records; a snapshot is a cache of the fold and is deletable.

**Record kinds and bodies** (every record is an `Envelope` with
`kind: "org.<name>"`, `run: <role id>`, `prev`, `hash`; bodies below).

| kind | body | law |
| --- | --- | --- |
| `genesis` | `charter{ scope[], decides[], never_decides[], escalates_to, supervisor, supervises[], capabilities[] }` | seq 1 only; exactly one |
| `resume` | `incarnation{ id, host, session_ref, started_at }` | `prev` == tip; starts an incarnation |
| `checkpoint` | `state{ goal, doing, decided[{what, why}], open[], next[], refs[] }`, `incarnation_id` | `next` non-empty; ≤ 4 KB; `incarnation_id` == current |
| `handoff` | same as `checkpoint` + `reason: stop\|compaction\|release` | ends an incarnation cleanly |
| `mark` | `mechanical{ session_ref, git{branch, head, dirty[]}, last_tools[], transcript_offset }` | host-authored; degraded |
| `takeover` | `by: <supervisor role>, from_incarnation, reason, evidence[]` | only a role named `supervisor` in genesis; ends the current incarnation |
| `assign` / `release` | `work{ kind: dossier, id }`, `incarnation_id` | one open assign per work id across all chains (checked by drive at write time, law at fold time) |
| `message.sent` / `message.received` | `msg{ type, to\|from, ref{role, seq, hash}, body }` | `type ∈ {delegate, report, escalate, ask, answer, takeover_notice}` |

**Fold output — `RoleState`.**

```
RoleState {
  Role, Charter,
  Tip{ Seq, Hash }, Count,
  Incarnation *{ ID, Host, SessionRef, Since },     // nil when no live incarnation
  State{ Goal, Doing, Decided, Open, Next, Refs, Degraded bool, At seq },
  Assignments[]{ Work, Since },
  Outbox[]{ To, Type, Seq },  Inbox[]{ From, Type, Seq },
  Supervisor, Supervises[]
}
```

Liveness is **not** a field. drive derives it from `Tip` age and host
signals at read time.

**Versioning.** `schema_version` on every body; one version today; a
compatibility rule is decided if and when `0.2.0` exists, from evidence
(execution's stance).

## 6. API contract

**`contracts/org` (Go, leaf).**

```go
func ValidateRecord(r Record) error                 // contract law per kind (§5 laws)
func Reduce(records []Record) (RoleState, error)    // the fold; refuses on any law breach
func Admissible(tip RoleState, next Record) error   // what an appender asks before writing
```

Refusal codes (stable strings, surfaced verbatim by every runtime):

| code | meaning |
| --- | --- |
| `chain_missing` | no genesis; a role with no chain is not a role |
| `genesis_misplaced` | genesis not at seq 1, or a second genesis |
| `seq_gap` | `seq` not contiguous from 1 (switchboard's `SequenceGap`) |
| `prev_mismatch` | `prev` ≠ hash of the previous record — fork, race, or stale incarnation |
| `stale_incarnation` | a `checkpoint`/`assign`/`message.sent` whose `incarnation_id` is not the current one (a takeover or handoff intervened) |
| `not_supervisor` | `takeover` by a role the genesis does not name |
| `empty_next` | `checkpoint`/`handoff` with no next actions |
| `oversize_state` | `state` > 4 KB |
| `anchor_mismatch` | keyed tip anchor disagrees with `(head, count)` — truncation or rewrite |

**Runtime verbs (drive; CLI + MCP, same names).**

```
org incarnate <role> [--host claude|sdk|codex] [--session <ref>]   → RoleState (the boot), or a refusal
org checkpoint <role> --state <json>                                → seq
org handoff   <role> --state <json> --reason stop|compaction|release
org mark      <role> (host adapter only)
org takeover  <role> --by <supervisor> --reason <text> [--evidence ...]
org assign    <role> --work dossier:<project/phase/task>
org send      <role> --to <role> --type delegate|report|escalate|ask|answer --body <json>
org fold      <role> [--at <seq>]                                   → RoleState
org tree                                                            → the org chart with derived liveness
org audit     <role>                                                → anchor + chain check (gate audit's twin)
```

Every verb's stdout is the JSON result; exit codes `0` ok, `1` refused
(code in JSON), `4` error. Refusals are loud and name the remedy, like
custody's.

**Host adapter contract (hooks).** Three hooks, all soft-fail, ≤ 3 s:
`SessionStart` → `org incarnate` and inject the fold; `PreCompact` and `Stop`
→ prompt for `org handoff`, else `org mark`; every N tool calls → prompt for
`org checkpoint`. Memory dir resolution is repo-keyed (worktree → repo root)
so auto-memory stops fragmenting across 79 buckets; this is a one-line fix
the adapter ships with.

## 7. Key flows

### 7.1 Incarnate (the boot)

1. Host starts a session for role R. Adapter calls `org incarnate R`.
2. Runtime reads the chain, verifies the anchor, folds to the tip.
   `chain_missing` → refuse (a role must be chartered first).
3. If the fold shows a live incarnation (tip is a `checkpoint` younger than
   the liveness threshold, host signals agree) → refuse with `prev_mismatch`
   and the current incarnation's id. Incarnating is not taking over.
4. Append `resume{ id, host, session_ref }` with `prev = tip.hash`.
5. Inject the fold as the session's first context: charter, state (flagged
   `degraded` if the tip was a `mark`), assignments, inbox, supervisor.
6. The incarnation's first turn is indistinguishable from the prior one's
   next turn. That sentence is the validation gate (§11).

### 7.2 Checkpoint cadence

Every N tool calls, on `PreCompact`, on `Stop`, and immediately before any
`message.sent`, the adapter asks the incarnation for a structured state. It
appends `checkpoint` (or `handoff` on stop/compaction). If no state arrives
within the hook budget, it appends `mark`. The chain never has a gap longer
than one cadence tick.

### 7.3 Crash → takeover → the stale write

1. IC `ivy-ic-3`'s host dies mid-task. Its tip is a `checkpoint` at seq 41.
2. Supervisor `ivy-lead` derives liveness at read time: tip age > threshold,
   transcript mtime stale, no process. It appends
   `takeover{ by: ivy-lead, from_incarnation: inc-7, reason, evidence }` at
   seq 42, then incarnates a replacement (`resume` at 43) — or spawns a
   worker that does.
3. drive re-mints the scope cap for the new incarnation and revokes inc-7's;
   custody/gate grants naming inc-7 are dead (§4.6).
4. inc-7 was not dead, only slow. Its next `checkpoint` arrives with
   `prev = hash(41)` and `incarnation_id = inc-7`: refused, `prev_mismatch`
   (and `stale_incarnation` on the body). The refusal names the successor.
   inc-7's host adapter stops the session cleanly. Nothing was corrupted;
   nothing needed a lock.

### 7.4 Delegate

1. `ivy-lead` folds its chain, reads its scope (a dossier phase), and picks a
   task.
2. `org assign ivy-ic-3 --work dossier:ivy/p2/t4` — refused if any chain
   holds an open assign for that work id.
3. `org send ivy-lead --to ivy-ic-3 --type delegate --body {...}` → a
   `message.sent` at `(ivy-lead, 88)`; transport delivers; the IC's next
   incarnate or checkpoint tick appends `message.received` naming
   `(ivy-lead, 88, hash)`.
4. parley's `org-delegate.parley` says the only legal reply to `delegate` is
   `report` or `escalate`; `observe` over both chains flags anything else.

### 7.5 Report / escalate routing

`report` goes to the sender's supervisor chain. `escalate` carries a tier;
the lead's charter says which tiers it decides and which it must forward.
The operator sees only what reaches a lead's `escalates_to: operator` — the
five percent.

### 7.6 Cross-chain verification

`org audit` over two chains: for every `message.sent` on A naming
`(B, seq, hash)`, B's record at `seq` has that hash and is a
`message.received` naming A's record back. Either chain can be lying; both
can't agree on a lie without the anchor key.

## 8. Concurrency / consistency / failure model

- **One writer per chain at a time**, enforced by `prev` — not by a lock.
  The file lock around the `write(2)` is a mechanism for atomic appends, not
  the ownership model.
- **Torn tail** → truncate on read, recount, continue (switchboard). A torn
  tail after a `resume` means the incarnation never existed; it re-incarnates.
- **Anchor key missing** → `anchor_key_missing`, loud; minting is a
  first-append concern only (gate's rule).
- **Host down, chain healthy** → nothing is lost; the next incarnation folds
  to the tip. This is the whole point.
- **State dir lost** → the role is lost. Chains are small files; back them up
  with the rest of the state dir. A future phase may replicate the anchor to
  the gate log as a receipt.
- **Clock skew** → liveness is a heuristic over several signals; the chain
  itself never depends on wall time for correctness.
- **Two supervisors race a takeover** → second `takeover` has a stale
  `prev`; refused. The two-leads topology makes this the common case, and it
  is handled by the same rule as every other fork.

## 9. Rollout / implementation plan

| Phase | Goal | High-level tasks | Depends on | Gate |
| --- | --- | --- | --- | --- |
| **p0 charter** | This document reviewed and locked | Reviewer panel; fold findings; decide §10 | — | design locked |
| **p1 contracts/org** | The leaf package | Types + embedded schemas for the 5 contracts; `validate.go`; `reduce.go` (fold + refusals); conformance, property, and fuzz tests; `Envelope` kind registration; hygiene CI (leaf imports nothing) | p0 | `go test ./contracts/org/...` green; mutation tests for each refusal |
| **p2 laws** | Machine-checked chain laws | Port `fm-epoch-replay-laws` fold ≡ checkpoint-resume ≡ replay; add contiguity, single-tip, resume-requires-tip, takeover-invalidates-stale; adversarial reducer that admits a fork must fail | p1 | `lake build`, axiom audit, counterexample fixture |
| **p3 host adapter** | One role resumes mid-thought on Claude Code | `org` CLI (incarnate / checkpoint / handoff / mark / fold / audit) over `contracts/org`; hooks: SessionStart / PreCompact / Stop / N-calls; repo-keyed memory; chain state dir + anchor | p1 | **VALIDATION GATE** — §11 test 1 |
| p4 drive runtime | Roles over driver/worker | genesis/charter verbs; supervision reducer; `takeover` mints/revokes caps; `org tree`; grants carry `incarnation_id` (custody + gate) | p3 ✓ | §11 test 2 |
| p5 parley | Legal conversations | `org-delegate.parley`; `grants`/`effects`/`receipt` in the algebra; `observe` over chain pairs | p4 | real chains classify clean |
| p6 the slice | The org at 1/10 scale | one lead, three ICs, one repo, one day; kill an IC; count operator prompts | p4, p5 | §11 test 3 |

Committed: p0–p3. p4–p6 are gated on p3 proving the thesis for a single
role. Rough scope: p1 ≈ execution's size (~1.5k weighted LOC incl. tests);
p3 ≈ 600 (bash + Go CLI); p2 ≈ one Lean file plus adversarial twin.

## 10. Open questions

1. **Grant binding field** (§4.6): extend custody's and gate's grant bodies
   separately, or introduce `contracts/authority` with an `incarnation`
   field both adopt? Leaning shared; reviewer call.
2. **Checkpoint authorship on hosts without a prompt surface** (`codex exec`,
   SDK workers): the adapter can only `mark`. Is a chain of marks with
   occasional agent-authored handoffs good enough for ICs, or does the SDK
   host need a mandatory end-of-turn `org checkpoint` tool call?
3. **N for the tool-call cadence.** Start at 25; measure state drift between
   checkpoints in p3 and tune.
4. **Where the state dir lives across machines.** Today `~/dev/*-state`
  siblings; a role incarnated on a second machine needs the chain and the
  anchor key. Sync mechanism is out of scope here but must be named by p4.
5. **Operator as a role?** The operator's decisions (grants minted, parks
   resolved) already land in gate's log. Whether the operator gets a chain
   (so `escalate` has a receiver with a fold) is a p4 question.
6. **Naming.** `org` is the working name; `roster` is taken.
7. **Does any part of the org need a live actor?** switchboard showed the
   resident form's one unique win is serialized ownership, which the chain
   now supplies without a process. The remaining candidate is message
   *delivery*: a per-role inbox that must serialize concurrent senders. The
   chain already serializes `message.received` appends, so the bet is no —
   channel's locked `write(2)` is enough transport. If p6 shows lost or
   reordered delegations, switchboard's session actor (Gleam/OTP, journal
   replay, `SequenceGap`) is the ready-made answer for that one seam, and
   parley's Gleam bus already runs in its style.

## 11. Validation plan

Three binary tests, one per gate, no vibes.

1. **Resume mid-thought (p3 gate).** In each of ivy, ship, and gate: charter
   one role; run a real task for ≥ 30 tool calls; kill the session without
   warning; incarnate fresh. Ask "where were we and what's next." Pass if
   the answer names the current work, the last decision and its why, the
   open threads, and the next action — and a blind reader cannot tell the
   transcript was cut. Run with the chain and without (today's cold open) on
   the same three tasks; the delta is the result.
2. **Ownership (p4 gate).** Two incarnations of one role race: exactly one
   holds the tip; the other's write is refused with `prev_mismatch`. A
   supervisor takeover makes the displaced incarnation's grant unspendable
   at custody — checked by a refused request in custody's log.
3. **The slice (p6 gate).** One lead, three ICs, one repo, one working day.
   Pass if: the operator sends ≤ 10 prompts; an IC killed mid-task is taken
   over and its replacement resumes from the chain without operator input;
   `org audit` over every chain pair is clean; parley `observe` classifies
   every trace as complete or stalled, none deviating.
