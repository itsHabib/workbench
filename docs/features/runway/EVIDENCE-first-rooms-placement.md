# Evidence — first real Runway → Rooms placement

**Date:** 2026-07-20 · **Host:** rooms-host (Ubuntu, Hyper-V; real Firecracker + KVM)
**Adapter:** `cmd/runway/internal/backend/rooms/` (Phase 2, merged in #68)
**Run:** `run_8a454a187b1f89666ff1b9aae19e9678` · **Room:** `01kxzt72kwyct7xgq4r0r7cv18`

## What ran

A minimal `WorkSpec` (agent-cursor profile, one haiku task) was placed
end-to-end through `runway run` onto a live microVM — the first real `WorkSpec`
to traverse Runway onto Rooms. The controller compiled the placed `Request`,
the rooms adapter shelled out to the local `rooms` binary
(`sudo -E rooms run --runner cursor --lifecycle …`), a real Firecracker room
booted, and a terminal receipt (`result.json`, schema `0.1.0`) came back.

- **Request:** `Placement{backend: "rooms", profile: "agent-cursor"}`,
  `Workspace{git, github.com/itsHabib/rooms, <main sha>}`, one `task` input
  (sha-sealed), `Secret{CURSOR_API_KEY, env:CURSOR_API_KEY}`,
  `Policy{deadline 300s, grace 2s}`.
- **Runway env:** `RUNWAY_ROOMS_IMAGE=~/rooms/images/agent-alpine-cursor.ext4`,
  `RUNWAY_ROOMS_MODEL=composer-2.5`, `RUNWAY_ROOMS_BIN=/usr/local/bin/rooms`.
- **Receipt:** `status: failed`, `reason_code: workload_failed`,
  `terminal_phase: workload`, `workload_exit_code: 2`,
  `allocation_id: 01kxzt72kwyct7xgq4r0r7cv18`, `image_sha256: 48f03a83…`,
  `enforced: {cpu 1, mem 256MiB, network egress, rootfs readonly_overlay,
  secret_transport ssh_sendenv}`.

**Why the workload failed (and why that's fine for this gate):** the placement
ran with a deliberately fake `CURSOR_API_KEY` (`dummy-not-a-real-key-…`) so no
real secret entered the run. The cursor runner failed auth (exit 2) — but the
point of this gate is **lifecycle truth through the frozen schema**, and the
lifecycle is fully exercised regardless of workload outcome. Re-running
`~/runway-rooms-placement/place-rooms.sh` with a real key (from
`~/.rooms-creds.env`) yields a green haiku on the same path.

## What reconciled

Runway's canonical `RunEvents` (its own `events.ndjson`) line up 1:1 with the
room's own `rooms --lifecycle` NDJSON that the adapter drove — the adapter
translates rooms-native events into canonical phases, and the two independent
records agree on every transition:

| Runway `RunEvent` (canonical) | Phase | Room-native lifecycle (`rooms --lifecycle`) |
| --- | --- | --- |
| `run_accepted` | admission | — |
| `inputs_materialized` | preparation | — |
| `placement_profile_resolved` | startup | — |
| `placement_allocated` | startup | `slot_allocated` (slot 1, tap-fc1) |
| `vmm_started` | startup | `vmm_started` |
| `guest_ready` | startup | `guest_ready` |
| `workload_ready` | startup | `ssh_ready` |
| `workload_started` | workload | `workload_started` |
| `workload_exited` | workload | `workload_exited` |
| `cleanup_completed` | cleanup | `collection_started` → `collection_done` → `cleanup_done` |
| `run_terminal` | terminal | — |

The boot/ready distinction the lifecycle surface was built for holds across the
boundary: Runway's `vmm_started` → `guest_ready` → `workload_ready` maps onto
rooms' `vmm_started` → `guest_ready` → `ssh_ready`, so a slow boot is never
conflated with a ready workload channel. The pre-boot Runway events
(`run_accepted`, `inputs_materialized`, `placement_profile_resolved`) and the
`run_terminal` bookend have no rooms-native counterpart by design — they are
controller-side, above the substrate. The rooms-side collection events
(`collection_started`/`done`) fold into Runway's single `cleanup_completed`.

**Zero leaks after:** `pgrep -a firecracker` empty, `rooms ls` empty.

## Reproduce

Staged on the rooms-host under `~/runway-rooms-placement/`:
`runway` (linux/amd64 build of `cmd/runway`), `bundle/` (task.md + work.json),
`request.json`, and `place-rooms.sh`. With `~/.rooms-creds.env` present:

```
sh ~/runway-rooms-placement/place-rooms.sh
```

## Friction found

None in the adapter — it drove the room cleanly and the receipt was faithful.
One contract-conformance note worth stating: `Workspace.Revision` must be an
immutable 40-hex commit; a symbolic `main` is rejected at admission
(`workspace.revision "main" is not an immutable full 40-hex commit`). Correct
by design (immutable-workspace law), but placement tooling must resolve refs to
SHAs before building the `Request`. No bug; the layers met only at the schema,
as intended.
