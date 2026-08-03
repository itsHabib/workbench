# Tier-aware review panel requirement — Technical Design Document

**Status:** draft / proposal — NOT a build commitment. The artifact we decide from.
**Owner:** @itsHabib
**Date:** 2026-08-03
**Related:** `cmd/gate/docs/DESIGN.md` (ladder law, verdict schema) · `cmd/gate/README.md` (risk-scaled autonomy) · `.ship.json` (panel declaration) · dossier project `gate`

> **Reviewers — focus areas:**
> - **§4.1** — the floor-coupling decision. This is the one that actually costs something; everything else follows from it.
> - **§4.2** — hard skip vs. "not required but still consolidated". Changes what a T0 pass means.
> - **§7.2** — the trust path for the policy value. If this is wrong, a PR can lower its own review bar.
> - **§8** — the fail-closed table. An unreadable tier must never widen the gate.

---

## 1. Problem & hypothesis

Gate's README states that autonomy is risk-scaled: clean low-risk work is cleared to merge, high-risk work parks for a human. The **decision** honours that — the reducer composes tiers monotone-max and the capability ceiling is checked against the result. The **review requirement** does not.

`verify.PanelCompleteness` ([`cmd/gate/internal/verify/panel.go:16`](../../../cmd/gate/internal/verify/panel.go)) takes no tier parameter:

```go
func PanelCompleteness(st *state.Store, run, evidenceID string, subject Subject) (state.Artifact, error)
```

It escalates whenever the declared panel has not reviewed the exact head, regardless of what the diff actually is, and hardcodes its own verdict at `Tier: "T0"`. The result is that a documentation typo and a rewrite of the capability checker carry identical review obligations.

**Observed, not hypothesised.** Gate run `run_0e59c569db4a0733` on `itsHabib/workbench#193` — a docs-only PR the deterministic floor tiered **T0** — blocked with:

```
review-panel-completeness: review panel incomplete: completed=0 expected=2 missing=[claude, codex]
```

**Hypothesis.** Making the *required* panel a function of the diff-derived tier removes the dominant source of low-value parks without weakening the gate on work that carries real risk. The cheapest correct change is a policy input, not new machinery: the tier is already computed before the panel rung runs.

### Non-goals

- **Not** changing what counts as a completed review (exact-head matching, `Pending`/`Missing`/`Unknown` classification all stay as-is).
- **Not** changing the reducer, the ladder law, or tier composition.
- **Not** touching `-reviews-optional`. That flag governs *GitHub review decisions and paid consolidation*; this governs *the declared bot panel*. They are orthogonal and both keep panel completeness deterministic.
- **Not** weakening any hard block. Readiness blocks (draft, not mergeable, `CHANGES_REQUESTED`, red CI) are untouched.

---

## 2. Requirements

**Functional**

- FR1 — Panel completeness consults the diff-derived tier when deciding whether the declared panel is *required*.
- FR2 — The threshold is declared per-repo in `.ship.json`, not hardcoded in gate.
- FR3 — A repo that declares no threshold behaves exactly as today (panel required at every tier). The change is opt-in.
- FR4 — When the panel is not required, the verdict says so explicitly, naming the tier and the threshold that produced the outcome.
- FR5 — An absent, unreadable, or unknown tier fails closed: the panel is required.

**Non-functional**

| Property | Target |
|---|---|
| Determinism | Panel completeness stays producer class `code`. No model call, no network beyond evidence already gathered. |
| Reconstructability | A pass that skipped the panel must be distinguishable from a pass where the panel completed, from state alone. |
| Ordering | No reordering of the ladder. `verify.Floor` already runs before `reviewVerdictIDs` ([`main.go:490`](../../../cmd/gate/main.go) vs `:499`). |
| Blast radius | One new parameter, one new optional `.ship.json` key. No contract version bump to `reviewpanel.Evidence`. |

---

## 3. Architecture overview

