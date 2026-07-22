**Status**: draft
**Owner**: @itsHabib
**Date**: 2026-07-22
**Related**: dossier task `authority-receipt-contract` (id: `tsk_01KY5C46ZMB7Z7S59NP5RVVX2R`), [grant-materialized rooms TDD](spec.md) §5, FR1

# contracts/authority: room-authority receipt + custody secret-ref grammar — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `contracts/authority/authority.go`, `contracts/authority/schema/authority-receipt-v1.json`, `contracts/execution/validate.go`, `contracts/execution/schema/work-spec-v0.1.0.json` | ~200 | 200 |
| Tests + fixtures | conformance fixtures, validate tests | ~220 | 110 |
| **Total** | | | **~310** |

Band: **amazing** per repo PR sizing.

## Goal

The cross-repo contract for "what authority a placed run held, and what
evidence covers what it did with it" exists as a leaf package, so P3's
placement wiring and rooms' artifacts both build against one shape. Plus the
one-line unblock review found: the execution contract's secret-ref grammar
must admit `custody:` refs, which today refuse at admission.

## Behavior / fix

**New leaf package `contracts/authority`** mirroring the TDD §5 receipt
exactly (v2 shape):

- `authority-receipt.v1`: one JSONL line per placed run that carried at least
  one `custody:` ref. Top level: `schema_version`, `run_id`, `allocation_id`,
  `grants[]`, `evidence`, `teardown`.
- `grants[]` — one entry per resolved ref: `secret_name`, `key`, `parent_id`,
  `parent_digest`, `parent_actions` (attenuation visible in-receipt),
  `child_id`, `child_digest`, `actions`, `bound_source`, `minted_at`,
  `expiry`, `delivery {channel, delivered_at, one_shot}`.
- `evidence.artifacts[]` — `{type, sha256}` with an open type vocabulary
  (`witness_pcap`, `witness_json`, `changeset`, …); additive within the major
  version. `evidence.custody_log[]` — `{child_id, request_count,
  lines_sha256}`: the child grant id is the selector over custody's
  interleaved JSONL; count + digest pin what it returned at assembly time.
- `teardown` — `{status, at}`; `status` is the closed enum `destroyed |
  failed | unknown`. All timestamps RFC 3339 UTC.
- Reader refuses unknown `schema_version` (mirror `contracts/execution`'s
  `checkVersion` posture and error vocabulary).

**Grammar change in `contracts/execution`** (additive revision): `Secret.Ref`
validation currently pins `^env:[A-Za-z_][A-Za-z0-9_]*$` in `validate.go` and
the work-spec JSON schema. Extend both to also accept
`custody:<key>/<action>[,<action>...]` where key and action match custody's
name alphabet (lowercase alnum + `-`, non-empty; actions non-empty,
comma-separated, no duplicates). `env:` behavior is untouched; every other
scheme still refuses.

## Acceptance

- `contracts/authority` passes the hygiene leaf-check (imports nothing else
  in the module).
- A cold reader traversing one valid fixture can answer: what authority
  existed (incl. that it was attenuated — parent vs child actions), when,
  delivered how, evidenced by what, torn down with what outcome — with no
  external store.
- Multi-secret fixture: two `grants[]` entries, one line.
- `custody:tracker/read,comment` admits; `custody:` / `custody:key/` /
  duplicate actions / `vault:x` refuse; `env:NAME` unchanged.

## Test plan

Fixture round-trips (valid single-grant, valid multi-grant, each invalid
class: bad enum, missing digest, unknown version); grammar table test over
accept/refuse cases in `contracts/execution`; hygiene job green.

## Non-goals

Receipt assembly (P3), hash-chaining (deferred per TDD §4 D5), driver-state
ingestion (TDD §10.2), any custody or runway code.

**Model/effort:** sonnet/extra — type-enforced contract work on the proven contracts/execution pattern.
