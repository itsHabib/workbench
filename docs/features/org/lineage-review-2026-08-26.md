# Ownership-continuity lineage review — 2026-08-26

**Status:** review record — independent reconciliation of what #245 designed,
what shipped, what open PRs measure, and what remains hypothesis. This file is
the fold-in target for the lock decisions on
[workbench#245](https://github.com/itsHabib/workbench/pull/245); each item in
§6 is written to be accepted, amended, or struck in place.
**Method:** every open PR head, base, CI state, review, and unresolved thread
re-verified live on 2026-08-26 against GitHub, the gate ledger
(`~/dev/gate/state/log.jsonl`), `~/.claude/settings.json`, the installed `org`
binary, and the live chains under `~/dev/org/state`. Nothing below is quoted
from a snapshot without re-checking. The commissioning map is
`cc-skills:skills/ticket-owner/references/review-corpus-2026-08-26.md`.
**Reviewer:** Claude (Fable), commissioned by the operator.

---

## 1. Verdict

**One coherent system is forming at the kernel and receipt layers; the
incoherence is concentrated at the seams and in this document pile.** What
shipped is small and disciplined — a contracts leaf with an exhaustively walked
state machine, a file home with flock-serialized admission, a byte-capped boot
index, two fail-open hook scripts, one instrument PR. The
too-much-architecture risk lives entirely in `vision.md`'s *unbuilt* planes
(broker, batond, levels, tenancy, fleet), and those are already fenced off
twice: by vision's own failed §7 P0 gate, and by the closure TDD's line —
*measure the closed loop before adding another plane, daemon, MCP, or state
store* (`docs/features/agentic-workbench-closure/spec.md`).

The actual defects are four, and none of them is the architecture:

1. **Consumers not consuming shipped mechanisms.** `ticket-owner` re-implements
   the kernel's intent law in prose while `org intent` / `org resolve` exist
   and structurally enforce it; the delivery-evidence-chain hypothesis invents
   receipt-pointer prose while substrate-autowiring already owns a locked
   `verdict`/`receipt` artifact vocabulary joined on `head_sha`; drive#47's
   watcher and flare's cards both notice parks and expiring grants without
   composing.
2. **Default-mode identity.** The kernel checks a *presented* incarnation even
   without `-strict`, but an absent one is auto-stamped as the current holder
   (`cmd/org/internal/home/home.go:154-177`). The stale-writer law the design
   spent its hardest review round getting right is opt-in at every call site.
3. **Stop semantics defined nowhere, load-bearing in three places.** The
   harness's Stop event fires each time the main agent finishes, not once per
   session. That hits `hooks:scripts/stop-discharge.sh` (thread filed on
   hooks#42), `cmd/org/hooks/stop-mark.sh` (no thread filed; would flood
   chains with marks once pasted), and #265's distill rate (its own unresolved
   P1: a session that checkpoints *and* gets a stop mark counts as two ends).
4. **#245 disagrees with itself** — 21 unresolved threads, including the
   status-block-vs-committed-phases contradiction in vision §7, the
   fence-propagation overclaim against T1, the `contracts/authority` name
   collision with the existing room-authority package, and the
   reconciliation brief restating the already-withdrawn notes-unreadable
   claim.

The system is **not** a pile of duplicated state. It has exactly one chain
store, one work store per side (dossier / Jira), one authorization ledger, and
the duplications that exist are narrow and named in §5.

## 2. Snapshot verification

