# Tier-aware review panel requirement — Technical Design Document

**Status:** draft / proposal — NOT a build commitment. The artifact we decide from.
**Owner:** @itsHabib
**Date:** 2026-08-03
**Related:** `cmd/gate/docs/DESIGN.md` (ladder law, verdict schema) · `cmd/gate/README.md` (risk-scaled autonomy) · `.ship.json` (panel declaration) · dossier project `gate`

> **Reviewers — focus areas:**
> - **§4.1** — the floor-coupling decision. This is the one that actually costs something; everything else follows from it. **Revised after review**: the first draft's "widening not creating" argument was wrong and is retained, refuted, as a record.
> - **§4.2** — hard skip vs. "not required but still consolidated". Changes what a T0 pass means.
> - **§7.2** — the trust path for the policy value. If this is wrong, a PR can lower its own review bar.
> - **§8** — the fail-closed table. An unreadable tier must never widen the gate.
>
> **Revision history:**
> - **v2** — §4.1 argument replaced (the original was refuted), option B ruled out, §5 parser gap, §7.2 test design, §8 malformed row, §11 corpus-diversity gate and a falsifiable criterion 2.
> - **v3** — the validation instrument was wrong: `gate backtest` re-gathers **live** evidence and does not replay recorded runs, so the gate as written was unmeasurable. Replaced with an offline analysis over `log.jsonl` (§9, §11). Resolved a §7.1/§8 contradiction on `Unknown` + below-threshold that would have had implementers and test authors disagree. Added §10.5 on policy-provenance auditability.

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

**Name the cost precisely.** An earlier draft of this section argued the change merely "widens an existing trust dependency rather than creating one," on the grounds that gate already trusts the floor's tier for the capability ceiling. Review refuted that, correctly, and the refutation is recorded here because the wrong framing is the more persuasive one:

- **Today:** floor under-tiers T3→T0 → the capability ceiling may be satisfied → **but the panel still runs**, and its findings still park the PR.
- **After:** floor under-tiers T3→T0 → ceiling satisfied → **panel also skipped** → both controls fail together.

The panel is currently a *compensating control* for floor accuracy. This change removes that compensation. That is not widening a dependency — it is eliminating a defense-in-depth layer, and the structural-parity framing conceals it.

Options:

| Option | Behaviour | Trade-off |
|---|---|---|
| **A. Accept the coupling** | T0 → panel not required. | Simplest, matches the risk-scaled thesis. Removes the compensating control. |
| **B. Floor-signal veto** | T0 skips the panel *only if* the floor recorded no signal above T0. | **Confirmed ineffective — see below.** |
| **C. Path-class guard** | T0 skips only when every changed path is in a declared docs-like allowlist. | The real fallback. Strongest, but duplicates classification deliberately delegated to `triage-floor`. Two classifiers to keep in sync. |

**Option B does not work, and the draft's own parenthetical was right.** `floorResult.Signals` contains only the signals the classifier *recognized*. A path that tricks the classifier into reading code as docs produces a **T0 signal** — it is not silently absent. So "no signal above T0" passes for exactly the same reason option A does. B buys nothing over A. If reviewers want a genuine fallback, it is C.

**Recommendation: A** — but on an empirical argument, not a structural one. The compensating control's value is proportional to the floor's misclassification rate. The floor's known blind spot (gate's own machinery) is covered by the compiled-in `-repo` path overrides in [`floor.go`](../../../cmd/gate/internal/verify/floor.go), which already exist and are already load-bearing for the capability ceiling. The marginal risk from removing the panel as compensation is bounded by that coverage.

**Open for the reviewer:** is that empirical bound good enough, or does removing a defense-in-depth layer warrant option C regardless of the measured rate? §10.1.

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

**Gap surfaced in review.** The field being on the contract type is not sufficient: `fetchExpectedReviewers` currently parses an **anonymous struct** carrying only `require`, so `Declaration.RequireAtTier` would silently stay `""` no matter what `.ship.json` says — and by §8 that reads as "required at every tier", i.e. the feature would appear to do nothing. Fix by having `fetchExpectedReviewers` parse `reviewpanel.Declaration` directly instead of a parallel struct: one type, no drift, and the contract is the parser.

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

**Step 5 deliberately fires before the `Unknown` check, and that is the intended reading.** Review surfaced the case: floor T0, threshold T1, declaration parsed, but `fetchRequestedReviewers` fails so `panel.Unknown` is populated ([`panel.go:48-50`](../../../cmd/gate/internal/evidence/panel.go)). Step 5 passes and step 6's `Unknown` escalate is never reached. **If the panel is not required at this tier, being unable to determine the panel's state is not a reason to park** — there is no requirement whose satisfaction is in doubt.

