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
> - **v4** — v2's own fix was wrong: `.ship.json` (config, nested, `require`) and `Declaration` (evidence, flat, `expected`) are deliberately different shapes, so parsing the contract type directly would have broken `Expected` too — extend the wrapper and keep translating. Also caught that `review-panel-v1.json` sets `additionalProperties:false`, so the schema must gain `require_at_tier` or it rejects every artifact the feature emits. §9 precision: the threshold is a parameter to the analysis, not something historical logs carry.
> - **v9** — closed a **fail-open this feature would have introduced**: a parseable-but-empty declaration (`require: []` with a threshold) sets the threshold while marking `Unknown:["declaration"]`, so a T0 run reached the shortcut and passed — turning today's malformed-declaration escalation into a pass. Step 2b now escalates on an untrustworthy policy *source* at any tier. §4.2's preferred option is downgraded to unsettled: it relocates the park rather than removing it, and it breaks the §9 analyzer (§10.7).
> - **v7/v8** — §4.2's recommendation needed a task (`reviewVerdictIDs`' early return) and a criterion that did not contradict it.
> - **v5/v6** — chased references v3 left stale (§10.1 still asked the refuted question and named the ruled-out option B), then fixed the one that mattered most: §4.2's "soft skip" is a **hard** skip in CI, because the enforced workflow always passes `-reviews-optional` and consolidation never runs. Also: no existing test enforces Go↔schema parity, so v4's claim that conformance would catch an omitted property was false.

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

**"Soft" is not available in the path that matters, and an earlier draft of this section was simply wrong about it.** The claim was that consolidation is a separate rung, so a below-threshold PR with an actionable bot finding would still park. Review checked the enforced workflow and it does not hold:

- [`.github/workflows/gate.yml:384`](../../../.github/workflows/gate.yml) **always** passes `-reviews-optional`.
- With that set, `reviewVerdictIDs` returns immediately after panel completeness ([`main.go:551-553`](../../../cmd/gate/main.go)) — consolidation never runs.

So in CI, panel completeness is the **only** review-derived control. Skip it at T0 and an actionable bot finding cannot park the run: the exact failure "soft" was chosen to prevent. Locally, without `-reviews-optional`, soft behaves as described — which is why the error survived a reading that did not check the enforced invocation.

The design must therefore pick explicitly, and cannot have both:

| | Behaviour in CI | Cost |
|---|---|---|
| **Accept hard skip at T0** | No review control at all below the threshold. | Simple, and arguably what "T0" should mean — but it is strictly weaker than today, and §4.2's original justification evaporates. |
| **Keep consolidation when the panel is skipped** | `-reviews-optional` stops implying "skip consolidation" when the tier is below the threshold. One of the two review controls may be dropped, never both. | **Preferred, but NOT yet settled** — two unresolved problems, §10.7. |

The recommended option carries a cost worth naming: it inverts the spend curve. T0 PRs — the cheapest, lowest-risk ones — become the only PRs paying for the model rung in CI, because higher tiers keep the deterministic panel instead. That is defensible (paying a little on the work you are *not* reviewing is the point) but it is a real change to §4.3's cost story and should not be discovered at adoption. Flagged as an open question in §10.6 for whoever locks the design.

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

**Gap surfaced in review.** The field being on the contract type is not sufficient: `fetchExpectedReviewers` currently parses an **anonymous struct** carrying only `require`, so `Declaration.RequireAtTier` would silently stay `""` no matter what `.ship.json` says — and by §8 that reads as "required at every tier", i.e. the feature would appear to do nothing.

**But "parse `reviewpanel.Declaration` directly" — v2's fix — is also wrong, and would break `Expected` as well.** The two are different shapes on purpose. `.ship.json` is a *config file*:

```json
{ "review": { "require": ["codex", "claude"], "require_at_tier": "T1" } }
```

`Declaration` is an *evidence artifact*: flat, `expected` not `require`, plus `path`/`revision` that have no config counterpart. `fetchExpectedReviewers` also normalizes (lowercase, trim, dedupe, reject blanks) between the two. The correct fix is therefore to **extend the existing nesting wrapper and keep translating**:

