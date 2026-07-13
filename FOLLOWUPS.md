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

Scope decision (operator, 2026-07-13): the queue is **gate, triage, tracelens**
— the actual workbench work. huddle is out. sense is a lean-no; revisit only if
a concrete pull appears (e.g. a ballot reducer needing the verdict contract).

- **gate** — the verdict type's behavioral source of truth. When next touched,
  graduate it in and have it *import* `contracts` for the shared type, so the
  conformance test guards drift from the inside. Until then `contracts` mirrors
  gate's `internal/verify` and is conformance-tested against the schema.
- **triage** — adopt `contracts`; drop its hand-parsed verdict copy.
- **tracelens** — adopt `contracts`; drop its hand-parsed verdict copy.
  Unblocked 2026-07-13 (tracelens PR #6 merged); dossier task
  `tracelens/migrate-into-workbench` tracks it.
- **local** — migrated in 2026-07-09 (the second tenant; going public touched
  it). `contracts` adoption is owed only if/when it reads verdicts — nothing in
  it does today.

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
