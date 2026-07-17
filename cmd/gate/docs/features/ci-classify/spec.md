# gate — ci-classify rung (red-CI cause classification)

**Status:** design, for review — NOT a build commitment. The artifact we decide from.
**Owner:** @itsHabib (michael)
**Date:** 2026-07-06
**Revision (v6):** cycle-4 (v5 final validation) folded — claude's verdict was *"ready for operator lock as specified in §9."* codex + claude **converged** on the one real bug: the scorer's `$wrapRe` was missing the `exit status` relay I'd added to §7 in v5 (a spec↔scorer divergence; now added, 20/20 unchanged — the exclusion is a no-op on this set, as both reviewers predicted). codex: the reproduce recipe now writes `raw.jsonl` into `<here>` where the scorers resolve `-raw`. claude clarity sharpeners: §8 names the three conformance keywords (`required`/`type`/`additionalProperties`) + the split-PR sequencing of `Finding.Evidence`; §4 pinned test asserts composed `Findings == nil`; `Source` gets a package-const implementation note.
**Revision (v5):** claude's cycle-2 + cycle-3 reviews folded (they'd been missed — the review action edits its comment in place, so a count-based watcher didn't see them; corrected). Load-bearing: **named `Source: "ci-classify"`** as the infra carrier's stable handle (§4); **resolved where the verbatim line lives** — a new `Finding.Evidence` field / v0.3.0 schema bump (§8), not a `Locus` overload; stated `Findings` are **not** promoted through a block (only `Why`); made the exclusion set **literal substrings** (kills the `Post job cleanup.` regex-`.`) + added Go/shell relays (`exit status`) with a named toolchain-coverage risk; **two-sided held-out gate** (over-exclusion too); §1 "zero trusted-wrong at 14B" **re-credited to the floor** (it catches 14B's two token-exchange misses; the verifier alone doesn't); finding-granularity + advisory-invoked-producer clarified (§3); dead `Math.Min` in `score.ps1` removed; phantom §4a→§3.
**Revision (v4):** cycle-3 folded (codex ×3, cursor clean): the §7 wrapper/teardown exclusion filter is now implemented in the eval scorer (line-level + teardown region) — numbers unchanged (20/20; every fire is live error context), so the shipping floor is measured, not just specified; the escalated-row safety claim restated structurally (an escalation is untrusted and steers no consumer — two 14B escalations were wrong-bucket and safe *only because* distrusted); reproduce recipe scores the fresh raw file.
**Revision (v3):** cycle-2 panel folded — the scorer itself was the finding. codex P1: the combined scorer accepted advisory buckets without the §6 verifier; codex P2: the vendored floor table still carried the demoted `ETIMEDOUT|ECONNREFUSED`. Both fixed; §1 re-scored under **shipping semantics** and reframed two-axis (coverage × on-handled accuracy — 7B 80.4%/90.2%, 14B 82.4%/**100%**, zero trusted-wrong at 14B). Scorers now fail closed on row-count mismatch (codex P2); extractor no longer skips unreadable logs silently (cursor).
**Revision (v2):** first bot panel folded (codex P1 + cursor converged on the same composition hole). Composition honesty: an infra `escalate` composes UNDER the readiness block — the run exits `blocked`, not parked; §4 now says so and P1 adds the small reducer change that carries escalation reasons through a block (visibility, not outcome). Red-run fetch widened beyond `--status failure` (timed_out / startup_failure / cancelled). Per-chunk→verdict rollup + single-producer rule defined (§3). Eval bundle **vendored** at `eval/` (was an out-of-repo reference). Claude-review fixes: chunk sizing made precise + `truncated` flag; empty-`evidence` distrust; Ollama error paths enumerated as escalate; wrapper/teardown exclusion set defined (§7); `ETIMEDOUT|ECONNREFUSED` demoted to advisory-only pending held-out validation; held-out bar raised ≥20→**≥40**; conformance test must reject unknown fields in both directions; rung ordered last pre-reduction. Q1–Q3 resolved.
**Related:** `docs/DESIGN.md` §The verdict schema / §The ladder law · `internal/verify/{floor,reviews,readiness}.go` (the rung patterns this copies) · eval provenance: **vendored at `eval/` in this directory** (dataset, prompts, schema, scorers, both raw model runs) · `docs/features/driver-merge-wiring/spec.md` (the consumer of these findings) · workbench-redesign consult (park the ship sink; build rungs outside ship) · gate-steward consult (this repo is the home; shape below is its answer).

> **Reviewers — focus areas:**
> - **§4 decision mapping** — the rung escalates only when attention is needed *beyond* the
>   mechanical fix-or-retry. `real-break → pass`-with-finding is the contentious row; the
>   rationale is spelled out. Argue with it.
> - **§5 evidence gatherer** — new `gh` reads + a chunker whose failure mode (cause scrolls
>   off the tail) is a *measured* miss source. The absence guardrail (§6) is the fail-closed
>   answer; check it has no hole.
> - **§7 floor signatures** — 100% precision (20/20 under the shipping table) was measured on
>   the same 51 rows that informed the signature set (mild circularity, named honestly). The
>   held-out hardening gate in §9 is the mitigation; is it sufficient before the rung's
>   findings steer a driver's retry?

## 1. Problem & hypothesis

A red CI check today is a `readiness` block with zero cause classification:
`check not green: CI (FAILURE)` — that's everything gate can say. The consumer
(a driver's merge tail, a judge, the operator reading `explain`) is left to
open the log itself, and the driver's only safe move is the expensive one:
treat every red check as a real break. Three distinct causes demand three
distinct next actions, and nothing distinguishes them:

- **flake** — transient; a plain retry fixes it. Treating it as a real break
  wastes a dispatch/fix cycle.
- **real-break** — the code/tests are wrong; fix the repo. Treating it as a
  flake wastes a retry and *masks* the break.
- **infra** — the CI environment is broken; retries and code-fixes both burn
  time until an environment owner acts.

**The bet:** a `ci-classify` verifier rung — deterministic signature floor
first, local-model advisory for the residual — classifies the failing log at
gate time and records the cause as verdict findings, so every downstream
consumer gets `why + evidence + suggested action` from state instead of
re-reading logs. Classification is *enrichment*: by the ladder law it can
never gate a merge (the local producer cannot block; the readiness block on
the red check stands regardless).

**Eval provenance (the gate this design already passed).** 51 real rows
(41 failed-step log chunks from 41 CI runs across 7 repos + 10 short lines),
labeled by hand, run 2026-07-06. Scored under **shipping semantics** — the
floor table §7 ships (demoted signatures out) and the §6 verifier applied to
every advisory row, a distrusted row counting as an *escalation*, not a
prediction. *(Correction history, on the record: v1 of the scorer accepted
advisory buckets without the verifier and kept a since-demoted signature —
the cycle-2 panel caught both. The old "92.2% / 96.1% of all rows" numbers
mixed escalations into the denominator; the honest metrics are two-axis,
the same coverage × strict-accuracy shape as the triage advisory gate.)*

| system | coverage (handled locally) | accuracy ON handled | escalated (unenriched, safe) |
|---|---|---|---|
| floor + 7B advisory | 41/51 = **80.4%** | 37/41 = **90.2%** | 10 (20%) |
| floor + 14B advisory | 42/51 = **82.4%** | 42/42 = **100%** | 9 (18%) |

Bar: coverage ≥ 60% AND on-handled accuracy ≥ 0.90 — both tiers clear it;
7B sits exactly at the accuracy bar, 14B clears it perfectly. (Bare
classifier, for contrast: 84.3% / 88.2% all-rows — NO-GO alone; the floor +
verifier are what make the system trustworthy, not the model.)

Floor precision 20/20 fired = 100% — measured *with* the §7
wrapper/teardown exclusion filter active (every fire sits in live error
context; the filter costs nothing on this set). The safety claim for
escalated rows is structural, not empirical: **an escalation is untrusted
and steers no consumer**, whatever bucket the model proposed. (Empirically,
most escalations happened to carry the right bucket with a non-verbatim
quote — 10/10 at 7B, 7/9 at 14B — but two 14B escalations were wrong-bucket,
and they are safe *only because* the verifier distrusted them. The verifier,
not model accuracy, is the guarantee.) Trusted-wrong rows — the only ones
that can mislead a consumer — are 4 at 7B (3 coverage-wrapper chunks → infra
+ 1 genuinely ambiguous test-timeout → flake, the single wasted-retry
direction, self-correcting) and **zero at 14B**. That zero is a *joint*
floor+verifier property, not a 14B-accuracy claim (panel caught the earlier
overstatement): 14B itself mislabels the two token-exchange rows
`real-break`, but the deterministic floor catches both (`failed to
authenticate`, `non human actor` → `infra`) before the model output is ever
trusted — so everything 14B *is left to decide*, it decides right. Credit the
floor for the model's blind spots, the verifier for the rest. 7B is the
default tier; 14B is a documented upside, not a dependency.

## 2. Non-goals (deliberate, with triggers)

- **No escalation routing / paging.** `infra` findings *want* a page; gate's
  posture is park-and-wait, and routing is a new escalation-transport plane
  owned by the workbench redesign (its RED-TEAM #9 / recon #6 hole). This rung
  records the signal; transport is out of scope. Trigger to revisit: the
  redesign lands a push-on-block seam.
- **No ship-driver surgery.** The original S2 plan mutated ship's
  `buildFailureTriageRequest` and emitted its dormant `ci-infra` escalation
  from inside the TS driver — exactly the frozen surface. Parked. The driver
  consumes this rung's verdict artifact through gate's exit-code/`explain`
  interface like everything else (driver-merge-wiring spec).
- **No error-aware chunker in v1.** Blind-tail chunking (per-step tail, 60
  lines / 8 KB) is what the GO numbers were measured on. A naive error-anchored
  window was *measured worse* (76.5% — wrapper lines trail into the window,
  fallback pulls setup noise). An error-aware selector is a follow-up with its
  own mini-eval; shipping it unmeasured would un-ground the eval.
- **No new model tier.** qwen2.5:7b, same as `reviews.go`. The 14B row above
  is recorded so the upgrade is one config change with a known payoff.

## 3. Shape: one new rung + one new evidence read

Follows the two patterns the plane already has: `Floor` (code producer,
deterministic, shells nothing here — signatures are stdlib `regexp` in-rung)
and `Reviews` (local-model producer, Ollama structured output, per-item calls,
escalate-on-distrust). Everything stdlib; state is the only channel.

```
readiness sees ≥1 red check-run on the head
  → evidence: fetch red-conclusion run logs (gh run list, filter to failure|timed_out|
     startup_failure|cancelled; gh run view <id> --log-failed), chunk per failed step, record KindEvidence
  → verify.CIClassify(st, run, logsEvidenceID, subject) (state.Artifact, error)
       1. deterministic signature floor over each chunk  → producer {code, ci-signature-floor}
       2. floor abstains → 7B advisory per chunk         → producer {local-model, qwen2.5:7b}
       3. nothing adjudicable → escalate (absence guardrail)
  → one Verdict artifact, parents = [logs evidence]; each Finding carries cause (Title) + verbatim line (Evidence)
```

The rung is **conditional**: it runs only when the rollup has a non-green
check-run. No red checks → no verdict from this source (Reduce's floor-presence
invariant is satisfied by `readiness`/`triage-floor` as today).

**Ordering:** `ci-classify` runs **last pre-reduction**, after `Floor` and
`Reviews`. It is the only rung that makes fresh network reads mid-run (the
others read already-recorded evidence); putting it last means a slow/failed
`gh` read stalls nothing ahead of it. Composition is monotone, so order
cannot change the outcome.

**Rollup (one verdict from many chunks):** worst-wins across all chunks of
all fetched runs — any chunk classified `infra` or `unclassifiable` →
`Decision: escalate`; otherwise `pass`. **Findings: one per failed run;
if a run's chunks disagree (step A flake, step B infra), one finding per
differing chunk, each annotating its step** — a mixed run is never flattened
to one misleading cause. Confidence = min across contributing chunk verdicts
(floor hits carry 1.0).

**Producer (one artifact, two paths):** the weakest class that contributed
to the decision. All chunks floor-decided → `{code, ci-signature-floor}`;
**any chunk for which the advisory was *invoked* — whether trusted or
distrusted-into-escalation → `{local-model, qwen2.5:7b}`** (advisory
involvement, not advisory trust, sets the class). This is the min-confidence
rule applied to the producer axis: the artifact claims no more authority than
its least-trusted contributor, and the ladder then constrains the whole
verdict accordingly.

**Error vs escalate boundary** (same contract as `Floor`/`Reviews`):
`st.Get` failure or a malformed evidence artifact → `error` (programmer
error — the rung was invoked wrong). Every *classification* failure — Ollama
unreachable, non-200, JSON parse failure, distrusted output — → `escalate`
verdict, never `error`: a red check plus no classifier is still a gateable
state, just an unenriched one.

## 4. Decision mapping (the policy, argued)

The rung's decision axis answers one question: *does this failure need
attention beyond the mechanical fix-or-retry the readiness block already
forces?* Flake and real-break have obvious next actions and an existing
enforcement (the block); infra and unclassifiable do not.

| classification | producer path | decision | finding `Title` |
|---|---|---|---|
| flake | floor or advisory | `pass` | `flake: <signature/cause> — likely passes on retry` |
| real-break | mostly advisory | `pass` | `real-break: <cause>` |
| infra | floor or advisory | `escalate` | `infra: <signature/cause> — environment owner must act` |
| unclassifiable / empty chunk / advisory distrusted | — | `escalate` | `unclassifiable: no adjudicable cause in excerpt` |

**Finding shape** (panel: the verbatim line had no defined home). Each finding
maps: `Title` = the row above; **`Evidence` = the verbatim log line that
justifies it** (the floor's matched line, or the advisory's verifier-checked
quote) — the current `verify.Finding` has no field for it, so §8 adds
`Finding.Evidence`; `Locus` = the `job / step` the chunk came from;
`Confidence` = advisory confidence (1.0 for floor hits). `Severity` is left
unset — a bucket is not a severity, and reusing it would collide with
`reviews.go`'s `severity→tier` mapping.

- `pass` here does **not** green anything — the readiness `block` on the same
  red check dominates in `Reduce`. It means "no judgment needed from me"; the
  finding is the payload.
- `real-break → pass` (not escalate): the readiness block already demands a
  code fix; escalating would park every ordinary red-CI PR for judgment and
  make the rung noise. The cause finding is provenance, not a request for
  attention. *(This deliberately diverges from the pre-consult S2 sketch,
  which routed real-break to ship's judgment path — gate's readiness block IS
  that surface.)* How a driver consumes a `block` + `real-break` finding
  (annotate, don't route to judgment) belongs to the driver-merge-wiring
  spec's advisory read-back, not here.
- `infra → escalate`: retry won't fix it, a code change won't fix it — an
  environment owner must act. **Composition honesty (panel P1, converged):**
  whenever this rung runs, `readiness` has already emitted a code `block` on
  the same red check, and block dominates — the run exits **`blocked` (1),
  not `parked_for_judgment` (2)**, and no `KindEscalation` artifact is minted
  from this escalate. The infra signal's canonical carrier is therefore the
  **recorded `ci-classify` verdict artifact itself** — recorded with
  `Source: "ci-classify"`, the stable handle `explain`, the driver, and the
  future paging plane query on, alongside `readiness` / `triage-floor` /
  `review-consolidation` (implementation: a package-level
  `const sourceCIClassify = "ci-classify"` so consumers compare against a
  constant, not a bare string). So the reduced outcome doesn't bury the reason, P1
  makes one small reducer change: **`Reduce` collects escalation whys *before*
  the block short-circuit and appends them to the blocked verdict's `Why`** —
  a plain `string` `join`, **no new `Verdict` fields** (so §8's conformance
  gate stays green), and `Findings` from the escalating rung are **not**
  promoted into the composed verdict (they already live on the `ci-classify`
  artifact its `Why` now names). Visibility only; decision, exit code, and the
  ladder are untouched. Pinned test: `block` + `escalate` composes to `blocked`
  with both reasons in `Why` and the composed `Findings` **nil** — `Reduce`
  never lifts a rung's `Findings` into the composed verdict; the escalating
  rung's findings live on its own recorded artifact.
- `unclassifiable → escalate` composes the same way under a block; in the
  no-block edge (a red check that turns green mid-run between the rollup read
  and classification), escalate parks the run — fail-closed either way.
- The advisory can emit only `pass`/`escalate` (`ClassLocal` — the reducer
  enforces it); the floor could block but never does — blocking is
  `readiness`'s job, and a classifier that blocks would gate merges on
  classification, the exact thing the eval bar was NOT set for.

## 5. Evidence: failed-run logs

New gatherer in `internal/evidence` (mechanism only, no judging):

- Resolve red runs: `gh run list --commit <headSHA> --json
  databaseId,workflowName,conclusion` — one authenticated read, no detailsUrl
  parsing — filtered in code to red conclusions: `failure`,
  `startup_failure`, `timed_out`, `cancelled`. *(Panel catch: `--status
  failure` alone misses timed-out / startup-failure / cancelled runs — prime
  flake/infra material — turning them into absent evidence.)* Dedup by run
  id; cap at the N=3 most recent with a finding-visible note when capped (no
  silent truncation).
- Per run: `gh run view <id> --log-failed`. Chunk: group lines by
  `job\tstep\t` prefix, strip the ISO-timestamp prefix, keep each failed
  step's **tail of 60 lines**, then cap the **whole per-run excerpt to its
  final 8 KB** — byte-faithful to the eval's extractor (vendored at
  `eval/build/main.go`; per-step line trim, per-run byte cap).
- Record one `KindEvidence` artifact: `{pr, runs: [{id, workflow, conclusion,
  chunks: [{step, text, truncated}]}]}` — `truncated: true` whenever the
  line-trim or byte-cap dropped content, so `explain` can surface it and the
  §6 guardrail has a hook. The rung reads this artifact only — never `gh`
  directly.
- An empty `--log-failed` (GitHub sometimes returns nothing for a red run)
  records the run with zero chunks; the rung escalates it (§6).

## 6. Absence guardrail (fail-closed, RED-TEAM #1 class)

Measured failure mode: service-container teardown and runner wrappers flood a
step's tail, scrolling the real cause out of the excerpt (3 of 44 candidate
chunks in the eval were undroppably unfair for exactly this reason). The
identical trap to "absence of signal reads as green":

- An **empty** `--log-failed` (GitHub sometimes returns nothing for a failed
  run) → `escalate`, finding `unclassifiable: no failed-step log`.
- A chunk where **neither floor nor advisory** produces a trusted verdict →
  `escalate`. Never default to flake: a flake misfire auto-retries and masks
  a real break.
- The advisory's trust check is the **verbatim-evidence verifier**: its
  `evidence` field must be a **non-empty** normalized substring of the chunk
  (empty or whitespace-only `evidence` is distrusted outright — the empty
  string is a substring of everything, so it would otherwise bypass the
  check), and `bucket` must be in-enum. Failed check → that chunk is
  *distrusted* → escalate. Its self-reported confidence is recorded but gates
  nothing (measured: 0/51 verdicts under 0.70 — confidently wrong at 0.90 on
  the misses; the verifier is the signal, confidence is not).
- **Every advisory transport/parse failure escalates, none error** (unlike
  `reviews.go`, which propagates them): Ollama unreachable, non-200 status,
  response-JSON decode failure, model-output unmarshal failure → finding
  `advisory unavailable/distrusted: <reason>` and `escalate`. A red check
  plus no classifier is still a gateable state, just an unenriched one. (The
  `error` return is reserved for the §3 programmer-error boundary.)

## 7. The deterministic signature floor

Stdlib `regexp` table in-rung, first match wins, most-specific-first — the
floor claims only `flake`/`infra` on unambiguous signatures; `real-break` is
the advisory's job (its strong suit). Seed set = the shipping table that
measured 20/20 (in the eval bundle's `floor-score.ps1`):

- **flake:** `the database system is (starting up|shutting down)` ·
  `connection to server on socket .*(failed|no such file)` · `EBUSY|resource
  busy or locked` · `EADDRINUSE|address already in use` · retry-passed
  phrasings.
- **infra:** `failed to authenticate` · `workflow initiated by non.?human
  actor` · `429 too many requests` · `could not resolve host` · `no space
  left on device` · runner-shutdown · `go version file .* does not exist` ·
  `/installation/token`.
- **Demoted to advisory-only** (panel catch): `ETIMEDOUT|ECONNREFUSED`.
  Both fire routinely inside *flaky integration tests* (a test that spins up
  a server and logs `ECONNREFUSED` when it isn't ready) — infra on the floor,
  flake in reality. They rejoin the floor only if the exclusion filter below
  is held-out validated with them included.

**The wrapper/teardown exclusion set (defined here, not deferred).** A line
matching any exclusion pattern can never fire a floor signature, and a
signature match is only trusted when the matching line is *not* inside a
teardown region. Patterns are **literal substrings**, not regexes — so a
sentinel's punctuation can never misfire as a metacharacter (panel: the
teardown sentinel had been written `Post job cleanup.`, whose `.` is regex
"any char"). First approximation (the held-out pass may narrow or grow it):
`ELIFECYCLE` · `ERR_PNPM_RECURSIVE` · `make: ***` · `Process completed with
exit code` · `Command failed with exit code` · `exit status ` (Go's
`exec.Cmd` relay) · `npm error code` · `waiting for other jobs` (npm/pnpm's
post-failure relay line, which sits *above* the real cause — excluded so a
signature word inside it can't shadow that cause) · `Cleaning up orphan
processes` · `Terminate orphan process` · `docker rm` · `docker network rm` ·
`Stop and remove container`. **Teardown region:** everything after the first
line containing the substring `Post job cleanup` in a step's chunk (the
sentinel GitHub Actions prints to start every post-step teardown). This is
the mechanism behind "a real-break that merely *logs* a network error
mid-trace can't flip to infra." **Toolchain-coverage is a named risk** (panel):
the seed relay patterns lean Node/pnpm; Go and shell runners relay
differently (`exit status N`, `make: *** [t] Error 2`). Since the 7 eval repos
span Rust/Go/Node, the held-out set must too — an audit of their toolchains
pre-fills the obvious gaps before P1. The eval's naive error-anchor experiment
(76.5%, worse than blind-tail) is the standing proof that untested
line-selection heuristics regress.

**Honest caveat:** the 100% precision was measured on the 51 rows that
informed the set — mild circularity. Hardening gate before the findings
steer any driver behavior (§9): held-out validation on **≥40 fresh failures**
the signature set has never seen (panel: 20 rows leaves a ~14% one-sided
false-positive bound; 40 tightens it to ~9%), precision bar 1.0 — a floor
that misfires even once gets its signature narrowed or dropped. **The gate is
two-sided** (panel): it also checks the exclusion set for *over*-exclusion —
a relay pattern broad enough to swallow a real cause line surfaces not as a
false positive but as the floor *abstaining* on a chunk it should have fired
on, so the pass criterion is both zero floor false-positives AND zero floor
abstentions traceable to an exclusion pattern eating a real cause. Fresh rows
accumulate from live portfolio failures post-P1; the rung ships findings-only
(nothing steers a driver) until this gate passes.

## 8. Schema graduation (rides along)

The verdict contract exists as this repo's Go type (behavioral source of
truth: the reducer) and, as of this design cycle, a first *versioned* JSON
expression (v0.2.0, structured producer). Per the steward consult it must not
live as an independent copy: this feature moves it into
`docs/schema/verdict-v0.3.0.json` **with a conformance test**.

**The bump to v0.3.0 is this feature's one contract change** (panel: the
verbatim evidence line had nowhere to live): add
`Evidence string \`json:"evidence,omitempty"\`` to `verify.Finding`, so a
finding carries the exact log line that justifies it — the substrate the §6
verifier checks and what `explain` shows for auditability. It is `omitempty`,
so `readiness` / `triage-floor` / `reviews` verdicts that don't set it are
unaffected (though `reviews.go` may adopt it — it currently discards the
evidence it extracts). This is a deliberate, versioned addition to a shared
type, not an overload of `Locus`, precisely so the contract stays legible.

The conformance test is a pinned Go test that marshals a fully-populated
`verify.Verdict` (every `Finding` field set, including `Evidence`) and
validates it against the schema file (stdlib-only validator handling just
`required`, `type`, and `additionalProperties` — the three JSON-Schema
keywords this schema uses; no full JSON-Schema-2020 implementation needed),
failing the build on drift **in either direction**: the
easy one (schema requires a field the Go type doesn't marshal) and the hard
one (the Go type grows a field the schema doesn't name) — so the validator
enforces `additionalProperties: false` on the marshaled output, top level and
every nested object, and the new `Evidence` field must appear in both the Go
struct and the schema or the test fails. Version bumps become deliberate acts.
In the **split-PR case** (§9), `Finding.Evidence` lands in Go with P1 while the
v0.3.0 schema file + conformance test land in the second PR with P2/P3 — the Go
type precedes its schema in that window, which is acceptable because the Go type
is the behavioral source of truth (the schema conforms *to it*).

## 9. Rollout

- **P1 — evidence + floor (deterministic core).** Gatherer, chunker, signature
  floor + exclusion filter, absence guardrail, wiring into the run flow behind
  the red-check condition, **and the §4 reducer visibility change** (escalation
  whys carried through a block; pinned test). Adds `Finding.Evidence` (§8; the
  floor's findings carry their matched line from day one) — the schema graduation
  + conformance test land in P3. Fully CI-testable: vendored chunk
  fixtures (pinned `-text` in `.gitattributes` — the verbatim verifier dies on
  CRLF rewrites), no network, no Ollama. The §7 held-out precision gate (≥40
  fresh rows, bar 1.0) accumulates from live failures post-P1 and gates any
  driver-steering use of the findings, not the P1 merge itself.
- **P2 — advisory rung.** Ollama call (same shape as `reviews.go`), verbatim
  verifier, distrust-→-escalate. Tests fake the HTTP boundary; the live model
  eval stays an offline gate (re-run on model/prompt change, results recorded
  in the eval bundle).
- **P3 — schema conformance (§8).**

P1–P3 are one PR if it stays reviewable (~≤700 changed lines target),
commits per phase; split at the P1/P2 seam if not. Standard panel
(@codex/@claude/@cursor) **plus an adversarial break-the-gate pass** — this
touches the verification plane; the panel catches diff-anchored holes, the
adversary hunts the contract-allowed fail-open (the §6 guardrail is the
attack surface).

## 10. Open questions — all three resolved by the v1 review panel

1. **N=3 recent red runs cap** — *resolved: keep.* A matrix failure is one run
   id; >3 independently failing workflows is itself an infra smell the
   escalation covers. The capped-note finding keeps it honest.
2. **Should `real-break → pass` carry the advisory's tier?** — *resolved: no,
   T0.* Tier is the grant-ceiling axis; classification asserts cause, not
   risk. Conflating them would let a classifier move approval requirements.
3. **Backtest subjects** (state=MERGED) — *resolved: skip.* The rung is
   conditional on a live non-green check; a merged PR has none. Enrichment is
   for action, not archaeology.
