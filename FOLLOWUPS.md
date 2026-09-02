# workbench — follow-ups

Tracked in-repo per portfolio convention (status doc, not issues).

## org: transfer's last orphan window needs a cross-chain transaction

`org transfer` writes to two chains under two locks. It assigns to the
destination first so a crash leaves a *visible* double-hold rather than a
silent orphan, fences each append to the tip it read, and re-reads the
destination immediately before unassigning the source. One window survives
that: if the destination holder drops the work between that re-read and the
source's append, the source's unassign still succeeds — its own tip has not
moved — and the item ends up held by nobody.

It cannot be closed at this layer. `Draft.ExpectTip` fences a chain against
ITS own movement; there is no way to make one chain's append conditional on
another chain's state, because there is no lock ordering across homes and no
two-phase commit. The options, when it matters:

- A tenant-level lock taken for the duration of a multi-chain verb. Simple,
  and it serializes every transfer in the tenant against every other.
- An intent record on both chains (the `intent`/`resolve` effect machinery
  already models exactly this: an open effect survives a crash and blocks new
  claims until resolved), making the transfer a two-phase operation whose
  half-done state is a first-class, kernel-refused-until-resolved condition
  rather than something a sweep notices afterwards.

The second is the shape this substrate already believes in. Until then the
residual window is documented, `sweep` reports the double-hold half of it,
and nothing reports the orphan half — which is the honest gap.

## org: recharter has no writer, because widening authority has no check

`KindRecharter` is kernel-admissible and its own doc says it is "authored under
the parent charter", but nothing enforces that: `checkRecharter` verifies only
that `min_reader` is monotone, and `checkWriter` accepts the current holder's
own incarnation. A CLI verb was written and then withdrawn from #272 on that
finding — exposing it would let a role raise its own tier, add effect classes,
lift its ceilings, drop the supervisors that may take it over, or widen its
scope, all self-signed.

Two things are missing, and the second is why the obvious guard does not work:

1. **Parent authority.** There is no mechanism for a record to be authorized by
   another role. `takeover` names a `party` and `checkTakeover` verifies it
   against `Terms.Supervisors`, so the shape exists; recharter needs the same,
   plus the operator-facing question of who the parent is for a top-level role.
2. **Attenuation the kernel can verify.** Effect classes (subset), supervisors
   (no shrink) and scope (every new entry covered by the old) are checkable.
   `Tier` is an opaque string — the kernel imposes no ordering, so it cannot
   tell T1→T3 from T3→T1 — and the ceilings have no consumer, so whether 0
   means "none" or "unlimited" is undecided. A law refusing what it cannot
   compare would have to freeze tier entirely.

Until both exist, terms are set once at charter. A role whose terms are wrong
is retired and re-chartered, which is visible in the chain rather than
self-signed inside it.

## org: annul is a repudiation, not a revert

`applyStructural` appends the annulled digest to `Annulled` and changes nothing
else: `Terms`, `Held`, `Active` and `NextDue` still carry whatever the annulled
record did. That is consistent with an append-only chain (correct forward), and
`org annul` now prints the effect still standing so the verb cannot be misread
as undo. What is undecided is whether a reader should SKIP annulled records
when folding. It cannot be done in one pass — a record's annulment is only
known after it has been applied — so it would mean a two-pass fold, and it
would change the derived state of every existing chain that carries an annul.
Worth deciding before anything depends on `Annulled` for more than reporting.

## org: scope membership cannot become an admission law as the kernel stands

The field report (§4.4) proved `assign` enforces no scope at all: a lane
chartered `github:Acme/Repo` accepted `jira:PROJ-9999`, `github:Other/Thing#1`
and `banana:whatever` without a murmur. The predicate now exists
(`contracts/org.InScope`) and both `intake` and `sweep` apply it, but it is a
FINDING, not a refusal. Two facts block the law:

1. **Replay re-admits.** `Reduce` folds by calling `Advance` → `Admissible` on
   every historical record. A law added today is therefore applied to records
   written years ago: every chain that ever assigned outside its scope stops
   folding, which is a worse failure than the drift it prevents.
2. **The obvious escape does not exist.** An opt-in charter term
   (`scope_enforced`) would bind only new charters — except `Terms.Canonical`
   emits *every* field, with no omission of zero values, so adding one changes
   the canonical bytes of every charter ever written and invalidates their
   digests. The encoder's own doc states the constraint: once a record is
   written, the bytes that produced its hash can never change.

