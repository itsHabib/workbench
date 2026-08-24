# org — Technical Design Document

> **SUPERSEDED by [`vision.md`](vision.md).** Kept for its review history and
> for the §5 record table and §6 refusal codes that `contracts/org` was built
> from. Where the two disagree, `vision.md` wins — notably the §3.9 state
> machine, which added `claim` / `yield` / `complete` as structural kinds,
> settled `abandon` as a claim terminal, and dropped `handoff` in favour of
> release-then-attach. Do not start new work from this file.

**Status:** draft / proposal — NOT a build commitment. The artifact we decide from.
**Owner:** @mh
**Date:** 2026-08-21 · **v2** 2026-08-22 (review round 1 folded; four bakeoff kernels read and adopted)
**Related:** [drive-v0](../../../../drive/docs/features/drive-v0/spec.md) (session classes, scope-bound caps, FR6 "liveness is derived"); [session-claims](../session-claims/spec.md); [execution-runtime](../execution-runtime/spec.md) and `contracts/execution` (the leaf-package shape this copies); `contracts/envelope.go` (the hash-chained record this reuses); [gate DESIGN — tamper model](../../../../gate/docs/DESIGN.md); [agents-as-processes-gleam](../../../../agents-as-processes-gleam/README.md) (the ownership finding); [fm-epoch-replay-laws](../../../../fm-epoch-replay-laws/README.md) (the fold laws to port); [parley](../../../../parley/README.md) (role protocols); the 21 Aug bakeoff kernels `hack-branchroom`, `hack-mandate`, `hack-obligation`, `hack-proofline` (§4.9); dossier project `org`.