The dangerous-looking sibling case is self-protecting: when the *declaration itself* is unreadable, `Unknown` is `["declaration"]` and `Expected` is empty, so `threshold` is `""` and step 2 routes to today's behaviour (required) before step 5 can fire. A run can never skip the panel on a threshold it failed to read.

§8's `Unknown` row is qualified accordingly. Getting this wrong in either direction is a real defect: a test author implementing §8 literally would assert `escalate` for `{T0, T1, Unknown:["codex"]}` and the implementation would emit `pass`.

### 7.2 Trust path for the policy value — the one that must not regress

`fetchExpectedReviewers` ([`cmd/gate/internal/evidence/panel.go`](../../../cmd/gate/internal/evidence/panel.go)) reads the declaration via:

```go
gh("api", fmt.Sprintf("repos/%s/contents/%s", repo, panelDeclarationPath))
```

with **no `?ref=`**, so GitHub serves the **default branch**, not the PR head. A PR therefore cannot alter the panel it will be judged against — and, once this lands, cannot lower its own `require_at_tier` either. **This is the property that makes the whole design safe, and it is already true.**

**The signature is the structural invariant.** `fetchExpectedReviewers(repo string)` takes only the repo — the head SHA is not a parameter, so a future developer cannot construct `?ref=<head>` by accident. That is stronger than the draft credited, and it means the test is a guard against two specific regressions rather than the sole defense:

1. someone adds `headSHA string` to the signature "for some other purpose" and then uses it in the URL;
2. a refactor of how `fetchPanel` calls it reintroduces the parameter implicitly.

**The test must inspect the URL, not the behaviour.** A behavioural test — "the content returned is the default branch's" — passes on `?ref=main` when the default branch is `main`, which is precisely *not* the invariant. Spy on the `gh` call and assert the constructed path carries no ref parameter.

**The comment at the fetch site is load-bearing, not decorative.** A one-line note naming the security property ("no `?ref=` so GitHub serves the default branch; a PR cannot lower its own review bar") is what stops the well-meaning "correctness" fix. Without it, that fix is *more* likely, not less.

### 7.3 Degraded paths

- `.ship.json` unreadable → `Unknown: ["declaration"]` → escalate (unchanged, and reached before any threshold logic).
- Floor rung failed → `runGate` already returns `codeError` before the panel rung runs.
- Floor tier present but not in `tier.Valid` → cannot occur via `Floor` (`parseFloorOutput` refuses it) but is handled defensively at step 3.

---

## 8. Fail-closed table

| Condition | Outcome | Why |
|---|---|---|
| `require_at_tier` absent | Panel required | Opt-in; no silent behaviour change for any existing repo. |
| `require_at_tier` malformed (`"T1x"`) | Escalate | A policy that does not parse is not a policy. Test this row **explicitly**: the invariant permits either escalate or required, so without a pinned row a later refactor can quietly convert it to "required" and lose the visibility the escalate was chosen for. |
| Floor tier empty / invalid | Panel required | Absence of signal never reads as low risk. |
| Panel evidence head ≠ judged head | Escalate | Integrity check precedes policy. Fires regardless of tier. |
| `Unknown` non-empty **and panel required** (step 5 did not fire) | Escalate | Unchanged. |
| `Unknown` non-empty **and panel not required** (step 5 fired) | Pass | No requirement is in doubt, so undeterminable panel state is not a park. An unreadable *declaration* cannot reach here — see §7.1. |
| Tier ≥ threshold | Today's checks | Unchanged. |

The invariant to state in the test name: **no input to this rung can turn a would-be escalate into a pass except a valid tier strictly below a valid declared threshold.**

---

## 9. Rollout plan

| Phase | Goal | High-level tasks | Depends on | Scope |
|---|---|---|---|---|
| **P1 — pin the trust path** | Make §7.2 an asserted invariant before anything depends on it. | **URL-inspection** test (not behavioural) asserting the declaration fetch carries no ref parameter; comment at the fetch site naming the security property. | — | ~40 LOC, tests only |
| **P2 — thread the tier** | Tier reaches the panel rung; behaviour identical. | Add `floorTier` param to `PanelCompleteness` + `reviewVerdictIDs`; pass from the floor verdict; existing tests green unchanged. | P1 | ~60 LOC |
| **P3 — the policy** | `require_at_tier` decodes, is honoured, fails closed. | `Declaration.RequireAtTier`; **switch `fetchExpectedReviewers` to parse `reviewpanel.Declaration` instead of its anonymous struct** (§5) or the field never populates; §7.1 decision flow; §8 table as a table test; verdict `why` wording. | P2 | ~150 LOC |
| **VALIDATION GATE** | Does this actually remove the parks it was built for? | Offline analysis over `log.jsonl`: replay the §7.1 decision against each recorded run's floor tier + panel evidence, diff against recorded outcomes. **Not `gate backtest`** — see the note below. | P3 | ~80 LOC, throwaway |
| **P4 — adopt** | Turn it on for workbench. | Set `require_at_tier: "T1"` in workbench `.ship.json`; observe. | Gate | ~5 LOC |
| **P5 — docs** | README + DESIGN describe the policy and its trust path. | `cmd/gate/README.md`, `docs/DESIGN.md`. | P4 | ~60 LOC |

