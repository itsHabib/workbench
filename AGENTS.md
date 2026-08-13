# workbench

The home for the Go agentic-infra family — one repo, one Go module. Tools live
side by side and **share contracts, not call stacks**: they compose at runtime
through artifacts (exit codes + JSONL on disk), and share only *types and
schemas* in-process — never one another's decision code.

Read `docs/DESIGN.md` first — it is the charter: the single-module decision and
why, what's in and out, the boundary law, the lazy-migration policy, and the
triggers that would later split `contracts` into its own module.

New to the system as a whole (not just the module boundary)? `docs/workbench-101.md`
is the teaching doc: the full methodology top to bottom (why it exists, the loop,
the five planes, gate as the flagship, where it is going), opening with a one-screen
Orientation block you can point an agent at to ground it fast.

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
  `cmd/<tool>/internal/`. Each tool keeps byte-identical scoped guidance in
  `cmd/<tool>/CLAUDE.md` and `cmd/<tool>/AGENTS.md`, plus `docs/DESIGN.md`.
  CI requires the guide pair to stay synchronized so either harness discovers
  the same exit codes, invariants, and checks.
  Today: `flare` (the escalation/block routing sink — an Observability tool, not
  a plane), `tracelens` (agent trace
  diagnostics — consumed via its CLI exit-code seam, never as a Go import),
  `triage` (PR risk floor + escalate-only advisory; two binaries,
  `triage-floor` / `triage-advisory`, sharing one `cmd/triage/internal/`),
  `gate` (the merge-authorization boundary — grants, the verifier ladder, the
  hash-chained decision log; exit codes 0 pass / 1 blocked / 2 parked /
  3 refused / 4 error are a load-bearing seam),
  plus `local`'s CLIs (`local`, `eval`).
- `docs/DESIGN.md` — the repo charter. `FOLLOWUPS.md` — the lazy-migration queue
  and deferred decisions (the engineering debt this codebase owes).
  `friction-log.md` — where this repo's tooling and docs failed an agent working
  in it; `/health` reads it for the cross-repo rollup, so tooling friction goes
  there, not in FOLLOWUPS.

## The one rule

A tool may share **types and schemas** through `contracts`. A tool may **not**
import another tool's decision logic. When a tool needs another tool's *output*,
it reads an artifact. CI's `hygiene` job enforces this — it is not a convention.

## Review-cycle discipline

Per PR, at most **two fix-rounds** against the review panel: fix every
verified finding at P1 or higher and anything touching authorization
invariants, push once, re-trigger the panel once — then one more round
at the same bar. After round two, STOP fixing. Residual P2s and nits go
to the judge with a written why — a judgment can accept
verified-addressed-but-unretracted threads and recorded deferrals (a
FOLLOWUPS.md at the repo root). Reviewers generate second-order findings
on every new diff indefinitely, so "zero open findings" is a
non-terminating exit condition; the judge's residual acceptance is the
terminating one. The round cap caps panel re-triggers, not fixes: a
verified P1-or-higher surfaced by the final run still gets fixed, then
goes to the judge as verified-addressed-but-unretracted rather than
through a fourth panel round.

Two fix-rounds plus the initial panel run is three review cycles, which
is why review caps default to `max_cycles: 3`; `max_requests` caps total
panel re-triggers across the PR. These caps are the stop signal, not
friction — never respond to a ceiling park by asking for a wider grant.
A blown cap means the process looped; the fix is fewer rounds, not more
budget. Behavioral claims that reviewers keep re-litigating belong in
e2e tests asserted every CI run, not in review rounds.

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
`hygiene` boundary-law assertions. Third-party Go dependencies are allowed.

<!-- local-offload:start -->
## Local-first offload

Before spending cloud tokens on a mechanical sub-step, check for a free local path (needs the `local` CLI / Ollama on this machine):

- Narrowing a big file list, extracting structure from noisy tool output, shallow classification -> `/offload`
- "Have we solved/decided this before?" questions about the operator's own work -> `/ask-portfolio`
- Triaging a PR's bot-comment pile -> `/review-digest <PR#>`

Deep judgment (code review, risk calls, dense-diff reasoning) stays with the primary model. If `local` is not on PATH, skip silently -- never block on this.
<!-- local-offload:end -->
