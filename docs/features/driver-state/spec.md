# driver-state — Technical Design Document

**Status:** draft / proposal — NOT a build commitment. The artifact we decide from.
**Owner:** @michael
**Date:** 2026-07-16
**Related:** `docs/DESIGN.md` (workbench charter), `contracts/` (verdict-v0.3.0 precedent), gate `docs/DESIGN.md` (artifact-ledger prior art), `pers/workbench-friction.md` 2026-07-15 entries (motivating evidence), dossier project `workbench`

> **Reviewers — focus areas:** §4 D1 (DECIDED by operator: the workbench ledger is the one canonical record; ship emits into it receipts-style — scrutinize the best-effort emission failure modes), §4 D2 (JSONL event ledger + reduce, NOT a SQLite mirror of ship's store), §7 flow 3 (session crash → resume from ledger alone), §8 (multi-writer locking on Windows + the TS-writable chain rule). The riskiest wrong turn is rebuilding ship's store in Go — §1 non-goals draws that line.

## 1. Problem & hypothesis

Ship's driver fuses two planes that the workbench charter says must stay separate: **Execution**
(spawn agents, poll runners) and **State** (the run/stream/attempt ledger). Consequences, all
observed in the 2026-07-15 dogfood run (friction log):

- Only ship's own engine loop can write the middle of a stream's lifecycle. An LLM session
  driving the same loop by hand (PR, reviews, gate, land) has no way to record what it did —
  its state lives in conversation context, the exact merge-tail-in-prose failure mode the
  agentic-workflow audit paid for.
- The recording surface an agent *can* reach is split: ship's MCP connector and terminal CLI
  read different stores (MSIX AppData virtualization), and the CLI path stacks shell friction
  (pnpm error misdirection, buffered ticks, a legacy row hard-failing `driver list`).
- gate, triage, and tracelens are migrating into workbench; ship will most likely NOT. Whatever
  contract records driver state must therefore outlive ship's store, not be trapped inside it.

**Hypothesis:** if the *transitions* of driver work become a typed, append-only event contract
owned by workbench — with a validating reducer and one MCP surface over one state dir — then the
executor becomes swappable (ship's engine, an LLM session, a human) without any reader
(`/wip`, `/shipped`, render, flare) caring who did the work. Prose shrinks, guarantees grow.

**Non-goals:**
- NOT a change to ship-driven /work-driver behavior. The regular ship-engine drive keeps
  working exactly as today — its engine, its verbs, its flow. Ship gains one additive,
  best-effort event emitter (§4 D1, §9 P5), the same grade as its existing receipts write;
  a ledger failure never fails a drive. Per-drive component choice is the point; replacing
  components is not.
- NOT a rebuild of ship's engine or store in Go. Ship keeps its SQLite store and its engine;
  this plane records *events about* driver work, it does not drive dispatch, poll runners, or
  merge PRs.
- NOT a generic workflow engine. The event vocabulary is exactly the driver lifecycle we run
  today — opinionated, ours.
- NOT a message bus / daemon. Writers append via CLI/MCP verbs; there is no long-running service.
- NOT (yet) ship emitting these events. A ship→driver-state bridge is post-gate (§9 P5).

## 2. Functional & non-functional requirements

**FR**
1. A writer can record a driver run (import) and every stream transition
   (`dispatched`, `attempt`, `pr_opened`, `landed`, `failed`, `skipped`, `merged`) as typed events.
2. Illegal transitions are rejected at write time with a structured error (the state machine
   validates even when it isn't driving).
3. A reader can reconstruct current run/stream state, and the full history, from the ledger alone.
4. All verbs are exposed over MCP (primary, for agents) and CLI (humans, cron) — same store,
   same validation, one binary.
5. A `/work-driver --engine session` run can complete a full task lifecycle writing only through
   this surface, and a killed session can resume from a fresh context using only ledger reads.

**NFR**

| Dimension | Target |
|---|---|
| Durability | fsync on append; a crash mid-write loses at most the in-flight event, never corrupts prior events |
| Consistency | single-writer per run enforced by lock file (gate's Windows-tested pattern); readers never block writers |
| Integrity | events are hash-chained per run (gate pattern); `verify` detects truncation/tampering |
| Latency | append + validate < 50 ms locally (it's a file append, not a service call) |
| Operability | one state dir `~/.workbench/driver-state/`; `explain <run>` reconstructs any run with zero other inputs |
| Compat | schema carries `v`; readers are tolerant (unknown event kinds skipped with warning, never hard-fail a listing — the `driver list` grok-4.5 lesson) |

## 3. Architecture overview

```
contracts/                       # leaf — types + JSON schema, no decision logic
  driverstate: Event, RunRecord, StreamRecord, kinds, schema (driver-state-v0.1.0)

driverstate/                     # shared-mechanism package (like local/): ledger + reducer
  append(event) -> validated, locked, hash-chained JSONL per run
  reduce(run)   -> RunState (current statuses, attempts, PRs)   # pure

cmd/workbench-mcp/               # the unified MCP surface (stdio server)
  driver_record / driver_state / driver_runs   (this feature)
  + read-only verbs from migrated tenants as they land (tracelens, triage, gate explain)

cmd/driverstate/                 # thin CLI over the same package (record | state | render | verify)
```

Writers: `/work-driver --engine session` (the LLM session, via MCP), a human (CLI), and
post-gate, optionally ship (bridge). Readers: `/wip`, `/shipped`, `driver render`-equivalent,
flare (tails the ledger the same way it tails gate's log). Everything composes via artifacts —
no tool imports another tool's decision logic (charter boundary law; the reducer lives in the
shared-mechanism package, which carries the *contract's* semantics, not any tool's policy).

What's reused: gate's proven mechanics (append-only JSONL, hash chain, lock-file multi-writer,
`explain` from artifacts alone), `contracts/` conformance-test pattern, the `local/`
shared-mechanism precedent. What's new: the event vocabulary, the reducer, the MCP server.

## 4. Key decisions & trade-offs

**D1 — the workbench ledger is THE driver-state record; every engine writes it (decided).**
Session-engine drives write it natively (their only store). Ship-engine drives keep their
flow and SQLite store untouched as *engine-internal working state*, and additionally emit
lifecycle events into the ledger **receipts-style: best-effort, never failing a tick on a
write error** — the exact pattern ship already uses for park receipts that flare tails, so
this is an extension of an existing seam, not a new kind of coupling. The ledger is the one
canonical read surface; ship's SQLite is consulted only when debugging the engine itself,
like any tool's internals.
Alternatives rejected: (a) beef up ship's MCP and make its SQLite the shared store — points
the dependency arrow at the TS repo staying outside the family, and makes ship load-bearing
even for shipless drives; (b) two authoritative stores + read-time join in `/wip`/`/shipped`
— no drift risk (disjoint ownership) but two formats, two mental models, two things to debug;
operator verdict: the unified record is the point of the unifying repo. Residual cost of the
chosen design: best-effort emission means a ship drive's ledger view can lag or hole on a
write failure — acceptable because ship's own store remains authoritative for its engine
loop, and `driver_verify` makes holes visible rather than silent.

**D2 — append-only event ledger + pure reducer, not a mutable-row store.**
Alternative: SQLite tables mirroring ship's `driver_runs/streams`. Rejected: events are the
source of truth the whole workbench thesis runs on (gate's explain/audit "from artifacts alone"
held under adversarial review); a reducer gives current state as a pure function; tolerant
reads fall out naturally. Cost: no ad-hoc SQL queries — acceptable at solo-operator volume.

**D3 — one MCP server for the workbench, verbs namespaced per tenant.**
Alternative: one MCP server per tool. Rejected: agents pay per-server connection/config
overhead, and the 2026-07-15 run showed agents driving via CLI is strictly worse (shell
quoting, pnpm misdirection, buffered output, exit-code-vs-JSON reconciliation). MCP is what a
driving agent prefers: schema-validated params, structured results, no shell. CLI remains for
humans and cron. History note: /work-driver standardized on the CLI as a *workaround* — ship's
MCP connector and terminal CLI resolved different stores (MSIX virtualization) and the MCP
surface lacked land/render/cancel, so mixing surfaces mid-run corrupted the picture. That was
an argument against ship-as-MCP, not against MCP: with this plane owning a single state dir and
full verb parity, the workaround retires and MCP returns to being the primary agent surface. Constraint carried from the capability plane: **mutating gate verbs (grant
minting) are NOT exposed over MCP** — grants stay a human CLI act; the MCP surface gets
read/record verbs only, per-verb allowlisted.

**D4 — session engine records through the plane; state never lives in context.**
`/work-driver --engine session` is a skill variant, not workbench code: the session executes
(worktree, subagents, PR, reviews, gate), and after every transition calls `driver_record`.
Resume = `driver_state <run>` in a fresh session. Declared scope: N≤3 streams, single writer
per run (the lock enforces it). If a session run needs tick leases or orphan re-attach, that's
the signal it belongs on ship's engine instead — not a feature request here.

**D5 — dispatch-policy hardening stays in ship (parallel track).**
`.ship.json` enforcement at `ShipService.startShip` (the chokepoint every dispatch funnels
through, closing the direct-`ship.ship` bypass) and a credential-source constraint (which
Claude token / gh account a repo's dispatches may use — this is the work machine; personal
Max-sub token and itsHabib gh auth must not leak into work-repo runs) are ship-repo changes.
They ride this TDD's rollout as P6 because they share the motivation, not the codebase.

**D6 — the review panel is per-repo contract, not skill prose.**
Today the reviewer set is hardcoded in /work-driver's text (@codex/@claude/@cursor +
Copilot) — wrong the moment a drive runs outside the personal repos. Work repos have
CodeRabbit only, auto-firing, with no mention-trigger and its own severity vocabulary.
The tail (and any consolidation step) must read the panel from repo-level config:
which reviewers exist, how each is triggered (`mention` | `auto` | `reviewer-request`),
and what "panel complete" means (which subset must report before consolidation, and a
per-reviewer latency budget after which the panel is degraded-but-settled). Proposed
shape (final home is an open question, §10 Q5):

```json
"review": {
  "panel": [ {"name": "coderabbit", "trigger": "auto"} ],
  "require": ["coderabbit"],
  "settle_minutes": 15
}
```

**No implicit default (operator decision 2026-07-16):** absent or empty `review` key
= the tail runs NO automated review step — no pings, no consolidation, review is
whatever humans do — and the drive records `review: unconfigured` in its state so the
omission is visible, never silently papered over with a built-in panel. Discipline
means following the contract that's there, not the one we remember. Consequence:
each personal repo declares its four-bot panel explicitly in its `.ship.json` (a
one-time ~10-line chore per repo, folded into P4's rollout). The panel-degraded
warning from the 2026-07-15 run (3 of 4 finders silent, discovered by waiting)
becomes computable: settled = every `require` reported or its budget expired.

## 5. Data model

Event (JSONL, one file per run: `~/.workbench/driver-state/<run_id>/events.jsonl`):

```go
type Event struct {
    ID      string          // evt_<ulid> — CLIENT-minted (idempotency key, see below)
    Run     string          // dsr_<ulid>  (driver-state run)
    V       string          // "driver-state-v0.1.0"
    Kind    Kind            // run_imported | stream_dispatched | stream_attempt |
                            // review_cycle | stream_pr_opened | stream_landed |
                            // stream_failed | stream_skipped | stream_merged | run_finished
    Stream  string          // dss_<ulid>; empty for run-scoped kinds
    Time    time.Time       // writer-supplied; monotonic within a run enforced by append
    Actor   string          // "session:<id>" | "ship:<drv_id>" | "human:mh"
    ExtRef  string          // optional top-level external correlate (e.g. ship drv_id on
                            // run_imported, PR URL on pr_opened) — top-level, not body,
                            // so cross-store queries don't parse every body (review: Q2)
    Body    json.RawMessage // kind-specific payload, schema-validated
    Prev    string          // prior event's Hash; "" for the first event
    Hash    string          // SHA-256 over the canonical encoding (see below) with Hash ""
}
```

**Canonical encoding, pinned (review M3 — a P1 deliverable, not an open question):**
UTF-8 JSON, fields in the order documented by the contract schema (= Go struct
declaration order), no insignificant whitespace, `Body` hashed as its raw bytes
verbatim (never re-marshalled — the writer's bytes are the canonical bytes). The
contract ships a **reference test vector** (one full event + its expected hash) that
every implementation — Go package and ship's TS emitter alike — must reproduce; the
conformance suite fails if they don't. This is what keeps a cross-language chain from
breaking silently.

**Idempotency (review finding, committed):** `ID` is minted by the *writer*, and `Append`
is idempotent by it — re-appending an ID already in the ledger returns the original
committed event instead of duplicating. This is what makes the at-least-once writer model
(§8) safe for terminal events and lost MCP responses. `run_imported` additionally dedupes
on `(repo, source, generated_at)` — ship's proven import key — so a retried import cannot
mint a second run.

**`review_cycle` is a first-class kind from v0.1.0 (review finding, committed):** cycles
are retroactively unreconstructable from coarse attempt snapshots, and the morning-queue
reader wants "3 cycles" vs "landed first try." Body: `{cycle, panel_settled, findings}`.

Kind payloads (schema-enforced): `run_imported` carries the manifest snapshot (repo, source,
batches/streams — the `driver.md` frontmatter, verbatim, so render round-trips);
`stream_attempt` carries `{seq, doc_path, terminal, failure_category?}` (append-only ledger
semantics — seq must increase); `stream_pr_opened` `{pr, url, head_sha}`; `stream_merged`
`{pr, merge_commit, merged_at}`. Reducer output:

```go
type RunState struct {
    Run      RunRecord            // repo, source, status (derived), imported_at
    Streams  map[string]StreamRecord // status, attempts[], pr, merge_commit — derived only
}
```

Status is always derived by `reduce`, never stored — there is no row to drift.

Legal transitions — the single authoritative table (review finding: v1's prose sequence
contradicted F1; this table wins over any prose):

| From | Event | To |
|---|---|---|
| `pending` | `stream_dispatched` | `dispatched` |
| `dispatched` | `stream_attempt` (non-terminal) | `dispatched` |
| `dispatched` | `stream_attempt` (terminal, ok) | `landed` |
| `dispatched` | `stream_attempt` (terminal, failure_category set) | `failed` |
| `dispatched` | `stream_failed` | `failed` |
| `landed` | `stream_pr_opened` | `pr_open` |
| `pr_open` | `review_cycle` | `pr_open` |
| `pr_open` | `stream_merged` | `merged` |
| `failed` | `stream_dispatched` (retry) | `dispatched` |
| `pending`, `failed` | `stream_skipped` | `skipped` |

`landed` = the executor finished producing work (ship's meaning); `pr_open` sits between
landing and merging. A terminal attempt is a single write: `failure_category` set → the
`failed` transition; absent → `landed` (no second `stream_failed` event required).
`run_finished` is valid when every stream is in `{merged, skipped, failed}`; emitting it
closes the run and the reducer treats `failed` streams in a finished run as non-retriable
— finishing IS the no-retry declaration, no separate abandon event.

## 6. API contract

`contracts/driverstate` (leaf): the types above + embedded JSON schema + `Envelope`-style
tolerant reader + conformance tests (schema↔Go parity, enum↔const parity — same trio as
verdict-v0.3.0).

`driverstate` package (shared mechanism):

```go
func Claim(dir, run, actor string) (Lease, error)     // durable run ownership; ErrLocked{Holder} if held
func (l Lease) Renew() error                          // heartbeat; stale = expired lease, not just PID
func (l Lease) Release() error
func Append(dir string, l Lease, e Event) (Event, error) // requires a live lease; idempotent by e.ID
func Reduce(dir, run string) (RunState, error)        // pure fold; unknown KINDS tolerated; chain break = error
// Reduce initializes RunState.Streams from run_imported.Body.streams and overlays
// statuses from subsequent events — a stream with no events yet is `pending`, so a
// resuming session always sees the full stream set, not just the ones that got events.
func Runs(dir string) ([]RunSummary, error)           // never hard-fails on one bad run (tolerant listing)
func Verify(dir, run string) error                    // chain integrity
```

`Append` semantics, nailed down per review: acquire the append lock → **truncate any torn
tail to the last verified newline** (a crash's partial line must not corrupt the next
event) → read the head **inside the lock** (chain, time-monotonicity, and `stream_attempt`
seq validation all use this read) → write + fsync → release. The run *lease* (Claim) is
what enforces single-writer-per-run across a whole session — the per-append lock alone
only prevents byte races, it cannot deliver F4's `ErrLocked` promise. Lease file carries
`{actor, pid, expires_at}`; staleness = expiry (inherit gate's threshold as the default,
configurable), so a killed session's lease self-clears within one threshold window.

Errors are values with stable codes: `ErrIllegalTransition{From, Event}`, `ErrChainBroken`,
`ErrLocked`. No panics, no silent skips on write paths.

MCP verbs (`cmd/workbench-mcp`, stdio):

| Verb | In | Out |
|---|---|---|
| `driver_record` | `{run?, event}` (run omitted on `run_imported` → minted) | the appended event (id, hash) or structured error |
| `driver_state` | `{run}` | `RunState` |
| `driver_runs` | `{repo?, live?}` | `[]RunSummary` |
| `driver_verify` | `{run}` | ok / `ErrChainBroken` detail |

Lease lifecycle over MCP (review M2): the server holds the active `Lease` and
**auto-renews it on a background goroutine at interval = staleness-threshold / 2** for as
long as the client session is connected — a session parked for hours on CI or panel
settlement keeps its lease without a verb call, and there is deliberately no
`driver_renew` verb. Server exit (stdio close) stops renewal, so an orphaned lease
self-expires within one threshold window.

CLI mirrors 1:1 (`workbench driverstate record|state|runs|verify`, `--json`). Server
registration: `.mcp.json` (project or user scope), `WORKBENCH_STATE_DIR` flowing through
the server env. **State-root resolution is canonical, not ambient** (review P2): the
server resolves the state dir once at startup — explicit env var, else the real
(non-virtualized) user profile — and *prints the resolved path at startup*; two MCP
instances resolving different roots is the ship failure mode this exists to kill, so the
§11 gate includes a cross-client check. Verb exposure is compile-time registration in
`cmd/workbench-mcp` (opt-in per tenant; capability-mutating verbs excluded by
construction); unknown verbs return MCP `MethodNotFound`.

## 7. Key flows

**F1 — session-engine happy path.** Session imports a `driver.md` → `driver_record run_imported`
(manifest snapshot in body) → creates worktree, spawns impl subagent → `stream_dispatched` →
subagent lands commit → `stream_attempt{terminal:true}` (stream now `landed`) → session
pushes, opens PR → `stream_pr_opened` → each panel round records a `review_cycle` →
gate/merge in the existing tail → `stream_merged` → `run_finished`. At no point is stream
status held only in context.

**F2 — illegal write.** Session (confused after compaction) records `stream_merged` on a stream
whose ledger says `dispatched` with no `pr_opened`. Append rejects with
`ErrIllegalTransition{from: dispatched, event: stream_merged}` — the contract corrects the
agent, mirror-image of ship's engine correcting a manifest. The session re-reads
`driver_state` and reconciles. This rejection is the plane's whole value: a validator that
works even when it isn't the driver.

**F3 — crash and resume.** Session dies mid-run (context loss, reboot). A fresh session runs
`driver_runs {live:true}` → finds the run → `driver_state` → sees stream A merged, stream B
`pr_open` (PR #12) → resumes the tail for B only. Nothing is re-derived from prose; the
2026-07-15 park-and-resume worked exactly this way against gate's log + dossier notes — this
makes that recovery a first-class read instead of archaeology.
Two review-mandated rules that make this honest:
- **The ledger says where to look, not what's true.** A crash can land *between* an external
  action and its record (PR opened but never recorded; merge clicked but `stream_merged`
  lost). On resume the session RECONCILES: for each non-terminal stream, check the external
  facts (branch exists? PR state? merge commit?) against ledger state, record the missing
  events (idempotent IDs make this safe), and only then continue. Resume = read ledger →
  verify world → record deltas → proceed. Never act on ledger state alone.
- **A run with only `run_imported` resumes from the manifest snapshot** in that event's body
  (the one place a body payload drives control flow), re-running dispatch for every stream.
A resumed session appends under a new `session:` actor — two actors on one run is the audit
trail working, not an anomaly (render/flare must not treat it as one).

**F4 — concurrent writer (the degraded mode).** A second session calls `Claim` on a run whose
lease is live: fails fast with `ErrLocked{Holder}` naming the holding actor. No queueing, no
merge — single-writer is declared scope (§4 D4), and it's the *lease*, not the per-append
lock, that enforces it (review P1). The Windows delete-pending → retry-everything lesson from
gate's lock applies verbatim.

**F5 — tolerant read over a bad ledger.** One run's chain is broken (disk hiccup, hand edit).
`driver_runs` still lists every other run and flags the bad one (`status: corrupt`) —
listing never hard-fails on one row. `driver_verify` gives the detail. This is the direct fix
for the `driver list` grok-4.5 failure class.

## 8. Concurrency / consistency / failure model

- **Single-writer per run via a durable lease** (`Claim`/`Renew`/`Release`, lease file with
  actor+pid+expiry, gate's Windows delete-pending retry) — scope is the run dir, so
  concurrent sessions on *different* runs write freely. The per-append lock inside a lease
  handles byte-level safety: truncate torn tail, read head, validate, write, fsync — all in
  one lock window.
- **Readers are lock-free.** A torn *final* line is discarded with a warning. A break
  *mid-chain* is an error: `Reduce` fails loudly (never silently truncates — a swallowed
  `stream_merged` in a truncated tail would re-drive a merged PR); recovery is `Verify` +
  operator, not automatic.
- **Writer-supplied time, append-enforced monotonicity** per run (reject an event older than
  the head — catches clock skew and replayed writes).
- **At-least-once writers, idempotent appends:** event IDs are client-minted; a retried
  append returns the original committed event. Belt-and-braces rule in the skill text:
  before any terminal event, re-read `driver_state` and reconcile external facts (§7 F3).
- **Schema tolerance in both directions:** unknown event kinds are skipped-with-warning on
  read; body types use tolerant decoding so field *additions* are never breaking.
- **Dependency-down:** there are no dependencies — no network, no daemon. The failure surface
  is the filesystem, and the answer is fsync + chain verify.

## 9. Rollout / implementation plan

| # | Phase | Goal | High-level tasks | Depends on | Gate | ~Weighted LOC |
|---|---|---|---|---|---|---|
| P1 | `driver-state-contract` | Event/RunState types + schema + conformance tests in `contracts/` | types; embedded schema; parity tests; tolerant reader | — | — | ~350 |
| P2 | `driver-state-ledger` | `driverstate/` package: Append/Reduce/Runs/Verify + lock + chain | append+validate; reducer; tolerant listing; verify; lock (reuse gate pattern) | P1 | — | ~600 (split into 2 PRs: write path / read path) |
| P3 | `workbench-mcp-v0` | `cmd/workbench-mcp` stdio server exposing the four driver verbs + `cmd/driverstate` CLI | MCP server scaffold; verb handlers; CLI mirror; state-dir config | P2 | **VALIDATION GATE** (§11) | ~450 |
| P4 | `session-engine-skill` | `/work-driver --engine session` skill variant recording through MCP | skill text; resume flow; N≤3 scope; grant-resolution step (pre-minted, never mint); panel-from-config tail (§4 D6, CodeRabbit-only work repos); thin jira-epic ingestion (epic → dossier tasks) for the work demo | P3 gate | — | skill prose + panel config schema, ~150 |
| P5 | `ship-emitter` (committed) | ship emits lifecycle events into the ledger, receipts-style | TS emitter in ship repo writing the JSON contract (best-effort, never fails a tick); `/wip`/`/shipped` repointed at the ledger as the one read surface | P2 + P3 gate | — | ~250 (ship repo) |
| P6 | `ship-policy-hardening` (parallel, ship repo) | `.ship.json` enforced at `ShipService.startShip`; credential-source constraint (`claude` token source + gh account per repo) | ShipService check; policy `credentials` key; tests | — (independent) | — | ~300 |

Phases P1–P3 are the committed spine (this week's target: P1+P2 moving, P3 opened). P4 is
cheap once P3 exists. P5 is committed (operator decision 2026-07-16: one canonical record in
the unifying repo) but sequenced after the P3 gate — don't wire ship into an unproven contract.
Note for P1/P2: the ledger's on-disk format (canonical JSON, hash rule) must be writable from
TS as well as Go — keep the chain rule dead simple and document it in the contract, since ship's
emitter implements it independently. P6 can run in parallel any time via the ship repo.

## 10. Open questions

1. ~~Event granularity~~ **Resolved (review, 2026-07-16): `review_cycle` is a first-class
   kind from v0.1.0** — cycles are retroactively unreconstructable from coarse attempts.
2. ~~`run_id` ↔ ship `drv_id` correlation~~ **Resolved (review, 2026-07-16): top-level
   optional `ExtRef` field** — promoting it later would be a breaking change; body-level
   would cost O(N·parse) on cross-store queries.
3. **flare integration depth:** does flare tail this ledger in v0 (one more source in its
   config), or wait for a real escalation kind (`stream_failed` with `needs_judgment`)?
4. **MCP server hosting for Desktop:** Claude Code terminal sessions get the real state dir;
   Claude Desktop connectors see virtualized AppData (MSIX memory). Ship `WORKBENCH_STATE_DIR`
   in v0 and document, or refuse to run under a virtualized root?
5. ~~Where does the review-panel config (§4 D6) live?~~ **Resolved (operator,
   2026-07-16): extend `.ship.json`** — one repo-policy file, discovery/validation
   already built; the ship-flavored name is accepted cost (rename is a one-commit
   mechanical sweep later if it ever grates).

## 11. Validation plan

The P3 gate, binary and baseline-free: **one dogfood run — a real dossier task driven by
`/work-driver --engine session` end-to-end (dispatch → PR → reviews → gate → merge) where (a)
every transition was written through `workbench-mcp`, (b) mid-run the session is killed and a
fresh session resumes correctly via ledger-read + external reconcile (§7 F3) — including at
least one kill placed BETWEEN an external action and its record, the hard case, and (c)
`driverstate render` of the finished run matches what actually happened on GitHub.** Pass →
P4/P5 unlock. Fail on (b) → the state model is wrong, stop and redesign before any more
phases. Secondary criteria (review-promoted): a terminal Claude Code client and a Desktop
connector both resolve the SAME state root (the MSIX assumption, tested hands-on, not
assumed); zero shell-friction entries in the friction log for the recording path (the
MCP-vs-CLI bet).