> **Reviewers — focus areas:** §4.2 (the tip of a role's chain *is* ownership; lock scope; the cap ledger as a derived cache), §4.6 (grants carry a fence — a change to gate's trust model, not an additive field), §4.9 (four of these laws are already built; port rather than rewrite), §7.3 (the re-reading stale writer — the case that decides §6's check order), §7.6 (absence-based audit and the suppression attack), §9 (what is committed before the validation gate vs. after).

> **What changed in v2.** Nine findings from review round 1 are folded, and four
> decisions replace v1 hand-waves: the append critical section is now specified
> (§4.2); the cap ledger is demoted to a derived cache so the two-plane write
> need not be atomic (§4.2); liveness thresholds become a `next_due` the
> incarnation declares (§4.2); and grant revocation gets a real mechanism — a
> fencing token that needs no replication and no runtime dependency on the org
> subsystem (§4.6). Reading the four bakeoff kernels then produced three
> corrections v1 got wrong: the stale-writer case that actually matters is the
> one that re-reads the tip (§7.3), which reverses §6's check order; incarnation
> ids must be digests rather than counters (§5); and the cross-chain audit must
> reason about absence or it is a suppression attack (§7.6).
>
> §4.11 is new. Every incarnation is disposable and the chain is what makes it
> so; a
> simpler per-tick architecture — cron'd sweeps with the world as the source of
> truth — is correct for maintenance, and the two are not rival designs but
> different **role kinds** whose chains differ only in density. It adds the law
> governing what a record may hold (*if it can be derived, don't record it*),
> a `wip_limit` bounding production the way grants bound authority, and the
> observation that an organization's work item is usually a ticket or a doc
> rather than a pull request — which is where a single external store stops
> knowing the state and the chain earns its place.
>
> §4.12 is new and answers a question the document had been treating as a
> single line in a table: can anyone actually operate this? It adds the one
> thing that was genuinely missing — a correction path, since an append-only
> chain with no `annul` means a wrongly-recorded decision poisons every future
> fold forever — plus refusals that carry their remedy as data, an `explain`
> and a `doctor` in the shape the rest of the portfolio already uses, and the
> observation that checkpoint quality is the design's biggest untested
> assumption. Each is gated by §11.1c–1d rather than asserted.

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

The two halves of that evidence are one mechanism, not two symptoms. Memory
is keyed by working directory, so a worktree session accrues state into a
bucket nothing will ever read again — which makes continuity *invisible*,
which is why the hand-off verbs feel like bureaucracy nobody types. Fixing
the key is not a footnote in the host adapter (§6); it is the smallest
version of this document's whole claim, and the org substrate gets it as a
consequence of making the chain — not the directory — the thing state hangs
off.

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
- Not a new work store. dossier holds tasks; assignments point at them. More
  strongly (§4.11): the chain records only what has no other home.
- Not a scheduler, and not a replacement for cron. Sweep work — dependency
  bumps, dead-code sweeps, test pruning, alert triage — runs on the simpler
  per-tick architecture in §4.11. The *role* that owns such an area still has
  a chain; its ticks do not.
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
| Operability | Somebody who was not here can diagnose and repair a role using only the tools — no author, no transcript. Every refusal returns `{code, message, remedy, evidence}`; every mistake is correctable by append (`annul`); `explain` and `doctor` answer "what is going on" and "what is wrong" without joining stores by hand. Gated by §11.1d, not asserted (§4.12). |
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
(§7.3). *Alternative:* leases/heartbeats — rejected twice over: liveness is
never recorded (FR7), and a lease needs a clock-holding daemon, which is the
always-on process the non-goals exclude.

**The `prev` check is detective; the lock is what makes it preventive.**
Without a lock, two incarnations can both read `tip = hash(41)`, both build a
record with `prev = hash(41)`, and both append: the file now has two records
at seq 42, and the *next reader* refuses with `seq_gap` — but both writers
believed they won, which is precisely the corruption switchboard's baseline
suffered. So the append verb takes an exclusive advisory lock on that one
chain file across the **whole** critical section — acquire → read tail →
`Admissible(tip, next)` → write → fsync → release — not just the write. The
two mechanisms are deliberately redundant: the lock prevents the fork, and
`prev` still detects it if the lock is ever bypassed (a hand-edited file, a
second implementation, a stale-lock steal). Stale-lock handling reuses gate's
`lock.go` staleness clock, including its known TOCTOU takeover race — bounded
here because a thief still has to pass the `prev` check.

**Authority is computed, not stored — the cap ledger is a derived cache.**
The reviewer asked whether the chain append and drive's cap revocation are
atomic. They are not, and making them atomic would require the contracts leaf
to reach into drive's ledger, which the boundary law forbids. Instead the
divergence is made *harmless* rather than merely detectable: **the chain is
authoritative for role ownership; drive's cap ledger is a derived cache that
may lag but can never grant more than the chain allows.** Every authority
decision is a join of (cap ledger, chain tip) in which the chain wins, so a
`takeover` that lands on the chain while the cap revoke fails leaves the
displaced incarnation with a cap that no reader will honor. The cap ledger
exists for speed and for drive's own bookkeeping, never as a second source of
truth.

**Liveness thresholds are declared, not configured.** §7.1 needs to know
whether a tip is stale, and a global threshold would have to be tuned against
the checkpoint cadence — the reviewer correctly noted these are one
calibration, not two. Rather than couple them, every `checkpoint`, `handoff`,
and `mark` carries `next_due`: a wall-clock deadline the incarnation commits
to writing its next record by. Staleness is then a comparison against a
timestamp already in the record, needing no global tuning, and an incarnation
about to do something slow extends its own deadline rather than tripping a
supervisor. A missed `next_due` is *evidence* of death, joined with host
signals (§7.3) — never proof on its own, since FR7 still forbids treating a
recorded timestamp as recorded liveness.

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

### 4.6 Grants carry an incarnation and a fence

A gate grant or custody grant minted for a role carries two new fields:
`incarnation` (which incarnation it was minted to) and `fence` (the role
chain's `seq` at mint time). *Alternative considered:* bind to the exact
`(seq, hash)` — rejected: every checkpoint would invalidate every grant.
Binding to the incarnation means grants survive checkpoints and die on
`takeover`, which is the intended semantics.

**The fence is how a verifier refuses a displaced incarnation without asking
anyone.** "A displaced incarnation cannot spend its grant" was a goal
statement in v1, not a design. Three mechanisms were considered: gate queries
the org chain at verify time (couples gate's availability to the org state
dir, which a CI-hosted required check will not have); drive pushes a
revocation to gate and custody at takeover (the same two-plane write, one
level down — it can fail); or the takeover is replicated into gate's log as a
record it already knows how to read (needs the replication step, which can
also fail). All three leave a window.

The fence closes it without replication, using the classic fencing token: a
verifier keeps a **per-role high-water mark** of the highest `fence` it has
honored, and refuses any grant presenting a `fence` below it. Because a
`takeover` advances the chain's `seq`, the successor's grants necessarily
carry a higher fence than the predecessor's; the first time the successor
does anything at all, the predecessor's grants become permanently
unspendable — at every verifier independently, with no message passing
between planes. The residual window is "after the takeover, before the
successor's first spend," and `org incarnate` closes even that by bumping the
fence as a no-op on resume. The high-water mark is one integer per role in
each verifier's existing state; gate and custody gain no dependency on the
org subsystem, only a monotone comparison.

**This is a real change to gate's trust model, not an additive field.** Gate
grants are currently bounded by scope, tier, and time. A fence adds
*ordering* as a fourth bound, and a stored high-water mark is new mutable
state in the verifier. It is small, but it should be reviewed as a change to
what gate promises, not as a schema addition.

**Decision on where the fields live** (v1 left this to the reviewer): a
shared `contracts/authority` extension carrying `Incarnation` and `Fence`,
imported by both custody and gate — not parallel fields in each. The
criterion is that this is one invariant ("authority follows continuity"), and
an invariant maintained in two places is an invariant that will drift; a
single type gives one import, one conformance test, and one place to state
the monotonicity law. The cost is coupling between two grant shapes that may
later diverge, which is accepted because the shapes are stable and the
invariant is load-bearing.

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

**The hook budget is a hard timeout, never a wait.** The adapter asks for a
structured state and, when the ≤ 3 s budget expires, appends a `mark` and
returns. It never blocks the session and never interrupts an in-flight model
call to get one. A live-but-busy incarnation therefore produces marks, which
is correct: a mark says "still here, nothing authored," which is exactly true.

**`mark` is exempt from `empty_next`, and the asymmetry is deliberate.** A
`checkpoint` is authored by the incarnation and is a statement of intent, so
having no next action is malformed. A `mark` is authored by the host and is a
statement of continuity, not intent — it has no `next` field at all. A reader
of `validate.go` should find that stated rather than inferred.

**The cold-reconstruction floor.** A `mark` points at a transcript that may
be gone — the host crashed, the runner was ephemeral, the machine is another
machine. So the guarantee is stated in two tiers rather than assumed. After a
`mark` tip, a resumed incarnation is **guaranteed**: its role identity and
charter; its open assignments; the last *authored* state from the most recent
`checkpoint`/`handoff`, however old; and the mark's git facts (branch, head,
dirty paths), which are recoverable from the repository itself even with no
transcript. It is **not** guaranteed the reasoning since that last authored
state. That is the honest floor: continuity of *commitment*, not of thought.
It also sets the checkpoint cadence's real job — the cadence bounds how much
thought a crash can cost, and §10.3's N is that dial. §11 test 1 therefore
runs the cold case (transcript deleted before resume), not only the warm one.

### 4.8 Where it lives

`contracts/org` is a leaf: types, embedded JSON schema, `validate.go`
(contract law), `reduce.go` (the fold and refusals), conformance + fuzz
tests, no decision logic — identical in shape to `contracts/execution`. The
runtime (incarnate, supervise, takeover, tree) is drive, which already owns
session classes and scope caps. The Claude Code host adapter is hooks. The
role protocols are parley. No new repository.

### 4.9 Four of these laws are already built — port them

The 21 Aug 2026 bakeoff round produced four standard-library Go kernels that,
read against this design, are not experiments about verification in general.
They are implementations of four laws this substrate needs, each with frozen
fixtures and a planted mutant. p1 ports them; it does not rewrite them.

| Kernel | Law it already implements | Where it lands |
| --- | --- | --- |
| **hack-branchroom** | A pure `Reduce(state, event) (state, Decision)` with the tip as `parentEventDigest`, `nextSequence` covering duplicate-and-gap in one condition, and a fresh-epoch check that refuses a *perfectly correlated* stale writer | `reduce.go` — the fold, near-verbatim |
| **hack-mandate** | Attenuating delegation: a child may shrink actions, shrink the validity window, and decrement depth; subject and artifacts are equality-locked; the audience of a grant is the only key that may mint the next one | `contracts/authority` — lead → IC |
| **hack-obligation** | A monotone frontier: content-addressed obligation identity, a four-state lattice where `discharged` is re-enterable and `superseded` is terminal, and an add-only agent overlay with a mandatory ratchet | the definition of done a role cannot shrink by fiat |
| **hack-proofline** | The two-witness rule for a bilateral fact, and the split between *the edge that made something historical* and *everything that went historical* | `org audit` (§7.6) |

**The single best idea across all four is content-addressed identity.**
hack-obligation derives an obligation's id from `(kind, claim, goal
identity)`, so a change to the subject *forks a new obligation* rather than
mutating an existing one — cross-revision contamination becomes impossible by
construction rather than by a check. §5's incarnation ids adopt the same
move, and for the same reason.

**What does not port.** Every one of the four models its subject as a git
artifact — `base_sha`, `head_sha`, `diff_digest`. A dossier task has no head
SHA. The generalization that preserves the useful properties is a fixed
identity spine (`task`, `revision`, `kind`, `digest`) where the digest covers
a kind-specific body: the structs stay comparable, the revision-pinning attack
story survives, and git-ness leaves the contract. Three further mismatches are
recorded as open questions rather than smoothed over: mandate's grants assume
an immutable subject and a short window (§10.7), obligation assumes one active
goal where a role holds many, and obligation treats an oracle snapshot as a
*complete statement of the world* so that silence revokes — the semantic most
likely to need inverting when signals come from independent sources.

### 4.10 Two corrections that apply to all four, and to us

**Canonical encoding must not be `json.Marshal`.** All four kernels
canonicalize by marshalling Go structs, which means field order is Go
*declaration* order and `&`, `<`, `>` are HTML-escaped. The digests are stable
Go-to-Go and nowhere else, and none of them carries a digest-scheme version,
so reordering a struct field silently invalidates every historical digest.
That is acceptable in a frozen hackathon artifact and unacceptable in an
append-only chain meant to outlive refactors and be verified by a second
implementation. `contracts/org` ships an explicit field-ordered canonical
encoder with a versioned scheme before it writes its first record. This is
cheap now and unfixable later.

**The mutant is not optional — it is how a law is stated.** Each kernel ships
the *wrong* law as a first-class code path (a `headOnly bool`, a
`SettledIsTerminal` string, a retain-parent-epoch binder that bypasses the
public constructor) and asserts in tests that it gets the planted case wrong,
while proving both paths consumed byte-identical input. That last assertion is
what forecloses the "you fed them different inputs" objection. Every refusal
in §6 gets the same treatment in p1: a mutant that removes the check, a
fixture the mutant accepts and production refuses, and the refusal identifier
frozen into a golden artifact digest so that renaming a code is a test
failure. This is the difference between a law and a comment.

### 4.11 Every incarnation is disposable, and the chain is what makes it so

Any incarnation may be killed at any moment, and replacing one is routine
rather than an incident. That is the design's premise, not a concession to
it — and **the chain is what makes it true.** An agent holding three days of
reasoning in its head cannot be killed without losing something, so it is
precious whether you wanted it to be or not. Externalize what it knows and
disposability becomes structural. Residency is settled by the
same argument, from the other side: switchboard showed a live process buys
serialized ownership and nothing else, and the tip rule supplies that without
the process.

A working system on this premise already exists and is worth naming, because
it is simpler than this one and correct where it applies. In
[*Claude Code, 12 days straight*](https://malucelli.net/posts/2026-08-18-claude-code-12-days-straight/)
Malucelli runs seven cron'd loops on a $40/month VM against a Claude Max
subscription and reports 229 pull requests opened and 213 merged in twelve
days. His whole state story is one sentence: *nothing on the box is worth
backing up* — GitHub is the source of truth, every tick re-reads open PRs and
tracking issues, and losing the box costs only the ticks it missed.

**That is the right architecture for the work he runs, and this design should
not compete with it.** His agents are stateless sweeps: read the world, find
one bounded unit, open a PR, exit. Nothing is ever half-finished inside an
agent, because the work item *is* the pull request. There is no thread to
preserve, so a chain would be pure overhead.

But the split is not *chain or no chain*, and an earlier draft of this section
got that wrong by writing maintenance out of the design entirely. A monitor
owner is still a **role**: somebody durably owns alert health for a repo, tuned
this monitor and retired that one for stated reasons, and escalated the thing
nobody could reproduce. The individual tick is stateless; the *ownership* is
not. What differs between role kinds is only **how much their chain carries** —
which falls straight out of the law below, since a sweep role has almost
nothing that another store does not already know.

| Role kind | Owns | Chain density | Cadence |
| --- | --- | --- | --- |
| **IC** | one work item at a time, for days | **thick** — checkpoints every N tool calls; the thread across restarts is the whole point | continuous while assigned |
| **Maintainer / monitor owner** | a standing area: alert health, dependency health, test hygiene | **thin** — charter, tuning decisions and their why, escalations. Almost no checkpoints; each tick re-derives from the world | cron'd; per-tick idempotent |
| **Lead** | a scope and the roles inside it | **medium** — delegations, reports received, escalations, and the calls made on them | event-driven |

A maintainer's chain may sit unchanged for days and that is correct, not a
failure to record. Its ticks are Malucelli's architecture exactly — cron,
re-read the world, one bounded unit, exit — and the chain holds only the part
that would otherwise be lost: who owns this area, and what they decided about
it.

**And in an organization the work item is usually not a pull request.** It is a
ticket, a design doc, a runbook, an incident, a thread. Malucelli's
GitHub-as-truth works *because* his work item is a PR and GitHub therefore
knows the entire state of it. The moment work spans a tracker, a docs site, a
chat thread, and a repository, no single store knows what is going on — which
is the argument for the chain at its strongest, not a weakening of it. The
practical consequences are that `work.kind` must not be PR-shaped (§5) and
that a charter's capabilities are custody action manifests — comment on a
ticket, publish a doc, tune a monitor — rather than git verbs.

**The principle this yields: if it can be derived, don't record it.** FR7
already forbids recording liveness; generalize it. PR state, CI status, branch
existence, task status, worktree presence — all derived at read time, never
appended. The chain earns a field only when the fact exists nowhere else:
who currently owns this work, what authority is in force, what was decided and
why, and what comes next. The 4 KB cap on `state` is that principle with a
number attached; when a field is proposed for a record, the first question is
which other store already knows it.

**Backpressure belongs in the charter.** Malucelli bounds *production*, not
just authority: a sweep with five or more of its own pull requests already
open ends the tick instead of proposing a sixth, tying output to review
velocity rather than to the clock. Grants bound what a role may *do* — TTL,
tier ceiling, cycle cap — and nothing yet bounds how fast it may create work
for others. A role's charter carries a `wip_limit`, and a role at its limit
reports rather than produces. His merge ratio is the evidence that this is
load-bearing and not decoration.

**Two failure modes to design against, observed rather than predicted.**
Claude Code sessions expire at seven days, so a lead meant to persist for
weeks structurally cannot be one session — an external confirmation of §4.3.
And a session that hits a usage limit *does not exit*; it sits holding the
tick, which is why he screen-scrapes for the limit message. That case is the
argument for `next_due` over a process check: a process-liveness test calls
that session alive, while a declared deadline correctly reports a missed
commitment. The supervisor must treat "process exists" as the weakest of its
signals.

**What his setup does not answer, and this one must.** Two secrets sit on the
box — a signing key and a GitHub token — and a human SSHes in every couple of
days to re-authenticate. One credential, full reach, no attenuation, no
per-role scoping, no receipts beyond the PR list. That is fine for one
person's repositories and it is exactly the property that stops the shape from
travelling anywhere with a security review. The plumbing this design adds on
top of his — attenuated per-role grants, a fence that survives takeover, an
append-only record of who decided what under which authority, and an audit
that reasons about absence — is the entire difference between a personal
automation and something an organization can run.

### 4.12 Somebody has to operate this

Everything above is about being correct. This section is about being
*operable*, which until now the document treated as one line in the
non-functional table and a few assertions that refusals would "name the
remedy." That is the same failure the whole workbench exists to correct —
written intention where a mechanism belongs — so operability gets
requirements, a mechanism, and a gate that can fail, exactly like correctness
got the mutant discipline (§4.10).

The bar is one sentence: **somebody who was not here must be able to work out
what a role is doing, why it refused, and how to fix it — using only the tools
and the artifacts, with no access to the author and no transcript.** That is
the same property gate already claims for merges ("show me why this shipped")
applied to roles.

**A mistake must be correctable, and today it is not.** The chain is
append-only and has no correction record, so an agent that records a wrong
decision, a wrong assignment, or a checkpoint that misstates what it did has
poisoned every future fold of that role — permanently, with no path back short
of abandoning the role. Append-only is the right storage discipline and the
wrong error-handling story. So there is an `annul` record: it names the record
it corrects by `(seq, hash)`, carries a reason, and is itself an ordinary
append — the original is never rewritten, and the fold reports both the
mistake and the correction. Three laws keep it from becoming a rewrite
primitive:

- A role may annul **its own** content records; a supervisor may annul those
  of roles it supervises. Self-correction is normal and should not need
  ceremony.
- **Structural records cannot be annulled** — `genesis`, `resume`, `takeover`.
  Annulling those would rewrite ownership history, which is the one thing the
  chain exists to make impossible.
- An annulled record still counts for `seq`, `prev`, and the anchor. Annulment
  changes interpretation, never the chain.

**A refusal must carry its remedy, as data.** `prev_mismatch` is a code, not
an answer, and `fence_regression` is worse — understanding it currently
requires knowing about chains, sequences, incarnations, and a high-water mark
that lives invisibly inside a verifier. So every refusal returns
`{ code, message, remedy, evidence }`, where `remedy` is the command to run or
the fact to check, and `evidence` names the records that produced the verdict.
custody already does this — its refusals name the command that unsticks
them — and this is that convention made a contract rather than a habit.

**One command answers "what is going on with this role."** The portfolio
already has this instinct everywhere else: `console` explains gate's state and
decides nothing, `rooms doctor` runs twelve host checks, `gate explain` exists.
The org plane had `audit` and `tree` and no equivalent, which would leave an
operator joining the chain, the cap ledger, dossier, and GitHub by hand to
find out whether the chain lied or the derivation did. `org explain <role>`
renders one page — charter, current incarnation and how liveness was derived,
folded state and its age, assignments, authority in force and its fence, recent
refusals with remedies, and which store each line came from. `org doctor`
checks the environment the way rooms does: anchor key present, state dir
writable, clock sane, orphaned chains, chains that no longer fold.

**Adoption is a design property, and the portfolio has hard evidence about
it.** Of 51 authored skills on this machine, 23 have ever been invoked and
two-thirds have never fired once — including most of the delivery machinery
(`work-driver`, `pr-risk`, `review-coordinator`, `shipped`, `health`,
`roster`, `recover`, `consult`). The most-used skill by a factor of two is
`/continue` at 16 invocations, which exists solely to hand-write a handoff
when continuity fails. The pattern separating used from unused is not quality:
a tool is used when there is an unmistakable moment of need and it is the
obvious response, and unused when it must be *remembered* while the operator
is thinking about something else. `/claim`'s two events are the same finding
at a smaller scale.

Two consequences bind this design:

- **Nothing load-bearing may require remembering a verb.** `incarnate`,
  `checkpoint`, `handoff`, and `mark` are hook-driven for this reason, and
  that is not a convenience — it is the difference between working and joining
  the unused two-thirds. The operator-facing verbs (`explain`, `doctor`,
  `annul`, `audit`) survive only because a refusal's `remedy` field names the
  command at the moment it is needed. Any future verb without such a delivery
  path should be assumed dead on arrival.
- **This must retire more surface than it adds.** In a portfolio where two
  thirds of tools go unused, adding thirteen verbs is only defensible if the
  chain subsumes what already exists. It does: `/continue` (the automatic
  handoff replaces the hand-written one), `/claim` and `/release` (assignment
  is structural), `/roster` and `/recover` (both re-derive at read time what
  the chain records), and the state-reconstruction half of `/status` and
  `/wip`. Retiring them is p3 scope, not a later tidy-up — if the chain ships
  and `/continue` is still being typed, the design has failed on its own
  terms regardless of what the reducer proves.

**The agent is the primary user, and the untested assumption is checkpoint
quality.** Roles are operated by agents far more than by people, and §4.7 asks
an incarnation to author its own distilled state. If agents write vague
checkpoints, every downstream fold is confidently wrong — and because §11's
gate grades the *resume*, a bad checkpoint and a bad fold produce identical
failures. They must be graded separately (§11.1c) or a failure cannot be
diagnosed, which is the operability bar applied to our own validation plan.

## 5. Data model

**Role id.** `org/<role-slug>` — e.g. `org/lead-a`, `org/ivy-lead`,
`org/ivy-ic-3`. Lowercase, stable, never reused.

**Incarnation id must be unguessable.** It is the digest of the `resume` (or
`takeover`) record that created it — not a counter, not a sequence. This is a
correction taken from hack-branchroom, whose epochs are `parent + n` and are
therefore trivially guessable: its stale-writer refusal is only sound if the
displaced writer never stamps the fresh epoch, and a robust writer that
re-reads the tail before appending will copy the fresh id *by accident* and
sail through. A digest cannot be arrived at by a writer that has not read the
record that minted it, which is exactly the population we want to exclude.

**Chain layout.** `<state>/org/<role-slug>/chain.jsonl` plus the anchor
record under the key dir (per gate). Optional `snapshot-<seq>.json` every
256 records; a snapshot is a cache of the fold and is deletable.

**Record kinds and bodies** (every record is an `Envelope` with
`kind: "org.<name>"`, `run: <role id>`, `prev`, `hash`; bodies below).

| kind | body | law |
| --- | --- | --- |
| `genesis` | `charter{ kind, scope[], decides[], never_decides[], escalates_to, supervisor, supervises[], capabilities[], wip_limit }` | seq 1 only; exactly one. `kind ∈ {ic, maintainer, lead}` sets checkpoint cadence and expected chain density (§4.11). `capabilities[]` are custody action manifests — comment on a ticket, publish a doc, tune a monitor — never git verbs. `wip_limit` bounds open work in flight |
| `resume` | `incarnation{ id, host, session_ref, started_at }` | `prev` == tip; starts an incarnation; `id` is the digest of this record, never a counter (see below) |
| `checkpoint` | `state{ goal, doing, decided[{what, why}], open[], next[], refs[] }`, `incarnation_id`, `next_due` | `next` non-empty; ≤ 4 KB; `incarnation_id` == current; `next_due` in the future |
| `handoff` | same as `checkpoint` + `reason: stop\|compaction\|release` | ends an incarnation cleanly; no `next_due` (nothing is coming) |
| `mark` | `mechanical{ session_ref, git{branch, head, dirty[]}, last_tools[], transcript_offset }`, `next_due` | host-authored; degraded; exempt from `empty_next` (§4.7) |
| `takeover` | `by: <supervisor role>, from_incarnation, reason, evidence[]` | only a role named `supervisor` in genesis; ends the current incarnation |
| `assign` / `release` | `work{ kind, id }`, `incarnation_id` | `kind ∈ {dossier, jira, pr, doc, incident, area, free}` (drive's vocabulary, extended — open, never an enum in the schema); one open assign per work id across all chains (checked by drive at write time, law at fold time). `area` is the standing-ownership kind a maintainer holds indefinitely rather than completes |
| `message.sent` / `message.received` | `msg{ type, to\|from, ref{role, seq, hash}, body }` | `type ∈ {delegate, report, escalate, ask, answer, takeover_notice}` |
| `annul` | `annuls{ seq, hash }, reason, by` | own content records, or a supervised role's; never `genesis`/`resume`/`takeover`; the annulled record still counts for seq, prev, and the anchor (§4.12) |

**Fold output — `RoleState`.**

```
RoleState {
  Role, Charter,
  Tip{ Seq, Hash }, Count,
  Incarnation *{ ID, Host, SessionRef, Since, NextDue },  // nil when none live
  State{ Goal, Doing, Decided, Open, Next, Refs, Degraded bool, At seq },
  OrphanedSince *int,        // set when a takeover cut an incarnation off mid-work;
                             // the successor must assess the refs before continuing (§7.3)
  Assignments[]{ Work, Since },
  Outbox[]{ To, Type, Seq },  Inbox[]{ From, Type, Seq },
  Supervisor, Supervises[]
}
```

`NextDue` is a declared deadline, not a heartbeat: the fold reports what the
incarnation committed to, and a supervisor joins that with host signals to
*derive* liveness. Nothing in `RoleState` asserts that a role is alive.

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
| `fence_regression` | a grant presents a `fence` below the verifier's high-water mark for that role (§4.6) |

**Check order is part of the contract.** Refusals are evaluated
structural-before-semantic, and the first failure wins, so that a given
malformed record always produces the same code no matter which runtime
evaluated it:

1. **Version** — `schema_version` this reader accepts.
2. **Shape** — `ValidateRecord`: per-kind body law (`empty_next`,
   `oversize_state`, malformed ids and refs).
3. **Role** — the record names this chain's role at all (a routing error, not
   a staleness one).
4. **Incarnation** — `stale_incarnation`, for every kind that carries an
   `incarnation_id`. `resume` and `takeover` are exempt: they *establish* an
   incarnation rather than asserting one.
5. **Chain position** — `seq_gap`, then `prev_mismatch`.
6. **Role semantics** — `not_supervisor`, one-open-assign.

**Incarnation is checked before chain position, and that order is load-bearing
rather than cosmetic.** The intuition runs the other way — structural before
semantic — and v1 had it backwards. The case that decides it is §7.3's
re-reading writer: a displaced incarnation that re-reads the tail before
appending presents a correct `prev` and a correct `seq`, so a
position-first order returns *no* refusal from step 5 and reaches the
incarnation check anyway. Meanwhile a displaced incarnation that appends
blindly gets `prev_mismatch` under a position-first order — a *worse*
diagnosis, because the true condition is "you have been replaced," not "you
lost a race." Checking incarnation first gives the same correct answer in both
cases. hack-branchroom's reducer independently arrived at this: it compares
epoch before branch, sequence, tip digest, and call id, precisely so that a
perfectly-correlated stale writer still receives the specific reason.

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
org annul     <role> --seq <n> --reason <text>                      → corrects a content record (§4.12)
org tree                                                            → the org chart with derived liveness
org audit     <role>                                                → anchor + chain check (gate audit's twin)
org explain   <role>                                                → one page: charter, incarnation and how
                                                                      liveness was derived, folded state and its
                                                                      age, assignments, authority and its fence,
                                                                      recent refusals with remedies, and which
                                                                      store each line came from
org doctor    [--json]                                              → environment checks, rooms-style: anchor key,
                                                                      state dir writable, clock sane, orphaned
                                                                      chains, chains that no longer fold
```

`explain` and `doctor` decide nothing and write nothing — console's stance,
kept deliberately: an explainer that can act is an explainer you stop trusting.

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
2. Supervisor `ivy-lead` derives liveness at read time: the record's declared
   `next_due` has passed, transcript mtime is stale, no process. It appends
   `takeover{ by: ivy-lead, from_incarnation: inc-7, reason, evidence }` at
   seq 42, then incarnates a replacement (`resume` at 43) — or spawns a
   worker that does.
3. The successor's grants are minted at `fence = 43`. drive re-mints the scope
   cap and marks inc-7's revoked, but that write is a cache update, not the
   authority (§4.2): even if it fails, inc-7's grants carry `fence = 40` and
   every verifier refuses them the moment it has honored a higher fence
   (§4.6). `org incarnate` bumps the fence on resume so the window does not
   depend on the successor reaching a verifier first.
4. inc-7 was not dead, only slow, and comes back to write. There are **two
   shapes, and the second is the common one**:
   - *Blind append.* inc-7 writes with the `prev` it remembered,
     `hash(41)` — refused on chain position.
   - *Re-read then append.* inc-7's write fails, or its adapter simply reads
     the tail before every append (the normal shape for a robust writer), so
     it re-reads the chain and appends with the **current** tip, seq 44, and
     `incarnation_id = inc-7`. Now `prev` matches. `seq` matches. Every
     correlation field matches. **Only `incarnation_id` refuses it.**

   The second case is why the incarnation check is not redundant with `prev`
   in a linear chain, and it is exactly the law hack-branchroom's reducer
   already implements and mutation-tests: its late-parent terminal is
   perfectly correlated on branch, sequence, call id, and tip digest, and is
   caught solely by the epoch comparison. v1 of this document described only
   the blind-append case and would have justified dropping the check.

   Either way the refusal names the successor, and inc-7's host adapter stops
   the session cleanly.

**What is and is not guaranteed here.** The chain is uncorrupted and no lock
was needed for the ownership decision. But inc-7 may have *done work* between
seq 41 and its refused append: files written, commits pushed, a message sent
whose receiving chain now records a delegation from an incarnation that no
longer exists. That work is real and lives outside the chain. The design does
not pretend otherwise — it makes the assessment an explicit obligation of the
successor: the fold surfaces `orphaned_since = 41` with the last-known refs,
and the successor's first task is to assess what it finds at those refs before
continuing. This is inherent to any crash-recovery model, not a flaw, but v1
overclaimed by saying "nothing was corrupted" without qualifying it to the
chain.

**Cross-chain fallout.** A `message.sent` from inc-7 that a peer already
recorded as `message.received` is not retracted — the record stands, since
chains are append-only. What changes is its interpretation: `org audit`
reports it as *sent by a since-displaced incarnation*, and the receiving role
may treat it as advisory. Retraction, if it is ever wanted, is a new record on
the sender's chain, never an edit.

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
`message.received` naming A's record back.

**The audit must reason about absence, not only presence.** This is the
sharpest correction in the review round, and it comes from hack-proofline,
which computes retraction only when *both* witnessing edges are recorded and
otherwise reports "current." Over a single trusted bundle that fail-open rule
is a feature: nothing is retracted without evidence. Across two chains that
may disagree, **the identical rule is a suppression attack** — role B simply
omits the record, and A's grant goes on looking live forever. So the audit
carries a completeness obligation in both directions: every `message.sent` on
A *must* have a counterpart on B, and a missing counterpart is a finding
(`counterpart_absent`), never a silent pass. Presence-based reasoning is safe
only when you trust the store, and the whole point of two chains is that you
do not.

**What the audit can and cannot catch.** With the keyed anchor it detects
truncation, rewrite, and now omission. It does **not** by itself catch a
*coherently* lying chain — an adversary who rewrites a record and updates
every reference around it consistently. Catching that needs per-record
signatures binding each append to the role's key, so that a record's presence
on A is evidence *about A* rather than an assertion by whoever assembled the
bundle. Signatures are deliberately **not** in p1: the realistic adversary
here is drift and accident, matching gate's stated tamper model, and adding
keys would cost the offline, keyless verification that makes the audit cheap
to run everywhere. It is recorded as the next layer down rather than as
something this design already has.

**Invalidation is seq-scoped, not merely reachable.** When a takeover
displaces an incarnation at seq N, the facts that become historical are those
that depended on it *after* N. A pure reachability closure — proofline's
`descendants()` over one relation — would also sweep in facts that consumed
the incarnation legitimately *before* N. So the cascade filters on chain
position, and the two outputs stay separate the way proofline separates them:
the single edge that made something historical, and the set of everything
that went historical. Nothing is deleted; status is computed per query and
history stays byte-identical.

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
- **Both leads down at once** → nobody left who is named as supervisor, and
  the org cannot recover itself. Rather than add a third watcher (which just
  moves the problem), the **operator is the implicit supervisor of every
  role**: `org takeover --by operator` is always legal, on any chain,
  regardless of what the genesis names. This is the one privileged write in
  the model, and it is the right one — the human is the root of authority
  everywhere else in the workbench too. §11 test 3 kills both leads to
  confirm the path is real rather than assumed.
- **The supervisor itself is stale** → a supervisor derives liveness from
  `next_due` plus host signals, and can be wrong. A takeover on a role that
  was merely slow is not corruption: the displaced incarnation is refused
  cleanly (§7.3) and the cost is the orphaned-work assessment. The model
  optimizes for *never two owners*, accepting *occasionally a premature
  handover* — the reverse trade would require recorded liveness.

## 9. Rollout / implementation plan

| Phase | Goal | High-level tasks | Depends on | Gate |
| --- | --- | --- | --- | --- |
| **p0 charter** | This document reviewed and locked | Reviewer panel; fold findings; decide §10 | — | design locked |
| **p1 contracts/org** | The leaf package | Canonical encoder (versioned scheme) **first**; types + embedded schemas for the 5 contracts; `validate.go`; `reduce.go` — the fold **ported from hack-branchroom**, not written fresh; `contracts/authority` ported from hack-mandate; conformance, property, and fuzz tests; `Envelope` kind registration; hygiene CI (leaf imports nothing) | p0 | `go test ./contracts/org/...` green; **one mutant per refusal code** (§4.10), each proving production and mutant consumed byte-identical input; refusal identifiers frozen into a golden digest |
| **p2 laws** | Machine-checked chain laws | Port `fm-epoch-replay-laws` fold ≡ checkpoint-resume ≡ replay; add contiguity, single-tip, resume-requires-tip, takeover-invalidates-stale; adversarial reducer that admits a fork must fail | p1 | `lake build`, axiom audit, counterexample fixture |
| **p3 host adapter** | One role resumes mid-thought on Claude Code | `org` CLI (incarnate / checkpoint / handoff / mark / fold / audit) over `contracts/org`; hooks: SessionStart / PreCompact / Stop / N-calls; repo-keyed memory; chain state dir + anchor | p1 | **VALIDATION GATE** — §11 test 1 |
| p4 drive runtime | Roles over driver/worker | genesis/charter verbs; supervision reducer; `takeover` mints caps and advances the fence; `org tree`; custody and gate adopt `contracts/authority` and keep a per-role fence high-water mark | p3 ✓ | §11 test 2 |
| p5 parley | Legal conversations | `org-delegate.parley`; `grants`/`effects`/`receipt` in the algebra; `observe` over chain pairs | p4 | real chains classify clean |
| p6 the slice | The org at 1/10 scale | one lead, three ICs, one repo, one day; kill an IC; count operator prompts. **Runs on a rented VM, not the operator's laptop** — §4.11's reference setup is a $40/month box against an existing subscription, which is what makes four concurrent day-long sessions practical at all | p4, p5 | §11 test 3 |

Committed: p0–p3. p4–p6 are gated on p3 proving the thesis for a single
role. Rough scope: p1 ≈ execution's size (~1.5k weighted LOC incl. tests);
p3 ≈ 600 (bash + Go CLI); p2 ≈ one Lean file plus adversarial twin.

## 10. Open questions

*(v1 items 1 and 2's first half are now decided — see §4.6 and §4.7. Items
below are renumbered; new questions 8–10 come from reading hack-mandate.)*

1. **Checkpoint authorship on hosts without a prompt surface** (`codex exec`,
   SDK workers): the adapter can only `mark`. Is a chain of marks with
   occasional agent-authored handoffs good enough for ICs, or does the SDK
   host need a mandatory end-of-turn `org checkpoint` tool call? §4.7's cold
   floor makes this measurable rather than theoretical — a mark-only host
   guarantees continuity of commitment but never of thought, so the question
   is whether an IC's 2–3 day run can afford that. Decide from p3 evidence.
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
7. **Subject drift on long-running work.** hack-mandate binds authority to a
   `TaskRevision`, and bumping it kills every outstanding grant instantly.
   Correct for a fixed diff; a footgun for a three-day IC task where the lead
   adds a phase on day two — you either never bump (defeating the binding) or
   you silently revoke work in flight. The chain suggests a third path: a
   revision bump is a `message.sent` to the assignee, and the IC's grant
   survives until it acknowledges or the lead takes the work back. Needs
   deciding before p4 mints anything against a dossier task.
8. **Canonical encoding.** hack-mandate's `EncodeCanonical` is
   `json.Marshal` — field order is Go declaration order and `&`/`<`/`>` are
   HTML-escaped, so it is Go-to-Go only. Anything this substrate signs or
   digests must use a real canonical form (RFC 8785 / JCS or an explicit
   field-ordered encoder) before a second language or a second process ever
   verifies it. Cheap now, expensive after the first signed record exists.
9. **Does a role need an obligation frontier, and where?** hack-obligation's
   monotone frontier is the natural answer to "may this role report done" —
   an IC cannot close a task while mandatory work is open, and cannot shrink
   that set by fiat. It is deliberately *not* in v0's record set, because it
   needs two things this design has not settled: which principal may discharge
   which kind of obligation (authority, which the frontier explicitly refuses
   to hold), and whether a role's many concurrent goals each get their own
   frontier. A p4 question. If it lands, take the kernel's lesson and **emit
   the predicate** — a `mandatory_open` field, not a shape every caller
   re-derives and half of them get wrong.
10. **Grant consumption.** hack-mandate records no `RequestID` and enforces no
   budget: "may push" is not "may push 200 times." A three-day IC will make
   thousands of requests against one grant. Does `contracts/authority` need a
   consumption counter, or does custody's request log plus the fence make
   after-the-fact accounting sufficient? Leaning the latter — accounting
   beats enforcement here — but it should be a decision, not an omission.

## 11. Validation plan

Three binary tests, one per gate, no vibes.

1. **Resume mid-thought (p3 gate).** In each of ivy, ship, and gate: charter
   one role; run a real task for ≥ 30 tool calls; kill the session without
   warning; incarnate fresh. Ask "where were we and what's next." Pass if
   the answer names the current work, the last decision and its why, the
   open threads, and the next action — and a blind reader cannot tell the
   transcript was cut. Run with the chain and without (today's cold open) on
   the same three tasks; the delta is the result.
   **1b — the cold case.** Repeat once per repo with the transcript deleted
   before incarnating, so the tip is a `mark` pointing at nothing. Pass if
   the resumed incarnation correctly reports what it does *and does not*
   know: it names its charter, assignments, and last authored state, and it
   says plainly that the reasoning since then is gone rather than inventing
   it. A confident wrong answer here is a worse failure than an honest
   partial one, and this test is the only place that distinction is caught.
   **1c — grade the checkpoint, not only the resume.** Before killing the
   session, score its last authored `state` on its own terms: does `decided`
   carry the *why*, does `next` name an action someone could start cold, does
   `open` list what it is actually waiting on? A vague checkpoint and a broken
   fold fail 1a identically, so without this the result cannot be diagnosed —
   §4.12's bar applied to our own validation plan. If checkpoints score well
   and resumes still fail, the fold is wrong; if checkpoints score badly, the
   prompt is wrong and no amount of reducer work will fix it.
   **1d — the operability bar.** Hand a second person, or a fresh agent with
   no transcript, a role broken three ways: a chain that no longer folds, a
   grant refused on its fence, and a checkpoint carrying a wrong decision.
   Pass if they diagnose all three and repair the third using only
   `org explain`, `org doctor`, `org audit`, and `org annul`, with no access
   to whoever built it. This is the only test of §4.12, and it gates the same
   way the others do: a design nobody else can operate has not shipped.
2. **Ownership (p4 gate).** Two incarnations of one role race: exactly one
   holds the tip; the other's write is refused with `prev_mismatch`. A
   supervisor takeover makes the displaced incarnation's grant unspendable
   at custody — checked by a refused request in custody's log carrying
   `fence_regression`, with the org state dir made *unreadable* during the
   check to prove the verifier needed no access to it (§4.6).
3. **The slice (p6 gate).** One lead, three ICs, one repo, one working day.
   Pass if: the operator sends ≤ 10 prompts; an IC killed mid-task is taken
   over and its replacement resumes from the chain without operator input;
   `org audit` over every chain pair is clean; parley `observe` classifies
   every trace as complete or stalled, none deviating.
   **3b — decapitation.** Kill both leads at once. Pass if
   `org takeover --by operator` recovers a lead chain and the org resumes —
   confirming §8's supervisor-of-last-resort is a real path and not an
   assumption written into a document.
