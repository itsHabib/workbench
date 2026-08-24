# Baton — Architecture Vision

> **This file is canonical.** One goal runs through every document listed below:
> *the next agent starts where the last one stopped, and two agents do not
> silently reach different conclusions about the same thing.* Everything else —
> roles, chains, ownership, authorization — is machinery for that.
>
> | document | what it is | status |
> |---|---|---|
> | **`vision.md`** (this file) | the architecture and the argument for it | **canonical** |
> | [`p0-findings.md`](p0-findings.md) | every number, and how it was measured | evidence; cite it rather than restating figures |
> | [`where-this-stands.md`](where-this-stands.md) | proven vs claimed, and the next step | synthesis; **folds into §0 here once #245 is locked** |
> | [`spec.md`](spec.md) | the earlier TDD draft | **superseded** by this file; kept for its review history |
> | `drive:docs/features/discharge/spec.md` | conclusions as owned data, via two hooks | **the P1 slice that ships first** — see below |
>
> **Status, 2026-08-23.** `contracts/org` is on `main`: the spine, the contract
> law, and the ownership fold, with all 86 reachable states walked. The §7 P0
> gate has been evaluated and **does not currently pass** — false positives are
> 27.6% against a <20% bar, a role-day is affordable to ~25 roles against a
> target of 75, and the collision count is unmeasured. Do not start P2 through
> P7.
>
> **Next step: `drive` PR #46 (discharge), phases d0 and d1.** It is the only
> way to obtain the collision count, which is unmeasurable retrospectively
> because holding was never recorded. It is also the same two hooks (§3.3) this
> design needs regardless, so nothing built there is wasted either way.
>
> **How discharge and this design relate.** They want opposite things from the
> same situation, and that is the point. Baton asks *who owns this work and may
> act on it* and answers with a compare-and-swap that **refuses** the second
> writer — acting is exclusive. Discharge asks *what was concluded, and where do
> we disagree* and **records both**, rendering the disagreement — concluding is
> not exclusive. Ownership without conclusions is a lock with nothing behind it;
> conclusions without ownership is a wiki.
>
> ---
>
> ### The target, stated by the operator
>
> Not a 75-role fleet. **A small number of role leads, each owning an area of
> the operator's own work, each trusted with a set of dossier tasks, each
> reporting back.** `lead:agentic-development` manages the portfolio's own
> tooling; `lead:rooms` manages rooms. Two to five of them, not seventy-five.
>
> A role lead is real when four things are true, and each maps to something
> that already exists or is in flight:
>
> | what makes it a *lead* | mechanism | status |
> |---|---|---|
> | it outlives any session | `contracts/org` chain + fold | **on `main`** |
> | it holds work, and you can see what | `assign` over `dossier:proj/phase/task` URIs | contract done; binding not built |
> | a fresh session inherits its judgment | discharge — `SessionStart` read, `Stop` write | `drive` PR #46 |
> | you can trust it unattended | charter pins effect classes; every act is in the chain | designed, not enforced |
>
> **What a lead injects at session start** is the whole product in one screen:
> its charter, the tasks it holds and their state, what the last incarnation
> concluded, what it left open, and when it is next due. A session that starts
> with that is productive in its first sentence rather than its fortieth.
>
> **Why this reframes the §7 P0 gate.** That gate asks whether to invest in an
> enterprise substrate — collisions ≥ 10, a role-day affordable at 75. Those are
> the right questions for a product sold to strangers and the wrong ones for
> tooling whose only user is its builder. At two to five leads the role-day
> arithmetic is not close to binding (see [`p0-findings.md`](p0-findings.md) §4:
> ~25 roles is affordable, and this needs five), and the collision count is
> something to *watch* rather than a threshold to clear before starting. **Ship
> the increment that helps you build the next one; measure because the numbers
> keep turning out different, not to earn permission.**

## 1. The system in one paragraph

Baton is a control plane that gives every durable unit of organizational work an owner that outlives the process doing it, and gives every external effect a recorded intent that outlives the process that issued it. A **role** — lead, project lead, IC, maintainer — is a row of data with an append-only journal; an **incarnation** is a disposable session on some host that reads the journal's tip, takes the tip, acts, and writes back. To act you must append; to append you must present the tip you read. That one rule produces continuity (starting is folding), mutual exclusion (two incarnations cannot both hold the tip), handover (a supervisor appends a takeover), and revocation (credentials are minted against a chain position, so displacing an incarnation kills its authority everywhere with no message sent). Underneath it, a single broker is the only path from an agent to the outside world: it holds the credentials the agent never sees, records the *intent* of every effect before the wire and the *outcome* after, and refuses any effect that has not declared how a replacement resolves it if the issuer dies mid-flight. Everything else — the fleet, the boards, the protocol checker, the merge gate — is a client, a reader, or an adapter. The system is for one thing: **you can kill any agent at any moment, and nothing is lost and nothing is duplicated.**

---

## 2. The thesis

Two claims carry the weight. Stated so they can be attacked.

**T1 — Ownership and continuity are the same fact, and the fact is a compare-and-swap.**

Four propositions, jointly sufficient:

1. Role state lives outside every process.
2. Every write is CAS-guarded against a token the writer had to read first.
3. Liveness is derived from write recency against a deadline the writer itself declared — never from self-report.
4. Authority is bound to that same token, so authority expires when ownership moves.

From these: an agent that dies loses nothing (state is external); two agents cannot both act as a role (CAS); a hung agent that never exits is correctly seen as dead (it stopped appending); and a displaced agent's credentials are dead the instant the token advances, including on hosts you cannot reach.

**What T1 explicitly does not claim.** The hash chain is not doing this work. A monotone integer with CAS gives identical ownership semantics. This matters because a critique landed hard here and the design changed in response: **the ownership token and the audit ledger have opposite cost profiles** — the token wants to be small, cheap, and disposable; the ledger wants to be permanent, canonically encoded, and tamper-evident — and fusing them makes the continuity path pay the audit path's entire bill, including a GDPR erasure problem with no customer asking for the audit yet.

The resolution is a split, and it is the single most consequential structural decision in this document:

> **The spine is chained; the body is a blob.** Every record's spine — `{v, scheme, seq, prev, tenant, role, kind, kind_class, incarnation, fence, at, body_digest, body_class, refs[], next_due}` — is fixed-shape, free of prose, canonically encoded, hash-chained, and retained forever. The *body* — the distilled prose — is a content-addressed blob stored separately, referenced by digest, classified, and **erasable**: deleting a blob leaves a tombstone and the chain still verifies.

That split buys three things at once. Erasure and retention become possible without breaking verification. Record classification and redaction get a place to live (a `body_class` field and one store to encrypt). And the audit requirement stops setting the schedule for the continuity requirement — you can ship ownership in week two and anchor keys in month six.

**T2 — An effect is not done until the ledger says the world saw it, and no intent may be issued that has not declared its own recovery.**

The agent that says "done" is making a claim. A committed effect is a fact. Completion is derived from facts. And the failure that matters — an agent issued an external effect and died before recording the outcome — is bounded by refusing the effect at issue time unless it names how a stranger resolves it: replay an idempotency key, run a probe, run a compensation, or escalate to a human. `effect_recovery_undeclared` is a refusal, not a warning.

