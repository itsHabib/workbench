# eventq

Find slow commands and nonzero exits across local agent history without reparsing
gigabytes of JSON for every question. Build a metadata snapshot once, then query
typed columns with exact source-file and line references.

```sh
go install ./cmd/eventq
eventq build --codex --allow-partial-tail --out commands.eq "$HOME/.codex/sessions"
eventq query --where 'exit_code != 0' --group cwd commands.eq
eventq query --where 'duration_ms >= 30000' --top duration_ms --limit 10 commands.eq
eventq query --where 'day == "2026-09-26" AND exit_code != 0' --group cwd commands.eq
eventq query --where 'duration_ms >= 30000 AND exit_code != 0' --json commands.eq
eventq schema commands.eq
```

Flags precede the index/input paths. `--limit` defaults to 20; counts and sums
always cover all matching rows. `--count` omits rows. `--group` sorts groups by
count, descending, with deterministic ties. `--top` uses a bounded heap to retain
the largest numeric values; nulls sort last, ties retain source order.

The Codex preset selects completed `CommandExecution` records. It retains only
timestamp, UTC day, session ID, item ID, cwd, status, duration_ms, exit_code,
source, and line. **Commands, prompts, arguments, and output bodies are not
stored.** Working directories remain file URLs as recorded by Codex. Source
paths and metadata are still private information: keep indexes local. Files are
created mode 0600; no network, daemon, credentials, or model is needed.

`source` and `line` point to the original JSONL record when deeper inspection is
needed. Nonzero exits include expected search misses and deliberate negative
tests. Summed command durations include concurrent/background work; they are
recorded runtime, not elapsed human time, cost, or wasted time. Milliseconds are
floored per command, so submillisecond commands have duration 0.

## Other JSONL producers

Select fields explicitly. Integers stay exact, including values beyond float64's
exact range. Wrong types fail with source and line; missing/null stays missing.

```sh
eventq build --out events.eq \
  --column duration=metrics.elapsed_ms:int \
  --column kind=kind:string \
  ./logs
eventq query --where 'kind == "test" AND duration >= 500' --sum duration events.eq
eventq query --where 'duration IS NULL' --count events.eq
```

Query grammar: comparisons joined by `AND` or `&&`; integer operators
`== != < <= > >=`; string operators `== !=`; `IS NULL` / `IS NOT NULL` for
presence. Strings use double quotes. Every comparison against missing/null is
false, including `!=`. There is no SQL execution, shell evaluation, OR, joins,
arrays, floating point, or general expression language.

## Snapshot semantics and limits

Indexes are frozen snapshots, labeled with their build time, not live status.
Build to a new filename to refresh; existing paths are never overwritten.
Each file is read only through its size at open, and its consumed prefix is
SHA-256 fingerprinted. Files are visited in sorted absolute-path order. Repeated
paths are deduplicated; copied events in different files are not. No event-ID
deduplication or exactly-once claim is made.

An input changing in place is not locked or atomically captured across files.
Later appends are excluded. Use an archive or stopped producer when a globally
consistent cutoff is required. Fingerprints record the bytes consumed; querying
an index does not revalidate current source files or source locations.

Malformed records fail. `--allow-partial-tail` explicitly tolerates unexpected
JSON EOF on a file's unterminated final record; its count remains visible in
build/query output. Other syntax errors still fail. Blank lines are ignored.
Input symlinks are rejected. The v1 bounds are 32 MiB per JSONL line, 30 selected
columns, 10 million rows, 256 MiB of dense column payload, 8 MiB of distinct
dictionary string bytes, a 16 MiB encoded header, and a 1 GiB index. The ingestion
bounds limit retained data, not total process RSS; Go slice growth, dictionaries,
JSON decoding, and serialization add overhead. Partition larger workloads.

## The low-level part

The compiler resolves field names, types, and string literals once, yielding a
plan of column kernels. Strings become dictionary codes; nullable integer/code
columns use contiguous int64 values and presence masks. There is no per-row AST
interpreter or JIT. Predicates combine selection masks; reductions operate on
the result. Integer sums detect overflow. The binary format includes a version,
bounded schema, packed little-endian columns, and an integrity checksum.

Default builds use Go 1.26-compatible scalar kernels. An optional Go 1.27 backend
uses the experimental portable SIMD API, with scalar tails and a scalar oracle:

```sh
GOEXPERIMENT=simd GOTOOLCHAIN=go1.27.1 go build -o /tmp/eventq-simd ./cmd/eventq
/tmp/eventq-simd query --where 'duration_ms >= 30000' --count commands.eq
/tmp/eventq-simd query --scalar --where 'duration_ms >= 30000' --count commands.eq
```

The runtime chooses hardware SIMD or emulation; `simd-experimental` reports the
compiled backend, not a guarantee of native instructions. See the
[Go SIMD API](https://go.dev/blog/simd-experiment). Experimental APIs can change.
No global Go upgrade is needed.

The main gain on today's corpus comes from projection/index reuse. SIMD is a
separate, modest optimization; JSON ingestion, index loading, grouping, and
serialization can dominate. This is not a claim to outperform a general database.
See [validation](docs/VALIDATION.md) for measured boundaries and reproduction.

## Validate

```sh
go test -race ./cmd/eventq/...
go vet ./cmd/eventq/...
golangci-lint run ./cmd/eventq/...
GOEXPERIMENT=simd GOTOOLCHAIN=go1.27.1 go test ./cmd/eventq/...
GODEBUG=simd=0 GOEXPERIMENT=simd GOTOOLCHAIN=go1.27.1 go test ./cmd/eventq/...
GOEXPERIMENT=simd GOTOOLCHAIN=go1.27.1 go test ./cmd/eventq/internal/query -run '^$' -bench BenchmarkQuery -count=3
python3 cmd/eventq/scripts/verify-codex.py /absolute/path/to/eventq /absolute/path/to/commands.eq
```

The last check independently parses the exact original prefixes and compares
counts, grouped counts, sums, and top five durations. It requires unchanged
prefixes and zero ignored tails. It emits aggregate measurements, never payloads.
Exit codes: 0 success (including no matches); 2 input, query, or I/O error.
