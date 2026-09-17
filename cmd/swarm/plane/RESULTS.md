# Plane fault results, 2026-09-17, local

Binary at `2c83528`. macOS arm64, Redis 8.10 started and killed by the harness. Evidence:
`runs/2026-09-17-local/` (the full table, every run's report, and one full store history per
backend). Kill condition, fixed before the run: **one** duplicate acceptance, stale accepted
commit, grant after commit, stolen lease, rewound epoch, unfinished item, seat pool overrun,
or lapsed lease left past the bound fails the run.

Per seeded run: 120 tasks (34 more created by splits inside commits), one delivery event per
task, 8 task workers contending for 5 seats, 2 watchers racing for deliveries; workers die on
purpose at three crash points, 12 random SIGKILLs, 4 SIGSTOPs held past the lease, and on RESP
two unannounced kills of the server. Crashed peers return as new incarnations.

| store | runs | passed | accepted exactly once | violations | peer deaths per run | stale attempts refused | lease takeovers | worst recovery | seat peak / pool |
|---|---|---|---|---|---|---|---|---|---|
| file | 10 | 10 | 308 of 308, every run | 0 | 51 to 75 | 14 to 51 | 44 to 88 | 1,325 ms (bound 2,080) | 5 / 5 |
| RESP, server killed mid-run | 10 | 10 | 308 of 308, every run | 0 | 52 to 69 | 0 to 2 | 24 to 40 | 684 ms (bound 2,080) | 5 / 5 |

The checker was then run against four deliberately broken stores under the same faults. It
must fail them, and did:

| mutant | what is switched off | runs failed | invariant named |
|---|---|---|---|
| no-epoch | commit accepts any epoch | 5 of 5 | current-lease |
| no-done | commit and claim ignore "already committed" | 5 of 5 | accepted-once, no-claim-after |
| no-pending | claim grants committed items | 5 of 5 | no-claim-after |
| epoch-reuse | release rewinds the epoch | 5 of 5 | epochs-grow |

## What getting here found

- The first matrix had no release in the workload, so the epoch-reuse mutant was caught once
  in six. A harness that never exercises an operation cannot vouch for it. The seat pool was
  added for that, and it put admission under the same faults.
- The file store first rewrote its whole state, history included, on every step, and missed
  the recovery bound once by 10 ms as the history grew. History is now an append-only synced
  log; state is the commit point; history beyond the committed sequence is trimmed on load
  (`TestFileCrashBetweenHistoryAndState`).
- Redis's Lua JSON encoder writes microsecond timestamps as lossy floats. Times cross the wire
  as exact strings.
- Colima did not forward newly published container ports, so the store under test is a native
  `redis-server` the harness owns. That is also what lets it kill the store.

## What this does not show

One host, one OS, simulated work measured in milliseconds, no network partition, no clock
skew between a store and its clients beyond what the server's own clock removes. The Rooms run
(`swarm plane rooms`) is next: the same loop with every peer in its own microVM clone. And the
`swarm` verbs do not run on the plane yet; this proves the contract they will move onto.

# In Rooms, first matrix, 2026-09-17 (binary 1dc7624)

Run by the rooms lead session on its Lima VZ host (aarch64, 6 vCPU, 4 GiB; 512 MiB
single-vCPU Firecracker clones; redis-server 7.0.15 on the host's address). 240 tasks per run.
Evidence: `runs/2026-09-17-rooms-1dc7624/`.

| run | passed | accepted / items | unfinished | violations | wall |
|---|---|---|---|---|---|
| 2 clones | no | 308 / 616 | 308 | 0 | 75 s |
| 4 clones | no | 308 / 616 | 308 | 0 | 54 s |
| 6 clones | yes | 616 / 616 | 0 | 0 | 100 s |
| 2 clones, clone and store killed | no | 60 / 318 | 258 | 0 | 181 s |
| 4 clones, clone and store killed | no | 308 / 616 | 308 | 0 | 183 s |
| 6 clones, clone and store killed | yes | 616 / 616 | 0 | 0 | 185 s |

**Safety held in every run; liveness failed in four, and the cause was the driver, not the
store.** Each clone chose "builder" or "watcher" from a random digit of its uuid. In three runs
no clone rolled watcher, so every task was accepted and none of the 308 deliveries were. In the
two-clone fault run the only builder was the clone that got killed. The six-clone fault run
passed with a clone killed and Redis restarted a quarter of the way through. Fault runs take
the full wall because `rooms clone --command` waits out `--max-wall` after a member is killed
(a rooms follow-up, not this driver).

Fixed at `5989d33`: roles are claimed from the store at start-up, the one thing identical
clones do not share; a peer with nothing of its own kind to do helps with the other, so no
role distribution and no single death can strand a kind of work; workers log a start and an
end line (peer.log was empty in every clone of this matrix). Rerun pending.
