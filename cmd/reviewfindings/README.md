# reviewfindings

Two verbs over one artifact. `reviewfindings github` is the **producer**: it
reads a PR's inline review comments through `gh`, keeps only the comments whose
`original_commit_id` is the exact head *and* whose author is a reviewer the
caller named as completed, and writes a validated `ReviewFindingsV1` document
whose `artifact_id` is derived from a canonical SHA-256 of its own content.
`reviewfindings address` is the session-native **consumer**: it takes that
artifact and drives one bounded address cycle — accept, claim, started,
completed, resume — on the implementation child's driver-state ledger, without
invoking Ship. The producer emits findings and stops; it never dispatches,
never merges, and never calls a model or provider API.

Tenant layout: the binary is `cmd/reviewfindings` (`main.go` = producer,
`address.go` = consumer seam); the shared vocabulary and all validation law
live in `contracts/reviewfindings`, which carries the schema and the invariants
but no decision logic; ledger mechanics come from the top-level `driverstate`
package. The load-bearing seam is the exit-code contract plus the artifact
file itself.

## Use

```
go build ./cmd/reviewfindings

# produce — exact head, explicit panel, both lists required
reviewfindings github -repo owner/repo -pr 181 -head <sha> \
  -requested codex,cursor,claude,copilot -completed codex,cursor \
  -catalog-revision <full-commit-sha|sha256:...> \
  -out findings.json                      # prints the output path on success

# consume — one bounded address cycle on the child run's ledger
reviewfindings address accept    -run <child-run> -stream <stream> \
                                 -artifact findings.json -max-cycles 3
reviewfindings address claim     -run <child-run> -stream <stream> -work <raw_id>
reviewfindings address started   -run <child-run> -stream <stream> -work <raw_id> \
                                 -task <codex-task-id>
reviewfindings address completed -run <child-run> -stream <stream> -work <raw_id> \
                                 -head <new-live-head>
reviewfindings address resume    -run <child-run> -stream <stream> -work <raw_id>
```

`-producer` (default `codex:reviewfindings-github`) and `-actor` (default
`session:codex`) override the recorded identities.

| Code | Meaning |
| ---: | --- |
| 0 | success — artifact written, or the ledger transition recorded (JSON on stdout) |
| 2 | parked ambiguity — e.g. work claimed but no task id recorded, so a crash may have happened after task creation |
| 3 | refused — stale head, PR not open, malformed artifact, duplicate/consumed cycle, panel not settled, finding-count mismatch, out-of-order lifecycle |
| 4 | operational or usage error |

## How it works

The producer refuses before touching `-out`: the PR must be `OPEN`, the live
head must equal `-head`, and at least one sourced exact-head inline finding
must survive filtering ("no sourced exact-head inline findings"). Only then is
the artifact canonicalized, given its `rf_<32 hex>` id, re-validated, and
written through a temp file + rename. Severity is a keyword match over the
comment body defaulting to `advisory`; the summary is the first non-decorative
line, truncated on a rune boundary at 512 bytes.

`address accept` re-reads the live PR head, takes a run lease, and persists
`AddressWorkV1` — the artifact embedded alongside its digest and a
deterministic `work_id` — before consuming it, so a resumed child needs no
provider transcript. `completed` and `resume` re-read the live head and
reconcile it against the recorded source/result heads.

## Constraints that are design decisions, not omissions

- **The panel is caller-asserted, not discovered.** `-requested` and
  `-completed` are required inputs; the contract only checks that
  completed/missing partition requested and that every finding's source
  reviewer appears in `completed`.
- **`-catalog-revision` is optional for compatibility.** Omitting it still
  produces a valid artifact; per `CLAUDE.md` that artifact cannot complete a
  closure receipt, and `driverstate/closure.go` carries the receipt field.
- **Findings are recorded, not judged.** No severity policy, no dedup across
  reviewers beyond identical comment sources, hard caps at 100 findings and
  1 MiB per artifact.
- **The consumer creates no task.** `started` records an externally created
  Codex task id; the CLI never spawns one.
- **Only `decision: "address"` exists in v1**, and only `pull_request`
  subjects.

## Relationship to `review`

`review` and `reviewfindings` are separate binaries with **no Go import between
them**: `cmd/review` uses `contracts/reviewroute` and `contracts/reviewpanel`,
while `cmd/reviewfindings` uses `contracts/reviewfindings`. They meet at the
artifact — `cmd/review/CLAUDE.md` directs callers here for sourced, nonempty
`ReviewFindingsV1` evidence to feed a review cycle. In-repo importers of
`contracts/reviewfindings` are `cmd/reviewfindings` and `driverstate`.

`CLAUDE.md` is the canonical scoped guidance; there is no tenant-local
`docs/DESIGN.md`.