So enforcement needs a scheme version: `canon/v2` with a Terms shape that
omits absent fields, records written at the new scheme, and admission laws
gated on the record's own scheme so a v1 record is judged by v1 rules. That is
a real migration, not a flag. Until then, drift is reported by `org sweep`
(`scope_drift`), `org intake`, and `org transfer` (which warns when the
destination's scope does not cover the work it just moved), and the skills'
"mechanical predicate" claim is true of the predicate but not of admission —
say detected, not prevented.

## gate: mid-run merge race can still park (codex P1 on #219, deferred)

The already-merged refusal (#219) reads the view snapshot gathered at run
start. A PR that merges *during* the run — between evidence and the terminal
outcome, a window the model rungs can stretch to minutes — still evaluates
against the OPEN snapshot and can park. Eliminating the race would need a
second live read inside `act`, which today decides purely from recorded state
(the decisions-reconstructable-from-state-alone contract); any such read must
be recorded as evidence, not consulted as a side channel. Deferred because the
park is now recoverable instead of unresolvable: re-running `gate gate`
refuses `already_merged`, and that refusal supersedes the stale park in both
the inbox reduction and the protected-authorization terminal index. The pass
path was already safe — the emitted merge command is `--match-head-commit`
pinned and GitHub refuses to merge a merged PR. Revisit only if the race is
observed parking runs in practice.

## runway: `writeResultAtomic` does not fsync the containing directory (claude on #259, deferred)

`controller.writeResultAtomic` syncs the temp file before `os.Rename`, so
`result.json`'s *contents* are durable, but it never fsyncs the run directory
afterward. A power cut can therefore lose the directory entry for a file whose
bytes reached the platter. Pre-existing — surfaced while reviewing the
read-ordering fix in #259, not introduced by it.

Deferred because the failure is already benign under the reconcile contract: a
lost `result.json` reads as "no result", the journal cannot be terminal without
it (that ordering is the invariant #259 restored on the read side), and
`reconcileControllerLost` simply writes a fresh `controller_lost` receipt. The
run loses its original terminal reason, not its terminal truth. Close this by
opening the parent dir and `Sync`ing it after the rename — in
`writeCancelMarker` and `claim.createExclusive` too, which share the shape — if
runway ever needs the receipt's *reason* to survive host power loss.

## AI gateway egress

- **Construction-time credential read.** Gate reads `ANTHROPIC_API_KEY` once
  when it constructs the cloud model. That matches gate's short-lived CLI shape,
  but a future long-running consumer would keep a stale gateway token until
  restart. If that consumer appears, replace the stored key with an injected
  `KeySource` function; do not put a token-minting subprocess in gate's request
  path.
- **Cursor runtime has no gateway route.** The Rooms `agent-cursor` profile
  carries `CURSOR_API_KEY`, but Cursor's runtime exposes no base-URL override.
  In an environment where the AI gateway is the only sanctioned egress, that
  runtime is unavailable rather than misconfigured. Retirement is a separate,
  lifecycle-compatible change; do not deepen the dependency meanwhile.
- **Local escalation target.** `local.Ask` already accepts an injected
  `Escalate` function, but no caller wires gate's cloud path into it. Consider
  that composition only when a real caller needs routine low-confidence
  escalation; keep `local/` on Ollama and preserve its leaf-package boundary.

## `local` CLI name shadows the bash/zsh builtin

At a top-level bash/zsh prompt, `local` resolves to the shell builtin before
`$PATH`, so a bare invocation fails with "can only be used in a function". The
README quick-start uses `env local` to sidestep it. If real adoption friction
shows up (users tripping on it past the quick-start), consider a distinct
binary name — operator's call; renaming a CLI is a breaking change to every
skill that shells to it.

## gate `resolve` open-check→append is not atomic (cross-process double-apply) — PARTIALLY CLOSED (2026-08-02, codex P1 on #137; scope corrected by codex P1s on #210)

**Partially closed.** The same-run case is fixed; two variants remain open and are
named below. Do not read this entry as done.

The note called for "a single append that fails if the run's terminal
moved" — and by the time it was written that primitive already existed:
`state.AppendIfAbsentParentWhereAfterAudit` evaluates a caller `check` against
the audit snapshot *while holding the store lock* (it landed with the
authorization work, and `record`/`recordEscalation` already used it via
`checkNoOpenClaim`). `applyJudgment` now takes that path with a
`requireOpenEscalation` option: the check re-derives the run's newest
action-or-escalation terminal from `audit.All` and refuses with
`errStaleEscalation` unless it is still this escalation. `cmdResolve` sets the
option; `judge` does not — a probability trade-off, not immunity, recorded as
"Still open (3)" below.

The unlocked `escalationIsOpen` pre-check in `cmdResolve` stays, now explicitly
advisory — it gives a replayed tap a friendly message without a store round-trip,
and the authoritative test is the one under the lock. This covers what the
process-local `escLocks` in `cmd/escalate/internal/serve` could not: a second
serve process on the same `-state`, and a CLI resolve racing an HTTP callback.

Note the uniqueness guard was never sufficient here on its own: it is keyed on
the escalation id, so it stops a *second judgment for the same park* but not a
judgment landing against a park a concurrent re-park had already superseded.
That second case, **within one run**, is what this closes.

### Still open (1): the open-park notion is run-scoped; the inbox's is subject-scoped

`newestTerminal` filters by run, matching the pre-existing `escalationIsOpen`.
`observe.parkedRuns` does not: it folds per run and *then* reduces by subject —
`key := "<repo>#<number>"`, newest `order` wins (`inbox.go:539-556`). So when one
PR has been gated repeatedly, resolving a stale escalation on the older run passes
the run-scoped check, appends an action at the end of the log, and that action
becomes the subject's newest terminal — suppressing the genuinely newer park from
both inbox projections.

This predates the fix (main's `escalationIsOpen` was already run-scoped), so it is
a scope limitation rather than a regression. The durable fix is to stop having
three copies of "is this park open": `cmdResolve`'s pre-check, the locked check,
and `parkedRuns` each reduce it independently. Extract the subject-scoped
reduction once and have all three consume it — otherwise they will keep drifting.
Owner: gate. Surfaced by codex P1 on #210.

### Still open (2): the judgment is linearized, the terminal is not

The locked check guards the *judgment* append. The state transition the inbox
actually consumes is the terminal `act` append, which happens later in
`finishJudgment` → `actAfterJudgment` under a separate lock whose predicate is
`checkNoOpenClaim` — "the subject has no open claim," not "this escalation is
still the open park." Another run can park for the same PR in that window; the
resolve's action then lands after it and hides it.

Closing this means carrying the expected-open escalation into the terminal append's
predicate, which touches the shared `act` path used by ordinary gate runs — a wider
blast radius than the judgment-only guard, and why it is not in #210. Owner: gate.
Surfaced by codex P1 on #210.

### Still open (3): `judge` does not take the guard, by choice

`cmdJudge` leaves `requireOpenEscalation` false, so the same TOCTOU the guard
closes for `resolve` remains open on the judge path: `cmdJudge` reads the run,
derives the newest escalation, and appends — and a concurrent re-park in that
window lands the judgment against a superseded park.
`TestJudgeDoesNotTakeTheResolveOnlyOpenGuard` constructs exactly that ordering
and asserts the judgment is accepted, so the cost is pinned rather than hidden.

The two paths differ in *how stale* their id can be, not in whether the window
exists. `resolve` is handed an id a notification carried, which can be
arbitrarily old — human-scale. `judge` derives its own, so its exposure is
program-scale: the microseconds between read and append. Failing judge there
would hand an operator a retry for a race that is possible but practically
improbable, which is why the trade-off went this way.

It is a probability judgement about a merge-authorization boundary, so it should
be revisited if concurrent writers on one run ever stop being hypothetical — a
second `escalate serve`, or agents judging the same run in parallel. Closing it
is one line (`cmdJudge` sets the option) plus a decision to accept the retry.
Owner: gate. Surfaced independently by codex P1 and by review on #210.

## ~~`escalate serve`: acknowledge the Slack tap within 3s, deliver the outcome async~~ (2026-07-27, codex P1 on #140)

**Done.** `ServeHTTP` now verifies + authorizes synchronously, acks 200 within
Slack's ~3s window (replacing the card with a working state that drops the stale
buttons), and runs the grant lookup + `gate resolve` in a background goroutine —
then POSTs the outcome to the interaction's `response_url` (`replace_original`,
✅ merged / ⛔ blocked / ☑️ already resolved), guarded to an https Slack host. The
open UX question (what the card shows during the run, whether to drop the buttons)
was answered by the default: the ack card shows "⏳ Recording …" and drops the
buttons immediately. See `docs/features/escalation-plane/escalate-serve.md`.

## `escalate serve`: durable resolution across a HARD crash (2026-07-27, codex P1 follow-on on #150)

The async ack (#150) acks the Slack tap with a 200 before `gate resolve` runs, so
Slack never retries it. A **graceful** shutdown (SIGTERM / Ctrl-C / redeploy) now
drains the in-flight resolves before exit (`Server.Wait`, wired in `cmdServe`), so
the controlled case is covered. The residual gap is a **hard** stop mid-resolve —
SIGKILL, panic in the runtime, power loss — after the 200 but before gate records
the decision: the tap is lost silently (buttons already gone, no Slack retry).

Durable fix (deferred — it is a real rearchitecture, not proportionate to the POC
single-operator ingress): persist the accepted decision to disk BEFORE acking, and
replay any unfinished entries on startup — an at-least-once accept log in front of
`gate resolve` (whose `escalationIsOpen` guard already makes replay idempotent).
Owner: `escalate serve`. Warranted once the ingress is always-on / multi-operator
rather than a phone-tap POC behind an off-by-default toggle.

## Lazy-migration queue (graduate in when next touched)

New planes are born here; existing tools migrate in when next in hand, not as a
sweep. Order is by convenience, not priority. Each is handed to that tool's
owner — not this repo's work to force.

- ~~**gate** — the verdict type's behavioral source of truth. When next touched,
  graduate it in and have it *import* `contracts` for the shared type, so the
  conformance test guards drift from the inside.~~ Done 2026-07-17: migrated in
  as `cmd/gate` (byte-identical move, then `contracts` adoption in the
  follow-up PR — `verify`'s Verdict/Producer/Subject/Finding are now aliases of
  `contracts`, `observe` decodes `contracts.Verdict` instead of a hand-parsed
  copy, and `ProducerString` presentation moved into gate). The reducer and the
  ladder law never moved. The only queue entries left are huddle and sense,
  both "graduate in when next touched" — no active migration is planned
  (2026-07-13 freeze: huddle out, sense lean-no).
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

- ~~**`bestTandem` may skip a loop start after a partial periodic match**
  (e.g. `A,B,A,X,A,X,A,X` at period 2: the failed scan from 0 jumps past
  the real `A,X`-run start at 2). Changing the scan changes detector
  behavior, so it is owed to tracelens's own iteration with a corpus case
  that pins the improvement — not to a relocation diff.~~ Done 2026-07-22
  (PR #88): the `i=j` skip now fires only for confirmed runs
  (`r>=minRepeats`); a stray match advances by one. Pinned by
  `TestBestTandem_RunStartNotSkippedAfterStrayMatch`; corpus gate unchanged.
- ~~**A Claude stream truncated right after an `assistant` `tool_use` event
  decodes as "unrecognized ship event dialect"** (exit 2) instead of an
  analyzable aborted run — the dialect markers only key on `user`/`result`
  events. Same rule as above: a decoder-behavior change, owed to
  tracelens's own iteration with a truncated-at-tool_use corpus case.~~
  Done 2026-07-22 (PR #88): an `assistant` event carrying a `tool_use` block
  is now a Claude-dialect signal (cursor uses `tool_call`, codex `item.*`),
  so the truncated run detects and decodes as an aborted trajectory. Pinned
  by `TestDecodeShipEvents_TruncatedAtToolUseDecodesAsClaude`; a bare
  assistant *text* event stays dialect-neutral.
- **`bestTandem`'s confirmed-run skip can leap a longer run starting inside
  the confirmed one** (2026-07-22, PR #88 review). After a confirmed run
  ending at `j`, `i = j` skips positions in `(j-p, j)`; a period-p run
  starting there begins its extension checks past `j` and can extend beyond
  it (e.g. `A,B,A,B,A,X,A,X,A,X` at p=2: the `(A,B)×2` run at 0 skips to 5,
  hiding the longer `(A,X)×3` run at 4 — reported repeats undercount).
  Pre-existing, not introduced by the PR #88 guard fix. Same rule as the
  entries above: owed to tracelens's own iteration with a corpus case that
  pins the improvement.

## triage migration — deferred findings (2026-07-16, PR #51 review)

All three are pre-existing behavior, byte-identical to the standalone repo —
real observations, but classifier-behavior changes the byte-identical move
must not smuggle in (same rule as tracelens above):

- ~~**Empty stdin classifies as T0 instead of failing.** `triage-floor` on
  zero-byte input returns a T0 floor, so an upstream failure that yields
  empty output (`gh pr diff` auth/network error piped through) reads as
  "safe". A guard (empty diff → exit 1, operational failure) is owed to
  triage's own iteration — the A/B matrix pins today's shape, empty-stdin
  case included.~~ Done 2026-07-20: fail-closed on empty stdin + scanner
  errors (`triage-fail-closed-guards`).
- **The control-plane rule covers the classifier's evidence, not its
  source.** `RUBRIC.md` / `labels/` / `mismatches.jsonl` fire T3;
  `cmd/triage/internal/{floor,advisory}/**` do not — equally true pre-move
  (`internal/floor/**` never matched). Extending it to the classifier
  source is a RUBRIC §5.4 policy change plus a corpus case, owed to
  triage's iteration.
- ~~**A diff line over the scanner's 16 MiB cap silently truncates the
  parse.** `ParseUnifiedDiff` never checks `sc.Err()`, so `ErrTooLong`
  (e.g. a minified generated asset) classifies the partial diff instead of
  failing. Propagating scanner errors is a parser-behavior change; owed
  with a corpus case that pins it.~~ Done 2026-07-20 with the fail-closed
  guards above.
- ~~**Hygiene nits in the moved floor code** (claude review, all pre-move):
  `sawCode` and `reLoosenGuard` are set/compiled then blanked out
  (vestigial or unimplemented heuristic — decide which), `hasCodeChange`
  merges `Added`+`Removed` into a throwaway slice, and `triage-advisory`'s
  main drops the `MarshalIndent` error where `triage-floor` propagates.
  Behavior-neutral cleanups, but the move ships the files byte-identical;
  fold into the same triage iteration as the items above.~~ Done 2026-07-20:
  vestigial `sawCode`/`reLoosenGuard` deleted, `hasCodeChange` iterates the
  two slices, `triage-advisory` propagates the marshal error.

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

## ~~@claude reviewer~~

~~`claude.yml` is committed but @claude is **not** requested until the operator
sets the `CLAUDE_CODE_OAUTH_TOKEN` repo secret. Once set, @claude joins the
reviewer set (@codex, @cursor) on the next PR.~~ Done 2026-08-03: the
`CLAUDE_CODE_OAUTH_TOKEN` repo secret was set; @claude has joined the reviewer
set (@codex, @cursor) and reviewed #275.

## ~~cmd/triage: gocognit debt in internal/floor (2026-07-17)~~

~~`golangci-lint run ./...` flags `manifestIsDev`, `migrationStatements`, and
`ParseUnifiedDiff` in `cmd/triage/internal/floor` for cognitive complexity.
Pre-existing — the code arrived byte-identical with the tenant move (#51) and
was outside every subsequent PR's diff. Fix is the house-style extraction
(≤2 nesting per scope) the next time triage is touched; not worth a
standalone churn PR.~~ Done 2026-07-20: house-style extraction paid the
gocognit debt on those three; `Classify` still carries its own
`nolint:gocognit,cyclop` (rubric-as-one-pass — separate deferral).

## custody derive-attenuation — deferred findings (2026-07-22, PR #105 adversarial pass)

Both surfaced by the skeptic pass on the parent-chained-grants PR; neither is a
correctness hole in what shipped, and both are owed to custody's own iteration:

- **The signing pre-image injectivity property test doesn't draw the new signed
  fields.** `Parent` and `BoundSource` joined the `sign` pre-image at Version 2
  and have example-level mutation coverage (`sign_covers_parent_and_bound_source`
  mutates each in the persisted record and asserts the token invalidates), but
  the cross-field-ambiguity property (the length-prefix injectivity generator
  that pins `["a,b"]` ≠ `["a","b"]` across arbitrary field values) still only
  draws the original fields. Extend that generator to draw `Parent`/`BoundSource`
  so the ambiguity law covers the full v2 pre-image, not just its v1 subset.
- **The parent-record hot-path load in `checkChainDepth` is an undocumented
  cascade-revoke coupling.** `Validate` loads a child's parent record to read its
  `parent` field (depth check); if that record is gone, `load` returns
  `ErrNoGrant` and the child refuses. That is fail-closed and arguably desirable
  — deleting a parent grant revokes its children — but it is a behavioral
  coupling roots don't have (a root's validity depends only on its own record),
  and it is nowhere written down. Decide whether cascade-revoke is intended
  semantics (then document it in the custody design doc + a test that pins
  "delete parent → child refuses") or an accident to sever (child carries the one
  parent field it needs, no live parent load) — the P3 tap-listener work is the
  natural place to settle it.

## Idea (parked 2026-07-17): an MCP surface for the merge-loop verbs

Operator flagged mid-migration ("something to take on next maybe"): wrapping the
loop verbs an agent drives by shell today — `gate gate`/`judge`/`explain`, the
review-cycle bookkeeping — in an MCP the way `workbench-mcp` wraps the driver
state verbs. The standing position stays: planes compose via exit codes + JSONL
(lightest channel wins; the exit code IS the API; CI runners have no MCP), and
`judge -decision` should remain a deliberately *inconvenient* human act, never
one tool-call away from an agent. What would change the calculus: session-state
verbs emerging on the gate side (e.g. a park-inbox an agent polls, cross-run
grant/cycle queries) where discovery + typed schemas beat re-shelling — the
same bar workbench-mcp cleared. Evaluate then; not before.
