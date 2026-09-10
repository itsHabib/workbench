# Org / Fleet boundary: who owns which fact

**Status:** draft decision document, for review. **Date:** 2026-09-10. **Scope:** docs only; every
decision below names the PR that will carry it. Nothing here changes code by itself.

## Sources

Every decision cites these by the tag in brackets.

- [R] Rehearsal record, three runs on 2026-09-09:
  <https://github.com/itsHabib/fleet-demo-sandbox/blob/main/docs/REHEARSAL-2026-09-09.md>.
  Frictions 1 to 11, the message metrics table, and "Changes to make before the next run".
- [C2] `docs/RUN-CONTRACT.md` and [C3] `docs/RUN-CONTRACT-v3.md` in `itsHabib/fleet-demo-sandbox`,
  the run 2 (desktop messaging) and run 3 (headless, mailbox delivery) contracts.
- [S] Scorecards `runs/run-1a-headless.json`, `runs/run-1b-loop.json`, `runs/run-2-native.json` in
  the same repository, produced by `scripts/run-metrics.py` from the chains, the PR comments and
  the hook telemetry, not from anyone's report.
- [F] workbench `cmd/fleet/README.md`: the five rules, the store, ownership rows, "what is
  deliberately not here".
- [O] workbench `cmd/org/CLAUDE.md`, `cmd/org/README.md`, `cmd/org/hooks/sessionstart-boot.sh`,
  and the org design PR <https://github.com/itsHabib/workbench/pull/245>.
- [K] workbench `contracts/org/record.go` (kinds, `Terms`), `contracts/org/reduce.go` (the phase
  law table), `contracts/org/scope.go` (`InScope`).
- [P295] <https://github.com/itsHabib/workbench/pull/295> and [P296]
  <https://github.com/itsHabib/workbench/pull/296>, the mail bakeoff, with the comment threads.
  The verdicts cited are AI reviewer outputs posted through the operator's account; where a
  comment is quoted its URL is given.
- [TS] cc-skills `docs/features/agent-fleet-rules/TWO-SYSTEMS.md`, [V]
  `docs/features/agent-fleet-rules/VISION.md`, [P62] <https://github.com/itsHabib/cc-skills/pull/62>
  (the substrate/policy boundary), and [P65] <https://github.com/itsHabib/cc-skills/pull/65> at
  `48c60849d402231fb09fd967532d31685e224e9d`: `skills/task-supervisor/SKILL.md`,
  `references/coordination.md`, `references/native-messaging.md`, `scripts/board.py`, and the
  lead and author cards under `tools/fleet-work-setup/cards/`.

## The problem, in one page

Two systems in workbench both describe the same team of agents, and tonight they described it
three ways at once.

**Org** ([O], [K]) is a role's continuity: a charter with terms (scope, effect classes, takeover
authority, ceilings), an append-only hash chain per role, a holder (the incarnation) fenced by
compare-and-swap, held work, obligations (open intents and escalations), a self-declared
`next_due`, and a byte-capped `boot` index the next session reads. Its invariants say "the home
adds no judgment" and "liveness is derived from the writer's own declared `next_due`".

**Fleet** ([F]) is what the hook can see: a session's identity from the directory it launched in
(`roles.map`), branch and resource leases with one holder per key, dispatch rows with an
accountable role as a column, receipts at an exact head, and a watcher that folds the store into
a board. Its rules say "facts come from the hook, never from an agent", "the substrate learns no
domain word", and "done is evidence".

