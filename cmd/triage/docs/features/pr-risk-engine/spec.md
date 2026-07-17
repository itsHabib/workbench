# triage — PR Risk-Classification Engine — Technical Design Document

**Status:** draft / proposal — NOT a build commitment. The artifact we decide from.
**Owner:** @itsHabib
**Date:** 2026-07-02 (v2 — rewritten after an adversarial review pass; see §12 for what changed and why)
**Related:** conversation brainstorm (review bottleneck at 10-20 PRs/week); `review-coordinator` skill; operator memory `feedback_adversarial_workflow_for_gates`, `reference_agent_friendly_stack` (guardrail ladder).

> **Note (2026-07-02):** an earlier draft referred to folding the tier-ballot reconciler into `sense`'s consensus reducer. Correction: `sense` has **no** consensus/aggregation engine — it was only ever a proposal, never built. Any reconciler (deferred to the team phase, §10) needs a home decided fresh; do not assume `sense`.

> **Reviewers — focus areas:** §5 (the deterministic signal map is the whole safety story — attack the blind spots), §5.4 (control-plane + content signals — the v1 killer was here), §11 (is the gate now actually falsifiable and two-sided), §9 (does v0 touch the bottleneck at all). Try to make a genuinely dangerous PR classify low.

---

## 1. Problem & hypothesis

**Problem.** When an engineer can ship 10-20 PRs/week with AI assistance, human review becomes the throughput ceiling. Review secretly does four jobs — correctness, design coherence, knowledge transfer, accountability — and only correctness is automatable by gates (tests, e2e, canaries). More gates make *green CI* cheaper without touching the real queue: one human's attention on the design/accountability jobs, which don't parallelize by adding CI stages. The bottleneck is **judgment allocation**, not verification throughput.

**Hypothesis.** Most PRs don't need a human, and the dangerous ones can be identified *deterministically* enough to route them there automatically — with a hard guarantee against *under*-classifying danger. If so, humans stop reviewing the majority that machines can clear and spend their whole attention budget on the minority where judgment is irreducible.

**The bet in one line:** review load should scale with *risk*, not with *PR count*.

**Non-goals (and why):**
- **Not a replacement for review.** triage routes; `review-coordinator` + the bot panel review. It sits strictly upstream.
- **Not a scoreboard.** We track review latency and %-auto-merged to know if it works, not to rank or profile engineers. (This is why v0 has no per-author calibration log — see §12/T3.)
- **Not generic.** It encodes *our* risk opinions. Opinions are the value. (`feedback_opinionated_not_generic`.)

## 2. What the adversarial pass forced (read this before the rest)

v1 of this doc failed four independent skeptics on the same two structural faults. v2 is built around the fixes. In one paragraph so the rest of the doc reads correctly:

1. **The deterministic floor is the safety mechanism; the agent is an advisory layer that must earn its slot.** An LLM in the safety path buys nondeterminism and latency; a path/content signal map buys a reproducible guarantee. So v0 leads with the deterministic floor (§5) and treats the agent as escalation-only advice whose whole dogfood job is to prove it catches real risk the floor misses (§6). If it never does, it never ships.
2. **The human two-scorer / override machinery is deferred to a real team.** Blind author self-scoring and second-signer override are inert in a single-actor pers/ context and were the shakiest part of v1. v0 is honestly agent-over-floor only; the ballot design moves to the team phase (§10) where a second human actually exists — and that's where the reconciler becomes a small pure aggregation module (home TBD), not a hand-rolled `max()`.
3. **The classifier's own control plane is the highest-risk change class, not the lowest** (the v1 killer: editing `RUBRIC.md` auto-merged as "docs"). §5.4 adds control-plane, lockfile/manifest, policy-as-data, and diff-*content* signals that were the fail-open holes.
4. **The oracle must be independent and the gate two-sided.** §11 is rebuilt: held-out labels from a labeler who hasn't read the rubric, forward validation, k-run worst-case, a precision floor (kills the degenerate always-T3 classifier), and an honest statistical bound.

## 3. Functional & non-functional requirements

