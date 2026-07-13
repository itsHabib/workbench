# workbench

The home for the Go agentic-infra family — one repo, one Go module. Tools live
side by side and **share contracts, not call stacks**: they compose at runtime
through artifacts (exit codes + JSONL on disk), and share only *types and
schemas* in-process — never one another's decision code.

Read `docs/DESIGN.md` first — it is the charter: the single-module decision and
why, what's in and out, the boundary law, the lazy-migration policy, and the
triggers that would later split `contracts` into its own module.

## Map

- `contracts/` — the shared vocabulary: the verdict schema + Go types every
  verifier emits, and the artifact envelope every producer writes. A **leaf**
  package that imports nothing else in the module and carries no decision logic.
  This is the debt payment — one source of truth instead of a parser per tool.
- `local/` — the shared local-model mechanism: structured Ollama calls + an
  escalate-on-uncertainty gate. A top-level *mechanism* package — carries no
  tool's decision logic, leaf-checked like `contracts` (may import at most
  `contracts`). See `local/README.md` for the eval verdicts and the
  when-to-route-local rule. Its CLIs live at `cmd/local` and `cmd/eval`.
- `cmd/<tool>/` — one binary per tool; its guts stay private under
  `cmd/<tool>/internal/`. Each tool keeps its own `CLAUDE.md` + `docs/DESIGN.md`.
  Today: `flare` (the escalation-routing plane), plus `local`'s CLIs (`local`,
  `eval`).
- `docs/DESIGN.md` — the repo charter. `FOLLOWUPS.md` — the lazy-migration queue
  and deferred decisions.

## The one rule

A tool may share **types and schemas** through `contracts`. A tool may **not**
import another tool's decision logic. When a tool needs another tool's *output*,
it reads an artifact. CI's `hygiene` job enforces this — it is not a convention.

<!-- BEGIN eng-philo (managed by /eng-philo — re-run to refresh; hand-edits inside this block will be overwritten) -->
## Engineering principles

How code is written here — Dave Cheney lineage ([Practical Go](https://dave.cheney.net/practical-go)): simplicity, clarity, line-of-sight. Apply on every change; the lint below catches the slips.

1. **No `else` — line-of-sight.** Handle errors / edge cases with early returns and guard clauses; keep the happy path un-indented, flowing down the left margin. Reaching for `else` → return early instead.
2. **Shallow nesting — ≤2 levels *per scope*.** A `for` + an `if` is the ceiling in one scope. The budget is per-scope, not per-function — a closure / anon fn is its own scope, so a `for`+`if` inside a closure is fine. Deeper in one scope → extract a function.
3. **Policy vs mechanism.** Separate the decisions (policy: validation, state machines, business rules) from the plumbing (mechanism: persistence, transport, I/O). Mechanism is dumb and swappable; policy lives in a layer above it. Never let policy leak into a mechanism layer.
4. **Composition of single-responsibility layers.** Each layer / package owns ~one responsibility; the app is a *composition* of them; any piece is swappable without rippling into the others. Dependencies flow one direction.
5. **Small, sharp APIs.** Export the least callers need. Intention-revealing names. Accept the narrowest input, return concrete types. Make the zero value useful.
6. **Errors are values; simplicity over cleverness.** Handle or propagate errors explicitly — never swallow. Readable > clever > short. A little copying beats a premature abstraction or dependency.

### Go idioms + enforcement

Accept interfaces, return structs; small interfaces (1–2 methods); errors lowercase + wrapped (`%w`); early-return / line-of-sight.

*Enforce:* golangci-lint — `gocognit`, `nestif`, `cyclop`, `revive`.
<!-- END eng-philo -->

## Checks

```
gofmt -l . && go vet ./...
golangci-lint run ./...
go test ./...
```

CI (`.github/workflows/ci.yml`) additionally runs `go test -race` and the
`hygiene` boundary-law assertions. Standard library only; no third-party
dependencies in production. Exception: `cmd/controlroom/e2e` may use an exact,
lockfile-pinned Playwright version as a test-only Node dependency; it is never
linked into a production binary.

<!-- local-offload:start -->
## Local-first offload

Before spending cloud tokens on a mechanical sub-step, check for a free local path (needs the `local` CLI / Ollama on this machine):

- Narrowing a big file list, extracting structure from noisy tool output, shallow classification -> `/offload`
- "Have we solved/decided this before?" questions about the operator's own work -> `/ask-portfolio`
- Triaging a PR's bot-comment pile -> `/review-digest <PR#>`

Deep judgment (code review, risk calls, dense-diff reasoning) stays with the primary model. If `local` is not on PATH, skip silently -- never block on this.
<!-- local-offload:end -->
