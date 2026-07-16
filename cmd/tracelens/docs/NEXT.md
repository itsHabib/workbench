# NEXT — parked scope

Shipped v0 is a green vertical slice: parse JSONL trace → 5 detectors → ranked
verdict with evidence + repair, plus a demo and a real CLI. Parked, in rough
priority order:

## Detection
- **Evidence-backed normalization expansion.** Semantic loop detection ignores
  a small explicit set of volatile producer metadata today. Add a new category
  only with a positive corpus case and a hard negative; page numbers remain
  meaningful progress by default.
- **Oscillation vs. progress-with-repeats.** A run can legitimately revisit a
  tool; distinguish "stuck oscillation" from "healthy re-check" using a
  progress-toward-goal signal, not just novelty of state.
- **Latency storms.** Same shape as retry storms but on `latency_ms` tails —
  flag a tool whose p95 blows up mid-run.
- **Token→cost table.** Derive `cost_usd` from `tokens_in/out` and a pricing
  map when a producer only logs tokens.

## Ingestion
- **More producer dialects.** Neutral JSONL and Ship's Cursor, Claude, and
  Codex streams are supported. Add another adapter only from a concrete
  persisted format; do not grow a speculative plugin framework.
- **Ship telemetry unlock.** Ship's streams carry no per-step cost/tokens, no
  tool-failure marker, and mostly no timestamps, so cost-hotspot and
  retry-storm detectors stay dormant on ship traces. A ship-side task
  (dossier project `ship`, `trace-telemetry-gap`) tracks persisting those;
  when it lands, the adapter maps them and both detectors light up.
- **Streaming ingest.** Analyze an in-flight run and emit a finding the moment a
  loop crosses threshold (early-kill signal), instead of post-hoc only.

## Output
- **Baseline diffing.** Compare a run against a "known-good" run of the same
  task and report regressions (more steps, new hotspots).
- **Severity budget policy.** Config to promote/demote kinds per team (e.g.
  treat any stuck run as CRITICAL in CI) and a non-zero exit code for gating.
  The `ship` subcommand gates via exit code today (`block` → 1); the
  generic mode and per-kind promotion policy are still open.
- **Self-contained HTML.** Markdown reports are portable and deterministic.
  Add HTML only if a real inspection workflow needs richer navigation.