**Functional:**
- FR1. Given a PR (diff + changed-path list + manifests), compute a **deterministic risk floor** from the signal map, with the fired signals + why.
- FR2. Run an **agent advisory pass** that may only *raise* the tier above the deterministic floor, logging each escalation and its rationale.
- FR3. **Route** the final tier to a review requirement (auto / peer / owner / owner+adversarial).
- FR4. **Auto-merge** a narrow, deterministically-detected, provably-safe slice (§9) so the bottleneck is actually touched in v0.
- FR5. **Audit:** sample a fraction of low-tier PRs for independent blind re-labeling; the escape rate is the only safety number (§11).
- FR6. The rubric (`RUBRIC.md`) is one version-controlled artifact; every classification records its git SHA.

**Non-functional:**

| Property | Target |
|---|---|
| **Safety (the one that matters)** | Zero false-negatives at tier ≥ T2 on the *held-out* corpus, worst-case across k≥5 runs. Reported with a Clopper-Pearson upper bound on the true miss rate, not as a bare "recall=1.0" — honest about what the sample size can certify. The corpus gate is a *screen*; the real guarantee comes from staged production auditing (§11). |
| Precision (now gated, not soft) | A precision floor is part of the gate (§11) so an always-escalate classifier fails. Over-firing is still the tuning target for adoption, but the gate has a two-sided pass. |
| Determinism | The **floor** (path + content + manifest signals) is fully reproducible: same diff → same floor, always. The agent pass is non-deterministic but is *advisory in v0* and can only escalate, so nondeterminism never sits in the safety path. |
| Latency | Deterministic floor is instant (globs + regex). Agent pass < 60s, and because it's advisory it is off the merge-blocking critical path in v0. |
| Operability | v0 is prose + a skill + a signal table: no service, no DB. State is append-only files. |

## 4. Architecture overview

```
                       ┌──────────────────────────────────────┐
   PR (diff, paths,    │  DETERMINISTIC FLOOR  (the safety     │
   manifests) ─────────►  mechanism — §5)                      │─── floor tier ─┐
                       │  path globs + diff-content regex +    │                │
                       │  manifest/lockfile + control-plane    │                ▼
                       └──────────────────────────────────────┘        route per tier
                                                                                ▲
   agent advisory pass (§6) ── may only ESCALATE above floor ──────────────────┘
   (semantic residual; logged; must earn its slot; never lowers)                │
                                                                                ▼
                                              auto-merge safe slice (§9)  +  audit sample (§11)
```

Two pieces, one clear hierarchy:

1. **Deterministic floor** (`RUBRIC.md` signal table, applied by simple matching) — carries the guarantee. Reproducible, instant, auditable.
2. **Agent advisory pass** — the thin layer for the *irreducibly semantic* residual (a logic change that widens access without matching any content pattern). It can only raise the tier, it's logged, and the dogfood measures whether it ever usefully fires above the floor. This is the guardrail-ladder instinct from `reference_agent_friendly_stack`: express as much risk as possible in the deterministic tier where it can't-be-wrong, push only the true residual to judgment.

No reconciliation engine in v0 at all. When the team phase adds a second human ballot (§10), *that* aggregation is a small pure reducer whose home is decided then — **not** `sense` (which has no such engine today), and not hand-rolled ad hoc. (`project_consensus_decision_engine`.)

## 5. The signal → tier map (the classifier)

`RUBRIC.md` is the authoritative, evolving version. Tiers:

| Tier | Name | Human requirement |
|---|---|---|
| **T0** | auto | No human. Gates green → auto-merge eligible (narrow slice, §9). |
| **T1** | standard | One peer review. |
| **T2** | sensitive | Owner (CODEOWNERS) review required. |
| **T3** | critical | Owner + adversarial skeptic pass + author "why this is safe" defense. |

`floor = max(all fired signal floors)`. All four categories below are **deterministic** (path glob, filename, or diff-content regex) — no agent needed to compute the floor.

### 5.1 Surface signals (path/keyword)

| Touches… | Floor |
|---|---|
| DB migration / schema change / backfill | T3 |
| auth / authz / session / crypto / secrets / token | T3 |
| money / billing / ledger / payment / invoice | T3 |
| irreversible op: delete path, data destruction, non-reversible migration | T3 |
| public API / exported type / wire contract | T2 |
| persisted data shape (stored/serialized format) | T2 |
| infra/deploy config: CI, Dockerfile, IaC, feature-flag defaults | T2 |
| concurrency / locking / retry / idempotency / ordering | T2 |
| internal (non-exported) behavior change | T1 |
| pure refactor / comments / tests-only / generated / copy / non-policy docs | T0 |

