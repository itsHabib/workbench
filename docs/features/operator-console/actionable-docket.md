# Actionable docket correction

## Problem

Console v0 renders gate's per-run parked projection literally. The live operator
state currently shows dozens of parked rows even though the identifiable PRs are
already merged, and the docket leads with opaque `run_...` identifiers without a
clickable PR subject. It therefore fails its primary question: "what needs me?"

The audit log is append-only and must remain untouched. The correction belongs in
gate's read-only `next` projection; console remains a renderer over that projection.

## Required behavior

### Gate projection

1. Recover a run's PR subject from the artifacts already in the log, not only from
   the escalation body. Support the shapes gate itself has emitted over time:
   escalation `repo`/`number`, verdict or judgment `subject`, and PR evidence
   `pr` plus its `data` payload.
2. Recover useful display facts when present: repository, PR number, title, and
   judged head SHA. Add a canonical GitHub pull URL derived from repo + number.
3. Reduce terminal state by PR subject, not only by run ID. For all runs that refer
   to the same `repo#number`, the newest terminal artifact wins:
   - newest terminal is an escalation -> exactly one actionable parked row;
   - newest terminal is an action -> no actionable row for that PR.
   This must make a later judged/released run supersede earlier parked attempts.
4. Preserve per-run reduction before subject reduction: a later action in the same
   run still resolves that run as today.
5. Never silently present a subject-less legacy run as an actionable PR. Keep truly
   unattributable parked runs in a separate JSON collection/count so they remain
   inspectable without inflating the main "needs attention" count.
6. Add an explicit live-read mode for the attention surface. It may query GitHub
   read-only to retain OPEN PRs and remove MERGED/CLOSED PRs. Lookup failure must
   fail visible and keep the row as `unknown` (never silently drop it). Keep the
   default non-live projection deterministic and offline.
7. Keep existing JSON fields compatible; new fields must be additive. Text output
   should lead with `repo#number` when known and keep the run ID as diagnostic
   detail.

### Console docket

1. Render each actionable item with a prominent clickable `repo#number` link to
   the PR. Render title when available.
2. Make the run ID secondary, while preserving case-file navigation and copyable
   judge/explain commands.
3. Show the main count from actionable PR-backed rows only.
4. If unattributed legacy rows exist, show them in a visually secondary, collapsed
   diagnostic section with case-file links. Do not mix them into "needs attention."
5. Preserve escaping, loopback/host-pin/CSP posture, and read-only routes.

## Tests

- Gate projection tests cover:
  - two parked runs for one PR collapse to the newest;
  - an action in a newer run suppresses an older parked run for the same PR;
  - subject recovery from legacy verdict/evidence shapes;
  - title, head SHA, and canonical PR URL projection;
  - a truly unattributed escalation is separated from actionable parked rows;
  - deterministic ordering for multiple PRs.
- Console tests/UI contract cover clickable PR links, title rendering, secondary
  run IDs, and the separated legacy section.
- Run `gofmt -l .`, `go vet ./...`, `golangci-lint run ./...`, and `go test ./...`.
- Dogfood the built `gate next -json` against
  `C:/Users/MichaelHabib/pers/gate/state`; confirm already-resolved duplicate PRs
  disappear while genuinely latest parked PRs remain.

## Scope walls

- No judge or mint HTTP endpoints in this PR.
- No mutation, compaction, or deletion of the gate audit log.
- No GitHub mutation. Live PR-state reads stay in gate; console only requests and
  renders gate's live projection.
- Do not fold in the four non-blocking console-v0 review observations unless a
  touched line requires a mechanical adjustment.

## Follow-on

Phase 4 (judge) and phase 5 (mint) remain separate authority-bearing changes.
Before implementation, turn the existing brainstorm requirements into one reviewed
spec per phase: judge first (CSRF + required rationale + exact argv consent echo),
then mint (human-only surface + two-step confirmation + tier/TTL/absolute-expiry).
