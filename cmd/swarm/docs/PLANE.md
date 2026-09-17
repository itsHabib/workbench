# The plane: what a swarm shares when its peers share nothing else

`internal/plane` is the coordination store. It exists because the first substrate kept its
state in files under one git directory, and an independent review reproduced thirteen ways
that went wrong (see `../KNOWN-DEFECTS.md`), most of them some form of "read, then act".
Peers in Rooms microVM clones share no filesystem at all, share a frozen hostname and
address, and get killed and restored mid-task. The plane is built for that.

## One mechanism

Everything a swarm coordinates is an **item**: a task to build, a request to rule on, a seat
to occupy, a resource to hold, an event to deliver. An item is pending until exactly one
attempt commits it.

- **Claim** atomically checks the item is still pending and unowned (or its lease lapsed) and
  grants a lease under a fresh **epoch**. "Is it still pending?" is never read separately
  from the grant.
- **Commit** atomically checks the caller still holds the current epoch and nobody committed
  first, records the result, and creates any follow-on items in the same step: the children of
  a split, the event that tells someone it landed.
- Both are **idempotent by key**. A retry after a crash or a lost reply returns the original
  answer; it never acts twice.
- **Epochs only grow**, including across release. A token handed out once is never handed out
  again, so a paused peer that wakes up is fenced, not trusted.
- **Time is the store's.** No peer's clock decides whether a lease has lapsed.

Attempts are at-least-once; acceptance is exactly-once. The store cannot make a push or an
install or a model call happen once. The protocol is: work under a lease, publish an
immutable result, and let Commit decide whether it is the accepted one. Work done under a
lost lease is discarded, never merged.

## Backends, one contract

The transitions are written once, as pure functions of the state and a clock (`core.go`).

| backend | for | atomic by | durable by |
|---|---|---|---|
| `Mem` | tests, models, mutants | a mutex | nothing |
| `File` | many processes on one host | an OS advisory lock | history appended and synced, then state synced and renamed; history beyond the committed state is trimmed on load |
| `RESP` | peers that share no filesystem (Redis, Valkey) | one Lua script per step, on the server's clock | the server's append-only file; run it with `appendfsync always` |

`plane_test.go` runs the same contract over every backend. The RESP script mirrors `core.go`
line for line and is held to it by that suite and by the fault harness.

## The checker, and why it can be believed

`Check` replays the store's own history and reports every violation of:

| invariant | meaning |
|---|---|
| accepted-once | an item is committed at most once |
| current-lease | an accepted commit carries the item's newest epoch and its owner |
| epochs-grow | a granted epoch exceeds every epoch granted before it |
| no-claim-after | nothing is granted on an item after it is committed |
| no-steal | nothing is granted while another owner's lease is unexpired |
| replay-faithful | an idempotent replay repeats the original answer |

It shares no code with the transitions it judges. And it has been made to fail: `Broken`
switches off one safety check at a time, and `TestCheckerCatchesBrokenStores` plus the
`--mutant` runs of the harness require the checker to name the invariant each one breaks. A
fault that was never delivered is "not tested", never a pass.

## The fault harness

```sh
swarm plane fault --store file --seeds 10
swarm plane fault --store resp --seeds 10          # starts its own redis-server, and kills it
swarm plane fault --store file --mutant no-epoch   # must FAIL
```

Real processes, not goroutines. Per seeded run: 120 tasks, every seventh splitting into two
children inside its commit; 8 task workers contending for a pool of 5 seats they claim and
release around each task; 2 watchers racing to deliver the event each task emits. Faults:
workers die on purpose after a claim, before a commit and after a commit; a dozen SIGKILLs at
random; SIGSTOP past the lease then SIGCONT (the paused holder); and on RESP, the server
killed without warning and brought back on its data directory. A crashed peer returns as a
new incarnation. The run passes only if the checker finds nothing, every task and event ends
committed, the seat pool was never exceeded, and no lapsed lease waited past the bound for a
new owner.

Results: `../plane/RESULTS.md`.

## In Rooms

```sh
# on the rooms host, as the user that runs rooms
swarm plane rooms --snapshot SNAP --image IMG --toolstore TS --host-ip HOST_IP -n 6 --faults --out DIR
```

It starts the store on the host, serves its own binary to the guests over HTTP (a clone's
command line cannot carry it), and starts N clones whose command downloads it and runs the
same worker loop. Each peer mints its incarnation from `/proc/sys/kernel/random/uuid` after
restore, because nothing else tells clones apart. With `--faults` it kills one clone and the
store once a quarter of the tasks are done.

## What the plane is not

It is not yet what the `swarm` verbs run on. `ask`, `rule`, `take`, `admit`, `split` and the
inbox still use the single-host state in `internal/swarm`, with the review's defects fixed.
Moving them onto the plane is the next step, and it is what lets a swarm, with or without
leads, span machines: a lead is a seat with a higher tier and a loop, and who rules is a
routing policy over the same items.
