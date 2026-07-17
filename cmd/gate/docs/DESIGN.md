# gate — design

Status: v0
Date: 2026-07-05

## Problem

Merging agent-authored PRs is the highest-stakes seam in an agent-driven
workflow, and today the rules that govern it live in prose — skill documents,
reviewer conventions, operator habit. Prose rules have two failure modes: an
agent can misread them, and nothing structural stops a well-meaning step from
skipping them. The failures that motivated this tool were exactly that shape:
a merge recorded without verification, review-cycle policy drifting between
skill revisions, and no way to reconstruct *why* a given PR merged.

`gate` moves the merge decision from prose into one small binary with an
auditable state trail. It gates; it does not dispatch work, manage worktrees,
or own project memory — those stay in the existing tools it composes with.

## Shape

One Go binary. Verbs: `grant`, `gate`, `judge`, `explain`,
`audit`, `backtest`, `stress`. Callers integrate via exit codes and JSON on
stdout, never prose. Packages, dependencies pointing strictly downward:

| package | responsibility |
|---|---|
| `internal/state` | append-only, hash-chained, fs-locked artifact log — the substrate everything writes through |
| `internal/capability` | HMAC-signed grants: scoped, timed, tier-capped |
| `internal/evidence` | real GitHub reads via `gh`, recorded as evidence artifacts |
| `internal/verify` | the verdict schema, the verifier rungs, the monotone reducer, the auto-judge |
| `internal/observe` | `explain`/`audit` — read-only, storeless, state-fed |
| `cmd/gate` | composition: wires one vertical pass per invocation |

## The artifact contract

Every step of a gate run is an `Artifact`: typed (`evidence`, `verdict`,
`grant`, `action`, `escalation`, `judgment`), grouped by run id, with explicit
provenance (`Parents` — a verdict names the evidence it judged; an action
names the verdict and grant that authorized it), hash-chained to the previous
log entry. Consequences the code enforces:

- **An unverified action is unrepresentable.** An action artifact's parents
  must name a reducer verdict and a live grant; there is no code path that
  records an outcome without them.
- **Observability needs nothing but state.** `Explain` reconstructs the full
  decision chain from the log alone; `Audit` replays the hash chain, names the
  first tampered artifact, and checks the replayed head and count against a
  keyed anchor (see *Tamper model* below). If either needed a side channel, the
  contract would be leaking.
- **An artifact that parks work carries everything the eventual judge needs.**
  The reducer aggregates every escalating verifier's reasoning into the
  composed verdict, and the escalation artifact embeds the full question.
  Parking with a pointer back into prose is the leak this design exists to
  prevent.

### Concurrency

The log is guarded by an exclusive lock file (`O_CREATE|O_EXCL`, stale
takeover, every acquisition error retried until deadline). The retry-all
posture is load-bearing on Windows: a racing create during lock release fails
`ACCESS_DENIED` (delete-pending), not `EEXIST`, and treating that as fatal
loses writes under contention. `TestConcurrentAppendKeepsChainIntact` and the
`stress` verb pin this cross-goroutine and cross-process.

### Tamper model

State the boundary plainly, matched 1:1 to what `Audit` enforces. The hash
chain is tamper-*evidence*, not access control or non-repudiation.

- **Catches (chain replay, unkeyed).** Naive body edits — the stored `Hash` no
  longer matches the recomputed one. Broken `Prev` links. Mid-log reordering —
  a moved entry's `Prev` stops matching its new neighbour.
- **Did not catch, before the keyed anchor.** Tail truncation and whole-log
  deletion both replayed as an intact prefix and reported "chain intact" — the
  chain has no notion of how long it is supposed to be. Wholesale
  rewrite-with-rehash: the chain is unkeyed SHA-256, so anyone with file-write
  can rebuild the log end-to-end, recomputing every `Prev`/`Hash`, and a pure
  replay accepts it. A last-entry edit is the rewrite case with a suffix of one.
- **Closed now, by the keyed anchor.** A small anchor record — `HMAC(key,
  head ‖ count)` plus the expected head and count — is written atomically
  (temp + rename) under the same lock as every `Append`, and lives **outside**
  the state dir. `Audit` holds that same lock across both the chain replay and
  the anchor check, so a concurrent writer can't tear the two into a false
  positive; it then verifies the replayed head and count against the anchor.
  A rewrite fails (`rewrite:`) because the attacker with only state-dir write
  cannot forge the HMAC. Truncation fails (`truncation:`) because the replayed
  count is short of the pinned count; whole-log deletion is called out
  distinctly (`deletion:`) — the anchor expects `N ≥ 1` against an empty or
  absent log. A log one entry ahead of the anchor is the benign crash window
  (entry synced, anchor rename not yet landed) and is named `incomplete-append:`
  rather than overstated as a rewrite. It fails the audit until the next
  successful `Append`, which reconciles the count from the real log length (the
  append reads the true tail; when that doesn't match the anchor's recorded
  head, `rebind` recounts instead of blindly incrementing) — so a single crash
  cannot brick audit permanently. The anchor key
  is *loaded, never minted* on the verify path (a missing key is a loud
  `anchor_key_missing`, mirroring the grant key); minting is a first-append
  concern only. The anchor *record* is per-state-dir (its filename carries a
  hash of the absolute state dir), so several logs can share one key dir without
  one log's appends invalidating another's audit; the key itself is shared,
  since a secret cannot be forged regardless of how many logs it anchors.

