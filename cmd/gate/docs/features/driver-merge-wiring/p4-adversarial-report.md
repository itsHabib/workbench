# P4 — adversarial break-the-gate report

**Date:** 2026-07-06
**Branch under test:** `driver-wiring-core` (P1+P2+cycle-1/2 fixes; through commit 6a1a1f6)
**Method:** a Workflow of independent skeptics, each an Opus / high-effort sub-agent told to
*break* the gate (not bless it), reading the real worktree code + the spec. Five structural
fail-open probes × **3 independent skeptics each = 15 break attempts**, then one Opus adjudicator
per probe that adversarially re-checked every "broke" claim. A probe HOLDS unless a **majority
(>1 of 3)** achieve a genuine structural break. Total: 20 agents, ~1.31M sub-agent tokens.

## Verdict: all five probes HELD — 0 structural breaks (15/15 skeptics failed to break); 0 design fixes needed now.

| # | Probe | Verdict | Breaks | Design fix needed |
|---|-------|---------|--------|-------------------|
| 1 | Stale verified-SHA race (§8) | **held** | 0 / 3 | no |
| 2 | `already_merged` short-circuit bypass (§7c) | **held** | 0 / 3 | no |
| 3 | Single-writer-is-convention residual (§7c) | **held** | 0 / 3 | no |
| 4 | Exit-code-space collisions (§6 mf2) | **held** | 0 / 3 | no |
| 5 | Cycle-count join under-report (§4 D1 / mf1) | **held** | 0 / 3 | no |

Scoping the skeptics kept honest: `-live` real merges are **unbuilt** (gate records
`merge_not_implemented`, never calls `gh pr merge`), so probes 1/2/3 are largely against the
**design contract** in the spec, while probes 4/5 are against the **built code** (skeptics built the
binary and ran malformed inputs / tamper attempts empirically).

---

## Probe 1 — stale verified-SHA race (held, 0/3)

**Attack:** can the bounded caller null or influence the `--match-head-commit` pin so a moved head
reads as a pass, or can a `--match-head-commit` refusal be swallowed to exit 0?

**Why it held:** `HeadSHA` has exactly one write site — `verify/readiness.go` sets it from gate's
own authenticated `gh pr view … headRefOid` readback (`evidence.go`). There is **no `-sha`/`-head`
flag**; the caller passes only `-repo/-pr/-grant/-live`, so it cannot influence the pin. `verify.Reduce`
preserves the subject, so `act` emits `--match-head-commit reduced.Subject.HeadSHA` carrying the exact
verified head. Against the design contract, a moved-head refusal maps to exit 4 (fail-closed).

**Held-with-note (not a break):** `act` emits the command with no explicit non-empty guard on
`HeadSHA` — an empty SHA would degrade the pin to a no-op while still exiting 0. **Not
attacker-reachable today**: reaching a merge outcome requires readiness `MERGEABLE` + green
`statusCheckRollup`, which co-occur with a populated `headRefOid` on any real PR, and no merge runs in
built code. The guarantee currently rests on that incidental coupling rather than an explicit check.
→ **P5 hardening (recommend, do not block):** `if reduced.Subject.HeadSHA == "" { exit 4 }` at the
merge-emit site before `-live` lands.

## Probe 2 — `already_merged` short-circuit bypass (held, 0/3)

**Attack:** make a not-yet-merged, unauthorized PR read as `already_merged` for a free exit-0 land.

**Why it held:** the `MERGED` signal is GitHub-authenticated — `evidence.Gather` requests `state` and
`title` as separate fields; `readiness` reads `pv.State` from `state` only, never from attacker-controlled
title/body. The short-circuit is **design-only, unbuilt** (`already_merged` appears only in the exit-code
doc table). The readiness `MERGED` exemptions do **not** exempt the CI floor — the status-check loop and
empty-CI escalation still fire for merged subjects. Built exit-0 is reached only after the `TierWithin`
and `CyclesWithin` guards over a verdict that fails closed on an absent floor.

**Held-with-note:** readiness grants `MERGED` subjects an exemption (needed for backtesting historical
merged PRs) — bounded, reachable only on GitHub-authoritative `state==MERGED`, CI floor still enforced.
→ **Build-time note for P5:** keep the short-circuit strictly post-capability-check, a no-op that never
calls `gh pr merge`, + a pinned test asserting `already_merged` emits no merge command.

## Probe 3 — single-writer-is-convention residual (held, 0/3)

**Attack:** construct a two-writer interleaving (double-merge / unverified merge) that defeats all three
§7c guarantees at once without invoking the explicitly-named residual.

**Why it held:** built code has **no merge writer**. Every interleaving the skeptics built required
invoking the spec's **explicitly-named** residual (§7c: wiring a *merging* verb into the record step),
which the probe defines as held-with-note, not a break. `--match-head-commit` + `already_merged`
short-circuit + record-only-skips-merge is layered defense that holds across killed/retried ticks.

