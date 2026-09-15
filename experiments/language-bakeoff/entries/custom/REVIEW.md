# Independent correctness review

A fresh-context reviewer read only this repository at commit 4e328ba. It
built and ran `wb` in a scratch directory and reported 11 findings, each
reproduced (CONFIRMED). All 11 were fixed in one round, and each fix has
a regression test.

| # | Severity | Finding | Disposition |
|---|---|---|---|
| 1 | P0 | `.WB/journal.jsonl` passed the case-sensitive reserved-directory check and, on a case-insensitive filesystem, overwrote the journal. `Notes.txt` and `notes.txt` could both be owned. | **Fixed.** Paths must be printable ASCII (no Unicode aliasing), and the reserved-directory, temporary-suffix and ownership checks all ignore case. Tests: the `reserved path in another case`, `ownership differing only in case` and `non-ASCII path` cases in `TestCompileReportsExplicitErrors`. |
| 2 | P1 | A symlink planted at `<path>.wb-partial` redirected the write into an unowned file. The temporary name itself was not reserved. | **Fixed.** `foundation.CreateTemp` removes anything at the temporary name and creates the file with `O_EXCL`, so a write never follows a link. The checker rejects owned paths ending in `.wb-partial`. Tests: `TestWriteNeverFollowsAPlantedTemporaryLink`, `TestPlantedTemporaryLinkIsNotFollowed`, and the `temporary-file suffix` checker case. |
| 3 | P1 | A literal input reached through a symlink was digested with `Lstat`, so edits to the linked content never made the work stale. | **Fixed.** Literal inputs are digested with `ObserveTarget`, which follows links; `os.Root` still refuses a link that leaves the workspace. Tests: `TestObserveTargetFollowsLinksOnlyInsideTheWorkspace`, `TestSymlinkedInputIsTracked`. A remaining limit is in the README: a link that aliases another declaration's owned path is not ordered after its owner. |
| 4 | P2 | Invalid UTF-8 in strings became U+FFFD in digests and saved plans. | **Fixed.** The lexer rejects invalid UTF-8 with a position. Test: `invalid UTF-8` case. |
| 5 | P2 | A literal input named `journal` collided with the plan's journal check, so every apply was refused. | **Fixed.** The journal check is keyed by the reserved `.wb/journal.jsonl`. Test: `TestInputNamedJournalDoesNotCollide`. |
| 6 | P2 | Work whose output path held a directory ran its command anyway, then failed and left a temporary file behind. A symlink at the output was silently replaced. | **Fixed.** The planner reports a conflict for work whose output is a directory or a symlink, and apply refuses before anything runs. A failed rename removes the temporary file. Test: `TestWorkOutputThatIsNotAFileIsAConflict`. |
| 7 | P2 | Work writing into a directory that only a later `keep file` would create failed on every apply. | **Fixed.** Creating the temporary file also creates missing parent directories, as `keep file` already did. Tests: `TestRunCreatesTheOutputDirectory`, `TestWorkOutputDirectoryIsCreated`. |
| 8 | P2 | `TestEffectsStayInsideTheWorkspace` never reached an effect: a mutant that bypassed `os.Root` in the write path still passed. | **Fixed.** New tests reach the write itself: `TestWritesCannotLeaveTheWorkspace`, `TestWriteNeverFollowsAPlantedTemporaryLink` and `TestRunRefusesAWorkingDirectoryOutsideTheWorkspace`. |
| 9 | P2 | An edited saved plan (for example with an empty `argv`) could panic after writing a start record. | **Fixed.** A saved plan now carries its source text. `apply -plan` recompiles that text with the same checker and refuses (exit 2) a plan whose declarations do not match it. The command adapter also rejects an empty argv. Tests: `TestEditedOrMalformedSavedPlanIsRefused`, `TestRunRejectsAnEmptyArgv`. |
| 10 | P3 | A malformed plan exited 1 ("failed partway") instead of 2 ("invalid input"). | **Fixed.** Plans are validated on load. Test: `TestEditedOrMalformedSavedPlanIsRefused`. |
| 11 | P3 | The README said renaming a kept resource does nothing when the content matches. In fact, work that references it reruns once, because inputs are keyed by upstream name. | **Fixed** in the README's identity row. |