The rung ordering already gives us what we need — nothing moves:

```
evidence.Gather
   ├─ verify.Readiness   (main.go:486)
   ├─ verify.Floor       (main.go:490)  ──► Tier: res.Floor        ◄── already computed here
   └─ reviewVerdictIDs   (main.go:499)
        └─ verify.PanelCompleteness     ◄── currently tier-blind; gains the tier
   ↓
verify.Reduce  →  act  →  capability ceiling
```

The only new data flow is the floor's tier reaching the panel rung. Two candidate seams:

- **(a) Pass the tier value** from the recorded floor verdict into `PanelCompleteness`.
- **(b) Pass the floor artifact id** and have the panel rung load it.

(a) is preferred: it keeps `PanelCompleteness` a pure function of its inputs, matching the other rungs, and avoids a second state read. §6 assumes (a).

The threshold itself rides in the existing panel evidence, so the policy and the panel state it governs arrive together and are recorded together.

---

## 4. Key decisions & trade-offs

### 4.1 Coupling the panel requirement to the floor's accuracy — the real cost

**Today, panel completeness is an independent check.** It does not care what the floor thought. That independence is a genuine safety property: two verifiers disagreeing about a diff is a signal, and a floor that under-tiers a change still leaves the panel requirement standing.

**After this change, the floor's tier gates the panel.** If the floor ever classifies a code change as T0 — a path-override gap, a diff the classifier reads as docs — the change loses required review entirely rather than degrading to a weaker check.

This is the decision that needs a call, not a rubber stamp. Options:

| Option | Behaviour | Trade-off |
|---|---|---|
| **A. Accept the coupling** | T0 → panel not required. | Simplest, matches the risk-scaled thesis. Concentrates trust in `triage-floor`. |
| **B. Floor-signal veto** | T0 skips the panel *only if* the floor recorded no signal above T0. | Narrower blast radius; a mixed diff keeps its review. More logic, and the floor already takes the max — this may be a distinction without a difference. |
| **C. Path-class guard** | T0 skips only when every changed path is in a declared docs-like allowlist. | Strongest, but duplicates classification gate deliberately delegated to `triage-floor`. Two classifiers to keep in sync. |

**Recommendation: A**, with the mitigation that the compensating control already exists — `triage-floor` takes `-repo` and applies that repo's compiled-in path overrides ([`floor.go`](../../../cmd/gate/internal/verify/floor.go), the "gate-machinery blind spot" control). If the floor under-tiers gate's own machinery, that is a floor bug with a known fix location, and it is *already* load-bearing for the capability ceiling today. This change does not create the dependency on floor accuracy; it widens what that accuracy buys.

**Open for the reviewer:** is widening an existing trust dependency materially different from creating one? §10.1.

### 4.2 Hard skip vs. "not required, but still consolidated"

When the tier is below the threshold:

- **Hard skip** — panel completeness passes without examining `Completed`/`Missing`. Bot findings on a T0 PR are never read.
- **Soft** — the panel is not *required*, but if bots did comment, `review-consolidation` still processes them, so an actionable finding still parks.

**Recommendation: soft.** The failure this guards against is real and cheap to avoid: a T0 docs PR where codex flags an actionable problem, and the gate passes without anyone reading it. Note these are already separate rungs — consolidation is governed by `-reviews-optional`, not by panel completeness — so "soft" is close to free: it means *not* short-circuiting the consolidation rung, which panel completeness has no authority over anyway.

### 4.3 Threshold in `.ship.json`, not a flag

The panel declaration already lives at `.ship.json` under `review`. Putting the threshold beside it keeps one source of truth, versions the policy with the repo, and makes it visible in the same evidence artifact. A CLI flag would let the policy vary per invocation, which is precisely what an authorization boundary must not allow.

### 4.4 Threshold semantics: "required at or above"

