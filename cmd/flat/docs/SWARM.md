# How to create a swarm

A swarm is a fleet with no ranks: many seats, one substrate, an index that routes a question
to whoever holds the context, and wakes so a seat that finished still answers. This is the
recipe `flat` supports today, in the order the work happens.

## 1. One owner, one goal, on paper

Someone owns the goal. That is a person or a seat, not a layer. The owner writes two things:

- `briefs/BRIEF.md`: the goal, what "done" looks like, and which decisions are the owner's.
  Anything product-shaped that is not written here will come back as an operator request.
- `briefs/tasks.json` and one card per task under `briefs/tasks/`: branch, title, files it will
  touch, the resource it needs, what counts as done.

The decomposition is a mind's job and it runs at the start, and again when the goal changes.
It does not tick. `flat poc init --tasks N --packages P` is a stand-in that generates both from
a spec; a planner seat that writes them from a prose goal is the same role with a prompt.

## 2. Bind the substrate to the repository

```sh
flat init --operator <owner> --lead <any name that may rule above peers, or none>
flat install-hook
flat watch --fetch --wake --wake-model <model> --verify "go test ./..." --disk-min 10G
```

One watcher per machine. It derives the board, nudges, wakes, verifies landed heads, and
writes `watch/digest.md`. Nobody reports to it.

## 3. Admit seats, do not spawn them

A seat starts when `flat admit --seats N --disk-min` says so. Seats are branches in their own
worktrees; the branch is the address. Give every seat the rules block from the README and its
card. In the poc the runner does this; on a desktop, the owner or a script does.

## 4. Let the substrate carry the coordination

- Contention: `flat check` before an edit, at package scope. Contended and unruled means ask.
- Rulings: `flat ask` routes by affinity to the seats with context; they rule with
  `flat rule --order`; ties resolve by tier. The ledger is the "decided" section.
- Intent: `--needs operator` for anything the brief did not settle. Never guess.
- Liveness: a silent seat is nudged; a seat between turns is woken; a pin violation is flagged.
- Red: a landing whose tests fail at its pinned head gets a receipt and a nudge.

## 5. Re-scope from the inside

Work is not always known in advance. A seat that discovers its task is really several does
not ask a lead; it splits: new cards and task rows, a ruling that records the split and who
owns the parent, and admission picks the children up. Ownership of the parent stays with the
seat that split it until the children land; its RESULT.json names them as claims.

## 6. Consolidate by the ledger's order

`flat order` prints the merge order from the order rulings and start times. A consolidator
seat merges in that order, tests after each merge, records what it could not reconcile, and
lands a theme branch like any other seat. That is the theme map the lead used to write by hand.

## 7. Add capacity by wait, not by rank

`flat load` shows, per address, how long requests sit before someone claims them. When the
operator's row grows, the brief is missing decisions: write them down. When a peer's row grows,
the fleet is short of seats with that context: add a peer or let wakes carry it. Nothing here
calls for a session above the seats.

## What the owner still does

Reads the digest. Answers the requests that need intent. Ratifies the theme when the
consolidator lands it. Rewrites the brief when the goal moves. That is the whole job.
