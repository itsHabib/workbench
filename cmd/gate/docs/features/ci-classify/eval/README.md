# ci-classify eval — reproducible bundle

Frozen inputs + harness for the eval gate that precedes `../spec.md`. Vendored here so the
spec's §1 claims (7B: coverage 80.4% / on-handled 90.2%; 14B: 82.4% / 100%; floor 20-of-20)
are reproducible from this checkout alone. `floor-score.ps1` scores under shipping semantics
(§6 verifier applied to advisory rows; demoted signatures excluded) — its header records the
cycle-2 correction that produced this framing.

- `ci-lines-v2.jsonl` — the 51-row dataset (41 real failed-step chunks from 41 CI runs across
  7 repos + 10 isolated short lines). `{input, expected, meta}` per line.
- `ci-classify.prompt.txt` — the tuned v2 prompt (locked; the one the GO numbers used).
  `ci-classify.prompt.v1.txt` — the minimal v1, for the tuning delta (82.4% → 84.3%).
- `ci-classify.schema.json` — the extract-shaped advisory schema `{bucket, evidence, why, confidence}`.
- `labels.tsv` — ground-truth bucket per source run (audit key; never fed to the model).
- `build/` — the log→chunk extractor (blind-tail baseline the eval measured; `stepWindow` inside
  it is the naive error-anchor variant that measured *worse* — kept for the design record).
- `score.ps1` — bare-classifier scoring: accuracy, verbatim rate, confusion matrix, misdirection audit.
- `floor-score.ps1` — the floor+advisory combined scorer; the seed signature table lives at its top.
- `ci-eval-raw.7b.jsonl` / `ci-eval-raw.14b.jsonl` — the two locked model runs (bare
  all-rows: 84.3% / 88.2%; shipping-semantics results per the spec §1 table).

`.gitattributes` pins every file `-text`: the verbatim-evidence verifier is a byte-level
substring check and CRLF rewrites silently break it.

## Reproduce

```
# from a checkout of github.com/itsHabib/local (Ollama running, qwen2.5:7b pulled)
go run ./cmd/eval -prompt @<here>/ci-classify.prompt.txt -schema @<here>/ci-classify.schema.json \
  -dataset <here>/ci-lines-v2.jsonl -field bucket -verbatim evidence [-model qwen2.5:14b] -jsonl > <here>/raw.jsonl
pwsh <here>/score.ps1 -raw raw.jsonl       # bare classifier (omit -raw to score the vendored 7B run)
pwsh <here>/floor-score.ps1 -raw raw.jsonl # floor + advisory, shipping semantics
# both scorers resolve -raw against their own dir (<here>), so write the run there, not the cwd.
```

Re-run on any model or prompt change — the verdict is only meaningful next to the exact
prompt + schema that produced it. Full analysis narrative: the operator's eval record
(`s2-ci-classify-gate-2026-07-06`), summarized in `../spec.md` §1.
