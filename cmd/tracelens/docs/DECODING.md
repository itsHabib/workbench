# Trace decoding architecture

Tracelens separates input mechanisms from detector and verdict policy:

```text
raw trace
  -> dialect detection / selected decoder
  -> Trajectory + dialect provenance
  -> deterministic detectors
  -> Report + gate-aligned Verdict
  -> terminal, JSON, Markdown, or corpus evaluation
```

`DecodedTrace` carries the normalized `Trajectory` and `Dialect`. Detectors see
only the trajectory; no detector branches on producer format.

## Supported formats

| Dialect | Lifecycle model | Missing terminal event |
|---|---|---|
| `neutral-jsonl` | one normalized step per line | represented as supplied |
| `ship-cursor` | `tool_call` events pair by `call_id` | `OK == nil` |
| `ship-claude` | assistant `tool_use` pairs with user `tool_result` by `tool_use_id` | `OK == nil` |
| `ship-codex` | `item.started` pairs with `item.completed` by item ID | `OK == nil` |

Interleaved calls retain start order. Unknown events within a recognized
dialect are ignored only when they have no normalized step meaning. Malformed
JSON reports its line number. Empty, unrecognized, and mixed-dialect streams
fail closed.

Producer-declared terminal failures (codex `turn.failed`, a claude `result`
with `is_error` or an error subtype) set `Trajectory.DeclaredFailure`; the
`RunFailureDetector` turns it into a Critical `run_failure` finding, so a run
the producer itself declared failed blocks even when every tool step
succeeded. `ship.*` control events (`ship.resumed`) are dialect-neutral, as is
the `assistant` envelope — both the cursor and claude dialects emit it.

Neutral JSONL may provide explicit `i` evidence indices. They must be unique;
duplicates fail with the offending line and first-use line rather than letting
report evidence silently resolve to the wrong step.

The decoder seam is intentionally small. It is not a registration framework:
the common shape was introduced only after three concrete Ship dialects made
the lifecycle pattern visible.

## Telemetry rules

Decoders never invent telemetry. Aggregate turn/run token or cost values are
not assigned to individual steps. An absent tool completion remains unknown,
not success or failure. These rules keep downstream findings reproducible and
prevent cost-hotspot or retry-storm claims unsupported by the producer.

## Semantic loop normalization

Exact `(tool,args)` identity remains the base behavior. The default analyzer
also ignores these explicit producer-metadata keys when detecting loops:

```text
call_id request_id run_id session_id timestamp trace_id ts
```

Values are replaced recursively with an inspectable `__volatile__` sentinel
before stable JSON encoding. Page numbers, offsets, paths, commands, arbitrary
`id` fields, and all other arguments remain exact because they may represent
real progress. A semantic match blocks only when every cycle position also
repeats the same confirmed outcome; changing observations are progress.
Exact-only behavior remains available by setting
`Config.KeepVolatileArgs` to true; the zero value keeps normalization on.

Any expansion of this list requires a positive labeled case and a nearby hard
negative. Generic JSON edit distance is deliberately rejected: it would make
healthy pagination and iterative repair look like loops.
