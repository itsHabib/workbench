# ship#142 — `driver land` verb

**Correct tier: T2** (sensitive)

## Signals that should fire
- `public-api` (T2) — adds a new exported driver verb consumers script against.
- `concurrency` (T2) — the land/merge path coordinates merge-tail ordering.
- confidence nudge — new surface, tests added (no nudge up).

## Why not lower
Not T0/T1: it changes the outward driver contract and touches merge sequencing; a broken land verb corrupts the merge tail. A peer glance (T1) isn't enough — wants the owner.

## Why not higher
Not T3: no migration/auth/money/irreversible surface. Reversible, no data destruction.

## Notes
Illustrative label for the format, reconstructed from memory of the ship land-verb work — replace with the verified diff during P0 corpus build.