- **Trust boundary.** Every agent on this box has shell access, so file-write to
  the state dir is in-scope for the realistic adversary — which here is drift
  and accidental corruption, not a funded attacker. Two custody facts make the
  anchor meaningful against that adversary: the anchor key and the grant signing
  key live outside the state dir (default: the user config dir; `-key`
  overrides), so writing `log.jsonl` does not grant the ability to forge the
  anchor or mint grants. This is **not** cryptographic non-repudiation: an actor
  who can read the key directory can still forge both. The claim is bounded —
  tamper-evident against accidental and naive modification, and, after this
  change, against rewrite and truncation by a state-dir-only writer.

**Explicitly untouched** (noted, not fixed here): the stale-lock TOCTOU takeover
race (a second writer can win the lock between staleness check and re-create)
and the single-file-log durability model (no SQLite/WAL). Both are recorded in
`docs/FOLLOWUPS.md`; neither is a truncation/rewrite-integrity or key-custody
concern, so both are out of scope for this pass. The `lock.go` staleness clock
*was* threaded through the store's injected `now` as part of this change, so
takeover is now testable without real-time sleeps.

## The verdict schema

`verify.Verdict` is the one body every verifier emits, and the type other
tools align to. Its two load-bearing choices:

1. **`Decision` and `Tier` are orthogonal axes.** Decision (pass / escalate /
   block) says who may proceed; tier (T0–T3) says who must approve. A
   deterministic floor emits tier-with-pass, a CI readback emits
   decision-with-no-tier, review consolidation emits escalate-with-findings.
   Collapsing them into one severity axis fails on the first real PR.
2. **`Producer` is a structured `{class, impl}` pair.** Class (`code`,
   `local-model`, `judgment`) carries the ladder semantics; impl (`qwen2.5:7b`,
   `claude-cli`, `operator`) is provenance only — the reducer must never
   branch on it. This was originally a `"class/impl"` string convention, and
   it silently broke class matching; the schema now makes the distinction
   structural, and `Reduce` rejects unknown classes outright.

## The ladder law

Encoded in `verify.Reduce` as errors and pinned tests, not conventions:

- The deterministic floor always runs and can never be lowered.
- A local-model producer may pass or escalate, never block (`ErrLocalBlock`) —
  small models confabulate on dense content, so escalation is the safe failure.
- Judgment resolves escalations but cannot override a code block — red
  evidence stays red.
- Composition is monotone: worst decision wins, max tier wins, min confidence
  carries.
- Unknown tiers rank highest and unknown producer classes are rejected: fail
  closed, both.
- The grant's tier ceiling caps auto-land *after* judgment — a judgment pass
  cannot launder a high-risk tier past the ceiling. Both reasons land in the
  outcome artifact.

## Capability

A grant is minted by the operator (`grant` verb), HMAC-signed with a local
key, and checked before any evidence is gathered — and again before a
judgment is applied, because resolving an escalation is effectful. Refusals
are coded errors (`grant_expired`, `grant_scope_mismatch`,
`grant_bad_signature`, `grant_tier_exceeded`), exit 3, nothing recorded but
the refusal. Review-cycle policy that used to live in skill prose (cycle
caps, coordinator thresholds) becomes grant fields and reducer configuration
as the integration matures.

## Composition with existing tools

- **triage** stays the home of the deterministic risk floor; `gate` invokes
  the `triage-floor` binary over recorded diff evidence as its code-class
  tier verifier.
- **Review consolidation** (local Ollama model, per-comment
  extract-don't-judge, confidence-gated) replaces the mechanical half of the
  review-coordination workflow; the judgment half becomes `judge -auto`.
- **The driver merge tail** calls `gate` via exit codes — the same pattern as
  gating a driver run on a trace check: a Go binary gating an engine from
  outside, no engine surgery.
- **`judge -auto`** hands a frontier model (via the `claude` CLI) only what
  state holds: the escalation, the verifier verdicts, the recorded diff. If a
  good judgment needs more than the artifacts carry, that is a contract bug
  to fix in the artifacts, not a reason to let the judge read outside state.

## Deliberately out of v0, with triggers

- **Live merge execution.** `-live` is wired but records
  `merge_not_implemented`; the dry-run prints the exact merge command. It
  activates only alongside the driver-integration step, after an adversarial
  break-the-gate exercise passes against a real repo.
- **Content-addressed evidence blobs.** Evidence bodies are inline JSON.
  Trigger: the first diff over ~100KB.
- **Any daemon or server.** The substrate is a file contract; at current scale
  the lock file carries multi-writer safety and the deploy story stays zero.
- **A work-item store.** Runs and artifacts are not tasks. Trigger: the gate
  log and the project-memory store visibly double-booking the same fact.
