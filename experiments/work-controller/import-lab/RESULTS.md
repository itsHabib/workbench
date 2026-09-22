# Two working apps. The controller has not beaten the simple baseline.

Cheap builders handled the work when given concrete feedback and independent checks. We built
and repaired the same CSV import app two ways: a controller registry plus role cards, and native
messages plus a task file. Both recovered from a deliberate replacement at a written checkpoint.
Both finished with a working browser upload flow and passing API checks.

| Same final oracle | Controller arm | Native arm |
|---|---|---|
| Initial small-app API checks | 5/5 | 5/5 |
| Initial pressure checks | 4/6; cap error, retry failure | 3/6; cap, persistence, retry failures |
| Final small-app API checks | 5/5 | 5/5 |
| Final pressure checks | 6/6 | 6/6 |
| SIGKILL after acceptance, restart, retry | Pass | Pass |
| Kill during accepted unfinished import | Not covered: synchronous | Not covered: synchronous |
| Final Chromium upload, row error, 390px layout | Pass | Pass |
| 16 concurrent retries, one import, conflicting content 409 | Pass | Pass |

Final source commits: controller `dfe60b29518f3bcca3dd9d46118f376a052acf38`, native
`9724853493f9c1e941f3da1fd0ab0117a2d8b5fb`. Exact revisions, oracle hashes and receipts are in
[harness/runs/README.md](harness/runs/README.md). UI and additional probes are in
[runs/local-01](runs/local-01).

## What the controller actually decided

1. **Start one builder per comparison arm.** Each chose its architecture. Native started in
   memory; controller started with a JSON file. Neither received a prescribed queue or database.
2. **Reuse context for new operating needs.** Both already handled 50,000 rows responsively.
   Fix persistence, retry identity, body limits and browser defects with the existing owner.
   No evidence justified another app builder or a background queue.
3. **Resume from records.** Both original builders saved a checkpoint. Fresh Luna sessions
   continued without their predecessors' conversation. The controller invalidated its old
   attempt; an injected late result was rejected. Native recovered using TASKS.md.
4. **Turn discoveries into new work.** A later fault probe found a failed JSON save left a
   memory-only result that retries falsely treated as saved. The coordinator created a new
   job, reused the replacement worker, and independently verified the repair. A final probe
   created another small job for empty-string request IDs. Native's reviewer found raw email
   spaces being silently trimmed; that became a native follow-up and regression test.

The durable controller recorded decisions and fenced old acceptance. The native baseline also
recovered and delivered. There is no demonstrated productivity or economic advantage here.
The routing agent was this interactive parent session, not an autonomous scheduler inside the CLI.

## Lessons worth keeping

- **Let pressure choose infrastructure.** Native added SQLite to meet persistence and retry
  needs. Controller repaired its file store. Both stayed synchronous; a queue was unnecessary
  for this workload. We added no telemetry service: external request traces answered the questions.
- **Check the user path.** Initial Python tests passed while native JavaScript failed to parse
  and the other app overflowed on mobile. A real browser caught both.
- **Review the reviewer.** The first oracle wrongly required a form element and particular
  terminal labels. We removed those assumptions, kept the original failures, and replayed
  the same old commits with the corrected oracle. We also tightened concurrency checks and
  added an explicitly separate accepted-result crash test. Oracle changes are recorded, not hidden.
- **A successful retry must mean durable work.** Injecting a save failure found a bug normal
  restart tests missed. The file store now rolls back memory on save failure; the regression
  retries and reopens the store. A SQLite write-refusal probe also recovered successfully.
- **A good handoff is a strong baseline.** The native task file was sufficient for its fresh
  worker. Extra storage earns its place only when it solves losses or reconstruction costs
  that a simple file does not.

## Limits

Four Luna worker sessions (two initial, two replacements), one Sol runner, and this parent
coordinator/reviewer. Models were explicitly requested; complete provider billing and token
accounting are unavailable. One paired run on one shared host is not a statistical comparison.
The controller arm received extra post-hoc probes; do not turn final bug counts into a score.

Replacement happened deliberately after a committed checkpoint, not during an unannounced agent
crash. Service SIGKILL tests are real, but only cover completed accepted work. We did not induce
a merge conflict, an independent feature needing another builder, or a coordinator-session crash.
Large-data checks use counts and sentinels, with uniqueness in concurrency; they do not compare
every returned field against a full 50,000-row oracle. Overlap is observed at the client threads,
not proof of simultaneous processing inside the server. HTTP/static checks and browser checks
are separate. Browser QA covers a small file; no large-table rendering or screen-reader claim.
The UI does not yet attach request IDs, so API retry guarantees are not an end-to-end browser
lost-reply guarantee. The apps are disposable loopback demos, not production services.

Rooms was attempted but admission returned `pool_full: all 8 slots claimed` while `rooms ls`
reported none. The pre-existing records were preserved, forwarding was torn down, and the local
VM was stopped. No app ran in Rooms and no cloud resources were created. See the
[readiness receipt](runs/local-01/rooms-readiness.md).

## Next experiment

Use continuous arrivals and one genuinely independent feature, then interrupt a worker between
checkpoints. Compare coordinator recovery time and lost work against the same native task-file
baseline. Promote the registry into Fleet only if that comparison shows a benefit. Restore Rooms
admission before trying the same artifact there; don't add a scheduler to work around broken slots.
