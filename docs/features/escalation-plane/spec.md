# The Escalation plane

**Status:** POC (built end-to-end; the plane-promotion argument is a proposal, not a settled decision).
**Scope:** the typed escalation contract, the resolution back-channel that closes
the agent→human→agent loop, and the case for modeling Escalation as its own plane.

---

## 1. Thesis

**Escalation is the agent→human PUSH surface.** When an agent is stuck, over its
ceiling, or holding a call it may not make, it pushes: *"I need a human — here is
what, why, and how bad."* That is a distinct role from the two planes it is
usually mistaken for:

- **Observability (§4, plane 5) is the human PULL.** `gate next`, `console`,
  `explain` — the operator *goes and looks*. Read-only, owns no authoritative
  state (Amendment 3). Escalation is the opposite arrow: the system reaches *out*
  to the human, unprompted.
- **Verification (§4, plane 3) judges FINISHED work.** The ladder produces a
  verdict about a completed artifact. Escalation is not a verdict — it is the
  admission that a verdict *cannot be reached here* and must come from elsewhere,
  plus the return path for that answer.

The push has two halves: the outbound page (*"here is the problem"*) and the
inbound resolution (*"here is my decision"*). Today the workbench implements the
outbound half well and leaves the inbound half homeless. That gap is the whole
argument below.

## 2. The diagnostic: one plane doing another's job

`workbench-101.md` §4 earns the plane decomposition with one payoff: *every
recurring failure turns out to be one plane doing another plane's job.* Apply the
diagnostic to escalation as it stands today:

| Step | Who does it now | Whose job is it really |
|---|---|---|
| A run parks (emits the escalation) | **gate** — records a `KindEscalation` artifact as a Verification outcome | fine: the *decision to escalate* is a Verification result |
| The park reaches a human | **flare** — tails gate's log, routes a Slack page (Observability) | fine: routing a derived signal is Observability, and flare stays read-only (Amendment 3) |
| The human's decision returns | **nobody** — it comes back only out-of-band, as an operator hand-running `gate judge` | ← **the gap** |

The third row is the tell. There is a typed artifact for the push (the
escalation) and a real transport for it (flare), but **no plane owns the
resolution ingest**. The decision re-enters the system through a side door: a
human, out of band, reconstructs the run id and types `gate judge`. Nothing
models *"a decision came back for escalation X"* as a first-class, contract-
coupled event. The push surface is half-built — it can page, but it cannot
*receive an answer* as anything but a manual CLI invocation.

That homeless seam is the argument. In the plane vocabulary: **resolution ingest
is a responsibility with no plane**, so it defaulted to "a human's muscle
memory." Naming Escalation as a plane gives it an owner and a contract.

## 3. Why a 6th plane (and not a bolt-on to an existing one)

The honest counter-argument is: *escalation is already covered — the park is
Verification, the routing is Observability, done.* That is exactly the "one plane
doing another's job" trap the diagnostic warns about. Walk the alternatives:

