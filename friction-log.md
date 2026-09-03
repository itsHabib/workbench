# Foreign-agent friction log — Runway Rooms adapter

Agent: Codex. Started: 2026-07-18 (America/Los_Angeles).

## Worked as documented

- **Repo orientation.** Read `docs/DESIGN.md`, the parent `CLAUDE.md`, and the
  repo `CLAUDE.md`. The single-module boundary law and canonical checks were
  present and unambiguous.
- **Rooms prerequisite checkout.** The checkout at `~/dev/rooms` existed and
  contained the released `rooms run --lifecycle` implementation, its lifecycle
  spec, and host-gated tests. The event vocabulary in the kickoff matches the
  implementation.
- **Git baseline.** `git fetch origin --prune` succeeded; local `main` and
  `origin/main` were identical. Creating `runway-rooms-adapter` from that base
  succeeded without touching the pre-existing untracked planning files.
- **Dossier protocol.** The installed `dossier` executable serves standard MCP
  over stdio. After locating the corpus, a normal MCP `task.claim` call claimed
  `tsk_01KX7652WS78ZN6S7H4CW5HWDG` for `codex:michael`.

## Friction

### Kickoff points at sections that do not exist

- **What I tried:** read `cmd/runway/docs/DESIGN.md` §6 and §9, as directed by
  `kickoff-codex-runway-adapter.md`.
- **What happened:** that file is a short Phase-1 design note with no numbered
  sections. It links to `docs/features/execution-runtime/spec.md`, where §6 and
  §9 actually live. `origin/main` has the same mismatch.
- **Class:** `doc-lie`.
- **Smallest fix:** change the kickoff's source-of-truth path to
  `docs/features/execution-runtime/spec.md`; keep `cmd/runway/docs/DESIGN.md` as
  the implementation note it currently is.

### Foreign-agent task claiming is possible but not discoverable

- **What I tried:** followed the parent/repo `CLAUDE.md` files, ran
  `dossier --help`, and inspected the installed one-shot subcommands.
- **What happened:** neither CLAUDE file names the dossier corpus, the
  `DOSSIER_CORPUS` environment variable is unset, and the one-shot CLI exposes
  list/update/complete but not claim. A filesystem search found
  `~/dev/dossier-state/.dossier`; source/docs then showed that `task.claim` is
  available only through the MCP server. Sending the standard MCP initialize +
  `tools/call` exchange to `dossier serve --corpus ~/dev/dossier-state`
  succeeded.
- **Class:** `discoverability`.
- **Smallest fix:** document the canonical corpus path and a client-neutral MCP
  invocation in `~/dev/CLAUDE.md`, or add a `dossier task_claim` one-shot
  command alongside `task_update` and `task_complete`.

### The locked TDD does not define the `agent-cursor` profile resolution

- **What I tried:** read the Runway TDD, Rooms lifecycle/runner contracts,
  Rooms CLI flags, Ship's existing `RoomCursorRunner`, and queried the local
  portfolio RAG for a newer decision.
- **What happened:** the TDD requires a resolved `agent-cursor` profile and
  pins only the SendEnv secret allowlist. It does not define where image/model
  configuration comes from or how a portable command/task input becomes the
  `--runner cursor` flags. The local RAG returned 0.60 confidence and explicitly
  found no exact profile contract; its nearest source was
  `rooms/docs/features/poc-m4-anthropic-curl/spec.md`, which only establishes
  the substrate-level `--command <STRING>` convention.
- **Class:** `genuine-gap`.
- **Smallest fix:** add one profile table to the Runway TDD naming the config
  source, required work/input shape, exact Rooms argv, enforced receipt fields,
  and secret allowlist. Until then the adapter has to make and document that
  decision locally.

### The canonical test command has a Windows-only takeover-race flake

- **What I tried:** ran the required uncached suite with
  `go test -count=1 ./...` after the focused adapter tests passed.
- **What happened:** `TestClaimTakeoverRace_OneWinner` failed once because
  Windows rejected the takeover-file rename while another contender still had
  the destination open: `The process cannot access the file because it is
  being used by another process.` The adapter/controller packages passed in
  that run. `go test -count=1 ./cmd/runway/internal/claim` immediately passed,
  followed by a green `go test ./...`.
- **Class:** `flaky-test`.
- **Smallest fix:** make the Windows takeover publication primitive tolerate
  the expected sharing violation (bounded retry plus identity recheck), then
  run this race test repeatedly on a Windows CI lane.

### The GitHub review-thread helper assumes a UTF-8 locale

- **What I tried:** ran the `gh-address-comments` skill's bundled
  `scripts/fetch_comments.py` with the workspace's bundled Python after the
  three bot reviews arrived.