`require_at_tier: "T1"` means *the panel is required when the floor tier ranks at or above T1*. `T0` therefore skips. A repo wanting today's behaviour sets `"T0"` or omits the key. `tier.Rank` already gives the ordering, and already ranks unknown values highest — which is the fail-closed direction.

---

## 5. Data model

One optional key added to the existing `.ship.json` `review` object:

```json
{
  "review": {
    "panel": [ { "name": "codex", "trigger": "mention" }, { "name": "claude", "trigger": "mention" } ],
    "require": ["codex", "claude"],
    "require_at_tier": "T1",
    "settle_minutes": 15
  }
}
```

`contracts/reviewpanel.Declaration` gains a matching optional field:

```go
type Declaration struct {
    Path          string   `json:"path"`
    Revision      string   `json:"revision,omitempty"`
    Expected      []string `json:"expected"`
    RequireAtTier string   `json:"require_at_tier,omitempty"`  // new; "" = required at every tier
}
```

Additive and optional, so `schema_version` does not bump and every historical panel evidence artifact keeps decoding — the same append-only guarantee pinned by `TestHistoricalVerdictLineStillDecodes` (PR #202).

---

## 6. API contract

```go
// PanelCompleteness gains the diff-derived tier. An empty tier is treated as
// unknown and fails closed (panel required).
func PanelCompleteness(st *state.Store, run, evidenceID string, subject Subject, floorTier string) (state.Artifact, error)
```

Call site ([`main.go:537`](../../../cmd/gate/main.go), inside `reviewVerdictIDs`) threads the tier from the already-recorded floor verdict; `reviewVerdictIDs` gains the same parameter from `runGate`.

Verdict shape when the panel is not required — decision `pass`, and the `why` records the policy that produced it so the audit log can tell an intentional skip from a panel that actually completed (mirroring how readiness records `reviews-optional: absent GitHub review decision accepted`):

```
review panel not required at tier T0 (threshold T1 declared by .ship.json@<rev>)
```

No new error codes. No change to the escalate messages.

---

## 7. Key flows

### 7.1 Decision flow

```
floorTier ← recorded triage-floor verdict
threshold ← panel.Declaration.RequireAtTier

1. subject mismatch (repo/number/head)      → escalate   [unchanged, checked first]
2. threshold == ""                          → today's behaviour (required)
3. !tier.Valid(floorTier)                   → required   [fail closed]
4. !tier.Valid(threshold)                   → escalate   [malformed policy is not a licence]
5. tier.Rank(floorTier) < tier.Rank(threshold)
                                            → pass, why names tier + threshold + revision
6. otherwise                                → today's checks (Unknown / Pending / Missing)
```

Step 1 stays first: an evidence/head mismatch is a integrity failure and must not be short-circuited by a policy that says "no review needed". Step 4 escalates rather than falling back to "required" so a typo'd policy is *visible* rather than silently strict.

### 7.2 Trust path for the policy value — the one that must not regress

`fetchExpectedReviewers` ([`cmd/gate/internal/evidence/panel.go`](../../../cmd/gate/internal/evidence/panel.go)) reads the declaration via:

```go
gh("api", fmt.Sprintf("repos/%s/contents/%s", repo, panelDeclarationPath))
```

with **no `?ref=`**, so GitHub serves the **default branch**, not the PR head. A PR therefore cannot alter the panel it will be judged against — and, once this lands, cannot lower its own `require_at_tier` either. **This is the property that makes the whole design safe, and it is already true.**

It must be pinned by a test, because it is currently an emergent consequence of an omitted parameter rather than an asserted invariant. Adding `?ref=<head>` "for correctness" would silently convert this into a self-service bypass.

### 7.3 Degraded paths

- `.ship.json` unreadable → `Unknown: ["declaration"]` → escalate (unchanged, and reached before any threshold logic).
- Floor rung failed → `runGate` already returns `codeError` before the panel rung runs.
- Floor tier present but not in `tier.Valid` → cannot occur via `Floor` (`parseFloorOutput` refuses it) but is handled defensively at step 3.

---

## 8. Fail-closed table

| Condition | Outcome | Why |
|---|---|---|
| `require_at_tier` absent | Panel required | Opt-in; no silent behaviour change for any existing repo. |
| `require_at_tier` malformed | Escalate | A policy that does not parse is not a policy. |
| Floor tier empty / invalid | Panel required | Absence of signal never reads as low risk. |
| Panel evidence head ≠ judged head | Escalate | Integrity check precedes policy. |
| `Unknown` non-empty | Escalate | Unchanged. |
| Tier ≥ threshold | Today's checks | Unchanged. |

The invariant to state in the test name: **no input to this rung can turn a would-be escalate into a pass except a valid tier strictly below a valid declared threshold.**

---

## 9. Rollout plan

| Phase | Goal | High-level tasks | Depends on | Scope |
|---|---|---|---|---|
| **P1 — pin the trust path** | Make §7.2 an asserted invariant before anything depends on it. | Test that the declaration is fetched without a `ref` / from the default branch; comment naming why. | — | ~40 LOC, tests only |
| **P2 — thread the tier** | Tier reaches the panel rung; behaviour identical. | Add `floorTier` param to `PanelCompleteness` + `reviewVerdictIDs`; pass from the floor verdict; existing tests green unchanged. | P1 | ~60 LOC |
| **P3 — the policy** | `require_at_tier` decodes, is honoured, fails closed. | `Declaration.RequireAtTier`; §7.1 decision flow; §8 table as a table test; verdict `why` wording. | P2 | ~150 LOC |
| **VALIDATION GATE** | Does this actually remove the parks it was built for? | Re-run gate over the last N merged PRs with `backtest`; compare park rate and confirm no PR that *should* have required review passes. | P3 | — |
| **P4 — adopt** | Turn it on for workbench. | Set `require_at_tier: "T1"` in workbench `.ship.json`; observe. | Gate | ~5 LOC |
| **P5 — docs** | README + DESIGN describe the policy and its trust path. | `cmd/gate/README.md`, `docs/DESIGN.md`. | P4 | ~60 LOC |

`gate backtest` already exists and is the right instrument for the gate — it replays recorded runs, so the validation is against real history rather than a synthetic corpus.

---

## 10. Open questions

1. **§4.1** — Is widening an existing trust dependency on `triage-floor` materially different from creating one? If reviewers think yes, option B (floor-signal veto) is the fallback.
2. **Should `require_at_tier` also gate `review-consolidation`?** §4.2 recommends no — consolidation is governed by `-reviews-optional` and is a different question. But a repo setting `require_at_tier: "T1"` may reasonably expect T0 PRs to skip the paid rung too.
3. **Per-reviewer thresholds?** e.g. codex required at T1, claude only at T2. Deferred — no evidence the extra expressiveness is wanted, and it multiplies the policy surface.
4. **Does the escalation brief need the threshold?** When a PR parks *because* it is at or above the threshold, naming the threshold in the brief might help the judge. Cheap, but speculative until observed.

---

## 11. Validation plan

**Binary go/no-go**, measured at the gate after P3:

1. **It removes the parks it targets.** `gate backtest` over the recorded run history: with `require_at_tier: "T1"`, every run whose *only* escalation reason was `review-panel-completeness` at floor tier T0 now passes. Target: that class goes to zero.
2. **It removes nothing else.** No run that escalated for any other reason changes outcome. Any change outside class (1) is a defect, not a win.
3. **Fail-closed holds.** The §8 table passes as an explicit test, including the malformed-policy and invalid-tier rows.
4. **The trust path is pinned.** P1's test fails if the declaration fetch is ever changed to read the PR head.

Failing (2) or (3) means the design is wrong, not that the thresholds need tuning — stop and revisit §4.1.
