# Known defects

Reproduced by an independent review on 2026-09-17 against `cd3295b`, with deterministic Go
probes (`poc/review-2026-09-17-codex/review_probe_test.go.txt`, run through `go test -overlay`;
output in `probes.txt`). A PASS in that output means the bad behavior reproduced. None of these
is covered by the package's own tests. **Status 2026-09-17: all thirteen are fixed on branch `swarm/real`, each with a regression test
in `internal/swarm/review_regress_test.go` (or `internal/poc/score_test.go` for the scorer) that
began as the review's probe and now asserts the safe behavior.** How each was fixed is in the
last column.

| # | defect | where | consequence | fix |
|---|---|---|---|---|
| 1 | A holder's expired claim still rules when it passes its old epoch; a superseded epoch rules after the newer holder expires | `decide.go` claimForRule | fencing does not enforce the live-claim contract | Fixed: an explicit epoch is a fencing token; it must be the caller's live, unexpired claim and never acquires. |
| 2 | A peer overrides an operator ruling by naming it in `--supersedes` | `decide.go` tiebreak | the tier order can be bypassed | Fixed: rank is checked before `--supersedes` is honored. |
| 3 | A lock older than a minute is broken while its holder lives; the old holder's release then deletes the new holder's lock | `state.go` lock | concurrent mutators; age is not proof of death | Fixed: OS advisory locks (flock / LockFileEx) on a file that is never deleted; death releases, age does nothing. |
| 4 | Restoring a request to its pre-save state after the decision append lets it be ruled twice | `decide.go` Rule | decision append and request completion are not one transaction | Fixed: the ledger append is the commit point; a retry finds the prior ruling and completes the request. |
| 5 | Two admissions against one free seat both succeed | `admit.go` | admission reserves nothing | Fixed: admission checks and reserves under one lock; reservations lapse or convert when the branch appears. |
| 6 | take, drop, take reuses epoch 1; a delayed drop from the same seat deletes the replacement lease | `resource.go` | tokens are not monotonic | Fixed: release leaves a tombstone so epochs only grow; `drop` takes the token and refuses an old one. |
| 7 | A split accepts two children with one name; a refused second split has already queued its child | `split.go` | duplicate and partially committed task creation | Fixed: the batch is validated before any write; the ruling is scoped to the children and found again on retry. |
| 8 | A merge that exists but was never verified is skipped as already merged on retry | `consolidate.go` | an unverified merge is accepted | Fixed: acceptance needs a passing receipt at the exact head; an unverified head is verified on entry and a red one refuses. |
| 9 | With the provider missing, a wake consumes the note, starts nothing, and records exit 0 | `wake.go` | lost messages | Fixed: notes are acknowledged only after the provider took a turn; failures back off, and park for the operator after three. |
| 10 | An old session's late Stop after a replacement's SessionStart makes the old session the resumable one | `wake.go` recordSession | a wake can resume the wrong session | Fixed: only a SessionStart may replace a seat's session; late events from another session are ignored. |
| 11 | After A merges B and B advances, B's old files are attributed to A; files introduced only in a merge commit are invisible | `board.go` ownFiles | wrong contention and routing exactly where consolidation happens | Fixed: own changes follow first-parent history with combined merge diffs. |
| 12 | Removing the last ledger line is accepted; a torn append makes the whole ledger unreadable; no fsync | `state.go`, `decide.go` | the chain detects edits, not truncation or crashes | Fixed: a committed head pointer detects a removed tail; a torn tail beyond the head is dropped and trimmed; writes fsync. |
| 13 | Scorer kill condition 4 initializes pass and never clears it | `poc/score.go` killFaultA | "passed every kill condition" was not a safety result | Fixed: passes only on a recorded pin alert; unexposed conditions render as "not tested". |

Also from the review: `a/b` and `a_b` collided in inbox, session and resource file names (fixed:
names are hex-escaped);
`check` then edit, take then write and admit then start are observations, not grants; wakes
have per-wake limits but no cumulative budget or backoff.