The review-corpus map is accurate: every head it pins matched live state on
2026-08-26. Additions the map does not state: workbench#266 is based on
`feat/org-sweep` (stacked on #265); #252 stacks on #251; hooks#43 stacks on
hooks#42; drive also has #46 (the discharge TDD, draft) and #48 open. CI is
green everywhere it runs.

Independently verified ground truth:

- **Wiring.** `~/.claude/settings.json` has only `PreToolUse`/`PostToolUse`.
  No SessionStart, no Stop, zero org mentions. The hook paste is structurally
  an operator action (the auto-mode classifier blocks agents editing hook
  config); installed copies wait at `~/dev/org/hooks/`.
- **org-mcp is live.** Registered user-scope; agent sessions on this machine
  carry `org_boot` … `org_verify` as native tools (and no `org_sweep`,
  consistent with #265 unmerged).
- **Live chains are real but thin.** Tenant `mh`: `lead:agentic-development`
  11 records (1 checkpoint, 0 marks, 3 assigns, one claim/yield pair, last
  write 2026-08-24); `lead:rooms` charter-only. `roles.map` binds the
  workbench and rooms checkouts.
- **Gate ledger.** workbench#262: 25 records; #263: 15 — the governed-path
  claim for both is corroborated. hooks#42: 13 records. #245, #265, #266,
  hooks#43, drive#46/#47, cc-skills#29: none. "Release authorized" applies to
  exactly two things in this lineage.
- **The installed `org` binary is stale.** Built 2026-08-24T00:21 from
  `df45b2fe` (`vcs.modified=false`) — before #262 (merged 18:28) and #263
  (22:20) landed. The binary that wrote the live chains predates the merged
  runtime. Rebuild from `main` before wiring anything.
- **The handoff has a local uncommitted draft** (+40/−15 in the `docs-org-tdd`
  worktree; not PR content). Its additions verify: governed path for
  #262/#263, org-mcp registration, operator-only hook paste. One claim does
  not: hooks#42 "merge-ready" — #42 carries 6 unresolved threads with zero
  reply comments on record, including two P1s (Stop dedup; Codex envelope
  parsing). The fixes may exist at the head; the thread ledger does not say so.

## 3. Layer reconciliation

1. **What #245 originally designed.** Two documents. `spec.md` (superseded;
   kept for §5/§6 history) designed chains-as-Envelope,
   incarnate/checkpoint/handoff, annul, explain/doctor. `vision.md`
   (canonical) redesigned it: spine/blob split, the §3.9 claim state machine,
   host-written marks plus a distiller, the effect plane inside custody,
   fences, levels, batond. The governing correction — *the metaphor is not the
   mechanism*; build durable-assignee identity, authority scoping, conclusion
   routing — postdates both and is the standard everything else is read
   against.
2. **What #246/#248/#262/#263 shipped** — the honest core: versioned canonical
   encoder (`canon/v1`), record spine with kind classes and `min_reader`, the
   ownership fold with identity-checked-before-position and the L1–L3 claim
   machine (86 reachable states walked; the takeover→unassign→retire
   obligation-stranding bug found only by enumeration), then the file home,
   flock admission, boot/status/log/verify/blob, receipts with fence,
   presented identity, `context.d`, org-mcp. Both runtime PRs landed through
   panel + gate + pinned merge. What shipped is a deliberate *subset* of the
   docs, with recorded deviations (JSONL not SQLite; write-as-holder default;
   marks-not-checkpoints) — smaller and more defensible than the documented
   thing.
3. **What #265/#266 measure.** Exactly the two rates the bet rests on:
   distilled session ends (checkpoints vs marks) and inherited obligations
   discharged (vs orphaned), computed by replay because an orphan is only
   visible as a fold transition; plus tenant-scoped `assign_conflicts` as the
   honest detected-not-prevented downgrade of vision Appendix A4. Two live
   gaps: the mark+checkpoint double-count P1, and MCP-side tenant plumbing
   (#266 scopes the CLI sweep to `-tenant`; the MCP verb passes none, and an
   empty `ORG_TENANT` now yields an empty sweep). #266's base also fixed two
   of #265's threads (valid-prefix reads) — merge in stack order.
4. **What the earlier closure/evidence/gate PRs already own** — the constraint
   layer. workbench#4/#5: no new orchestrator or store; artifacts, not call
   stacks; measure before adding a plane/daemon/MCP/store. #129 +
   substrate-autowiring: the `verdict` + `receipt` dossier artifacts emitted
   at the gate boundary and the merge hook, driver-agnostic, joined on
   `head_sha` — **the already-designed receipt vocabulary the evidence chain
   should consume**. #156: exact-head review findings. Meanwhile gate is
   growing subject-continuity piecemeal — #253 (merged), #249, #254, #258
   (open) — which is the "Baton's first customer is gate" thesis being served
   *without the chain*. The missing decision: does gate own its subject fold,
   or consume org's? Nobody has written that down (§6 D6).
5. **What hooks#42/#43 and drive#47 add or duplicate.** Three genuinely
   distinct properties: task-keyed conclusions into dossier (discharge);
   owed-discharge detection/backfill (sweep — measured 21 sessions / 49 tasks
   / 0 recorded); resident noticing with durable deduped findings (watch). The
   duplications are narrower than "same thing twice": stop-mark and
   stop-discharge are two sinks for one event that must share the
   Stop-semantics fix, the transcript summarizer, and one distill budget;
   watch-gate and flare both surface parks/expiring grants and should compose
   (watch findings → flare sink) rather than ship as parallel notifiers.
6. **What cc-skills#29 proves locally** — fixture-proven, precisely: a
   mode-0600 lease carries `{tenant, role, work, incarnation}` across process
   replacement; a recovered transition key reused against an idempotent effect
   store produces exactly one effect while a changed key detectably produces
   two; a released incarnation's strict write is refused; corrupt lease state
   is refused untouched. It proves nothing about live Jira/Ship/GitHub — and
   says so itself. Two changes are owed before its first mutating tick: adopt
   `org intent`/`org resolve` as the intent channel (the kernel already
   refuses new claims *and* release while an intent is open —
   `contracts/org/reduce.go:371-373,459-467` — turning the skill's whole §3
   idempotency rule into chain law; recovery becomes
   `boot -json → .boot.open_intents[0]`, a structured field, instead of
   prefix-parsing `last_word.excerpt`, a truncatable display surface), and
   fold its last open thread (top-level PR comments in the review-completeness
   rule — the known Codex verdict-shape trap).
7. **What remains hypothesis.** The epic steward and the five-stage delivery
   evidence chain (`cc-skills:skills/ticket-owner/references/continuity-chain.md`).
   Correctly labeled, correctly gated behind the one-ticket trial. The steward
   survives the metaphor-substitution test better than vision's tree sections
   (Jira's epic→child structure is the tracker's own hierarchy), with one
   sharpening: on the personal side it would duplicate dossier's
   project/phase/task model — its must-not-become-the-store list names Jira,
   GitHub, Ship, review, and gate, and omits dossier. The honest statement of
   the hypothesis: *does the project/phase/task pattern port to an external
   tracker via a chain role, consuming Jira/Ship/gate receipts it does not
   own?*

## 4. Every major #245 claim, classified

Rungs, from the review map: design documented → mechanism merged → open PR →
installed/wired → fixture-proven → live-dogfood-proven → release authorized.
Nothing below is promoted a rung because the story coheres.

### Continuity plane

| Claim | Status |
|---|---|
| Tip rule: ownership = CAS on the chain; identity checked before position | **Implemented and live** (#248/#262); live guarantee shrunk by the write-as-holder default |
| Record spine, kind classes, `min_reader`, versioned canonical encoding | **Implemented and live** (#246/#248) |
| Spine/blob split; erasable bodies with tombstones | **Implemented and live** (#262/#263) |
| L1–L3: one active claim; work_ref derived from state; dangling-claim inheritance; teardown refusals | **Implemented and live**; fixture-proven by exhaustive enumeration |
| One-outstanding-intent (T2's keystone, chain side) | **Implemented but unconsumed** — verbs and refusals shipped; no writer uses them; ticket-owner re-implements in prose |
| `next_due` declared liveness; LATE derived at read | **Implemented and live** |
| Boot re-entry index, byte-budgeted, `context.d` | **Implemented but unwired** — MCP-manual today; hook injection staged; paste is operator-only |
| Host-written `mark` at Stop | **Implemented but unwired**; per-turn Stop semantics unresolved |
| The distiller (verbless checkpoint authoring) | **Still entirely untested** — vision's own "only untested load-bearing assumption," still true |
| Resume canary / resume-fidelity ≥ 90% | **Still entirely untested** — nothing measures whether the chain carries thought rather than commitment |
| `seal` every K checkpoints | **Shrunk** — manual verb only |
| `annul` correction path | **Contract kind exists, no runtime verb** — unreachable in practice; §4.12's correctability bar unmet |
| `explain` / `doctor` | **Not built** |
| Takeover by supervisor | **Implemented, shrunk** — kernel requires a party named in Terms; spec §8's universal `--by operator` not implemented; the by-string is unauthenticated (open thread) |
| Cross-chain two-witness audit; `counterpart_absent` | **Untested** — kinds exist; the audit is not built (`verify` is single-chain) |
| Human-as-role | **Shrunk** — mapped checkouts attach the *lead* role; no `human:<name>` identity |
| Reorg records (split/merge/recharter); `wip_limit` | **Design only** |

### Authority plane

| Claim | Status |
|---|---|
| Grants carry incarnation + fence | **Field shipped** (#263 receipts); **enforcement untested** — no verifier keeps a high-water mark; gate/custody untouched; the open thread further narrows the claim to per-verifier windows |
| `contracts/authority` extraction | **Contradicted** — name collides with the existing room-authority receipts package; correctly held |
| cycle/spend/concurrency ceilings | **Stored, enforced nowhere** — charter decoration today |
| Two roots; dead-man key refresh; attenuation chain | **Design only** |

### Effect plane

| Claim | Status |
|---|---|
| Intent-before-wire in custody; R/Q/C/U classes; probes; `effect_unstamped`; reconciler; external timer | **Still entirely untested** — nothing built; the kernel's intent-ref is the only shipped piece |
| Model calls as effects; spend metering | **Design only** |

### Work plane

| Claim | Status |
|---|---|
| Work URIs with schemes; `subject_digest` on assign | **Implemented and live** |
| One-open-assign across all chains | **Contradicted as a law; shrunk to detection** — #266 (open PR) is the honest downgrade Appendix A4 demanded |
| Derived vs attested completion | **Design only** |

### Surface plane and process

| Claim | Status |
|---|---|
| org-mcp | **Implemented and live**. Shipped ahead of the closure TDD's measure-first line — recorded tension, defensible via `store-decision.md`, said out loud here |
| batond; If-Match API; tenancy; OIDC; levels; shadow report; console headline numbers | **Design only**; gated; should stay gated |
| §7 P0 gate | **Evaluated: fails 2 of 3** (27.6% vs <20%; ~25 vs 75 affordable role-days); collisions unmeasured quantitatively — but drive#46 measured a real collision cluster qualitatively: six concurrent sessions on this PR's own docs, four documented collisions, two 645-message zero-artifact sessions. The strongest collision evidence in the corpus is about the org work itself |
| POC-A | **Not run** — blocked on the operator hook paste; instrumentation staged |
| "Baton's first customer is gate" (re-key the observer by subject) | **Documented, unstarted as such** — while gate grows subject-continuity piecemeal (#249/#253/#254/#258); ownership decision missing (§6 D6) |
| Lean/Quint/parley laws wired to `contracts/org` | **Prior artifacts exist; none wired in-situ** |

### Consumers

| Claim | Status |
|---|---|
| ticket-owner bounded tick (cc-skills#29) | **Fixture-proven only**; live Jira dogfood not run; one unresolved thread; intent-as-prose P1 stands |
| Epic steward + delivery evidence chain | **Hypothesis only**; owes a named join to the substrate-autowiring `verdict`/`receipt` vocabulary on the personal side |

## 5. Duplications and missing joins

Duplications (narrow, named):

- **Two Stop writers** — `cmd/org/hooks/stop-mark.sh` (role chain) and
  `hooks:scripts/stop-discharge.sh` (dossier task notes). Two legitimate
  properties, one event. They must share the Stop-semantics fix, the
  transcript summarizer, and a single distill budget; wire both only with
  per-session dedup.
- **Two park-noticing paths** — flare cards (workbench#251/#252) and drive#47's
  gate watcher. Compose (watch findings → flare sink) or pick one.
- **Prose intent vs kernel intent** — ticket-owner's `"intent: <key>"`
  checkpoint vs `org intent`/`resolve`. The kernel wins; the skill shrinks.
- **Receipt prose vs receipt artifacts** — continuity-chain.md's evidence
  pointers vs substrate-autowiring's `verdict`/`receipt` kinds. The artifact
  vocabulary wins on the personal side; Jira/Ship receipts play that role at
  work.
- **store-decision vs the shipped home** — the SQLite-for-the-chain claim is
  withdrawn by events (open thread agrees); the SQLite∩Postgres subset
  survives where it belongs: drive#47's findings store.

Missing joins (each is real work nobody owns yet):

- The `(role, fence, work_ref, effect_id)` stamp on custody requests, and any
  verifier high-water mark — the entire enforcement half of the fence.
- Gate's run→subject fold (the observer re-keying) — no PR exists.
- `org sweep` into the dogfood evidence card — the trial's instrument is not
  referenced by the trial.
- A committed definition of "session end" shared by both Stop hooks and the
  sweep's distill denominator.

## 6. Decisions to fold (accept, amend, or strike in place)

- **D1 — Stop semantics.** Define session end once (final Stop per session,
  deduped), apply to stop-mark, stop-discharge, and #265's rates. Gates the
  hook paste. *Recommended: accept before anything else wires.*
- **D2 — Strict identity default.** Flip cmd/org to strict-by-default with an
  explicit `-as-holder` opt-out for interactive operator use, or make
  holder-writes a per-tenant/charter policy. *Recommended: flip before a
  second writer exists on any tenant.*
- **D3 — Intent channel.** ticket-owner adopts `org intent`/`org resolve`;
  recovery reads `open_intents`, never `last_word.excerpt`. *Recommended:
  accept; it deletes prose.*
- **D4 — Evidence vocabulary.** The delivery evidence chain names dossier
  `verdict`/`receipt` artifacts (substrate-autowiring) as its receipt form on
  the personal side; Jira/Ship native receipts at work. *Recommended: accept.*
- **D5 — Notification composition.** Watch produces findings; flare is the
  sink. Neither duplicates the other's store. *Recommended: accept; align
  #251/#252 and drive#47 before merging both.*
- **D6 — Who owns gate's subject continuity.** Either gate keeps growing its
  own subject keying (#249/#254/#258 direction) and org stays out, or the
  gate observer folds by subject over org chains. Pick one. *Recommended:
  gate-side for now; revisit after the ticket trial.*
- **D7 — The unreachable annul.** Either wire the `annul` verb (CLI + MCP) or
  strike §4.12's correctability claim until it exists. *Recommended: wire; it
  is small and the claim is load-bearing for operability.*
- **D8 — vision.md status reconciliation.** Rewrite the §0 status block to
  2026-08-26 truth (runtime shipped and governed; sweep open; POC-A blocked on
  the paste; P0 gate status per p0-findings including the classifier-boundary
  caveat), fold where-this-stands' verdict table in as its header says, banner
  reconciliation-brief and handoff as archival session prompts, commit the
  local handoff draft. Then fold the 21 open threads and lock. *Recommended:
  accept; #245 merges only after this.*
- **D9 — Do not start** batond, levels, tenancy, the broker, model-calls-as-
  effects, or the epic steward. The corpus's own gates already say this.
  *Recommended: accept as a standing fence, re-examined only on trial
  evidence.*
- **D10 — Binary hygiene.** Rebuild the installed `org` from `main`; the
  work-laptop preflight's pin-and-record discipline applies to the personal
  machine too. *Recommended: accept.*

## 7. Build-and-dogfood order

The main point, stated plainly: **stop reviewing, start running.** Everything
below is wiring and trials, not architecture.

### Today, on the work laptop (honest path)

1. Fresh-laptop preflight from `dogfood.md`: install pinned `org` +
   `skill-sync`, record `go version -m`, sync the skill catalog, `gh auth
   status`.
2. Bind exactly one approved Jira interface and one Ship interface into the
   operations profile (`context-template.md`) under a dedicated work tenant
   and state root. No credentials in the profile.
3. Run `continuity-smoke.sh` on that host — the mechanism control on the
   machine that matters, where BSD/GNU divergence has bitten before.
4. Operator charters and assigns the role; start the loop **read-only**:
   `/loop 10m /ticket-owner jira:<KEY> --role steward:<key> --repo <owner/name>
   --org-state <root> --tenant work`. Two identical read-only ticks proving
   same-state → same-conclusion is the first evidence, and it can land today.
5. **Before the first mutating tick:** D3 lands on cc-skills#29 (intent
   channel) and its last review thread folds. Then run the six break tests in
   `dogfood.md` and keep the evidence card per tick.

### This week, on the personal machine

6. D1 (Stop semantics), then the operator pastes the SessionStart/Stop hooks —
   POC-A's collision and distill counting starts the same hour.
7. Merge #265 → #266 (resolve the double-count and MCP-tenant threads or
   record them as judged residuals); `org sweep` becomes the standing
   instrument for both machines' evidence.
8. Reconcile hooks#42's thread ledger, then merge #42 → #43; dossier
   discharge starts accruing where tasks live.
9. D8: lock and merge this PR as the design record.

### Only after the trial passes its break tests

10. One real child ticket plan→draft-PR→independent exact-head verification
    with every receipt preserved and one forced replacement incarnation
    (continuity-chain.md's step 2). The epic steward stays on the shelf until
    then.

### The deployment shape (one loop lane per role)

The operator's framing is right and is exactly vision §4.11 made operational:
**each durable role gets one serialized scheduler lane running a bounded tick,
and org (CLI or MCP) is how the incarnation manages itself** — attach/resume
identity, claim, at most one authorized transition, checkpoint or intent,
yield, release. By role kind:

- **IC at work** — the `/loop 10m /ticket-owner …` lane above. The lease
  carries incarnation identity across tick processes; the lane, not the lease,
  is the mutex.
- **Maintainer** — cron-shaped ticks that re-derive from the world: `org
  sweep` on a schedule, drive#47's watcher, hooks#43's discharge sweep. Thin
  chains; the chain records only ownership and tuning decisions.
- **Lead (personal machine)** — no loop needed: sessions *are* the ticks once
  the hooks are pasted; SessionStart attach + Stop mark is the lane.

Three laws hold every lane: one non-overlapping lane per role; a bounded tick
with at most one external transition; strict identity on every write. A lane
that cannot guarantee non-overlap stops before mutation — that rule is already
in the skill and it generalizes to every agent that "manages itself."

## 8. What this review does not do

It does not merge anything, does not resolve #245's threads, does not decide
D1–D10 — those fold here, on this PR, by the operator. And it does not promote
any claim a rung: the ladder in §4 ends today at *fixture-proven* for the
consumer layer and *installed-but-unwired* for re-entry. The next two rungs —
wired, and live-dogfood-proven — are a paste and a trial, not a document.
