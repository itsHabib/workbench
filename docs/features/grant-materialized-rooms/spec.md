# grant-materialized rooms - Technical Design Document

**Status:** draft / proposal - NOT a build commitment. The artifact we decide from.
**Owner:** @itsHabib
**Date:** 2026-07-22
**Related:** [custody TDD](../custody/spec.md), [execution-runtime spec](../execution-runtime/spec.md),
[auto-mode defaults](../../auto-mode-defaults.md), rooms `vsock-secrets` + `host-witness` specs
(itsHabib/rooms `docs/features/`), dossier project `workbench`

> **Reviewers - focus areas:** the reach-path decision (§4 D2 - tap-listener vs
> vsock bridge, and whether the multi-bind loosening of custody's localhost-only
> NFR is sound), the attenuation law and its signing pre-image change (§4 D1, §6),
> the receipt schema as a cross-repo contract (§5), and the token-in-pcap residue
> (§8.1). This is a design review, not a code review.

## 1. Problem & hypothesis

The portfolio's capability plane is advisory where it matters most. gate parks a
merge and custody refuses a request, but the *execution environment* an agent
works in still carries ambient authority: whatever credentials reach the
workload are held by every process in it, for the whole run, usable for
anything, from anywhere. Three shipped pieces each solve a third of this:

