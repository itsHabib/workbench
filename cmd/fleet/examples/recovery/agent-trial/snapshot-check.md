# Frozen snapshot validation

Validation passed for `snapshot.json`. Source SHA256: `2f156c4b94cb33e0cc17d6a8d9c30cec13c1e1946b61000dc7ee727c105c2207`.
The bytes matched the committed snapshot at `d9e5305fa4ba6b35281efd8d14e0fa16e6e135a9` before this validation commit. The frozen observation identifies `itsHabib/workbench` and records `2026-09-13T23:22:28.277805Z` (`observed_at: 1789341748.277805`).

A Python standard-library check parsed the JSON, checked the exact top-level and PR field sets and their types, required five unique PR numbers (334, 337, 338, 339, 340), checked 40-character hexadecimal heads and matching repository/PR URLs, and validated every check record's string fields, timestamps, status and conclusion consistency. All 26 records are `CheckRun` records. Counts are 21 `SUCCESS`, two `SKIPPED`, one `NEUTRAL`, and two `IN_PROGRESS` with empty conclusions.

| PR | Exact head | Draft | Merge state reported | Check observations |
|---|---|---|---|---|
| 334 | `d9ca988176bd47beb6afc33a70b31092b4302e0e` | false | UNKNOWN | Five successful checks |
| 337 | `6aa2c9430f221e3895b16da4683c199e6e76f284` | true | UNKNOWN | Six successful; Claude skipped |
| 338 | `3836bd742421f5c8d03665a72f974fadc4414dc0` | true | UNKNOWN | Three successful checks |
| 339 | `f3ddb66a258f1954e850659674b1809f8c6b0e2c` | true | UNSTABLE | Hygiene successful; check and fuzz in progress |
| 340 | `d8c66cfa2a38105a4ee7f82d933b44cee79fbc47` | true | CLEAN | Six successful; Claude skipped; Cursor Bugbot neutral |

Every `reviewDecision` is an empty string. No failure conclusion appears in this snapshot; that does not turn the two unfinished checks into successes. Their `completedAt` values are the `0001-01-01T00:00:00Z` placeholder, not completion evidence.

This validates the supplied frozen data's shape and internal consistency. It does not independently authenticate its GitHub origin, refresh current heads or checks, identify all required checks, inspect diffs or review findings, establish reviewer approval, or prove behavioral correctness. The snapshot contains no grant or merge authorization. Draft state, `CLEAN`, successful checks, skipped reviewer jobs, neutral checks and successful signal jobs must retain their distinct meanings. Rendering and independently checking the queue, followed by the per-PR human-assessment recommendations in `ASSESSMENT.md`, remain unfinished.