The argument for T2 is not enterprise payments. It is that the operator's own platform generates the crash-mid-effect case on a schedule, for free, today: a usage-limited session does not exit, it holds the tick; derived liveness correctly marks it dead; a supervisor takes over; and then the original wakes up with an effect in flight. That is observed, not imagined.

**The keystone that joins them:** T1 serializes the role, so a role has **at most one outstanding unknown effect**. Recovery is therefore a fixed procedure over a single record, not a search. Without T1, T2 is a distributed-systems research project. With it, T2 is one probe.

---

## 3. The architecture

### 3.1 Six planes, one law of composition

Planes compose through **typed artifacts and exit codes**, never call stacks. The one forbidden import stands: no plane imports another plane's decision logic. Generalized to services: an adapter consumes verdicts and may never import policy.

| Plane | Answers | Owns | Home today |
|---|---|---|---|
| **Continuity** | who is this role, who holds it, is it alive | chains, folds, takeover, derived liveness | `contracts/org` + `drive` |
| **Authority** | may this be done, by whom, until when, how much | grants, attenuation, fences, ceilings | `contracts/authority` + `gate`'s grant, extracted |
| **Effect** | what did the world actually see | intent / attempt / outcome, classes, probes, reconciliation | `custody`, extended |
| **Execution** | where and how did code run | work specs, placement, run events, terminal results | `runway` + `rooms` |
| **Work** | what is this about | work URIs, subject digests, derived and attested completion | URI scheme; `dossier` is one backend |
| **Surface** | how humans and other systems reach in | API, projections, console, escalation, notification | new (`batond`) + `console`/`escalate`/`flare` |

Cross-cutting: **evidence** (canonical encoding, spine chain, seals, anchors) and **levels** (§5).

### 3.2 Where the write path runs — and the contradiction that had to be resolved

One framing put the chain behind a service (`Append(tenant, role, expectedTip, record)`) and then, to answer the "you are a new single point of failure in our dev loop" objection, promised the plane degrades rather than blocks with a local append buffer. Those two statements cannot both hold: if appends buffer locally during a partition, two incarnations both believe they hold the tip, and serialized ownership is precisely the property you cannot degrade.

**Decision.** The write path is a **local library append under a local lock** — `contracts/org` called in-process, no RPC, no daemon — against the role's **home**. A role has exactly one home: a state directory on a laptop, or a Postgres row in a deployment. The home is the serialization domain. If the home is unreachable, you cannot append, therefore you cannot act, and that is correct.

The law that resolves the contradiction in one line:

> **The lock never degrades; the policy always can.**

Ownership blocks. Refusals, verdicts, protocol conformance, effect classification, and evidence anchoring all degrade by level (§5). On a laptop the home is a file, so there is no service to be down. In a deployment the home is the database the platform team already runs and already considers available. `batond` is a **reader, a reconciler, and a remote home** — never a hop on a local write path. This also honors the operator's own switchboard result: no residency without a comparator win. The only resident processes in the design are the broker (comparator win: it *is* the egress path) and the read API (comparator win: it serves other machines).

### 3.3 The continuity plane

**Record spine** as in §2. **Kind classes** are the schema-evolution answer, and the failure they prevent is specific: an old reader that silently skips an unknown `takeover` concludes it still holds the role — two holders, produced by routine version skew, with every hash valid.

- `kind_class: structural` — `charter`, `attach`, `takeover`, `release`, `retire`, `recharter`, `split`, `merge`, `abandon`, `revoke`, `delegate`, `assign`, `unassign`, `intent-ref`, `escalation`, `resolution`, `seal`, `annul`. An unknown structural kind makes the fold **refuse** `scheme_unsupported` with an upgrade remedy.
- `kind_class: advisory` — `checkpoint`, `mark`, `note`, `report`, `message`. Unknown advisory kinds are preserved verbatim and skipped.

Genesis carries a monotone `min_reader`. One CI fixture: an old reader against a new chain must refuse, not skip.

**The fold is bounded, and the goal is not writable.** A three-day IC chain has thousands of records; the fold's entire purpose is to fit in a fresh context window, and no framing bounded it. Worse, if each incarnation rewrites `goal`, a role's purpose is subject to a dozen rounds of lossy re-summarization with nothing to compare against.

- `goal`, `scope`, `tier`, permitted effect classes, budget, cadence, and retirement condition live in the **charter**, are **inherited** by the fold, and are changeable only by a `recharter` authored under the parent role's grant. Attempting to write them from a checkpoint is `charter_immutable`.
- Fold state is a fixed-size reducer: inherited charter, current holder, last `doing`, last K `decided`, `open[]` as a set with explicit close, `next[]`, assignments, open intents, liveness, seal reference.
- Every K checkpoints the home appends a **`seal`** — the folded state, content-addressed. A cold read is `last seal + tail`. The seal also bounds erasure: bodies behind a seal are deletable because the seal preserves what the fold made of them.

**The checkpoint is written by the host, not by the agent.** This is the answer to the hardest evidence in the whole review. `~/.claude/session-claims/claims.jsonl` contains two records, both from one day two weeks ago, and nothing since — an ownership ledger with claim and link events, built on this machine, abandoned after a day, alongside `/claim`, `/release`, `/roster`. It died because it required a verb. And 51 authored skills with 23 ever invoked says the same thing louder.

Nothing survives that requires the agent to remember anything, and a "required end-of-turn tool call" is a verb wearing a costume. So:

1. `SessionStart` hook → **attach**: read the tip, append `attach`, and *inject the folded context into the session*. The agent gets its memory back for free. If the hook fails, the agent starts blind — visible in the first sentence, not silently wrong.
2. Every N tool calls and at `PreCompact` → the host writes a **mark** containing the mechanical facts it can observe without the model's cooperation: tool calls, files touched, effect ids issued, commands run.
3. A **distiller** — a cheap local or small-model call, not the working agent — folds the transcript tail plus the marks into a body of the record shape. The least reliable component at its least reliable moment is removed from the loop.
4. `Stop` → **release**, or, on abnormal termination, nothing at all — and derived liveness catches it.

This also disposes of "a soft-fail hook cannot enforce a checkpoint." Correct, and it no longer needs to: the marks are mechanical, the distillation is asynchronous and retryable from the transcript, and the tip advances on `attach`, which is the only moment enforcement actually matters.

**The resume canary is a first-class component, because distillation quality is the thesis's only untested load-bearing assumption.** Periodically, fork a fresh incarnation from the chain alone, ask it to state the goal and the next action, and score it against what the live incarnation is doing. That produces a per-role **resume-fidelity** number that degrades visibly before it fails catastrophically. It is the only instrument that tells you whether the chain is carrying thought or only commitment.

**The human is a role.** An interactive session attaches through the same hook as `human:<name>` and takes the tip like anything else. Without this, "two sessions held PROJ-412 on Tuesday" is false whenever one of them is the operator, which today is most of the time.

**Reorg is four records.** `split` and `merge` are terminal for the source and genesis-referencing for the target, so lineage survives and a merged role's fold includes its source's folded tail; `recharter` retargets scope; `abandon` names every open item. A role with open assignments cannot `retire`. The property worth claiming: **when roles are data, being wrong about org shape costs an append.**

