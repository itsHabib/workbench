**Status**: draft
**Owner**: @itsHabib
**Date**: 2026-07-13
**Related**: dossier task `control-room-contract-fixtures-config` (id: `tsk_01KXCY4CGWVB7132RF1CA0QK7M`); [`../spec.md`](../spec.md); Ship PRs #193 and #194

# Capture Control Room contracts and explicit source configuration

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Contract fixtures | `cmd/controlroom/testdata/contracts/{ship,dossier,github,tracelens,toolhealth,tower}/**` plus a fixture inventory/readme | ~330 | ~165 |
| Configuration/privacy contract | `docs/features/portfolio-control-room/source-config.md`; full weight because executable boundaries, scope validation, and privacy rules require design judgment | ~180 | ~180 |
| Provenance/envelope research | Fixture inventory with exact producer command, PR/SHA, version, and public-key provenance | ~50 | ~50 |
| Validation | `cmd/controlroom/fixtures_test.go` for JSON/JSONL syntax, inventory coverage, and secret/path sanitization | ~200 | ~200 |
| **Total** | | **~760** | **~595** |

Band: **ideal** per the repository PR-sizing convention.

## Functional

Pin sanitized, producer-shaped fixtures for every Control Room source before the presentation model or adapters land. Required sources are Ship workflow list/status observability, Ship driver list, Dossier task reads over stdio MCP, scoped GitHub PR inventory/detail, Tracelens JSON analysis, and toolhealth's current machine or tolerant text surface. Tower is optional and must have both an available read fixture and an unavailable fixture.

Each source directory must include at least one healthy payload and one explicit unavailable or degraded case. GitHub fixtures also cover truncated detail/inventory semantics; Ship covers empty and malformed additive records; Dossier covers a normal result and session failure; Tracelens covers findings and unsupported/unavailable telemetry; toolhealth distinguishes accumulated friction from a live incident. Fixtures use stable timestamps and synthetic IDs anchored to the TDD demo clock. They must contain no real usernames, credentials, absolute operator paths, raw prompts, or sensitive traces.

Document the versioned source configuration consumed by later adapter tasks. Configuration names executable paths, argv, timeouts, workspace root, Dossier corpus, GitHub scopes, and optional-source enablement explicitly. It may discover executables on `PATH`, but it never derives or reads sibling store paths. Record the exact privacy/redaction rules and the fixture provenance command for each producer without requiring those commands at test time.

This task is docs/fixtures-only. Do not create the `cmd/controlroom` binary or presentation types early; Phase 2 owns the command/model skeleton and will add typed fixture parsers.

## Tradeoffs

- Preserve producer envelopes instead of designing adapter-owned convenience shapes. Normalization belongs to Phase 2/4.
- Commit deterministic synthetic fixtures rather than snapshots of live operator state. Live captures may be used only as a shape reference and must be rewritten/sanitized before commit.
- Toolhealth may remain a tolerant text fixture until its owner exposes JSON; document that provisional seam rather than reimplementing its local-model bucketing.
- Tower stays supplemental. Its available and unavailable fixtures are required contract coverage; Tower's runtime presence remains optional and missing Tower is never a startup failure.

## EDs

- Ship fixtures reflect the merged owner contracts from PR #193 (`ship driver list --json`) and PR #194 (`ship list/status --json` observability); no SQLite, manifest, or result-artifact fixture is permitted. Include owner-issued workflow `docPath` and driver `specPath` identities using neutral relative values such as `docs/features/example-feature/spec.md`; they are required linkage fields, not operator metadata.
- Dossier fixtures model the long-lived stdio MCP session and JSON-RPC result/error framing, not direct markdown corpus reads.
- GitHub config accepts one to four explicit `user:`, `org:`, or `repo:` scopes. Fixtures model the four-page/200-PR cap and `detail_state = complete | truncated | unknown` rules.
- Every unavailable/degraded case has a typed fixture receipt or expected error descriptor; raw child stderr is never a browser-facing fixture.
- Fixture validation rejects credential-like strings, real operator home paths, and JSON/JSONL syntax errors. Synthetic POSIX/Windows paths use neutral placeholders only when a source contract genuinely carries a path.
- Configuration is versioned (`v: 1`) and additive-field tolerant. Unknown fields can be ignored by later readers; missing required identity/command fields fail closed.

## Validation

- Parse every `.json` document and every `.jsonl` line in a repository test or validation command.
- Scan committed fixtures/docs for the configured `OPERATOR_DISPLAY_NAME`, private-key headers, environment assignments containing secrets, expanded Windows home paths matching `[A-Z]:\\Users\\`, and Unix/macOS home prefixes matching `/home/<name>/` or `/Users/<name>/`. `%USERPROFILE%` is a neutral placeholder and is allowed only in documentation, never as if it were an observed absolute path.
- Assert the inventory contains all six source directories, healthy coverage for every required source, and unavailable/degraded coverage for every required or optional source.
- Verify Ship fixture keys match the merged PR #193/#194 public envelopes and exclude `sourceJson`, `manifestPath`, `artifactsDir`, raw trace content, and absolute paths.
- Run `gofmt -l .`, `go vet ./...`, `go test -race ./...`, and the repository lint/build gates. Docs-only additions must not break the existing module.

The deterministic credential scan uses this minimum pattern table; implementations may add stricter patterns but must keep synthetic fixtures from accidentally matching them:

| Secret class | Required detection |
|---|---|
| GitHub | prefixes `ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_`, or `github_pat_` followed by a non-placeholder value |
| Cursor | `CURSOR_API_KEY=` with a non-placeholder value and bearer-style authorization values |
| OpenAI / Anthropic | non-placeholder values beginning `sk-` or `sk-ant-` |
| Generic environment | case-insensitive variable names containing `TOKEN`, `KEY`, `SECRET`, `PASSWORD`, or `CREDENTIAL`, followed by `=` and a non-placeholder value |
| Private keys | `BEGIN ... PRIVATE KEY` PEM headers |

## Risks

- Hand-authored fixtures can drift from producer contracts. Record source PR/SHA and capture commands in the inventory, and keep fixtures intentionally small enough for review.
- Over-sanitizing may erase required path/identity semantics. Preserve relative owner-issued identities while replacing only machine-specific values.
- A validation script can accidentally become a second parser. Keep it limited to syntax, inventory, and privacy assertions; typed semantic parsing lands with the Phase 2 model.

## Out-of-scope

- `cmd/controlroom` production code, HTTP handlers, UI, ranking policy, source subprocesses, MCP lifecycle management, refresh orchestration, and Playwright.
- New producer APIs or direct reads of any sibling database/corpus.
- Live screenshots or demo artifacts.

## Implementation-plan

1. Inventory the exact merged/public producer envelopes and write a fixture provenance table.
2. Add small synthetic healthy plus degraded/unavailable fixtures for Ship, Dossier, GitHub, Tracelens, toolhealth, and optional Tower.
3. Document versioned executable/argv/timeout/scope/path configuration and privacy rules.
4. Add syntax/inventory/secret-path validation, run repository gates, and review the full fixture corpus for operator data.