- **Put resolution ingest in Verification (gate)?** Partly right — the *effect*
  (record a judgment, re-reduce, re-apply the ceiling) must live in gate, because
  only gate may write gate's log. This POC does put the effect there (`gate
  resolve`, §5). But the *ingest surface* — the thing a notification's ack, a
  webhook, a future gate UI all post to — is not a verification concern; it is a
  transport-agnostic front door. Folding it entirely into gate re-answers "who
  owns escalation?" with "gate does, again," which is the muddle the plane model
  exists to dissolve.
- **Put it in Observability (flare)?** Forbidden, and correctly so. Amendment 3:
  Observability is read-only and owns no authoritative decision. A flare that
  wrote a resolution would be a dashboard *becoming* a source of truth — the exact
  failure Amendment 3 was written to prevent. So the resolution ingest *cannot*
  be a flare change. It must be a new component.

Once resolution ingest can live in neither incumbent without breaking that
incumbent's charter, it is its own responsibility. That is what "a plane" means
here: **a surface where a contract can live** — versionable, testable, auditable.
Escalation qualifies: it has a typed contract (§4), a push origin, a routing
mechanism, and — newly — a resolution ingest with an audit trail.

**This is a proposal, not a fait accompli.** A 6th plane is a real cost (more
vocabulary, another boundary to police). The POC's job is to make the seam
concrete enough to decide honestly, not to declare victory. See §9 open questions.

## 4. The contract: `contracts/escalation` (`escalation.v1`)

A leaf package mirroring `contracts/authority` and `contracts/driverstate`
exactly: the ergonomic Go view of an embedded JSON Schema, kept in lockstep by
`conformance_test.go`, stdlib-only, no decision logic. Cross-language readers
(ship, dossier) read the schema file; in-process readers use the Go type.

The Go type is `escalation.V1` (revive-clean; the contract is *escalation.v1*).
It is a **superset of the untyped body gate wrote before**, so adoption changed
no persisted wire value:

| Field | Req? | Meaning |
|---|---|---|
| `schema_version` | ✓ | `escalation.v1` — the version gate's anchor (additive) |
| `outcome` | ✓ | gate's outcome string (`parked_for_judgment`) — leads a notification title |
| `verdict` | ✓ | the reduced verdict id the park stands on |
| `grant` | ✓ | the grant the run ran under |
| `question` | ✓ | the park reason a zero-context reader sees first |
| `run_id` | — | the run, for a self-contained body (additive) |
| `code` | — | machine park code — `grant_tier_exceeded` / `cycle_count_unreadable` / `grant_cycle_exceeded`; **absent on a content park** (which carries a brief) |
| `repo`, `number` | — | the PR subject, for a click-target |
| `brief` | — | the synthesized plain-language page for a zero-context approver |
| `resolution` | — | **new** — the closed-loop stamp (below), absent at park time |

The `code` enum values **equal gate's own persisted park codes**
(`capability.ErrTierCeiling.Error()` and friends) — a gate-side test
(`TestParkCodesMatchCapability`) pins that equality so the shared vocabulary and
gate's errors can never drift.

`Resolution` is the missing seam made typed: `{decision (pass|block), who, at,
judgment_id}`. It doubles as (a) the body of the standalone resolution artifact
the back-channel appends and (b) the embedded `V1.Resolution` a reader projects.

**Two readers, one law.** `DecodeBody` is tolerant — no version gate, unknown
fields ignored — because the routing and projection readers must still render
older/unknown bodies; the migration must never break the existing best-effort
route. `Validate` is the separate strict law (version gate + required fields) a
consumer applies when it wants teeth. flare and gate's inbox use the tolerant
reader.

### The collapse (D1)

The escalation body was an **implicit contract declared three times**: an untyped
`map[string]any` on gate's write side, and a locally-redeclared, tolerantly-
decoded struct in *two* readers (flare's `internal/source/gatelog.go` and gate's
own `internal/observe/inbox.go`). All three now share `escalation.V1`:

- **gate write** (`cmd/gate/main.go`, `act`): builds a typed `escalation.V1`
  instead of a map. Same wire shape.
- **flare read** (`gatelog.go`): `escalation.DecodeBody`, still tolerant — an
  undecodable body yields the zero value (brief nil → briefed "no"), never a
  corrupt page. flare's read-only doctrine is untouched.
- **gate inbox read** (`inbox.go`): `escalation.DecodeBody`, still best-effort —
  a drifted body never drops a park from the inbox.

## 5. The incumbents, named

| Escalation-plane role | Component | Status |
|---|---|---|
| **Push origin** (decide to escalate, emit the typed park) | **gate** (`verify` ladder → `act` → `KindEscalation`) | reused as-is |
| **Routing** (deliver the page to a human) | **flare** (tails the log, Slack/toast/webhook) | **reused as-is — NOT absorbed** |
| **Resolution effect** (record judgment, re-reduce, re-apply ceiling, stamp) | **gate** `resolve` verb (new) | built |
| **Resolution ingest** (receive `(id, decision)` from a notification ack) | **`cmd/escalate`** (new component) | built (POC) |

**What happens to flare — the explicit answer.** flare is **reused, not absorbed
and not rebuilt.** It stays the routing mechanism and stays read-only (Amendment
3). The Escalation plane does not swallow flare; it *names* flare as the plane's
routing incumbent, exactly as the State plane names gate's log and the
driver-state ledger without merging them. The one change flare saw here is
internal hygiene: it now decodes the shared contract instead of a hand-rolled
struct. Its charter — pure sink, never writes a decision — is not just preserved
but is the *reason* the resolution ingest had to be a separate component.

### `gate resolve` — the effect (D2a)

`gate resolve -escalation <esc_id> -grant <grt> -decision pass|block -why … -who …`

Keyed on the **escalation id a notification carries** (where `gate judge` is keyed
on a run id — the operator already at a terminal). It resolves the id to its run,
records the decision through the *same* judgment core `judge` uses (so the grant
ceiling re-applies — a resolution can never launder a ceiling), then appends a
`KindResolution` artifact — `{decision, who, at, judgment_id}` — parented to
`[escalation, judgment]`. It exits on gate's exit-code contract (0/1/2/3), so the
same driver branch reads a resolve outcome as a gate run.

`KindResolution` is **provenance, not a decision**: the effect is the judgment +
re-reduction the shared core already recorded. It sits outside the
action/escalation outcome families, so the cycle count and the parked/ready
projections ignore it — recording a resolution never counts as a review cycle or
masquerades as a fresh park.

### `cmd/escalate` — the ingest (D2b)

A new binary (**not** in flare). Its `resolve` verb ingests a `Decision`
`{escalation, verdict, who, why, grant}` — transport-agnostic; the POC fills it
from CLI flags, but the same shape is what a Slack action ack, a webhook POST, or
a future gate UI would carry — validates it (a junk id or out-of-vocabulary
verdict never reaches a subprocess), and drives `gate resolve` through the CLI
seam (shelling the binary, never importing it — exactly how `console` composes
with gate). It passes gate's exit code through faithfully; an ingest-side failure
exits 5, outside gate's code space.

Why split effect (gate) from ingest (escalate)? Because only gate may write
gate's log, but the *front door* for a decision is not a gate concern. The split
is the plane boundary made real: the ingest is the new surface; the effect stays
where the log lives.

## 6. Flows

**Push (unchanged, now typed):**
```
gate run parks → KindEscalation (escalation.V1, typed) → flare tails → Slack page
```

**Resolution (the new seam):**
```
notification ack ──(esc_id, decision)──▶ cmd/escalate resolve
      │  validate (id shape, pass|block, provenance)
      ▼
   gate resolve -escalation <id> …
      │  runOfEscalation(id) → run
      │  applyJudgment: append KindJudgment (parent: escalation) → re-reduce → act (ceiling re-applies)
      │  stampResolution: append KindResolution (parents: [escalation, judgment])
      ▼
   exit 0/1/2/3  ·  gate audit still verifies