- **What happened:** on Windows it decoded `gh api graphql` output as cp1252
  and crashed on the bots' Unicode review text with
  `UnicodeDecodeError: 'charmap' codec can't decode byte 0x8f`, followed by
  `TypeError: the JSON object must be str, bytes or bytearray, not NoneType`.
  Re-running with `PYTHONUTF8=1` succeeded and returned full thread state.
- **Class:** `tool-bug`.
- **Smallest fix:** make the helper's subprocess text reads explicitly
  `encoding="utf-8"` (and fail on the decode error before calling
  `json.loads`) so its behavior does not depend on the host locale.

## Implementation and validation

- The adapter stayed behind Runway's private backend registry; provider names
  did not enter the portable contracts or controller policy. It consumes the
  Rooms lifecycle stream across startup, workload, collection, and cleanup,
  and durable reconcile fails closed unless `rooms ls --json` proves the
  allocation absent under the known schema.
- The hermetic CLI double exercised success, pool exhaustion, collection and
  cleanup failures, context cancellation, durable recovery, and secret
  handling without requiring KVM or an installed Rooms binary. The separate
  `rooms_host` build-tag test compiles normally and runs only when explicitly
  enabled on the target host.
- Green checks: `gofmt -l .`, `go vet ./...`, `golangci-lint run ./...`,
  `go test ./...`, `go build ./...`,
  `go run ./cmd/tracelens eval ./cmd/tracelens/testdata/corpus`, and
  `go test -tags rooms_host ./cmd/runway/internal/backend/rooms`. Local
  `go test -race` is unavailable because this Windows Go environment has CGO
  disabled (`go: -race requires cgo`); the Linux CI race lane remains the
  authoritative run.

## Top three fixes

1. **Lock the `agent-cursor` profile table in the Runway TDD.** This removes
   the only substantive policy guess: image/model resolution, task mapping,
   receipt assertions, and secret names should be reviewable in one place.
2. **Make the kickoff self-contained and accurate.** Correct the §6/§9 path
   and include the dossier corpus/claim invocation so a foreign agent does not
   need repository archaeology before starting the requested work.
3. **Harden and continuously exercise claim takeover on Windows.** The
   canonical local check should be trustworthy on the operator's primary OS;
   a known sharing-violation race should not force humans to distinguish a
   product regression from an infrastructure retry.

## Verdict

The repository's architectural charter and Rooms lifecycle contract were
strong enough to support a clean adapter without importing decision code
across tools. The kickoff was less reliable than the code: it cited the wrong
design document and omitted both task-claim discovery and the one unresolved
profile decision. Tests gave good leverage once implementation began, though
the Windows takeover flake weakens confidence in the canonical one-command
check. Fixing the profile table and kickoff path would remove most of the
foreign-agent tax; hardening the Windows claim race would remove the remaining
false alarm.

## Delivery record

- Opened [workbench PR #68](https://github.com/itsHabib/workbench/pull/68)
  from `runway-rooms-adapter` to `main`, then marked it ready after the initial
  CI run passed. Both `check` (including the Linux race lane) and `hygiene`
  were green before and after the review patch.
- Requested Copilot and posted `@codex review` plus `@claude review`. Copilot's
  four inline findings and Claude's six actionable suggestions were all
  implemented. Codex raised two findings: context-cancellable image hashing
  was implemented; the claim that `pool_full` lacks `room_id` was rejected
  against current Rooms source, which mints the ID and binds the lifecycle
  writer before attempting the slot claim. Every inline thread was answered
  and resolved.
- Consulted Gate without creating or supplying a grant:
  `gate gate -repo itsHabib/workbench -pr 68 -state
  C:\Users\<you>\dev\gate\state`. It refused with exit code 4 and
  `gate: gate: -repo, -pr, -grant required`, as required by the kickoff's
  no-grant boundary.
- No grant was minted, no merge was attempted, and nothing was pushed to
  `main`.

---

# Friction log — Map docs PR (#214) merge session

Agent: Claude (Opus 5). Started: 2026-08-05 (America/Los_Angeles).

## Worked as documented

- **The delivery loop.** Worktree off `origin/main`, docs-only change, canonical
  checks, PR #214, `@codex review` + `@claude review` per `.ship.json`
  (`expected=2`). All three CI checks green; both reviewers cleared the exact
  head with zero actionable findings on the first cycle.
- **The mint boundary.** The agent stopped at the grant handoff as documented;
  the operator minted `grt_28cb0dbdbe48a39b` and the read-only `gate next` view
  surfaced it without prompting.

## Friction

### Ollama being down turns a clean T0 PR into a parked run

- **What I tried:** `gate gate -repo itsHabib/workbench -pr 214 -grant
  grt_28cb0dbdbe48a39b` (a valid T2 grant), on a two-file docs PR (+8/−2) with
  green CI and both required reviewers completed at the exact head.
- **What happened:** parked (exit 2, `run_4bb803ea7c990b90`). Three of four
  verifiers passed; the only escalation was `review-consolidation`, whose
  local-model extraction failed with `ollama: ... connection refused` — the
  daemon wasn't running. The escalation-brief synthesis failed the same way.
  The one comment it did parse it misread, taking Claude's "Reviewed commit"
  blob hashes as the verdict and labelling it `unknown`. Nothing about the
  review content needed judgment; the park was purely an infra outage
  surfacing as "needs judgment".
- **Class:** `infra-outage`.
- **Smallest fix:** run ollama as a supervised service (`brew services start
  ollama` — done this session, auto-starts on reboot). Better: when the local
  model is unreachable, review-consolidation should say so in its verdict
  (`local_model_unavailable`) instead of dressing an outage as a judgment
  question — the operator's remedy differs completely.

### Gate opens and parks runs on already-merged PRs

- **What I tried:** the same `gate gate` invocation — unknown to the agent,
  PR #214 had already been merged out of band hours earlier (the squash
  commit `dffa508` is stamped 2026-08-04 17:17 −0700).
