# Plane matrix on GCP, 2026-09-17

Run by the rooms lead session on one rented n2-standard-16 Spot box (16 vCPU, 62 GiB,
us-central1-a), Firecracker clones from one 512 MiB base, Redis 7.0.15 bound to the box's
private address. Box lived 30 minutes, about $0.12 of compute. `swarm` at 2db2742
(sha256 6164b188…); the plane code is byte-identical to the current head.

| run | rooms build | accepted / items | violations | wall |
|---|---|---|---|---|
| n2 | main | 618 / 618 | none | 67 s |
| n4 | main | 618 / 618 | none | 35 s |
| n6 | main | 618 / 618 | none | 25 s |
| n12 | main + density patches | 1238 / 1238 | none | 26 s |
| n16 | main + density patches | 1238 / 1238 | none | 21 s |
| n6 --faults | main | 618 / 618 | none | 181 s |
| n12 --faults | main + density patches | 1238 / 1238 | none | 181 s |
| n16 --faults | main + density patches | 1238 / 1238 | none | 182 s |

240 tasks for n ≤ 6, 480 for 12 and 16, two watchers. Fault runs kill one clone and restart
the store; their wall is the rooms `--max-wall` wait after the kill. n12 and n16 needed a
research-only rooms build (eight-room cap lifted). Every run passed the history checker:
accepted once, current lease, epochs grow, no claim after, no steal, replay faithful.

Each directory holds `summary.json`, `history.jsonl`, clone stdout and stderr and per-room
output. Redis data excluded.

Also verified on the same box, for running real agent sessions inside clones: a guest reaches
api.anthropic.com (HTTP 401 with no credential, so the path is open); a secret passed with
`rooms clone --secret NAME` lands at `/run/rooms/secrets.env` (0600, guest user `rooms`),
readable by a `--command` workload and not exported into its environment; the guest user is
uid 1000, which `claude --dangerously-skip-permissions` requires.