The reviewer also checked the following and found them correct:

- `os.Root` confinement against `..`, absolute paths and symlinked parents
- planning never writes
- unchanged re-apply spawns no child process
- stale-plan refusal for input edits, tampered outputs and new journal records
- "decided during apply" work
- torn-line sealing and start-before-effect ordering in the journal
- cycle detection
- the documented exit codes on the main paths
- the demo transcript matches real output

## Round 2

A second fresh-context reviewer read only this repository at 9cfa543. It
found no P0 or P1 problems. Ten of the 11 fixes held; finding 8 was judged
not closed. It reported 6 P2 and 2 P3 findings, all reproduced.

This was the last round allowed by the two-round cap. The fix bar for it
was the same as round 1: evidence integrity (this POC's equivalent of an
authorization invariant) and holes or regressions in the round-1 fixes.
Everything below that bar is recorded with its reason and left as is.
Round 3 was not run.

| # | Severity | Finding | Disposition |
|---|---|---|---|
| R2-1 | P2 | Work is not rechecked against the plan immediately before it runs, unlike kept resources. An edit to a work's owned output made while earlier work is running gets overwritten instead of refused. | **Deferred.** The work is re-decided from fresh observation, so it never runs against stale inputs. The only effect is that an outside edit to a wb-owned output made mid-apply is overwritten. The README's Limits section says so. The fix is known and small: reuse the kept-resource recheck before `run`. |
| R2-2 | P2 | A directory symlink (`state -> .wb`) got past the lexical `.wb` and ownership checks and wiped the journal. | **Fixed.** Evidence integrity. `foundation.Observe`, `CreateTemp` and `MkdirAll` refuse any owned path under a symlinked parent directory, so the plan fails before any effect. Tests: `TestSymlinkedParentCannotReachTheJournal`, `TestNoWriteThroughASymlinkedParentEvenInside`. |
| R2-3 | P2 | The JSON compaction added in round 1 corrupted list values that end in `",`, so a legitimate saved plan was refused as edited. | **Fixed.** This was a regression from round 1. Each item is now marshalled on its own. Test: `TestSavedPlanKeepsQuotedArguments`. |
| R2-4 | P2 | Journal reads and appends followed a symlink planted at `.wb/journal.jsonl`. | **Fixed.** Evidence integrity. `.wb` must be a real directory and the journal a regular file, checked when reading and when appending. Test: `TestJournalMustBeARealFile`. |
| R2-5 | P2 | Saved-plan validation bound `decls` to the source, but a plan with `"checks": []` skipped the stale-plan refusal. | **Fixed.** Checks are now compared in both directions: a fact the plan never recorded counts as stale (exit 3). Test: `TestSavedPlanCannotDropItsChecks`. |
| R2-6 | P2 | `.wb-partial` was reserved only as a final suffix, so `x.wb-partial/y` could block `x`'s temporary file. | **Fixed.** The round-1 fix was incomplete. The suffix is now reserved in every path component, ignoring case. Test: the `temporary-file suffix in a directory` checker case. |
| R2-7 | P3 | The README said wb "never deletes anything", but `CreateTemp` removes whatever sits at `<path>.wb-partial`. | **Fixed** in the README. |
| R2-8 (= 8) | P3 | Mutants that swap the `os.Root` write primitives for plain `os` calls still pass the suite, because the `Remove` and realParents checks on the same path refuse every escape first. | **Deferred.** In deterministic tests these are equivalent mutants: any path that could escape at write time is refused by an earlier check on that same path. `os.Root` adds protection only against a concurrent symlink swap. The README records this as a limit. |
