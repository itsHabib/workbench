**Status**: draft
**Owner**: @itsHabib
**Date**: 2026-07-22
**Related**: dossier task `custody-derive-attenuation` (id: `tsk_01KY5C3ZENR2B7P3JZA51EPR6M`), [grant-materialized rooms TDD](spec.md) §4 D1/D2b, §6

# custody: parent-chained grants + derive verb — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `cmd/custody/internal/grant/grant.go`, `cmd/custody/main.go` | ~180 | 180 |
| Tests | `grant_test.go`, property tests | ~340 | 170 |
| **Total** | | | **~350** |

Band: **amazing** per repo PR sizing.

## Goal

Custody grants become chainable, attenuated by law: a `derive` verb mints a
child grant from a live parent, narrower on every axis, so a placed run can
hold exactly the authority its task needs and no more. Attenuation is enforced
in the signed envelope, not by caller discipline.

## Behavior / fix

In `cmd/custody/internal/grant`:

- `Grant` gains two fields, both joining the signing pre-image (extend `sign`
  and its doc-comment enumeration): `Parent string` (grant id this was derived
  from; empty = operator-minted root) and `BoundSource string` (transport
  source this grant is usable from; empty = unbound — usable on the localhost
  listener only; enforcement of the bind itself is P3, out of scope here).
- Bump `Version` to 2 and `tokenPrefix` to `cst2_`. Version-1 tokens refuse
  with the existing unsupported-scheme path; migration is re-mint (short TTLs).
- New `Derive` on `Store`: validates the parent token live, then mints a child
  refusing on three coded errors: `refused_attenuation_actions` (child actions
  not a subset of parent's), `refused_attenuation_ttl` (child expiry after
  parent expiry), `refused_chain_depth` (parent itself has a parent — depth is
  capped at 1). Child records persist beside grants in the existing layout.
- `Validate` independently rejects any presented chain whose parent record
  carries a non-empty `parent` — a depth-2 token assembled outside `Derive`
  (old binary, hand-built record) refuses at validation, not just at mint.
  Per-request validation stays pure crypto + local records: no live re-check
  of the parent's own signature chain on the hot path beyond loading its
  record for the depth/expiry fields already needed.

In `cmd/custody/main.go`: `custody derive -grant <cst2_parent-token>
-actions a[,b] -ttl <d> [-bound-source <ip>]` → prints the child token.
Refusals print the coded error + remedy naming the exact mint command.

## Acceptance

- `mint → derive → validate` round-trips; the child's `Covers` reflects only
  the derived subset.
- Each of the three attenuation refusals fires with its coded error and is
  branchable by callers.
- A depth-2 chain presented to `Validate` refuses even though `Derive` never
  produced it (constructed directly in the test).
- `cst1_` tokens refuse with the unsupported-scheme error.
- `bound_source` and `parent` are covered by the signature: mutating either in
  the persisted record invalidates the token.

## Test plan

Unit: `derive_refuses_action_superset`, `derive_refuses_ttl_past_parent`,
`derive_refuses_grandchild`, `validate_rejects_depth_two_chain`,
`sign_covers_parent_and_bound_source`. Property tests (extend the existing
suite): generated action-sets/TTLs — every successful derive satisfies
subset + expiry-cap + depth invariants; every violating input refuses.

## Non-goals

The tap listener and `refused_source_mismatch` enforcement (P3,
`custody-tap-listener`); the resolver; receipt schema; delegation depth > 1;
any gate changes.

**Model/effort:** opus/extra — signing pre-image change on a security envelope; correctness-critical.
