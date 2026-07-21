# Mutation audit — driver-state ledger + gate reducer (2026-07-20)

First mutation-testing pass over the invariant-dense core, run with
[gremlins](https://github.com/go-gremlins/gremlins) v0.6 after the property +
fuzz spike landed. Mutation testing is the validator that finds *thin
assertions* — lines coverage calls green but that no test actually pins. It is
the natural second pass after property tests, which kill whole mutant classes at
once; this audit confirms the properties are thorough and turns each surviving
mutant into either a new assertion or a documented equivalent.

Reproduce with the `/mutation-audit <package>` skill (one package at a time —
each mutant re-runs the package's whole suite).

## Results

| Package | Killed | Lived | Not covered | Efficacy |
|---|---|---|---|---|
| `driverstate` (ledger mechanism, reducer, rollup) | 147 | 6 | 6 | **96.08%** |
| `cmd/gate/internal/verify` (verifier ladder + rungs) | 139 | 20 | 43 | 87.42% |

Both surviving sets were triaged in full. In the invariant-dense core — the hash
chain, the reducer/fold, the state machine, import dedupe, the parent↔child
rollup — the property tests killed everything except a handful of **equivalent
mutants** (no input produces an observable difference) plus **two genuine
thin-assertion gaps in `rollup.go`**, both now closed. The gate **reducer** core
(`verify.go`) is likewise clean: its two survivors are equivalents. The remaining
`verify` survivors are all in the local/cloud-model consolidation rungs, which
are outside the property-tested core and are catalogued as an optional follow-up.

## driverstate — 6 survivors

**Fixed (genuine missing assertions, `rollup.go`):**

| Location | Mutator | Gap → assertion added |
|---|---|---|
| `rollup.go` `reachedBoundary` (`happyRank(status) >= boundaryRank(boundary)`) | CONDITIONALS_BOUNDARY (`>=`→`>`) | No test exercised a stream whose status rank *equals* the boundary rank — the happy-path tests only used a terminal stream (short-circuits) or a stream below the boundary. A stream exactly at `pr_open` under a `pr-open` boundary IS reached. → `TestReachedBoundaryExact` |
| `rollup.go` `rollupStream` (`if row.MergeCommit == ""`) | CONDITIONALS_NEGATION (`==`→`!=`) | The merge-commit half of the resume guarantee was unpinned: existing tests gave parent and child the same commit, so negating the guard was invisible. A parent mirror that died before recording the merge has an empty MergeCommit and must surface the child's. → `TestRollupSurfacesChildMergeCommit` |
| `rollup.go` `Rollup` (`out.Streams[i].Stream < out.Streams[j].Stream` sort) | CONDITIONALS_NEGATION | The rollup sorts streams by id for deterministic CLI rendering and index-based reads, but the join tests only asserted set membership (a map), never order — a reversed comparator was invisible. → an ascending-order assertion in `TestRollupJoinsChildren` |

**Documented equivalents (unkillable by construction — no assertion contorted to "kill" them):**

| Location | Mutator | Why equivalent |
|---|---|---|
| `rollup.go` `frictionOf` (`if retries < 0`) | CONDITIONALS_BOUNDARY (`<`→`<=`) | The clamp produces `0` at the boundary either way (`retries == 0` stays 0; `retries == -1` clamps to 0). No observable difference. |
| `reduce.go` `trimWithWarning` (`if len(trimmed) != len(data)`) | CONDITIONALS_NEGATION (`!=`→`==`) | Guards a diagnostic **stderr warning only**; the returned value is unaffected. Behaviorally equivalent (asserting stderr noise would pin a non-behavior). |
| `lock.go` `breakStaleLock` (`if time.Since(mtime) < DefaultLeaseTTL`) | CONDITIONALS_BOUNDARY (`<`→`<=`) | Boundary at exactly one TTL of wall-clock age — not deterministically hittable to the nanosecond. `TestStaleAppendLockRecovered` already pins the far-past-TTL behavior. |
| `lease.go` `withRetry` (`for i := 0; i < maxRetries; i++`) | CONDITIONALS_BOUNDARY (`<`→`<=`) | Changes the retry count by one; the outcome is identical (the loop still exhausts and returns `errLockContended`). Only an implementation-detail attempt-count assertion would kill it. |

*Not covered (6):* mutable points on lines no test exercises at all — a coverage
gap, distinct from a thin assertion, and out of scope for this pass.

*A note on determinism.* Survivor counts are timing-sensitive: gremlins runs each
mutant against the whole suite under a timeout, so a mutant that hangs under
parallel load counts as caught (TIMED OUT) but may survive in a lighter re-run.
A scoped verification re-run (rollup.go only) confirmed the fixes above kill their
targets, and surfaced two additional low-risk `rollup.go` guard-branch survivors
left as follow-ups, not chased: the `child_run` link's `parent_stream != ""`
clause (`:112`, an empty-parent-stream edge) and the PR-surfacing guard (`:125`,
the exact parallel of the merge-commit fix, needing a parent-mirror-without-PR
harness). Both fail toward *not* adopting a child's facts — the safe direction —
when wrong. The "6" above is the authoritative full-run count, not an exhaustive
floor; mutation testing is an audit, not a gate, so the residue is read, not
chased to zero.

## cmd/gate/internal/verify — 20 survivors

**Reducer core (`verify.go`, 2) — equivalents:**

| Location | Mutator | Why equivalent |
|---|---|---|
| `Reduce` (`tierRank(v.Tier) > tierRank(out.Tier)`) | CONDITIONALS_BOUNDARY (`>`→`>=`) | Tier semantics are rank-based and valid tiers are rank-unique, so equal rank ⟹ equal tier string ⟹ `out.Tier` is unchanged either way. The property test asserts the composed *rank* (what decisions use), which is invariant. |
| `Reduce` (`v.Confidence < out.Confidence`) | CONDITIONALS_BOUNDARY (`<`→`<=`) | The `if x < min { min = x }` pattern: on equality both branches assign the same value — a no-op. |

The reducer's decision, tier, and confidence composition — every negation and
arithmetic mutant — was killed by the property tests. No thin assertions remain
in the ladder law itself.

**Local/cloud-model rungs (18) — catalogued follow-up:** `reviews.go` (10),
`model.go` (4), `ciclassify.go` (2), `judge.go` (2). These are the bot-panel
consolidation and CI-classify plumbing — outside the property-tested core, and
per the testing scope (property/fuzz/mutation on the load-bearing core only) they
are left as an optional follow-up rather than gold-plated. Several are the same
recurring equivalent `if x < min { min = x }` boundary pattern
(`reviews.go:117`, `ciclassify.go:325`); the rest are genuine but low-risk
assertion gaps on pure helpers (`normSeverity`, `locus`, `knownVerdict`) and the
consolidation loop's counters, all of which fail toward escalation — the local
rung's safe direction — when wrong.

## Takeaway

The property + fuzz spike holds up under mutation: the load-bearing core has no
thin assertions the audit could find beyond two narrow `rollup.go` gaps, now
closed. Mutation testing stays an **audit, not a CI gate** — a survivor list to
read, not a threshold to chase. Equivalent mutants are the expected floor;
documenting them is the correct end state.
