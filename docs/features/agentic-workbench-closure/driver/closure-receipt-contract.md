**Status:** draft
**Owner:** @itsHabib
**Date:** 2026-07-28
**Related:** Dossier task `closure-receipt-contract` (`tsk_01KYMQGTT50C302GZA9Y3YVK93`)

# Minimal reconstructable closure receipt contract

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Driver-state contract | schema, Go types, reducer/render | ~250 | 250 |
| Tests and vectors | examples, model/properties, compatibility | ~300 | 150 |
| Docs | closure receipt description | ~70 | 35 |
| **Total** | | | **~435** |

Band: **stretch but PR-sized**.

## Goal

Extend the existing driver-state vocabulary—without a new binary or store—so a
Codex review/address/Gate/land loop is reconstructable from typed artifacts.

## Behavior

- Reuse existing run, stream, attempt, review-cycle, PR, and merge facts.
- Add only missing receipt facts: seat/harness, model/provider/effort, native
  producer id, catalog revision, review artifact id/digest/head, linked Ship and
  Gate refs, and typed interventions.
- An intervention records `time`, `kind`, `reason_code`, `actor`, and the
  judgment question ref when applicable.
- Incomplete or contradictory joins remain visibly incomplete; they never render
  as a completed closure.
- Preserve compatibility with existing ledgers.

## Acceptance

- One complete sequence validates, reduces, and renders with every required ref.
- Malformed identifiers, mismatched heads, ambiguous intervention
  classification, and duplicate terminal closure refuse or remain incomplete.
- Old conformance fixtures stay readable.

## Test plan

- Named happy-path and refusal examples.
- Generated/model properties for exact-head/ref joins and at-most-one terminal
  closure.
- Full Workbench format, vet, lint, unit, race/hygiene-equivalent checks.

## Non-goals

Ship emission, analytics UI, another state store, or Claude validation.