- **What happened:** gate's readiness verifier read back `state=MERGED` and
  *passed*, the ladder ran, and the run parked for judgment. The park is
  unresolvable: a merged PR has no merge left to authorize, and judging it
  pass would stamp one-shot merge authorization for a merge that already
  happened — manufactured audit evidence. The run was left parked and
  unjudged.
- **Class:** `tool-bug`.
- **Smallest fix:** short-circuit in `gate gate` when the readback shows the
  PR merged — refuse (exit 3, reason `already_merged`, naming the merge
  commit) before the verifier ladder runs. Chipped as its own task; landed as
  PR #219, which also widens exit 3's contract definition (formerly
  `capability_refused` alone) to name `already_merged` as a refusal, so the
  exit-code table and this remedy agree.

---

## 2026-08-23 — a dirty main checkout read as live work

- **What I tried:** act on a peer session's report that
  `~/dev/workbench` carried "uncommitted changes that no session is claiming"
  in `cmd/gate/internal/verify/`.
- **What happened:** none of it was live. The reflog showed no branch activity
  since 2026-08-07 and the files were last written 2026-08-16. Every tracked
  modification's distinctive content was already on `main` (landed via #235).
  `readiness_panel_test.go` and `docs/features/tier-aware-panel/` only *looked*
  untracked because `HEAD` was an Aug-7 commit that predated them. Five
  "hackathon" docs were byte-identical to `~/dev/bakeoff/token-opt-08-09/*/BRIEF.md`.
  The checkout was 18,496 lines behind `main` across 147 files.
- **Cost:** two sessions. One reported it as in-flight work; one spent a
  significant part of a turn proving it was not.
- **Class:** `stale-state`.
- **Fix landed:** `CLAUDE.md` now states that the main checkout stays on `main`
  and work happens in worktrees, with the two commands to verify it. The
  checkout was reset and the genuinely-local content — five paths that existed
  nowhere else — preserved on
  `archive/main-checkout-leftovers-2026-08-23` before the reset, so nothing
  was destroyed. 33 local branches whose PRs were merged or closed were removed
  at the same time.
- **What would have caught it sooner:** `git status --short` on the repo root
  at session start. It is two seconds and it distinguishes "someone is working
  here" from "someone left".

## 2026-08-29 — the notification plane was eight days stale, and nobody could tell

- **What I tried:** find why the operator's Slack was "just noise" — two
  identical "Don't merge ivy#50" cards a minute apart, and a parked ivy#51
  still showing a live Approve button hours after it was judged.
- **What happened:** two independent faults, and the second hid the first.
  Routing paged every rung of the verifier ladder — the reducer's fold *and*
  every component verdict it folded, which restate the fold's `why` verbatim.
  396 delivered cards were 88 escalations and 308 restatements. Separately,
  `~/go/bin/flare` was built 2026-08-21, so #251's close-a-park's-card-on-
  resolve had never actually run on this machine: the journal contained
  `delivered` and `error` records and not one `card-update`, ever.
- **Cost:** unmeasurable but ongoing — the operator stopped reading the
  channel, which is the only failure mode a notification plane really has.
