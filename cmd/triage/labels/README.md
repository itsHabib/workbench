# labels/ — the oracle

Hand-scored PRs that define what "correct" means, written **before** the classifier is trusted (test-first). The classifier passes iff it reproduces these labels with **zero false-negatives at tier ≥ T2** (spec §11).

One file per labeled PR: `<repo>-<num>.md`. Draw from real pers/ git history across ship / dossier / rooms, plus synthetic edge-cases that pin the tricky boundaries. Target 25-40 for P0.

Each label records the *correct* tier and which signals *should* fire — so a classifier miss is debuggable signal-by-signal, not just "wrong."

`mismatches.jsonl` (created at runtime) is the append-only calibration log — separate from these ground-truth labels.

## Edge cases the corpus MUST contain

These are the boundaries that separate a real classifier from a line-counter:

- **3-line auth change** → T3. (Tiny diff, maximal risk. Kills "size = risk.")
- **2,000-line generated-client regeneration, tests pass** → T0. (Huge diff, no risk.)
- **Renames a public exported symbol** → T2. (Small, but breaks consumers.)
- **Adds an `else` that loosens a permission check** → T3 semantic. (No sensitive *path* touched by name.)
- **Deletes a test that guarded a payment path** → T2+. (Removal, not addition — the diff *shrinks* risk-guarding code.)
- **Migration file that is purely additive + reversible** vs **a destructive one** → both T3 by surface floor, but the label notes why the router still wants a human on the "safe" one.

Added after the v1 adversarial pass (these were the fail-open holes — the corpus MUST pin them):

- **Edits `RUBRIC.md` / the skill / `CODEOWNERS` / `labels/`** → T3 control-plane, even though it "looks like docs." (The v1 killer.)
- **Lockfile version bump** (`go.sum` / `package-lock.json`) → T2, NOT "generated → T0." Registry/override edit (`.npmrc`, `[patch]`) → T3.
- **Removes an `authorize()` / rate-limit call from a handler in a non-`auth/` path** → T3 by *content* signal (no path keyword fires).
- **Config data flips `require_2fa: false` or `allowed_origins: ["*"]`** → T2 policy-as-data (no path keyword fires).
- **Split-PR pair:** PR-A adds an unwired public route (T1), PR-B wires it (T0) → the corpus notes neither trips composite risk; documents the cross-PR gap the team phase must close.

Independence rule (spec §11): the *held-out* portion of this corpus must be labeled by a labeler who has NOT read `RUBRIC.md`, from the neutral prompt "does this PR need a human, and how much?" — otherwise the oracle is circular.