```go
var declaration struct {
    Review struct {
        Require       []string `json:"require"`
        RequireAtTier string   `json:"require_at_tier"`
    } `json:"review"`
}
```

The config→evidence translation is a real boundary, not incidental duplication. Collapsing it would couple the artifact schema to the config file's shape.

**The JSON schema must be updated in the same change.** [`contracts/reviewpanel/schema/review-panel-v1.json`](../../../contracts/reviewpanel/schema/review-panel-v1.json) declares `declaration` with `"additionalProperties": false`, so until `require_at_tier` is added to its `properties`, the schema rejects **every artifact the feature produces** — for any external consumer validating against the portable contract, even though gate's own Go decoder accepts it. Adding an optional property does not bump `schema_version`.

**Nothing currently catches that omission, and an earlier draft claimed otherwise.** `TestSchemaCollectionAndStateConformance` ([`reviewpanel_test.go:62-94`](../../../contracts/reviewpanel/reviewpanel_test.go)) checks collection shapes and the reviewer-state enum only — it never compares `Declaration`'s fields against `declaration.properties`, nor validates a marshaled artifact against the schema. P3 could ship the Go field, omit the schema property, and pass every existing contract test, recreating exactly the rejection above. **P3 must add Go↔schema parity coverage** (or a concrete marshaled-artifact validation) so the next additive field cannot repeat it.

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

**The sibling case is NOT self-protecting, and an earlier draft claimed it was.** The claim: when the declaration is unreadable, `Expected` is empty and `threshold` is `""`, so step 2 routes to "required" before step 5 fires. Review found the hole — a declaration can be *parseable but empty*:

```json
{ "review": { "require": [], "require_at_tier": "T1" } }
```

`fetchPanel` parses this successfully, records an empty `Expected`, marks `Unknown: ["declaration"]` ([`panel.go:41-45`](../../../cmd/gate/internal/evidence/panel.go)) — **and, once the translation lands, a populated threshold.** A T0 run then reaches step 5 and passes, converting today's malformed-declaration escalation into a pass. That is a fail-open in the merge boundary, produced by this feature.

The fix is to split what "unknown" means, because the two cases are not alike:

- **The policy source is untrustworthy** (`Unknown` contains `"declaration"`, or `Expected` is empty) → **escalate, before the threshold shortcut**. A threshold read out of a declaration we could not validate is not a policy, and §7.1 gains this as **step 2b**, ahead of step 5.
- **The panel's state is merely undeterminable** (`Unknown` holds reviewer names — the requested-reviewers fetch failed while the declaration was sound) → below threshold, pass. No requirement is in doubt.

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
| `Unknown` contains `"declaration"`, or `Expected` empty | Escalate | **At any tier.** The policy source is untrustworthy, so its threshold is not a policy. Step 2b, ahead of the shortcut. |
| `Unknown` holds reviewer names **and panel required** | Escalate | Unchanged. |
| `Unknown` holds reviewer names **and panel not required** | Pass | No requirement is in doubt, so undeterminable panel *state* is not a park. |
| Tier ≥ threshold | Today's checks | Unchanged. |

The invariant to state in the test name: **no input to this rung can turn a would-be escalate into a pass except a valid tier strictly below a valid declared threshold.**

---

## 9. Rollout plan

