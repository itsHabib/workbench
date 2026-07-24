# Needs-a-Grant Surface — Technical Design Document

**Status:** draft / proposal — NOT a build commitment. The artifact we decide from.
**Owner:** @itsHabib
**Date:** 2026-07-24
**Related:** `cmd/gate/docs/DESIGN.md` (the grant model + exit-code contract), `cmd/console/docs/DESIGN.md` (renderer boundary), the console trace redesign (PR #120).

> **Reviewers — focus areas:** §4 (the three real forks: new `gate next` field vs. a new subcommand; how the suggested `-max-tier` is chosen; how "open PRs" are enumerated without a per-repo GitHub sweep) and §7 (the enumeration flow + its failure mode). The rest is mechanical.

## 1. Problem & hypothesis

When gate refuses a merge for lack of a live grant, it returns `capability_refused` (exit 3) with `run: ""` — **no run id, no persisted artifact**. The refusal is a CLI-only message to whoever ran `gate gate`. Nothing lands in gate's log, so nothing surfaces in the console or flare.

The result: a "grant needed" state is **invisible**. The only breadcrumb today is the console's grant ledger showing expired grants ("workbench T2 · expired 1h ago"), but nothing connects "that grant expired" to "…and repo X has open PRs waiting on it." The operator discovers a missing grant only when an agent happens to run `gate gate` and reads the exit code. Minting is a human act (the operator's delegation), so a surface that *asks* for the mint is exactly what's missing — the mint-side twin of the judge cards the console already shows for parked runs.

**Hypothesis:** if the console proactively shows *"repo X has N open PRs and no live grant → here's the mint command,"* the operator mints when needed instead of finding out by accident, and no autonomous merge silently stalls on an expired grant.

**Non-goals:**
- Auto-minting. Minting stays a human act — this surface only *suggests* the command; it never runs it. (Same posture as the judge cards: paste-ready, operator runs it.)
- A new notification channel. This is a console surface; whether flare also pages on it is a follow-up, deliberately out of scope (and paging on "you might want to mint" risks the same noise the procedural-park cards already cause).
- Changing the grant model, the tier ceiling semantics, or the exit-code contract.

## 2. Functional & non-functional requirements

**FR:**
1. The console shows a **NEEDS A GRANT** section listing each repo that has ≥1 open PR gate would act on *and* no live grant covering it (none minted, or the covering grant expired).
2. Each row carries a **ready-to-run `gate grant` command** with a copy button, matching the judge-card pattern (correct `-repo`, a suggested `-max-tier`, a default `-ttl`, and `-state`).
3. A repo with a live covering grant does **not** appear (no false "needs a grant").
4. The projection is owned by gate; the console only renders it (workbench boundary — console shells gate, never imports it).

**NFR:**
| Concern | Target |
|---|---|
| Latency | The surface reuses the data `gate next -live` already gathers — no *additional* per-repo GitHub round-trips beyond what the live docket reconciliation already costs. |
| Correctness | Never suggest a mint for a repo already covered by a live grant (dedup is load-bearing — a false page trains the operator to ignore the section). |
| Boundary | No `cmd/gate` import in the console; the surface is a field/section in gate's JSON projection, rendered verbatim. Enforced by the `hygiene` job. |
| Security | Read-only. No mint is ever executed. The suggested command is text. |

## 3. Architecture overview

Reuse the existing seam: the console already calls `gate next -json -live`, and gate's `next` already performs a **live PR reconciliation** to decide which parked runs are still real (open) vs. resolved (merged/closed). That reconciliation is the exact place that already knows "the open PRs gate is tracking." We extend it to also emit the *inverse*: repos with open PRs that have **no live grant**.

```
console  ──GET /api/next──▶  gate next -json -live
                                  │
                                  ├─ parked[]      (existing)
                                  ├─ grants[]      (existing)
                                  └─ needs_grant[] (NEW — per repo: open-PR count, grant state, suggested mint)
console renders: AWAITING JUDGMENT · NEEDS A GRANT · GRANTS
```

