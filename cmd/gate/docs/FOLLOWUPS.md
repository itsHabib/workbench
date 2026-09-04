# Follow-ups — red-team hardening

Source: independent adversarial review, 2026-07-05. Full critique kept at
`pers/workbench-redesign/RED-TEAM.md` (outside this repo).

That pass endorsed the **scoping** (one thin gate binary, not a five-plane platform) but found the
gate **not yet trustworthy at its target seam**. These are the fixes standing between here and
wiring `gate` into the merge tail.

## Must fix before wiring into the merge tail

- [ ] **Close the absence-of-signal fail-opens.** *(blocker)*
  An empty bot-review panel makes `Reviews` pass; `Reduce(nil)` returns pass/T0 because
  floor-presence is enforced by `main.go`'s call order rather than the reducer; an empty
  `reviewDecision` passes on unprotected repos. Same "absence reads as green" class as rooms#47.
  **Fix:** move the floor-presence invariant into `Reduce` itself — no code-floor verdict in the
  set → escalate/block, never pass — and treat zero-signal (no reviews, empty CI, empty
  `reviewDecision`) as *escalate*, mirroring what `readiness.go` already does on an empty CI rollup.
  **Done when:** tests pin `Reduce(nil)` → not-pass and empty-reviews → escalate, and the invariant
  lives in the reducer, not the caller.

- [x] **Write down and enforce the capability backstop.** *(serious)*
  Minting was unprivileged — anyone who can run `gate` can run `gate grant`, and `backtest`
  self-minted a spendable T2 grant into durable state — while every agent the gate governs already
  holds a `gh` token that can `gh pr merge` around it. The capability plane is advisory until
  something *forces* merges through the gate.
  **Landed:** the enforcement model is written in `docs/enforcement.md`, stated without overclaim —
  branch protection requiring the `gate` check is the forcing function (until then the plane is
  discipline plus an audit trail, not prevention); token custody names the intended
  merge-capable-identity-vs-bounded-agent split; mint authority (unprivileged; `MintedBy` is an
  unauthenticated free string) and the `grant.key` custody decision (cross-referenced to the tamper
  task) are recorded; the `gh pr merge` bypass is named rather than implied-closed; and the operator
  branch-protection action is written down as the `-live` precondition. `backtest` no longer mints a
  spendable grant — it runs against a throwaway ephemeral store, so no grant reaches the durable log
  (pinned by tests). The README no longer implies the gate bounds/forces merges it can't force and
  links to `docs/enforcement.md`.
  **Still open (documented as future, not built here):** token custody is not yet *real* on the
  single box (every local agent shares one `gh` credential) — closing it is a precondition for
  `-live`; and real mint authentication (so only a designated identity can mint a spendable grant)
  is future work.

- [x] **State the tamper threat model honestly, then decide what to harden.** *(serious)*
  `Audit` caught naive body edits, broken links, and reordering — but **not** tail truncation or
  whole-log deletion (reported "chain intact"), and the unkeyed SHA-256 chain could be wholesale
  rewritten by anyone with file-write. `grant.key` also sat in the same directory as `log.jsonl`.
  **Landed:** the threat model is written in `docs/DESIGN.md` (*Tamper model*), matched 1:1 to the
  code. A keyed tip anchor — `HMAC(key, head ‖ count)` under a key held outside the state dir —
  now defeats wholesale rewrite; the recorded `count` catches truncation and whole-log deletion;
  `grant.key` and the anchor key moved out of the state dir (`-key` overrides, default user config
  dir), and previously-minted grants still validate after the move. Tests pin truncation, deletion,
  and rehashed-rewrite detection.
  **Still open (out of scope for that pass, noted in the design):** the stale-lock TOCTOU takeover
  race and a SQLite/WAL durability model.

## Tidy-up

