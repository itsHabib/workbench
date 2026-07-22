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
  placement - no `custody:` ref ever falls back to a raw secret. This requires
  extending the execution contract's secret-ref grammar: today
  `contracts/execution` validates `Secret.Ref` against `^env:NAME$` in both the
  validator and the JSON schema, so a `custody:` ref would refuse at Runway
  admission before any resolver ran. The grammar gains the `custody:` scheme as
  a minor, additive schema revision (P2) - existing `env:` refs are untouched.
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
operator-minted roots) and `bound_source string` (see D2b). `derive` refuses
unless child.actions is a subset of parent.actions, child expiry is at or
before parent expiry, and the parent itself validates live at derive time. The
signing pre-image extends to cover both new fields, which per custody's own
rule means a Version bump (`cst2_` prefix); grants are short-lived and
mint-fresh across it, so migration is re-mint, not rewrite. The complete
pre-image enumeration lives where it does today - the `sign` doc comment in
`cmd/custody/internal/grant` - and extends only alongside the Version bump.
Depth is capped at 1 in v0, enforced in *both* directions: `derive` refuses a
parent that itself has a parent, and `Validate` independently rejects any
presented chain where the parent record carries a non-empty `parent` - a
depth-2 token assembled outside `derive` (old binary, hand-built record)
refuses at the proxy, not just at mint. Per-request validation stays pure
crypto + local records: expiry and scope are in the signed pre-image, so
`Validate` never makes a live parent lookup on the hot path - custody's store
being briefly unavailable cannot fail in-flight proxied calls for non-expired
tokens. Alternative rejected: modeling the child as a separate record type -
two validation paths for one trust question; the chain belongs in the one
envelope.

**D2 - guest reaches custody over the room's existing tap; custody grows a
second, explicitly-configured listener. (RECOMMENDED, reviewers weigh in.)**
The guest's default gateway *is* the host's tap IP, so reach requires zero new
rooms mechanism - custody binds the tap gateway address (`-addr` exists) and
the runbook pins firewall rules restricting that listener to the room subnet
and custody's port. Bonus: custody traffic transits the tap, so the witness
pcap records every authorized call alongside any unauthorized egress - the
same artifact shows both. Cost: custody's v0 NFR said 127.0.0.1 only; this
loosens it, deliberately and only for an explicitly-flagged second listener
(`custody serve -tap-addr <gw:port>`, refusing wildcard binds and any address
not on a tap interface - "tap interface" pinned in implementation as an
interface-name-prefix check with an override flag, settled against the
rooms-host). The firewall pin is a *process guard, not a runbook promise*:
`-tap-addr` startup fails closed unless it can verify the expected source
restriction is in force (probe the ruleset for the pinned rule; refuse to
serve otherwise) - a runbook step that can be skipped is structurally weaker
than a startup check that cannot. Alternative: a vsock TCP bridge (guest-side
forwarder, host-side vsock->localhost proxy) keeps custody localhost-only but
adds a guest agent + a host proxy - two new moving parts to avoid one flagged
bind. Rejected for v0; revisit if the tap listener's source-restriction proves
fragile in the runbook.

