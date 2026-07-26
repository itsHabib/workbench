# substrate-autowiring — Technical Design Document

**Status:** draft / proposal — NOT a build commitment. The artifact we decide from.
**Owner:** @itsHabib
**Date:** 2026-07-25
**Related:** dossier [`docs/features/state-substrate/spec.md`](../../../../dossier/docs/features/state-substrate/spec.md) §9 Phase D + §4 **D6** (this is that deferred "driver auto-wiring" initiative), dossier [`PROTOCOL.md`](../../../../dossier/PROTOCOL.md) "Artifact kind conventions" (the `verdict`/`receipt` shapes this writes), `~/pers/hooks/scripts/posttool-gh-pr-merge.sh` (the emitter that already exists), `workbench/cmd/gate` (the verdict fact-owner), CLAUDE.md "The shape underneath" (the five contract planes).

> **Reviewers — focus areas:** §4 **D1** (where the *verdict* is emitted — gate-side vs a close-out wrapper — the one real fork), §4 **D2** (FK joined on `head_sha`, not the un-reconstructable `gate_run_id`), §6.3 (the bare-merge project-mapping gap), §8 (is the enabling `--meta` task really the only hard dependency?).

## Glossary — one line, so it is never re-debated

**The substrate remembers; the planes that own each fact write it; no executor is trusted to.** A `verdict` is gate's fact (Verification); a `receipt` is the merge event's fact (a Capability-authorized action). dossier (State) just *receives* them as typed artifacts. "Auto-wiring" ≠ "ship writes them" — that would be Execution reaching into State by a call stack, the anti-pattern this design exists to avoid.

## 1. Problem & hypothesis

The State substrate (dossier `verdict` + `receipt` artifacts) is **live and dogfood-proven** — 7 real rows, "why did this PR merge?" answerable from `artifact.list` alone (dossier state-substrate Phase B, GO 2026-07-24). But every one of those rows was written **by a human running `artifact.link` by hand at close-out.** The substrate is only as complete as someone's discipline. Its consumers — `/wip`, `/shipped`, `flare`, "why did this merge" — can only *trust* `artifact.list` once **every** governed merge lands a row; until then a reader that believes the substrate silently misses whatever nobody recorded. That gap is the sole thing blocking the observability cutover (dossier Phase C).

**The bet:** populate the substrate from the **plane boundaries every merge crosses**, not from a particular executor.