**Time is owned by the home.** The home stamps `at`. Agent-supplied timestamps are advisory and may never be the basis of a refusal. One law, house-shaped: *no refusal may depend on a clock the refusing party does not own.*

**The trust boundary, written down.** The chain is a record of **claims** by an incarnation; only broker outcomes are **facts**. A hash-chained journal makes a prompt-injected checkpoint *durable* and inherited by every future incarnation — the design makes one thing worse than the status quo, and pretending otherwise fails a security review. Two cheap structural mitigations: `refs[]` carry `trust ∈ {operator, internal, external}` so a decision derived from untrusted content is visible in the fold and the audit; and permitted effect classes are pinned in the charter, so an injected incarnation is confined to what the role could do anyway. Blast radius bounded, not zero. Say it that way.

### 3.4 The authority plane

A grant: `{subject_role, incarnation, fence, actions[], resource_predicates, tier_ceiling, window, depth, spend_ceiling, concurrency_ceiling, cycle_ceiling, audience, parent}`.

Ten ordered checks, first failure wins. A child may shrink actions, shrink the window, decrement depth, lower every ceiling. Subject and artifacts are equality-locked. **The audience of a grant is the only key that may mint the next one.** Signature validity alone is insufficient — the monotonicity predicate is the law. This is `hack-mandate`, promoted verbatim.

**The fence** is the grant's chain position at mint. Verifiers keep a per-role high-water mark; a lower fence is `fence_regression`. There is deliberately **no revoke endpoint**: appending `takeover` or `revoke` advances the mark, and every credential minted against that role dies in one local write with nothing sent. Authority is computed, not stored; the cap ledger is a derived cache.

**Scoping the fence claim honestly.** A fence is only enforceable by a verifier that keeps state. `custody serve` and a local `gate` do. A CI check on an ephemeral runner does not — its high-water mark is always zero and every displaced grant verifies fine. So: **fences are enforced by long-lived verifiers only. The CI check enforces scope, tier, and time and explicitly does not claim fence enforcement.** Written into gate's docs, not left to be assumed exactly where it fails.

**Two roots, both outside the agentic system, both named.** At half a human-touch per role per day the human cannot mint routinely, so leads mint — which reintroduces a broad long-lived root unless it is pinned. It is pinned two ways. *Root issuance is operator-only; attenuation is delegable* — that is the one line reconciling the new topology with the standing rule that agents never mint. And the root key is keychain- or hardware-held, signs only lead charter-plus-grant pairs on a short window, and requires a weekly human refresh, which makes its absence a dead-man's switch on the whole fleet.

**Ceilings that are not about security.** `cycle_ceiling` encodes the operator's hardest-won process rule — two fix-rounds against the review panel, and the uncapped loop is the panel *before* the gate — as an attenuating grant dimension rather than as prose an agent may or may not read. A looping panel becomes a refusal with a remedy instead of a silently burned budget. `spend_ceiling` and `concurrency_ceiling` do the same for cost, once model calls run through the broker (§3.5).

### 3.5 The effect plane — the part that does not exist yet

**It lives inside custody, and it requires two charter reversals, not three small changes.** Today custody writes its request line *after* the upstream returns, once, and a log-write failure is deliberately not fail-closed on the grounds that the log is tuning evidence rather than a control. The effect plane requires the exact opposite: **intent fsynced before the wire, fail-closed.** A crash between the wire and the log today leaves no record at all — not an intent without an outcome, nothing. Naming this as a reversal rather than an addition is the difference between a two-day estimate and a correct one.

**Three records**, in their own log, same envelope discipline:

```
intent  {effect_id, tenant, role, fence, work_ref, class, upstream, action,
         request_digest, idem_key?, probe?, compensate?, deadline}   ← before the wire, fail-closed
attempt {effect_id, n, at, transport}                                 ← before each retry
outcome {effect_id, n, status ∈ committed|absent|unknown, evidence_digest}
```

**The missing field that turns three repos into one system.** custody's log record today carries `key`, `grant_id`, `grant_digest`, `rule_fired`, `verdict` — and no role, no incarnation, no work id. A replacement incarnation folds its role chain and has no join column to discover its predecessor's open effects. So: **every request through the broker must carry `(role, fence, work_ref, effect_id)`, and custody refuses `effect_unstamped` if it does not.** That single stamp is the seam. It is a day of work and it is the difference between four adjacent programs and one system.

**`effect_id` is content-addressed** over what the effect is *about* — tenant, role, work subject, class, canonical request digest. A changed request forks a new effect rather than mutating one; a retry of the same intent is the same id. That is `hack-obligation`'s identity rule, and it is the exactly-once hook. Cost, stated: hashing a request body forces the proxy to buffer where it currently streams.

**Four classes, and the recovery is declared at intent time or the intent is refused.**

| Class | Meaning | Recovery |
|---|---|---|
| **R** | upstream honors an idempotency key | replay the key |
| **Q** | no key, but a query distinguishes committed from absent | run the declared **probe**, evaluate the declared predicate |
| **C** | reversible by a declared compensating effect | compensate, then retry |
| **U** | at-most-once, irreversible and unobservable | never automatic — escalate with evidence |

Class Q is the workhorse: most GitHub, ticket, and document effects are Q, and the probe is often exactly what the seven-cron-loops comparison point does statelessly every tick ("re-read the open PRs"). The difference is that here it is *declared per effect and run only on the open one*, so it also covers the cases where re-reading everything does not answer the question. An inconclusive Q probe promotes the effect to U and escalates. The ladder never guesses.

**`unknown` is a status on the effect record, not a new value in `contracts/execution`'s terminal enum.** Adding a value to an existing enum falls silently through every consumer's switch, including ship's independent TypeScript emitter and its goldens. A run can succeed while its effect is unknown — the process exited 0 and the HTTP response was lost — and the two planes must be allowed to disagree.

**Model calls are effects.** The largest pipe in the system — prompt out, completion in — bypassed every plane in every framing. Routing the model provider as one more custody upstream with a manifest costs almost nothing and buys four things at once: token spend metered against the grant's `spend_ceiling`, so `spend-audit` becomes a derived view rather than a scraper; a fence-bound model credential minted at attach, so a displaced incarnation cannot think, let alone act; `prompt_digest` plus classification tags, so "which agent saw PII" has an answer; and a real `concurrency_ceiling`, so the fleet degrades by queueing rather than by rate-limit failure. Harness seat and concurrency limits, not architecture, are what actually cap fleet size — the comparison point runs seven loops on a subscription and that number is not incidental.

**The reconciler is a maintainer role, not a daemon** — thin chain, stateless tick, re-derives from the world the status of every intent past its deadline. Which raises the thing every framing left unowned.

**There is a cron at the bottom of this design.** A role only acts when an incarnation starts, and something must start it. Derived liveness correctly reports that nobody is alive when both leads are stalled, and then nobody is left to append the takeover. The mutual-restart topology does not close that loop; a timer does. So: **exactly one external stateless timer**, doing nothing but `POST /tick`, monitored by a dead-man's-switch service outside the system. It is the availability floor for takeover and reconciliation, it is drawn on the diagram, and it is the only stateful thing outside the chains.

### 3.6 The work plane, and the one-field decision

