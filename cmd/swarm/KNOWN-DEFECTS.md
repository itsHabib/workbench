# Known defects

Reproduced by an independent review on 2026-09-17 against `cd3295b`, with deterministic Go
probes (`poc/review-2026-09-17-codex/review_probe_test.go.txt`, run through `go test -overlay`;
output in `probes.txt`). A PASS in that output means the bad behavior reproduced. None of these
is covered by the package's own tests. They are listed, not fixed: most sit in code the review
recommends deleting in favor of `cmd/fleet`.

| # | defect | where | consequence |
|---|---|---|---|
| 1 | A holder's expired claim still rules when it passes its old epoch; a superseded epoch rules after the newer holder expires | `decide.go` claimForRule | fencing does not enforce the live-claim contract |
| 2 | A peer overrides an operator ruling by naming it in `--supersedes` | `decide.go` tiebreak | the tier order can be bypassed |
| 3 | A lock older than a minute is broken while its holder lives; the old holder's release then deletes the new holder's lock | `state.go` lock | concurrent mutators; age is not proof of death |
| 4 | Restoring a request to its pre-save state after the decision append lets it be ruled twice | `decide.go` Rule | decision append and request completion are not one transaction |
| 5 | Two admissions against one free seat both succeed | `admit.go` | admission reserves nothing |
| 6 | take, drop, take reuses epoch 1; a delayed drop from the same seat deletes the replacement lease | `resource.go` | tokens are not monotonic |
| 7 | A split accepts two children with one name; a refused second split has already queued its child | `split.go` | duplicate and partially committed task creation |
| 8 | A merge that exists but was never verified is skipped as already merged on retry | `consolidate.go` | an unverified merge is accepted |
| 9 | With the provider missing, a wake consumes the note, starts nothing, and records exit 0 | `wake.go` | lost messages |
| 10 | An old session's late Stop after a replacement's SessionStart makes the old session the resumable one | `wake.go` recordSession | a wake can resume the wrong session |
| 11 | After A merges B and B advances, B's old files are attributed to A; files introduced only in a merge commit are invisible | `board.go` ownFiles | wrong contention and routing exactly where consolidation happens |
| 12 | Removing the last ledger line is accepted; a torn append makes the whole ledger unreadable; no fsync | `state.go`, `decide.go` | the chain detects edits, not truncation or crashes |
| 13 | Scorer kill condition 4 initializes pass and never clears it | `poc/score.go` killFaultA | "passed every kill condition" was not a safety result |

Also from the review: `a/b` and `a_b` collide in inbox, session and resource file names;
`check` then edit, take then write and admit then start are observations, not grants; wakes
have per-wake limits but no cumulative budget or backoff.