New = one array in the `gate next -json` output + one console section. Reused = the live-reconciliation path, the grant ledger read, the console's card/copy machinery.

## 4. Key decisions & trade-offs

**D1 — New field in `gate next -json`, not a new subcommand.**
The console already fetches `/api/next` on every docket render; the grant ledger and parked list live there. Adding `needs_grant[]` to the same projection means zero new console plumbing (no new endpoint, no new fetch) and one place that owns "what needs you." *Alternative:* a `gate grants-needed` subcommand + a new `/api/needs-grant` route — cleaner separation but a second GitHub reconciliation pass and more console wiring for no gain. **Choose: extend `gate next -json`.**

**D2 — How is "open PRs for a repo" enumerated?**
`gate next -live` already reconciles the PR state of *parked runs* — but a repo whose PRs were never gated has no parked runs, so those PRs aren't in that set. Two options:
- **(a) Grant-ledger-scoped:** only consider repos that appear in the grant ledger (a grant was minted for them at some point). For each such repo with no *live* grant, do one `gh pr list -R <repo> --state open` to count actionable open PRs. Bounded (one call per lapsed-grant repo), and it naturally scopes to "repos we actually gate."
- **(b) Global:** enumerate every repo the operator has open PRs in — needs a repo list from somewhere gate doesn't have. Rejected: gate has no "my repos" notion.
**Choose (a).** It answers the real question ("a grant lapsed and PRs are waiting") with a bounded cost, and a repo that never had a grant isn't in gate's world anyway. *Open fork for reviewers:* is one `gh pr list` per lapsed-grant repo acceptable latency on the docket path, or should `needs_grant[]` only be computed on an explicit refresh? (See §10.)

**D3 — The suggested `-max-tier`.**
Options: (i) a safe fixed default (e.g. `T2`, the common ceiling); (ii) derive from the open PRs' likely verdict tier (would need to run the floor over each PR — expensive, and the console isn't deciding). **Choose (i): suggest a fixed `-max-tier T2`** (the modal ceiling in the ledger today) and let the operator edit — consistent with the judge cards, which pre-draft a value the operator can change. Minting is the human's call; the command is a starting point, not a decision. *Reviewer note:* confirm `T2` vs. surfacing the last-used tier for that repo from the ledger (a nicer default).

**D4 — Dedup against a live grant is the correctness core.**
A repo is included **iff** it has open actionable PRs AND `max(live grant ceilings for that repo) == none`. A live grant (even expiring) suppresses the row. This is the one place a bug turns the feature into noise, so it gets a pinned test: expired-only → shown; one live grant → hidden; live-but-expiring → hidden (still valid).

## 5. Data model

No persisted state changes — `needs_grant[]` is a *projection*, computed at `gate next` time, never written to the log. Shape (sibling to `parked[]` / `grants[]` in `gate next -json`):

```json
"needs_grant": [
  {
    "repo": "itsHabib/dossier",
    "open_prs": 3,
    "grant_state": "expired",          // "expired" | "none"
    "last_expired_at": "2026-07-24T17:39:00Z",  // present when grant_state == "expired"
    "suggested_mint": "gate grant -repo itsHabib/dossier -max-tier T2 -ttl 24h -state <dir>"
  }
]
```

`suggested_mint` carries `-state` the same way `gate next`'s judge command already does (the console passes `-state` to gate; gate echoes it into emitted commands), so the console renders it fully paste-ready with no client knowledge.

## 6. API contract

- **gate:** `gate next -json [-live]` gains a top-level `needs_grant` array (above). Absent/empty when no repo needs a grant. Non-`-live` runs MAY omit it (no PR reconciliation to base it on) — decided in §10.
- **console:** no new route. `handleNext` passes gate's JSON through verbatim (unchanged). `app.html` reads `data.needs_grant` and renders a `NEEDS A GRANT` section between `AWAITING JUDGMENT` and `GRANTS`, reusing the existing `.section-label` + card + copy-button machinery. The `ui_contract_test` seam is unaffected (no new fetch path).

