# triage

A PR risk-classification engine — a [workbench](https://github.com/itsHabib/workbench) tenant. Routes scarce human review attention to the PRs that need judgment, and machine-clears the ones that don't — so review load scales with **risk**, not **PR count**.

Two independent scorers (an agent + the PR author, blind) each assign a risk tier; the higher wins (fail-safe); the tier routes the PR to the right amount of review. The one hard guarantee: it must never *under*-classify a dangerous PR.

- **Run it:** the `/pr-risk` skill (canonical in `~/.claude/skills/pr-risk/`) — floor engine → agent advisory → route → log.
- **Engine (the deterministic safety layer):** [`internal/floor`](internal/floor) + [`triage-floor`](triage-floor). From the workbench module root: `gh pr diff N -R owner/repo | go run ./cmd/triage/triage-floor -v`
- **The classifier policy:** [`RUBRIC.md`](RUBRIC.md)
- **The oracle (test-first):** [`labels/`](labels/)
- **Design + evidence:** [`docs/features/pr-risk-engine/`](docs/features/pr-risk-engine/) — spec + `EXPERIMENT-01.md`

Status: **dogfooding.** Engine built + tested + run live on ship/dossier/rooms PRs. Runtime is a registry skill; this repo is its engine + design home. Recommend-only until the auto-merge safe-slice is flipped on.
