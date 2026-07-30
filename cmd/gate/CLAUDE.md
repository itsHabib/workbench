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
  judgment. The narrow authenticated Codex reviewed-commit sentinel is the
  existing structured exception.
- **State and keys live outside the source tree.** A running gate's `-state`
  and `-key` dirs are operational data, never files in this source tree. The
  hosted executor uses a fresh Workbench-only ledger, never the machine-global
  Gate ledger. Generic Actions has no `gate-state` write authority; the Gate
  App is the sole state writer. Signing keys remain protected-environment
  secrets, and the operator-owned `GATE_EXECUTOR_ARMED` variable is the final
  release switch.
- **Execution authority is a durable PR claim, not status.** The protected
  executor contract verifies run-specific independent approval, exact
  repo/PR/head/base, newest action, and unchanged `--match-head-commit` argv.
  The one-App custody/order amendment, one-time exact-action bootstrap, and
  claim-only expired reconciliation path are implemented pending exact-head
  review, operator bootstrap, and live canaries. The reconciler has no merge
  operation, although its one-App
  `contents: write` token remains technically merge-capable. Neither path
  posts reusable green status or adds `--admin`.
