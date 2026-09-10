# Org / Fleet boundary: who owns which fact

**Status:** draft decision document, for review. **Date:** 2026-09-10. **Scope:** docs only; every
decision below names the PR that will carry it. Nothing here changes code by itself.

**Revision note.** The first two panel rounds reviewed a version that kept Org's holder for lead
roles, wrapped every effect in an intent, made mail depend on a contact policy derived from Org,
and designed automatic launch into the mail slice. The second round's three P1s all landed on
the holder machinery, and a further review against the first head found that the contact policy
and the launch design restored complexity the operator had already removed from [P295] at
`60bbbf1`. This revision removes the machinery instead of patching it: exclusivity has one owner,
effects are idempotent by id, the lead's record shrinks to three kinds, mail is same-tenant and
open, and automatic launch is deferred to its own contract with the run 3 findings as its
acceptance. D2, D3, D4, D5, D8, D9, the table, the migration and the open questions changed.

## Sources

Every decision cites these by the tag in brackets.

- [R] Rehearsal record, three runs on 2026-09-09:
  <https://github.com/itsHabib/fleet-demo-sandbox/blob/main/docs/REHEARSAL-2026-09-09.md>.
  Frictions 1 to 17, the message metrics table, "Changes to make before the next run", and the
  run 3 first findings (mail-woken sessions, the per-message launch storm, the live-but-idle
  holder).
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
  comment is quoted its URL is given. The bakeoff argued over heads `bb26d7d` to `1c9d03d` of
  [P295]; the operator then approved a smaller contract, pushed at `60bbbf1`: same-tenant role
  send, read and ack, hook notification lines, replacement-sender retries, and no contacts
  allowlist, charter traversal, dispatch restriction, automatic launch, delivery configuration
  or delivery stamp (<https://github.com/itsHabib/workbench/pull/295#issuecomment-5613417453>).
  That head is what "mail" means below.
- [TS] cc-skills `docs/features/agent-fleet-rules/TWO-SYSTEMS.md`, [V]
  `docs/features/agent-fleet-rules/VISION.md`, [P62] <https://github.com/itsHabib/cc-skills/pull/62>
  (the substrate/policy boundary), and [P65] <https://github.com/itsHabib/cc-skills/pull/65>
  (both cc-skills PR numbers; [P295] and [P296] are workbench PR numbers) at
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
   `chain.jsonl` itself. [P295], before its simplification, declared contacts in a Fleet-side
   `contacts.json`. Four sources for one tree, and the bakeoff's final verdict was that the one
   thing that cannot be retrofitted is which of them is true
   (<https://github.com/itsHabib/workbench/pull/295#issuecomment-5613272865>). The operator's
   answer was to take contacts out of mail altogether; the tree question remains for the roles
   that read it.
2. **The lead's tick is mostly ceremony.** The tick procedure was attach, assign (a pinned
   placeholder), claim, intent, one effect, resolution, checkpoint, yield, release [R]. The
   lifecycle part ran on every tick whether or not an effect was eligible: in the live loop run
   the overall lead's chain gained 8 attach, 8 claim, 8 checkpoint, 8 yield and 8 release records
   while only one tick produced an intent, a message and a resolution [S, run-1b]. Lead ticks cost 18 to 33 turns
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
   mailbox is per role and two author seats would share one" [C3]; friction 12 [R] records it as
   "the seat (slot) needs to be an address".
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
has one writer; a reader consumes the owner's validated output, never its storage.** Two
corollaries carry most of the weight below. Who occupies a directory right now is an observed
fact, so exclusivity belongs to Fleet and Org does not keep a second holder for a lead. And an
effect that is safe to repeat needs no ledger of intentions in front of it, so the retry-safe id
that `fleet request` and `fleet send` already use becomes the rule for every effect a lead takes.

## Decisions

### D1. Identity and topology: charters are the tree, `roles.map` is the binding, `org` is the one reader

**Decision.**

- *Topology* (which role reports to which) lives in Org charters and nowhere else. A role's parent
  is the role whose charter scope names `role:<child>` ([P65] `SKILL.md`, "Reporting edges derive
  from parent charter scopes `role:<child>`"). `terms.supervisors` is takeover authority only and is
  never read for topology. Multiple parents, cycles and a `role:` scope naming a role that is not
  chartered are errors that `org tree` detects and refuses, not guesses. The kernel has no
  cross-charter check ([K]: `Terms.Scope` and `Terms.Supervisors` are plain lists), and this
  document does not add one; the check lives in the read verb.
- *Identity* (which directory is which role) lives in `roles.map` under `$ORG_STATE`, with the
  columns it has today: path, tenant, role, optional seat name. Fleet is its only writer
  (`fleet role`, `fleet pool`). It is a machine-local artifact, because directories are.
- *One reader.* Org gains a read verb, `org tree -json`, that folds every chartered role in the
  tenant and returns, per role: parent, children, scope, takeover authority, and the `roles.map`
  paths bound to it. The supervisor helper's board, a lead deciding where its escalation goes,
  and any future board consume that output through the CLI or the MCP face. Nobody outside
  `contracts/org` and `cmd/org` parses `chain.jsonl`. `roles.map` stays readable by both hooks
  because its line format is a contract, but neither hook derives a tree from it.
- *The tree is for discovery, not for permission.* Mail does not read it (D4). A malformed tree
  is a board error and a routing question for the lead, never a reason a message cannot be sent.

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
  chose it only because "Org has no cheap published contacts relation"; D1 supplies that relation,
  so it completes what [P295] deferred rather than choosing against it.
- *Add parent columns to `roles.map`.* The map is per machine and per directory; the tree is per
  tenant and must be the same on a second machine that shares only the git remote [F, "no
  cross-machine store"].

**Consequences.** `roles.map` loses nothing. `org tree` is a new read verb (exit 0, JSON), cheap to
build over the existing fold. The helper's topology code in [P65] `board.py` becomes a consumer
of that verb rather than a second fold.

### D2. The lead's record: escalation, resolution, checkpoint, on Org's chain; no holder, no lifecycle

**Decision.**

- A lead's authored record is exactly three kinds: an `escalation` when a question goes up
  (D3), the `resolution` that closes it, and one `checkpoint` per tick with the conclusion. That
  record stays on the role's Org chain. It does not move beside the Fleet store.
- A lead role has no holder. The kernel admits `escalation`, `resolution`, `checkpoint`, `mark`
  and `note` from the `Chartered` phase with `incarnation` set to the writing session's id and no
  holder check. Nothing attaches, claims, yields or releases; no intent is written. The
  `Held`/`Active` phases, `attach`, `claim`, `takeover`, `revoke` and the fence stay in the kernel
  unchanged for roles that hold durable work across sessions (the operator's own lanes), which a
  lead is not.
- Exclusivity of the chain is not Org's concern: a session writes as the role because it
  occupies the role's directory, which Fleet's session store records. Org records who wrote (the
  incarnation), serializes appends under its lock so the chain never forks, and detects rather
  than prevents two occupants writing in turn (friction 17 [R] produced four), the same stance
  `sweep` already takes for scope drift and assign conflicts [O]. Whether occupancy should become
  a lease is a question for the launcher that would need it (D5, open question 1).

**Evidence that forced it.**

- Chain shapes [S]. Run 1b, overall lead: 8 attach, 8 claim, 8 checkpoint, 8 yield, 8 release
  around 1 intent, 1 message and 1 resolution. Run 2, lead B: 9 claim, 9 yield, 18 mark, 19
  checkpoint around 10 intents, 12 resolutions, 2 escalations. Nobody read an attach, claim,
  yield, release or intent record tonight; the parent read children's escalations and
  checkpoints.
- The pinned `assign` was a placeholder by construction: [C2] tells bucket leads to "pin their
  branch with text `"<task> per RUN-CONTRACT run 2"` until their draft PR exists", because the
  kernel admits `intent-ref` only from an active claim ([K] `phaseLaw`). A record written to
  satisfy a law, not to state a fact, is ceremony.
- Cost [R]: "tonight every tick spent most of its turns on attach/claim/yield/release around one
  effect"; lead ticks 18 to 33 turns, $1.85 to $3.39; eleven live-loop ticks about $12.
- The intent's job is restart without repeating an effect ([P65] `coordination.md`, "On restart,
  read open intents before taking a new action"). Every effect a lead takes is, or becomes
  (D8), retry-safe by a caller-chosen id: `fleet request` returns the existing assignment for the
  same id and payload [F]; `fleet send` does the same [P295]. With that, restart is "run it again
  with the same id" and the intent ledger has no reader left.
- Keeping a holder for a disposable session needed, in the reviewed draft, three hook rules
  (seats never attach, yield before release, a late release when Fleet proves the holder dead),
  a kernel relaxation, and a persisted join from an attach digest to a harness session id that
  nothing today records. All of it existed to reconcile two owners of one fact.
- [V] §4 names the destination: "Attaching to a lane becomes a side effect of a session
  starting… nobody attaches, the hook does." Removing the attach goes one step further for the
  same reason.

**Alternatives rejected.**

- *Keep the holder and have hooks attach and release.* The reviewed draft; priced above. The
  missed-release path alone needed a new artifact (incarnation to session) and a Fleet query
  Org does not have.
- *Move the lead's record beside the Fleet store.* Fleet's first rule is that facts come from
  the hook and never from an agent [F]; an escalation and a conclusion are authored judgment.
  It would also create a third store with no hash chain and no boot index, days before [V]
  Stage B plans to fold the Org chain into gate's ledger. The boundary here should be the one
  that ports there.
- *Drop `checkpoint` and let mail carry the conclusion.* The boot index is built from the
  checkpoint; a mail record is addressed to one recipient and is not the role's own last word.

**Consequences.** `org boot` for a lead shows the charter, open escalations and the last
checkpoint; the holder line reads "none (lead)". The Stop hook's `mark` stays; a tick that ends
without a checkpoint still renders `degraded`. `sweep`'s "obligations orphaned by a displaced
holder" has no member for leads, because nothing displaces a holder; open escalations are
still counted as dangling.

### D3. Messaging: mail is the transport and the record of a send; Org keeps `escalation`/`resolution` for a pending question; Org's `message` kind is retired from the procedure

**Decision.**

- Every agent-to-agent message travels as `fleet send` and is read with `fleet mail`/`fleet ack`,
  as in [C3]. Mail is a Fleet verb because delivery is an observed fact: a record exists, it was
  delivered, it was acknowledged, and none of those is an agent's claim.
- The mail record is the record of the send. A send carries a caller-chosen id and is retry-safe
  (same id and payload, same record), so nothing is written on the sender's chain for an answer,
  an order, a report or a request.
- A question that goes up is additionally an `escalation` on the asker's chain, whose subject
  names the mail id (`mail:<address>:<id>`), closed by a `resolution` that names the answer's
  mail id. The chain therefore shows a pending question as a dangling obligation, which `boot`
  and `sweep` already render; the mail record carries the words.
- Org's `message` kind stays in the kernel (removing a kind changes canon) but no lead procedure
  writes it. A lead does not read another role's chain to find mail.
- A seat has no chain. Its question, report and handoff are mail records and PR comments, as
  [C3] already has it; nothing above applies to a seat.

**Evidence that forced it.**

- Run 1a's relay by chain: a worker's refusal became a PR comment, then an escalation on lead A's
  chain, then the overall lead read both children's chains and answered on its own chain, then
  lead A read the parent's chain on its next tick [R]. Every hop cost a full tick (three to four
  minutes) and every role had to read every child chain every tick.
- Run 2's relays over desktop messaging took 16 to 200 seconds per relay and needed no chain
  reads, but 3 of 25 sends failed on the session-id mismatch and 9 carried no id [S, run-2]. Run 3
  keeps the addressing (role) and drops the transport's dependence on a live session id [C3].
- [C3] already treats a seat's send as its record: "seats have no chain; their send is the
  record". A retry-safe send is its own record for a lead too; the intent/resolve pair around it
  in [C3] guarded against a duplicate send that the id already prevents.
- The obligation still matters: [O] `sweep` counts obligations orphaned versus discharged; an
  unanswered question must be visible there, and a mail record is not on any chain.
- The recommendation's line: "An `order` is a message, not an assignment or authorization. An
  acknowledgement is not completion."
  (<https://github.com/itsHabib/workbench/pull/295#issuecomment-5613258713>). Mail carries words;
  authority and obligations stay where they are.

**Alternatives rejected.**

- *Keep Org `message` as the transport.* It cannot wake anyone, it needs the recipient to poll
  the sender's chain, and it puts a body on a hash chain that exists to hold conclusions.
- *Let mail replace `escalation` as well.* Then a pending question has no obligation record, the
  boot index cannot show it, and a successor session cannot see what its predecessor was
  waiting on.
- *Wrap every send in `intent`/`resolution`* ([C3], the reviewed draft). Two chain records per
  send to guard against a duplicate the message id already makes harmless, and a kernel bound
  (one open intent) that serialized effects for no benefit.
- *Mirror every mail record onto the chain.* [P65] `native-messaging.md` already ruled it out:
  "Do not mirror every conversational acknowledgement into another ledger."

### D4. Contacts: any identified role in the tenant may write to any address in it; the tree is discovery, the one-hop rule lives in the card

**Decision.**

- `fleet send` accepts any address in the sender's tenant from any session whose identity
  `roles.map` resolves. Tenant is the only check, as [P295] at `60bbbf1` implements it. There is
  no contacts allowlist, no charter traversal at send time, no dispatch-row restriction, and no
  refusal for a seat without a row.
- Who a role *should* write to is policy, and it lives where policy lives: the lead card and the
  supervisor skill say "your parent and your declared children, and nothing sideways", and
  `org tree` (D1) tells a lead who those are. A worker's brief names its accountable lead. A
  message outside that shape is a contract violation the receiving role reports, not a send the
  substrate refuses.
- Mail confers nothing. A message is never an assignment, a resource, an authorization or a
  completion; the recipient checks its own charter, rows and receipts before acting on one.

**Evidence that forced it.**

- The operator's approved contract for mail: "any identified role can communicate with any
  identified role in its tenant. There is no contacts allowlist, charter traversal, dispatch
  relationship restriction…" ([P295] body at `60bbbf1`;
  <https://github.com/itsHabib/workbench/pull/295#issuecomment-5613417453>). Both bakeoff shapes
  (derived contacts, declared contacts) made an ordinary send depend on a second system's state
  and refuse on its faults; the reviewer of this document's first head called that "restoring the
  complexity the operator explicitly removed"
  (<https://github.com/itsHabib/workbench/pull/297#issuecomment-5613656842>).
- The one-hop rule held by card alone: run 2's contract allowed sibling knowledge questions and
  none were sent (0 sibling edges, [S, run-2]); every relay was parent to child or lead to seat.
  A rule that holds without a mechanism does not need one yet, and [P62]'s line applies:
  Fleet checks admission and provenance, the caller says what is owed.
- [TS] §4, defect 3: a domain policy ("supervisors don't act on work") encoded as a substrate
  permission failed because the permission could not tell the cases apart, while the
  opinion-free mechanism held. A contact allowlist is the same shape.
- Friction 14 [R]: an Org chain wedged between ticks and the lead could not write its intent.
  Under derived contacts the same wedge would have stopped the send too.

**Alternatives rejected.**

- *Derived contacts as permission* ([P296], and this document's first head): makes every send
  depend on Org's fold and refuse on a malformed tree; couples the substrate to a policy it
  cannot explain. The derive-from-Org *decision* survives in D1, as discovery.
- *Declared `contacts.json`* (early [P295]): a second copy of the tree that drifts.
- *Seat contact from the dispatch row* (this document's first head): blocks a seat's first
  question when its row is late or ambiguous, the moment a worker most needs to ask.

**What remains true about seats and rows.** The accountable role on a seat's row is where its
report belongs and where `--reply-to` points; that is routing advice in the brief, not a rule in
the verb. A seat writing elsewhere is visible in the mail record and on the board.

### D5. Delivery and liveness: Fleet's hook is the liveness source; mail is delivered by the existing hook lines; automatic launch is deferred to its own contract

**Decision.**

- Liveness of a role is whether a session in that role's directory is alive, as the Fleet hook
  and `SessionAlive` already decide it [F]. An unreadable session record is unknown, never death.
- Delivery is what [P295] at `60bbbf1` does: up to five unacknowledged mail lines at SessionStart
  and every UserPromptSubmit, then the count, no automatic ack. A role with no session keeps its
  mail queued until something starts a session in its directory: the operator, a loop the
  operator runs, or a launcher that does not yet exist. Mail is usable without any of that.
- Automatic launch is not a decision this document makes. It is a separate contract, built when
  a real caller needs it ([P62]: "a separate application earns its place when a real caller
  needs it"), and its acceptance is already written by run 3: one launch per role per watcher
  fold carrying every unread message; a started launch counts as live until its session record
  appears or a timeout passes; a live session with no open turn and idle past a grace counts as
  absent (frictions 16 and 17, [R]). It needs its own proof that two launch paths cannot both
  occupy a directory; the current SessionStart path records a session and returns context, and
  is not that proof (<https://github.com/itsHabib/workbench/pull/297#issuecomment-5613656842>,
  item 4). Until then, launch stays a run-contract feature on a scratch binary, as in [C3].
- With D2, a lead has no holder, so Org's `next_due`, takeover and revoke are not read or
  written for leads. They keep their meaning for roles that hold durable work. A wedged lead
  session is a Fleet matter: the operator ends it, or `fleet stop` stands it down on what it
  holds; the next session in the directory simply starts.
- Nothing requires a long-lived session. Sessions are disposable; branch and resource leases
  end with sessions; a role's continuity is its chain and its mailbox, not a process. A
  long-lived desktop loop is allowed and is simply a session that stays, with friction 16 as the
  warning about what an idle one costs.

**Evidence that forced it.**

- Run 3's first findings [R]: mail woke nine sessions with no human or scheduler in the loop,
  and then two defects of the launch path stalled it, both in the [P295] build before the
  simplification: a live-but-idle desktop chip blocked delivery for two hours (friction 16), and
  one fold started four sessions for one role, one per unread message (friction 17). The
  operator removed automatic launch from the mail slice the same hour
  (<https://github.com/itsHabib/workbench/pull/295#issuecomment-5613417453>).
- Headless workers read `abandoned` between turns because `claude -p` ends the session and
  SessionEnd releases leases (friction 6, [R]). Correct for a session that is gone; the wake is
  the launcher's job, when there is one, and the mail waits meanwhile.
- Long-lived leads were the expensive part: run 2's three leads ran 38 to 53 turns each on a two
  to three minute cadence, three quarters of 516k output tokens [S, run-2]; run 1b's eleven lead
  ticks cost about $12 against $2.50 for all workers and verifiers [R].
- This document's first head designed the launcher into D5 (a watcher-owned directory
  reservation handed to the child, then a hook-taken lease with a dead-holder rule). The second
  panel round found the handoff unspecified and the dead-lease recovery absent; the later
  review found the exclusion boundary asserted, not proven. Both were right, and both are
  answered by not deciding the launcher here.

**Alternatives rejected.**

- *Keep `next_due` as the lead's liveness and have a launcher read it.* A deadline is an
  agent's promise, not an observation; Fleet's board never lies because no fact in it was asked
  of an agent [F].
- *Design the launcher in this document* (its first head): every P1 the panel raised on D5 was a
  launcher question, and the mail slice does not need the answers.
- *Long-lived lead loops as the delivery mechanism.* Proven in run 1b and run 2, and priced.

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

- [C3] moved a task to a different seat kind to avoid a shared mailbox, and friction 12 [R]
  names the fix: "the seat (slot) needs to be an address". Bending the work to the address
  grammar is the signal that the grammar is wrong.
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

### D8. The tick rule: one send per addressee per tick, any number of idempotent local effects

**Decision.** Replace "at most one authorized coordination action" ([P65] `SKILL.md`, step 3)
with:

> At most one outbound send per addressee per tick. Any number of independent local effects
> (dispatch, seat assignment, verify row, launch, correlated answer), each carrying a
> caller-chosen id so that running it again is harmless. The tick ends when the reconciled view
> has no eligible effect left.

Two rules make this safe without an intent ledger:

- Every Fleet effect verb takes `--id` and is retry-safe: same id and payload returns the
  existing record, a changed payload under the same id refuses. `fleet request` and
  `fleet send` already do this [F], [P295]; `fleet dispatch` and `fleet assign` get the same
  treatment (migration step 5). Restart is "run the tick again"; the ids make the second run a
  no-op where the first succeeded.
- "One send per addressee" rather than "one send per tick", because the two-effect case that
  forced the change had two addressees (answer the worker, escalate to the parent), and [C3]
  already runs under "one `fleet send` per addressee per tick". Two sends to one addressee in a
  tick are either the same retry-safe id or a violation.

**Evidence that forced it.**

- [R], "Changes to make" item 1: "lead A tick 2 last night had two independent effects ready (act
  on the finisher answer, escalate the bench order) and parked one for three minutes and two
  dollars; tonight every tick spent most of its turns on attach/claim/yield/release around one
  effect… The safeguard that mattered in practice was intent-before-effect, not the count."
  Proposed wording on [P65]:
  <https://github.com/itsHabib/cc-skills/pull/65#issuecomment-5612044415>. The safeguard's job
  is no duplicate effect on restart; an id does that job mechanically, and [V] §2's thesis is
  that a rule that matters must be a mechanism.
- Lead A tick 4 in the live loop exercised judgment on a verifier inconsistency and re-dispatched
  a verify row within its contract [R]; the one-action rule would have split that across two
  ticks with nothing gained.
- The kernel bounds a role to one open intent ([K] `checkIntent`), so intent-wrapped effects
  were serialized whether or not they touched the same thing. Ids need no such bound.
- Friction 14 [R]: in run 3 a lead's `org intent` was refused (`claim_active`) while yield and
  resolve both said there was nothing open, so the wrapper was unwritable for a tick and the
  lead recorded its send with `org note`. "The record survived, the ceremony did not."


**Alternatives rejected.**

- *One action per tick* (the current rule): priced above.
- *Any number of intent-wrapped effects* (the reviewed draft, [C3]): two chain records per
  effect and kernel-enforced serialization, to guard against a duplicate the id prevents.
- *No send limit.* The send is the one effect another role must spend a turn on; the limit is a
  budget on other roles' attention, not on the sender's.

### D9. What Org keeps: charters, the tree, the authored record, and the held-work machinery for roles that hold work

**Decision.** After D1 to D8, Org owns:

- **Charters** and their terms: scope, effect classes, ceilings, and `supervisors` as takeover
  authority. Chartering, recharter, retire, split, merge, delegate: unchanged.
- **The tree**, as the projection of `role:` scopes, published through `org tree`.
- **The authored record** of every chartered role: for leads, escalations, resolutions and
  checkpoints; for roles that hold durable work, additionally held work, claims and intents as
  today; and the boot index over all of it.
- **The held-work machinery** for roles that use it: attach, claim, yield, complete, takeover,
  revoke, the fence, `next_due`. None of it is written for a lead.
- **Continuity instruments**: `boot`, `sweep`, `verify`, `intake`, `transfer`, `annul`.

Org stops being: the transport (`message` as delivery), the lead's lifecycle (no attach, claim,
yield, release or intent for a lead), the exclusivity mechanism for a lead's chain (directory
occupancy is Fleet's), the liveness oracle for leads, and the topology every reader re-derives.

**Evidence that forced it.** The recommendation's proposed split
(<https://github.com/itsHabib/workbench/pull/295#issuecomment-5613258713>): "Retaining Org is
justified by continuity, what a replacement agent inherits, not by maintaining a second
holder/claim/liveness loop." Tonight the continuity machinery was what worked: every hop in the
run 1a relay was reconstructible from chains alone, and the metrics table [R] was built "from the
chains and the PRs, not from anyone's say-so". The holder/claim/liveness loop was what cost.

**Alternative rejected.** *Retire Org and keep role history in Fleet.* Fleet may not hold
authored facts [F]; [V] Stage B's destination for the chain is gate's ledger, not Fleet's store;
and the migration that would absorb Org is that one, not this one. The boundary here is drawn so
that the lead's three record kinds move into a signed ledger unchanged.

## Who owns what, after the decisions

| fact | owner (writer) | readers | how a reader gets it |
|---|---|---|---|
| identity: directory to tenant, role, seat | Fleet (`fleet role`, `fleet pool`) writes `roles.map` | Fleet hook, Org SessionStart hook | the file; line format is the contract |
| topology: parent and children | Org (charter `role:` scopes) | board helper, a lead routing an escalation | `org tree -json` |
| exclusivity: who holds a branch or a resource | Fleet hook (`repo:`), `take`/`drop` (`slot:`) | everyone | `fleet leases`, refusals |
| occupancy: which sessions are in a directory | Fleet hook (session records); observed, not leased | board, a future launcher | `fleet sessions` |
| who may send to whom | nobody enforces beyond tenant (Fleet); the card states the shape | the receiving role | the mail record, the board |
| rows: change, relationship, accountable role, seat | Fleet (`fleet dispatch`, `reassign`), retry-safe by id | leads, seats, the board | `fleet work --json` |
| receipts: kind, verdict, head, session | Fleet (`fleet receipt`) | `fleet done`, gate, leads | `fleet receipts`, PR mirror |
| messages: body, ack | Fleet (`fleet send`, `fleet ack`); notification by the hook lines | addressee's hook lines, `fleet mail` | the mail record |
| lead record: escalation, resolution, checkpoint | Org (the occupying session, as incarnation) | next session, parent, `sweep` | `org boot -json`, `org log` |
| escalation: open question, its closure | Org (`escalation` + `resolution` on the asker's chain); the words travel as mail | parent, `boot`, `sweep` | `org boot` dangling obligations |
| liveness | Fleet hook (session alive); Org `next_due` only for roles that hold work | watcher, board | `fleet sessions`, `fleet board` |
| authority | Org charter terms (scope, effect classes, takeover); gate for merge | kernel admission; gate | refusals with the reason id |
| held work, holder, fence | Org, for roles that hold durable work; never for a lead | `org boot`, `sweep` | the chain |

A message is never an assignment; a row is never an acknowledgement; a receipt is never a merge;
a lead has no holder; a send is never refused for who the sender is, only for where.

## Migration, in order, with the PRs that carry each step

1. **Land [P295] at its simplified head** (`60bbbf1` or later): same-tenant send, read, ack,
   hook lines, replacement-sender retries. Nothing in this document is a prerequisite for it.
   Close [P296] with credit; its retry identity is already in.
2. **Seat addresses** (workbench, one small PR on top of 1): the address grammar accepts a seat
   name; mailboxes keyed by `(tenant, address)` with a collision-free encoding; the hook's mail
   lines keyed by the session's address (D7).
3. **`fleet role` refusals and projection blocks** (workbench, one PR): refuse repository roots
   and paths with nested worktrees; marked projection block; unbind removes it (D6). The operator
   unbinds the roots still bound.
4. **Kernel: lead records without a holder** (workbench, one PR): `escalation`, `resolution`,
   `checkpoint`, `mark` and `note` admissible from `Chartered` with `incarnation` required ([K]
   `phaseLaw`, `validate.go`); `boot` renders "no holder (lead)"; the `cmd/org` guide pair
   updated so `message` is a dormant kind and a lead's record is the three kinds (D2, D3).
   Conformance test updated. Relaxing admission refuses no historical record.
5. **`org tree`** (workbench, one PR): the read verb from D1, over the existing fold; refuses
   multiple parents, cycles and unchartered `role:` targets. Consumed by the supervisor helper
   and by a lead choosing where an escalation goes. Not read by mail.
6. **Idempotent effects** (workbench, one PR): `fleet dispatch` and `fleet assign` take `--id`
   with `fleet request`'s retry semantics (D8).
7. **[P65] revised** (cc-skills): the tick rule (D8); the record shape (D2); mail as the
   transport, `escalation`/`resolution` only around a pending question (D3); the one-hop rule
   stated as the card's policy over `org tree`, with no send refused for it (D4); the lead and
   author cards updated to match; `native-messaging.md` reduced to the desktop case for attended
   runs.
8. **Run 4** in `itsHabib/fleet-demo-sandbox`: the run 3 contract against the landed binary,
   sessions started by the operator or a run-contract launcher on a scratch build, the conflict
   forced on stage as [R] asks, scored by `scripts/run-metrics.py`. Exit criterion: every relay
   in the scorecard joins by id, zero sends to the operator, no send refused, and lead chains
   that gain no record kinds other than escalation, resolution, checkpoint and the Stop hook's
   mark.
9. **A launcher, if run 4 shows a caller needs one** (workbench, its own design note first):
   the contract in D5 (one launch per role per fold, a started launch counts as live, idle past
   a grace counts as absent) and a proven occupancy boundary, with frictions 16 and 17 as the
   acceptance scenarios.

Order of dependence: 2 needs 1; 7 needs 2, 4, 5 and 6; 8 needs 7; 9 needs 8. 1, 3, 4, 5 and 6
can start now.

## Open questions that remain

1. **The launcher's occupancy boundary.** A launcher needs proof that a watcher fold and a
   session the operator opens cannot both occupy one directory, and a rule for a dead occupant.
   Fleet has that proof for branch leases (the model and the suite [F]); whether a `dir:` key
   gets the branch rule, or occupancy stays an observed fact and the launcher serializes itself
   another way, is step 9's design question. Frictions 16 and 17 [R] are its acceptance tests.
2. **Reaching the operator.** The overall lead is the only role that addresses the operator, who
   has no directory and no mailbox. Flare is the notification sink for receipts; whether an
   `escalation` whose target is `human:<name>` should be delivered by flare, by the desktop, or
   only in the lead's digest is not settled here.
3. **Receipts append or replace.** A second receipt of the same kind at the same head replaces
   the first (friction 9, [R]); a fail is erased by a later pass. Not a boundary question, but the
   verifier inconsistency in run 1b is only visible in a PR comment today.
4. **Cross-machine mail.** Two machines share only the git remote; rows and receipts are mirrored
   to the PR and pulled back by `fleet sync` [F]. Whether mail is mirrored the same way, or a
   second machine's roles are simply unreachable, is undecided.
5. **Stage B.** [V] plans to fold the Org chain into gate's ledger. D2 and D9 are drawn so the
   authored record moves unchanged; whether `org tree` survives as a gate read or stays an `org`
   verb is that migration's call.
6. **A writer that was not the occupant.** D2 detects rather than prevents a session writing to
   a lead's chain from outside the role's directory: the record names the incarnation, and a
   later sweep can compare it against Fleet's session store. Whether the Org home should refuse
   such a write at append time is a question for after run 4 shows whether it ever happens.
7. **The seat's cost gate and its allow list** (friction 13, [R]) and the variadic
   `--allowedTools` (friction 15) are launch-shape defects outside this boundary; they belong to
   `cmd/fleet` and the run contract respectively, and are listed so they are not lost.
