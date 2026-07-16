# tracelens

Agent trace diagnostics. Feed it an agent run's trace and it tells you what went wrong and what to change: loops, redundant tool calls, retry storms, cost hotspots, and stuck (no-progress) states — each finding with the exact evidence steps, the dollars it wasted, and a concrete fix.

A workbench tenant, stdlib only. No network, no model, no credentials — pure computation over the parsed trace, so every verdict is reproducible and the suite runs anywhere. `-json` output makes it usable as a gate on agent runs in CI. A versioned labeled corpus measures detector regressions instead of relying on the demo alone.

```bash
go install github.com/itsHabib/workbench/cmd/tracelens@latest
```

## How to run (from the module root)

```bash
go test -count=1 ./cmd/tracelens/...                               # the suite
go test -run Example ./cmd/tracelens/internal/tracelens            # zero-arg demo, pinned as an Example
go run ./cmd/tracelens cmd/tracelens/testdata/sample.jsonl         # analyze a trace file
go run ./cmd/tracelens -json cmd/tracelens/testdata/sample.jsonl   # machine-readable verdict
go run ./cmd/tracelens eval cmd/tracelens/testdata/corpus          # labeled-corpus quality gate
```

Generate a portable Markdown report with evidence excerpts:

```bash
go run ./cmd/tracelens report -dialect neutral-jsonl cmd/tracelens/testdata/sample.jsonl
go run ./cmd/tracelens report -dialect ship-claude cmd/tracelens/testdata/corpus/ship-claude-healthy.ndjson
go run ./cmd/tracelens report -dialect ship-codex cmd/tracelens/testdata/corpus/ship-codex-healthy.ndjson
go run ./cmd/tracelens report -dialect ship-cursor -output report.md cmd/tracelens/testdata/ship/wf_01KVNKHBS61WJKZ9BVEQG6B5Y6/events.ndjson
```

CI also runs the suite with `-race`; the race detector needs cgo, so on Windows without a C toolchain run the plain `go test` form above — CI covers the `-race` build.

## Analyze a ship run

`tracelens ship` detects and decodes what Ship persists for Cursor, Claude stream-json, and Codex runs (`<runs-dir>/<wf-id>/events.ndjson`) and gates on the verdict — exit 0 pass/escalate, 1 block, 2 error:

```bash
go run ./cmd/tracelens ship wf_01ABC…               # a run id, resolved under SHIP_RUNS_DIR
go run ./cmd/tracelens ship path/to/run-dir         # a run directory (events.ndjson inside)
go run ./cmd/tracelens ship -json <run-ref>         # machine-readable verdict for gating

# the committed fixture — a real `ship driver` stream that burned its 30m cap:
go run ./cmd/tracelens ship -quiet cmd/tracelens/testdata/ship/wf_01KVNKHBS61WJKZ9BVEQG6B5Y6
```

The fixture's verdict explains the cap burn the driver could only call "timeout-near-cap": a 6× loop re-editing the same file (steps 67–72), pathological, exit 1.

Ship streams do not consistently carry per-step cost/tokens/latency. Tracelens leaves unavailable telemetry unknown rather than distributing aggregate totals across steps. Cost-hotspot and retry-storm findings therefore activate only when the normalized steps actually contain the required evidence.

## How it works

- **Loop** — a tandem-repeat scan over `(tool, args)` signatures finds cycles of any period (`A A A`, `A B A B A B`) and reports the strongest. The default semantic mode ignores only an explicit list of volatile producer metadata and additionally requires repeated confirmed outcomes; pagination, paths, commands, arbitrary IDs, and changing observations remain progress.
- **Stuck** — a progress model replays the run tracking seen *state* signatures and flags a trailing stall where the agent only revisits known states — distinct from a loop: it catches three different queries that all return the same nothing.
- **Retry storm / redundancy / cost hotspot** — attribute wasted spend, kept disjoint so dollars never double-count.
- **Verdict (policy)** — ranks findings by severity then measured waste, sets `pass | escalate | block` plus an orthogonal approval tier, and emits an evidence-filled repair per finding.

Mechanism and policy are separate layers: detectors (`detect.go`) report findings behind one `Detector` interface; `buildReport` (`analyze.go`) owns thresholds, ranking, and health rules. Adding a pathology means adding one detector.

The guard test `TestAnalyze_LoopMakesRunPathological` fails if the detector core is stubbed to return no findings — a green suite certifies the analysis actually ran, not just that constructors work.

## What's rough (honest)

- Four formats feed one analysis: neutral JSONL plus Ship Cursor, Claude, and Codex. Other producers still require concrete adapters.
- Semantic loop normalization is intentionally narrow. Variation in meaningful arguments still makes calls distinct, and no generic similarity model is used.
- Wasted-$ is a lower bound (redundant successful recompute + retry failures); it doesn't price a failing test inside a loop.
- Repair strings are deterministic templates, not LLM-authored.
- The curated corpus is regression evidence, not a statistically representative sample of all agent runs; see [docs/EVALUATION.md](docs/EVALUATION.md).

## Docs

`docs/PITCH.md` explains why this exists, `docs/DECODING.md` documents normalization, `docs/EVALUATION.md` defines the corpus gate, and `docs/NEXT.md` parks remaining scope. Provenance: built in the 2026-06-30 agent build-hackathon; graduated as the top-ranked project.
