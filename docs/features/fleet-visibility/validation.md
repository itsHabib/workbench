# Fleet visibility validation

Date: 2026-09-11. This records executed checks and their limits; private provider
traces, prompts and credentials are not published.

## Source identity

- Visibility code: `3a36614507d519fc1aa8e94baffd3efa72121970` (PR #319).
- Runtime code: `70133937aa467a93a4f89ccefc54ed7afc35a8f8` (PR #318).
- A clean `git merge-tree --write-tree` of those heads produced
  `ce54afd0a0624466f80e3077bb38ebe448f50866`. Fleet, Console and TraceLens were
  built from a disposable export of that tree. This was not a merge to main.
- After the final panel, the browser timeout was aligned to 45 seconds for
  Fleet's 30-second read plus TraceLens's 10-second call and response margin.
  JavaScript syntax and the three state regressions were rechecked; the Go
  binaries were unchanged. This record and deferrals accompany that bounded
  post-cap P1 repair; no fourth panel was requested.
- The unchanged native Output renderer was subsequently moved from PR #318
  into PR #319, with `TestInspectRendersNativeAppServerOutput` proving command,
  file-change and assistant text reach Inspect's visible tail without raw data
  or watcher mutation. This removes the runtime merge dependency.

## Automated checks

The visibility implementation passed full-module race tests, vet and lint at its
initial head. Each code fix round then passed:

```
go test -race ./cmd/console/... ./cmd/fleet/... ./cmd/tracelens/...
go vet ./cmd/console/... ./cmd/fleet/... ./cmd/tracelens/...
golangci-lint run ./cmd/console/... ./cmd/fleet/... ./cmd/tracelens/...
node --test cmd/console/e2e/visibility-state.test.mjs
```

All five CI checks passed at `3a36614`, including Mac and Windows portability.
The final combined source export also passed Console, Fleet watch/core and
TraceLens package/integration tests. Go's HTTP integration test builds the real
Fleet and TraceLens CLIs under disposable state, checks diagnosis and rejects an
arbitrary path as an address without starting a watcher.

Focused regressions cover the complete record at a trace-window boundary,
oversized mailbox/unknown counts, Unicode excerpts, same-session trace changes,
diagnostic fingerprint mismatches, unchanged inspect data with changed attempt
reports, native file-change/final-agent-text extraction, and unsupported native
activity alongside supported steps.

## Browser and real-provider evidence

Browser actions used the local read-only Console pages and actual retained run
state. They did not control providers or modify that state.

| Run evidence | Observed in Console |
| --- | --- |
| Earlier R8 Claude sandbox | Three agents; stopped watcher; $2.63 across seven attempts; handoff, received answer/ack, output and separate author attempts. Removing only the disposable preview Fleet binary showed a disconnected/stale snapshot; restoring it recovered the view. |
| Final Codex runtime workflow | Three agents; real provider session IDs and completed turns; native command, file-change and assistant Output; handoffs; zero collected exit codes. Cost remained unknown because none of six attempts reported cost. |
| Claude SDK runtime workflow | Three completed/exited agents; real session and handoff; $0.82 across all six attempts. Provider-terminal observations were present for each address and the watcher was stopped. |

Arrow-key tab navigation and tab/panel associations were checked in the browser.
On-demand TraceLens accepted both real provider traces with explicit observed-file
coverage. The final Codex author trace normalized to 16 steps. Neither run showed
a supported diagnostic pattern. This is not proof that every provider event kind
is supported or that task completion follows from a diagnostic pass.

The Codex and Claude workflow fixtures store verifier receipts separately from
Fleet work records. Console correctly showed no joined work receipt; it did not
infer one from the provider's completed turn.

The runtime owner records execution and verifier evidence separately:

- [Codex full workflow evidence](https://github.com/itsHabib/workbench/pull/318#issuecomment-5636905609),
  producing [sandbox draft PR #18](https://github.com/itsHabib/fleet-demo-sandbox/pull/18).
- [Final-provider validation](https://github.com/itsHabib/workbench/pull/318#issuecomment-5637018776),
  including Claude [sandbox draft PR #19](https://github.com/itsHabib/fleet-demo-sandbox/pull/19)
  and fresh/resume/cancellation repeats after the runtime's final safety fixes.
- [Earlier combined visibility check](https://github.com/itsHabib/workbench/pull/319#issuecomment-5636865307).

## Limits

No narrow-viewport visual test or large-fleet performance benchmark was run.
The timestamp view contains latest source observations, not a complete event
journal. Provider execution is tested by the runtime owner; these browser checks
validate its consumers. Private raw traces remain local. Known final-review
diagnostic, context and scaling gaps are recorded in [FOLLOWUPS.md](../../../FOLLOWUPS.md)
for judgment; the tests above do not erase them.
