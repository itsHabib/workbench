# flat: design

## The claim

A management hierarchy over a fleet of coding agents (lead, sub-leads, a director when the
lead saturates) exists because of two gaps in the substrate, not because coordination needs
ranks. Messages are point-to-point and queue behind the recipient's turn, so fan-in needed a
human-shaped aggregator. Context windows are small, so state had to live somewhere, and a
lead's memory was the somewhere. Close both gaps and what remains of a lead is two things:
writing a builder's handoff, and a ruling that needs intent nobody wrote down.

The asymmetry that makes a tree unnecessary: a person cannot rebuild the whole picture on
demand, so span of control is bounded. An agent rebuilds it from git in seconds. Once "who
knows what" stops mattering, what remains is "who decides what", and that is a lattice of
scopes (paths, resources) under a total order of tiers, not a chain of command.

## The pieces

**Board.** Every branch that differs from base, with its tip time, the files it changed, and
its state. State is derived: `working`, `silent` (tip older than the idle line), `landed`
(RESULT.json's `head_sha` is on the branch and the tip differs from it by RESULT.json only),
`pin_violation` (commits after the pin), `pin_invalid` (the pin is not on the branch),
`blocked` (RESULT.json names a request). Contention is every file touched by two or more
branches, with whether an effective ruling covers it. Nobody writes to the board.

**Ledger.** Requests are files; rulings are lines in a hash-chained `decisions.jsonl`. A
request has a scope (paths, directories, `resource:<name>`), a question, and the tier it
needs. A ruling has a scope, text, evidence, the ruler, the ruler's tier, and what it
supersedes. Effective rulings are those no later ruling names. `flat check <path>` and
`flat decisions --scope` are the "decided" section of the old board, as a query.

**Tiers, not tree.** peer < lead < operator, from `tiers.json`; unnamed is peer. A request
needing tier T is ruled by anyone at T or above. Two rulings on overlapping scope resolve by
tier; equal tiers must name what they supersede. Escalation raises the tier a request needs.
This is enough to break every tie the runbook's tree broke, with no middle level.

**Claims.** A ruling is a claim plus a decision. A claim has a holder, an epoch and an
expiry. A rival's live claim refuses; an expired claim changes hands with a new epoch; a
ruling under an old epoch is fenced. The same shape as fleet's leases, applied to decisions.

**Routing.** `flat ask` fans out to seats with affinity on the scope: branches whose changed
files overlap it, seats that ruled on it before, holders of a resource in it. Git and the
ledger are the index; nobody registers. The watcher widens to every working seat if nobody
claims within its threshold. Lead- and operator-tier requests route to the names the tiers
map grants.

**Resources.** `flat take` and `flat drop` lease an exclusive thing to a seat with an epoch
and expiry. No liveness probe: the machine a resource drives may outlive its holder.

**Admission.** Seats, a disk floor, and a resource the new builder needs. The runbook's
fleet ran with no gate and filled a disk with five levels of management in place.

**Watcher.** The lead's loop as a process: derive the board, compute standing alerts by key
(silent builder, pin violation, unruled overlap, unclaimed request, stale claim, expired
lease, low disk), raise each once, renudge on a cadence, clear when the condition goes, and
write the digest, the table, the phone line and the JSONL. Alerts nudge seats.

**Inbox, hook, wake.** A nudge is a file in `inbox/<seat>/`. The hook, installed in the
project's settings, injects a seat's undelivered notes into its next tool call and records
the session id and turn boundaries. A seat between turns or ended is woken: the watcher
resumes its recorded session with the notes as the prompt, so the seat that changed a file
answers questions about it with the context it built, after it finished. A seat with no
recorded Stop is presumed mid-turn and only hears through the hook.

## What stays a mind

Handoff authoring: turning intent into a builder's card. Rulings that need intent not in the
brief. Both go to whoever holds the evidence, by tier, and the ledger keeps the answer so it
is never asked twice. The proof of concept measures whether the fleet routes those two things
correctly without a session in the middle.

## Non-goals

No merge authority: `landed` is a claim checked against git, and merging is gate's job. No
verification of substantive claims in RESULT.json. No transport beyond files: a network
fleet would put the state directory on a shared filesystem or replace `State` with a store;
the verbs would not change. No liveness probes for resources.

## Portability

Stdlib and `x/sys`. Atomic writes by rename, locks by exclusive create, LF everywhere, paths
through `filepath`. Disk free by `statfs` or `GetDiskFreeSpaceEx`. The hook command is the
binary's path as `install-hook` writes it, `flat.exe hook` on Windows.