- [ ] **Load the review panel once per run.** `PanelCompleteness` and readiness's
  `panelStandIn` each call `loadPanel` on the same evidence id, so one panel body is read,
  JSON-parsed, `reviewpanel.Validate`-d, and `evaluatePanel`-ed twice per gate run. Correctness
  is unaffected — it is the same validated evidence both times — so this is mechanism, not a
  defect. Deduping means loading once in `runGateWithSynthesis` and passing the parsed
  `reviewpanel.Evidence` to both consumers, which changes the exported signatures of
  `verify.Readiness` and `verify.PanelCompleteness` (evidence id → parsed struct) and ripples
  through their tests. Left out of the botChangeRequest PR to keep a security fix focused;
  worth doing on its own.

## Before broader trust / real dogfood

- [ ] **Decide what a bodyless bot `CHANGES_REQUESTED` is worth.**
  Raised by the Codex panel on the readiness panel stand-in PR, as a P1 against
  `resolveStandIn`: a human's exact-head approval satisfies readiness even when a required
  panel reviewer's completed review asks for changes. That ordering is deliberate and
  separately pinned (`TestReadinessBotChangeRequestDoesNotSuppressHumanApproval` — bot
  findings are findings; authorization belongs to the account with repository authority),
  so reversing it was out of scope for that PR, and readiness now at least RECORDS the
  objection it passed (`objectionNote`). The residual is narrower than the finding as
  written: a change-request review **with a body** still reaches the review-consolidation
  rung, which escalates on its findings. A **bodyless** one reaches nothing — no findings to
  extract, and readiness deliberately declines to block — so an explicit objection can leave
  no rung holding it. **Decide:** does a bodyless CHANGES_REQUESTED from a required reviewer
  block, escalate, or stay advisory? Whichever way, the answer belongs in one rung with a
  test, not in the gap between two.

