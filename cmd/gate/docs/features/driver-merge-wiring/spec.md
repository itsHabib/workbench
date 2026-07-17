# gate — driver merge-tail wiring

**Status:** design, for review — NOT a build commitment. The artifact we decide from.
**Owner:** @itsHabib (michael)
**Date:** 2026-07-06
**Revision (v3):** Fable review fully folded — verdict **ship-with-changes**, all 5 load-bearing ship claims independently CONFIRMED. Two operator decisions locked — (1) an over-cap park resolves by **re-minting a wider grant** (like a tier-over-ceiling park), not a judgment clear; (2) landing gate **supersedes** ship's abandoned in-engine merge-verdict plan (the #160-resume) as part of this task's done-definition — and all 5 must-fixes applied: **mf1** cycle-count join via `action → parent reducer-verdict → Subject`; **mf2** exit-code-space ownership (`flag.ContinueOnError`, parse/panic → exit 4, code↔JSON-outcome must agree); **mf3** the concrete supersede actions named; **mf4** `driver land --record-only` + a post-merge view-read retry + the named single-writer-*is-convention* residual; **mf5** `-max-cycles` defaults to 3.
**Related:** `docs/DESIGN.md` §Capability / §Composition / §The ladder law · `docs/enforcement.md` (the `-live` preconditions) · ship `packages/driver` (`land.ts`, `merge-verdict.ts`, `gh-port.ts`) · dossier project `gate`, phase `driver-wiring`.

This doc covers the **whole** driver-wiring seam, both dossier tasks:

- `wire-gate-as-driver-merge-step` — buildable now: the exit-code contract, the signed cycle cap, and `/work-driver` calling `gate` as a **dry-run advisory** merge step. §§4–8, rollout P1–P3.
- `adversarial-gate-before-live-merge` — **design captured here, build gated**: how `-live` composes with ship's `land` (§7c), the failure model (§8), and the cutover + enforcement preconditions (§10). Rollout P4–P6.