**Work references are URIs with a scheme**: `github:acme/api#88`, `jira:PROJ-412`, `pagerduty:INC-9`, `slack:C04…/p1699…`, `dossier:proj/phase/task`. Committed now, because it is one field and it decides everything downstream: with a scheme, the substrate can attach to work an organization already has and `dossier` becomes one backend among several; with `project/phase/task`, the system only ever works for the operator's own tracker. In an organization the work item is usually not a PR, and "GitHub is the source of truth" is a valid architecture only when it is.

An `assign` captures `subject_digest` — a content digest of the work item at assignment time — so a renamed or rewritten ticket produces `subject_drift` rather than silent absorption.

**Completion has two honest kinds.** `effect-derived` — a committed effect satisfies the item's declared predicate. `attested{by, grant, at}` — a named principal accepted it. Derived completion is the better one and it is the right default for merges and deploys, but it does not work for a design doc, a triage call, a runbook, or a thread, which is most of what leads and PMs do. Stretching derivation over judgment work would make the strongest claim contradict the strongest requirement. Attestation is a claim with an owner and a grant behind it; that is enough.

### 3.7 The surface: `batond`

Small, resource-oriented, tenant-rooted. The CLI is a client of the library locally and of this API remotely, with the same verbs, so nothing has two implementations.

```
POST /v1/t/{t}/roles/{role}/records      If-Match: <seq>.<tip>   409 → tip_stale
GET  /v1/t/{t}/roles/{role}              folded projection; every derived field labeled `derived`
GET  /v1/t/{t}/roles?held_by=&stalled=   roster + derived liveness
POST /v1/t/{t}/effects/intents           idempotent on effect_id
POST /v1/t/{t}/effects/{id}/outcome
GET  /v1/t/{t}/effects?status=unknown    the recovery queue
POST /v1/t/{t}/grants                    attenuating mint, parent = caller's grant. No revoke route.
GET  /v1/t/{t}/work/{uri}                which roles and chains touch this item
GET  /v1/t/{t}/report?scope=&since=      the shadow report: what would have been refused
GET  /v1/t/{t}/events?from=              SSE, replayable — audit export and integration surface in one
POST /v1/t/{t}/escalations/{id}/resolve
PUT|GET|DELETE /v1/t/{t}/blobs/{digest}  DELETE leaves a tombstone; the chain still verifies
```

`If-Match` on the tip is the entire concurrency story — no lease service, no bespoke protocol, one sentence to a platform team.

One correction to "if it can be derived, don't record it." The law is right for the chain and hostile to an API: consumers want fields, not a fold to reimplement, and a reducer that changes makes historical verdicts unreproducible. So the law is scoped and paired. **The chain records the minimum. The read API serves a fully materialized projection with every derived field labeled `derived` and stamped with the tip digest and the reducer version it was computed from.** A stale or differently-versioned view is *known* stale, never silently wrong, and an audit can say which code produced a verdict.

**Identity: three subjects.** Principal (a human, via OIDC), role (the durable office, the addressable identity), incarnation (a session on a host, with a keypair minted at attach and the public half bound into the `attach` record). No long-lived tokens anywhere on an agent host. The comparison point's two secrets sitting on a VM, re-authenticated by a human over SSH every couple of days, is the precise anti-pattern; Baton keeps that setup's disposability — nothing on the box is worth backing up — and takes the box to zero secrets.

**Tenancy** is the root of every namespace and the partition key of every chain, log, blob, and anchor, enforced at the storage layer. Schema-per-tenant on Postgres is correct for the first fifty customers. And to be blunt about a line that would not survive ninety seconds with a platform engineer: fold purity does *not* make tenancy free. Tenancy costs auth, isolation tests, per-tenant keys, backup granularity, deletion, and residency. It is priced as a phase (§7 P6), not as a footnote.

### 3.8 The seams, exactly

| Seam | Typed artifact | Writer | Reader |
|---|---|---|---|
| Continuity → Authority | `{role, incarnation, fence}` embedded in every grant; verifier keeps a per-role high-water mark | attach / takeover | custody, local gate. **Not CI.** |
| Continuity → Work | `assign{work_uri, subject_digest}`; one open assign per URI across all chains | the holder | fold-time law; work adapters |
| Continuity → Effect | `(role, fence, work_ref, effect_id)` stamped on every brokered request | the incarnation | custody, `effect_unstamped` if absent |
| Authority → Execution | `Secret{Name, Ref}` — a `custody:` handle, never a value; structured argv over logical roots, never a shell line | delegating role / runway | placement backends |
| Effect → Evidence | `intent`/`attempt`/`outcome` + authority receipt, joined on `effect_id` | custody | reconciler, audit, console |
| Execution → Work | run result + artifact links + PR/ticket URI | runway / ship-as-role | work plane, provenance |
| Any → Human | `escalation{question, options[], evidence_refs[], default, deadline}` → `resolution`. **An escalation with no default and no deadline is refused.** | any role | escalate/flare out; the answer appends to the *role*, not the session |
| Everything → Protocol | `parleyc`-emitted table of legal `(state, message-type)` pairs, checked into the repo | Haskell, build time | `contracts/org.Admissible`, Go, run time |
| Host → Continuity | SessionStart→attach, N-tool-calls/PreCompact→mark, Stop→release; distiller writes bodies | hooks | the `org` library |

The escalation seam deserves one more line: the human answers the **role**. The original incarnation is almost certainly dead by then — at half a touch per role per day the wait is measured in days — and a reply delivered to a session evaporates with it. As a durable append, human latency is free.

---

### 3.9 The organizing idea: one state machine, and every plane is a guard

The six planes above are a decomposition, not yet a reason the pieces belong
together. This is that reason, and it is the frame the rest of the document
should be read through.

**The platform is a state machine over work ownership in which illegal states
are unrepresentable, and every plane exists to guard one of its transitions.**

```
Chartered ──attach──▶ Held(incarnation) ──retire──▶ Retired
                            │      ▲
                   claim(w) │      │ yield · complete · abandon
                            ▼      │
                     Active(incarnation, w)
                            │
                 every effect carries (role, incarnation, fence, w)
```

Four laws, each a guard, each owned by exactly one plane. They are stated as
things the system *cannot represent*, not things an agent should remember.

**L1 — An incarnation cannot exist without owned work.** `attach` requires a
charter whose scope names at least one work reference, and `prev == tip`.
There is no unscoped agent. A lead's scope is a scope, a maintainer's is an
`area`, an IC's is a task; the `work.kind` vocabulary (§3.6) is what lets one
rule cover all three. *Guarded by Continuity.*

**L2 — An incarnation may hold many work items and may act on exactly one.**
`claim(w)` requires `w` to be held, no other claim active, and no open intent.
`act` requires the state to be `Active`. This is the law with the largest
practical payoff and it was missing from every framing: because exactly one
claim is active, **the `work_ref` on an effect stamp is derived from state
rather than supplied by the caller.** An agent structurally cannot attribute
an effect to the wrong work item — a misuse class deleted rather than
documented. It also makes context-switching an event, so the chain records
what the incarnation was actually doing at each moment instead of that it held
five things. *Guarded by Continuity; consumed by Effect.*

