# tracelens

Agent trace diagnostics. Feed it an agent run's JSONL trace and it diagnoses the run — loops, redundant tool calls, retry storms, cost hotspots, stuck states — each finding with the exact evidence steps and a concrete fix. Pure computation over the parsed trace: stdlib only, no network, no credentials, fully reproducible.

A workbench tenant: the binary is `cmd/tracelens`, the library is private under `cmd/tracelens/internal/tracelens`. Nothing imports the library as Go code — consumers (ship's driver, controlroom's adapter) shell out to the `tracelens` binary and read exit codes + JSON. That CLI seam is the contract; keep the binary name and exit codes stable.

## Develop (from the module root)

```
go test -count=1 ./cmd/tracelens/...                               # the suite
go test -run Example ./cmd/tracelens/internal/tracelens            # the old zero-arg demo, pinned as an Example
go run ./cmd/tracelens cmd/tracelens/testdata/sample.jsonl         # analyze a trace file
go run ./cmd/tracelens -json cmd/tracelens/testdata/sample.jsonl   # machine-readable verdict
go run ./cmd/tracelens eval cmd/tracelens/testdata/corpus          # labeled-corpus quality gate
```

Workbench CI runs gofmt + vet + golangci-lint + `go test -race` module-wide, plus the corpus gate (`eval` on the committed corpus) so detector regressions fail CI. `-race` needs cgo, which a plain Windows box lacks — locally run the suite without `-race`; CI covers it.

## Layout

- `internal/tracelens` — the library: `trace.go` (model + exact signatures), `decode.go` plus the concrete decoder files (dialect detection and normalization), `detect.go` (the detector pipeline), `analyze.go` (verdict policy), `eval.go` (labeled-corpus comparison and gate), and the terminal/Markdown renderers.
- `main.go` + siblings — the CLI: generic trace mode plus `ship <run-ref>`, `eval <corpus>`, and `report <trace>` subcommands. Ship exit codes: 0 pass/escalate, 1 block, 2 error. Eval: 0 gate pass, 1 quality failure, 2 error.
- `internal/tracelens/example_test.go` — the old `cmd/demo`, folded into a pinned `Example` over a built-in messy trajectory.
- `docs/` — pitch, decoding architecture, evaluation methodology, and parked scope.
- `testdata/` — the labeled corpus (all four dialects) + a real persisted ship run.

Mechanism vs policy: detectors (`detect.go`) report findings; `buildReport` (`analyze.go`) owns thresholds, ranking, and the decision/tier rules. Adding a pathology means adding one `Detector`, nothing else.

The guard test `TestAnalyze_LoopMakesRunPathological` fails if the detector core is stubbed to return no findings — keep that property: a green suite must certify the detectors actually ran.

## Checks

```
gofmt -l . && go vet ./... && golangci-lint run ./... && go test ./...
```

Standard library only; the sole in-module import is `contracts` — the shared verdict vocabulary tracelens emits (`contracts.Verdict` / `contracts.Finding`). Decision logic (the tier ladder, `decisionTier`, the detectors) stays here; `contracts` carries none. The golden test `TestVerdictJSON_Golden` pins the emitted verdict JSON byte-for-byte — output drift on the wire surface fails the suite.