| Phase | Goal | High-level tasks | Depends on | Scope |
|---|---|---|---|---|
| **P1 — pin the trust path** | Make §7.2 an asserted invariant before anything depends on it. | **URL-inspection** test (not behavioural) asserting the declaration fetch carries no ref parameter; comment at the fetch site naming the security property. | — | ~40 LOC, tests only |
| **P2 — thread the tier** | Tier reaches the panel rung; behaviour identical. | Add `floorTier` param to `PanelCompleteness` + `reviewVerdictIDs`; pass from the floor verdict; existing tests green unchanged. | P1 | ~60 LOC |
| **P3 — the policy** | `require_at_tier` decodes, is honoured, fails closed. | `Declaration.RequireAtTier`; **add `require_at_tier` to `review-panel-v1.json`'s `declaration.properties`** or `additionalProperties:false` rejects every artifact the feature makes; **extend the `.ship.json` nesting wrapper and keep translating** — do *not* parse `Declaration` directly (§5); **add Go↔schema parity coverage** — nothing today catches an omitted schema property; **make `reviewVerdictIDs`' `-reviews-optional` early return ([`main.go:551-553`](../../../cmd/gate/main.go)) conditional on the panel having been *required*** — §4.2's recommended option is a caller-side change, not just a policy statement, and without it CI keeps hard-skipping; §7.1 decision flow; §8 table as a table test; verdict `why` wording. | P2 | ~220 LOC |
| **VALIDATION GATE** | Does this actually remove the parks it was built for? | Offline analysis over `log.jsonl`: replay the §7.1 decision against each recorded run's floor tier + panel evidence, diff against recorded outcomes. **Not `gate backtest`** — see the note below. | P3 | ~80 LOC, throwaway |
| **P4 — adopt** | Turn it on for workbench. | Set `require_at_tier: "T1"` in workbench `.ship.json`; observe. | Gate | ~5 LOC |
| **P5 — docs** | README + DESIGN describe the policy and its trust path. | `cmd/gate/README.md`, `docs/DESIGN.md`. | P4 | ~60 LOC |

**The validation instrument is a pure analysis over the recorded log — not `gate backtest`.** v1 of this doc claimed backtest "replays recorded runs." That is false, and the name plus its own doc comment ("replays the gate over historical PRs") is why: `runBacktest` calls `runGate` ([`main.go:1793`](../../../cmd/gate/main.go)), which calls `evidence.Gather` → `fetchPanel` → the **live GitHub API**. It replays *the gate*, not *the runs*, against today's PR state. It also mints a T2 ephemeral grant, so a T3 PR parks on the ceiling regardless of this feature.

Using it here would have made the gate unfalsifiable in two ways: criterion 1 could not be measured before P4 puts the threshold on the default branch (backtest reads `.ship.json` live and would find none), and criterion 2 would be confounded by evidence that changed in the intervening time — a PR that parked historically may simply have been reviewed since, so a pass could not be attributed to this feature.

**The §7.1 decision is a pure function of `(floorTier, threshold, panel evidence)`, and two of the three are already recorded on every historical run.** The floor verdict's tier and the panel evidence are in the log; the *threshold* is not, because no historical run had one — it is supplied as a **parameter to the analysis** (`"T1"` for criterion 1), which is precisely what lets the gate be measured before P4 puts anything on the default branch.

So the validation is an offline analysis over `log.jsonl`: for each recorded run, read its floor tier and panel evidence, evaluate the new flow at the candidate threshold, and diff against the recorded outcome. Deterministic, no live fetches, no evidence-drift confound, and no new gate verb. `gate backtest` stays what it is — a live dry-run preview, useful, but not this.

---

## 10. Open questions

1. **§4.1** — The panel is today a compensating control for floor accuracy, and option A removes it. The case for A is *empirical* — the marginal risk is bounded by the compiled-in `-repo` overrides already covering the known blind spot. Is that bound good enough, or does removing a defense-in-depth layer warrant option C (path-class guard) regardless of the measured rate? **C, not B** — B is ruled out in §4.1 (a mis-tiered path emits a T0 signal, so the veto clears for the same reason A does).
2. **Should `require_at_tier` also gate `review-consolidation`?** §4.2 recommends no — consolidation is governed by `-reviews-optional` and is a different question. But a repo setting `require_at_tier: "T1"` may reasonably expect T0 PRs to skip the paid rung too. P4 adoption will likely surface this empirically; **record the decision and its reasoning in `docs/DESIGN.md` during P5** so the follow-on PR does not have to reconstruct the argument from scratch.
3. **Per-reviewer thresholds?** e.g. codex required at T1, claude only at T2. Deferred — no evidence the extra expressiveness is wanted, and it multiplies the policy surface.
4. **Does the escalation brief need the threshold?** When a PR parks *because* it is at or above the threshold, naming the threshold in the brief might help the judge. Cheap, but speculative until observed.
7. **The conditional-consolidation option is not implementable as stated — resolve before P3.** Review surfaced two problems with §4.2's preferred option, and neither has an answer yet:
   - **It relocates the park rather than removing it.** `verify.Reviews` escalates when there are no eligible bot comments ([`reviews.go:140-145`](../../../cmd/gate/internal/verify/reviews.go)). The common case this feature targets — a T0 PR with no reviews and no comments — would skip the panel and then park on consolidation instead. Extraction failures and low-confidence non-actionable comments park too. The skipped-panel path needs its own consolidation semantics (an empty comment set is a *pass* there, not an escalate), or the option delivers nothing.
   - **It breaks the §9 validation instrument.** Once consolidation is conditional, the outcome stops being a pure function of `(floorTier, threshold, panel evidence)`: `verify.Reviews` consumes recorded comments and calls the model per comment, and historical `-reviews-optional` runs stored no consolidation verdict to reuse. An analyzer over those three inputs alone cannot produce the pass-vs-consolidation-escalate split criterion 1 now requires. Either the analyzer must replay consolidation over the recorded comments, or criterion 1 must be measurable without that split.

   Until both are answered, §4.2's table is a live choice, not a recommendation. Hard-skip remains selectable and is the cheaper answer if these prove expensive.