**L3 — Stopping must have an effect on the work.** An incarnation cannot end
cleanly without a terminal record — `yield`, `complete`, `abandon`, or
`handoff`. Abnormal termination leaves the claim dangling, the fold reports
`dangling_claim`, and **the next incarnation must resolve it before it may
claim anything.** Silent disappearance is not a representable outcome. This is
the same shape as the open-intent rule and for the same reason: an
unresolved obligation blocks progress rather than being inherited invisibly.
*Guarded by Continuity; resolved with Effect.*

**L4 — An action that propagates state must produce a receipt.** Every effect
requires `Active`, a grant covering its class, and a declared recovery, and
produces intent / attempt / outcome. No receipt, no effect. *Guarded by
Authority and Effect.*

**Why this is the organizing idea rather than a detail.** It answers the
question the plane table cannot: why these six and not four or nine. Each
plane is the guard on a transition that would otherwise be enforced by prose,
and a plane with no transition to guard does not belong. It also explains the
composition law — a guard needs the artifact the previous transition produced,
never the previous plane's code — which is why contracts and not call stacks
falls out of the model rather than being imposed on it.

**Where the sum types go.** Go cannot express a state machine whose invalid
combinations are unconstructible, and pretending otherwise produces a
hand-maintained switch statement that drifts from the document. The move is
the one §6 already makes for protocols: **define the lifecycle where sum types
are real, prove the transition relation total and the illegal states
unreachable, emit the transition table as a checked-in artifact, and have Go
interpret it.** Same mechanism as `parleyc`, second use — which is the
evidence the mechanism was right. Nobody installs GHC to run the product, and
the state machine has machine-verified provenance instead of a reviewer's
recollection.

**The friction this creates, and its resolution.** "Cannot act without a
claim" is correct for an IC under `enforce` and hostile to an operator opening
a terminal to poke at something. Levels resolve it exactly as they resolve
every other instance of this tension: at `observe` and `advise` the claim is
auto-created from context as a free-text work reference and the absence of one
is recorded rather than refused; at `enforce` it is required. Same evaluation,
different handling — the rule levels already obey (§5).

## 4. Component map

### Load-bearing

| Component | Role in the composed system | Change required |
|---|---|---|
| **custody** | The broker: policy enforcement point, credential boundary, effect ledger, idempotency fence, audit source of truth. One component because the agent cannot exceed its grant when it possesses no credential to exceed it with — mechanism, not policy. | Large. Intent-before-wire fail-closed (a charter reversal), `(role, fence, work_ref, effect_id)` stamp with refusal, classes and probes in the manifest, model provider as an upstream. **The highest-leverage single piece of work in the portfolio.** |
| **contracts/org** | The continuity contract: spine types, embedded schema, validation, bounded pure fold, refusal codes. Leaf package, no decision logic. | Build new (§4.4). |
| **contracts/authority** | Grant type with incarnation, fence, ceilings, and the monotonicity predicate. Extracted from gate so it is not one tool's private notion. | Extract + extend. |
| **drive** | The org runtime and the fleet's flagship *client*. | An **inversion**, not an addition. Its ledger is real and tested — attach/link/release, liveness reducer, roster, tree, torn-write tests — but it is keyed on `launch_id`, incarnation-first. The thesis is role-first with incarnations disposable. Plan it as a rewrite of the key. |
| **runway + contracts/execution** | Execution plane. Portable work spec, placed request, ordered events, one terminal result, pure reducer, provider-neutral by law, reconcile verb. Correct as designed. | Add reconciliation against the effect ledger. Do **not** add `unknown` to its terminal enum. |
| **rooms** | Isolation and egress control. See the honest scoping below. | None to the code; a lot to how it is positioned. |
| **hooks** | The host adapter, and the entire adoption story. Mechanism only. | Wire SessionStart/PreCompact/Stop; add the distiller. |
| **gate** | Demoted from flagship to **one adapter**: the merge-class verifier at `attest`. Its transferable value is the grant, which moves out. | Extract the grant; arm it on one repo. |
| **escalate + flare** | Escalation transport. | Append-to-role semantics; enforce default+deadline. |
| **console** | The human surface: roster, liveness, escalations, level dials, evidence bundles. | Add the two headline numbers (§4.5). |
| **channel** | Message transport, deliberately untyped. The *fact* of a message is the pair of chain records; the bus line is not authoritative. | None. |
| **dossier** | One work-URI scheme and a local work backend. Not the core. | Accept the URI decision; expose `subject_digest`. |
| **ship** | Becomes a **role** — a maintainer whose work items happen to be PRs — not a parallel engine. Two schedulers is how a system becomes a pile. | Reframe; its driver state machine survives intact. |

**The containment scoping, stated plainly.** "A displaced incarnation's credentials die everywhere" is true only where the broker sits in the path. On a laptop the agent has bash, a network, an already-authorized `gh` keychain entry, and any MCP server it likes; custody is a boundary only for traffic that chooses to traverse it. What makes custody a *mechanism* rather than a *policy* is egress control, which means the sandbox, which means rooms, which means KVM, which means Linux — which is not the Mac all of this lives on. Therefore: **containment is a level, not a baseline.** At `observe` and `advise`, custody buys audit, attribution, and convenience. At `enforce` and above, the agent must run where its only network path is the broker. The operator's personal fleet will run at `advise` with audit-only containment, and the sentence said to a security reviewer is made about the rooms-hosted configuration only. Rooms is the reference provider behind runway's contract, never a customer requirement — a platform team that hears "you must run Firecracker" says "we have Kubernetes" and the conversation is over.

### Evidence (results the design rests on; no code ships)

- **switchboard** — the falsification that shaped the whole design. Crash recovery does not distinguish a resident actor from reload-from-disk; residency uniquely supplies **serialized ownership**. Cite it whenever someone proposes a daemon.
- **hack-branchroom** — the fold. Parent-digest tip, duplicate and gap in one condition, and the refusal of a *perfectly correlated* delayed terminal from a parent epoch, which is exactly the fence check. Ports into `contracts/org/reduce.go`.
- **hack-mandate** — the attenuation law, ports into `contracts/authority`.
- **hack-obligation** — content-addressed identity (the `effect_id` rule) and the add-only ratchet (the level ratchet).
- **hack-proofline** — the two-witness rule, and the honest finding that fail-open is correct over one trusted bundle and a **suppression attack** across two chains. This is why `org audit` reports `counterpart_absent` rather than passing silently — bounded by the receiver's declared `next_due`, so it is not noise on its first run.
- **fm-epoch-replay-laws**, **workbench-laws-lean**, **fm-grant-race**, **fm-jsonl-append-race**, **fm-custody-race**, **fm-crash-cut-publish**, **fm-scoped-path-laws** — see §6.
- **repair-loop-kernel** — the executable reference model of the effect plane. §3.5 is its implementation.
- **bailiff** — negative evidence: the EVM adds the capability model you already had, plus machinery. Closes that question permanently.
- **formal-methods** — the tool ladder, and the reason the `fm-*` repos are small and finished.

All four hackathon kernels canonicalize with `json.Marshal`, tying digests to Go declaration order and HTML-escaping them. Their **laws** graduate into `contracts/*` on the shipped canonical encoder; the **repos** retire to a results page. Nobody adopts a portfolio; a teammate needs an artifact to point at.

### Tooling (real value, no architectural weight)

