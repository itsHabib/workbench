# Cloud backend eval — gateway validation

The frozen 51-row ci-classify eval ran through the Anthropic-native gateway
path with `claude-sonnet-4-6`. The gateway endpoint and credential were supplied
only at runtime.

## Results

| Metric | Result | Acceptance bar |
|---|---:|---:|
| Coverage | 47/51 = **92.2%** | ≥ 60% |
| On-handled accuracy | 45/47 = **95.7%** | ≥ 90% |
| Escalated | 4/51 = **7.8%** | informational |

The deterministic floor handled 20 rows at 100% precision. The model advisory
handled 27 rows at 93% accuracy. Two trusted advisory classifications were
wrong; four model answers escalated because their evidence was not verbatim.

The score matches the Phase-0 Haiku reference (92.2% coverage / 95.7%
on-handled), but the comparison is indicative rather than apples-to-apples
because this run used a different model.

## Security check

The generated JSONL contains 51 rows. Exact-match scans found neither the
runtime API key, the configured base URL, nor its hostname in the output. No
gateway-specific configuration was written to stderr or verdict artifacts.

## Reproduce

```bash
export ANTHROPIC_BASE_URL=...   # gateway origin + provider prefix
export ANTHROPIC_API_KEY=...    # gateway-issued token
export GATE_CLOUD_MODEL=claude-sonnet-4-6
go run ./cmd/gate/docs/features/ci-classify/eval/run-cloud \
  -eval-dir cmd/gate/docs/features/ci-classify/eval \
  -out cmd/gate/docs/features/ci-classify/eval/ci-eval-raw.gateway.jsonl
pwsh cmd/gate/docs/features/ci-classify/eval/floor-score.ps1 \
  -s cmd/gate/docs/features/ci-classify/eval \
  -raw ci-eval-raw.gateway.jsonl
```

`run-cloud` keeps metadata opaque so it can replay the frozen dataset's string
metadata without imposing an unrelated object shape.
