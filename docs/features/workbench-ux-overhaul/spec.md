# Workbench UX overhaul — Technical Design Document

**Status:** draft / proposal — **NOT a build commitment.** The artifact we decide *from*.
**Owner:** @itsHabib
**Date:** 2026-08-02
**Evidence base:** `~/dev/friction-log.md` (the portfolio-level cross-cutting log, outside any repo), session 2026-08-02 — 16 entries, one PR-sweep session across workbench / rooms / dossier / ship / orchestra / cc-skills. Every claim in §1 cites an entry; nothing here is speculative UX.
**Related:** [`docs/DESIGN.md`](../../DESIGN.md) (the boundary law this must not move), [`docs/workbench-101.md`](../../workbench-101.md) (the five planes), [`docs/features/gate-approval-ux/spec.md`](../gate-approval-ux/spec.md) (#186 — the committed slice beneath this doc), `cmd/gate/docs/enforcement.md`.

> **Reviewers — focus areas.** This is a **design review**, not a code review.
> 1. **§4.4 (D4)** — grant-scoped readiness policy. The only decision here that changes what gate *authorizes*. §4.4 states both readings of "findings ≠ authorization"; pick one.
> 2. **§4.1–§4.2 (D1/D2)** — the `Observation` envelope and the `unknown` migration window. Is that window a real hole or an acceptable cost?
> 3. **§4.3 (D3)** — declared self-gating registry vs causal detection. The argument that registry drift is a *degradation and not a hole* rests entirely on Tier-1 being unconditional. Check that.
> 4. **§7.3 + §8** — the staleness predicate. It uses timestamps as a proxy for "the base moved"; the force-push failure mode is named, not solved.
> 5. **§9** — sequencing. Is capture-first (P0) worth the delay, and is P5 correctly *after* the gate rather than being it?

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
| Terminal legibility | **0** gate terminal states with an empty or generic reason. The 2026-08-02 `judge_provider_failed:` (empty after the colon) is the regression test. |
| Provenance coverage | 100% of verdicts gate consumes carry `subject.head_sha` + `observed.at`. Absent ⇒ `unknown` ⇒ refused at the points §4.2 declares — never silently defaulted. |
| Staleness | A check verdict observed before the PR's merge-base commit time is **never** counted as passing. |
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
   │    Covers(obs, head, baseAt) → (bool, reason)                       │
   │    escape-route table + self-gating failure-code registry           │
   └────────────────────────────────────────────────────────────────────┘
```

**What's reused:** the whole composition model. Tools still share types and never call stacks; they still compose through exit codes and JSONL on disk. `Verdict.Subject.HeadSHA` already exists — it is simply `omitempty` today, which is the hole.

**What's new:** one leaf type (`Observation`), one artifact-body contract (`degradation`), and one policy package inside gate.

**The seam this deliberately respects:** the *type* goes in `contracts`; the *predicate that decides what a stale observation licenses* goes in `cmd/gate/internal/readiness`. Provenance is a fact; applicability is a decision. Putting `Covers()` in `contracts` would put decision logic in the leaf and break the one rule — a reviewer should check exactly this boundary.

## 4. Key decisions & trade-offs

### 4.1 `Observation` is an envelope field in `contracts`, not per-tool provenance — **D1**

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

So registry drift **degrades the message, never the decision, and never hides the escape route.** That argument is the whole justification for D3; if Tier 1 is not truly unconditional, D3 collapses. Reviewer focus.

### 4.4 Readiness substitution is minted into the **grant**, not decided by gate — **D4** *(the load-bearing decision)*

Portfolio repos configure no approvers, so `reviewDecision` is empty forever and gate parks on every merge. The obvious fix — let gate credit a fully-resolved bot panel plus green required checks as readiness when a repo declares no approvers — **collides with gate's charter: findings are not authorization.** Two defensible readings:

- **(a)** Readiness and authorization are different questions. Authorization comes from the operator-minted grant; readiness only asks "was this reviewed at all." Crediting the panel for *readiness* leaves the authority untouched.
- **(b)** Crediting the panel makes it load-bearing for merges. That is the substitution gate exists to prevent, whatever it is called.

**Proposed resolution — make it a capability, not an inference.** `gate grant … -readiness {human|panel}`, default `human`. The operator authorizes the substitution **once, at mint time**, bounded by the grant's existing TTL and tier ceiling and visible in the ledger. Gate never self-selects a policy; a repo with no `-readiness panel` grant still parks. This keeps mint authority with the operator — the property the whole charter is built on — and converts a security question into a capability question, which is the plane the workbench already has for it.

**Rejected:** gate detecting repo capability (`no approvers configured`) and self-selecting a policy. It is more convenient and it is gate deciding its own authorization scope.

**This is the decision that needs the operator's call. It blocks P4.**

### 4.5 Staleness is measured against the **merge-base commit time**, not a TTL — **D5**

A TTL is simpler and wrong in both directions: a 3-day-old green against an unchanged main is fine; a 10-minute-old green against a main that moved 5 minutes ago is not. The correct predicate is *did the base move under this check*.

**Honest limitation:** GitHub does not record the merge-base a check ran against, so the implementation compares the check's `completed_at` against the merge-base commit's `committedDate` — a **proxy**. It fails when the base is force-pushed with rewritten dates. Failure mode named in §8, not solved. When the merge-base SHA *is* recoverable, compare SHAs and use timestamps only as fallback.

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

**`KindEvidence` gets a body contract.** Today evidence bodies are free-form `map[string]any` — which is why nothing carries provenance. Evidence bodies gain the same `observed` field, same shape.

**`KindDegradation` (new kind).** One artifact per fallback, parented into the run:

```go
type Degradation struct {
    Component string `json:"component"` // "ollama" | "codex-cli" | …
    Reason    string `json:"reason"`    // never empty — the empty-reason regression is the point
    Fatal     bool   `json:"fatal"`     // true ⇒ consumer must refuse, not proceed
}
```

`Fatal` is the distinction §10.5 asks about: a degraded *narrator* (ollama's escalation-brief synthesis falling back to the raw question) is harmless; a degraded *verifier* is not. Proposed rule, open question.

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

New grant field (D4):

```sh
gate grant -repo <owner/repo> -max-tier T2 -ttl 24h -readiness {human|panel} -state ~/dev/gate/state
```

Default `human`. Recorded in the grant body, surfaced in the ledger and in `console`.

### 6.3 `cmd/gate/internal/readiness` (new package — policy)

```go
// Covers reports whether obs still describes the head under evaluation, and why
// not when it does not. reason is never empty on false.
func Covers(obs *contracts.Observation, head string, baseCommittedAt time.Time) (ok bool, reason string)

// Escape returns the route that does not pass through the component that
// produced code. Total: every terminal code has an entry (D3 Tier 1).
func Escape(code string) Route

// SelfGated reports whether code is repairable only by changing the component
// that emitted it (D3 Tier 2). Drift here degrades the message, never Escape.
func SelfGated(code string) bool
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

### 7.3 Stale green — refuse, do not pass *(entry #7)*
```
1. check verdict: subject.head_sha = <PR head>, observed.at = T_check
2. merge-base commit time = T_base
3. Covers(): T_check < T_base  ⇒ false, "checks predate the merge base by 3.9d"
4. gate does NOT count the check. Outcome is parked/blocked with that reason —
   NEVER pass. `ship#242` promoted on this evidence and had to be reverted.
5. If the merge-base read itself FAILS: base unknown ⇒ Covers() false ⇒ park.
   Fail-closed by construction (§8).
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

**Fail-closed points.** Merge-base read fails ⇒ base unknown ⇒ `Covers()` false ⇒ park. Provenance absent at a declared refusal point ⇒ refuse. `Degradation.Fatal` ⇒ the consumer refuses rather than proceeding. There is no path where a missing input produces a pass.

**The staleness proxy's failure mode (D5).** Comparing `completed_at` to the base's `committedDate` is a proxy for "the base moved." It breaks under **force-push to the base with rewritten committer dates**: a rewritten-earlier date makes a genuinely stale check look fresh. Clock skew between GitHub-reported timestamps is negligible in practice; force-push is not. Mitigation where available: compare merge-base **SHAs** and fall back to timestamps only when the SHA is unrecoverable. Residual risk accepted and recorded — a reviewer who disagrees should say so in §10.

**Registry drift (D3).** `SelfGated` returning false for a genuinely self-gating code downgrades the message from "retrying cannot help" to a plain terminal state — but `Escape` is total, so the route still prints. Drift costs precision, never the exit.

**Concurrency.** Nothing here introduces new writers. The gate store stays single-writer append-only; `readiness` is pure. The one added read (merge-base commit time) is idempotent and cacheable within a run.

**Backward compatibility.** Envelope decoding is already tolerant of unknown fields — the package documents this explicitly — so a new producer's artifacts are readable by an old consumer and vice versa. Old verdicts decode with `Observed == nil` and take the §7.5 path.

## 9. Rollout / implementation plan

Multi-repo. Committed phases are P0–P4; everything after the gate is a stub until validation earns it. `#186` runs in parallel as its own committed track with its own gate — **not re-planned here**.

| Phase | Goal | High-level tasks | Depends on | Repo | Gate | ~wLOC | Model/effort |
|---|---|---|---|---|---|---|---|
| **P0 — instrument** | The corpus exists before the fixes do | one-line `friction` capture verb (append: repo, ts, session, text; **no class**); `friction rollup --since` classifies in bulk; `chmod +x friction-scan.sh` | — | cc-skills | pre-gate | ≤150 | sonnet/extra |
| **P1 — the merge path explains itself** | Theme 1's cheap 80% | `readiness.Escape` (total) + `SelfGated` registry; wire `escape`/`self_gated`/`retry_helps` into every non-zero terminal state; pretool-guard inspects only the invoked command, not `--body`/`-m` string args; guard remedy reads the real state path | — | workbench, hooks | pre-gate | ≤250 | **opus/extra** — touches gate's terminal-state surface |
| **P2 — local signals stop lying** | "Green" means green *here* | `#[cfg(target_os="linux")]` the two `rooms` rootfs host-shell tests; land `ship#246` (state the env, don't inherit it); `/worktree-add` runs the repo's install step when it sees a lockfile; note the zsh word-splitting difference where shell snippets are kept | — | rooms, ship, cc-skills | pre-gate | ≤120 | sonnet/extra |
| **P3 — provenance contract** | One vocabulary for evidence | `contracts`: `Observation`, `Degradation`, `KindDegradation`; `Verdict.Observed`; evidence-body contract; schema `v0.3.0`→`v0.4.0` + conformance; producers stamp it | — | workbench | pre-gate — **no-regret leaf** | ≤300 | sonnet/extra — type-enforced, schema-pinned |
| **P4 — readiness truth in gate** | Theme 2's meat | `readiness.Covers` + merge-base staleness (D5); unverifiable-by-construction vs not-verified; `-readiness {human\|panel}` grant field + ledger/console surfacing (**D4 — blocked on §10.1**); fold ollama degradation into `KindDegradation`; declared refusal points (D2) | P3 | workbench | pre-gate | 400–600 | **opus/max** — changes what gate authorizes |
| **VG** | **VALIDATION GATE** | §11 | P0–P4 | — | **binary** | — | — |
| **#186** | *Committed parallel track* — phone-friendly executor approvals | See [`gate-approval-ux/spec.md`](../gate-approval-ux/spec.md) §9. Own phases, own VG. **Not widened by this doc.** | — | workbench | its own | — | per that doc |
| P5 — review-thread truth *(gated)* | A resolved thread is distinguishable from an ignored one | key threads on `id` + `originalLine`, **never** live `line`; post-push step that surfaces unresolved threads whose finding text overlaps the new commit and offers to answer them | VG | cc-skills, workbench | post-gate | — | opus/extra |
| P6 — provenance adoption *(gated)* | Close D2's `unknown` window | flare / console / triage / tracelens consume `Observation`; remaining permissive readers become declared refusal points | VG | workbench | post-gate | — | sonnet/extra |

**Why P5 is after the gate, not the gate.** Its core move — deciding whether a commit addresses a thread's concern — is a fuzzy match with no cheap falsifier (§10.4). Everything P0–P4 does is mechanically checkable. Putting the one unfalsifiable heuristic before the gate would let it decide whether the gate passes.

**Why P2 sits with the rest.** Three one-line fixes in three other repos look like unrelated hygiene. They are Theme 2 at the local layer: a documented gate that is permanently red trains everyone to read past a failing `make check` — exactly the habit that hides a real regression.

## 10. Open questions

1. **D4's reading — operator's call, blocks P4.** Is grant-scoped readiness substitution a legitimate use of the Capability plane (reading **a**), or does *any* non-human readiness signal violate "findings ≠ authorization" (reading **b**)? §4.4 argues (a) and moves mint authority to the operator to make it safe. If the answer is (b), P4 drops the grant field and every merge keeps parking — which makes #186 the entire mitigation.
2. **Where `Observation` lives.** `contracts` root, or a `contracts/observation` sub-package? The lazy-migration policy says defer until a second consumer exists; there are already ≥3 (gate, triage, tracelens). Leaning root.
3. **One friction log or many?** Today: per-repo logs plus a cross-cutting one, split by human judgment at write time. A single log with a `repo` field rolls up better and reads worse — and *reading* is what makes the corpus useful. Unresolved; P0 should not hard-code the answer.
4. **P5's thread-matching falsifier.** How do we test "this commit addresses this thread" without an LLM call on every push? No answer yet. This is why P5 is gated.
5. **Is `degradation.fatal` a per-case flag or a rule?** §5 proposes the rule *narrator degradations are advisory, verifier degradations are fatal*. That rule needs to hold for producers not yet written, or it will be decided case-by-case and drift.
6. **Does P1's guard fix belong in this doc at all?** It lives in `~/.claude` hooks, not workbench, and it is the only phase item with no artifact in the chain. Kept because it is the same failure — a blocker explaining itself wrongly — but a reviewer may reasonably route it out.

## 11. Validation plan

The gate is **binary and baseline-free**, in two parts. Both must pass.

**1. Recorded-scenario replay (mechanical, in CI).** The five failures of 2026-08-02 become fixtures, each asserting the *new* terminal behavior:

| # | Scenario | Required outcome |
|---|---|---|
| 1 | judge provider unavailable | `self_gated: true`, `retry_helps: false`, escape route printed, reason non-empty |
| 2 | no `reviewDecision`, no `-readiness panel` grant | parks **naming the grant remedy** — not a generic readiness park |
| 3 | checks green but observed before the merge-base | **refused as stale**, never counted |
| 4 | ollama down | `KindDegradation` in the run; stdout parses as JSON with zero preceding lines |
| 5 | `gh pr create --body "…gh pr merge…"` | guard permits; remedy text (if any) names the real state dir |

**2. Live self-direction canary (binary).** One real portfolio PR driven from park to merged where **every command run was named by the previous command's output** — no source reading, no state-dir guessing, no judging whether a green is stale. Checkable from the session transcript after the fact. It happened or it didn't.

That second half is the actual test of the hypothesis: the claim in §1 is that the workbench records decisions but not evidence, and a self-directing chain is what having both looks like.

**Explicitly NOT the gate — the product hypothesis.** *Does friction of class `deadlock` / `genuine-gap` in the merge path actually drop?* That needs the P0 corpus and several weeks of sessions, and it has no baseline today — the corpus was empty for the session that generated all of this. Measured over ~4 weeks post-VG as a **separate** judgment, never folded into the engineering gate. Conflating them would make the gate unfalsifiable, which is the failure mode this entire doc is about.
