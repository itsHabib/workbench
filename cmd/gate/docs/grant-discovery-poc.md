# Grant discovery POC

At baseline `10b066cac0bb959ca3dfa7dc2d77886eed277e78`, `gate gate` requires a pasted grant ID. `next` and
`preflight` expose advisory inventory, but do not authenticate signatures or
every subject binding, and preflight chooses one widest-first record before
checking cycle fit. The shared PR-sweep skill also says to mint as soon as a PR
is ready. These paths can prompt for authority that the operator already granted.

The correction is one resolver shared by an optional read-only
`discover-grant` command and `gate gate` when `-grant` is omitted. It reads the
current head and the existing deterministic floor, audits cycle accounting, and
uses the existing capability checks across all relevant grants. Among grants
that fit the subject, floor and next cycle, it selects the broadest tier ceiling
so a newer narrow grant cannot hide a usable older one. Every selected grant
is checked again by the normal Gate path.

Discovery selects authority for assessment, not permission to merge. The floor
is a lower bound; the full verifier ladder can raise the required tier. An
unreadable head, diff, floor, ledger or key is an assessment failure, not proof
that a new grant is needed. Only assessed authority gaps produce a mint request.
Explicit `-grant` and `-slack` paths retain their behavior; judgment remains bound
to its existing run and grant flow. No key creation, grant minting, state append,
status publication or merge occurs during discovery.

Passing regression scenarios: an expired grant beside a valid one; an older
T2 beside a newer T1; a high-tier grant with exhausted cycles beside a usable
lower-tier grant; wrong repo/action/head/PR and invalid signatures; a newly
minted grant after a prior uncovered attempt; unassessed inputs; explicit-ID
failure with a usable alternate present; and a covering grant reaching normal
Gate evaluation without a new-grant prompt.

Live inventory checked on 2026-09-13 included RoxIQ T2 `grt_cd034d098e99eda7`
alongside newer T1 `grt_9087a1b0914279b4`, and Workbench T1
`grt_efbb165e61a4b277` alongside newer T0 `grt_2fe70e45b5512981`.
These are inventory observations, not merge authorizations. The POC's tests use
temporary fixture state and keys; live custody and installed tools are unchanged.


Run the regressions from the module root:

```sh
go test -race ./cmd/gate/... -count=1
```

The suite also checks read-only log/anchor/key bytes, custom key/floor command
preservation, a closed PR with no grant, and head movement both during discovery
and before Gate's first view. Fixture tools replace PATH, so tests cannot fall
through to authenticated GitHub or a real model. Operator minting in tests occurs
only in throwaway state and key directories.

Review regressions additionally cover the exit-4/no-outcome terminal for a
failed assessment, relative key/floor paths copied across working directories,
and visible rejected-grant diagnostics alongside a valid selection. An oversized
diff fixture verifies routing into the existing local fallback with pinned Git
arguments, including rejection of a head move during that fallback.

Local validation passed module-wide vet, lint (zero issues), race tests and
build. The final ordering change refreshes the PR before selecting authority;
Gate's race suite, lint, vet and build were repeated after it.

Read-only live probes on 2026-09-13 used a worktree-built Gate and floor, without
installing either or evaluating Gate against live state:

| Subject and observed head | Result |
| --- | --- |
| Workbench #335, `4350bbedf13f2b383c651c9949359a1cb50f4d78` | T0 floor, next cycle 1; reused existing T1 `grt_efbb165e61a4b277`; no mint request. |
| RoxIQ #250, `1e30dbd0ee7a693d9cee9a6089cb218eaa1b38f6` | T3 floor, next cycle 2; active T2 and T1 grants cannot cover the minimum; returned the actual tier gap. |
| Workbench #334, `d9ca988176bd47beb6afc33a70b31092b4302e0e` | T1 floor, next cycle 1; existing T1 permits assessment. This does not cover the T2 requirement identified by its separate reviewer; the floor is only a minimum. |

These snapshots are evidence of discovery behavior, not lasting grant validity
or merge readiness. The full ladder and exact-head action remain necessary.
The companion shared-skill source changes direct normal delivery to the no-ID
path, keep PR sweeps read-only, and remove mint-first/last-five-log-row guidance.
No installed guidance or global agent instructions were edited.
