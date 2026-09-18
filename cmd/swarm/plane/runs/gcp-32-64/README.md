# Plane matrix at 32 and 64 clones, 2026-09-18

Run by the rooms lead on a rented n2-standard-16 Spot box, 512 MiB single-vCPU Firecracker
clones, Redis 7, 960 tasks, two watchers. swarm at 17e24fd, rooms main ce64f16 plus the
density patches. Same arguments as the first matrix (`../gcp/`).

| run | accepted / items | violations | wall |
|---|---|---|---|
| n32 | 2470 / 2470 | none | 26 s |
| n64 | 2470 / 2470 | none | 29 s |
| n32 --faults | 2470 / 2470 | none | 362 s |
| n64 --faults | 2470 / 2470 | none | 363 s |

960 tasks in 26 s at 32 clones and 29 s at 64, against 480 in 21 s at 16 the day before: the
host's 16 cores were the limit at 64 guests, not the store. Nothing in the claim path grew.
Fault walls are the six-minute `--wall` rooms waits out after the kill. Host memory stayed under
10 GiB with 64 live clones. Every run passed the history checker.
