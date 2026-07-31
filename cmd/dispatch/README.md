# dispatch

One small stdlib-only Go binary that decides **placement** — which engine,
provider, model, effort, and runtime a task gets, plus its advisory escalation
rule — by first-match-scanning a versioned, content-hashed policy file against
a task descriptor. It emits the decision as one JSON line on stdout and, when
asked, appends an append-only JSONL receipt. It never dispatches, polls, lands,
or calls a provider: ship's `dispatch` verb executes what this decides.

A workbench tenant: the binary lives at `cmd/dispatch`, its guts under
`cmd/dispatch/internal/` (`policy`, `placement`, `receipt`, plus a `replay`
test harness). Two seams are load-bearing — the exit-code contract below, and
the decides-but-never-executes boundary.

## Use

```
go build -o dispatch.exe ./cmd/dispatch

./dispatch.exe validate --policy policy.json            # author pre-flight; prints {"valid":true,...}
./dispatch.exe decide --policy policy.json --task '{"repo":"o/r","task_class":"analytical","weighted_loc":900,"risk_tier":"T2"}'
echo '{...descriptor...}' | ./dispatch.exe decide --policy policy.json   # descriptor from stdin when --task is omitted
./dispatch.exe decide --policy policy.json --receipts receipts.jsonl     # also append one receipt line per decision
```

Those are the only flags: `decide` takes `--policy`, `--task`, `--receipts`;
`validate` takes `--policy`, required by both. Errors are single-line JSON
`{code, message}` on stderr.

## Exit codes

| code | `decide` | `validate` |
|---|---|---|
| 0 | placed | valid |
| 1 | — | valid, with warnings (no catch-all rule) |
| 2 | bad/missing/empty policy, unknown `task_class` in a match block, usage | invalid policy, usage |
| 3 | no rule matched (the unmatched values are on stderr) | — |
| 4 | bad descriptor | — |
| 5 | `--receipts` given and the append failed (stdout stays empty) | — |

No non-zero exit ever emits a placement on stdout.

## How it works

`policy.Load` reads the file, takes the sha256 of the **exact on-disk bytes
before parsing**, then validates fail-closed: unknown JSON fields, an
unsupported `version`, an empty rule set, an unnamed rule, an incomplete
`place`, and an unknown `task_class` in a match block are all refused.
`placement.ParseDescriptor` decodes the descriptor with unknown fields refused
and requires `repo`, a frozen-enum `task_class`
(`mechanical | analytical | generative`), a non-negative `weighted_loc`, and an
opaque-string `risk_tier` (the tier vocabulary is `/pr-risk`'s). Rules are
scanned in file order and the first whose set constraints all hold wins; an
empty `match: {}` is the explicit catch-all. The placement carries its own
`schema_version` plus provenance — rule name, policy version, policy sha256.
With `--receipts`, the receipt (`decided_at`, rule, policy sha256, full
descriptor, full placement) is appended *before* the placement hits stdout.

Constraints that are design decisions, not omissions:

- **Fail-closed on an unmatched descriptor.** A descriptor matching no rule is
  exit 3, never a fallback placement. To get a default the operator must write
  `match: {}` into the file; `validate` warns (exit 1) when no catch-all
  exists.
- **It decides placement only.** No dispatch, no polling, no landing, no
  provider call. The `escalation` block is carried in the placement as
  advisory data — the seat driving the run is what would enforce it.
- **Determinism is by law.** No clock, network, or randomness on the decide
  path; output is marshaled from fixed-field structs, never by ranging a map.
  `decided_at` is the only time read and it lands in the receipt file, never in
  the stdout placement.
- **No placement on a non-zero exit.** The receipt-before-stdout ordering is
  what makes a failed append (exit 5) leave stdout empty.
- **No in-repo dependency.** Standard library only; `placement` deliberately
  does not enter `contracts/` yet — that is the phase-4 trigger, a second
  consumer.

## Status

Phase 1 (`dispatch-decide-core`) — the `decide` + `validate` verbs, built and
test-covered. `internal/replay` is the phase-2 validation harness: a table test
that replays 8 committed historical streams through the real loader and matcher
against a fixture policy. `drift` and a scorecard ingest (phase 3),
`/work-driver-prep` calling `decide` (phase 4), and promoting `placement` into
`contracts/` are not built. Concurrent receipt writers are declared
unsupported — callers are serial.

Read [`docs/DESIGN.md`](docs/DESIGN.md) for the charter (the laws, the frozen
taxonomy, and the frozen descriptor-derivation rules) and
[`CLAUDE.md`](CLAUDE.md) for the scoped agent guidance; the binding TDD is
[`docs/features/dispatch/spec.md`](../../docs/features/dispatch/spec.md), whose
own status header still reads draft/proposal.
