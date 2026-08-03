# Workbench UX overhaul — Technical Design Document

**Status:** draft / proposal — **NOT a build commitment.** The artifact we decide *from*.
**Owner:** @itsHabib
**Date:** 2026-08-02
**Revision:** v2 — folds in review round 1 (Codex 4×P1 + 1×P2, Claude full pass). Changes: `-reviews-optional` subordinated to the grant (§4.4); Tier 1 extended to pre-result and substrate failures (§4.3, §7.6); `Degradation.Fatal` removed from `contracts` in favour of objective `Role` (§5); timestamp-only staleness fallback **deleted** as a false-pass path (§4.5, §7.3, §8); per-signal provenance + conservative aggregation for readiness (§5, §6.3). Every code claim in the review was verified against source before folding.
**Evidence base:** `~/dev/friction-log.md` (the portfolio-level cross-cutting log, outside any repo), session 2026-08-02 — 16 entries, one PR-sweep session across workbench / rooms / dossier / ship / orchestra / cc-skills. Every claim in §1 cites an entry; nothing here is speculative UX.
**Related:** [`docs/DESIGN.md`](../../DESIGN.md) (the boundary law this must not move), [`docs/workbench-101.md`](../../workbench-101.md) (the five planes), [`docs/features/gate-approval-ux/spec.md`](../gate-approval-ux/spec.md) (#186 — the committed slice beneath this doc), `cmd/gate/docs/enforcement.md`.

> **Reviewers — focus areas (v2).** This is a **design review**, not a code review. Round 1 is folded in; these are what round 2 should attack.
> 1. **§4.4 (D4)** — round 1 confirmed reading (a) *and* found the hole: `-reviews-optional` already grants the substitution outside any grant. v2 subordinates the flag to grant-signed policy on a custody-domain argument (CI mints its own grant into its own erased state dir, so its rail survives). **Is that argument sound, and is the resulting behavior change to existing callers acceptable?**
> 2. **§4.3 + §7.6 (D3)** — Tier 1 now has to cover terminal states that occur *before a result exists* and failures *of the very state substrate the generic escape route depends on*. Does the extended guarantee hold, or is it still narrower than D3 needs?
> 3. **§5 + §6.3** — per-signal provenance and the conservative aggregation rule. A readiness verdict aggregates N signals with N observation times; is "any member that fails coverage fails the aggregate" worth its false-refusal cost?
> 4. **§4.5 / §7.3 / §8** — the timestamp fallback is **gone**; an unrecoverable base SHA now parks. Confirm no remaining path lets unverifiable coverage produce a pass.
> 5. **§4.2 + §9** — the `unknown` window, and the P3→P4 ordering constraint round 1 surfaced: P3 must stamp *every producer gate itself calls*, or P4 refuses gate's own evidence on its first run.

---

## 1. Problem & hypothesis

One session, seven PRs, five repos. The house delivery loop itself held — worktree → implement → checks → PR → panel → re-verify read exactly as documented, in every repo, and the bot panel found a real P1 within minutes. The tools did not fail at *doing*. They failed at *saying what they knew*.

Sixteen recorded frictions collapse into two structural shapes.

**Theme 1 — tools that gate their own repair.** `gate gate` parked; both judge providers failed; the fixes for both failures (#198, #201) were unmerged PRs, and merging them required the judge. The same shape, independently, in a second subsystem: `sync.sh` reported 13 conflicts because the catalog fix (`cc-skills#18`) was unmerged, so installed skills were newer than the registry that is supposed to be their source. Two subsystems, one property: **the only path to change X was broken in a way only a change to X could fix.** An escape hatch existed in both cases — the human escalation path (`console` → `escalate` → `gate resolve`) does not depend on the auto-judge — but nothing named the cycle or pointed at the hatch. Diagnosis required reading the source of an unmerged PR.

**Theme 2 — readiness signals that report confidently and wrongly.** Five entries, one shape: a signal presents a definite state with no representation of the evidence's age, scope, or applicability.

| Signal | Said | Was |
|---|---|---|
| Review threads (`rooms#99`, `ship#235`, `workbench#198`) | unresolved — findings ignored | fixed in 7–13 min, in a *different file*, so GitHub never marked the thread outdated |
| `ship#242` checks | green | green against a main from 3.9 days earlier; `AddressOpts` had gained a required field |
| `rooms` `make check` | the documented pre-push gate | permanently red on macOS — two host-shell tests, green only on CI's ubuntu |
| `ship` test suite | red locally | red only because `ANTHROPIC_BASE_URL` was exported in the operator's shell |
| `reviewDecision` | cannot verify readiness → park | *structurally* empty — portfolio repos configure no approvers, so it is empty on every PR forever |

That last row is where the two themes meet. Because readiness can never self-verify, **every portfolio merge parks — unconditionally, not incidentally.** That makes the judge a hard dependency of every merge, which is precisely what turned two broken providers into a total outage.

**The hypothesis.** These are not eleven bugs. They are one missing property: *the workbench records decisions but not the evidence behind them.* `contracts` already carries the verdict schema and the artifact envelope — the decision vocabulary — while a verdict's **inputs** are consumed as bare booleans with no provenance. Make evidence provenance a contract, make every consumer refuse or degrade explicitly instead of answering confidently, and make every terminal state print a route that does not pass through the thing that failed. The bet is that this removes the *class*, not the instances.

**Non-goals.**
- **No change to gate's exit-code seam.** 0 pass / 1 blocked / 2 parked / 3 refused / 4 error stay exactly as they are — a load-bearing contract per the repo charter.
- **No new plane, no new binary, no new MCP verb.** This tightens an existing seam.
- **No widening of #186.** The approval-UX slice keeps its own spec, its own review, its own validation gate. See §4.7.
- **No "workbench doctor."** An aggregate health command is another confident summary over signals that are themselves unjustified. Fix the signals (§4.8).
- **Not the root cause of the flaky tests** — only that a documented check is honest about the platform it requires.
- **Out of this doc entirely:** the harness lessons (never batch state-changing GraphQL mutations in one shell loop; `zsh` does not word-split, so prefer `${p%%:*}` over `set --`). Real, recorded, but they are agent practice, not tool surface.

**Scope honesty.** "Workbench" here is the *composed portfolio surface* — the planes plus the skills that drive them — not only the Go module. The doc lives in `itsHabib/workbench` because that repo owns `contracts` and the planes; §9 marks which repo each phase lands in. Four of the sixteen entries are one-line fixes in `rooms` / `ship` / `cc-skills`; they are in scope as *evidence for the principle* and as P2 tasks, not as workbench code.

**Status of the evidence as of writing.** The specific deadlock broke from outside while this doc was being drafted: `cc-skills#18` merged 2026-08-02T23:57Z, `workbench#200` merged, `#198` was closed as superseded by `#202`, and `#201` merged 2026-08-03T01:12Z. **The instance is resolved; the structural property is not.** Nothing in the system today would name the next cycle any faster.

## 2. Functional & non-functional requirements

**Functional**

1. Every non-zero gate terminal state names its cause, says whether retrying can help, and prints **one runnable next command**.
2. A terminal state whose cause is a change to the failing component itself is marked as such, and the route printed does not pass through that component.
3. Every verdict gate consumes carries the head it was observed against and when. A verdict that cannot be shown to cover the head under evaluation is **refused, never counted**.
4. A repo that *cannot* produce a human review decision is distinguishable from a PR that merely lacks one — and the substitution policy is minted by the operator into the grant, never chosen by gate (§4.4).
5. A producer that falls back records the degradation as a chain-sealed artifact; happy-path stdout stays parse-clean.
6. Recording friction costs **one command, one line**, at the moment of pain — classification deferred to a rollup.
7. A documented local check passes on the machine it is documented for, or declares the platform it requires.

**Non-functional**

| Property | Target |
|---|---|
| Terminal legibility | **0** gate terminal states with an empty or generic reason, **including those that exit before a result exists**. The 2026-08-02 `judge_provider_failed:` (empty after the colon) is the regression test. |
| Provenance coverage | 100% of verdicts gate consumes carry `subject.head_sha` + `observed.at`. Absent ⇒ `unknown` ⇒ refused at the points §4.2 declares — never silently defaulted. |
| Staleness | Coverage requires a head match or an **exact observed base SHA**. An unrecoverable base parks; timestamps never authorize. **0** paths where unverifiable coverage yields a pass. |
| Degradation visibility | 0 diagnostic lines interleaved before JSON on the happy path. Every fallback appears as a `degradation` artifact in the run. |
| Capture cost | ≤1 command, ≤1 line, no file choice, no format, <3 s. If it is slower than *not* logging, it loses to flow — which is exactly how the corpus got to 11 entries in one repo across a 17-repo portfolio. |
| Blast radius | `contracts` stays a leaf (imports nothing in-module, no decision logic). No tool imports another tool's decision code. CI's `hygiene` job unchanged and green. |
| Chain integrity | Every new field is inside the hashed `Body`. Nothing is added to the envelope's top level (§8). |
| Backward compat | Artifacts predating this doc remain readable. `json.Unmarshal` tolerance is the existing contract; absent optional fields stay zero. |
| Added cost | ≤1 extra GitHub read on gate's happy path (the merge-base commit time). No new network dependency. |

## 3. Architecture overview

```
   producers (observe)                                consumers (decide)
 ┌──────────────────────────────┐              ┌──────────────────────────────┐
 │ github:checks                │              │ gate   (Verification+Capability)│
 │ github:reviewthreads         │              │ console · flare · /pr-sweep    │
 │ github:reviewdecision        │              │ triage · tracelens             │
 │ local:make-check             │              └───────────────┬──────────────┘
 └──────────────┬───────────────┘                              │ read
                │ emit                                          │
                ▼                                               ▼
   ┌────────────────────────────────────────────────────────────────────┐
   │  contracts/   — LEAF: types + schema only, no decision logic        │
   │    Verdict.Subject{Repo, Number, HeadSHA}      ← exists (optional)  │
   │    Verdict.Observed *Observation               ← NEW  §5            │
   │    KindDegradation body                        ← NEW  §5            │
   └────────────────────────────────────────────────────────────────────┘
                │  hash-chained JSONL artifacts — the seam, unchanged
                ▼
   ┌────────────────────────────────────────────────────────────────────┐
   │  cmd/gate/internal/readiness/  — the POLICY (new pkg)  §6.3         │
   │    Covers / CoversAll(obs, head, mergeBase) → (bool, reason)        │
   │    Fatal(degradation) → bool          ← policy, NOT a contract field │
   │    Escape(code, substrateOK) + self-gating failure-code registry     │
   └────────────────────────────────────────────────────────────────────┘
```

**What's reused:** the whole composition model. Tools still share types and never call stacks; they still compose through exit codes and JSONL on disk. `Verdict.Subject.HeadSHA` already exists — it is simply `omitempty` today, which is the hole.

**What's new:** one leaf type (`Observation`), one artifact-body contract (`degradation`), and one policy package inside gate.

**The seam this deliberately respects:** the *type* goes in `contracts`; the *predicate that decides what a stale observation licenses* goes in `cmd/gate/internal/readiness`. Provenance is a fact; applicability is a decision. Putting `Covers()` in `contracts` would put decision logic in the leaf and break the one rule — a reviewer should check exactly this boundary.

v1 stated that rule and then broke it one section later with `Degradation.Fatal`, which round 1 caught. v2 keeps the boundary consistently: `contracts` records *what failed and what role it served*; `readiness.Fatal` decides what that licenses. The same test applies to `Observation.Invalidates` (§5) — it records the observation's scope, never the conclusion.

## 4. Key decisions & trade-offs

### 4.1 `Observation` is one shared type in `contracts`, not per-tool provenance — **D1**

Each producer could stamp provenance in its own shape. That is zero coordination and reproduces exactly the parser-per-tool debt `contracts` exists to pay off. One shared type costs a schema bump (`verdict-v0.3.0.json` → `v0.4.0`, which the existing conformance test *enforces*) and forces every producer to fill it.

**Choose the shared envelope.** The cost is a coordinated bump; the alternative is the debt the charter already rejected.

### 4.2 Absent provenance is `unknown`, and `unknown` is refused only at declared points — **D2**

`Observed` is a pointer: `nil` means unknown, which is honest for every artifact written before this lands.

- *Fail-closed everywhere immediately* — every legacy artifact becomes unusable at once. A flag day in the merge-authorization path, which is the worst place in the portfolio to have one.
- *Fail-open* — defeats the entire doc.

**Choose:** `unknown` is representable, and **P4 declares exactly which consumers refuse it** (gate's readiness read: yes; `console`'s display: no, it renders "unknown"). **Honest cost:** there is a window where some consumers still read `unknown` permissively. That window is a real gap, not a technicality, and it closes only when P6 finishes adoption. A reviewer who thinks the window is unacceptable should say so — the alternative is the flag day.

### 4.3 Self-gating detection is a **declared failure-code registry**, not causal analysis — **D3**

Ideal: infer "this park is caused by an unmerged PR touching the failing code path." That needs diff→code-path mapping and is not cheaply buildable. A hand-maintained registry of failure codes that are repairable *only* by changing the failing component (`judge_provider_failed`, `judgment_malformed`, `judge_provider_unconfigured`) will drift and will miss novel self-gates.

The registry is acceptable **only because of the tiering**:

- **Tier 1 (unconditional).** *Every* terminal state prints the route that does not pass through the component that failed. Near-zero cost, needs no registry, and covers the novel cases — the 2026-08-02 deadlock would have cost minutes instead of a session on Tier 1 alone.
- **Tier 2 (declared).** A registered code additionally gets `self_gated: true` and "retrying cannot help."

So registry drift **degrades the message, never the decision, and never hides the escape route.**

**v1 asserted that unconditionality; round 1 showed it was false in two places, and both are now part of the design rather than assumptions.**

1. **Terminal states with no result.** `newEnv` and `runGate` return errors before a `gateResult` exists, so `printJSON(res)` never runs (`cmd/gate/main.go:428-434`). A route attached to the result object is silent for every environment-construction, flag-validation, and early-abort failure. **Fix:** the route is emitted by the *error* path in `main`, not by the result printer. A terminal exit with no result still prints a route.
2. **Failures of the substrate the route depends on.** The generic route is `console → escalate → gate resolve` — all of which read the gate state dir. When the terminal state *is* a state-open, anchor, or audit-chain failure, that route is advice to use the broken thing. **Fix:** substrate failures get **external repair routes** (verify/restore custody, re-point `-state`, re-anchor) that assume nothing about the store's health. `Escape` is partitioned by whether the state substrate is usable.

Plus the totality detail round 1 caught: the 2026-08-02 failure was literally `judge_provider_failed:` with an **empty suffix**. `Escape` must return a usable route for `""` and for codes it has never seen — an explicit fallback key, pinned by a test that includes `""` and a novel string, not just the known table.

With those three, drift costs precision and never the exit. Without them D3 collapses — which is why §7.6 now carries the flow and §11 carries the replay scenario.

### 4.4 Readiness substitution is minted into the **grant**, not decided by gate — **D4** *(the load-bearing decision)*

Portfolio repos configure no approvers, so `reviewDecision` is empty forever and gate parks on every merge. The obvious fix — let gate credit a fully-resolved bot panel plus green required checks as readiness when a repo declares no approvers — **collides with gate's charter: findings are not authorization.** Two defensible readings:

- **(a)** Readiness and authorization are different questions. Authorization comes from the operator-minted grant; readiness only asks "was this reviewed at all." Crediting the panel for *readiness* leaves the authority untouched.
- **(b)** Crediting the panel makes it load-bearing for merges. That is the substitution gate exists to prevent, whatever it is called.

**Resolution — make it a capability, not an inference.** `gate grant … -readiness {human|panel}`, default `human`. The operator authorizes the substitution **once, at mint time**, bounded by the grant's existing TTL and tier ceiling and visible in the ledger. Gate never self-selects a policy; a repo with no `-readiness panel` grant still parks. This keeps mint authority with the operator — the property the whole charter is built on — and converts a security question into a capability question, which is the plane the workbench already has for it.

Round 1 confirmed reading **(a)**: the violation is gate *inferring* its own authorization scope, which the rejected alternative does and this does not.

**But round 1 also found the hole, and it is fatal to v1 as written.** The substitution is *already* available outside any grant. `-reviews-optional` is a plain caller-controlled flag (`cmd/gate/main.go:417`, default `false`), threaded straight into `verify.Readiness(…, reviewsOptional)`; and `.github/workflows/gate.yml:365-384` self-mints an ephemeral grant and passes it. Adding a grant field while that flag stands means a caller holding a default-`human` grant can still select panel readiness — the boundary D4 claims to draw would not exist.

**v2: the flag is subordinated to the grant.** `-reviews-optional` becomes a *request*, honored only when the grant it runs under carries `readiness=panel`. Under a `human` grant it is refused, loudly — not ignored.

**Why this does not break the CI rail.** The workflow mints its own grant, into its own `mktemp` state and key dirs, erased by an `EXIT` trap, with `-stamp=false` precisely because that ledger does not survive. CI is a **separate custody domain**, not a caller borrowing the operator's authority. It keeps working by minting `-readiness panel` in the same self-mint it already performs — one flag on a line it already owns. The boundary this restores is in the *operator's* domain, where the flag currently confers authority nobody minted.

**Honest cost:** this is a behavior change for any existing caller that passes `-reviews-optional` under an operator-minted grant. Those calls start refusing until the grant is re-minted. That is the point, but it should be a deliberate choice rather than a surprise — flagged in §10.1.

**Rejected:** gate detecting repo capability (`no approvers configured`) and self-selecting a policy. It is more convenient and it is gate deciding its own authorization scope. **Also rejected:** leaving `-reviews-optional` as an independent switch alongside a grant field — two authorization paths for one decision is worse than either alone.

### 4.5 Staleness is measured against the **merge-base commit time**, not a TTL — **D5**

A TTL is simpler and wrong in both directions: a 3-day-old green against an unchanged main is fine; a 10-minute-old green against a main that moved 5 minutes ago is not. The correct predicate is *did the base move under this check*.

**v1 proposed a timestamp proxy with a named-but-unsolved force-push hole. v2 deletes that path.** Round 1 was right that it contradicts this doc's own fail-closed requirement: under a force-push with rewritten committer dates, a check against the *previous* base reads as fresh and can authorize a merge. A named failure mode that can produce a false pass in the merge-authorization path is not an accepted trade-off, it is a bug with documentation.

**The predicate, in order:**

1. `check.head_sha == pr.head_sha` → **no staleness question arises.** The check ran on the head being evaluated. This is the common case and it costs nothing.
2. Otherwise, an **exact observed merge-base SHA** is required. Coverage holds only if it equals the PR's current merge-base.
3. Base SHA unrecoverable → **park.** Never pass.

Round 1 also caught a real imprecision in v1's phrasing: GitHub's Checks API records the **head** SHA a check ran against, not the merge-base, so "compare merge-base SHAs" conflated comparison with an ancestry query. Step 1 is the cheap correct special case; step 2 needs the base recorded *by the producer at observation time* (`Observation.BaseSHA`, §5) rather than reconstructed later.

**Timestamps survive as diagnostics only** — useful in a park's reason string ("checks predate the merge base by 3.9d"), never as an authorizing signal.

### 4.6 Capture-first sequencing — **D6**

The instrument (P0) ships before the fixes. This looks like process for its own sake and delays visible wins by one small phase. The alternative makes §11's product hypothesis unfalsifiable forever — which is the exact state entry #12 describes: the corpus was empty for the session that generated every item on this page, because logging is a manual out-of-band act performed mid-flow, and it loses to flow every time.

**Choose capture-first,** hold it to one phase, keep the verb to one line.

### 4.7 #186 is a committed slice beneath this doc, and is **not** widened — **D7**

[`gate-approval-ux`](../gate-approval-ux/spec.md) makes the custodied executor's two-phase approvals *buzz → read card → type four words → done*. It keeps its own spec, its own reviewer focus areas, its own phases, and its own validation gate.

The relationship this doc adds and nothing more: **because every merge parks (§1), the human decision path is on the critical path of every merge, not an exception.** #186 makes that path cheap; D4 makes it *rarer*. They are complements attacking the same structural fact from opposite ends, and they are independently shippable — neither blocks the other.

### 4.8 Considered and rejected (recorded so absence reads as decided)

| Rejected | Why |
|---|---|
| A `workbench doctor` aggregate health command | Another confident summary layered over signals that are themselves unjustified. Fix the signals. |
| Gate auto-retrying the judge with a fallback provider | Turns a legible failure into a slower, less legible one. On 2026-08-02 *both* providers were broken. |
| Caching readiness verdicts across runs | Adds a staleness surface to solve a staleness problem. |
| Putting `Covers()` in `contracts` | Decision logic in the leaf. Breaks the one rule; the `hygiene` job would catch it. |
| A top-level `degraded[]` field on the envelope | Sits outside `hashArtifact` — unsealed, tamper-able. See §8. |
| **`Degradation.Fatal` in `contracts`** *(v2 — was in v1)* | Same boundary argument that keeps `Covers()` out, applied inconsistently. `Fatal` lets a producer dictate every consumer's refusal behavior from the leaf. §5 records objective `Role`; gate decides fatality. |
| **Timestamp-only staleness fallback** *(v2 — was in v1)* | Produces a false pass under force-push with rewritten dates, contradicting this doc's own fail-closed rule. §4.5 now parks instead. |
| **`-reviews-optional` as an independent switch beside a grant field** | Two authorization paths for one decision is worse than either alone. §4.4 subordinates the flag. |

## 5. Data model

New in `contracts` (leaf; no logic):

```go
// Observation records where a verdict's inputs came from and when they were
// true. Provenance, not decision: what an absent or stale Observation licenses
// is the consumer's policy and never lives here.
type Observation struct {
    Surface     string    `json:"surface"`               // "github:checks" | "github:reviewthreads" |
                                                         // "github:reviewdecision" | "local:make-check"
    At          time.Time `json:"at"`                    // when the state was TRUE — not when the line was written
    BaseSHA     string    `json:"base_sha,omitempty"`    // merge-base the observation assumed, when recoverable
    Host        string    `json:"host,omitempty"`        // local surfaces only
    OS          string    `json:"os,omitempty"`          // local surfaces only — GOOS
    Invalidates []string  `json:"invalidates,omitempty"` // "head_moved" | "base_moved" | "host_specific"
}
```

`Verdict` gains exactly one field:

```go
type Verdict struct {
    Subject    Subject      `json:"subject"`             // Repo, Number, HeadSHA — HeadSHA stops being optional
    // … unchanged …
    Observed   *Observation `json:"observed,omitempty"`  // NEW — nil ⇒ unknown (D2)
}
```

`Envelope.Time` is when the *line was written*; `Observation.At` is when the *state was true*. They differ whenever a producer reports a GitHub-reported state, which is every readiness signal in §1.

**`Invalidates` records the observation's own scope, never what to do about it.** A producer saying `host_specific` is stating a fact about where it looked. Whether that makes the observation inapplicable to *this* decision is `Covers()`'s call, in gate. Round 1 flagged this as drift-prone; it is pinned here so a future author does not grow policy semantics into the field.

**Provenance is per-signal, not per-verdict (v2).** Round 1 caught that a readiness verdict aggregates many status checks *plus* the review decision, each with its own observation time — and v1 gave the aggregate a single `Observation.At`. Every choice for that one value is wrong: collection time makes an old check look fresh; newest-completion masks older stale members; oldest needlessly invalidates unrelated fresh signals.

So each observed signal carries its own `Observation`, and the aggregate carries a **conservative** coverage rule rather than a timestamp:

> An aggregate verdict covers a head **iff every member signal covers it.** One stale member fails the aggregate.

This can over-refuse — an unrelated stale check parks a merge whose relevant signals are all fresh. That is the correct direction to be wrong in for a merge gate, and the park's reason names the offending member so the fix is obvious. §6.3 gives the function; §10.7 keeps the false-refusal cost open.

**`KindEvidence` gets a body contract.** Today evidence bodies are free-form `map[string]any` — which is why nothing carries provenance. Evidence bodies gain the same `observed` field, same shape.

**`KindDegradation` (new kind).** One artifact per fallback, parented into the run:

```go
type Degradation struct {
    Component string `json:"component"` // "ollama" | "codex-cli" | …
    Role      string `json:"role"`      // what it was DOING: "verifier" | "narrator" | "notifier" | …
    Reason    string `json:"reason"`    // never empty — the empty-reason regression is the point
}
```

**v1 had a `Fatal bool` here. v2 removes it** — round 1 was right, and it caught me applying my own boundary argument inconsistently. `Fatal` is not provenance: it tells *every* consumer whether it must refuse, which is a producer choosing cross-tool authorization behavior from inside the leaf. That is precisely the reason `Covers()` is kept out of `contracts` (§3), so it cannot be the reason `Fatal` stays in.

The contract records the objective facts — **what failed and what role it was serving.** Gate's readiness policy decides what that licenses, in `cmd/gate/internal/readiness` where every other applicability decision lives. The proposed rule (*a degraded narrator is advisory; a degraded verifier is fatal*) survives intact — it just becomes gate policy instead of a contract field, which also settles the "rule vs per-case" half of the old §10.5.

**Schema:** `contracts/schema/verdict-v0.3.0.json` → `v0.4.0`. The conformance test pins Go fields against the embedded schema, so this bump is enforced, not remembered.

## 6. API contract

### 6.1 `contracts` — §5. Additive; every field optional-on-read.

### 6.2 gate CLI

**Exit codes unchanged** — 0/1/2/3/4. Nothing in this doc moves that seam.

New JSON on the run artifact (all additive, inside `Body`):

```jsonc
{
  "outcome": "parked_for_judgment",         // existing value — unchanged
  "reason":  "judge_provider_failed: claude CLI exit=1, stdout: \"Not logged in\"",
  "self_gated": true,                       // D3 Tier 2 — a registered failure code
  "retry_helps": false,
  "escape": {                               // D3 Tier 1 — ALWAYS present on non-zero
    "why":  "the automated judge is the component that failed; this route does not use it",
    "next": "gate next -state ~/dev/gate/state    # then: console → escalate → gate resolve"
  }
}
```

`escape.next` is a **runnable command**, and its `-state` is read from the same resolution the rest of the loop uses — the 2026-08-02 guard printed `~/pers/gate/state`, which does not exist on this machine (`GATE_STATE` is pinned to `~/dev/gate/state`).

`escape` is emitted from the **error path in `main`**, not from the result printer — a terminal exit that never produced a `gateResult` still prints one (§4.3, §7.6).

New grant field (D4):

```sh
gate grant -repo <owner/repo> -max-tier T2 -ttl 24h -readiness {human|panel} -state ~/dev/gate/state
```

Default `human`. Recorded in the grant body — so it needs a `Readiness` field on `contracts/gateauthorization` and a **schema bump with conformance enforcement**, exactly as load-bearing as P3's verdict bump. Surfaced in the ledger and in `console`.

`-reviews-optional` keeps its spelling and loses its authority: it now *requests* a policy the grant must already carry. Under a `readiness=human` grant it refuses with a reason naming the grant, rather than silently proceeding or silently ignoring the flag.

### 6.3 `cmd/gate/internal/readiness` (new package — policy)

```go
// Covers reports whether obs still describes the head under evaluation, and why
// not when it does not. reason is never empty on false.
//
// head short-circuits: an observation whose subject head EQUALS head raises no
// staleness question. Otherwise coverage requires an exact observed base SHA
// matching mergeBase; an absent or mismatched base is NOT covered (§4.5).
// Timestamps never authorize — they only enrich reason.
func Covers(obs *contracts.Observation, head, mergeBase string) (ok bool, reason string)

// CoversAll applies Covers conservatively across an aggregate's member signals:
// covered iff EVERY member is covered. reason names the first offending member,
// so a park says which signal is stale rather than that something is (§5).
func CoversAll(obs []*contracts.Observation, head, mergeBase string) (ok bool, reason string)

// Escape returns the route that does not pass through the component that
// produced code (D3 Tier 1). TOTAL — including "" and codes never seen.
//
// substrateOK partitions the answer: when the gate state store is unusable, the
// generic console -> escalate -> gate resolve route is advice to use the broken
// thing, so Escape returns an EXTERNAL repair route instead.
func Escape(code string, substrateOK bool) Route

// SelfGated reports whether code is repairable only by changing the component
// that emitted it (D3 Tier 2). Drift here degrades the message, never Escape.
func SelfGated(code string) bool

// Fatal reports whether a degradation blocks the decision. POLICY — this is why
// contracts records Role and not a Fatal bool (§5). Verifier: fatal.
// Narrator/notifier: advisory.
func Fatal(d contracts.Degradation) bool
```

### 6.4 Friction capture (cc-skills)

```sh
friction "gate judge -auto failed with an empty reason"    # appends, exits. no class, no file choice.
friction rollup --since 7d                                  # classifies later, in bulk
```

## 7. Key flows

### 7.1 Park with a broken judge — the Theme-1 path *(entry #1)*
```
1. gate gate → verifier ladder → readiness unverifiable → PARK (exit 2)   [unchanged]
2. judge invoked → provider exits non-zero
3. providerFailure records status + EVERY stream that said anything        [#198/#202 — landed]
4. readiness.SelfGated("judge_provider_failed") → true
     ⇒ self_gated: true, retry_helps: false
5. readiness.Escape(code) → the human path (console → escalate → gate resolve)
     ⇒ printed on stdout AND in the artifact                               [Tier 1: prints even at step 5
                                                                             if step 4 returns false]
6. operator follows the printed command. No source reading.
```

### 7.2 A no-approver repo under a `-readiness panel` grant — the D4 path *(entry #2)*
```
1. gate reads reviewDecision → empty
2. grant carries readiness=panel?
     no  → PARK, reason "readiness: no review decision; grant does not authorize substitution"
           escape: "mint with -readiness panel, or resolve by hand: <cmd>"
     yes → evaluate the declared policy: panel fully resolved AND required checks green
3. every input to that policy must Cover() the head (7.3). A stale input REFUSES.
4. pass ⇒ exit 0. The grant, not the panel, remains the authority.
```

### 7.3 Stale green — refuse, do not pass *(entry #7)* — **rewritten in v2**
```
1. check verdict: subject.head_sha = S_check, observed.base_sha = B_check
2. S_check == pr.head?  ⇒ COVERED. No staleness question — the check ran on the
                           head under evaluation. Common case, costs nothing.
3. else B_check present AND == pr.mergeBase?  ⇒ COVERED.
4. else                                       ⇒ NOT COVERED ⇒ PARK.
     - B_check absent  → "check ran on <S_check>; no observed base to verify against"
     - B_check differs → "check ran against base <B_check>, now <mergeBase>"
     - timestamps appear in the reason ONLY as colour ("predates the base by 3.9d")
5. gate NEVER counts an uncovered check. `ship#242` was promoted on exactly this
   evidence and had to be reverted.
6. Aggregate readiness: CoversAll — one uncovered member parks the whole,
   naming that member (§5).
```
**v1 had a timestamp fallback here.** It let a force-push with rewritten committer dates present a check against the *old* base as fresh — a false pass in the merge-authorization path, contradicting §8's own fail-closed rule. Deleted, not documented.

### 7.6 Terminal state with no result, or a broken substrate — **new in v2**
```
1. gate fails BEFORE a gateResult exists (newEnv, flag validation, early abort)
     ⇒ the error path in main — not printJSON — emits the route.
       A terminal exit is never silent.        [D3 Tier 1, extended]
2. gate fails because the STATE SUBSTRATE is broken (state open, anchor, audit chain)
     ⇒ substrateOK=false ⇒ Escape returns an EXTERNAL repair route:
         verify/restore custody · re-point -state · re-anchor
       NOT "console → escalate → gate resolve", all of which read the store
       that just failed.
3. unknown or empty terminal code (the 2026-08-02 `judge_provider_failed:` shape)
     ⇒ explicit fallback key ⇒ still a usable route.
4. in all three: the artifact may be unwritable. The route is a STDOUT guarantee
   first and an artifact field only when the store works.
```

### 7.4 Degraded producer — clean stdout *(entry #6)*
```
1. ollama refuses the connection during escalation-brief synthesis
2. producer appends KindDegradation{component:"ollama", reason:"connect: connection refused",
   fatal:false} parented into the run
3. falls back to the raw question — behavior unchanged
4. stdout emits JSON and nothing else. The 2026-08-02 form — a diagnostic line
   printed BEFORE the JSON on every single call, making a clean result read as an
   error — is a regression test.
```

### 7.5 Missing provenance — the `unknown` path *(D2)*
```
verdict.Observed == nil
  → gate readiness read:  REFUSE, reason "verdict carries no observation; cannot
                          establish it covers <head>"        (declared refusal point)
  → console display:      render "provenance: unknown"        (no refusal — it explains, never decides)
  → flare:                notify unchanged                    (a sink never gates)
```

## 8. Concurrency / consistency / failure model

**Chain integrity — the constraint that shapes §5.** `hashArtifact` seals `ID|Kind|Run|Time|Prev|Parents|Body`. A field added to the envelope's *top level* would be **outside the hash** — recorded but not tamper-evident, in the one log whose whole purpose is tamper evidence. Therefore: `Observed` goes inside `Verdict` (which is `Body`), and degradation is its own **artifact kind** rather than an envelope field. The envelope struct does not change at all. This is a correctness constraint, not a style preference.

**Fail-closed points.** Observed base SHA absent or mismatched ⇒ `Covers()` false ⇒ park. Any aggregate member uncovered ⇒ `CoversAll` false ⇒ park. Provenance absent at a declared refusal point ⇒ refuse. `readiness.Fatal(d)` on a verifier-role degradation ⇒ refuse rather than proceed. `-reviews-optional` under a `human` grant ⇒ refuse. **There is no path where a missing or unverifiable input produces a pass** — v2 closed the one that existed.

**The force-push case (D5) — closed, not accepted.** v1 compared `completed_at` against the base's `committedDate` and named force-push-with-rewritten-dates as unsolved residual risk. Round 1 was right that this is a false-pass path in the merge-authorization component, which no amount of documentation makes acceptable. v2 requires an exact observed base SHA (or a head match) and parks otherwise; timestamps are diagnostic only. The cost is more parks when producers have not yet stamped `BaseSHA` — the correct direction, and it converges as P3 lands.

**Over-refusal is the accepted cost.** Conservative aggregation (§5) parks a merge when any member signal is uncovered, including ones irrelevant to the change. For a merge gate, refusing a mergeable PR is recoverable in seconds; authorizing an unmergeable one is not. The park names the offending member so the fix is mechanical. Tracked in §10.7.

**Registry drift (D3).** `SelfGated` returning false for a genuinely self-gating code downgrades the message from "retrying cannot help" to a plain terminal state — but `Escape` is total, so the route still prints. Drift costs precision, never the exit.

**Concurrency.** Nothing here introduces new writers. The gate store stays single-writer append-only; `readiness` is pure. The one added read (merge-base commit time) is idempotent and cacheable within a run.

**Backward compatibility.** Envelope decoding is already tolerant of unknown fields — the package documents this explicitly — so a new producer's artifacts are readable by an old consumer and vice versa. Old verdicts decode with `Observed == nil` and take the §7.5 path.

## 9. Rollout / implementation plan

Multi-repo. Committed phases are P0–P4; everything after the gate is a stub until validation earns it. `#186` runs in parallel as its own committed track with its own gate — **not re-planned here**.

| Phase | Goal | High-level tasks | Depends on | Repo | Gate | ~wLOC | Model/effort |
|---|---|---|---|---|---|---|---|
| **P0 — instrument** | The corpus exists before the fixes do | one-line `friction` capture verb (append: repo, ts, session, text; **no class**); `friction rollup --since` classifies in bulk; `chmod +x friction-scan.sh` | — | cc-skills | pre-gate | ≤150 | sonnet/extra |
| **P1 — the merge path explains itself** | Theme 1's cheap 80% | `readiness.Escape` (total over `""` + unseen codes, partitioned on `substrateOK`) + `SelfGated` registry; emit from the **error path in `main`**, not the result printer, so pre-result exits still print; external repair routes for substrate failures; wire `escape`/`self_gated`/`retry_helps` into every non-zero terminal state; pretool-guard inspects only the invoked command, not `--body`/`-m` string args; guard remedy reads the real state path | — | workbench, hooks | pre-gate | ≤300 | **opus/extra** — touches gate's terminal-state surface; D3 rests on this being total |
| **P2 — local signals stop lying** | "Green" means green *here* | `#[cfg(target_os="linux")]` the two `rooms` rootfs host-shell tests; land `ship#246` (state the env, don't inherit it); `/worktree-add` runs the repo's install step when it sees a lockfile; note the zsh word-splitting difference where shell snippets are kept | — | rooms, ship, cc-skills | pre-gate | ≤120 | sonnet/extra |
| **P3 — provenance contract** | One vocabulary for evidence | `contracts`: `Observation`, `Degradation`, `KindDegradation`; `Verdict.Observed`; evidence-body contract; schema `v0.3.0`→`v0.4.0` + conformance; producers stamp it | — | workbench | pre-gate — **no-regret leaf** | ≤300 | sonnet/extra — type-enforced, schema-pinned |
| **P4 — readiness truth in gate** | Theme 2's meat | `readiness.Covers` + `CoversAll` (SHA-based, D5); `readiness.Fatal` policy; unverifiable-by-construction vs not-verified; `-readiness {human\|panel}` grant field + `gateauthorization` schema bump + conformance; **subordinate `-reviews-optional` to the grant** (§4.4); ledger/console surfacing; fold ollama degradation into `KindDegradation`; declared refusal points (D2) | P3 | workbench | pre-gate | 400–600 | **opus/max** — changes what gate authorizes |
| **VG** | **VALIDATION GATE** | §11 | P0–P4 | — | **binary** | — | — |
| **#186** | *Committed parallel track* — phone-friendly executor approvals | See [`gate-approval-ux/spec.md`](../gate-approval-ux/spec.md) §9. Own phases, own VG. **Not widened by this doc.** | — | workbench | its own | — | per that doc |
| P5 — review-thread truth *(gated)* | A resolved thread is distinguishable from an ignored one | key threads on `id` + `originalLine`, **never** live `line`; post-push step that surfaces unresolved threads whose finding text overlaps the new commit and offers to answer them | VG | cc-skills, workbench | post-gate | — | opus/extra |
| P6 — provenance adoption *(gated)* | Close D2's `unknown` window | flare / console / triage / tracelens consume `Observation`; remaining permissive readers become declared refusal points | VG | workbench | post-gate | — | sonnet/extra |

**P0, P1 and P2 have no ordering dependency between them** — the table reads top-to-bottom for presentation only. All three are unblocked and can ship in any order or at once. §4.6's capture-first argument is about not letting P0 sprawl, not about P0 blocking anything.

**P3 must stamp every producer gate itself calls** (round 1). P3 is not just "schema bump + conformance": if gate's own evidence collectors are not stamping `Observation` before P4's refusal points land, P4 refuses gate's own artifacts on its first run. The `wux-producers-stamp-observation` task covers this and is the real gate on P4, more than the type is.

**Why P5 is after the gate, not the gate.** Its core move — deciding whether a commit addresses a thread's concern — is a fuzzy match with no cheap falsifier (§10.4). Everything P0–P4 does is mechanically checkable. Putting the one unfalsifiable heuristic before the gate would let it decide whether the gate passes.

**Why P2 sits with the rest.** Three one-line fixes in three other repos look like unrelated hygiene. They are Theme 2 at the local layer: a documented gate that is permanently red trains everyone to read past a failing `make check` — exactly the habit that hides a real regression.

## 10. Open questions

1. **~~D4's reading~~ — RESOLVED (v2, round 1).** Reading **(a)**: the violation is gate *inferring* its own scope, which this design does not do. **The residual question is the migration, and it is the operator's call:** subordinating `-reviews-optional` (§4.4) makes every existing call that passes it under an operator-minted grant start refusing until the grant is re-minted with `-readiness panel`. Deliberate break, or does the flag need a deprecation window that keeps the hole open meanwhile?
2. **Where `Observation` lives.** `contracts` root, or a `contracts/observation` sub-package? The lazy-migration policy says defer until a second consumer exists; there are already ≥3 (gate, triage, tracelens). Leaning root.
3. **One friction log or many?** Today: per-repo logs plus a cross-cutting one, split by human judgment at write time. A single log with a `repo` field rolls up better and reads worse — and *reading* is what makes the corpus useful. Unresolved; P0 should not hard-code the answer.
4. **P5's thread-matching falsifier.** How do we test "this commit addresses this thread" without an LLM call on every push? No answer yet. This is why P5 is gated.
5. **~~Is `degradation.fatal` a per-case flag or a rule?~~ — RESOLVED (v2, round 1).** The field is gone from `contracts` entirely (§5); the rule (*narrator advisory, verifier fatal*) lives in `readiness.Fatal` as gate policy. What remains is bookkeeping: the `Role` vocabulary must stay small and closed, or it drifts back into policy by another name.
6. **Does P1's guard fix belong in this doc at all?** It lives in `~/.claude` hooks, not workbench, and it is the only phase item with no artifact in the chain. Kept because it is the same failure — a blocker explaining itself wrongly — but a reviewer may reasonably route it out.
7. **What does conservative aggregation cost in practice? (new, v2.)** "Any uncovered member parks the aggregate" (§5) is the safe direction, but nobody has measured how often an irrelevant stale check would park a genuinely mergeable PR. If that rate is high, the answer is probably per-signal *relevance* scoping rather than loosening coverage — but that is a design, not a knob, and it needs data P0's corpus will produce.
8. **Do substrate-failure escape routes need to be machine-checkable? (new, v2.)** §7.6 promises an external repair route when the state store is unusable. Those routes cannot be integration-tested against a healthy store, and a route that is wrong is worse than none — it is confident, wrong instructions at the exact moment the operator has least ability to verify them.

## 11. Validation plan

The gate is **binary and baseline-free**, in two parts. Both must pass.

**1. Recorded-scenario replay (mechanical, in CI).** The five failures of 2026-08-02 become fixtures, each asserting the *new* terminal behavior:

| # | Scenario | Required outcome |
|---|---|---|
| 1 | judge provider unavailable | `self_gated: true`, `retry_helps: false`, escape route printed, reason non-empty |
| 2 | no `reviewDecision`, no `-readiness panel` grant | parks **naming the grant remedy** — not a generic readiness park |
| 3 | checks green but observed before the merge-base | **refused as stale**, never counted |
| 4 | ollama down | `KindDegradation` with `role: "narrator"` in the run; `readiness.Fatal` false; stdout parses as JSON with zero preceding lines |
| 5 | `gh pr create --body "…gh pr merge…"` | guard permits; remedy text (if any) names the real state dir |

Round 1 added four more, each pinning a v2 change against the failure it closes:

| # | Scenario | Required outcome |
|---|---|---|
| 6 | terminal exit **before** a `gateResult` exists (`newEnv` failure) | a route still prints on stdout — the exit is never silent |
| 7 | state-open / anchor / audit-chain failure | the route is an **external** repair path, never `console → escalate → gate resolve` |
| 8 | `Escape("")` and `Escape("novel_unseen_code")` | both return a usable route via the explicit fallback key |
| 9 | check ran against a force-pushed base with an **older rewritten** committer date | **parks** — the v1 timestamp path would have passed it |
| 10 | `-reviews-optional` passed under a `readiness=human` grant | refuses, naming the grant — neither proceeds nor silently ignores the flag |

**2. Live self-direction canary (binary).** One real portfolio PR driven from park to merged where **every command run was named by the previous command's output** — no source reading, no state-dir guessing, no judging whether a green is stale. Checkable from the session transcript after the fact. It happened or it didn't.

That second half is the actual test of the hypothesis: the claim in §1 is that the workbench records decisions but not evidence, and a self-directing chain is what having both looks like.

**Explicitly NOT the gate — the product hypothesis.** *Does friction of class `deadlock` / `genuine-gap` in the merge path actually drop?* That needs the P0 corpus and several weeks of sessions, and it has no baseline today — the corpus was empty for the session that generated all of this. Measured over ~4 weeks post-VG as a **separate** judgment, never folded into the engineering gate. Conflating them would make the gate unfalsifiable, which is the failure mode this entire doc is about.
