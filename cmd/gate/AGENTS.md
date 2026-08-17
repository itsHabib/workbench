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

Constraints that are design decisions, not omissions:

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
- **Thread disposition prepares, it never resolves.** `gate threads -repo R
  -pr N` reads a PR's unresolved review threads and, for each, looks for the
  commit after the thread's anchor that changes the reviewed file *and* carries
  a test change. Only that pairing produces a disposition — an exact-commit +
  evidence resolve comment plus the exact resolve call, for a human to run. A
  missing fix commit, a fix with no accompanying test, or an anchor outside the
  PR's history all report the thread still-actionable: a false "this was fixed"
  buries a live finding, a false "still actionable" costs a look. A DELETED test
  file is coverage removed, never coverage added. The head is read on both sides
  of the sweep and the run is abandoned if it moved, so a stamped comment never
  cites history its head does not contain. Like `explain` and `next` it is
  read-only — no artifact, no state write, exit 0 or 4 only.
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