**D2b - child grants are source-bound; sibling-room replay refuses. (Promoted
to v0 by review.)** Concurrent rooms share the tap subnet, so the listener is
*not* single-room-reachable: without binding, room A could replay room B's
child token until expiry, and custody would attribute the borrowed calls to
room B's run - breaking both containment and the receipt's attribution claim.
So the resolver stamps the room's tap source address into the child grant
(`bound_source`, in the signed pre-image, D1), and the tap listener refuses a
request whose transport source does not match the presented grant's binding
(`refused_source_mismatch`). Grants without a binding refuse on the tap
listener outright - localhost callers keep using unbound grants; room-bound
grants are useless off their room. This also defuses the pcap-live-token case
(§8.1): a token lifted from evidence replays from nowhere except the one room
that is already authorized. Spoofing the source requires host-level access,
which is already game over (§8.1). Cost: one signed field plus one listener
check; this was P4's "allocation binding" - two independent reviewers showed
v0's claims don't hold without it, so it stops being optional.

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
    Parent      string `json:"parent,omitempty"`       // grant id this was derived from; "" = operator-minted root
    BoundSource string `json:"bound_source,omitempty"` // transport source this grant is usable from; "" = unbound (localhost listener only)
}
```

Both fields join the signing pre-image. Attenuation laws (§4 D1) are enforced
by `derive`, re-checked by `Validate` when a chain is presented; source
binding (§4 D2b) is enforced by the tap listener per request.

**Secret ref grammar** (contract change, P2): `contracts/execution` today
constrains `Secret.Ref` to `^env:NAME$` in validator and schema; the grammar
gains `custody:<key>/<action>[,<action>...]` as an additive revision - e.g.
`{"name": "CUSTODY_GRANT_TRACKER", "ref": "custody:tracker/read,comment"}`.
`env:` refs flow through the existing raw-secret path untouched.

**room-authority receipt** (`contracts/authority`, `authority-receipt.v1`,
JSONL, exactly one line per placed run that carried at least one `custody:`
ref; a run's independent refs are entries in `grants[]`, so multi-secret runs
neither drop authority nor split into multiple lines). All timestamps are
RFC 3339 UTC. `teardown.status` is a closed enum: `destroyed | failed |
unknown`.

```jsonc
{
  "schema_version": "authority-receipt.v1",
  "run_id": "run_...",              // runway run
  "allocation_id": "...",           // rooms allocation from PlacementReceipt
  "grants": [                       // one entry per resolved custody: ref (§8: one derive per (run, ref))
    {
      "secret_name": "CUSTODY_GRANT_TRACKER",
      "key": "tracker",
      "parent_id": "…", "parent_digest": "sha256:…",
      "parent_actions": ["read", "comment"],   // attenuation visible in-receipt: no external lookup needed
      "child_id": "…",  "child_digest": "sha256:…",
      "actions": ["read"],
      "bound_source": "172.30.0.7",
      "minted_at": "…", "expiry": "…",
      "delivery": { "channel": "vsock", "delivered_at": "…", "one_shot": true }
    }
  ],
  "evidence": {
    "artifacts": [                  // digest refs into Result.Artifacts; open type vocabulary so
      { "type": "witness_pcap", "sha256": "…" },   // rooms can evolve artifact naming without a schema rev
      { "type": "witness_json", "sha256": "…" },
      { "type": "changeset",    "sha256": "…" }
    ],
    // The unambiguous selector over custody's global, interleaved JSONL is the
    // child grant id (unique per (run, ref)): filter log lines on grant_id ==
    // child_id. request_count + the digest of the selected lines pin what the
    // selector returned at assembly time, so later log tampering is detectable
    // against the receipt.
    "custody_log": [ { "child_id": "…", "request_count": 17, "lines_sha256": "…" } ]
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
custody derive -grant <cst2_parent-token> -actions a[,b] -ttl <d> [-bound-source <ip>]   -> cst2_child-token
    refusals (coded): parent invalid/expired -> the existing four classes;
    actions not a subset      -> refused_attenuation_actions
    ttl beyond parent expiry  -> refused_attenuation_ttl
    parent has a parent       -> refused_chain_depth
custody serve -addr 127.0.0.1:8127 [-tap-addr <gw-ip>:8127]
    -tap-addr refuses 0.0.0.0 / :: and any address not on a tap interface,
    and fails closed at startup unless the pinned source-restriction rule is
    verifiably in force (§4 D2)
    per-request on the tap listener: grant unbound, or transport source !=
    grant.bound_source -> refused_source_mismatch (§4 D2b)
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
(witnessed on the tap) or it lands in an artifact. The token is only
accepted by custody's tap listener, whose reach off-host is closed by the
startup-verified bind + firewall rules (a deployment property the D2 preflight
guard makes fail-closed, not an assumption) - and even on-subnet, the source
binding (D2b) means it replays from nowhere except the one room already
authorized to hold it, until that room's teardown. What remains is scoped to
granted actions and dead within the run bound. The receipt + pcap make the
leak *findable*; the attenuation + binding make it *small*. This is the honest
claim - containment of blast radius, not impossibility of leakage.

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
  design - the witness must see everything), **and it can still be live when
  collected**: a workload that finishes early publishes artifacts while the
  run-capped TTL has time left. The claim is therefore *not* "expired by read
  time". The backstops are structural: the token is bound to its (now
  torn-down) room's source (D2b) and the tap listener is unreachable off-host
  by the startup-verified bind rules - a token read out of a pcap replays from
  nowhere that custody accepts. Still: do not treat pcaps as shareable
  artifacts within a token's TTL window; defense in depth, not a substitute
  for it.