- **custody** (workbench #83/#84) scopes *reach* - which requests a grant may
  make - but its client today is an agent on the operator's own box, sharing a
  session with the mint key, the state dir, and every other credential.
- **rooms** vsock secret injection (rooms #79) delivers a secret into a
  disposable microVM one-shot, memory-only - but what it delivers is the *raw
  vendor secret*, so inside the room the authority is still unscoped and
  unexpiring.
- **rooms** host witness (rooms #77) records every byte of guest egress
  unforgeably - but nothing connects what was *observed* to what was
  *authorized*.

**Hypothesis:** compose them and the grant stops being advisory and becomes the
environment. A Runway placement onto rooms resolves a live operator grant,
custody derives a short-lived attenuated child grant, and *only the child token*
enters the room - the vendor secret never crosses the VM boundary at all. The
agent reaches the vendor exclusively through custody's proxy, so every request
is rule-checked and logged; the witness pcap independently records the same
traffic from outside the trust boundary; teardown destroys the only environment
in which the token was ever usable. Authority lives in the room, not the agent.

This subsumes `custody-drain`'s end-state for placed work (the secret never
materializes where the agent runs - stronger than "the plaintext file stops
existing") and is the practical form of the Action Escrow direction from rooms'
`product-directions.md`: the proposal boundary becomes structural without
building replicas or applicators.

**Non-goals (v0):** fleet / multi-host placement; any cloud-provider or
hosted-inference integration; agents minting grants for sub-agents (the schema
supports delegation from day one - nothing implements it); revoking a child
grant mid-run (TTL + teardown are the v0 bounds); non-HTTP or request-signing
credentials (custody's own compatibility rule); changing gate. Each is
evidence-driven later work.

## 2. Functional & non-functional requirements

Functional:

- FR1: a WorkSpec `Secret` may carry a `custody:` ref naming a key and action
  set; the Runway rooms adapter resolves it at placement time or refuses the
  placement - no `custody:` ref ever falls back to a raw secret.
- FR2: resolution requires a live operator-minted parent grant covering the
  requested key and actions; absent one, the placement refuses with the exact
  `custody grant` remedy command (gate's park-with-remedy pattern).
- FR3: the derived child grant is attenuated by construction: actions a strict
  subset-or-equal of the parent's, expiry `min(parent expiry, run deadline +
  cancel grace)`, and a signed `parent` field chaining child to parent.
- FR4: only the child token enters the guest (vsock, first-read-then-delete,
  rooms #79 semantics). The vendor secret is readable by custody alone, on the
  host, from the OS credential store.
- FR5: the run emits one `room-authority receipt` joining: the grant chain
  (parent id, child id, digests), the delivery record, the witness artifact
  digests, the changeset digest, and teardown status - self-contained and
  vendor-neutral, per §5.
- FR6: failure anywhere fails closed: no live parent -> no placement; derive
  error -> no boot; delivery failure -> no workload start (rooms T6);
  custody unreachable from the guest -> vendor calls fail with no fallback path.

Non-functional:

| Dimension | Target |
|---|---|
| Latency | derive + inject adds < 250 ms to placement (one CLI call + existing vsock path) |
| Security | vendor secret bytes never present in guest memory, guest filesystem, artifacts, or transcripts; child token unusable after `min(TTL, teardown)` |
| Operability | one receipt answers "what authority did this run hold and what did it do with it"; every refusal prints its remedy |
| Blast radius | losing a child token leaks a capability scoped to one key's granted actions for at most one run's deadline |
| Dependencies | no new third-party deps; rooms gains configuration/runbook, not consumer concepts |

## 3. Architecture overview

Everything below the new seam already ships. The new work is the resolver, the
derive verb, and the receipt.

```
/work-driver task
  -> Runway placement (rooms backend)
       1. WorkSpec.Secrets: [{name: TRACKER, ref: "custody:tracker/read"}]
       2. resolver: live parent grant?        - no -> refuse + remedy (FR2)
       3. custody derive -> child token        (attenuated, run-capped TTL)
       4. rooms run --secret CHILD_TOKEN ...   (vsock one-shot, rooms #79)
  -> in the room: agent base URL = custody listener on the tap
       every vendor call: child token -> custody rule-match -> secret injected
       host-side, forwarded upstream          (custody #83/#84, unchanged core)
       witness pcap records the same traffic  (rooms #77)
  -> teardown: token's only usable environment is destroyed
  -> collection: room-authority receipt assembled host-side
       -> driver-state ledger / dossier artifact
```

Reused: custody's grant mechanism, manifest, matcher, proxy engine; rooms'
vsock delivery, witness, changeset, teardown; Runway's placement + result
contract (`PlacementReceipt.Details` carries the receipt ref). New: custody
`derive` (one verb + one signed field), the Runway secret-ref resolver
(placement-side policy), the receipt schema in `contracts`, and a custody
listener reachable from the room's tap.

The seam law holds: rooms stays substrate (it delivers an opaque secret value
and records egress - it learns nothing about grants); custody stays reach
policy (it learns nothing about placement); Runway composes them through
artifacts. The receipt is assembled by the adapter from the pieces each layer
already emits - no layer imports another's decision code.

## 4. Key decisions & trade-offs

**D1 - attenuation is a signed field and a law, not a convention. (The
schema-now requirement.)** `Grant` gains `parent string` (empty for
operator-minted roots). `derive` refuses unless child.actions is a subset of
parent.actions, child expiry is at or before parent expiry, and the parent
itself validates live at derive time. The signing pre-image extends to cover
`parent`, which per custody's own rule means a Version bump (`cst2_` prefix);
grants are short-lived and mint-fresh across it, so migration is re-mint, not
rewrite. Depth is capped at 1 in v0 (`derive` refuses a parent that itself has
a parent) - delegation chains are a schema capability, not a feature.
Alternative rejected: modeling the child as a separate record type - two
validation paths for one trust question; the chain belongs in the one envelope.

**D2 - guest reaches custody over the room's existing tap; custody grows a
second, explicitly-configured listener. (RECOMMENDED, reviewers weigh in.)**
The guest's default gateway *is* the host's tap IP, so reach requires zero new
rooms mechanism - custody binds the tap gateway address (`-addr` exists) and
the runbook pins firewall rules restricting that listener to the room subnet
and custody's port. Bonus: custody traffic transits the tap, so the witness
pcap records every authorized call alongside any unauthorized egress - the
same artifact shows both. Cost: custody's v0 NFR said 127.0.0.1 only; this
loosens it, deliberately and only for an explicitly-flagged second listener
(`custody serve -tap-addr <gw:port>`, refusing wildcard binds). Alternative: a
vsock TCP bridge (guest-side forwarder, host-side vsock->localhost proxy) keeps
custody localhost-only but adds a guest agent + a host proxy - two new moving
parts to avoid one flagged bind. Rejected for v0; revisit if the tap listener's
source-restriction proves fragile in the runbook.

**D3 - the vendor secret never enters the guest; proxy mode is the only v0
mode.** A "direct mode" that vsock-injects a derived *vendor* credential only
works for vendors with real ephemeral tokens; for static-header keys (custody's
entire v0 domain) direct injection is just today's status quo - unscoped
authority with a witness. Rather than ship a weak mode beside a strong one,
v0 refuses `custody:` refs whose key cannot be proxied. Non-proxyable
credentials keep the existing raw `--secret` path, unchanged and un-blessed.

**D4 - child TTL is capped by the run, not chosen by the caller.** The resolver
computes `min(parent expiry, now + deadline_ms + cancel_grace_ms + margin)`.
Nothing in the WorkSpec can ask for longer; a longer need means the operator
minted the parent wrong. This is what makes "authority lives in the room"
literal - the token outlives its room by at most the margin.

**D5 - the receipt is assembled host-side by the adapter, not emitted by rooms
or custody.** Rooms would otherwise learn grant concepts (consumer leakage);
custody would otherwise learn placement. The adapter already holds every input
at collection time: the derive record, Runway's `Result` artifacts (witness,
changeset digests), and teardown status from the room lifecycle. Plain JSONL +
a JSON schema in `contracts`; hash-chaining deferred until a receipt gates an
effectful verb chain (same rationale as custody's log).

**D6 - provider-shaped environment, no globals.** Which custody endpoint the
guest uses, and any inference endpoint the workload needs, are placement
profile fields expanded into the room's environment - never compiled-in
addresses. This is the one concession v0 makes to the fleet horizon: it costs
a profile field today and prevents a rip-up later.

## 5. Data model

**Grant envelope change** (custody, Version 2):

```go
type Grant struct {
    // ... existing fields ...
    Parent string `json:"parent,omitempty"` // grant id this was derived from; "" = operator-minted root
}
```

`parent` joins the signing pre-image. Attenuation laws (§4 D1) are enforced by
`derive`, re-checked by `Validate` when a chain is presented.

**Secret ref grammar** (WorkSpec, no schema change - `Secret.Ref` is already
opaque): `custody:<key>/<action>[,<action>...]` - e.g.
`{"name": "CUSTODY_GRANT_TRACKER", "ref": "custody:tracker/read,comment"}`.
Anything else in `Ref` flows through the existing raw-secret path untouched.

**room-authority receipt** (`contracts/authority`, `authority-receipt.v1`,
JSONL, one line per placed run with a `custody:` ref):

```jsonc
{
  "schema_version": "authority-receipt.v1",
  "run_id": "run_...",              // runway run
  "allocation_id": "...",           // rooms allocation from PlacementReceipt
  "key": "tracker",
  "grant": {
    "parent_id": "…", "parent_digest": "sha256:…",
    "child_id": "…",  "child_digest": "sha256:…",
    "actions": ["read"],
    "minted_at": "…", "expiry": "…"
  },
  "delivery": { "channel": "vsock", "delivered_at": "…", "one_shot": true },
  "evidence": {
    "witness_pcap_sha256": "…",     // from Result.Artifacts
    "witness_json_sha256": "…",
    "changeset_sha256": "…",
    "custody_log_ref": "req_… ..req_…"   // request-id span in custody's own JSONL
  },
  "teardown": { "status": "destroyed", "at": "…" }
}
```

Self-contained (a reader needs no other store to know what authority existed,
when, and what evidence covers it) and vendor-neutral (no tool names beyond the
portfolio's own; no provider identifiers). This is the cross-repo contract:
workbench owns the schema; rooms' artifacts are referenced by digest, never
restructured.

**State:** derive records persist beside grants (`<state>/grants/`, existing
layout); receipts append to `<runway-state>/<run>/authority-receipt.jsonl` and
are named in `Result.Artifacts`.

## 6. API contract

```
custody derive -grant <cst2_parent-token> -actions a[,b] -ttl <d>   -> cst2_child-token
    refusals (coded): parent invalid/expired -> the existing four classes;
    actions not a subset      -> refused_attenuation_actions
    ttl beyond parent expiry  -> refused_attenuation_ttl
    parent has a parent       -> refused_chain_depth
custody serve -addr 127.0.0.1:8127 [-tap-addr <gw-ip>:8127]
    -tap-addr refuses 0.0.0.0 / :: and any address not on a tap interface
```

Runway rooms adapter (internal, placement-time):
`resolve(secret Secret, policy Policy) (injectable value, deriveRecord, error)`
- the only component that reads both a grant and a placement. Refusal surfaces
as a Runway admission/preparation failure with `reason_code:
authority_unresolved` and the remedy in diagnostics.

Guest environment (expanded from the placement profile, D6):
`CUSTODY_GRANT_<KEY>` (the child token, via vsock staging file - not ambient
env; the runner reads and exports it into the agent process only, matching
rooms T3 semantics) and `CUSTODY_BASE_<KEY>=http://<tap-gw>:8127/<key>`.

## 7. Key flows

**A - happy path.** Driver places a task needing tracker reads -> resolver
finds live parent grant (read, 7d) -> derive: child (read, 42min = deadline
40m + grace + margin) -> room boots, vsock delivers child token, staging file
deleted pre-agent -> agent GETs `$CUSTODY_BASE_TRACKER/rest/api/2/issue/PROJ-1`
with the child token -> custody validates chain, matches `read[0]`, injects the
PAT host-side, forwards -> witness records the tap frames -> run ends, teardown
-> adapter assembles the receipt, names it in `Result.Artifacts`, driver-state
records it.

**B - no live grant.** Resolver finds none covering `tracker/comment` ->
placement refuses before any room boots: exit 4 semantics with
`authority_unresolved` + remedy `custody grant -key tracker -actions comment
-ttl 8h`. The driver parks the stream the way gate parks a merge - a human
mints, the stream resumes. Nothing falls back to a raw secret.

**C - agent exceeds its scope.** Agent POSTs a comment holding a read-only
child -> custody `403 denied_no_action_match`, logged with the child's grant id
-> the vendor never sees the request. The overreach is visible twice: custody's
log line and, structurally, in the receipt's action set vs the driver record of
what the task claimed to need. No parent scope was ever in the room to abuse.

**D - room outlives the token.** Deadline overruns, cancel grace expires, TTL
lapses -> in-flight vendor calls start failing `401 refused_expired` -> rooms
teardown proceeds regardless; receipt records expiry-before-teardown. The
margin (D4) makes this rare; the failure mode is stalled work, never widened
authority.

**E - exfiltrated child token.** Suppose the agent leaks its token outward
(witnessed on the tap) or it lands in an artifact. Off-box the token is only
useful against custody's tap listener, which is unreachable off-host; on-box
it is scoped to granted actions and dead within the run bound. The receipt +
pcap make the leak *findable*; the attenuation makes it *small*. This is the
honest claim - containment of blast radius, not impossibility of leakage.

**F - crash mid-run.** Controller dies after boot -> `runway reconcile` takes
over, cleans the allocation, writes the terminal receipt; the adapter assembles
the authority receipt from what was durably recorded (derive record + whatever
artifacts collected), marking teardown from the reconcile outcome. A receipt
with `teardown: unknown` is a red flag the console can surface, not silence.

## 8. Concurrency / consistency / failure model

One derive per (run, secret ref); the derive record is written before boot
(refuse-before-evidence: no record, no room). Custody's proxy is stateless per
request as today; the tap listener shares the engine, so rule semantics cannot
diverge between listeners. Receipt assembly is idempotent from durable inputs -
re-running collection rewrites the same line. Custody down mid-run = vendor
calls fail closed inside the room; the room itself keeps running (agent decides
whether partial work is submittable - same posture as any upstream outage).

### 8.1 Threat model honesty

- **The child token appears in the witness pcap** (plain HTTP on the tap, by
  design - the witness must see everything). Accepted: the pcap is
  operator-owned evidence, and by the time anyone reads it the token is expired
  and its only usable environment destroyed. Do not treat pcaps as shareable
  artifacts within a token's TTL window.
- **In-guest exposure is narrowed, not eliminated.** The child token sits in
  the agent process's memory (rooms T3 residue). What changed: what's exposed
  is a scoped, expiring capability instead of the operator's identity.
- **Host compromise remains game over** - custody's store, the mint key, and
  rooms' host side all live there. Unchanged trust assumption, stated plainly.
- **Attribution is per-room, not per-actor.** The child token is a bearer
  capability inside its room; anything in the guest can use it. The receipt
  attributes authority to a run, which is exactly the granularity the driver
  ledger needs - not to a process within the guest.
- **The receipt is only as available as collection.** A hard host crash between
  derive and any durable artifact leaves a derive record and nothing else; §7 F
  bounds this to "visible gap", not "silent authority".

### 8.2 Settled in implementation, not prose

The tap listener's source-restriction mechanics (firewall vs bind-address
granularity, and their preflight checks); the margin constant in D4; the exact
receipt fields beyond §5's set (additive); how the resolver caches parent
lookups across a batch of streams; whether the guest env crosses via the
existing staging-file template or a second vsock payload. Each is cheaper to
learn in code against the e2e harness than to theorize here.

## 9. Rollout / implementation plan

| Phase | Goal | High-level tasks | Depends on | Gate |
|---|---|---|---|---|
| 1. `gmr-attenuation` | custody grants can chain, attenuated by law | `parent` field + pre-image + Version 2 (`cst2_`); `derive` verb with the three refusal classes; chain re-validation in `Validate`; property tests over the attenuation laws | - | - |
| 2. `gmr-receipt-contract` | the cross-repo receipt exists as a contract | `contracts/authority` schema + Go types + conformance fixtures; leaf-check wiring in hygiene | - | - |
| 3. `gmr-placement` | one real task runs with authority materialized | `custody:` ref resolver in the rooms adapter (refuse + remedy path); `-tap-addr` listener + runbook (bind refusals, firewall pins); receipt assembly at collection; e2e against the rooms-host | 1, 2 | **VALIDATION GATE** below |
| 4. `gmr-hardening` | evidence-driven only (stub) | teardown-triggered child revocation; allocation-id binding in the pre-image; console surfacing of `teardown: unknown` receipts; delegation depth > 1 | 3 + gate | each item needs a logged incident or review finding first |

Rough scope: phase 1 ~300 weighted LOC, phase 2 ~250, phase 3 ~450 (the
resolver and e2e are the bulk). Phase 4 is deliberately unsized.

**VALIDATION GATE (after phase 3):** one real driver task placed through
Runway onto rooms where (a) the guest held only a derived child grant - the
vendor secret appears nowhere in guest artifacts, the changeset, or the session
transcript (grep, zero hits); (b) an in-room over-scope request was refused by
custody and is visible in both custody's log and the receipt trail; (c) the
receipt chains parent -> child -> witness digests -> teardown in one line a
cold reader can follow. Phase 4 is not committed until this passes.

## 10. Open questions

1. **Does the tap listener need per-room grant pinning?** v0 scopes by subnet;
   a stricter form binds each child grant to its room's tap source address so a
   token replayed from a sibling room refuses. Cheap to add to the pre-image
   later (phase 4 allocation binding) - is subnet + TTL enough for v0?
2. **Receipt residence.** JSONL beside the run is v0; should the driver-state
   ledger ingest receipts as events (giving `driverstate render` the authority
   view), or is the artifact ref in `Result` enough until someone asks?
3. **Batch ergonomics.** A 5-stream driver run against one key derives five
   children from one parent. Fine by construction - but should the resolver
   coalesce (one child per run) or stay one-per-stream for cleaner receipts?

## 11. Validation plan

The §9 gate is the plan; its signals are binary and baseline-free: a grep with
zero hits (secret absence), a refused request present in two independent
records (enforcement), and one receipt line traversable by a cold reader
(explainability). No metric requires a baseline period, and every signal is
producible in a single gate-week run on the existing rooms-host.