### 5.2 Content signals (diff-text, path-independent) — closes the "danger in an unnamed file" hole

Fire on the *content* of added/removed lines regardless of filename:

| When the diff… | Floor |
|---|---|
| **removes** a line matching an authz/permission/validation/rate-limit/crypto call pattern | T3 |
| adds/loosens a check comparison (e.g. `==` → `!=`, `&&` → `\|\|` in a guard), or deletes an assertion guarding a sensitive path | T2 |
| introduces an unbounded/uncapped construct (unpaginated query, uncapped retry/spend, unbounded loop over external input) | T2 |

Deletion-of-a-control is the class v1 missed entirely: it fired *nothing* mechanical because no path keyword matched. Content signals make it a deterministic T3.

### 5.3 Dependency / supply-chain signals — lockfiles are NOT "generated → T0"

| When the diff touches… | Floor |
|---|---|
| manifest (`package.json`, `Cargo.toml`, `go.mod`, `pyproject.toml`, `mix.exs`) | T2 |
| lockfile (`*-lock.json`, `Cargo.lock`, `go.sum`, `poetry.lock`) — version bump of an existing dep | T2 |
| registry / source override: `.npmrc`, `[patch]`, `resolutions`, `overrides`, `git`/`path` dep | T3 |
| new external dependency added | T2 |

v1 had a single `new dep → T1` signal and read lockfiles as generated code (→ T0), auto-merging a poisoned version bump. Fixed.

### 5.4 Control-plane signals (highest priority) — the v1 killer

Any diff touching the **risk system's own control plane** floors at **T3**, and is **exempt** from the "docs/generated → T0" row:

- `RUBRIC.md`, the `/pr-risk` skill, the routing table
- `labels/**` (the oracle) and `mismatches.jsonl` (the log)
- `CODEOWNERS`, branch-protection config
- any CI that invokes triage

Rationale: a change to the classifier or its evidence is the single most consequential class of change — one silent auto-merge permanently lowers the floor for *all future PRs*. It must be the hardest to land, not the easiest. The "docs → T0" row explicitly excludes any file that is itself policy, config, classifier state, or ownership.

### 5.5 Policy-as-data signals

Security enforced as data must not route below sensitive:

| File/content | Floor |
|---|---|
| IAM / rego / k8s RBAC / RoleBinding / Secret manifests | T3 |
| CORS / allowed-origins / `require_2fa`-style security toggles in config data | T2 |
| `.env*`, secret-bearing config | T2 |
| **unknown path that matches no signal** | **T1** (a peer glance) — never auto-T0 |

### 5.6 Size signals (weakest — the one genuinely sharp v1 idea, kept)

LOC/file-count may nudge within T0↔T1 only. Size **never** sets T2/T3 and **never** alone lifts a well-tested pure change out of T0. A 3-line auth change outranks a 2,000-line tested codegen regeneration. Most naive "big PR = scary" bots invert this; encoding the inversion is core.

## 6. The agent advisory pass (must earn its slot)

After the deterministic floor is computed, an agent reads the diff + `RUBRIC.md` and may propose an **escalation** above the floor for the semantic residual the deterministic signals can't express:

- a logic change that widens a trust boundary without matching a content pattern,
- a default change whose production impact needs understanding, not pattern-matching,
- cross-cutting risk visible only by reading the change as a whole.

Hard constraints: the agent may **only raise** the tier, never lower it. Every escalation is logged `{floor, proposed, why}`. **The agent's entire justification for existing in v0 is measured:** over the dogfood, count how often it escalates above the deterministic floor and whether a human confirms the escalation was real. If that count is ~0, the agent is dead weight and v0 ships as a pure deterministic classifier. This is the honest version of "LLM + rubric beats a glob bot" — it's a hypothesis the dogfood tests, not an assumption.

## 7. Interface & failure model

**Scorer output** (printed + logged), records `rubric_sha`:
```jsonc
{
  "pr": "owner/repo#123",
  "floor_tier": "T2",
  "floor_signals": [{"signal": "manifest-change", "floor": "T2", "why": "go.mod dep bump"}],
  "agent_tier": "T2",            // == floor unless the agent escalated
  "agent_escalation": null,       // or {from, to, why}  — logged for the earn-its-slot metric
  "final_tier": "T2",
  "route": "owner-review",
  "rubric_sha": "e56801a"
}
```

