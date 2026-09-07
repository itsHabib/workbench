# Operational report

`fleet report` prints the last 24 hours of local observations. Use
`fleet report --since 48h` for another positive Go duration. The command only
reads files; it does not migrate keys, sync remote data, revive a watcher, or
write any state. Each watcher tick also publishes `watch/report.md` for 24 hours.

The report answers:

- Every recorded real refusal and the same harness/session's next event.
  Identical tool/input/cwd fingerprints identify repetition. A different cwd
  with an allowed next tool call shows work elsewhere. Stop/end shows idle/end.
  Other next events and missing next events stay unknown; repetition cannot
  establish whether the agent understood the refusal.
- Each attention transition and time until the next matching committed
  dispatch, reassign or revoke, or until attention clears without one. Matching
  uses repository/change/relationship, or a named seat. These are recorded
  actions, not claims about the actor's identity.
- Unique rows observed undeclared on each UTC day. The watcher seeds current
  row snapshots on upgrade and retains a daily snapshot when rows do not change.
  Unobserved days are not inferred to have zero rows.
- Dispatch, first observed hands, and matching completion receipt per dispatch
  epoch. Lateness is grouped by last recorded accountable role; this is not a
  reconstruction of historical blame. Retired/superseded epochs stop accruing
  delay. Watcher timestamps are observation bounds, not exact acquisition times.
- Automatic branch replacements of dead holders. Successfully unwound adapter
  replacements are excluded. A later tool refusal does not necessarily undo a
  lease replacement and is not treated as doing so.
- Prompt events with capped board lines and total lines exceeding the existing
  700-byte cap.
- Per-harness/event count, nearest-rank p50/p95/p99 and maximum hook evaluation
  latency. File append and watcher revival are outside this timer.

## Records and coverage

Real and shadow hooks now append to `events.jsonl`, preserving the original
shadow verdict shape and adding `shadow`, `cwd`, a SHA-256 tool-input fingerprint,
`prompt_truncated`, and `takeovers`. Raw tool input is not persisted. Existing
bounded verdict reason/context excerpts remain. Shadow records never count as
real behavior. `shadow-report` still combines legacy `shadow.jsonl` with only the
shadow-tagged records from `events.jsonl`; historical files need not be renamed.

`actions.jsonl` is a passive journal of successfully committed verb mutations.
The watcher adds repository identity and the observed work row to
`watch/observed.jsonl`. No new agent declarations or role vocabulary are needed.
Telemetry is not consulted by authorization, and append failures do not change
hook or verb outcomes.

Missing/unreadable logs and malformed records are reported. The first retained
real hook timestamp marks available hook coverage. Earlier real refusals, missed
watcher intervals and deleted pre-upgrade rows cannot be reconstructed from
mutable current files. Process death between mutation and telemetry append can
also leave a gap. The report describes retained observations, not an audit-grade
exactly-once ledger. It contains local telemetry only.

`install.sh --apply` removes the exact installed binary's Claude/Codex shadow
commands while retaining unrelated hooks. Config backups remain the rollback
mechanism. An already-running watcher must be restarted to use a newly installed
binary. Windows cross-compilation is not Windows runtime verification.
