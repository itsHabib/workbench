# Metadata query engine

Outcome: a local operator or agent can query command history repeatedly and
return evidence locations without copying sensitive command/output bodies.

`eventq build` projects explicitly selected fields into an immutable binary
index. The Codex adapter recognizes completed command records and retains a
small metadata allowlist. Generic ingestion requires named, typed columns.
`eventq query` compiles conjunctions into integer/dictionary scan kernels and
reduces their selection mask into counts, sums, grouped counts, or bounded rows.
`eventq schema` explains the stored projection. There is no new telemetry writer.

The private query package owns storage and execution; no other Workbench tool
imports its decisions. Integration is through this CLI's exit codes and JSON.
It does not replace Fleet's live status/cost surfaces or Tracelens diagnostics.

Explicit choices: dense int64 columns for a small uniform kernel; dictionary
strings rather than repeated allocations; presence masks rather than sentinel
numeric values; stable scalar backend plus opt-in SIMD; bounded top-K heap;
whole-index loading rather than mmap or a server. The latter is adequate for
the measured ~12 MB index and keeps lifetimes, integrity checking, and deployment
simple. Incremental updates, compressed masks, block pruning, fused multi-column
kernels, and an interactive server require an observed workload before addition.

Presence-only predicates deliberately use the scalar kernel. Other predicates
use vector comparisons and masks when built with SIMD. Go 1.27.1 miscompiles a
three-way integer AND to invalid AVX-512 assembly; the portable backend combines
0/-1 presence/selection lanes using a sum-equals-minus-two test instead. Both
representations have the same truth table. Cross-architecture CI must stay on.

The query grammar is deliberately limited. Compile-time schema/literal errors
cannot silently select everything. Missing values never satisfy comparisons.
Source bytes are read only and fingerprinted; indexes are private, immutable,
atomically published without replacing existing paths. A checksum detects
accidental corruption, not malicious tampering by someone who can rewrite it.
