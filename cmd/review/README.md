# review

`review` decides *who* has to review an exact pull-request head and *whether
another cycle is warranted* — and nothing else. `plan` shells `gh` and
`triage-floor`, turns the floor's tier into a `ReviewPlanV1` (reviewer set,
required set, cycle cap, requirements), and stamps it with a content-derived
plan ID plus the digest of the policy used. `request` is the only place that
writes to GitHub to trigger reviewers. `observe` reads reviews, comments, and
requested reviewers back into a `ReviewPanelV1`. `decide` applies a
deterministic stop / continue / address / escalate / park rule to a
`ReviewCycleInputV1`. `advise` asks a local model for an opinion that carries
no suppression authority. Every artifact re-reads the live head before and
after its work and refuses when the head moved.

Tenant layout: the binary is `cmd/review`; its decision logic is private under
`cmd/review/internal/policy`; the load-bearing seam is the pair of *exit codes*
(0 / 3 / 4) and the JSON artifacts on disk. The shared vocabulary lives in
`contracts/reviewroute` (plan, request receipt, cycle input, decision) and
`contracts/reviewpanel` (`ReviewPanelV1`). `review` imports triage's *output*,
never triage's code.

## Use

```
go build ./cmd/review

review plan    -repo owner/repo -pr 181 -head <sha> -out plan.json      # classify + route
review plan    -repo owner/repo -pr 181 -head <sha> -policy override.json \
               -triage-bin ./triage-floor -out plan.json                # validated override
review request -plan plan.json -out request.json                        # the only GitHub write
review request -plan plan.json -reviewers codex,cursor -out request.json # later, targeted cycle
review observe -plan plan.json -out panel.json                          # ReviewPanelV1
review decide  -plan plan.json -input cycle-input.json -out decision.json
review advise  -input finding-evidence.json -out advisory.json -model <ollama-model>
```

Findings evidence is *not* produced here — use the separate `reviewfindings`
binary, which emits `ReviewFindingsV1` from exact-head inline comments.

| Code | Meaning |
| ---: | --- |
| 0 | artifact written to `-out` (its path is echoed on stdout) |
| 3 | refused — stale head, closed PR, or an artifact that fails contract validation |
| 4 | operational error, including usage errors |

There is no exit 1 or 2; a "no" from `review` is exit 3 or a decision artifact
whose `action` is `park`/`escalate`, never a nonzero success code.

## How it works

`plan` reads the head, runs `gh pr diff | triage-floor -repo …`, then re-reads
the head. A stale requested head, a head that moved mid-classification, a
classifier failure, or an unreadable/invalid policy all produce a *successful*
`full_panel_fallback` plan over the hard-coded four-bot safe panel — never a
fabricated tier. Tier routing only happens when the subject repo is listed in
the policy's `enabled_repositories`; every other repo also falls back to the
full panel. The single checked-in policy
(`cmd/review/policies/tier-aware-canary.json`, embedded via `go:embed`) enables
exactly one repository today: `itsHabib/ship`.

`decide` refuses unless the plan and cycle input join on the same subject and
plan ID, then layers the rules in a fixed order: blockers (checks, panel,
coordinator, adversarial, per-finding), raised tier → escalate, cycle > 3
without T3 + a rationale → park, T0 continue → escalate, cycle ≥ cap → park,
accepted-but-unapplied findings → address.

## Constraints that are design decisions, not omissions

- **Continuation weights are shadow telemetry.** `continuation_weight` and
  `cumulative_weight` are recorded on every decision; no code spends or
  enforces a budget from them.
- **Policy is not caller-versioned.** There is one embedded canary policy;
  `-policy` is an explicit swappable override for experiments and failure
  tests, and the plan records the validated content's digest either way.
- **Proof substitution never replaces the first review.** It applies only from
  cycle 2, only when the tier allows it, never for critical findings, and
  never at T3 even if a custom policy asks for it.
- **Local advice cannot suppress.** `advise` returns a `fix|prove|defer|
  rereview` recommendation behind a verifier and a 0.75 confidence floor, and
  is not read by `decide`.
- **Comment-based completion is Codex-specific.** `observe` accepts a clean
  review from a structured Codex comment carrying a full reviewed commit;
  every other reviewer needs a real GitHub review at the exact head.
- **Review does not execute or merge.** Ship or the session ledger applies
  accepted findings; gate authorizes the merge.

## Status

Built and tested, **not adopted as the default review path.** Tier-aware review
is measurement-gated: `docs/review-credit-strategy.md` marks Phase 1
(tier-routed reviewer sets) **PARKED, data-gated**, and
`docs/features/tier-aware-review-canary/plan.md` reports "Implementation
locally green; PR review and live canary pending".

`docs/DESIGN.md` states the artifact-first boundary; `CLAUDE.md` is the
canonical scoped guidance and lists the invariants.