- [ ] **Make block/park verdicts sticky and bound judge re-runs.**
  Block verdicts aren't sticky and `judge -auto` is nondeterministic, so a judge can be re-run until
  it passes — the 3-cycle-cap-in-prose hole rebuilt one rung down. Persist block/park as sticky (a
  later pass can't silently overturn one without recording why); cap or record re-runs.

- [ ] **Feed the auto-judge raw evidence, not just local headlines.**
  `judge -auto` only sees the local model's extracted headlines, not the raw comment bodies already
  recorded in state — so "premium judges from artifacts" is judging a lossy summary. Include the raw
  bodies in `judgeContext`.

- [ ] **Add a park notification channel.**
  5 of 7 real PRs park, with no notification, so they park silently. Emit something on park
  (stdout/file/console to start) so a parked run surfaces instead of waiting to be noticed.

- [x] **Pin the primary diff path to the evaluated head.**
  Surfaced by the evidence-local-diff skeptic panel (2026-07-16). The oversized-PR fallback
  refuses unless `pulls.head == view.headRefOid`, but the primary path (`gh pr diff <n>`) fetches
  by PR number with no head pin: a force-push to an innocent head between the view read and the
  diff read records that head's diff, and a force-push back before merge still satisfies
  `--match-head-commit`. Window is a sub-call race needing push access + green CI on the decoy, but
  gate's threat model includes adversarial agents with push access. **Landed:** the primary path
  reads `pulls/<n>`, refuses unless `head.sha == view.headRefOid`, then fetches the merge-base diff
  through the SHA-pinned `compare/<base>...<head>` endpoint; the recorded evidence carries that
  verified head. Deterministic moved-head and A→B→A mutants prove that mismatched or substituted diff
  bytes never become recordable evidence. The fallback path already had this property.

- [x] **Refuse to reseal a mismatched anchor as crash recovery.**
  Surfaced by codex on the tenant-move review (workbench#59, 2026-07-17); the gate judge blocked
  the move until fixed, so it landed in the same PR as a separate reviewed commit (the move commit
  itself stays byte-identical and A/B-verified). **Landed:** rebind's reconcile path proves the
  entry at the pinned count still carries the pinned head before resealing; a rewritten prefix
  refuses with `ErrRebindRewrite` and Audit keeps failing (pinned by
  `TestAppendAfterRewriteRefusesReseal`).

- [x] **Validate the floor's tier before recording its verdict.**
  Surfaced by codex on the tenant-move review (workbench#59, 2026-07-17); the gate judge blocked
  the move until fixed, so it landed in the same PR as a separate reviewed commit. **Landed:**
  `parseFloorOutput` refuses an absent or unknown tier (`tier.Valid`) as an operational error
  before any verdict is recorded — no valid floor, no verdict (pinned by the
  `TestParseFloorOutput*` cases).

- [ ] **Authenticate appended entries (per-entry MAC) — the crash-window residual.**
  Surfaced by codex's second pass on workbench#59 (2026-07-17). The reseal path now proves the
  anchored prefix and bounds recovery to the one-append crash window
  (`ErrRebindRewrite` / `ErrRebindUnprovenSuffix`), which closes rewrite laundering and
  batch-suffix forgery — but ONE forged chain-consistent entry timed inside the crash window is
  still byte-indistinguishable from a genuinely interrupted append, and gets sealed by the next
  legitimate write. The chain is unkeyed by design; closing this fully means authenticating each
  entry at append time (per-entry MAC under the anchor key, or signing the writer), a tamper-model
  design change owed to the red-team-hardening thread — not a bolt-on. Until then the residual is
  one unauthenticated entry per genuine crash-recovery event, named here rather than implied
  closed.

## Policy questions raised by the Lean verdict-laws model (Phase 0)

Source: a separate experiment (`~/dev/workbench-laws-lean`) hand-ported gate's verdict reducer
(`cmd/gate/internal/verify/verify.go` `Reduce`, at workbench commit
`6eee6aa63ff0d7bcaf127b9cdf4f5af748659ac1`) and `Grant.TierWithin` into a pure Lean model and
machine-checked laws about it. Full write-up: `~/dev/workbench-laws-lean/docs/report.md`
(result + stop decision), `docs/source-map.md` (Go↔Lean conformance boundary), and
`WorkbenchLaws/Verdict/Reachability.md` (the TierWithin matrix + reachability).

**Provenance limit, stated once and load-bearing for both questions below.** The proofs bind to the
**Lean model, not the running Go binary** — there is no executable Go↔Lean correspondence, and
workbench can drift while every Lean proof stays green. So the report deliberately labeled BOTH
findings *policy questions for workbench, neither a change made here*. The exports on the workbench
side are correspondingly a **regression test that pins current behavior** plus the written question
below — NOT a behavior change. Canonicalizing the tier (Q1) or validating the candidate (Q2)
autonomously would be smuggling a policy decision out of a proof; that call is the operator's.

- [ ] **Q1 — Should the composed tier be canonicalized at a rank tie so raw output is
  order-independent?**
  **Finding:** `Reduce` replaces the composed tier only on a STRICTLY-greater rank
  (`cmd/gate/internal/verify/verify.go:142-144`), and `tier.Rank`
  (`cmd/gate/internal/tier/tier.go:12-23`) ranks `"T3"` and every unknown/empty string alike at 3.
  So at a rank tie the FIRST spelling reached wins: `[T3, garbage]` composes raw tier `"T3"`, the
  reverse composes `"garbage"` — same decision, same tier rank, different raw string.
  **Evidence / coverage:** the Rapid permutation + monotonicity generators draw only valid tiers
  (`cmd/gate/internal/verify/property_test.go`, `genLadderVerdicts`), which have no rank ties, so
  they never sampled this cross-product. Now pinned by
  `TestReduceRawTierIsOrderDependentAtRankTie` (example) and
  `TestPropReduceTierRankInvariantUnknownTiers` (property over the tie-bearing domain), which assert
  the invariants that DO hold — decision and tier rank — and document that the raw string does not.
  **Reachability / impact:** decision and tier rank — the axes any consumer branches on — are
  unaffected; only the raw tier STRING recorded in the composed verdict varies with input order.
  No current consumer branches on the raw tier string. Canonicalizing would make the artifact
  byte-stable under reordering, at the cost of no longer reproducing the reducer's actual
  first-strict-maximum behavior; per `source-map.md`, a later Gate-owned evaluation surface that
  compares raw tier must reproduce first-strict-maximum exactly, so canonicalizing is a real
  semantics choice, not a cleanup.
  **Decision owner:** operator. Until decided, behavior is unchanged and the tests pin it.

- [ ] **Q2 — Should `TierWithin` validate the candidate tier, given current reachability?**
  **Finding:** `Grant.TierWithin` (`cmd/gate/internal/capability/capability.go:140-148`) validates
  the grant's CEILING (`tier.Valid(g.MaxTier)`) but not the CANDIDATE; an unknown or empty candidate
  ranks 3 (`tier.Rank`) and so compares "within" a valid **T3** ceiling — and is rejected by every
  lower ceiling, exactly as a real T3 would be.
  **Evidence / coverage:** the full matrix is in `Reachability.md`; the unknown/empty-at-each-ceiling
  rows are now pinned by `TestTierWithinUnknownCandidateMatchesT3`
  (`cmd/gate/internal/capability/capability_test.go`). The pre-existing `TestTierCeilingFailsClosed`
  only exercised a T1 ceiling, where a rank-3 candidate is over the ceiling regardless — so the T3
  row was uncovered.
  **Reachability / impact:** per `Reachability.md`, every current owned producer path — triage-floor
  (`parseFloorOutput` rejects invalid tiers), submitted judgment (`ValidateJudgment` requires a valid
  tier + ceiling bound), readiness and ci-classify (both pin `T0`) — rejects or pins the tier before
  an unknown candidate could reach a live `TierWithin` call. Reaching this row requires a
  foreign/drifted artifact; the Lean project explicitly labeled it a semantics + reachability
  question, NOT a vulnerability. Adding candidate validation (unknown candidate → `TierWithin`
  false) would fail-close the drifted-artifact path; the question is whether that belongs here or in
  the producer boundary that already pins tiers.
  **Decision owner:** operator. Until decided, behavior is unchanged and the tests pin it.

## Deferred from #242 (cycle-ceiling pre-flight) — review panel, 2026-08-22

- [ ] **Concurrent admission is read-then-check, not atomic.** Two `gate gate` runs for the same
  PR started with one cycle left can both pass `preflightCycles` and both record counting
  outcomes: `act`'s block and content-park branches return before its ceiling backstop. Pre-existing
  (the old `act` counted in the same place); #242 only moved the serial-driver refusal earlier. Each
  resulting park stays judgeable (a judgment excludes its own run from the count), so the damage is
  a wasted cycle, not unjudgeable state. **Fix:** per-subject admission serialization — a lock or a
  reservation artifact with an expiry — which is its own design question in an append-only log.
- [ ] **`cycles_used` is 0 under an unbounded grant.** Neither `preflightCycles` nor `act` counts
  when `max_cycles == 0`, because the count replays the whole audited chain for a ceiling that does
  not exist. The result comment documents `0/0` as "unmeasured, unbounded". If drivers need usage
  under unbounded grants, count from a cached audit rather than a second replay.
- [ ] **`recordCycleRefusal` is hard, not best-effort.** A refusal whose artifact cannot be written
  exits 4 with no JSON, unlike the grant-lapse path. Deliberate: a refusal that is not in the log
  is not reconstructable, and exit 4 is the honest answer. Revisit only if drivers prove to need the
  exit-3 contract over durability.
- [ ] **`Project` / `Explain` can now fail on `st.List`** while projecting an awaiting escalation's
  budget (`parkedBudget`). Failing closed on a real I/O error is the chosen behaviour.