```

flare never appears in the resolution flow — that is the point.

## 7. Out of scope (POC boundaries)

- **No flare inbound.** flare does not gain a resolution route; Amendment 3 holds.
- **No live notification transport.** The ingest is a CLI verb; wiring a Slack
  interactive-action callback or an HTTP endpoint onto `ingest.Decision` is the
  obvious next step, deliberately not built.
- **No projection of the embedded `resolution` field.** The resolution is recorded
  as a `KindResolution` artifact with full provenance; teaching `gate next` /
  `console` to render `V1.Resolution` inline is additive and deferred.
- **No auto-resolve.** `resolve` requires an explicit human decision (the plane's
  point is the human's call). `gate judge -auto` remains the automated path.
- **No admission validator.** Per the leaf posture, `contracts/escalation` runs no
  runtime JSON-Schema validator; structural laws the schema states are future
  admission input. `Validate` therefore has no production caller yet — the
  tolerant read path deliberately skips it and the writer builds valid bodies
  in-code; it earns its keep when admission arrives.

## 7a. Known gaps to close before day-to-day use (from review)

An independent (Fable-model) review returned **merge-with-nits**: no boundary-law
violation, no state-corrupting bug. The seam is real; these are the follow-ups it
surfaced, captured so they aren't lost — none blocks the POC merge, but the
starred ones gate a *live transport* (§8 step 3).

- **★ Idempotence / replay guard.** `gate resolve` has no already-resolved check.
  Harmless at a single-shot CLI (it matches `judge`'s re-runnability), but a live
  transport that retries (Slack re-sends action callbacks on a slow ack, and a
  double-tapped button is routine) would append a fresh judgment+resolution each
  time. Before any transport: reject/no-op when a `KindResolution` already parents
  the escalation, or the run's latest state is no longer parked.
- **★ `who` is asserted, not authenticated.** The ingest only checks `who` is
  non-empty. Fine under gate's local-trust model; the moment a transport exists,
  `who` must derive from the transport's verified identity (e.g. the Slack user
  id), never the request payload.
- **★ Stale escalation ids.** A resolve can re-park, so a run accumulates
  escalations; `runOfEscalation` checks kind, not recency, so resolving an *old*
  esc id judges the run's current verdict set while parenting provenance to a
  stale park. Reject a non-latest escalation when adding the replay guard.
- **Judge/resolve provenance asymmetry.** A hand-run `gate judge` closes the same
  loop without a `KindResolution` stamp, so "who closed this park?" is answerable
  from the log for the back-channel path only. Extend the stamp to the judge path
  (with `who=operator`), or have a reader treat its absence as "resolved via
  judge" — decide *before* a projection reads the resolution.
- **Nits.** `applyJudgment` takes seven positional params (a params struct would
  remove the transposition hazard); the demo seed lives as an env-guarded `go
  test` (fold into a hidden gate verb if it outlives the POC).

## 8. Adoption sequence — from "merged" to "used"

The gap between merged and used is precisely: **nothing the operator sees today
carries the escalation id or the resolve command.** `gate next` still prints the
`gate judge -run …` line; flare's Slack page carries the esc id only as the
event's internal id, unrendered. A minimal path to a real day-to-day loop:

1. **Surface the paste-ready resolve line (immediate, small, read-only).** Teach
   `gate next`/the inbox and flare's Slack card to render
   `escalate resolve -escalation esc_… -grant grt_… -decision <pass|block> -who … -why "…"`
   next to the existing judge line. Rendering only — Amendment 3-safe. After this,
   the loop already works: Slack page on the phone → paste one line in a terminal.
   The cheapest dogfood loop; should land within days of merge.
2. **Idempotence + recency guards in `gate resolve`** (§7a ★) — the prerequisite
   for any retrying transport.
3. **`escalate serve` — the live transport.** A small HTTP listener *in
   cmd/escalate* taking Slack interactive-action callbacks: flare renders
   Approve/Block buttons (rendering only; the callback URL points at escalate,
   never flare), escalate verifies the Slack signature, maps the ack to
   `ingest.Decision` with `who` from the *verified* Slack identity, shells `gate
   resolve`. This is the real remote-approval unlock. Constraint: resolve still
   needs a live grant at resolve time, so remote approval only works inside an
   unexpired grant window (coherent — a resolution can't outrun delegation — but
   mint before stepping away).
4. **Project the resolution.** `gate next -json` + console join `KindResolution`
   to show "resolved by X at T" on recently-cleared parks — after the judge/resolve
   asymmetry (§7a) is resolved, so the projection isn't lying about judge-path
   decisions.
5. **Then decide the plane question** (§3) from usage evidence, as its own
   workbench-101 doc PR. Agent→agent resolution rides the same seam unchanged;
   build it only when a concrete bot-resolver use case exists.

## 9. Open questions (including the ones the operator raised)

- **Is this channel agent→agent too?** The POC is agent→human→agent (a human
  decides). But the ingest surface is decision-source-agnostic: `ingest.Decision`
  does not care whether `who` is a person or an automated policy agent. An
  agent→agent variant (a bot resolver posting a decision for a class of parks it
  is authorized to clear) is a natural extension of the *same* seam — the plane
  models the push+return, not the identity of the responder. Worth deciding
  explicitly before the seam ossifies around "human-only."
- **Does huddle (and the other Slack-adjacent surfaces) fold into Escalation?**
  Open, and a good "one plane doing another's job" question. The Escalation plane
  owns the *contract* for agent→human push + resolution ingest. The *transport*
  (Slack via flare, toast, webhook, and possibly huddle) is routing mechanism —
  swappable, plural, and NOT the plane. huddle is a different surface (agent
  coordination / chat), not obviously an escalation transport; absorbing it would
  itself risk one-plane-doing-another's-job. The honest read: several
  Slack-adjacent things exist because *routing is plural by design*, but the
  question of whether any of them should converge on the escalation contract (or
  stay separate) is unresolved and deserves its own look, not a reflex merge.
- **Is a 6th plane worth its cost?** The seam is real (§2); whether it earns
  standing plane status or stays "a contract gate+flare share" is the actual
  decision this POC exists to inform.
- **Where does the resolution field get projected?** If `gate next` renders
  `V1.Resolution`, the console gets a closed-loop view for free — but that couples
  the projection to the resolution artifact join. Deferred.

## 10. Validation

Proven end-to-end through the real binaries — see
`EVIDENCE-escalation-plane-poc.md`: a gate park writes a typed `escalation.v1`
body → the inbox reads it (typed) → `escalate resolve` ingests a decision by
escalation id → a `KindJudgment` is recorded parented to the escalation → a
`KindResolution` stamps the decision with provenance → `gate next` shows the park
resolved (now ready-to-merge) → `gate audit` reports `chain intact`.
