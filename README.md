# workbench

The home for the Go agentic-infra family: one repo, one Go module, nineteen small binaries
that let one person run a team of coding agents and trust what comes back. Tools live side by
side and **share contracts, not call stacks**: they compose at runtime through artifacts (exit
codes and JSONL on disk), never by importing each other's decision code.

```
go install github.com/itsHabib/workbench/cmd/<tool>@latest
```

## Start here

- **Run several agents at once and always know who is doing what:** [`cmd/fleet`](cmd/fleet/README.md).
  Read [the overview](cmd/fleet/docs/OVERVIEW.md), then [onboarding](cmd/fleet/docs/ONBOARDING.md);
  if you want the habits without the machinery, [the minimum](cmd/fleet/docs/MINIMUM.md).
- **Decide whether an exact pull-request head may merge, with a record:** [`cmd/gate`](cmd/gate/README.md).
- **What is this whole system?** [`docs/workbench-101.md`](docs/workbench-101.md), the teaching
  doc; [`docs/plain-language-overview.md`](docs/plain-language-overview.md) is the same picture
  with no jargon; [`docs/README.md`](docs/README.md) is the full table of contents.

## The tools

Grouped by what they own. Each has its own README under `cmd/<tool>/`.

**Running agents**

| tool | what it does |
|---|---|
| [`fleet`](cmd/fleet/README.md) | the substrate for a team of agents: a hook that derives identity, liveness and leases from harness events; seats, assignment rows, receipts, role-addressed mail, a watcher that folds it into a board |
| [`org`](cmd/org/README.md), [`org-mcp`](cmd/org-mcp/README.md) | role continuity: charters, the role tree, and each lead's own append-only record (intent, effect, resolution, escalation); the MCP face of the same verbs |
| [`runway`](cmd/runway/README.md) | foreground execution-runtime controller: one admitted request at a time |
| [`dispatch`](cmd/dispatch/README.md) | placement: which engine and where a task runs |
| [`driverstate`](cmd/driverstate/README.md) | the human and cron CLI over the driver-state event ledger |
| [`codexguard`](cmd/codexguard/README.md) | the deterministic policy floor for Codex tool calls |

**Deciding what may merge**

| tool | what it does |
|---|---|
| [`gate`](cmd/gate/README.md) | the merge-authorization boundary: scoped, tiered, time-boxed grants; a verifier ladder; a hash-chained decision log; exit codes `0` pass, `1` blocked, `2` parked, `3` refused, `4` error |
| [`triage`](cmd/triage/README.md) | PR risk classification: a deterministic floor plus an escalate-only advisory (`triage-floor`, `triage-advisory`) |
| [`review`](cmd/review/README.md) | who has to review an exact head, and whether they have |
| [`reviewfindings`](cmd/reviewfindings/README.md) | one artifact over the reviewers' findings: a producer that reads GitHub, a consumer that judges |
| [`escalate`](cmd/escalate/README.md) | the agent → human → agent back-channel: ingests a human's decision for a parked gate run and closes it |

**Seeing and being told**

| tool | what it does |
|---|---|
| [`console`](cmd/console/README.md) | a local, read-only web view of gate's inbox |
| [`flare`](cmd/flare/README.md) | notifications on authoritative receipts; a best-effort sink, never a gate |
| [`tracelens`](cmd/tracelens/README.md) | agent trace diagnostics: loops, redundant calls, retry storms, and what to change |
| [`workbench-mcp`](cmd/workbench-mcp/README.md) | the unified MCP surface over the workbench verbs |

**Local models and secrets**

| tool | what it does |
|---|---|
| [`local`](cmd/local/README.md), [`eval`](cmd/eval/README.md) | hand one sub-task to a local model (Ollama) with an escalate-on-uncertainty gate; measure which tasks can be exported to it |
| [`custody`](cmd/custody/README.md) | a localhost credential broker: the agent calls an API it is never handed the secret for; every request is an injected pass or a fail-closed refusal |

A taste, the `local` primitive classifying a CI log line on a local model (needs Ollama):

```sh
$ echo "Error: connect ETIMEDOUT registry.npmjs.org:443" | \
    env local -prompt "Classify this CI line: flake, infra, or real-break." \
              -schema '{"type":"object","properties":{"class":{"type":"string"}},"required":["class"]}'
{"source":"local","result":{"class":"infra"}}   # output varies by model; verified on qwen2.5:7b
```

(`env` sidesteps the `local` builtin in bash and zsh.)

## Layout

- `contracts/` — the shared vocabulary: the verdict schema and Go types every verifier emits,
  the artifact envelope every producer writes, the org record types. A leaf package that
  imports nothing else in the module.
- `local/` — the shared local-model mechanism behind `cmd/local` and `cmd/eval`; leaf-checked
  like `contracts`.
- `cmd/<tool>/` — one binary per tool, guts private under `cmd/<tool>/internal/`, docs beside
  it (`README.md`, and for most tools a `CLAUDE.md` and `AGENTS.md` pair CI keeps identical).
- `docs/` — the charter ([`DESIGN.md`](docs/DESIGN.md)), the teaching docs, the autonomy
  doctrine, and `docs/features/<feature>/` with a spec and evidence per feature. Decisions that
  cut across tools live there too, for example
  [`docs/features/org-fleet-boundary/spec.md`](docs/features/org-fleet-boundary/spec.md), which
  settles what `org` owns and what `fleet` owns.

## The one rule

A tool may share **types and schemas** through `contracts`. A tool may **not** import another
tool's decision logic. When a tool needs another tool's *output*, it reads an artifact. CI
enforces this (the `hygiene` job); it is not a convention.

## Where to read, by question

- **How does the daily work run?** [`docs/workflow-mechanics.md`](docs/workflow-mechanics.md).
- **How far may an agent act unattended?** [`docs/auto-mode-defaults.md`](docs/auto-mode-defaults.md)
  and its executable half, [`docs/auto-mode-rulebook.md`](docs/auto-mode-rulebook.md).
- **What did building it teach?** [`docs/lessons.md`](docs/lessons.md).
- **What do the words mean?** [`docs/glossary.md`](docs/glossary.md).
- **How do I prove a fleet build works?** [`cmd/fleet/docs/e2e.md`](cmd/fleet/docs/e2e.md).
- **What else exists around this repo?** [`docs/projects.md`](docs/projects.md), and the skills
  that drive the workflow at [github.com/itsHabib/skills](https://github.com/itsHabib/skills).

## Develop

```
gofmt -l . && go vet ./...
golangci-lint run ./...
go test ./...
```

Third-party Go dependencies are allowed. Every change lands from a worktree; `main` in the
root checkout stays clean.