- The naive version — "ship's driver writes the rows at land/record" — couples completeness to *one* execution path. Any merge that skips ship (a hand-driven PR, or the portfolio's stated direction: more-capable agents that drive work without ship) writes nothing. That just relocates the fragility from "human forgets" to "did it go through ship."
- Executors are a **depreciating asset** (same thesis as `dispatch`): vendors and better agents will eat dispatch/poll/land. The durable layer is *the record*. So anchor the writes where the **facts are owned** — gate authorizes every governed merge (Verification), and the authorized `gh pr merge` is the merge event (a Capability-gated action) — both boundaries every governed merge crosses regardless of who drove it.

**Grounding that shrinks this initiative** (see the survey in §3): the driver-agnostic emitter **already exists**. `~/pers/hooks/scripts/posttool-gh-pr-merge.sh` fires on every authorized merge (the `--match-head-commit` guard guarantees every merge that reaches it is gate-shaped), already resolves the dossier project+task from the PR body, and already writes an artifact. This is not "build an auto-writer" — it is "emit one more artifact kind, and unblock the one thing that stops a shell hook from writing meta."

**Non-goals (v1):**

- **Not a ship change.** ship stays a pure evidence-emitter that never writes its own verdicts; it may *enrich* later (§4 D5), but it is never the required writer. Zero ship code changes to reach the validation gate.
- **Not a new store, verb, or schema.** The `verdict`/`receipt` kinds, their canonical refs, meta keys, immutability, and supersede rule are **already locked** in dossier PROTOCOL.md. This initiative only *emits* against them.
- **Not gate policy.** gate's decision logic, tiers, and reducer are untouched. We read gate's existing output; we do not change how it decides.
- **No backfill.** Historical merges stay as the hand-emitted dogfood rows. This wires *new* merges going forward.

## 2. Functional & non-functional requirements

**FR:**

- **FR1** — On every governed merge, a `receipt` artifact lands in the correct dossier project, keyed by the canonical PR URL, with `meta = {event: merge, pr, merge_sha, verdict: <art_id>}`.
- **FR2** — On every gate authorization (the `verdict` fact), a `verdict` artifact lands, keyed by `gate://<repo>/pr/<n>/<gate_run_id>`, with `meta = {source: gate, outcome, pr, head_sha, grant, tier}`.
- **FR3** — The receipt's `meta.verdict` FK resolves to its verdict; when the exact id can't be threaded, the receipt still writes and the pair is joinable on `head_sha` (the §7.2 fallback), so a missing FK degrades, never breaks.
- **FR4** — Writes are **idempotent**: re-running close-out or re-firing the hook on the same merge is a no-op (the substrate's `(task, kind, ref)` dedup), never a duplicate.
- **FR5** — Emission is **best-effort and non-blocking**: a substrate write that fails NEVER blocks or fails the merge itself (soft-log, like the existing hook helpers).

| NFR | Target |
|---|---|
| Driver-independence | receipt + verdict land for a merge driven by ship, a bare human `!` merge, or any future agent — anything that clears gate + the guard |
| Coupling | artifacts only (the planes stay coupled by typed rows, not call stacks); gate/hook write State, nothing imports the other's internals |
| Failure model | fail-**open** on the write (never block a merge); self-healing on partial writes (§7) |
| Determinism | same merge → same rows; the `(task,kind,ref)` dedup makes retries safe |
| Dependencies | reuse what exists — the merge hook, `pr-lookup.sh`, gate's JSON; one new dossier CLI flag (§8 P0) |

## 3. Architecture overview

```
   gate authorizes PR (exit 0, Verification plane)
        │  emits gateResult JSON: {run, pr, decision, tier, outcome, head_sha, grant}
        ▼
   [verdict emitter]  ── writes ──►  dossier  kind=verdict
   (gate-side, or a thin              ref = gate://<repo>/pr/<n>/<run>
    close-out wrapper — §4 D1)        meta = source/outcome/pr/head_sha/grant/tier
        │
        │  gate emits the merge command:  gh pr merge N -R r --match-head-commit <head_sha>
        ▼
   the authorized merge runs (any executor: ship | agent | human `!`)
        │  Capability-gated action; pretool-guard lets ONLY --match-head-commit through
        ▼
   PostToolUse: posttool-gh-pr-merge.sh   (ALREADY fires here; already resolves project+task)
        │  parses PR#, head_sha (off --match-head-commit), merge_sha (gh pr view)
        │  looks up the verdict art_id:  artifact_list --kind verdict + match head_sha
        ▼
   [receipt emitter]  ── writes ──►  dossier  kind=receipt
   (one added artifact_link call)     ref = https://github.com/<owner>/<repo>/pull/<n>
                                      meta = event=merge/pr/merge_sha/verdict=<art_id>

   ship (Execution) — OPTIONAL enricher only: may add a `run` artifact
   (cycles, driver run id, engine) for the paths it drives. Never the required writer.
```

Two emitters, two moments, two plane-owners. State receives both as typed rows. Every governed merge crosses both boundaries, so completeness does not depend on the executor.

## 4. Design decisions

| # | Decision | Alternative | Why |
|---|---|---|---|
| **D1** | **The verdict is recorded at gate-decision time** (the only moment with `tier`/`grant`/`outcome` in hand). Two viable homes — **(a) gate itself writes it**, or **(b) a thin close-out wrapper** around gate's invocation reads its JSON and writes it. **Recommend (b) for v1**, graduate to (a) if a non-wrapped gate path appears. | Write the verdict from the merge hook. | The merge hook fires *after* gate; gate's stdout (tier/grant/outcome) is long gone by then, and GitHub carries none of it. Only gate-time has the data. (b) keeps gate pure (zero dossier awareness — it has none today, confirmed) and puts the glue in the Composition layer; (a) is more pillar-pure (fact-owner writes) but adds a repo→project dependency to gate (§6.3). Both emit the identical row. |
| **D2** | **FK is joined on `head_sha`, not `gate_run_id`.** The receipt hook looks up the verdict via `artifact_list --kind verdict` and matches `meta.head_sha` to the sha it parses off `--match-head-commit`, then copies that `art_id` into `receipt.meta.verdict`. | Reconstruct `gate://…/<run_id>` at merge time. | The `gate_run_id` lives only in gate's stdout + gate's state; it is **not** on the merge command and **not** in GitHub, so it is un-reconstructable at merge time. `head_sha` **is** on the command (`--match-head-commit`) and **is** in `verdict.meta.head_sha` — a strong, exact join key. This is exactly the format-independent path dossier PROTOCOL §7.2 already blesses. |
| **D3** | **Enabling task (P0): add `--meta key=val` to the dossier `artifact_link` CLI.** | Route the hook through MCP `artifact.link` (which already takes meta). | A shell hook cannot spawn an MCP client per merge; the CLI is its interface. Today `run_artifact_link` hardcodes empty meta (`dossier.rs:406`), so a shell emitter **physically cannot** write the meta the substrate is built around. This is the one hard dependency — nothing else works until it lands. Small: a repeatable `--meta` flag → `ArtifactLinkRequest.meta` → the existing `LinkArtifact{meta}` path (caps + immutability already enforced in the service layer). |
| **D4** | **Reuse the existing merge hook** (`posttool-gh-pr-merge.sh`) as the receipt emitter — add one `artifact_link kind=receipt` call. | A new dedicated hook. | It already fires on exactly the right event, already resolves project+task via `pr-lookup.sh`, already soft-fails without blocking merges. Extending it is smaller and inherits its correctness. |
| **D5** | **ship is an optional enricher, deferred past the gate.** | Wire ship's `land()`/`markMerged` to write dossier rows now. | ship never writes dossier today; its "receipt" is a *separate* ledger concept (don't conflate). The base receipt must come from the hook so non-ship merges are recorded. ship-as-enricher (adding `cycles`/run-id/engine to a `run` artifact) is a post-gate nicety, not a v1 requirement. |
| **D6** | **Correctness reuses the substrate's existing guarantees** — `(task,kind,ref)` dedup (idempotent retries), immutable meta (corrections via supersede), meta caps. Nothing new. | Add request-ids / write coordination. | The substrate already makes verdicts/receipts immutable facts; the emitters inherit idempotency for free (FR4). |

## 5. Data model

**No new types.** Emits against the locked dossier conventions (PROTOCOL.md §5 + `state-substrate-schema/verdict-receipt-conventions.md`):

- `verdict` — ref `gate://<repo>/pr/<n>/<gate_run_id>`; meta `{source:"gate", outcome, pr, head_sha, grant, tier}`.
- `receipt` — ref `https://github.com/<owner>/<repo>/pull/<n>` (lowercase host, no trailing slash / `.git`); meta `{event:"merge", pr, merge_sha, verdict:<art_id>, supersedes?}`.
- Anchor: project + optional task (D8 in the substrate spec). The hook resolves both via `pr-lookup.sh`.

## 6. Key flows

### 6.1 Verdict — at gate authorization
gate runs → `gateResult` JSON (`run, pr, decision, tier, outcome, head_sha, grant`). The verdict emitter (D1) maps this 1:1 to `verdict.meta`, formats the `gate://` ref, resolves the project (§6.3), and `artifact_link --kind verdict --meta …`. Idempotent on re-run.

### 6.2 Receipt — at the merge (existing hook, extended)
`posttool-gh-pr-merge.sh` already: confirms merge success, extracts PR#, resolves project+task, fetches `merge_sha`. **Added:** parse `head_sha` off `--match-head-commit`; `artifact_list --project <p> --kind verdict` → pick the row whose `meta.head_sha == head_sha` → its `art_id`; then `artifact_link --kind receipt --ref <PR URL> --meta event=merge,pr=N,merge_sha=…,verdict=<art_id>`.

### 6.3 Project resolution
Primary: the PR-body task link (`pr_lookup_task`) → the task's owning project — authoritative, already used. Fallback: repo-basename heuristic (`_infer_project_slug`) / `$DOSSIER_PROJECT`. **Open gap (§9):** a *bare* human merge with no `Closes task` line resolves a project only via the heuristic — decide whether that is authoritative or whether a real repo→project map is warranted.

## 7. Failure / consistency model

- **Write fails (dossier down, bad slug):** soft-log to `HOOKS_ERROR_LOG`, **never block the merge** (FR5) — inherits the existing hook contract.
- **Partial write (verdict written, receipt not, or vice-versa):** consistent + self-healing — re-running close-out / re-firing the hook is idempotent (dedup) for the written row and appends the missing one. A receipt whose FK never resolved still joins on `head_sha` (FR3).
- **Wrong meta:** immutable — corrected only by a superseding row (distinct ref + `meta.supersedes`), per the locked convention. No in-place fix.
- **Two emitters race the same merge:** the `(task,kind,ref)` dedup collapses them; last-writer-with-identical-meta is a no-op, differing-meta is rejected.

## 8. Rollout / implementation plan

Validation gate after P2 — the loop must populate the substrate for real, unattended, before ship-enrichment or the Phase C cutover is built on it.

| Phase | Goal | Tasks | Depends | Gate |
|---|---|---|---|---|
| **P0 — enable meta writes** *(dossier)* | A shell hook can write meta | `--meta key=val` (repeatable) on the `artifact_link` CLI → `ArtifactListRequest.meta` → `LinkArtifact{meta}`; test parity with MCP | — | pre-gate |
| **P1 — receipt on merge** *(hooks)* | Every governed merge lands a receipt | extend `posttool-gh-pr-merge.sh`: parse head_sha, verdict-FK lookup by head_sha, emit `kind=receipt` | P0 | pre-gate |
| **P2 — verdict on authorization** *(workbench)* | Every authorization lands a verdict | the D1 emitter (recommend: close-out wrapper around gate reading its JSON); format `gate://` ref; project resolution | P0 | **VALIDATION GATE** |
| **P3 — ship enricher** *(ship, optional)* | Richer provenance for driven merges | ship adds a `run` artifact (cycles, run id, engine) at `markMerged` | P2 | post-gate |

**Validation gate (after P2):** run N≥5 *real* governed dossier/workbench merges through the normal loop with **no hand-run `artifact.link`**. Pass = 5/5 land a correct verdict+receipt pair (FK resolved or head_sha-joinable), 0 merges blocked or failed by a substrate write, and "why did this merge?" answers from `artifact.list` alone — matching the hand-emitted rows byte-for-byte in shape. Red (missing rows, blocked merge, unresolved project on a bare merge) → fix before Phase C reads trust it.

## 9. Open questions

1. **D1 home** — start with the close-out wrapper (recommended) or bite off gate-side emission now? Wrapper ships faster and keeps gate pure; gate-side is the pillar-pure end state. Revisit if a gate path with no wrapper appears.
2. **Bare-merge project resolution (§6.3)** — is the repo-basename heuristic authoritative enough for merges with no task link, or does completeness require a real repo→project map? (Two un-resolvable bare merges = build the map.)
3. **Where does the verdict emitter run for a *human* `!`-driven gate pass?** The wrapper is natural for agent/skill close-out; a human running `gate gate` by hand then `!`-merging needs the wrapper too, or the receipt lands with an unresolved FK (degrades to head_sha join — acceptable, but note it).
4. **`escalation` as a kind?** flare's dominant signal is gate *escalations*, which the substrate doesn't model — out of scope here, but Phase C (flare cutover) will force the question; flag so the schemas don't diverge.
5. **`merged_at` on receipts** — `/shipped` wants a since-date; `linked_at` ≈ merge time but isn't the merge timestamp. Add `merged_at` to receipt meta here (the hook has it) so Phase C doesn't reopen this.
