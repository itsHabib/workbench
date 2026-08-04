**Status**: draft
**Owner**: @itsHabib
**Date**: 2026-08-04
**Related**: dossier task `pin-panel-declaration-default-branch` (id: `tsk_01KZ2QRQEVRJNHKFC1MEN439DS`), [tier-aware panel TDD §7.2](spec.md)

# Pin that the panel declaration is read from the default branch — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `cmd/gate/internal/evidence/panel.go` (comment only) | ~6 | 6 |
| Tests | `cmd/gate/internal/evidence/panel_test.go` | ~80 | 40 |
| **Total** | | | **~46** |

Band: **amazing**.

## Goal

`fetchExpectedReviewers` fetches the review-panel declaration with no `?ref=`, so GitHub serves the repository's **default branch**. That is the property that stops a pull request from altering the panel it will be judged against — and, once `require_at_tier` lands in a later phase, from lowering its own review bar.

Right now that property is an *emergent consequence of an omitted parameter*. Nothing asserts it. A future change that adds a ref "for correctness" would silently convert it into a self-service bypass and no test would fail. This phase pins it, before anything is built on top of it.

## Behavior / fix

Two changes, both small.

**1. A test asserting no argument to the declaration fetch can select a ref.**

The call site is `cmd/gate/internal/evidence/panel.go`:

```go
raw, err := gh("api", fmt.Sprintf("repos/%s/contents/%s", repo, panelDeclarationPath))
```

`gh` (`cmd/gate/internal/evidence/evidence.go:377`) runs `exec.Command("gh", args...)`, which resolves `gh` through `PATH`. So the seam already exists without any production change: put a fake `gh` earlier on `PATH`, have it record its full argv, and assert on what was recorded.

**Assert on the complete argument vector, not just the URL string.** `gh api` can select a ref in several ways — a `?ref=` query string, `-f ref=…`, `-F ref=…`, `--field ref=…`, `--raw-field ref=…`. Checking only the constructed path leaves every flag form as an unguarded way to reintroduce head-pinning. The invariant to encode is:

> No argument to this call can select a ref other than the default branch.

**2. A comment at the fetch site naming the security property.**

The omission must read as deliberate, or the next reader "fixes" it. One line naming what the absent ref buys — a PR cannot lower its own review bar — is what makes the constraint visible to someone about to change it.

## Acceptance

- A test fails if the declaration fetch gains a ref in **any** argument form (query string or any `-f` / `-F` / `--field` / `--raw-field` variant).
- The test name and failure message state *why* the property exists, so someone hitting the failure understands it before changing it rather than after.
- The fetch site carries a comment naming the security property.
- `gofmt -l . && go build ./... && go vet ./... && go test ./...` all green.

## Test plan

A fake `gh` on `PATH`, following the pattern used elsewhere in this package for external-binary tests:

- Write an executable script named `gh` into `t.TempDir()` that appends its argv to a file and prints a minimal valid contents-API response (`{"sha":"…","encoding":"base64","content":"…"}` whose decoded body is a valid `.ship.json`, so `fetchExpectedReviewers` reaches its normal return rather than erroring early).
- `t.Setenv("PATH", dir)` so the fake resolves ahead of any real `gh`.
- Call the declaration fetch, read back the recorded argv, and assert no element selects a ref.

Suggested name — the failure message should read as an explanation on its own:

`TestPanelDeclarationFetchCannotSelectARef`

## Non-goals

- Any behaviour change to `PanelCompleteness` or to how panel state is classified. This phase pins existing behaviour only.
- Threading the floor tier into the panel rung (P2).
- `require_at_tier`, the decision flow, or the schema (P3).
- Recording the default-branch HEAD alongside the blob SHA for retrospective provenance audit — that is TDD §10.5, deliberately deferred and not required for the live-path guard this task delivers.