**Fail-closed (corrected).** On agent failure or timeout: use the deterministic floor **but never trust a T0/T1 floor without agent corroboration for dependency/policy/content-sensitive diffs** — those fail to **T2**. (v1's "fail to the mechanical floor" was fail-*open* exactly where the floor is blindly low; a poisoned lockfile with an agent timeout must land T2, not T0.) Missing/malformed rubric → refuse to classify → T2. Unknown path → T1, never T0.

**Trust boundary.** The diff is untrusted input authored by the party with incentive to slip risk through. Every safety-relevant signal is deterministic and content/path-based, so it cannot be talked out of firing. There is no author-supplied tier in v0 to attack (§10 defers it).

## 8. Key decisions & trade-offs

- **D1 — Deterministic floor is the classifier; the agent is advisory and must earn its slot.** The guarantee lives in reproducible signals; the LLM is a tested add-on, not the mechanism. (Was inverted in v1.)
- **D2 — Tier is derived from signals, not picked.** Reproducible and debuggable signal-by-signal.
- **D3 — Size is the weakest signal.** The counterintuitive core; kept from v1.
- **D4 — Human ballots (self-score, override) are deferred to the team phase (§10).** They're inert in solo pers/ and were v1's weakest link. When reintroduced, the aggregation is a small pure reducer (home TBD), not hand-rolled ad hoc.
- **D5 — Two-sided, independently-labeled, k-run gate.** Recall floor on T2/T3 *and* precision floor on T0, on a held-out corpus, worst-case across runs (§11).
- **D6 — v0 auto-merges a narrow provably-safe slice from day one.** Without a real skip, the design adds cost and never touches the bottleneck it exists to fix (§9).
- **D7 — Control-plane changes are T3.** Meta-changes to the classifier are the highest-risk class.

## 9. Rollout / implementation plan

| Phase | Goal | Work | Depends-on | Gate |
|---|---|---|---|---|
| **P0 — Rubric + independent oracle** | Define "correct" before trusting the classifier | Harden §5 into `RUBRIC.md`; build a **held-out** corpus labeled by an independent labeler (fresh agent, neutral prompt, has *not* read the rubric — §11); seed adversarial red-team rows. | — | — |
| **P1 — Deterministic scorer + agent advisory + live safe-slice** | Ship the floor, the logged advisory pass, and a *real* auto-merge slice | `/pr-risk`: floor engine (path+content+manifest+control-plane) → agent advisory (escalate-only, logged) → route → auto-merge the narrow safe slice (tests-only / generated / non-policy-docs, control-plane **excluded**) → append log. | P0 | — |
| **🚦 VALIDATION GATE** | Prove it discriminates and never under-classifies | Two-sided, held-out, k≥5-run worst-case: recall=1.0 on T2/T3 **and** precision floor on T0; report Clopper-Pearson FN bound; agent-escalation-usefulness count. | P1 | **go/no-go** |
| **P2 — Dogfood live + audit** *(gated)* | Real pers/ PRs + independent audit channel | Wire into PR-open on ship/dossier; auto-merge the safe slice for real; sample K% of T0/T1 weekly for blind re-labeling; track escape rate. | gate | — |
| **P3 — Team ballot** *(parked; trigger = a real second human)* | Add author self-score + override as a small pure aggregation reducer (home TBD). | one-line stub. | P2 | — |
| **P4 — Broaden** *(parked; trigger = P2 evidence)* | Propose to the work team; adapt signals to their domain. | one-line stub. | P3 | — |

Scope: P0 is corpus + rubric judgment; P1 is a deterministic engine (globs+regex) + a thin agent call + a narrow auto-merge rule. P3/P4 are deliberately unspecced (killer-per-step).

## 10. Team phase — the deferred ballot (design, not v0 scope)

When there's a second human, reintroduce the two-scorer idea *properly*:
- Author self-scores; the **information problem is real** only with a distinct reviewer.
- The v1 incentive bug (self-score can only raise → decays to T0) is fixed by giving the score a **downward** lever with friction: author may propose `T0-with-justification` that the agent/owner must actively rebut. Honesty can then occasionally *buy less* review, not only cost more.
- Aggregation of {agent tier, author tier} is a **small pure reducer** (`max` policy + logged override), home decided at that point — not assumed into `sense` (no such engine exists) and not hand-rolled ad hoc.
- The mismatch log is **rubric-facing, not people-facing**: aggregate by signal/path to tune the rubric; no per-author attribution (that's the scoreboard the non-goals forbid).

## 11. Validation plan (rebuilt)

The v1 gate measured self-consistency against a corpus derived from the rubric. v2:

**Independence.** The held-out corpus is labeled by a labeler who has **not** read `RUBRIC.md`, from a neutral prompt ("does this PR need a human, and how much?"). Gate on the held-out set only; the authoring set is for debugging. In solo pers/, the independent labeler is a *fresh agent with only the neutral prompt* — disagreements with the operator's labels are themselves signal.

**Forward validation.** Freeze the rubric SHA, then label the next N incoming PRs *before* classifying them. Retrospective git-history corpora curate toward labelable (rubric-covered) cases; forward PRs don't.

**Red-team rows.** An adversarial pass whose job is to construct a genuinely dangerous PR that scores ≤T1. Every success is a new corpus row *and* a taxonomy gap to close. (This doc's own §2/T3 came from exactly that.)

**Non-determinism.** Run each corpus PR **k≥5** times; gate on **worst-case** (any single miss in any run = fail); report per-PR miss frequency.

**Two-sided gate:**
- **Recall = 1.0 on T2/T3** held-out, worst-case across runs, **reported with the Clopper-Pearson upper bound** on the true FN rate (at n≈15 positives, zero misses only bounds FN < ~20% — the corpus is a *screen*, not the guarantee).
- **Precision floor on T0:** ≥ X% of true-T0 held-out PRs must classify T0 — otherwise a degenerate always-T3 classifier "passes." Report the full confusion matrix.

**The real guarantee is the audit channel, not the corpus.** In P2, sample K% of auto-merged/T0/T1 PRs each week for independent blind re-labeling; the **T0 escape rate** is the only number allowed to speak to safety. `%T0` and review-latency measure *throughput*, not safety — a rubric that quietly under-fires posts *better* throughput numbers, so they can never certify safety on their own. Attribute any production incident back to its triage tier.

## 12. What changed v1 → v2 (adversarial review response)

Four independent skeptics (fail-open, incentives, oracle, architecture) reviewed v1. Dispositions:

- **T1 — control plane auto-merged as "docs" (fail-open, BLOCKER).** Accepted. → §5.4 (control-plane T3), §5.3 (lockfiles/manifests), §5.2 (content/deletion signals), §5.5 (policy-as-data), §7 (fail-closed corrected).
- **T2 — deterministic floor is the real safety mechanism, built last (architecture + oracle, BLOCKER).** Accepted. → pulled forward as the primary mechanism (§4, §5); agent demoted to advisory-that-must-earn-its-slot (§6, D1).
- **T3 — human scorer/override inert in solo, unstable in team; v0 never skips a review (incentives + architecture, MAJOR).** Accepted. → self-score/override deferred to team phase (§10, D4); v0 auto-merges a narrow safe slice from day one (§9, D6).
- **T4 — circular oracle, weak/one-sided gate, n too small, single nondeterministic pass (oracle, BLOCKER).** Accepted. → §11 rebuilt: held-out independent labels, forward validation, red-team rows, k-run worst-case, precision floor, Clopper-Pearson bound, audit channel as the real guarantee.
- **Scope — RESOLVED 2026-07-02: skill-first.** The runtime is the `/pr-risk` **skill** in the registry (`~/.claude/skills/pr-risk/`); this repo is its backing **engine + design home** (`internal/floor`, `RUBRIC.md`, `labels/`, docs). Skills compose (prose), code lives in a code repo with tests. The `sense`-fold option the architecture critic raised is void (`sense` has no aggregation engine). The deferred reconciler (§10) gets a home when the team phase arrives.
- **Pushed back on:** the oracle critic's "300 positives for <1% FN" — infeasible, and the critic concedes it argues for corpus-as-screen + production audit, which §11 now adopts instead. The architecture critic's "the agent may never ship" is held as an explicit hypothesis (§6), not a foregone conclusion — the content signals (§5.2) reclaim much of what looked "semantic," leaving a genuine but small residual for the agent to prove itself on.