`triage`, `tracelens`, `local`, `spend-audit`, `provenance`, the skills corpus. Two notes: `triage` gets promoted only in the sense that review depth becomes the `cycle_ceiling` grant dimension; `spend-audit` becomes a derived view once model calls go through the broker.

### Out of scope

ivy, roxiq, rung, roll-call, interject, finance, fitness, wellness-ai, bakeoff. They are the dogfood targets — the repos the org runs against — not parts of it.

### 4.4 What must be built new

1. **`contracts/org`** — spine types, kind classes, embedded schema, the bounded fold, `Admissible`, seals, refusal codes. One mutant per refusal code **on the authority-relevant subset only**.
2. **The blob store** — content-addressed bodies, classification, retention, erasure-with-tombstone.
3. **The distiller** — transcript tail plus marks to a record body, via a cheap model. The thing that makes continuity verbless.
4. **The resume canary** — fork from the chain alone, score fidelity, trend it.
5. **The effect ledger inside custody** — intent/attempt/outcome, classes, probes, the stamp, the refusals.
6. **The reconciler role** and the one external timer.
7. **`batond`** — the API of §3.7, file backend first, Postgres second.
8. **The shadow report** — what would have been refused, on your own history.
9. **The fleet simulator** — §6.
10. **`contracts/policy`** — one ordered level per (scope, effect class).

### 4.5 Surface retired versus added

Retired: `/continue` (16 invocations, the manual workaround for exactly this problem), `/claim`, `/release`, `/roster`, `/recover`, and `claims.jsonl`. Added, for agents: **zero verbs** — attach, mark, and release fire from session lifecycle, and the fold arrives as injected context plus a read-only MCP resource. Added, for humans: one CLI (`baton`) and two console numbers, `blocked_roles` and `human_debt` (escalations past their declared deadline). Those two are the steering pair: if `human_debt` trends up, either the org shape or the level dial is wrong, and nothing else distinguishes those two causes.

---

## 5. Levels

Enforcement today is a boolean per repo behind `GATE_ENFORCE`, which is too much friction for personal repos and too coarse for anything else. But four independent dials is a feature for a customer who does not exist. **Decision: one ordered level, set per `(scope, effect class)` pair, with the other properties derived from it.** Fewer knobs, same power, and it obeys derive-don't-record.

| Level | Refusals | Evidence | Containment | Escalation |
|---|---|---|---|---|
| **observe** | recorded, never returned | local chain | none | default applies on deadline |
| **advise** | returned as warnings; **the override is a first-class record with actor and reason** | local chain | none | default applies on deadline |
| **enforce** | bind for Baton's own operations | chain + periodic keyed anchor | broker-only egress required | human required for class U |
| **attest** | bind, and external systems require the evidence (branch protection, deploy gates) | anchors published off-box | sandboxed execution required | human required, no timeout, for class U and ceiling breaches |

Three laws.

**The same code path runs at every level.** The level changes verdict *handling*, never evaluation. This is the property that makes observe-mode data a trustworthy prediction of enforce-mode behavior, and without it the shadow report is a guess.

**Promotion requires the data the level below produced.** You cannot arm `enforce` on a scope without N days of `observe` history showing what it would have refused. This is simultaneously an honest safety property and the entire adoption motion: install at observe, change nothing, run thirty days, then hand over a report saying *here are the 87 merges that would have been refused, on your own history, with the evidence*. Demotion is an append with an author and a reason. Monotone, auditable.

**Overrides are the most valuable telemetry the system will ever collect.** An `advise`-level override is a rule that is wrong, recorded with a human's reason. That list is the tuning input; without it, promotion to `enforce` is guesswork.

The personal-versus-enterprise answer falls out: personal repos at `advise` for everything; the workbench at `enforce` for the merge class; a bank at `attest` for whatever it chooses. One substrate, one code path, one dial.

---

## 6. Where Gleam, Lean, and Quint are load-bearing

The rule: each must answer a question that a direct Go test cannot, and anything that fails that test is cut.

**Quint — load-bearing, and the clearest win in the portfolio.** Concurrency interleavings are exactly where intuition fails and exhaustive state exploration is exactly right. `fm-grant-race` found oversubscription of a one-cycle ceiling via stale local snapshots; `fm-jsonl-append-race` found short-write and lock-ownership failures with two writers. A Go test cannot enumerate interleavings; a model checker does it by construction. **Both counterexamples are promoted to permanent conformance tests replayed against the real adapter**, which is the ladder working as designed. New Quint spend goes to two seams only: the multi-writer append against the Postgres home, and the intent-before-wire crash cut.

**Lean — load-bearing at four places, and nowhere else.**

1. **Grant monotonicity.** A wrong law here is a security event, and the property is a universally quantified statement over an unbounded space of parent/child pairs. Property tests sample; a proof closes it.
2. **Fold boundedness.** The new one, and it is the one that matters for the thesis: *for every finite history, the fold's output is within the size budget.* No test establishes a bound over all histories.
3. **Ordering and tie-breaks.** This is where Lean already earned its keep — a *failed* proof in `workbench-laws-lean` found a real order-dependence at a rank tie in gate's verdict reducer. That is the lesson generalized: spend proof effort on comparators, orderings, and totality, where human intuition is reliably wrong.
4. **Protocol projection correctness** — that the compiler's per-role local contracts and the global type agree, so the admission table Go interprets has machine-verified provenance.

`fm-epoch-replay-laws` (fold ≡ checkpoint-resume ≡ replay) stays because it is already finished and it underwrites the claim that the startup path and the recovery path are the same code. It is not where new proof effort goes: it is a theorem about a pure function over a finite list, and the bugs in a chain implementation live in the locking, the torn tail, the migration, the encoder, and the error paths.

**Haskell — load-bearing at build time.** `parleyc` compiles a protocol written once as a global type, projects it into per-role local contracts, refuses incoherent protocols at the exact role and branch path, and **emits a JSON table of legal `(state, message-type)` pairs that is checked into the repo**. `contracts/org.Admissible` interprets that table at run time. Haskell owns the definition, Lean proves the projection, Go stays on the write path, nobody installs GHC to run the product. This is the move that converts the protocol compiler from decoration into structure, and it costs one artifact.

**Gleam — on probation, with one job and a cut date.** The observer's real finding — 254 traces, 171 complete, 81 stalled, 2 deviating, and structure the mental model had missed — came from *writing down the expected sequence of events*, not from a BEAM runtime. Replaying a log against a state table is a Go fold. So the Gleam bus is off the write path permanently (an in-line cross-language checker is a call stack, and the forbidden import applies to services too), and it keeps exactly one job: **differential oracle** — for every recorded trace, the Gleam bus and the Go table-interpreter must classify identically, and a disagreement is a bug in one of them. If that differential finds no disagreement in one quarter, the Gleam runtime retires to a README result and the compiler plus the table stay. That is falsifiable, which is the only standard that should keep it.

**Cut outright.** A mutant per refusal code *everywhere*. A mutant proves your test would catch a wrong law, not that your law is right. It is retained on the authority plane, where a wrong law is a security event, and dropped elsewhere, where it is a tax on the parts that most need to ship.

