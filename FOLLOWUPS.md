# workbench — follow-ups

Tracked in-repo per portfolio convention (status doc, not issues).

## `local` CLI name shadows the bash/zsh builtin

At a top-level bash/zsh prompt, `local` resolves to the shell builtin before
`$PATH`, so a bare invocation fails with "can only be used in a function". The
README quick-start uses `env local` to sidestep it. If real adoption friction
shows up (users tripping on it past the quick-start), consider a distinct
binary name — operator's call; renaming a CLI is a breaking change to every
skill that shells to it.

## Migration queue

Scope decision (operator, 2026-07-13): the set is **gate, triage, tracelens** —
huddle is out, sense is a lean-no (revisit only on a concrete pull, e.g. a
ballot reducer needing the verdict contract). Same day, the operator upgraded
these three from lazy ("when next touched") to **actively scheduled** — get them
over. The lazy rule still governs anything born later.

**Done:** flare (2026-07-08), local (2026-07-09). flare has since grown as a
tenant in-repo (Slack channel #34, ship-park-receipt lift #35) — the migrated-in
pattern is proven to keep taking features.

**Re-validated 2026-07-15** against the 07-13/07-14 merge batch (workbench
#21–#35, ship #199–#215, gate #14–#17). The sequence still holds; the only
change is a new tracelens CLI consumer (controlroom) — folded into the kickoff.

**Sequence for the remaining three — tracelens → triage → gate:**

- **tracelens** — SCOPED, ready, **fire first**. Kickoff at
  `docs/features/tracelens-migration/KICKOFF.md` (2-PR: move, then contracts
  swap); dossier `tracelens/migrate-into-workbench` (todo). `pers/tracelens@main`
  is unchanged since PR #6 (docs refresh only) and its verdict type still mirrors
  `contracts` with the two known deltas — re-verified 2026-07-15. **New:** there
  are now **two** CLI consumers, not one — ship *and* `cmd/controlroom`
  (its `internal/adapters/tracelens` `os/exec`'s the `tracelens` binary; no Go
  import, hygiene stays clean). The byte-identical exit-code + binary-name
  invariant now protects controlroom too; the kickoff §5 names it.
- **triage** — clean + dormant (last real commit 2026-07-08, only a docs refresh
  since — re-verified 2026-07-15). Same shape as tracelens, smaller. Dossier
  `triage/migrate-into-workbench`. Goes second so two consumers are on
  `contracts` before gate inverts.
- **gate** — LAST, and delicate. It is the verdict type's behavioral source of
  truth — `contracts` currently *mirrors* it; migrating inverts that (gate
  *imports* `contracts`, conformance guards move inside, `Reduce`/ladder stay in
  gate). Two constraints: (1) do it after tracelens+triage are on contracts;
  (2) gate is under **active development** — the 07-15 sweep shows it *more* busy,
  not less (#14 talk-readiness, #15 explain-json, #16 decision-trace viewer, #17
  self-gating via a gate status check + branch protection, all 07-13). A module
  move rewrites every import; wait for that push to lull and coordinate with the
  gate session. Dossier `gate/migrate-into-workbench` tracks the constraints;
  **not fire-now** — its hold is reinforced, not lifted.

**Hygiene note:** `pers/local` received a commit dated 2026-07-12 after being
declared an archive on 07-09 (the usage-log change that also landed in workbench
as #11). Confirm the standalone archive is not diverging from the workbench
copy; freeze it like `pers/flare` once reconciled.

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