- **Class:** `stale-install` + `noisy-signal`.
- **Fix landed:** `flare` reinstalled from `main` (card-closing now live);
  routes tightened to page escalations and blocks and drop the restatements;
  `dimension` added as a route selector (#271) so a routes file can say "page
  the fold, not its parts", with the shipped example fixed at the same time.
- **What would have caught it sooner:** nothing on this machine compares the
  installed binary against `main`. A merged fix that never reaches the
  operator's `$GOBIN` is indistinguishable from a fix that does not work —
  and flare's own `status` reports `healthy: true` either way, because it
  measures polling, not whether the binary is current. The tools that ship
  *behavior* to a long-running local daemon need a staleness check that
  `status` surfaces; `#251` was merged, tested, and inert for eight days.

# Friction log — runway reconcile read-window fix (#259)

Agent: Claude (Opus 5). Started: 2026-08-23 (America/Los_Angeles).

## Worked as documented

- **The delivery loop.** Worktree, root-cause fix in
  `cmd/runway/internal/controller`, canonical checks plus `-race`, PR #259,
  `@claude please review` per `.ship.json` (`require=[claude]`). All three CI
  checks green on the reviewed head; the reviewer cleared it with zero blocking
  findings on the first cycle.
- **The mint boundary.** The agent stopped at the grant handoff as documented;
  the operator minted `grt_ad5944b6b4ff7aaa` (T2, `max-cycles 3`).
- **`gate judge` is genuinely one-shot.** An escalation already carried a
  judgment; the duplicate `-auto` call refused with `judgment_duplicate` and
  named the existing `jdg_6bd4a74481c44b05` rather than stamping a second
  authorization. The refusal is the feature working.

## Friction

### Ollama cold-start timeout parks a run even with the daemon supervised

Second occurrence of the `#214` entry above, one failure mode further in.

- **What I tried:** `gate gate -repo itsHabib/workbench -pr 259 -grant
  grt_ad5944b6b4ff7aaa` on a reviewed, CI-green T1 PR at the exact head.
- **What happened:** parked (exit 2, `run_3ab398ccb6bc3d89`). `triage-floor`
  and `up-to-date` passed; `review-consolidation` escalated because its
  extraction failed with `ollama: ... context deadline exceeded (Client.Timeout
  exceeded while awaiting headers)`. The escalation-brief synthesis failed the
  same way. **The daemon was up** — `brew services` from the #214 session, with
  `qwen2.5:7b` present — so this is not the outage that entry describes: a cold
  load of a 4.7 GB model outruns the three-minute client timeout hard-coded at
  `local/local.go:28` (a package-level `var`, no flag, env var, or injection
  point), and the first run after the model is evicted from memory parks
  regardless of daemon health.
- **Class:** `infra-timeout`.
- **Smallest fix:** the #214 remedy still stands and is still unimplemented —
  review-consolidation should report `local_model_unavailable` rather than
  dressing an infra failure as a judgment question; a failed extraction is not
  a finding, and the operator's remedy differs completely. Supervising the
  daemon does not close this: pre-warm the model before the ladder runs, or
  make that timeout injectable so a cold first call can be given more room.

### A review request carrying context does not attest, so every PR burns a judgment

- **What I tried:** the same `gate gate` invocation, with the required reviewer
  completed at the exact head and no blocking findings.
- **What happened:** `readiness` escalated (`no review decision reported by
  GitHub`) and `review-panel-completeness` escalated (`completed=0 expected=1
  missing=[claude]`), so the run parked and a one-shot judgment became the only
  exit. The same pair parked #248.

  The cause is not that the panel comments rather than approving — #235's
  `panelStandIn` already answers an absent GitHub review decision, and
  `readiness.go` only escalates when `decisionAbsent && !stand.satisfied`. It is
  that the panel never completed, and it never completed because the
  *attestation* step in `.github/workflows/claude.yml` did not fire. That step
  posts the `gate:review-attestation` sentinel `evidence/panel.go` looks for,
  and it validates the WHOLE comment body against
  `^@claude [please] review [this [pr]]$`. Every review request in this repo
  carries focus areas after the verb, so the step exits `not a review request —
  no attestation`, the panel stays `missing=[claude]`, the stand-in cannot be
  satisfied, and readiness escalates downstream of that.
- **Class:** `tool-gap`.
- **Smallest fix:** have the claude workflow post a formal GitHub review
  (`gh pr review`) alongside its comment. That gives GitHub a real review
  decision, which satisfies `readiness` directly and `latestExactHeadReview`
  for panel completeness, and it needs no heuristic to be loosened.

  **Not** the tempting fix: widening the regex to match `@claude review` as a
  *prefix* is precisely what the step's own comment warns against, since
  `@claude review permissions` and `@claude review the failing CI logs` start
  identically and must not attest. The precision-over-recall choice there is
  deliberate and documented; the recall should be recovered somewhere that
  cannot credit a review that never happened.

  Also **not** the fix I first proposed here: teaching `review-panel-v1` to
  count a panel member's bot comment as a completed review. `evidence/panel.go`
  refuses that by design — "prose is not authority here, and a verdict with no
  commit anchor cannot state which tree it applies to." The sentinel exists
  because counting bare prose was already considered and rejected.
- **Workaround available today:** post the bare `@claude please review` as its
  own comment so the attestation fires, and put the focus areas in a second
  comment. Costs nothing and keeps the panel complete.
