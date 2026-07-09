# workbench

The home for the Go agentic-infra family — one repo, one Go module. Tools live
side by side and **share contracts, not call stacks**: they compose at runtime
through artifacts (exit codes + JSONL on disk), never by importing each other's
decision code.

```
go install github.com/itsHabib/workbench/cmd/<tool>@latest
```

## Layout

- `contracts/` — the shared vocabulary: the verdict schema + Go types every
  verifier emits, and the artifact envelope every producer writes. A leaf
  package that imports nothing else in the module. This is the debt payment:
  one source of truth instead of a hand-rolled parser per tool.
- `local/` — the shared local-model mechanism: structured Ollama calls + an
  escalate-on-uncertainty gate. A top-level *mechanism* package (it carries no
  tool's decision logic), leaf-checked like `contracts`. Its faces are
  `cmd/local` (agent co-processor) and `cmd/eval` (the local-exportability
  oracle).
- `cmd/<tool>/` — one binary per tool; its guts stay private under
  `cmd/<tool>/internal/`. Today: `flare`, `local`, `eval`.
- `docs/DESIGN.md` — the repo's charter: the single-module decision and why,
  what's in and out, the boundary law, the lazy-migration policy, and the
  triggers that would later split `contracts` into its own module.

## The one rule

A tool may share **types and schemas** through `contracts`. A tool may **not**
import another tool's decision logic — gate importing flare's routing, a
classifier importing the gate reducer. When a tool needs another tool's
*output*, it reads an artifact. CI enforces this (`hygiene` job); it is not a
convention.

## Develop

```
gofmt -l . && go vet ./...
golangci-lint run ./...
go test ./...
```

Standard library only; no third-party dependencies.
