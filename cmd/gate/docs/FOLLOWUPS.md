# Follow-ups — red-team hardening

Source: independent adversarial review, 2026-07-05. Full critique kept at
`pers/workbench-redesign/RED-TEAM.md` (outside this repo).

That pass endorsed the **scoping** (one thin gate binary, not a five-plane platform) but found the
gate **not yet trustworthy at its target seam**. These are the fixes standing between here and
wiring `gate` into the merge tail.

## Must fix before wiring into the merge tail

- [ ] **Close the absence-of-signal fail-opens.** *(blocker)*
  An empty bot-review panel makes `Reviews` pass; `Reduce(nil)` returns pass/T0 because
  floor-presence is enforced by `main.go`'s call order rather than the reducer; an empty
  `reviewDecision` passes on unprotected repos. Same "absence reads as green" class as rooms#47.
  **Fix:** move the floor-presence invariant into `Reduce` itself — no code-floor verdict in the
  set → escalate/block, never pass — and treat zero-signal (no reviews, empty CI, empty
  `reviewDecision`) as *escalate*, mirroring what `readiness.go` already does on an empty CI rollup.
  **Done when:** tests pin `Reduce(nil)` → not-pass and empty-reviews → escalate, and the invariant
  lives in the reducer, not the caller.

- [x] **Write down and enforce the capability backstop.** *(serious)*
  Minting was unprivileged — anyone who can run `gate` can run `gate grant`, and `backtest`
  self-minted a spendable T2 grant into durable state — while every agent the gate governs already
  holds a `gh` token that can `gh pr merge` around it. The capability plane is advisory until
  something *forces* merges through the gate.
  **Landed:** the enforcement model is written in `docs/enforcement.md`, stated without overclaim —
  branch protection requiring the `gate` check is the forcing function (until then the plane is
  discipline plus an audit trail, not prevention); token custody names the intended
  merge-capable-identity-vs-bounded-agent split; mint authority (unprivileged; `MintedBy` is an
  unauthenticated free string) and the `grant.key` custody decision (cross-referenced to the tamper
  task) are recorded; the `gh pr merge` bypass is named rather than implied-closed; and the operator
  branch-protection action is written down as the `-live` precondition. `backtest` no longer mints a
  spendable grant — it runs against a throwaway ephemeral store, so no grant reaches the durable log
  (pinned by tests). The README no longer implies the gate bounds/forces merges it can't force and
  links to `docs/enforcement.md`.
  **Still open (documented as future, not built here):** token custody is not yet *real* on the
  single box (every local agent shares one `gh` credential) — closing it is a precondition for
  `-live`; and real mint authentication (so only a designated identity can mint a spendable grant)
  is future work.

- [x] **State the tamper threat model honestly, then decide what to harden.** *(serious)*
  `Audit` caught naive body edits, broken links, and reordering — but **not** tail truncation or
  whole-log deletion (reported "chain intact"), and the unkeyed SHA-256 chain could be wholesale
  rewritten by anyone with file-write. `grant.key` also sat in the same directory as `log.jsonl`.
  **Landed:** the threat model is written in `docs/DESIGN.md` (*Tamper model*), matched 1:1 to the
  code. A keyed tip anchor — `HMAC(key, head ‖ count)` under a key held outside the state dir —
  now defeats wholesale rewrite; the recorded `count` catches truncation and whole-log deletion;
  `grant.key` and the anchor key moved out of the state dir (`-key` overrides, default user config
  dir), and previously-minted grants still validate after the move. Tests pin truncation, deletion,
  and rehashed-rewrite detection.
  **Still open (out of scope for that pass, noted in the design):** the stale-lock TOCTOU takeover
  race and a SQLite/WAL durability model.

## Before broader trust / real dogfood

- [ ] **Make block/park verdicts sticky and bound judge re-runs.**
  Block verdicts aren't sticky and `judge -auto` is nondeterministic, so a judge can be re-run until
  it passes — the 3-cycle-cap-in-prose hole rebuilt one rung down. Persist block/park as sticky (a
  later pass can't silently overturn one without recording why); cap or record re-runs.

- [ ] **Feed the auto-judge raw evidence, not just local headlines.**
  `judge -auto` only sees the local model's extracted headlines, not the raw comment bodies already
  recorded in state — so "premium judges from artifacts" is judging a lossy summary. Include the raw
  bodies in `judgeContext`.

- [ ] **Add a park notification channel.**
  5 of 7 real PRs park, with no notification, so they park silently. Emit something on park
  (stdout/file/console to start) so a parked run surfaces instead of waiting to be noticed.

- [ ] **Pin the primary diff path to the evaluated head.**
  Surfaced by the evidence-local-diff skeptic panel (2026-07-16). The oversized-PR fallback
  refuses unless `pulls.head == view.headRefOid`, but the primary path (`gh pr diff <n>`) fetches
  by PR number with no head pin: a force-push to an innocent head between the view read and the
  diff read records that head's diff, and a force-push back before merge still satisfies
  `--match-head-commit`. Window is a sub-call race needing push access + green CI on the decoy, but
  gate's threat model includes adversarial agents with push access. Fix: after a successful
  `gh pr diff`, re-read `pulls/<n>` and refuse unless `head.sha == view.headRefOid` (shrinks the
  window to a sub-call race); airtight variant fetches the under-cap diff SHA-pinned via the
  `compare` endpoint. The fallback path already has this property.
