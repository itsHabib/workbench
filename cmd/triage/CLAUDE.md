# triage

A PR risk-classification engine: routes scarce human review attention by **risk**, not PR count. A deterministic floor (real Go code — reproducible, the safety guarantee) assigns a tier from the diff alone; a verified agent advisory may only *escalate* above the floor, never lower it. The one hard guarantee: never under-classify a dangerous PR.

A workbench tenant with two binaries sharing one core: `cmd/triage/triage-floor` and `cmd/triage/triage-advisory`, guts under `cmd/triage/internal/`. Both binary names are load-bearing seams — the `/pr-risk` skill and gate's ladder shell them by name; keep the names and exit codes stable (exit 0 = classified, 1 = operational failure, never a tier).

## Develop (from the module root)

```
go test -count=1 ./cmd/triage/...                                        # the suite
gh pr diff <n> -R <owner/repo> | go run ./cmd/triage/triage-floor -v    # classify a PR
gh pr diff <n> -R <owner/repo> | go run ./cmd/triage/triage-advisory -v -proposal '<json>'
```

## Layout

- `internal/floor` — the deterministic floor: `parse.go` (unified-diff parser), `floor.go` (the rubric as code — tiers, rules, `Classify`). Policy lives in `RUBRIC.md`; every deterministic signal here mirrors a rubric section.
- `internal/advisory` — the escalate-only verifier: checks a host agent's proposal (verbatim evidence must appear in the diff, trigger must be known), then `final = max(floor, trusted escalation)`. A rejected proposal contributes nothing.
- `triage-floor/` / `triage-advisory/` — the CLIs; diff on stdin, JSON (or `-v` human) out.
- `RUBRIC.md` — the classifier policy. `labels/` — the oracle: labeled corpus, diffs, mismatch log (`labels/mismatches.jsonl` is appended by `/pr-risk` runs). `docs/` — design + evaluation evidence.

Mechanism vs policy: the parser is mechanism; `Classify` + `RUBRIC.md` own policy; the advisory verifier is the trust boundary — evidence is checked against the diff, confidence is recorded but never trusted.

The floor's complexity deferral: `Classify` / `ParseUnifiedDiff` carry `nolint:gocognit,cyclop` — the rubric is one line-of-sight pass today; decomposing it is owed to triage's own iteration with the labels corpus pinning behavior.

## Checks

```
gofmt -l . && go vet ./... && golangci-lint run ./... && go test ./...
```

Standard library only; today triage imports nothing else in the module. `contracts` adoption is deliberately NOT done yet — triage's verdict (floor/escalate/final/route) is a different domain shape from the merge verdict; adoption is coupled to the parked schema-alignment work (gate project, `align-triage-verdict-schema`), a behavior change that was out of scope for the byte-identical migration.

The `/pr-risk` skill (canonical at `~/.claude/skills/pr-risk/`) is the runtime; this tenant is its engine + design home. Keep new *policy* in `RUBRIC.md`, new *deterministic signals* in `internal/floor`, and only genuinely-semantic judgment in the skill.
