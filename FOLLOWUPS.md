# workbench — follow-ups

Tracked in-repo per portfolio convention (status doc, not issues).

## `local` CLI name shadows the bash/zsh builtin

At a top-level bash/zsh prompt, `local` resolves to the shell builtin before
`$PATH`, so a bare invocation fails with "can only be used in a function". The
README quick-start uses `env local` to sidestep it. If real adoption friction
shows up (users tripping on it past the quick-start), consider a distinct
binary name — operator's call; renaming a CLI is a breaking change to every
skill that shells to it.

## Lazy-migration queue (graduate in when next touched)

New planes are born here; existing tools migrate in when next in hand, not as a
sweep. Order is by convenience, not priority. Each is handed to that tool's
owner — not this repo's work to force.

- **gate** — the verdict type's behavioral source of truth. When next touched,
  graduate it in and have it *import* `contracts` for the shared type, so the
  conformance test guards drift from the inside. Until then `contracts` mirrors
  gate's `internal/verify` and is conformance-tested against the schema.
- **triage** — migrated in 2026-07-16 as `cmd/triage` (the fourth tenant; two
  binaries, `triage-floor`/`triage-advisory`, sharing `cmd/triage/internal/`).
  `contracts` adoption deliberately NOT done with the move: inspection showed
  triage's verdict (floor/escalate/final/route) is its own domain shape, not a
  mirror of the merge verdict — there is no hand-parsed copy to drop. Adoption
  is owed together with the parked schema-alignment work (gate project,
  `align-triage-verdict-schema`), a behavior change the byte-identical
  migration must not smuggle in.
- ~~**tracelens** — adopt `contracts`; drop its hand-parsed verdict copy.~~
  Migrated in 2026-07-16 as `cmd/tracelens` (the third tenant); imports
  `contracts` for the verdict type, local mirror deleted, emitted JSON pinned
  byte-identical by a golden test.
- **local** — migrated in 2026-07-09 (the second tenant; going public touched
  it). `contracts` adoption is owed only if/when it reads verdicts — nothing in
  it does today.
- **huddle, sense** — graduate in when next touched.

## tracelens migration — deferred findings (2026-07-16, PR #48 review)

Both surfaced by the move review; both are real, and both are deliberately
not the move's to fix (its contract is byte-identical output):

- **controlroom's tracelens adapter cannot accept a real verdict.**
  `cmd/controlroom/internal/adapters/tracelens/adapter.go` unmarshals a
  `model.Diagnosis` (requiring `run_id`) that the `tracelens ship -json`
  binary has never emitted, and its `runner.Run` treats a block's exit 1 as
  unavailable, discarding stdout. The fix is controlroom-side: parse the
  emitted `contracts`-shaped verdict and accept exit-1-with-stdout as a
  successful (blocking) analysis. tracelens's binary name + exit codes are
  the seam and stay fixed.
- **`bestTandem` may skip a loop start after a partial periodic match**
  (e.g. `A,B,A,X,A,X,A,X` at period 2: the failed scan from 0 jumps past
  the real `A,X`-run start at 2). Changing the scan changes detector
  behavior, so it is owed to tracelens's own iteration with a corpus case
  that pins the improvement — not to a relocation diff.
- **A Claude stream truncated right after an `assistant` `tool_use` event
  decodes as "unrecognized ship event dialect"** (exit 2) instead of an
  analyzable aborted run — the dialect markers only key on `user`/`result`
  events. Same rule as above: a decoder-behavior change, owed to
  tracelens's own iteration with a truncated-at-tool_use corpus case.

## flare migration — choices made

- **Plain copy, not `git subtree`.** flare's layout restructured on the way in
  (its `internal/` moved under `cmd/flare/internal/`, and every import path
  changed from `itsHabib/flare` to `itsHabib/workbench/cmd/flare/internal`), so
  every file was edited regardless — subtree's history-preservation bought
  little against a nested-prefix fight for a 2-commit tree. flare's history is
  preserved in its standalone repo (`pers/flare`), kept as an archive.
- **flare's own follow-ups** live at `cmd/flare/docs/FOLLOWUPS.md`; its
  integration asks to the ship/gate owners are unchanged. The envelope-schema
  ask there is now largely paid by `contracts`.

## local migration — choices made (2026-07-09)

- **Plain copy again** (the flare precedent): the import path changed everywhere
  anyway (`itsHabib/local` → `itsHabib/workbench/local`); history stays in the
  standalone `pers/local` repo, kept as an archive.
- **`local/` is a top-level mechanism package**, not a tool under `cmd/` — see
  the charter's shared-mechanism amendment; CI leaf-checks it alongside
  `contracts`.
- **`cmd/demo` did not migrate** — folded into `local/example_test.go`.
- **`cmd/eval/ci-lines.jsonl` scanned line-by-line** before entering a
  to-be-public repo: 10 CI log lines, no tokens, no creds, no employer refs.
- Consumers on the `replace github.com/itsHabib/local` directive
  (local-poc/reviewer-triage, local-poc/local) repointed at the workbench
  module.

## Deferred: split `contracts` into its own module

See DESIGN.md. Trigger: an outside-the-module Go consumer (a new Go repo that
imports the types, or publishing `contracts` as OSS). **Not** triggered by
in-repo tools importing it, nor by ship/dossier reading the JSON schema.

## CI — per-tool path-filtered jobs

Today CI runs module-wide (one module, ~2 packages; a shared-`contracts` change
must retest every consumer, so module-wide is both faster and safer than
path-filtering that could green a tool whose contract shifted under it). Split
into per-tool path-filtered jobs when tool count or test time makes module-wide
retest wasteful.

## @claude reviewer

`claude.yml` is committed but @claude is **not** requested until the operator
sets the `CLAUDE_CODE_OAUTH_TOKEN` repo secret. Once set, @claude joins the
reviewer set (@codex, @cursor) on the next PR.