## 7. Key flows

**Compute `needs_grant[]` (inside `gate next -live`):**
1. Read the grant ledger; group by repo; compute each repo's live-grant state (any grant with `expires_at > now` → covered).
2. For each repo that is **not** covered but appears in the ledger (a grant lapsed): `gh pr list -R <repo> --state open --json number` → count.
3. If count > 0, emit a `needs_grant` row (`grant_state`, `last_expired_at` from the newest expired grant, `suggested_mint` with the fixed `-max-tier` + `-state`).
4. A repo with a live grant is skipped entirely (D4).

**Failure mode:** a `gh pr list` failure for one repo must not fail the whole `gate next` (the docket must still render). On a per-repo lookup error, drop that repo from `needs_grant[]` and continue (best-effort, like the docket's existing "unknown PR state" handling) — never fail-closed the whole projection.

## 8. Concurrency / consistency / failure model

`needs_grant[]` is a read-time projection with no writes, so no concurrency concerns. Consistency is "as of this `gate next` call" — same freshness contract as the parked list and grant ledger it sits beside. The only external dependency is `gh pr list`; its failure is per-repo best-effort (§7).

## 9. Rollout / implementation plan

| Phase | Goal | High-level tasks | Depends-on | Gate |
|---|---|---|---|---|
| **P1 — gate projection** | `gate next -json -live` emits a correct, deduped `needs_grant[]` | (a) grant-ledger→per-repo live-state grouping; (b) lapsed-grant repo → `gh pr list` count (best-effort per-repo); (c) `suggested_mint` builder reusing the `-state`/command echo; (d) pinned dedup tests (D4) | — | **VALIDATION GATE:** with a deliberately-expired grant + an open PR in that repo, `gate next -json -live` shows exactly one correct `needs_grant` row; a live grant suppresses it. Prove the projection before touching the UI. |
| **P2 — console render** | The console shows the NEEDS A GRANT section with copy-ready mint commands | render `data.needs_grant` as a section (reuse card/copy/section-label); place between AWAITING JUDGMENT and GRANTS; empty → no section | P1 | Manual: expired-grant repo shows the section with a runnable mint command; minting clears it on next refresh. |

Small feature: two phases, P1 gated because a false/duplicate row (the D4 bug) would make the section noise. Depth beyond this is not warranted.

## 10. Open questions

1. **Latency placement (D2 fork).** Is one `gh pr list` per lapsed-grant repo acceptable on the default docket render, or should `needs_grant[]` compute only on explicit refresh (or a `-needs-grant` opt-in flag)? Lapsed-grant repos are usually few, but this is the one added cost. **Reviewer call.**
2. **`-max-tier` default (D3).** Fixed `T2`, or the repo's last-used ceiling from the ledger? The latter is a nicer default but couples the suggestion to ledger history.
3. **Non-`-live` behavior.** Omit `needs_grant[]` entirely without `-live` (no reconciliation), or compute grant-state-only rows (repos with a lapsed grant, `open_prs` unknown)? Leaning omit — the surface is inherently a live question.
4. **flare (explicitly deferred).** Should a newly-lapsed grant with waiting PRs page the operator once? Out of scope here; noted so it isn't silently forgotten. Given the procedural-park noise problem, any flare routing of this must be a single, deduped, genuinely-actionable page — a separate design.

## 11. Validation plan

**P1 gate (binary, baseline-free):** in a scratch/test gate state, mint a grant for a repo, let it expire (or mint with a tiny TTL), ensure that repo has an open PR, run `gate next -json -live`, and assert:
- exactly one `needs_grant` row for that repo, `grant_state: "expired"`, `open_prs >= 1`, a `suggested_mint` that parses to a valid `gate grant` invocation;
- after minting a fresh live grant, the row disappears;
- a repo with a live grant never appears.

If that holds, the projection is correct and P2 (render) is mechanical. If the dedup is wrong (a covered repo shows, or a lapsed repo with waiting PRs is missed), stop — that's the whole value.