**The validation instrument is a pure analysis over the recorded log — not `gate backtest`.** v1 of this doc claimed backtest "replays recorded runs." That is false, and the name plus its own doc comment ("replays the gate over historical PRs") is why: `runBacktest` calls `runGate` ([`main.go:1793`](../../../cmd/gate/main.go)), which calls `evidence.Gather` → `fetchPanel` → the **live GitHub API**. It replays *the gate*, not *the runs*, against today's PR state. It also mints a T2 ephemeral grant, so a T3 PR parks on the ceiling regardless of this feature.

Using it here would have made the gate unfalsifiable in two ways: criterion 1 could not be measured before P4 puts the threshold on the default branch (backtest reads `.ship.json` live and would find none), and criterion 2 would be confounded by evidence that changed in the intervening time — a PR that parked historically may simply have been reviewed since, so a pass could not be attributed to this feature.

**The §7.1 decision is a pure function of `(floorTier, threshold, panel evidence)`, and all three are already recorded on every historical run.** So the validation is an offline analysis over `log.jsonl`: for each recorded run, read the floor verdict's tier and the panel evidence, evaluate the new flow, and compare against the recorded outcome. Deterministic, no live fetches, no confound, and no new gate verb. `gate backtest` stays what it is — a live dry-run preview, useful, but not this.

---

## 10. Open questions

1. **§4.1** — Is widening an existing trust dependency on `triage-floor` materially different from creating one? If reviewers think yes, option B (floor-signal veto) is the fallback.
2. **Should `require_at_tier` also gate `review-consolidation`?** §4.2 recommends no — consolidation is governed by `-reviews-optional` and is a different question. But a repo setting `require_at_tier: "T1"` may reasonably expect T0 PRs to skip the paid rung too. P4 adoption will likely surface this empirically; **record the decision and its reasoning in `docs/DESIGN.md` during P5** so the follow-on PR does not have to reconstruct the argument from scratch.
3. **Per-reviewer thresholds?** e.g. codex required at T1, claude only at T2. Deferred — no evidence the extra expressiveness is wanted, and it multiplies the policy surface.
4. **Does the escalation brief need the threshold?** When a PR parks *because* it is at or above the threshold, naming the threshold in the brief might help the judge. Cheap, but speculative until observed.
5. **Retrospective auditability of a skip.** `Declaration.Revision` carries the **blob SHA** returned by the contents API, which identifies content but not the branch or commit it came from. So state alone cannot prove a given skip used *default-branch* policy — the live path is guarded by §7.2's URL-inspection test, but the recorded evidence does not carry the default-branch HEAD at fetch time. Recording it alongside the blob SHA would close this. Not a blocker; it only matters if retrospective audit of policy provenance becomes a requirement. Noted in P1.

---

## 11. Validation plan

**Binary go/no-go**, measured at the gate after P3:

0. **The corpus is diverse enough to measure anything.** Before reading criteria 1–4, confirm the backtest corpus contains at least one recorded run from **each** escalation class: readiness block, floor tier block, panel-completeness park, CI escalate, consolidation escalate. If any class has zero coverage, say so explicitly — a corpus dominated by panel parks (likely, since that is the observed problem) satisfies criterion 2 trivially while exercising none of the paths a defect would live in. **Absent coverage is not evidence of no regression.**
1. **It removes the parks it targets.** `gate backtest` over the recorded run history: with `require_at_tier: "T1"`, every run whose *only* escalation reason was `review-panel-completeness` at floor tier T0 now passes. Target: that class goes to zero.
2. **It removes nothing else.** No run outside class (1) changes from a **non-pass gate outcome to a pass gate outcome**. Measured on the gate *outcome* (exit code / action taken), not on individual rung verdicts — a run that escalated for panel completeness *and* another reason will legitimately show a changed panel verdict while its overall outcome stays parked, and that is not a regression. The looser wording ("changes outcome") also swept in harmless transitions like block→escalate; this is the criterion that must actually be falsifiable.
3. **Fail-closed holds.** The §8 table passes as an explicit test, including the malformed-policy and invalid-tier rows.
4. **The trust path is pinned.** P1's URL-inspection test fails if the declaration fetch is ever changed to read the PR head.

Failing (2) or (3) means the design is wrong, not that the thresholds need tuning — stop and revisit §4.1. Failing (0) means the gate has not been measured at all yet.