> **Reviewers — focus areas:**
> - **§7c** — how `-live` composes with ship's `land` verb without double-merging. The load-bearing flow.
> - **§4** — the live-path decisions: cycle-count *source of truth* (state-derived, not caller-passed); a cap park resolved by **re-minting a wider grant** (a judge pass can't launder a ceiling); and **gate's live merge uses `--match-head-commit`, never `--admin`**.
> - **§8 / §10** — the failure model (stale-SHA race, post-merge-lag record race, double-merge) and the `-live` cutover sequence. A structural fail-open here is exactly what the adversarial task must fail to find.

## 1. Problem & hypothesis

`gate` already decides whether a PR may merge and communicates that decision
through exit codes — but nothing consumes them. The `/work-driver` merge tail
still lands PRs by *prose* policy: three review cycles per PR, merge early on a
ship-it, admin-merge after the third. That cap is documentation, not
enforcement — the exact "a guarantee written in prose is a guarantee that
erodes" failure `gate` exists to kill: a driver can run a fourth cycle, or land
without the floor, and nothing structural stops it.

Confirmed against ship `main` (and independently re-verified by the Fable
review): the cap is **entirely prose**. `driver_streams.cycles` is a passive
integer the engine *never increments or caps* — the skill supplies `--cycles N`
purely for the audit record. And ship already *built* the structural answer —
`merge-verdict.ts:assembleMergeVerdict` (reviewer ballots + coordinator cycles +
CI + adversarial gate) — then never wired it: nothing calls it, the
`merge_grants` table was never added (migration `0011` is a gap in the sequence,
pre-assigned to a #160-resume that never landed), and the landing attempt (#160)
was closed as **"verdict forgeable."** That is the hypothesis in one word: an
*in-engine* merge verdict the bounded agent can forge is not a guarantee.
`gate`'s verdict is HMAC-signed, hash-chained, and derived from recorded state —
the un-forgeable external fill for the seam ship abandoned.

**#160's flaw, concretely — and why gate doesn't repeat it.** #160 was
"forgeable" for a specific reason: reviewer identity was matched by a *loose
login-substring regex with no bot allowlist* — a check a renamed or spoofed
login could pass, letting a forged "approval" authorize a merge. Gate closes
that class two ways: (a) its verdict and grant are HMAC-signed and hash-chained,
so a state-writer can't forge either; and (b) its reviewer signal is GitHub's
*authenticated* bot flag (`user.type == "Bot"`, recorded as evidence in
`evidence.go`), never a login match — and the review rung only ever *escalates*
on a bot finding (fail-safe), it never *authorizes* on one. A spoofed reviewer
can at worst force a park, not launder a merge.

**Decision (locked): gate supersedes the #160-resume.** Landing gate as the
merge step retires ship's abandoned in-engine plan. Recording that supersession
— and retiring or explicitly parking `assembleMergeVerdict` + the reserved
`merge_grants`/migration-`0011` slot — is part of *this task's* done-definition
(§9, §11 Q7), so the corpus never carries two competing merge-verdict mechanisms
and no one resumes #160 in parallel. Concretely (locked, mf3): park ship's
`freeze-scoped-merge-grant` task (`tsk_01KW3Q7027D8J9QCF3XB6Z3VZ4`), mark the
`driver-freeze-gate` manifest batch superseded, and note in ship's
`cloud-control-plane` spec that gate replaces the in-engine `merge_grants` plan.
These are ship-side mutations, named here; which window executes them is a
coordination call.

**The bet:** wiring `gate` as the driver's merge step — decision by exit code,
cap by signed grant — turns the merge tail from prose into a structural,
auditable gate, without any ship-engine surgery.

### Non-goals (deliberate, with triggers)

- **Shipping `-live` in this task.** `gate` keeps recording `merge_not_implemented`
  under `-live` through rollout P3; the dry-run keeps emitting the exact
  `gh pr merge --match-head-commit <SHA>` command. This doc *designs* the `-live`
  path (§7c, §8, §10) so the whole seam is captured, but **building** it is task
  `adversarial-gate-before-live-merge`, gated on §11's break-the-gate pass **and
  the operator's sign-off** and the `docs/enforcement.md` preconditions. The
  buildable scope of *this* task ends at **dry-run advisory + a structural cap** —
  the last safe step before live.
- **No ship-engine surgery — one minimal additive exception at `-live`.** `gate`
  gates from *outside* the driver engine via exit codes — the same shape as
  `tracelens ship <run-ref>` gating a driver run on a trace check. Ship's `land`
  step is unchanged **through P3**. The `-live` record step (P5/P6) adds *one
  minimal, additive* ship change — a `--record-only` flag on `driver land` plus a
  retry on its post-merge view read (§7c/§8) — not surgery to the merge-decision
  logic. Nothing gate-side touches the engine.
- **Replacing ship's `assertReady`.** Ship's always-on readiness floor
  (draft / conflict / failing-CI, enforced even under `--admin`) *stays*. `gate`
  **composes with** it (belt-and-suspenders on readiness) and **supersedes** the
  dead `assembleMergeVerdict` (the authorization policy). The *decision* to
  supersede (not resume #160) is locked here (§11 Q7); the actual ship-side
  deletion/parking of the dead code is a ship follow-up that gate-side work does
  not block on.
- **Coordinator thresholds as grant fields.** `DESIGN.md` §Capability foreshadows
  cycle caps *and* coordinator thresholds moving into grants. Only the cycle cap
  is in scope; coordinator thresholds stay prose until a second consumer needs
  them (`feedback_consensus`: don't build the quorum sibling before its trigger).

## 2. Requirements

### Functional

- `gate` exposes **named exit codes**; the decision→code mapping is a documented
  table and a pinned test, not inline literals.
- A grant carries an integer **cycle ceiling**. `gate` refuses to green a merge
  once a PR has consumed more review cycles than its grant allows.
- Cap-exceeded is an **escalation, not a block**: it parks (the structural form
  of "admin-merge after cycle 3"). The operator resolves it by **re-minting a
  wider grant** (a higher `-max-cycles`) or by stopping — exactly as a
  tier-over-ceiling park is resolved. The decision surfaces instead of the driver
  silently looping or silently landing.
- `/work-driver` mints one grant per repo (tier ceiling + cycle ceiling + TTL),
  passes the grant id to each `gate` invocation, and maps each exit code to a
  driver action.
- **(`-live`, design)** At `-live`, `gate` is the **single merge writer**: it runs
  `gh pr merge --match-head-commit <verified SHA>` itself and records the action;
  the driver records via `driver land --record-only` (a merge-skipping flag),
  never re-merging.

### Non-functional

| property | target |
|---|---|
| **Latency** | merge step adds one `gate` run per PR per cycle; evidence is 3–4 `gh` reads (~2–5s). Bounded by `gh`, negligible against the minutes-scale merge tail. |
| **Durability** | every decision is a hash-chained artifact; `explain`/`audit` reconstruct it from state alone. |
| **Consistency** | exactly one merge writer at `-live`; `--match-head-commit` pins the verified head so a post-verification push refuses rather than lands unverified code. |
| **Security** | cycle + tier ceilings are HMAC-signed (unsigned ⇒ forgeable ⇒ no guarantee). The cycle *count* is state-derived, never caller-asserted. Reviewer signal is GitHub's authenticated bot flag, not a login match (§1). |
| **Back-compat** | a grant with no cycle field (`MaxCycles == 0`) means *unbounded* — today's behavior for repos that don't opt in. |
| **Operability** | exit codes + JSON on stdout only; no daemon, no new dependency. `$0` (local `gh` + `triage-floor` + Ollama rung already required). |
| **Fail-closed** | an unreadable/ambiguous cycle count escalates; a stale-SHA merge refuses (exit 4); absence never reads as "0 cycles, proceed." |

## 3. Architecture

Where `gate` sits in the driver loop — unchanged from `DESIGN.md` §Composition:
a binary the driver calls and reads an exit code from.

```
ship driver:  import → dispatch → poll → judgment → [ MERGE STEP ] → record
                                                          │
                                          /work-driver calls: gate -repo R -pr N -grant G
                                                          │
                                        ┌─────────────────┼──────────────────┐
                                     exit 0             exit 1/2            exit 3/4
                                   would_merge        blocked/parked      refused/error
                                  land it (P3) /      stop / re-mint or   fix grant / surface
                                  gate merges (-live) judge (§7d)
```

Three layers, dependencies pointing down (the house rule — policy above
mechanism, single-responsibility layers):

- **mechanism** — `capability` (mint/check/sign grants), `state` (the append-only
  log the cycle count is read from). Dumb, swappable.
- **policy** — `verify.Reduce` (the ladder law) and the new cycle-ceiling check.
  Decisions live here.
- **contract** — the exit-code table in `cmd/gate`. The one surface the driver
  depends on; everything below can change as long as the codes hold.

**The ship-side seam (from mapping `main`, re-confirmed by the review).** Two
merge-gate mechanisms exist in ship's driver, disconnected:

| ship symbol | what it is | `gate`'s relationship |
|---|---|---|
| `land.ts:assertReady` | live, always-on readiness floor (draft / conflict / failing-or-pending CI). Runs before every merge, even under `--admin`. Gates on **no** reviews/approvals. | **compose** — keep it; `gate`'s readiness rung double-covers it, and `land` keeps it as the last guard before recording. |
| `merge-verdict.ts:assembleMergeVerdict` | pure reviewer/cycle/CI/adversarial verdict — **built, tested, wired to nothing** (`merge_grants` never landed; #160 closed "verdict forgeable" — loose login-substring reviewer match, no bot allowlist). | **supersede (locked, part of done — §1, §11 Q7)** — gate's signed, state-derived, API-sourced verdict replaces the abandoned #160-resume; the reserved `merge_grants`/migration-0011 slot is retired or parked. |

`gate` occupies the empty seam `assembleMergeVerdict` was built for and never
filled. No ship code moves to make room; the seam is already vacant.

## 4. Key decisions & trade-offs

**D1 — Who counts cycles? → gate derives from state.** The cycle *ceiling* is on
the grant; the current cycle *number* has two candidate sources:

- **(A) `gate` derives it from state.** Count the prior gate outcomes recorded
  for this repo+PR in the append-only log. Tamper-resistant: the bounded driver
  cannot under-report to sneak a fourth cycle past the cap — the whole point of a
  backstop the governed agent can't route around.
- **(B) driver passes `-cycle N`.** Simple, but it trusts the very identity the
  gate bounds — the driver could pass `N=1` forever.

**Chosen: (A)**, with `-cycle` accepted only as an optional advisory cross-check
that *logs* a discrepancy, never overrides. Matches `DESIGN.md` §Capability
("checked before any evidence is gathered — and again before a judgment is
applied") and the capability-backstop posture. Note: ship *also* has a `cycles`
integer, but it's skill-supplied for the audit record and equally un-trusted;
gate's count is derived from **gate's own** log, which the driver can't forge.
Cost: deriving the count needs a repo+PR scan of the log — see §11 Q1.

**What counts as a consumed cycle (refinement, post-review):** a run that
produced a ladder *decision* — blocked, parked for content judgment, or a
merge outcome. Authorization parks (tier- or cycle-over-ceiling, an unreadable
count) and capability refusals are policy *exhaustion*, not consumption, and
are excluded structurally (a machine-readable `code` on the escalation body,
never prose matching). Counting them would make the D2 re-mint resolution
self-defeating: the over-cap park itself would burn the cycle the wider grant
was minted to free, so a `-max-cycles`+1 re-mint would never unpark. The
exclusion is gate-authored and monotone — it can never hide a run that did
review work.

**D2 — Cap-exceeded parks, and the park is resolved by re-minting a wider grant.**
Over-cycle folds into the *existing* exit-2 park path — same code, same
escalation artifact the tier-over-ceiling case already uses in `act`. **How it
resolves (locked):** the operator **re-mints a grant with a higher `-max-cycles`**
(then the driver retries with the wider grant), exactly as a tier-over-ceiling
park resolves by a wider `-max-tier` — **not** by a judgment clear.

Rationale — this keeps the two axes clean and is forced by gate's own ladder law.
`DESIGN.md` §The ladder law: *"the grant's tier ceiling caps auto-land after
judgment — a judgment pass cannot launder a high-risk tier past the ceiling."* A
cycle ceiling is the same: a `judge` pass decides **content** ("is this code
OK?") and *cannot* launder a grant ceiling. Widening a ceiling is an
**authorization** decision ("may this identity exceed its cap?") — an operator
re-mint. Conflating the two (letting a judge clear the cap) would rebuild the
exact fail-open the ladder law forbids one rung down. Trade-off: a block would be
simpler but wrong — a fourth cycle isn't *bad code*, it's *policy exhaustion*,
and the operator, via the grant, owns that call.

**D3 — (`-live`) gate's merge uses `--match-head-commit`, never `--admin`.** Ship's
`land` merges with `gh pr merge --squash --delete-branch [--admin]` and **no SHA
pin**; `gate`'s sanctioned command is `--squash --delete-branch
--match-head-commit <verified SHA>` and **no `--admin`**. These encode opposite
philosophies, and gate's is the one going live:

- `--match-head-commit` lands *only the exact code gate verified*; a push after
  verification makes the merge refuse, not land blind. This is gate's reason to
  exist — keep it.
- **No `--admin`.** `--admin` bypasses branch protection. In the enforcing
  end-state (§10) `gate` *is* the required check; bypassing it would defeat the
  very enforcement that makes `-live` meaningful. So gate never admin-merges. Two
  distinct park reasons, two distinct resolutions (§7d): an **over-cap** park →
  re-mint a wider `-max-cycles` (D2); an **unmet required-approval** → a readiness
  block/escalation resolved by *actually* getting the approval (or a `judge` on a
  genuine escalation), never by gate `--admin`-ing past it. Trade-off: on repos
  *with* required reviews this is stricter than ship's current `--admin` land — an
  intentional tightening the operator opts into, not a regression.

**D4 — (`-live`) gate merges, the driver records via `driver land --record-only`;
one writer.** See §7c. The record step gets a minimal additive `--record-only`
flag (skip the merge, record from the gh view) rather than gate delegating its
merge to a `land` subprocess (which would couple ship to the gate binary) or
threading gate's SHA through `mark-merged` (which needs gate to emit a reliable
SHA). `--record-only` keeps `land`'s existing sha/time read as the record source,
adds no merge-decision logic, and reuses `land`'s stream-status guard.

## 5. Data model — the grant, extended

`capability.Grant` gains one field:

```go
type Grant struct {
    Repo      string
    Action    string
    MaxTier   string
    MaxCycles int       // review-cycle ceiling; 0 == unbounded (back-compat)
    ExpiresAt time.Time
    MintedBy  string
    Sig       string
}
```

- `capability.sign` extends its HMAC pre-image to include `MaxCycles` in a fixed
  position, so the ceiling is tamper-evident. Today the pre-image is
  `Repo | Action | MaxTier | ExpiresAt | MintedBy`; the new field joins it
  (e.g. `… | MaxTier | MaxCycles | ExpiresAt | …`). **This is a breaking change to
  existing signatures** — a grant minted before the field fails signature check
  because its pre-image changed. Because gate's grants are per-repo, per-run, and
  short-TTL (the driver mints one per run), the migration is "mint fresh grants,"
  not a data migration — recorded here, no versioned-signature machinery for v0
  (§11 Q2).
- `MaxCycles == 0` ⇒ unbounded: back-compat, and the honest default for a repo
  that hasn't opted into a cap.
- New coded error `ErrCycleExceeded = errors.New("grant_cycle_exceeded")`, in the
  same family as `ErrTierCeiling`, so the driver branches on a code, not prose.
- `func (g Grant) CyclesWithin(n int) bool` — mirrors `TierWithin`:
  `g.MaxCycles == 0 || n <= g.MaxCycles`.
- **The `-max-cycles` flag defaults to `3`** (mf5) — the canonical review-cycle
  policy, an *opinion* not a knob (`feedback_opinionated_not_generic`). A normal
  `gate grant` mints a 3-cycle cap; pass `-max-cycles 0` for explicit unbounded.
  The *field's* zero-value stays unbounded (back-compat for grants that never set
  it); only the CLI default is opinionated.

## 6. The exit-code contract (formalized)

Today's numbers, promoted from magic literals in `act` to named constants in
`cmd/gate` with a doc comment and a pinning test (`TestExitCodesAreStable`):

| code | const | outcome | decision | driver action |
|------|-------|---------|----------|---------------|
| 0 | `codeMerge` | `would_merge` / (`-live`) `merged` / `already_merged` | pass, within ceilings | land the PR (P3) / gate merges + `land --record-only` records (`-live`, §7c) |
| 1 | `codeBlocked` | `blocked` | code block | stop; do not merge |
| 2 | `codeParked` | `parked_for_judgment` | escalate, tier-over-ceiling, **or cycle-over-ceiling** | operator: **re-mint a wider grant** (ceiling park) or **`judge`** (content escalation) — §7d |
| 3 | `codeRefused` | `capability_refused` | no valid grant | mint/repair grant, retry once |
| 4 | `codeError` | (hard error) | evidence/reduce failure, **or stale-SHA merge refusal** | surface error, no merge |

The cycle-ceiling refusal folds into the **existing exit-2 park path** (D2) — no
new code. The contract's surface stays fixed at **five codes**; `-live` adds new
*outcome strings* (`merged`, `already_merged`) under the same code 0, and the
stale-SHA refusal (§8) surfaces under the existing code 4. The pinning test
asserts each decision→code mapping so a refactor can't silently renumber one.

**Exit-code-space ownership (mf2).** `gate` must OWN all five codes — nothing else
may emit one, or the driver misreads a malformed run as a decision. Three hazards,
all closed:

- **Flag parsing.** Every flagset today uses `flag.ExitOnError`, which calls
  `os.Exit(2)` on a bad flag — colliding with `codeParked`, so `gate gate -typo`
  would read as *parked*. Switch to `flag.ContinueOnError`, surface the parse
  error, and exit **4** (`codeError`). A malformed invocation must never
  masquerade as a park or a pass.
- **Panics / unexpected exits.** A top-level `recover` routes any panic to exit 4,
  never a bare runtime exit code.
- **Code↔outcome agreement.** The driver contract requires the exit code and the
  JSON `outcome` to *agree* (exit 0 ⟺ `would_merge`/`merged`/`already_merged`;
  exit 2 ⟺ `parked_for_judgment`; and so on). A bare code with no matching JSON
  outcome (a truncated/aborted run) is treated as an error, not a decision. The
  pinning test asserts the pairing, not just the code.

**Driver-side call contract** (the one surface `/work-driver` depends on):

```
gate grant -repo R -action merge -max-tier T1 -max-cycles 3 -ttl 6h   → grt_…
gate gate  -repo R -pr N -grant grt_…  [ -live ]                       → JSON + exit code
```

## 7. Key flows

### 7a. Per-repo grant mint (driver, once per run)

```
gate grant -repo owner/r -action merge -max-tier T1 -ttl 6h   # -max-cycles defaults to 3 (mf5)
→ grt_…   (printed id; the driver holds it for every gate call this run)
```

The prose "admin-merge after cycle 3" becomes: the `-max-cycles 3` default makes the
fourth attempt return exit 2; the operator resolves it by **re-minting with a
higher `-max-cycles`** (widening the ceiling, like a tier ceiling) — not a
judgment clear (D2). Policy is now the grant plus the ladder law, not a paragraph
in a skill.

### 7b. Merge step — dry-run advisory (driver, per PR, per cycle) — P3

```
gate gate -repo owner/r -pr N -grant grt_…
  exit 0 → would_merge   → driver lands via `driver land --pr N` (ship merges)
  exit 1 → blocked        → driver stops the stream; red stays red
  exit 2 → parked         → re-mint wider grant (ceiling) or judge (escalation) — §7d
  exit 3 → refused        → grant expired/mis-scoped; driver re-mints and retries once
  exit 4 → error          → driver surfaces the error; no merge
```

Through P3, **ship is the merge writer**: `gate` is advisory (records
`merge_not_implemented`), and `driver land --pr N` performs the `gh pr merge`.
Exactly one writer; no composition hazard because gate doesn't merge at all yet.

### 7c. `-live` composition with ship's `land` (the crux — no double-merge)

At `-live` the merge writer moves from ship's `land` to `gate`. The design keeps
**exactly one writer** by making gate merge and the driver *record-only*.

Ship's `land.ts:fetchMergedPrView` merges only when the PR isn't already merged:

```ts
let prView = await gh.viewPullRequest(repo, prNumber);
if (prView.state !== "MERGED") {          // ← the existing idempotency guard
  await assertReady(gh, repo, prNumber);  //   readiness floor (always on)
  await gh.mergePullRequest(repo, prNumber, { admin });
  prView = await readMergedViewWithRetry(gh, repo, prNumber, { sleep });
}
return markMerged(store, driverRunId, streamId, buildLandFacts(prView, opts));
```

The `-live` merge step, per PR per cycle, is two ordered calls — `gate -live`
replacing the skill's `gh pr merge --admin`, then `driver land --record-only`
(the merging `driver land` becomes record-only):

1. **`gate gate -repo R -pr N -grant G -live`** — on a within-ceilings pass, `gate`:
   - re-checks the grant (TTL bounds the *effect*, not just the run start — the
     existing re-check in `act`),
   - **short-circuits if already merged**: the gathered `gh pr view` evidence
     carries `state` and `mergedAt`; if the PR is already `MERGED`, gate records
     `already_merged` and exits 0 **without** calling `gh pr merge` (idempotent
     re-invocation),
   - else runs `gh pr merge N -R R --squash --delete-branch --match-head-commit <verified SHA>`,
     reads back the squash-merge commit SHA, records a `merged` action artifact
     (parents: the reducer verdict + the grant), and **emits that SHA** in its
     JSON result. Exits 0.
2. **Record-only — `driver land --pr N --record-only --cycles C`.** `--record-only`
   is a new, minimal *additive* flag on `land` (mf4): it **skips the merge call**
   and records from `land`'s existing post-merge `gh` view (`mergeCommit.oid` /
   `mergedAt`), reusing `land`'s stream-status guard. No merge attempt ⇒ it can't
   double-merge and doesn't depend on gate emitting a SHA. Because that read can
   hit GitHub's post-merge lag (§8), the same change adds a **retry** to `land`'s
   post-merge `viewPullRequest` (the merged view isn't final on the first read).
   This is the only ship-side change `-live` needs, and it touches no
   merge-decision logic.

**Why no double-merge, three independent guarantees:**

1. **The record step never merges** — `land --record-only` skips the merge call
   entirely (and even plain `land`'s `state !== "MERGED"` guard blocks a merged PR).
2. **Gate's `already_merged` short-circuit** — gate never merges a merged PR
   (covers a driver that calls `gate -live` twice).
3. **`--match-head-commit <SHA>`** — even in a genuine race, a second `gh pr merge`
   fails because the PR is already merged; and if the head *moved* between gate's
   evidence and the merge, gate's own command refuses rather than landing
   unverified code (§8).

The single-writer invariant is thus **ordering + idempotency**, not a lock.
**Residual (named, mf4):** single-writer is a *convention* — gate merges; the
record step is `--record-only`. If a future caller wires a *merging* verb into the
record step, the convention breaks; gate's `already_merged` short-circuit +
`--match-head-commit` still stop an actual double-merge, but this convention is
exactly what the adversarial pass (P4) must probe, not assume.

### 7d. Resolving a park (two paths, by park reason)

Exit 2 is not one thing — the resolution depends on *why* it parked:

- **Content escalation** (dirty/uncertain evidence: an actionable bot finding, an
  empty review panel, an unmet required-approval, a low-confidence extraction) →
  resolved by **`gate judge -run … -grant … -decision pass|block`** (or `-auto`),
  the existing judgment path. A judge can pass an escalation — but **cannot**
  exceed a grant ceiling (the re-check in `act` re-applies the ceiling after
  judgment).
- **Ceiling park** (tier-over-ceiling *or* cycle-over-ceiling) → resolved by
  **re-minting a wider grant** (higher `-max-tier` / `-max-cycles`), then retrying.
  A judge pass cannot launder a ceiling (D2, ladder law), so "admin-merge after
  cycle 3" is a **re-mint**, not a judge — and never a `--admin` flag (D3).

## 8. Concurrency / consistency / failure model

- **Stale verified SHA (the race that must fail closed).** Between gate gathering
  evidence (`headRefOid`) and running the pinned merge, someone pushes to the PR
  branch. `gh pr merge --match-head-commit <old SHA>` **refuses** (head moved).
  Gate must treat that refusal as **exit 4 (error), no merge** — never as a pass —
  and record the failed action. The driver's response is to **re-run `gate`**
  (fresh evidence over the new head), not to force the merge. Absence of a
  successful merge never reads as success. This is the merge-tail analogue of the
  reducer's "absence isn't green."
- **Post-merge view lag (the record-step race).** GitHub is eventually
  consistent: immediately after gate's `-live` merge, a `gh pr view` may still
  report `state: OPEN`, and `land`'s post-merge view read isn't final on the first
  read. The `-live` record step is `driver land --record-only`, which **skips the
  merge call entirely** (no redundant-merge window at all) and adds a **retry** on
  the post-merge `viewPullRequest` so the recorded `mergeCommit`/`mergedAt` are the
  final values, not a mid-lag `OPEN`. Even a stray plain-`land` in this window
  can't **double**-merge — its `state !== "MERGED"` guard + `assertReady` refuse a
  merged (or unready) PR — so the residual is a spurious error, not a double-merge.
  Note the honest limit (D3): plain `land` merges with **no SHA pin**, so a stray
  plain-`land` that *did* fire on a still-`OPEN` PR whose head moved could land an
  *unverified* head — it is `--match-head-commit` on **gate's** merge, not plain
  `land`, that closes the stale-SHA case. This is exactly why the `-live` end-state
  makes gate the **single merge-credential holder** (§7c residual, §10): so a stray
  plain-`land` is structurally impossible, not merely convention-forbidden.
- **Double-merge** — prevented by the three guarantees in §7c; none depends on a
  lock, so it holds across a killed/retried driver tick.
- **Cycle-count ambiguity** — if the repo+PR log scan is unreadable or ambiguous,
  gate escalates (exit 2), never assumes "0 cycles used, proceed" (§2 fail-closed).
- **Grant TTL lapse mid-run** — the `act` re-check already refuses an expired
  grant (exit 3); the driver re-mints and retries once (§7b). §11 Q3 weighs
  longer TTLs for multi-day runs.
- **Crash between merge and record** — gate's merge and its `action` artifact are
  separate steps; a crash after `gh pr merge` but before the artifact leaves the
  PR merged with no gate record. The driver's `land --record-only` still records
  to `driver_streams` from its `gh` view read, and gate's next `audit` shows the
  gap. Acceptable for v0; a merged-without-record PR is *observable*, not silent.

## 9. Rollout / implementation plan

| phase | goal | high-level tasks | depends-on | gate |
|---|---|---|---|---|
| **P1 — exit-code contract + code-space ownership** | stable surface to branch on | named `code*` consts in `cmd/gate`; the §6 table; `TestExitCodesAreStable`. **mf2:** flagsets → `flag.ContinueOnError` (parse error → exit 4, not the `os.Exit(2)` that collides with `codeParked`); top-level panic-recover → exit 4; pin code↔JSON-outcome agreement. | — | tests green; `gate -typo` exits 4 (not 2); `gate`/`backtest` output unchanged |
| **P2 — grant cycle ceiling** | cap becomes structural | `MaxCycles` on `Grant` + into `sign`; `CyclesWithin`; `ErrCycleExceeded`; `-max-cycles` on `grant` (**default 3**, mf5); the over-cap park in `act`; the state-derived count via the `action → parent-verdict → Subject` join (D1/mf1). | P1 | scripted N-cycle replay parks on cycle N+1, not before; signature covers the field; field zero-value ⇒ unbounded; flag default ⇒ 3 |
| **P3 — driver integration (dry-run)** | `/work-driver` calls gate | skill mints per-repo grants, calls `gate` as the merge step, branches per §6; the prose cycle number is **deleted** from the skill and expressed as `-max-cycles`; the over-cap resolution documented as a re-mint (D2). **Done 2026-07-08** — skill rewired (no cycle number; code↔JSON agreement required; missing binary = setup error); dogfooded on ship#182: exit 1 → stop, exit 2 → `judge`, exit 0 → land, JSON evidence on that PR. | P2 | a dry-run pass over a real PR shows gate's exit code driving the merge decision; skill states no cycle number |
| **╫ VALIDATION GATE** | prove the seam before live | — | P3 | **P3 green in dry-run** ⇒ proceed to the adversarial task |
| **P4 — adversarial break-the-gate** | skeptics fail to break the live path | the mandatory Workflow (§11); targets structural fail-opens in §7c/§8 (stale-SHA, post-merge-lag, already-merged bypass, single-writer, no-`--admin`). Builds on the 2026-07-05 red-team + #3/#4/#5, does not re-run them. | P3 | skeptics find no structural fail-open |
| **P5 — `-live` implementation** | gate becomes the merge writer | replace `merge_not_implemented` with the pinned `gh pr merge`; the `already_merged` short-circuit; stale-SHA → exit 4. **Ship-side (minimal, additive):** a `--record-only` flag on `driver land` + a post-merge view-read retry (mf4). | P4 **+ operator sign-off + §10 preconditions** | `-live` merges the exact verified SHA or refuses; the driver records via `land --record-only` without re-merging |
| **P6 — cutover on one repo** | dogfood, then expand | flip the skill's merge step to `gate -live` on a single canary repo (itsHabib/gate itself); observe N merges; expand. | P5 | N clean gate-driven merges; `audit` intact; no double-merge |

**Done-definition also carries the locked supersede (Decision 2 / mf3):** park
ship's `freeze-scoped-merge-grant` task (`tsk_01KW3Q7027D8J9QCF3XB6Z3VZ4`), mark
the `driver-freeze-gate` manifest batch superseded, and note in ship's
`cloud-control-plane` spec that gate replaces the in-engine `merge_grants` plan —
so two competing merge-verdict mechanisms don't coexist and the slot isn't resumed
in parallel. The gate-side rollout above does not block on these ship-side
mutations; which window executes them is a coordination call.

**Killer-per-step:** P1–P3 are the buildable near-term (task 1). P4–P6 are
design-captured here (breadth) but **built just-in-time** after the gate — no deep
impl spec for a live path we haven't earned.

## 10. `-live` cutover sequencing + enforcement preconditions (task 2)

Turning on real merges is not a code flip; it's an ordered sequence with
non-negotiable preconditions from `docs/enforcement.md`. This section is the
honest statement of what `-live` does and does **not** buy.

**The ordered gate before the first real merge:**

1. **P3 dry-run advisory green** — the skill branches on gate's codes; gate still
   records `merge_not_implemented`; ship still merges. (Where PR #8 ends.)
2. **Adversarial break-the-gate Workflow passes** (§11) — skeptics told to break
   the *live* path fail to find a structural fail-open. In addition to the bot
   panel + `/review-coordinator`; the adversary catches contract-allowed holes
   the diff-anchored panel misses.
3. **Operator sign-off** — the reserved decision; `-live` does not activate on an
   agent's judgment alone.
4. **Enforcement preconditions (`docs/enforcement.md`), for *enforcing* `-live`:**
   - **(a) Branch protection** on the target repo requires the `gate` status
     check, with admin-bypass disallowed and direct pushes disallowed. Sub-item:
     gate today only *exits with a code* — for branch protection to require a
     `gate` check, gate's verdict must be **published as a `gate` status check /
     check-run** on the PR head (a CI identity wired to gate, or gate posting the
     status). That publishing capability is **new work beyond this task** — the
     trigger for a follow-on phase, flagged in §11 Q5.
   - **(b) Token custody real** — a merge-only identity holds the merge token;
     every governed agent is demoted to push-and-open-PR-only. On the current
     single box, every agent shares one `gh` credential that can `gh pr merge`, so
     this is **not real yet**.

**The honest two postures (name them, don't blur):**

- **Sole *sanctioned* executor (what P6 buys on the single box).** `-live` makes
  gate the only merge path the *driver* uses — every driver merge flows through a
  signed grant + composed verdict + audit artifact, and the double-merge is closed
  by construction. But with the shared token and no branch protection, this is
  **discipline + an audit trail, not prevention** (exactly `enforcement.md`'s
  framing): a rogue or buggy step could still `gh pr merge` directly. Whether to
  run `-live` in *this* posture — before token custody is real — is the operator's
  call at sign-off (§11 Q6): the design provides the mechanism and names the
  residual risk; it does not silently weaken `enforcement.md`.
- **Enforcing (the deployment end-state).** Only once 4(a) **and** 4(b) hold does
  gate *force* merges through itself — prevention, not discipline. This is the
  `enforcement.md` operator action plus the gate-as-status-check capability; it is
  a separate phase, not driver-wiring.

`-live` execution **must not be wired until (2) and (3)**, and must not be claimed
*enforcing* until (4a) and (4b). The design defaults to that; any relaxation
(e.g. single-box dogfood at posture 1) is an explicit, signed-off operator
decision, named as such.

## 11. Open questions

1. **Cross-run cycle counting (D1 / mf1).** Deriving the count needs the log
   queried by repo+PR across runs. Key constraint: `act`'s recorded
   `action`/`escalation` bodies carry only `{outcome, verdict, grant}` — **no
   repo/PR**. So the count-join is `action → parent reducer-verdict → Subject`:
   enumerate `action`/`escalation` artifacts, follow each to its parent reduced
   verdict (`Parents[0]`), load that verdict's `Subject`, and count the distinct
   runs whose subject matches `Repo`+`Number`. An `O(log)` `state.List` scan + a
   parent lookup per outcome — fine at current log scale; a recorded per-PR counter
   artifact is the optimization *trigger*, not a v0 requirement.
2. **Signature migration.** Confirm "re-mint, no versioned signatures" is
   acceptable — i.e. no deployment holds long-lived grants that must survive the
   `sign` pre-image change. (Expected yes: grants are per-run, short-TTL.)
3. **Grant TTL across long runs.** If a run spans days and the TTL lapses
   mid-iteration, is re-mint (exit 3 → retry) the intended path, or should
   cycle-bounded grants carry a longer TTL?
4. **~~Does gate execute the merge at `-live`?~~ Resolved (D4 / §7c):** yes — gate
   owns the merge (it owns the verified-SHA pin); the driver records via
   `driver land --record-only` without re-merging.
5. **Gate-as-status-check.** The enforcing posture (§10.4a) needs gate's verdict
   published as a GitHub `gate` check so branch protection can require it. New
   capability — spin its own phase when the enforcing deployment is triggered
   (graduating off the single trusted box, or a second non-trusted agent).
6. **Dogfood `-live` pre-custody?** Is the operator willing to run posture-1
   `-live` (sole sanctioned executor, discipline+audit) on a single trusted canary
   before token custody is real? Operator call at sign-off.
7. **Ship-side dead code — RESOLVED (locked, Decision 2 / mf3).** Gate supersedes
   the #160-resume; the concrete actions (park `freeze-scoped-merge-grant`, mark
   the `driver-freeze-gate` batch superseded, note it in `cloud-control-plane`) are
   the done-definition (§9). The one sub-choice left to the ship cleanup: *delete*
   `assembleMergeVerdict`, or *repurpose* it as the typed MCP carrier of gate's
   verdict (`mergeVerdictSchema` already exists in ship's reserved
   judgment-request schemas). Ship-side call, not a gate blocker.

## 12. Validation plan

Binary go/no-go signals, one per phase — prefer a pinned test or a scripted
replay over a vibe:

- **P1:** `TestExitCodesAreStable` pins all five decision→code mappings *and*
  code↔outcome agreement; `gate -typo` exits 4 (not 2); `gate`/`backtest` stdout
  byte-unchanged. **Go** = all.
- **P2:** a scripted N-cycle replay parks on cycle N+1 and not before (count via
  the `action → parent-verdict → Subject` join); a signature test proves
  `MaxCycles` is in the pre-image (flip it → signature fails); the `-max-cycles`
  flag defaults to 3, field zero-value stays unbounded. **Go** = all.
- **P3:** a real dry-run driver pass over one open PR shows gate's exit code (not
  prose) driving land/stop/park, the `/work-driver` skill no longer states a cycle
  number, and the over-cap resolution reads as a re-mint (not a judge). **Go** = all.
- **VALIDATION GATE:** P3 go ⇒ start task 2.
- **P4 (the `-live` gate):** the adversarial Workflow — skeptics explicitly told
  to break the gate — returns **no structural fail-open** against §7c/§8 (incl. the
  post-merge-lag record race). This is *the* gate for real merges, alongside
  operator sign-off + `enforcement.md` preconditions. **No-go** on any surviving
  fail-open.
- **P5/P6:** `-live` lands only the exact verified SHA (a mutated head refuses);
  the driver records via `land --record-only` without re-merging; `gate audit`
  stays intact across N canary merges with zero double-merges. **Go** = the canary
  run is clean.