6. **Does T0 pay for consolidation in CI?** §4.2's recommended option means `-reviews-optional` stops implying "skip consolidation" when the tier is below the threshold — so T0 PRs become the only ones paying for the model rung in the enforced path, while higher tiers keep the deterministic panel. Defensible (you pay a little on the work you are *not* reviewing), but it inverts the spend curve and should be locked deliberately, not discovered at P4.
5. **Retrospective auditability of a skip.** `Declaration.Revision` carries the **blob SHA** returned by the contents API, which identifies content but not the branch or commit it came from. So state alone cannot prove a given skip used *default-branch* policy — the live path is guarded by §7.2's URL-inspection test, but the recorded evidence does not carry the default-branch HEAD at fetch time. Recording it alongside the blob SHA would close this. Not a blocker; it only matters if retrospective audit of policy provenance becomes a requirement. Noted in P1.

---

## 11. Validation plan

**Binary go/no-go**, measured at the gate after P3:

0. **The corpus is diverse enough to measure anything.** Before reading criteria 1–4, confirm the recorded-log corpus contains at least one run from **each** escalation class: readiness block, floor tier block, panel-completeness park, CI escalate, consolidation escalate. If any class has zero coverage, say so explicitly — a corpus dominated by panel parks (likely, since that is the observed problem) satisfies criterion 2 trivially while exercising none of the paths a defect would live in. **Absent coverage is not evidence of no regression.**
1. **It removes the parks it targets — minus the ones §4.2 deliberately keeps.** The §9 offline analysis over `log.jsonl`, run at threshold `"T1"`: every recorded run whose *only* escalation reason was `review-panel-completeness` at floor tier T0 now evaluates to pass, **except** those whose recorded comments carry an actionable finding. Under §4.2's recommended option those re-park on consolidation instead — a *different* rung, not the panel — and that is the feature working, not failing.

   So the measurement is two-part, and conflating them would either hide a regression or read a success as one: the panel-park class goes to zero, and every run that leaves it lands on **pass** or on **consolidation-escalate**, never on anything else. A panel-only T0 park that still escalates for a third reason is a defect. (Not `gate backtest` — §9.)
2. **It removes nothing else.** No run outside class (1) changes from a **non-pass gate outcome to a pass gate outcome**. Measured on the gate *outcome* (exit code / action taken), not on individual rung verdicts — a run that escalated for panel completeness *and* another reason will legitimately show a changed panel verdict while its overall outcome stays parked, and that is not a regression. The looser wording ("changes outcome") also swept in harmless transitions like block→escalate; this is the criterion that must actually be falsifiable.
3. **Fail-closed holds.** The §8 table passes as an explicit test, including the malformed-policy and invalid-tier rows.
4. **The trust path is pinned.** P1's URL-inspection test fails if the declaration fetch is ever changed to read the PR head.

Failing (2) or (3) means the design is wrong, not that the thresholds need tuning — stop and revisit §4.1. Failing (0) means the gate has not been measured at all yet.
