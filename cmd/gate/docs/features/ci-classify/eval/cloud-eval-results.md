# Cloud backend eval — pending operator run

The pluggable cloud backend (`-model-backend cloud`) and the `run-cloud` harness are
ready, but this CI environment has no `ANTHROPIC_API_KEY`, so the frozen
ci-classify eval could not be executed here.

## Reproduce (operator / CI with secret)

```bash
export ANTHROPIC_API_KEY=...
go run ./docs/features/ci-classify/eval/run-cloud \
  -out docs/features/ci-classify/eval/ci-eval-raw.haiku-cloud.jsonl
pwsh docs/features/ci-classify/eval/floor-score.ps1 -raw ci-eval-raw.haiku-cloud.jsonl
```

Acceptance bars (shipping semantics): coverage ≥ 60%, on-handled accuracy ≥ 90%;
Phase-0 Haiku reference: 92.2% coverage / 95.7% on-handled.

After scoring, replace this note with the raw percentages and a one-paragraph
comparison to Phase-0.
