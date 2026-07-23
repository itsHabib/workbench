**Status**: draft
**Owner**: @itsHabib
**Date**: 2026-07-22
**Related**: dossier task `runway-authority-resolver` (id: `tsk_01KY5C4W89NFM3P9H8QXBBF9KG`), [grant-materialized rooms TDD](spec.md) §4 D2b/D4/D6, §5, §7, §9

# runway rooms adapter: custody secret-ref resolver + receipt assembly + e2e — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `cmd/runway/internal/backend/rooms/` (new resolver + receipt assembly; wiring in `rooms.go`/`lifecycle.go`), `cmd/runway/internal/controller/controller.go` (`resolveSecrets` routing) | ~340 | 340 |
| Tests | resolver/TTL/receipt unit tests + reconcile-path test | ~340 | 170 |
| **Total** | | | **~490** |

Band: **amazing/ideal** boundary per repo PR sizing — no split: the resolver,
receipt assembly, and their failure model are one composition (single flow
from ref to receipt); the e2e gate run validates them together.

## Goal

Turn a WorkSpec `custody:<key>/<actions>` secret ref into a materialized room:
resolve the live parent grant, derive a run-capped source-bound child, hand
ONLY the token to rooms for delivery, and assemble the `authority-receipt.v1`
line at collection. This is P3's load-bearing composition — the TDD §9
validation gate drives it end-to-end.

## Behavior / fix

Resolver lives in the rooms backend adapter (`cmd/runway/internal/backend/rooms`)
— the only component reading both a grant and a placement:

- **Controller routing (prerequisite):** `resolveSecrets` in
  `cmd/runway/internal/controller/controller.go` runs before `be.Start` and
  today rejects any ref that is not `env:` — a `custody:` ref admitted by
  `contracts/execution` would fail preparation before the adapter ever sees
  it. Extend it to pass `custody:` refs through to the backend adapter
  untouched (no value expansion at the controller); a backend without a
  custody resolver (e.g. local) refuses them with a coded unsupported error.
  `env:` behavior unchanged.
- **Resolve:** parse the ref grammar (already admitted by
  `contracts/execution`); find a live parent grant covering key+actions.
  None → refuse with `authority_unresolved` + the exact `custody grant`
  remedy (FR2), surfaced as a Runway admission/preparation failure with the
  remedy in diagnostics.
- **Derive (D4, D2b):** child TTL = min(parent expiry, deadline + grace +
  margin); stamp the room's tap source into the child via `-bound-source`
  (source-bound derive). One child per stream in v0 (no batch coalescing,
  §10.3).
- **Expand into the room (D6):** `CUSTODY_GRANT_<KEY>` (vsock staging) +
  `CUSTODY_BASE_<KEY>` (profile-shaped base URL). Non-custody refs flow
  through the raw path untouched.
- **Receipt at collection (§5, §8):** assemble `authority-receipt.v1` (v2
  shape from `contracts/authority`) from the derive record + Result artifacts
  (witness/changeset digests) + teardown status: `grants[]` one entry per
  resolved ref (multi-secret run = one line), `parent_actions` included,
  `evidence.artifacts[]` refs, `custody_log` = child-id filter +
  request_count + lines digest. Name the receipt in `Result.Artifacts`.
  Assembly is idempotent from durable inputs — re-running collection yields
  the same line.
- **Reconcile path (§7 F):** controller loss after room start → receipt still
  assembles with `teardown: unknown`.

## Acceptance — TDD §9 validation gate on one real driver task (rooms-host e2e)

- (a) zero-hit secret grep in the room,
- (b) one refused over-scope request visible in the custody log AND the
  receipt (nice-to-have if cheap: a sibling-room replay attempt exercising
  `refused_source_mismatch` — not gate-blocking),
- (c) one cold-readable receipt line answering what authority existed /
  delivered how / evidenced by what / torn down with what outcome.
- Refusal path: missing parent grant fails admission/preparation with
  `authority_unresolved` + remedy in diagnostics.

The e2e gate run requires the rooms-host and is the VALIDATION step —
operator-in-the-loop; unit suite must be green before it.

## Test plan

Unit: ref-grammar edge cases at the resolver seam; refusal + remedy text;
TTL-cap math table (parent-limited, deadline-limited, margin); receipt
idempotency (same durable inputs → byte-identical line); reconcile →
`teardown: unknown`. e2e: the §9 gate run itself on rooms-host.

## Non-goals

Console surfacing, driver-state ingestion of receipts (§10.2), batch
coalescing (§10.3). NOTE: the source-bound derive path (stamping the room tap
source via `-bound-source`, D2b) is IN scope — §10.1 resolved per-room pinning
into v0 through D2b; do not skip it. Only pinning mechanisms beyond D2b are
deferred.

**Model/effort:** opus/extra — the load-bearing composition; failure-model and TTL-cap correctness carry the design.