**Two held-with-note residuals for the `-live` build (P5):**
1. **SHA-blind `already_merged`** — the design exits 0 on `state==MERGED` without asserting
   `mergeCommit.oid == verified HeadSHA`. If the named second-writer residual ever holds, a later
   `gate -live` could pass on code it never verified. → P5: assert merged-SHA == verified-SHA before
   `already_merged` exits 0.
2. **Spec internal contradiction (worth a doc fix):** D3 (§4) says plain `land` has "no SHA pin," while
   §8 credits a stray plain `land` with `--match-head-commit`. These contradict; the real two-writer
   exposure is an *unverified* merge (stale-SHA + plain land's missing pin). → Reconcile the two spec
   sections; at `-live` build, make gate the **sole merge-credential holder** so a stray plain `land` is
   structurally impossible, not convention-forbidden.

## Probe 4 — exit-code-space collisions (held, 0/3)

**Attack:** make a malformed/aborted invocation exit with a decision code (0-3) instead of 4.

**Why it held:** all 7 flagsets are `flag.ContinueOnError`; every bare `os.Exit` literal is `codeError`;
the top-level `recover` routes panics to 4; decision codes 0-3 always return through `act` with a matching
`record` outcome, and `printJSON` precedes `os.Exit(code)`. Empirically, all 3 skeptics built the binary
and forced bad flags, unknown subcommands, non-numeric ints, missing required flags, and `-h` — **every
one exited 4** (and `-h` now exits 0 after the cycle-2 fix) with no decision JSON. No malformed run reads
as a decision.

**Held-with-note:** (a) `printJSON` ignores `enc.Encode`'s error before `os.Exit(code)`, so a broken
stdout pipe could truncate a decision's JSON while still exiting a decision code — an environment failure
the driver already treats as fail-closed per the contract ("a bare code with no matching outcome is an
error, not a decision"). (b) Infra errors (grant-not-found, key-missing) map to `codeRefused(3)` rather
than `codeError(4)` — semantically overloaded but fail-closed (never merges).

## Probe 5 — cycle-count join under-report (held, 0/3)

**Attack:** make a genuine 4th review cycle count as ≤3, through gate's own interface or by tampering the
log.

**Why it held:** the adjudicator confirmed the cycle-2 fix is **stronger** than the skeptics' headline
concern: `cycleCount` calls `st.Audit()` **inline** at the top of the decision path and fails closed with
`errLogTampered` *before* it scans, so an in-band log rewrite **faults the merge decision** rather than
under-counting. The four under-count levers are all closed: (a) no `-cycle` is trusted (n is state-derived);
(b) `countsAsCycle` excludes only gate-authored non-merging authorization artifacts, so every
merge-advancing outcome counts and padding is monotone-safe; (c) the exact-string `Repo`/`Number` join is
backstopped by `capability.Check`'s identical exact-string scope + signing-key custody outside the state
dir (a spelling variant that dodges the count gets `ErrScope`-refused); (d) the run id is an internal 64-bit
random, not caller-settable. No path makes an Nth counting cycle read as ≤N-1 without forging the anchor MAC
(key lives outside the state dir) or under-invoking gate (which produces no merge).

**Two held-with-note preconditions (both spec-acknowledged, out of task scope):**
1. **Under-invocation** — a driver that simply doesn't call gate on early rounds under-accumulates the
   count and defeats the *escalate-after-3* intent. It yields **no** unauthorized/unverified merge (the
   final merge still needs a valid signed grant + fresh evidence/floor/reviews). Closed structurally only by
   the §10.4a enforcing posture (branch protection making gate a *required* check), explicitly out of this
   task's scope.
2. **Key custody** — the cap's tamper-resistance rests on `grant.key`/`anchor.key` staying outside the
   state dir. `newEnv` refuses a key dir within the state dir (`dirWithin`), but on a shared-box posture an
   operator who lets the key default into a driver-readable dir could collapse the cap. → Operator confirms
   key custody at deploy + enables gate as a required check.

---

## Bottom line

No structural fail-open survived. The buildable scope (P1-P3: exit-code contract + signed cycle cap +
dry-run advisory wiring) is sound to land. Every residual is either **not attacker-reachable in the
dry-run/advisory posture**, or a named **`-live` (P5) precondition** the spec already gates on operator
sign-off + the `enforcement.md` preconditions. The `-live` hard-stop is respected: no real-merge code was
written.

**Carried forward to `-live` (P5), when it is earned:**
1. Explicit non-empty `HeadSHA` guard at the merge-emit site (probe 1).
2. Assert `already_merged`'s merged-SHA == verified-SHA before exit 0 (probes 2, 3).
3. Reconcile the D3 §4 ↔ §8 "plain land SHA pin" contradiction; make gate the sole merge-credential
   holder so single-writer is structural, not convention (probe 3).
4. Operator confirms key custody + enables gate as a required GitHub check to close under-invocation (probe 5).