**And the rung that was missing entirely.** Lean and Quint prove properties of individual reducers; the deployment story starts at one Mac. The claim under test — *this works at 75 roles for three days with crashes* — lives in the gap, and nothing tests it before a customer does. So: **a deterministic fleet simulator** driving the *real* contracts and the *real* fold with synthetic roles, a fault schedule, injected clock skew, and the exported Quint counterexamples as fixtures. Everything is already a pure reducer over a log, so this is the fold plus a fault injector. It produces recovery time and blocked-role rate as numbers, which is what an enterprise asks for and what no amount of proof supplies.

---

## 7. Build order

Phases P0 through P4 are **committed**. P5 through P7 are **gated** on their predecessors' results.

### P0 — The falsification week. Zero new code. *Committed.*

Every item is answerable from data already on the machine, and any one of them can invalidate a plane.

1. **Classify the 87 blocked merges.** True positive or false positive. 87 of 247 actions is a 35% refusal rate against zero repos that require the check — those are unclassified predictions, not evidence, and the word "observational" is too kind. If false positives exceed 20%, the authority plane needs a redesign, not a rollout.
2. **Count the actual collisions.** Session-claims, worktree history, and transcripts, last 90 days: how many times did two sessions hold the same work item at once?
3. **Read 20 of the 81 stalled traces.** Defect or life? Until someone does, "32% stalled" measures the protocol's optimism.
4. **Price a role-day.** Eight concurrent ICs, one real day, read `spend-audit`. Multiply by 60. The reference topology comes from an engineer with internal capacity; the $40-a-month comparison point would be rate-limited into the ground at 75.

**Gate.** If collisions ≥ 10 in 90 days *and* false positives < 20% *and* a role-day is affordable at target scale, proceed to the full design. If collisions come back at 3, the honest product is a good `/continue` that fires from a hook — one week of work — and everything about ownership is premature. Write the numbers down before deciding.

### P1 — Continuity without a verb. *Committed.*

