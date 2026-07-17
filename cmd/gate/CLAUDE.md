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

Constraints that are design decisions, not omissions:

- **State is the only channel.** Verifiers, the judge, `explain`, and `audit`
  read artifacts from the log — never side channels, process memory, or path
  conventions.
- **The ladder law lives in code.** Local producers can never block, judgment
  cannot override a code block, tiers compose monotone-max, unknown values
  fail closed. These are reducer errors and pinned tests, not conventions.
- **The verdict vocabulary is `contracts`.** `verify`'s
  Verdict/Producer/Subject/Finding are aliases of the shared contract types;
  the reducer, the ladder law, and all tier logic stay here — decisions never
  live in the contract.
- **State and keys live outside the repo.** The migration was code-only: a
  running gate's `-state` and `-key` dirs are operational data on the
  operator's machine, never files in this tree.
