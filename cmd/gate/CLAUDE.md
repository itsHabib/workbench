# gate

One Go binary that decides whether a pull request may merge: gather
evidence, run the verifier ladder, compose verdicts monotonically, record the
outcome. Every step is an artifact in an append-only hash-chained log, so any
decision is reconstructable and auditable from state alone.

A workbench tenant: the binary lives at `cmd/gate`, its guts under
`cmd/gate/internal/`. The exit-code contract is a load-bearing seam — callers
(the driver merge tail, CI's status check) branch on it: **0 pass /
1 blocked / 2 parked / 3 refused / 4 error**. Keep it stable.

Read `docs/DESIGN.md` first — it defines the artifact contract, the verdict
schema, and the ladder law the code enforces structurally.

## Develop (from the module root)

```
go build -o gate.exe ./cmd/gate
go vet ./cmd/gate/...
golangci-lint run ./cmd/gate/...
go test ./cmd/gate/...
```

CI (`.github/workflows/ci.yml`) runs gofmt, vet, golangci-lint, `go test
-race`, and build module-wide; the `hygiene` job enforces the tenant boundary.
`.github/workflows/gate.yml` is gate's own dormant enforcement canary — see
`docs/enforcement.md`.

Known local quirk: `observe.TestExplainGolden` fails on a Windows checkout
(CRLF golden, no `.gitattributes`); it passes on Linux CI.

## Cloud model egress

`-model-backend cloud` uses the Anthropic-native Messages protocol. It honors
the standard provider variables and needs no gateway-specific configuration:

```bash
export ANTHROPIC_API_KEY=<provider-or-gateway-token>
export ANTHROPIC_BASE_URL=<gateway-origin-and-provider-prefix> # optional
export GATE_CLOUD_MODEL=<served-model-id>                       # required with a base URL
```

With `ANTHROPIC_BASE_URL` unset, gate uses the direct provider endpoint and its
existing default model. With it set, gate preserves the URL's path prefix,
appends `/v1/messages`, and requires `GATE_CLOUD_MODEL`; the gateway's served
catalogue may not contain the direct-provider default. Token acquisition is an
operator action. Gate reads all three values once at process construction and
never records the token or resolved endpoint in verdict artifacts or errors.

## Local model timeouts

The local rung (`review-consolidation`, the ci-classify advisory) calls ollama
once per item. `GATE_OLLAMA_TIMEOUT` bounds one round-trip and accepts a Go
duration (default 10m); anything unparseable or non-positive falls back to the
default. A call that does not complete is never reported as a low-confidence
extraction — the verdict escalates with `consolidation_unavailable` and names
the infrastructure fault, so a judge is not asked about findings that were
never read.

Constraints that are design decisions, not omissions:

- **Slack may mint only one exact T0 subject.** `gate -slack` fixes merge/T0,
  three cycles, ten minutes, one repo/PR/head. `grant-callback` accepts the
  original raw Slack body, independently verifies its HMAC, timestamp, and
  immutable-user allowlist, loads every scope field from the request artifact,
  re-reads the head, and records one mutually exclusive grant/denial terminal
  under the state lock. Flare and Escalate never receive a mint API. T1+ stays
  on the existing stronger authority paths.
- **A judgment names its decider.** Its body carries `decider` —
  `{who, method, at}` — where method is the CHANNEL the identity was established
  through (`cli-operator`, `slack-interactive`, `auto-<provider>`), never an
  authenticated claim. `judge` and `resolve` require `-who`; `-auto` derives its
  own (the resolved provider wrapper + model) and refuses a claimed one.
  Enforced on the write path only: readers stay tolerant so judgments predating
  the field still explain and re-reduce, and `explain` renders their absence as
  the literal `unattributed`.
- **Gate authorizes; it never merges.** An `action` says a merge was allowed; a
  `receipt` (`gate receipt -run <id>`, called by the executor AFTER the pinned
  command) says what landed, with the merge commit, actor, and time read back
  from GitHub — an independent clock, not the actor's own account. Classified
  head-to-head: a merge at a head the action never saw is `superseded`. One
  receipt per action, enforced by the store's absent-parent guard.
- **`reconcile` measures the negative.** `gate reconcile -repo R` reads the
  platform first and writes a `coverage` artifact: authorized-and-landed,
  authorized-never-landed, landed-without-authorization. Pre-adoption merges are
  classified apart (boundary derived from state — the first artifact naming the
  repo — unless `-effective-from` says otherwise) and never counted as bypasses.
  Basis is merged PRs; a direct push has no head to join by and belongs to branch
  protection. `audit` reports both anomalies as findings, exits 0 for them (a
  chain tamper is a fault; an incomplete record is not), and says UNMEASURED
  rather than zero before any reconcile.
- **A killed run must not strand a decision.** The judgment path resumes a
  persisted judgment instead of refusing it, `gate next` surfaces a
  judged-but-unauthorized run with the command that finishes it, and no GitHub
  call can precede the durable action — the stamp needs that artifact's chain
  hash, so the ordering is a precondition of the payload.
- **State is the only channel.** Verifiers, the provider-neutral judge,
  `explain`, and `audit`
  read artifacts from the log — never side channels, process memory, or path
  conventions.
- **Local auto-judgment has a closed provider set.** `judge -auto` accepts only
  `-provider claude|codex` and runs Gate's fixed, tool-disabled CLI projection
  from a fresh temporary working directory with a secret-stripped environment.
  A caller cannot supply an executable path or arguments; Gate records the
  resolved wrapper's SHA-256 digest. PATH, saved login, and the process identity
  remain same-user dependencies, so this is advisory automation; independent
  execution authority stays outside it.
- **The ladder law lives in code.** Local producers can never block, judgment
  cannot override a code block, tiers compose monotone-max, unknown values
  fail closed. These are reducer errors and pinned tests, not conventions.
- **The verdict vocabulary is `contracts`.** `verify`'s
  Verdict/Producer/Subject/Finding are aliases of the shared contract types;
  the reducer, the ladder law, and all tier logic stay here — decisions never
  live in the contract.
- **Panel completeness is exact-head evidence.** `ReviewPanelV1` records the
  repository-owned expected set and completed/pending/missing/unknown state.
  Its code verifier parks every incomplete state; findings remain a separate
  review-consolidation verdict. Provider prose and sticky issue comments are
  never authority: without a formal exact-head review or shared head-bound
  artifact the reviewer remains incomplete and Gate parks for provider-neutral
  judgment. Two narrow structured exceptions exist, both harness-emitted and
  exact-head: the authenticated Codex reviewed-commit sentinel, and a review
  attestation posted by the repository's own Actions token for a provider that
  publishes reviews only as issue comments (Claude). An attestation's authority
  is the workflow that checked out the head and ran the reviewer against it —
  never the review body, which a model writes. The one non-judged head Gate
  honours is a **diff-equivalent refresh**: when the PR's merge-base diff at an
  earlier reviewed head digests byte-identically to the judged head's, the
  evidence records a `ReviewPanelV1` `equivalence` and that head's reviews stay
  credited, so a conflict-free base refresh no longer parks the panel or burns a
  review cycle. Everything else on the ladder still runs against the judged head
  — CI is re-read, findings re-extracted, and any park still reaches a judge.
- **A complete panel is readiness's answer to an absent review decision.**
  GitHub populates `reviewDecision` only from submitted reviews, so a panel of
  comment-posting providers leaves it empty however completely it reviewed.
  Readiness reuses the panel rung's own completeness rule — one function, two
  callers, so the head binding (diff-equivalent refresh included) cannot drift —
  instead of parking for a judge to re-derive a fixed inference from facts
  already in state. An explicit non-approving `reviewDecision` still blocks
  outright — the panel answers only that field's ABSENCE. What the PANEL may
  stand in for is narrower still: a completed reviewer who asked for changes, or
  a human's or a bot's outstanding objection at any head, stops the panel
  standing in, and an incomplete, unknown, or stale-head panel escalates as
  before. The any-head clause is load-bearing: the panel rung's own change-
  request check is bound to the exact head, so a connector that re-completes a
  new head as COMMENTED would otherwise leave a formal CHANGES_REQUESTED
  standing against the superseded head with no rung holding it — the stance
  evidence is where it survives (`botChangeRequest`). It does **not**
  reorder the stand-ins ranked above it — an authoritative human's exact-head
  approval, and the reviews-optional flag in the enforced-check context, still
  carry readiness on their own warrant exactly as before, including over an
  objecting panel (`TestReadinessBotChangeRequestDoesNotSuppressHumanApproval`:
  bot findings are findings; authorization belongs to the account with
  repository authority). What is new there is only that the verdict records the
  objection such a pass stepped over. Mergeability, conflicts, and CI are
  untouched, and the verdict always names what carried readiness — the approval,
  the panel, or the flag.
- **Up-to-date protection is a preflight, not an afterthought.** Before the
  merge command is emitted, the ladder reads the base branch's up-to-date
  requirement from BOTH mechanisms that carry it — classic branch protection's
  `required_status_checks.strict` and a ruleset's
  `strict_required_status_checks_policy` — and unions them: a base can carry
  both, and GitHub enforces whichever requires more. A BEHIND head blocks only
  on a base that requires up-to-date-ness, naming the fix (refresh from base,
  let CI re-run, gate again). BEHIND alone never blocks: most bases require
  nothing, and a refresh there buys a wasted CI cycle. The negative answer is
  the demanding one — "nothing requires it" is a fact only when every mechanism
  was read, and only GitHub's specific `Branch not protected` response proves a
  branch carries no classic protection (a bare 404 is a missing repo, a mistyped
  branch, or a permissions problem). Anything unread degrades to the prior
  behaviour with the reason recorded in the verdict — an unread fact is not
  evidence and must never stop a merge.
- **Thread disposition observes, it never concludes.** `gate threads -repo R
  -pr N` lists a PR's unresolved review threads and, for each, the commits after
  its anchor that touch the reviewed file, flagging which of those also changed
  a test naming that file. It does NOT say whether a thread is fixed, does not
  prepare a resolve comment, and emits no resolve call. An earlier version did
  all three, and review found seven distinct ways for that claim to be wrong
  while reading exactly like a right one — a deleted test counted as coverage,
  an unrelated test counted as coverage, a test added then removed later, a
  truncated history, a rename, a reviewer's rebuttal posted after the candidate
  fix, a loosely-matched test path. The claim was removed rather than patched an
  eighth time: a false "this was fixed" buries a live finding, and nothing in
  the output distinguishes a wrong verdict from a right one. Like `explain` and
  `next` it is read-only — no artifact, no state write, exit 0 or 4 only.
- **The cycle ceiling is a pre-flight, not a post-mortem.** `gate gate` counts
  the PR's consumed review cycles from the log before it gathers any evidence
  and, when the run would land over the grant's `-max-cycles`, refuses at once
  — exit 3, `grant_cycle_exceeded`, a `grant_needed` record under the run —
  instead of doing the sweep and parking. The record is deliberately not an
  outcome: it burns no cycle and never supersedes a park already awaiting
  judgment for the same PR, which stays judgeable under its grant (a judgment
  spends no new cycle; a fresh run would). Every result carries `cycles_used`
  / `cycles_max`, and `next` and `explain` print `cycles N/M` on each
  awaiting-judgment line, so a driver sees the budget before acting. The
  ceiling in `act` remains as the backstop a judgment cannot launder.
- **The inbox is closed by supersession and by mootness, and `sweep` is what
  records the second.** A park is settled when a NEWER terminal for the same
  `repo#PR` displaces it, or when the pull request itself is no longer open.
  Both classes are derived — never deleted — by one subject-scoped reduction in
  `observe/closure.go` that the parked projection, the ready-to-merge
  projection, and `audit` all consume. The counts always project; `-all` shows
  the rows. A discharged park carries no `judge`/`resolve` command, so a
  one-shot judgment cannot be spent on a settled question.
  Mootness needs a fact the log could not previously hold: every action gate
  writes is `dry_run` / `would_merge`, because gate authorizes and an executor
  acts, so a landed PR left its row standing forever. `gate sweep` records that
  fact as a `subject_closed` artifact — provenance, outside the
  action/escalation families, so it burns no cycle and authorizes nothing. It
  reuses the open-PR seam `next -live` and `preflight` already share, and
  records only what that seam proves: `not_open`. Which commit landed, when, and
  by whom is `receipt`/`reconcile`'s claim, read back from the platform; a sweep
  must never fabricate it. `sweep` writes and therefore is NOT folded into
  `next`: observability views stay read-only, and `next -json` is on `escalate
  serve`'s Slack path under a hard budget where per-repo subprocesses would
  strand an interaction. An unread repo is UNKNOWN, never closed — assuming
  closure on a failed read would delete the queue on a network blip.
- **`audit` reports park discharge as health, never as integrity.** It prints
  the by-judgment / by-supersession ratio after the chain verifies and never
  changes the exit code. A park discharged by judgment is the loop working; one
  discharged by supersession is a question gate asked that a later run overtook
  before anyone answered — a churn signal for the review cycles upstream of the
  gate. A metric that could fail an audit would train the reader to ignore audit
  failures.
- **A run that decides nothing spends nothing.** Cycles are counted from
  OUTCOMES — one distinct run holding a counting action or escalation, joined
  to its subject through the reduced verdict — never from the evidence a run
  recorded. A run that dies mid-sweep (a reset connection under `gh pr diff`,
  after `gh pr view` already landed its artifact) burns no cycle: it appends a
  `run_aborted` record naming the subject and the cause, outside the
  action/escalation families like `grant_needed`, so the count, the reducer,
  and `next`'s subject reduction all ignore it. The evidence it recorded stays
  — an append-only log says "this died" by appending, never by un-writing —
  and `explain` shows why it stopped instead of leaving it indistinguishable
  from a run still in flight.
- **Evidence reads retry on an allowlist, and still fail closed.** Each `gh`
  call retries up to three times with growing backoff on transport faults and
  GitHub's retryable statuses; anything unrecognized (missing binary, bad
  credential, 404, malformed query) fails on the first attempt instead of
  sleeping through the bound. Retrying is safe only because these calls are
  reads, and it must never become a way to proceed without an answer — a
  failure that outlives the bound is returned unchanged and the run aborts.
- **State and keys live outside the source tree.** A running gate's `-state`
  and `-key` dirs are operational data, never files in this source tree. The
  hosted executor uses a fresh Workbench-only ledger, never the machine-global
  Gate ledger. Generic Actions has no `gate-state` write authority; the Gate
  App is the sole state writer. Signing keys remain protected-environment
  secrets, and the operator-owned `GATE_EXECUTOR_ARMED` variable is the final
  release switch.
- **Execution authority is a durable PR claim, not status.** The protected
  executor contract verifies run-specific independent approval, exact
  repo/PR/head/base, newest action, and the canonical commit-pinned merge
  intent before Gate performs the exact-head GitHub API call.
  The one-App custody/order amendment, one-time exact-action bootstrap, and
  claim-only expired reconciliation path are implemented pending exact-head
  review, operator bootstrap, and live canaries. The reconciler has no merge
  operation, although its one-App
  `contents: write` token remains technically merge-capable. Neither path
  posts reusable green status or adds `--admin`.