`contracts/canonical` unified (two hash schemes exist in one module today — driverstate's declaration-order encoder pinned as `v0`, everything new on `v1`), `contracts/org` spine and bounded fold, the blob store, hooks at SessionStart/PreCompact/Stop, the distiller, the resume canary, the human-as-role attach.

**Gate.** Kill a session mid-task; a fresh incarnation resumes and a **blind reader cannot tell** which records came from which incarnation. Resume-fidelity ≥ 90% across 20 canary runs. `/continue` invocations reach zero over 30 days and the skill is deleted. Two incarnations of one role run concurrently: the second is refused, the log is uncorrupted.

### P2 — One seam in anger. *Committed.*

Two fields on the grant (`incarnation`, `fence`), one high-water map in `custody serve`, one `(role, fence, work_ref, effect_id)` stamp on every request line with `effect_unstamped` as a refusal.

**Gate.** Kill an incarnation, append a takeover, and the dead incarnation's next custody request is refused inside one append with nothing sent to it. Verifiable in a day, and it is the moment three repos become one system.

### P3 — The effect ledger. *Committed.*

Intent-before-wire fail-closed, the four classes, probes in the manifest, `effect_recovery_undeclared` as a load-time refusal for any manifest action with no probe, the unknown queue, the reconciler role, the one external timer.

**Gate, and this is the demo that carries the whole thesis.** Kill an agent between `attempt` and `outcome` on a real merge. A replacement determines committed-versus-absent by probe alone, with no human, and never double-merges. Run it 20 times; zero duplicates, zero stalls.

### P4 — Bind one thing. *Committed.*

`enforce` on one repo, one effect class (merge), in anger. Not staged, not behind a plan.

**Gate.** A real merge is refused and the emitted remedy unsticks it. Until this happens, every claim in this document about authority is a claim about a program.

### P5 — Levels and the shadow report. *Gated on P0's classification and P4.*

`contracts/policy`, the four ordered levels, the override record, promotion-requires-evidence, and the report generator.

**Gate.** Personal repos at `advise`, workbench at `enforce`, and one shadow report generated from real history that a person who did not build the system can read and act on.

### P6 — A second human. *Gated on P1 through P4 holding for 30 days.*

`batond` with the API of §3.7, Postgres home, OIDC, tenant partition at the storage layer, blob classification and retention and erasure. This is the largest block of work and the least fun, and it is scheduled here for a specific reason: **the API's job is a second human on a second machine, not enterprise procurement.** Procurement asks for SOC2, a DPA, insurance, an SLA, and references, and a REST surface moves that needle by zero. What carries an enterprise conversation is the evidence pack — the proofs, the falsifications, the mutants, the shadow report — and what carries the product is one other person using it.

**Gate.** A second human, on a different machine, holds a role in the operator's org for two weeks, uses it without being taught a verb, and provably cannot read another tenant's chain. Nothing here is proven with one person, and every claim is unfalsifiable until this happens.

### P7 — Scale the org. *Gated on P6.*

Fleet simulator first, then two leads with mutual takeover grants, project leads, ICs, model calls through the broker, spend and concurrency and cycle ceilings live, the parley admission table on the write path, the Gleam differential.

**Gate.** Measure the real prompts-per-day number and the `human_debt` trend. If `human_debt` grows monotonically at 20 roles, the org shape is wrong and 75 will not work no matter what the substrate does.

---

## 8. The POC

**Two weeks. Two questions. Binary answers.**

**POC-A — Does ownership matter?** (P0, item 2, plus a one-week instrumented run.) Turn on attach-and-mark from the hook across every session on the machine, with no refusals of any kind. After seven days, count the events where two incarnations held the same role or the same work URI simultaneously.

- **Pass:** ≥ 10 collisions in seven days. Ownership is a real problem and the chain earns its place.
- **Fail:** ≤ 3 collisions. Ownership is a rare event, continuity is the whole product, and it is a text file plus a hook — one week of work, not a year. Stop and build that.

**POC-B — Does the seam close?** The single trace, end to end, on real infrastructure.

1. A role is chartered. An incarnation attaches, folds, and is assigned `github:<a real repo>#<a real PR>`.
2. It issues a merge through custody. custody records the `intent` (class Q, probe declared, deadline set) and fsyncs it **before** the wire.
3. The process is killed between `attempt` and `outcome`.
4. Derived liveness marks the role stalled at `next_due`. The timer fires; a supervisor appends `takeover`.
5. The dead incarnation is resurrected and made to retry. Its append is refused `tip_stale`; its custody request is refused `fence_regression`. **Nothing was sent to it.**
6. A fresh incarnation attaches, folds, and is **refused** any work append while an intent is open. It runs the declared probe, learns the merge committed, appends the `outcome`, and continues from `next[]`.
7. A blind reader, given only the chain and the effect log, reconstructs the whole sequence and states what the role is doing and why.

**Binary success criteria.** All seven steps, with no human intervention between 3 and 6, no duplicate merge, and the blind reader correct on goal and next action. Total elapsed recovery under ten minutes. Run it 20 times with the kill point randomized across the window; **20 out of 20 or it failed.**

If POC-A passes and POC-B passes, the architecture is real and P1 through P4 are the build. If POC-A fails, the product is smaller and better than this document. If POC-B fails, the failure will be in the join — the stamp, the probe, or the fold's refusal to proceed with an open intent — and that is exactly where the design most needs to be wrong early.

---

## 9. The honest risks

**Risk 1 — Construction velocity exceeds validation velocity, and the substrate becomes beautiful and unfalsifiable.**

This is the highest-probability failure and it is not speculation; the base rate is in data the operator collected himself. 51 authored skills, 23 ever invoked. gate: 4,818 records, 247 actions, zero repos requiring the check. drive: a spec and slices. And most damningly, `claims.jsonl` — two records, one day, two weeks ago, then abandoned: an ownership ledger with claim and link events, the exact mechanism this document proposes, already tried and already dead. The failure mode is not abandonment; it is `~/dev` at 55 repos with a gorgeous `contracts/org`, a Lean proof, a mutant per refusal code, and gate still bound to nothing.

*What falsifies it early:* the tell is mechanical — **any week in which a contract package ships and no repo changes its enforcement setting.** Two such weeks in a row and the build order has inverted itself. That is why P0 is a no-code week and why P4 (bind one repo, in anger) precedes every gated phase.

*And the specific answer to why this survives where `claims.jsonl` did not:* that one required `/claim`. This one has no verb at all — attach fires from SessionStart and pays the agent immediately by injecting its own memory, marks are mechanical facts the host observes without the model's cooperation, and the distillation is written by a separate cheap model reading the transcript. The agent's discipline is not in the loop. If it turns out that it is, this risk has already materialized.

**Risk 2 — The chain carries commitment but not thought.**

Every guarantee on offer is a guarantee about record ordering and digests. Zero guarantees are offered about whether the distilled context is still *true* after twelve handovers. A structurally perfect chain whose content has drifted produces exactly the failure the system exists to prevent, silently, with a valid hash. And what makes a fresh incarnation resume well is entirely the quality of the distillation — a markdown file holding the last five checkpoints resumes just as well as a 400-record chain if nobody reads record 12.

*What falsifies it early:* the resume canary, in P1, before anything else is built on top. If a blind reader cannot state the goal and the next action from the fold alone at ≥ 90% across 20 runs, the thesis is about locks and not about continuity, and the product shrinks to ownership plus an effect ledger — still valuable, but a different pitch and a much smaller build. This is the reason `goal` is charter-inherited and unwritable, the reason the fold is a bounded reducer with a Lean bound, and the reason the canary is a component rather than a nice-to-have.

**Risk 3 — The substrate accelerates production into a fixed acceptance bottleneck, and cost makes the target topology unreachable.**

Two problems with one shape. Sixty ICs producing work for two to three days each funnel through nine project leads and two leads into one human at two exception-handling prompts a day. Nothing in this architecture verifies 60 units a day of agent output — gate is a policy engine, not a reviewer. Amdahl's law applies to humans, and the honest consequence is that the substrate makes it possible to generate more unverified work faster while the binding constraint sits somewhere the architecture does not touch. Alongside it: a continuously working IC is somewhere between $20 and $150 a role-day depending on model mix, so 60 of them is somewhere between a car payment and a senior salary per month, and the reference topology comes from someone with internal capacity.

*What falsifies it early:* P0 item 4 prices a role-day in one day with a tool that already exists — do it before designing for 75. And in P7, `human_debt` (escalations past their declared deadline) is the instrument: if it grows monotonically at 20 roles, 75 is unreachable regardless of substrate quality, and the correct response is a smaller org with derived completion, attested completion, and `cycle_ceiling` doing more of the acceptance work — not a bigger one.

*What the design does about it, honestly:* completion derived from committed effects kills the "the agent said it was done" class outright; attested completion gives judgment work an owner and a grant rather than pretending derivation covers it; `cycle_ceiling` encodes the two-fix-rounds rule as a refusal instead of prose; and `blocked_roles` plus `human_debt` are the console headline so the bottleneck is visible before it is fatal. None of that creates review capacity. It only makes the shortage measurable, which is the most an architecture can do about it.

---

### One thing a buyer will ask inside ninety seconds, answered here so it does not have to be improvised

*What does this do that Temporal plus a Postgres advisory lock plus branch protection plus CODEOWNERS plus short-lived GitHub App tokens does not?*

Three things, and only three. **Fence-bound authority** — revocation is a local write that reaches partitioned hosts with no message sent, where every alternative is a TTL or a broadcast. **Declared recovery per effect class, refused at intent time** — the difference between logging what agents did and bounding what happens when one dies mid-effect. **Continuity as harness-neutral data** — Temporal owns the workflow and requires deterministic replay of a worker; here the worker is an LLM, nothing about it is deterministic, and the ledger belongs to the organization rather than to any one harness.

And the honest half: if your work *is* a deterministic workflow, use Temporal. This exists because it is not.
---

## Appendix A — Primitive design: useful, easy to use, hard to misuse

The vision above settles *what* to build. This appendix settles a separate
question the operator asked directly: are these good primitives? Graded on
Rusty Russell's scale, where the top is *impossible to get wrong*, the middle
is *read the docs and you will get it right*, and the bottom is *obvious usage
is wrong*. Four findings change the API surface.

**A1 — The append API must be a transaction closure, not a caller-supplied
tip.** This is the highest-leverage API decision in `contracts/org`. Given
`tip := Fold(role); …; Append(tip, rec)`, every caller owns the
read-verify-write window and some of them will get it wrong; the §3.2 lock
becomes a rule people have to know. Given
`Append(role, func(tip RoleState) (Record, error))`, the lock spans the
callback and the window is unreachable. Identical semantics, opposite ends of
the scale. **The tip is never a parameter the caller supplies from an earlier
read.**

**A2 — `Canonical()` completeness must be mechanically checked.** Nothing
forces a record type's canonical form to include all of its fields. Add a
field, forget the canonical form, and the digest silently stops covering it —
a chain that looks sealed and is not. Obvious usage is wrong. A conformance
test reflects over each record struct and asserts every exported field appears
in its canonical shape. This lands with the first record type, not after.

**A3 — A missing fence high-water mark must fail closed.** §3.4 correctly
scopes fence enforcement to long-lived verifiers. It does not address what
happens when a long-lived verifier *loses* that state — a redeploy, a new
machine, a wiped state dir. The mark resets to zero, every displaced grant
verifies again, and nothing signals it: security silently weaker after a
routine deploy. Missing high-water state is a refusal with a remedy, never an
accept.

**A4 — The cross-chain assign invariant needs an owner or an honest
downgrade.** "One open assign per work URI across all chains" cannot be
established by folding one chain. Today it is asserted at the contract and
enforced nowhere, so two leads can hand the same item to two ICs and only a
global sweep notices. Either the home owns it as a real uniqueness constraint
(trivial with a Postgres home, a scan with a file home), or the contract says
`assign_conflict` is **detected, not prevented**, and the reconciler surfaces
it. Silence is the only unacceptable option.

**A5 — Charters are a usability problem, not a correctness one.** They score
worst of any primitive here: hand-authored scope, decides, never-decides,
capabilities, and ceilings, with no template, no default, and no validation.
Too tight and the role is useless; too loose and it is overpowered; both fail
silently. The fix is not a law but a template per role kind plus a `doctor`
check that flags capabilities a role's work kinds never exercise.

**A6 — Checkpoint quality cannot be fixed by an API, only measured.**
`empty_next` catches empty and nothing catches vague. §3.3's move — the host
writes marks, a separate distiller writes bodies — removes the agent's
discipline from the loop, which is the real fix. The resume canary is the
instrument. No contract can do more.
