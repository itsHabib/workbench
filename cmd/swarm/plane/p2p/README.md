# Peers that share only a store

`SWARM_STORE=resp:HOST:PORT/PREFIX` moves requests, claims, rulings, escalations, notes,
resource leases and the tier map onto the plane. Each seat keeps its own checkout and its own
local state directory; nothing but the store (and the git remote) is shared. This is the
arrangement for seats on different machines or in Rooms clones.

Run on 2026-09-17 with three separate clones of one repository (`machineA`, `machineB`,
`machineC`), each with its own state directory, against a native `redis-server`
(`appendfsync always`). Every line below is a separate `swarm` process.

| what | result |
|---|---|
| A asks who lands first on `pkg/x`; B, in another clone, lists requests | B sees it |
| A blocks in `swarm wait`; B rules with `--order` | A's wait returns B's ruling and evidence |
| A reads its inbox | the ruling is there, delivered once |
| A asks a product question; B escalates it to the operator | the question moves up in one step; A and the operator are both told |
| B tries to rule the escalated question | refused `tier_too_low`: the tier map lives on the store, so every machine agrees who outranks whom |
| the operator, in a third clone, reads its inbox and rules | A, still waiting on the id it originally asked, gets the operator's answer |
| A takes `fixture-db`; B tries to | B refused `held_by_other`, naming A and the expiry |
| Redis is killed with SIGKILL while A waits, restarted three seconds later, then B rules | B's ruling lands; A's wait returns it |

The same behavior is covered in-process, under the race detector, by
`internal/swarm/plane_mode_test.go`: fencing of expired and superseded claims, a retry that
returns the original ruling, eight peers racing one request (one wins), and two same-tier
rulings racing on overlapping scope (one lands; the ledger lease serializes the tiebreak).

Not on the store yet: the git-derived board (it needs a shared remote the seats push to, which
they already have), admission reservations, `split`'s task rows, session records and wakes. A
seat in a clone can ask, answer, escalate, hear back and hold a resource; it cannot yet be
woken by a watcher on another machine.