They share the role name and nothing else. The recommendation on the mail bakeoff put it
exactly: "Org records holders, assigned work, and deadlines. Fleet separately records sessions,
dispatch accountability, leases, and deadlines. Sharing role names does not prevent these
operational ownership records from disagreeing."
(<https://github.com/itsHabib/workbench/pull/295#issuecomment-5613258713>). Tonight the
disagreement showed up as concrete cost, in five places.

1. **Three readers of the role tree.** The Fleet hook and the Org SessionStart hook each read
   `roles.map` for path to role. [P65]'s `board.py` derives reporting edges from charter scopes of
   the form `role:<child>` and states that `terms.supervisors` "is never a reporting edge". [P296]
   derived mail contacts from `terms.supervisors`, the takeover relation, by parsing
   `chain.jsonl` itself. [P295] declared contacts in a Fleet-side `contacts.json`. Four sources for
   one tree, and the bakeoff's final verdict was that the one thing that cannot be retrofitted is
   which of them is true (<https://github.com/itsHabib/workbench/pull/295#issuecomment-5613272865>).
2. **The lead's tick is mostly ceremony.** Every tick ran attach, assign (a pinned placeholder),
   claim, intent, one effect, resolution, checkpoint, yield, release [R]. In the live loop run the
   overall lead's chain gained 8 attach, 8 claim, 8 checkpoint, 8 yield and 8 release records
   around one intent, one message and one resolution [S, run-1b]. Lead ticks cost 18 to 33 turns
   and $1.85 to $3.39 each [R]. The kernel forces the shape: `intent-ref` is admissible only from
   an active claim ([K], `phaseLaw`), so a lead with no real held work must assign and claim
   something before it may record an intent.
3. **Org's `message` is a record, not a delivery.** In run 1a a bucket lead's escalation was
   written on its own chain, the overall lead found it by reading both children's chains, answered
   on its own chain, and the child found the answer on its next tick [R]. Each hop was a tick. Run
   2 moved the hop to desktop messaging and found two id spaces: the hook sees the Claude session
   id, the desktop wants `local_…`, and the workers' first sends failed with `Session not found`
   [R]. Run 3 moved the hop to `fleet mail`, addressed by role [C3].
4. **A mailbox per role cannot address a seat.** Two author seats of one repository share the
   role `author:fleet-demo-sandbox`. Run 3 had to move task `r3-b` to the finisher seat "because a
   mailbox is per role and two author seats would share one" [C3].
5. **A roled repository root poisons everything under it.** The workbench root had been roled
   `supervisor:workbench`; its projected denies applied to every worktree beneath it, so the
   session building `fleet mail` could not push (friction 11, [R]). Projected denies are a
   high-water mark that re-roling never removes.

The question the two PRs could not settle, and this document does: for each fact about the team,
which system is its owner, which is a reader, and what the reader is allowed to derive.

The principle applied throughout is the one both systems already state. Fleet: a fact is written
by the hook from an action the agent was taking anyway; nothing is asked of an agent [F]. Org: the
chain holds the agent's own conclusions and obligations, and the home adds no judgment [O].
[P62] draws the same line for receipts: Fleet checks admission and records provenance, the caller
says what is owed. So: **observed facts belong to Fleet; authored facts belong to Org; each fact
has one writer; a reader consumes the owner's validated output, never its storage.**

## Decisions

### D1. Identity and topology: charters are the tree, `roles.map` is the binding, `org` is the one reader

**Decision.**

- *Topology* (which role reports to which) lives in Org charters and nowhere else. A role's parent
  is the role whose charter scope names `role:<child>` ([P65] `SKILL.md`, "Reporting edges derive
  from parent charter scopes `role:<child>`"). `terms.supervisors` is takeover authority only and is
  never read for topology. Multiple parents, cycles and a `role:` scope naming a role that is not
  chartered are errors, not guesses.
- *Identity* (which directory is which role) lives in `roles.map` under `$ORG_STATE`, with the
  columns it has today: path, tenant, role, optional seat name. Fleet is its only writer
  (`fleet role`, `fleet pool`). It is a machine-local artifact, because directories are.
- *One reader.* Org gains a read verb, `org tree -json`, that folds every chartered role in the
  tenant and returns, per role: parent, children, scope, takeover authority, and the `roles.map`
  paths bound to it. Fleet's mail contacts (D4), the supervisor helper's board, and any future
  board consume that output through the CLI or the MCP face. Nobody outside `contracts/org` and
  `cmd/org` parses `chain.jsonl`. `roles.map` stays readable by both hooks because its line format
  is a contract, but neither hook derives a tree from it.

**Evidence that forced it.**

- [P296]'s `Parents()` read `terms.supervisors` from the raw chain and returned "no parents" for an
  unparseable line; the bakeoff's final verdict found the premise of "derived, with a fallback"
  false in the code and concluded "What is genuinely unretrofittable is the *decision* to derive,
  and that ports" (<https://github.com/itsHabib/workbench/pull/295#issuecomment-5613272865>). The
  reconciling recommendation: "if Org supplies that view, consume validated output rather than
  reinterpreting its journal" (<https://github.com/itsHabib/workbench/pull/295#issuecomment-5613258713>).
- [P65]'s helper already treats `role:` scopes as the reporting edge and `supervisors` as a
  different relation. Two relations read for one purpose is exactly the drift [TS] names: "a fact
  that was true somewhere, asserted about somewhere else".
- [V] §4: "Charters are the topology. `roles.map`, the comms hierarchy and the slot list are
  projections of it, never edited by hand."
- Tonight the tree was written once (`supervisor:demo` scoping `role:supervisor:demo-a` and
  `role:supervisor:demo-b`) and every relay in [S, run-2] followed it, so the edges are already
  correct where they are; what was wrong was the number of readers.

**Alternatives rejected.**

- *Derive from `terms.supervisors`* ([P296]). Wrong relation: takeover authority is who may
  displace a holder, and the skill forbids reading it as reporting. A parent that may not take
  over its child is a legitimate charter.
- *Declare a Fleet-side contacts file* ([P295]). A second copy of the tree, written by hand, that
  "will drift from the real hierarchy the day after it is written"
  (<https://github.com/itsHabib/workbench/pull/296#issuecomment-5613218342>). [P295]'s own docs
  chose it only because "Org has no cheap published contacts relation"; D1 supplies that relation.
- *Add parent columns to `roles.map`.* The map is per machine and per directory; the tree is per
  tenant and must be the same on a second machine that shares only the git remote [F, "no
  cross-machine store"].

**Consequences.** `roles.map` loses nothing. `org tree` is a new read verb (exit 0, JSON), cheap to
build over the existing fold. The helper's topology code in [P65] `board.py` becomes a consumer
of that verb rather than a second fold.

### D2. The lead's record: intent, evidence, resolution, escalation, checkpoint, on Org's chain; the tick ceremony moves to hooks

**Decision.**

- A lead tick's authored record is exactly: an `intent-ref` before each effect, the effect's
  read-back inside the `resolution` that closes it, an `escalation` when a question goes up, and
  one `checkpoint` with the tick's conclusion. Nothing else is written by the agent.
- That record stays on the role's Org chain. It does not move beside the Fleet store.
- `attach` and `release` are written by hooks, not by the agent: the SessionStart hook attaches
  the session as the role's incarnation (with `next_due` from the lane manifest's cadence, or
  none) and the SessionEnd hook releases it. Both stay fail-open: a refused attach (the role is
  held by a live session) leaves the session able to read but not write, and the boot output says
  so.
- The kernel relaxes one phase law: `intent-ref` becomes admissible from `Held` as well as
  `Active` ([K] `phaseLaw`). `assign` and `claim` are reserved for durable work a role actually
  holds across sessions, which a lead may or may not have; they are no longer a precondition for
  recording an intent. Relaxing an admission law refuses no historical record, so existing chains
  still fold.

**Evidence that forced it.**

- Chain shapes [S]. Run 1b, overall lead: 8 attach, 8 claim, 8 checkpoint, 8 yield, 8 release
  around 1 intent, 1 message, 1 resolution. Run 2, lead B: 9 claim, 9 yield, 18 mark, 19
  checkpoint around 10 intents, 12 resolutions, 2 escalations. Nobody read an attach, claim, yield
  or release record tonight; the parent read children's escalations and checkpoints, and the
  restart procedure reads open intents ([P65] `coordination.md`, "On restart, read open intents
  before taking a new action").
- The pinned `assign` was a placeholder by construction: [C2] tells bucket leads to "pin their
  branch with text `"<task> per RUN-CONTRACT run 2"` until their draft PR exists", because the
  kernel would otherwise refuse the intent. A record written to satisfy a law, not to state a
  fact, is ceremony.
- Cost [R]: "tonight every tick spent most of its turns on attach/claim/yield/release around one
  effect"; lead ticks 18 to 33 turns, $1.85 to $3.39; eleven live-loop ticks about $12.
- [V] §4 names the destination: "Attaching to a lane becomes a side effect of a session
  starting… nobody attaches, the hook does." [P62]'s handoff already refused to add "a new
  mandatory checkpoint schema or a new chain write".

**Alternatives rejected.**

- *Move the lead's record beside the Fleet store* (a `records/<role>.jsonl` under `~/.fleet`).
  Fleet's first rule is that facts come from the hook and never from an agent [F]; an intent and a
  conclusion are authored judgment, the one thing Fleet refuses to hold. It would also create a
  third store with no hash chain, no fence and no boot index, days before [V] Stage B plans to
  fold the Org chain into gate's ledger. The boundary drawn here should be the one that ports
  there.
- *Keep the full lifecycle and pay for it.* The records are not wrong, they are unread, and their
  cost is the majority of every tick.
- *Drop `checkpoint` too and let mail carry the conclusion.* The boot index is built from the
  checkpoint; a mail record is addressed to one recipient and is not the role's own last word.

**Consequences.** `org intent` no longer needs `org claim` first. The `-strict`/`-incarnation`
discipline is unchanged: the session still presents the incarnation the hook attached. The Stop
hook's `mark` stays; a tick that ends without a checkpoint still renders `degraded`.

### D3. Messaging: mail is the transport; Org keeps `escalation`/`resolution` as the obligation around a send; Org's `message` kind is retired from the procedure

**Decision.**

- Every agent-to-agent message travels as `fleet send` and is read with `fleet mail`/`fleet ack`,
  as in [C3]. Mail is a Fleet verb because delivery is an observed fact: a record exists, it was
  delivered, it was acknowledged, and none of those is an agent's claim.
- A send is an effect. On the sender's chain it is wrapped as any effect is: `intent-ref` naming
  the recipient role and the message id, `resolution` carrying the mail record's id and `at` as
  read-back. The message body lives in the mail record only.
- A question that goes up is additionally an `escalation` on the asker's chain, closed by a
  `resolution` that names the answer's mail id. The chain therefore shows a pending question as a
  dangling obligation, which `boot` and `sweep` already render; the mail record carries the words.
- Org's `message` kind stays in the kernel (removing a kind changes canon) but no lead procedure
  writes it. A lead does not read another role's chain to find mail.

**Evidence that forced it.**

- Run 1a's relay by chain: a worker's refusal became a PR comment, then an escalation on lead A's
  chain, then the overall lead read both children's chains and answered on its own chain, then
  lead A read the parent's chain on its next tick [R]. Every hop cost a full tick (three to four
  minutes) and every role had to read every child chain every tick.
- Run 2's relays over desktop messaging took 16 to 200 seconds per relay and needed no chain
  reads, but 3 of 25 sends failed on the session-id mismatch and 9 carried no id [S, run-2]. Run 3
  keeps the addressing (role) and drops the transport's dependence on a live session id [C3].
- The obligation still matters: [O] `sweep` counts "obligations orphaned by a displaced holder vs
  discharged by a successor"; an unanswered question must be visible there, and a mail record is
  not on any chain.
- The recommendation's line: "An `order` is a message, not an assignment or authorization. An
  acknowledgement is not completion."
  (<https://github.com/itsHabib/workbench/pull/295#issuecomment-5613258713>). Mail carries words;
  authority and obligations stay where they are.

**Alternatives rejected.**

- *Keep Org `message` as the transport.* It cannot wake anyone, it needs the recipient to poll
  the sender's chain, and it puts a body on a hash chain that exists to hold conclusions.
- *Let mail replace `escalation` as well.* Then a pending question has no obligation record, the
  boot index cannot show it, and a successor incarnation cannot see what its predecessor was
  waiting on.
- *Mirror every mail record onto the chain.* [P65] `native-messaging.md` already ruled it out:
  "Do not mirror every conversational acknowledgement into another ledger."

### D4. Contacts derive from the charter tree and the dispatch row; there is no declared contacts file

**Decision.**

- A chartered role's contacts are its parent and its children, from `org tree` (D1). No siblings
  by default: escalation "travels up exactly one hop" and "nothing sideways" ([P65] `SKILL.md`).
- A seat's only contact is the accountable role (`for`) on the dispatch row that matches its seat
  name, repository and checked-out branch, as [P295] does. Two rows disagreeing on `for` refuse as
  ambiguous accountability. A seat with no current row has no contact and cannot send.
- `contacts.json` is removed, not demoted. The two rules above cover every sender that may send;
  any other sender is refused with the allowed set, which is empty.
- Unknown parentage (a chain that will not fold, a `role:` scope naming an unchartered role)
  reads as unknown and the send is refused, never as "no contacts".
- For this slice the contact set is the permission to send. Per-kind policy on an edge (for
  example, only a parent may send `order`) is deferred; see open questions.

**Evidence that forced it.**

- The bakeoff converged on exactly this: "contacts derive from org, by consuming validated org
  output (`org` CLI/MCP), never by reparsing `chain.jsonl`, with `contacts.json` demoted to an
  explicit fallback for uncharted roles only, and unknown parentage reading as *unknown* (refuse
  the send)" (<https://github.com/itsHabib/workbench/pull/295#issuecomment-5613272865>). D1
  makes the "uncharted role" case empty: a role is either chartered (in the tree) or a seat (on a
  row), so the fallback has no member and is dropped.
- [P296]'s seat rule admitted a reused seat's previous accountable role through a slot-name match
  alone; a targeted test reproduced it
  (<https://github.com/itsHabib/workbench/pull/295#issuecomment-5613217379>). Matching seat plus
  repository plus branch is the row's identity in [F] ("repo, change, relationship, at").
- Every relay in [S, run-2] was parent-child or lead-seat. The sibling channel [C2] allowed for
  "knowledge questions only" was never used (0 sibling edges).

**What "seat contact = accountable role on the dispatch row" implies.**

- A seat's contact changes when its row changes: a verify row dispatched by lead B makes lead B
  the verifier seat's contact for that head, whoever dispatched the previous one.
- A seat with two live rows is a configuration error, surfaced at send time, not silently
  resolved.
- The reply address on a brief (`--reply-to`) is a hint; the durable address is the role on the
  row, which is what the lead card already says.
- The row is Fleet's; the tree is Org's; mail reads both and writes neither.

**Alternatives rejected.**

- *Declared `contacts.json`* ([P295]): drifts; second copy of D1's tree; chosen only for want of
  an Org read verb.
- *Keep `contacts.json` as fallback* (bakeoff consensus): a fallback nobody can populate is a
  second source waiting to be used. If a real uncharted sender appears, it is either a seat (give
  it a row) or a role (charter it).

### D5. Delivery and liveness: Fleet's hook is the liveness source; the watcher's launch is a Fleet launch; a role is held only while a session is live

**Decision.**

- Liveness of a role is whether a session in that role's directory is alive, as the Fleet hook
  and `SessionAlive` already decide it [F]. An unreadable session record is unknown, never death;
  the watcher does not launch on unknown ([P295]'s rule, ported from
  <https://github.com/itsHabib/workbench/pull/296#issuecomment-5613218342> item 1).
- The watcher launches a session for a role when the role has unacknowledged, undelivered mail
  older than the grace window and no live session. That launch goes through the same admission as
  any other Fleet launch: it takes the directory's lease (the same key the hook would lease for a
  session starting there) before it stamps and starts, so a watcher tick and a chip opened by the
  operator cannot both occupy the directory. This is the FOLLOWUPS item the bakeoff deferred
  ("route watcher launches through the same fleet admission path as any other launch",
  <https://github.com/itsHabib/workbench/pull/295#issuecomment-5613272865>), promoted to a
  precondition of automatic launch.
- With D2, a role is held from SessionStart to SessionEnd. Org's `next_due` is kept as the holder's
  own promised cadence for a long-lived session (a desktop loop) and is read as "late" only while
  held; a released role is idle, never late [O]. It is no longer the primary liveness signal for
  leads.
- Takeover and revoke stay Org operations against a held role, for the one case that needs them:
  a live session that holds the role and is not producing (wedged loop, runaway). Fleet's
  `stop`/`revoke --to` stay the branch-level stand-down. Neither is needed for a headless tick that
  ended; its release was written by the hook.
- Nothing needs a long-lived holder. Sessions are disposable; leases end with sessions; a role's
  continuity is its chain and its mailbox, not a process.

**Evidence that forced it.**

- Headless workers read `abandoned` between turns because `claude -p` ends the session and
  SessionEnd releases leases (friction 6, [R]). That is correct behaviour for a session that is
  gone; what was missing was a wake, which mail delivery supplies.
- Long-lived leads were the expensive part: run 2's three leads ran 38 to 53 turns each on a two
  to three minute cadence, three quarters of 516k output tokens [S, run-2]; run 1b's eleven lead
  ticks cost about $12 against $2.50 for all workers and verifiers [R]. Run 3 removes the
  long-lived sessions by design [C3].
- The delivery stamp "prevents retrying a message; it does not reserve a seat or prevent
  competing launches" (<https://github.com/itsHabib/workbench/pull/295#issuecomment-5613258713>).
  Friction 1 [R] is the same shape from the other side: a session that merely launched a worker
  became the seat's branch holder. Both are "who occupies this directory" questions, and Fleet
  already has the primitive for them.
- `fleet revoke --to <session>` leaves a stop flag that stands down every other session on the
  branch, so a later handoff needs `fleet resume` (friction 10, [R]). Stand-down is per branch;
  it should not be the tool for "the lead is wedged", which is a role question.

**Alternatives rejected.**

- *Keep `next_due` as the lead's liveness and have the watcher read it.* A deadline is an
  agent's promise, not an observation; Fleet's rule is that the board never lies because no fact
  in it was asked of an agent [F].
- *Long-lived lead loops as the delivery mechanism.* Proven in run 1b and run 2, and priced.
- *Let the delivery stamp be the reservation.* Two launch paths (watcher, chip) with different
  admission is how friction 1 happened with branches.

### D6. Roles on directories: a repository root carries no role

**Decision.**

- `fleet role` and `fleet pool` bind roles only to directories made for the purpose: seats
  (pooled worktrees) and lead directories. A path that is a repository's main checkout, or that
  has git worktrees nested beneath it, is refused with the reason and the alternative (make a
  worktree or a sibling directory and role that).
- The projection `fleet role` writes into a directory's harness settings is a marked block.
  Re-roling replaces the block; unbinding removes it. Denies are no longer a high-water mark.
- Existing bindings of repository roots (cc-skills, rooms, rung, per [R]) are the operator's to
  unbind; the refusal applies to new bindings and to re-roling.

**Evidence that forced it.**

- Friction 11 [R]: the workbench root roled `supervisor:workbench` denied `git push` to every
  worktree under it, including the one building `fleet mail`; [P296] had to be pushed by the
  orchestrator for that reason. The rule "a repository root carries no role" was adopted on the
  spot.
- [TS] defect 18: "the checkout / every worktree beneath it / six live sessions booted as
  `supervisor`", the same shape a week earlier on the other machine.
- [TS] §4 (defect 3): a domain policy ("supervisors don't act on work") encoded as a path-scoped
  permission "was inherited by every nested worktree, ~30 on this machine", while the lease hook,
  which knows nothing about roles, held the same line on evidence.

**Alternatives rejected.**

- *Keep the rule in prose.* It was already in prose and happened twice.
- *Stop projecting denies at all.* The seat proof in [R] shows the projection is the whole of the
  worker's permission rule and worked; the defect is the scope it was applied at, not the
  mechanism.

### D7. Seat addressing: a seat is an address

**Decision.**

- The mail address grammar is a chartered role (`supervisor:demo-a`) or a seat name
  (`fleet-demo-sandbox-author-1`). Mailboxes are keyed by `(tenant, address)` with a
  collision-free encoding, as the Codex finding on both PRs asked
  (<https://github.com/itsHabib/workbench/pull/296#discussion_r3975525832>).
- A seat session's own address is its seat name (the fourth column of its `roles.map` line). A
  lead writes to the seat named on the row it dispatched. A seat kind (`author:<repo>`) is not an
  address; a send to one is refused naming the seats that carry the kind.
- The hook injects mail lines for the session's address: the role for a lead directory, the seat
  for a seat. Ack is by a session at that address.

**Evidence that forced it.**

- [C3] moved a task to a different seat kind to avoid a shared mailbox. Bending the work to the
  address grammar is the signal that the grammar is wrong.
- [F]: "A **seat** (a pooled worktree with a name) is a roled directory prepared in advance, so
  the lead can say 'put this branch in `mono-finisher-2`'". The seat is already how a lead names
  where work goes; the same name should be how it reaches the worker there.
- [P65] `SKILL.md`: "Two simultaneous finisher seats do **not** attach to the same exclusive
  `finisher:<repo>` org role. Their membership comes from Fleet's assignment, slot and session."
  Seats are already outside Org's role identity; giving them a role-shaped mailbox reintroduces
  the shared identity the skill removed.
- The recommendation: "A role survives sessions and can use different seats. A seat is execution
  capacity; a session occupies it temporarily… These should not become competing meanings of
  'slot.'" (<https://github.com/itsHabib/workbench/pull/295#issuecomment-5613258713>).

**Alternatives rejected.**

- *One mailbox per role, filtered by dispatch row at read time.* Both seats' hooks would inject
  both seats' mail; an ack by the wrong seat is silent; the "five lines then `and N more`" cap
  fills with a sibling's traffic.
- *Address sessions.* Two id spaces [R], and a headless session has no id until it exists.

### D8. The tick rule: one send per addressee per tick, any number of intent-wrapped local effects

**Decision.** Replace "at most one authorized coordination action" ([P65] `SKILL.md`, step 3)
with:

> At most one outbound send per addressee per tick. Any number of independent local effects
> (dispatch, seat assignment, verify row, launch, correlated answer), each preceded by
> `org intent` and followed by `org resolve` with its read-back. The tick ends when the reconciled
> view has no eligible effect left.

"One send per addressee" rather than "one send per tick" because the two-effect case that forced
the change had two addressees (answer the worker, escalate to the parent), and [C3] already runs
under "one `fleet send` per addressee per tick". Two sends to one addressee in a tick are either
the same retry-safe id or a violation.

**Evidence that forced it.**

- [R], "Changes to make" item 1: "lead A tick 2 last night had two independent effects ready (act
  on the finisher answer, escalate the bench order) and parked one for three minutes and two
  dollars; tonight every tick spent most of its turns on attach/claim/yield/release around one
  effect… The safeguard that mattered in practice was intent-before-effect, not the count."
  Proposed wording on [P65]:
  <https://github.com/itsHabib/cc-skills/pull/65#issuecomment-5612044415>.
- Lead A tick 4 in the live loop exercised judgment on a verifier inconsistency and re-dispatched
  a verify row within its contract [R]; the one-action rule would have split that across two
  ticks with nothing gained.

**Alternatives rejected.**

- *One action per tick* (the current rule): priced above; its safety came from
  intent-before-effect, which is kept for every effect.
- *No send limit.* The send is the one effect another role must spend a turn on; the limit is a
  budget on other roles' attention, not on the sender's.

### D9. What Org keeps: charters, the tree, the authored record, and displacement of a held role

**Decision.** After D1 to D8, Org owns:

- **Charters** and their terms: scope, effect classes, ceilings, and `supervisors` as takeover
  authority. Chartering, recharter, retire, split, merge, delegate: unchanged.
- **The tree**, as the projection of `role:` scopes, published through `org tree`.
- **The authored record** of every chartered role: intents, resolutions, escalations,
  checkpoints, held work where a role has any, and the boot index over them.
- **Displacement of a held role**: takeover, revoke and the fence, for the live-but-wedged case.
- **Continuity instruments**: `boot`, `sweep`, `verify`, `intake`, `transfer`, `annul`.

Org stops being: the transport (`message` as delivery), the lead's per-tick lifecycle (attach and
release are hook-written; assign/claim/yield are not a precondition for intents), the liveness
oracle for leads (`next_due` is a promise, read only while held), and the topology every reader
re-derives.

**Evidence that forced it.** The recommendation's proposed split
(<https://github.com/itsHabib/workbench/pull/295#issuecomment-5613258713>): "Retaining Org is
justified by continuity, what a replacement agent inherits, not by maintaining a second
holder/claim/liveness loop." Tonight the continuity machinery was what worked: every hop in the
run 1a relay was reconstructible from chains alone, and the metrics table [R] was built "from the
chains and the PRs, not from anyone's say-so". The holder/claim/liveness loop was what cost.

**Alternative rejected.** *Retire Org and keep role history in Fleet.* Fleet may not hold
authored facts [F]; [V] Stage B's destination for the chain is gate's ledger, not Fleet's store;
and the migration that would absorb Org is that one, not this one. The boundary here is drawn so
that D2's record moves into a signed ledger unchanged.

## Who owns what, after the decisions

| fact | owner (writer) | readers | how a reader gets it |
|---|---|---|---|
| identity: directory to tenant, role, seat | Fleet (`fleet role`, `fleet pool`) writes `roles.map` | Fleet hook, Org SessionStart hook | the file; line format is the contract |
| topology: parent and children | Org (charter `role:` scopes) | mail contacts, board helper, watcher | `org tree -json` |
| leases: branch, resource, directory | Fleet hook, `take`/`drop`, watcher launch | everyone | `fleet leases`, refusals |
| rows: change, relationship, accountable role, seat | Fleet (`fleet dispatch`, `reassign`) | leads, seats, mail (seat contact) | `fleet work --json` |
| receipts: kind, verdict, head, session | Fleet (`fleet receipt`) | `fleet done`, gate, leads | `fleet receipts`, PR mirror |
| messages: body, delivery, ack | Fleet (`fleet send`, watcher, `fleet ack`) | addressee's hook lines, `fleet mail` | the mail record |
| lead record: intent, resolution, checkpoint | Org (the role's session, as incarnation) | next incarnation, parent, `sweep` | `org boot -json`, `org log` |
| escalation: open question, its closure | Org (`escalation` + `resolution` on the asker's chain); the words travel as mail | parent, `boot`, `sweep` | `org boot` dangling obligations |
| liveness | Fleet hook (session alive); Org `next_due` only while held, as the holder's promise | watcher, board | `fleet sessions`, `fleet board` |
| authority | Org charter terms (scope, effect classes, takeover); gate for merge | kernel admission; gate | refusals with the reason id |
| attach and release of a role | Org SessionStart and SessionEnd hooks | `org boot` | the chain |

A message is never an assignment; a row is never an acknowledgement; a receipt is never a merge.

## Migration, in order, with the PRs that carry each step

1. **`org tree`** (workbench, new PR after this one): the read verb from D1, over the existing
   fold; refuses multiple parents, cycles and unchartered `role:` targets. No kernel change.
2. **Land [P295] as the mail base** with the ports the bakeoff agreed: [P296]'s retry identity
   (sender role plus content, original session kept as provenance); contacts from `org tree`
   plus the dispatch row (D4), `contacts.json` removed; `(tenant, address)` mailbox keys and seat
   addresses (D7). Keep [P295]'s watcher liveness rule. Close [P296] with credit once these are
   reviewable, as its thread recorded.
3. **Kernel and hooks** (workbench, one PR): `intent-ref` admissible from `Held` ([K]
   `phaseLaw`); SessionStart hook attaches and SessionEnd hook releases, fail-open (D2, D5).
   Conformance test updated; `sweep` unchanged.
4. **Watcher launch through Fleet admission** (workbench, one PR): the launch takes the
   directory lease before stamping (D5). Until it lands, automatic launch is a run-contract
   feature, not a default.
5. **`fleet role` refusals and projection blocks** (workbench, one PR): refuse repository roots
   and paths with nested worktrees; marked projection block; unbind removes it (D6). Operator
   unbinds the roots still bound.
6. **[P65] revised** (cc-skills): the tick rule (D8); the record shape (D2); mail as the
   transport with `escalation`/`resolution` around a send (D3); contacts from the tree and the
   row (D4); the lead and author cards updated to match; `native-messaging.md` reduced to the
   desktop case for attended runs.
7. **Run 4** in `itsHabib/fleet-demo-sandbox`: the run 3 contract against the landed binary,
   with the conflict forced on stage as [R] asks, scored by `scripts/run-metrics.py`. Exit
   criterion: every relay in the scorecard joins by id, zero sends to the operator, and lead
   ticks whose chain gains only intent, resolution, escalation and checkpoint records.

Steps 1, 3, 4 and 5 are independent of each other; 2 depends on 1; 6 depends on 2 and 3; 7 on
all of them.

## Open questions that remain

1. **Reaching the operator.** The overall lead is the only role that addresses the operator, who
   has no directory and no mailbox. Flare is the notification sink for receipts; whether an
   `escalation` whose target is `human:<name>` should be delivered by flare, by the desktop, or
   only in the lead's digest is not settled here.
2. **Permission per kind on an edge.** D4 makes the contact set the permission. Whether `order`
   is parent-only, whether a seat may send `escalation`, and whether a sibling knowledge question
   ever earns a channel are policy questions for the cards, once mail has run under D4 at least
   once.
3. **Receipts append or replace.** A second receipt of the same kind at the same head replaces
   the first (friction 9, [R]); a fail is erased by a later pass. Not a boundary question, but the
   verifier inconsistency in run 1b is only visible in a PR comment today.
4. **Cross-machine mail.** Two machines share only the git remote; rows and receipts are mirrored
   to the PR and pulled back by `fleet sync` [F]. Whether mail is mirrored the same way, or a
   second machine's roles are simply not contacts, is undecided.
5. **Stage B.** [V] plans to fold the Org chain into gate's ledger. D2 and D9 are drawn so the
   authored record moves unchanged; whether `org tree` survives as a gate read or stays an `org`
   verb is that migration's call.
