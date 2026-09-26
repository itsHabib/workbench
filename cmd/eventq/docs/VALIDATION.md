# Validation — 2026-09-26

Host: Apple M5, darwin/arm64. Default build Go 1.26.5; experimental build Go
1.27.1 with GOEXPERIMENT=simd. No global toolchain setting changed.

## Real workload

A private local Codex archive, snapshotted at 2026-09-26T14:06:02Z:

| Measurement | Observed |
| --- | ---: |
| Source JSONL files | 631 |
| Source bytes consumed | 3,290,871,964 |
| Completed command rows | 52,580 |
| Index bytes | 12,402,630 |
| Build duration | 31.775 s |
| Incomplete tails omitted | 0 |
| Nonzero command exits | 4,323 |
| Recorded duration >= 30 seconds | 1,728 |
| Both predicates | 535 |

The independent Python verifier reread every fingerprinted source prefix and
matched counts, all grouped counts by cwd, total recorded duration, and the five
largest durations. All source prefix SHA-256 hashes matched the index manifest.
Its full raw parse took 7.149 s. Four sequential scalar CLI count invocations
took 411.163, 31.923, 29.875, and 28.548 ms including process startup, file load,
checksum validation, query execution, and JSON output. The first invocation was
slower; these are warm-filesystem observations, not controlled cold-disk tests.

This demonstrates useful query reuse, not a SIMD speedup over JSON or superiority
to an indexed database. Raw input privacy and index snapshot limitations are
described in the README. No raw traces or index are committed. Reproduce on
your own archive with scripts/verify-codex.py.

## SIMD experiment

BenchmarkQuery runs the complete in-memory count query, including selection-mask
allocation, initialization, two predicates, and reduction. Data are synthetic:
duration cycles 0..99,999 and exit code cycles 0..6. Both paths run in the same
Go 1.27.1 SIMD-enabled binary, with `--scalar` semantics selecting the reference.

After the AVX-512 portability fix, one million rows across three repetitions
measured scalar 1.845–1.891 ms versus SIMD 1.792–1.811 ms: roughly 4% lower
median query time for this workload. Both allocate
about 8 MB per query. At 50,000 rows, timings varied enough (about 0.08–0.16 ms)
that we do not claim a meaningful SIMD advantage for the current archive.
Loading the index costs much more than either scan.

Disassembly of the actual backend confirms native ARM NEON `VCMEQ`, `VCMGT`,
and `VAND` instructions. Moving operator dispatch outside each vector loop was
necessary: the initial implementation's per-vector string switch was slower
than scalar. AMD64 CI then exposed a Go 1.27.1 compiler/assembler failure for
three chained logical masks. Replacing that expression with equivalent mask
arithmetic fixes the cross-build, with the modest performance shown above.
Tests also run with `GODEBUG=simd=0` to cover portable emulation.
Default installation therefore remains scalar; SIMD stays an opt-in experiment.

## Correctness checks

- Module-wide Go tests, vet, and configured lint passed locally.
- eventq race tests passed with scalar and experimental SIMD builds.
- SIMD/scalar differential tests cover all operators, lengths 0–130, signed
  extrema, null masks, previously filtered masks, and non-vector-sized tails.
- Exact large integers, null-versus-empty strings, missing fields, malformed
  queries, integer overflow, bounded output, grouped reductions, and top-K ties.
- No-clobber output, mode 0600, round trips, truncated/corrupted indexes,
  invalid presence masks even with recomputed checksums, and payload sentinels.
- Incomplete versus malformed tails, missing duration members, and early
  dictionary budget rejection; independent reviewer reproduced 20 CLI cases.
- Bounded fuzz runs: 20,031 query-parser executions and 20,000 index-decoder
  executions, with no failures. These are smoke tests, not exhaustive assurance.

The dedicated CI workflow exercises the opt-in backend on amd64 and forced
emulation. Local evidence is ARM64; remote CI results must be checked separately.
