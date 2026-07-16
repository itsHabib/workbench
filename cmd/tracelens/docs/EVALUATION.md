# Evaluation methodology

Tracelens is deterministic, but deterministic does not mean correct. The
committed corpus makes detector quality inspectable and prevents tuning against
one impressive demo.

## Run the gate

```sh
go run ./cmd/tracelens eval cmd/tracelens/testdata/corpus
go run ./cmd/tracelens eval -json cmd/tracelens/testdata/corpus
```

The command exits 0 when the checked-in policy passes, 1 for a label or metric
failure, and 2 for an input/infrastructure error. A missing fixture, invalid
schema version, or declared/detected dialect mismatch is an infrastructure
error rather than a false negative.

## Corpus contract

`testdata/corpus/corpus.json` is schema version 1. Every case records:

- a stable ID and relative trace path;
- its declared dialect;
- provenance: `real-sanitized`, `derived`, or `synthetic`;
- expected and explicitly tolerated pathology kinds;
- an optional expected decision;
- a rationale, tags, and known limitations.

An expected finding earns a true positive when observed and a false negative
when absent. An unexpected, non-tolerated finding is a false positive. A
tolerated finding is neutral: it neither earns credit nor incurs a penalty.
Toleration is used only when a fixture intentionally activates another valid
detector—for example, a retry storm of identical calls is also an exact loop.

Labels are case-level, not finding-count-level. Three loop findings in one case
still count as one observed `loop` label. This measures whether a detector
recognizes a pathology, not whether it fragments the evidence optimally.

## Checked-in policy

The corpus currently requires:

- macro precision of at least 0.90 across pathology kinds with defined
  precision;
- recall of 1.00 for curated `loop` and `stuck` cases;
- no critical finding on any case tagged `healthy`;
- every case to decode under its declared dialect;
- all expected decisions to match when specified.

Precision or recall with a zero denominator is `n/a`, never `0%`. Undefined
metrics do not silently enter macro precision. A required recall metric that is
undefined fails the gate.

## Adding a case

Add a case only when it contributes a distinct boundary:

1. Sanitize the trace. Remove credentials, private prompts, personal data,
   customer data, and machine-specific paths that do not matter to behavior.
2. Identify provenance honestly. A trace recreated from an observed schema is
   `derived`, not `real-sanitized`.
3. Write the expected labels before changing detector code.
4. Add a hard negative beside any new heuristic.
5. Run the human and JSON evaluation commands.
6. Explain any tolerated finding in the case rationale or surrounding cases.

Do not add easy synthetic variants merely to increase the case count. The
corpus is deliberately small enough to review line by line.

## Current limitations

- The corpus is curated and small. Its perfect current scores are regression
  evidence, not a population-level accuracy estimate.
- Labels are maintained by the repository owner, not independent annotators.
- The real sanitized corpus coverage is currently strongest for Ship Cursor;
  Claude and Codex fixtures are derived from locally observed persisted schemas.
- Case-level metrics do not score evidence-span quality or duplicate findings.
- Ship Claude/Codex streams expose some aggregate usage, but Tracelens does not
  fabricate per-step attribution from aggregate totals. Cost and retry analyses
  remain limited by producer telemetry.