- **The tap listener is subnet-reachable, not single-room-reachable.** Every
  concurrent room shares the tap subnet and can carry frames to the listener;
  what stops a sibling from *using* another room's authority is the per-request
  source check (D2b), not network unreachability. Stated here so the D2
  security argument is read correctly.
- **In-guest exposure is narrowed, not eliminated.** The child token sits in
  the agent process's memory (rooms T3 residue). What changed: what's exposed
  is a scoped, expiring capability instead of the operator's identity.
- **Host compromise remains game over** - custody's store, the mint key, and
  rooms' host side all live there. This also bounds the *evidence*: the pcap
  and the receipt's digests over it are trustworthy exactly as far as the host
  is - `evidence.artifacts[].sha256` inherits host trust, it does not add to
  it. Unchanged trust assumption, stated plainly.
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
| 1. `gmr-attenuation` | custody grants can chain, attenuated by law | `parent` + `bound_source` fields + pre-image + Version 2 (`cst2_`); `derive` verb with the three refusal classes; chain re-validation + depth-cap rejection in `Validate`; property tests over the attenuation laws | - | - |
| 2. `gmr-receipt-contract` | the cross-repo contracts exist | `contracts/authority` schema + Go types + conformance fixtures; leaf-check wiring in hygiene; `contracts/execution` secret-ref grammar gains the `custody:` scheme (additive revision) | - | - |
| 3. `gmr-placement` | one real task runs with authority materialized | `custody:` ref resolver in the rooms adapter (refuse + remedy path, source-bound derive); `-tap-addr` listener with startup preflight guard + `refused_source_mismatch` + runbook; receipt assembly at collection; e2e against the rooms-host | 1, 2 | **VALIDATION GATE** below |
| 4. `gmr-hardening` | evidence-driven only (stub) | teardown-triggered child revocation; console surfacing of `teardown: unknown` receipts; delegation depth > 1 | 3 + gate | each item needs a logged incident or review finding first |

Rough scope: phase 1 ~350 weighted LOC, phase 2 ~300, phase 3 ~500 (the
resolver, source enforcement, and e2e are the bulk). Phase 4 is deliberately
unsized. (Allocation/source binding was P4 work in v1 of this doc; review
showed v0's containment and attribution claims don't hold without it - D2b.)

**VALIDATION GATE (after phase 3):** one real driver task placed through
Runway onto rooms where (a) the guest held only a derived child grant - the
vendor secret appears nowhere in guest artifacts, the changeset, or the session
transcript (grep, zero hits); (b) an in-room over-scope request was refused by
custody and is visible in both custody's log and the receipt trail; (c) the
receipt chains parent -> child -> witness digests -> teardown in one line a
cold reader can follow. Phase 4 is not committed until this passes.

## 10. Open questions

1. ~~Does the tap listener need per-room grant pinning?~~ **Resolved by
   review: yes, in v0.** Two reviewers independently showed subnet + TTL is
   insufficient (sibling replay breaks containment *and* receipt attribution).
   Now §4 D2b; the open remainder is only the margin/mechanics noted in §8.2.
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
