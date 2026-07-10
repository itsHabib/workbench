# Agentic Workbench Closure — Technical Design Document

**Status:** draft / proposal — **NOT a build commitment.** This is the artifact we decide from.
**Owner:** @itsHabib
**Date:** 2026-07-10
**Revision:** v0 — first Codex-authored synthesis of the post-C10 kickoff, the Fable-era redesign, its red-team, the current Workbench charter, and the operator's two workbench essays.
**Related:** [`docs/DESIGN.md`](../../DESIGN.md), [`FOLLOWUPS.md`](../../../FOLLOWUPS.md), `pers/docs/kickoff-post-c10-gpt56-2026-07-10.md`, `pers/workbench-redesign/DESIGN.md`, `pers/workbench-redesign/VERDICT.md`, `pers/workbench-redesign/RED-TEAM.md`, `pers/writer/drafts/embrace-the-slop.md`, `pers/writer/drafts/rebuilding-the-workbench.md`.

> **Reviewers — focus areas:** (1) the canonical-source and collision rules for Claude/Codex skills in §4 D1–D3, (2) whether `ReviewFindingsV1` is the smallest sufficient cross-harness seam in §5.2, (3) the Phase 1 binary validation gate in §9/§11, and (4) whether Gate should graduate into Workbench rather than force `contracts` into a separate module (§4 D5).

---

## 1. Problem & hypothesis

The workbench has moved in the right direction:

1. large orchestration applications (`cortex`, `orchestra`) gave way to small tools the model composes;
2. recurring choreography moved from long skills into Ship's durable driver engine;
3. verification became evidence-producing agents plus deterministic tests rather than operator diff-reading;
4. the Fable-era redesign moved safety-critical policy from prose into artifacts, verdict reducers, grants, and exit-code gates;
5. the new Workbench repo began consolidating the Go family around one law: **share contracts, not call stacks**.

The remaining problem is closure, not another architecture. The current system is transitional in five material ways:

- **Harness drift.** The operator's Claude catalog has 36 personal skills; Codex had 14 before this TDD session. Twenty-three missing Claude skills were copied into Codex as a local baseline, while 13 same-name collisions were deliberately preserved. The installed `work-driver` variants materially differ and Codex has no native `review-coordinator` variant yet. Installed directories are acting as competing sources of truth.
- **The review-fix loop is not yet one portable path.** Ship's C10 `driver address` verb exists, but its payoff still depends on Claude-specific skill choreography. A Codex seat can shell to the CLI, yet cannot consume the same reviewed workflow as a first-class surface.
- **The verdict contract has two homes.** `workbench/contracts` publishes the mirrored type/schema while Gate's private `internal/verify` type and reducer remain the behavioral source of truth. They match today, but no test spans the repositories.
- **Capability is only partially enforcing.** A local grant disciplines Gate's sanctioned path and creates an audit trail; it does not stop an agent holding a merge-capable GitHub token from bypassing Gate. Branch rules and credential custody are the actual enforcement boundary.
- **Review and local-routing policies are still exploratory.** Four reviewers every cycle is expensive and failure-prone; correlated review is not the same as an adversarial invariant pass. “Enum-valid” local output verifies shape, not semantic correctness. Self-reported confidence is weak evidence.

**Hypothesis:** the workbench becomes trustworthy across model generations when its durable center is a small set of versioned artifacts and engine guarantees, while harness-specific skills are installed projections over one canonical workflow. If Claude and Codex can each drive the same real review-fix-to-land loop, recover from provider and host failures, and produce comparable receipts without manual branch surgery, the redesign has crossed from a good personal workflow into a portable agentic workbench.

### Goals

- Make Claude and Codex first-class seats over the same workflow semantics.
- Close the C10 review-fix loop through a normalized artifact instead of a Claude-only call chain.
- Make long local runs survive active work, host sleep, process restart, and expiring credentials without silently lying about terminal state.
- Converge the verdict type/schema to one structural source of truth.
- Make Gate enforcement honest and externally enforceable.
- Replace static reviewer volume with risk-scaled, posture-diverse verification.
- Measure the closed loop before adding another plane, daemon, MCP, or state store.

### Non-goals

- Rebuilding the five redesign planes as services or packages.
- A new orchestrator, chat surface, dashboard, or “god app.”
- Physically unifying Dossier, Ship, Gate, and Flare state stores.
- Rewriting Ship from TypeScript for architectural symmetry.
- Making every Claude-only UI/session skill behave identically in Codex where the product primitive differs.
- Eliminating PRs. PRs remain the change transaction, CI/status-check boundary, and audit thread; human diff-reading is no longer their sole purpose.

## 2. Functional & non-functional requirements

### Functional requirements

- **FR1 — Catalog:** every personal skill has one catalog entry with owner, canonical source, visibility, supported harnesses, compatibility state, and installation policy.
- **FR2 — Collision safety:** an import/sync operation never overwrites a same-name target silently. It reports `identical`, `target-adapted`, `source-newer`, `target-newer`, or `manual`.
- **FR3 — Repo-first authorship:** `~/.claude/skills` and `~/.codex/skills` are installed projections. Durable edits originate in a versioned repository.
- **FR4 — Portable workflow contract:** harness-native skills share artifact schemas, commands, outcomes, and failure semantics without assuming identical prose or lifecycle primitives. Claude `Workflow`/chips and Codex thread/plugin operations remain native implementation details.
- **FR5 — Review artifact:** reviewer/coordinator implementations emit one versioned `ReviewFindingsV1` artifact. `driver address` consumes its file path without knowing which harness produced it.
- **FR6 — C10 closure:** a cloud PR with actionable findings can go review → address → re-tick → re-review → gate → land without manual checkout/push of its PR branch.
- **FR7 — Long-run liveness:** active remote/provider progress resets inactivity; local event-pump heartbeats do not. Startup, inactivity, expected-duration, and absolute-backstop budgets are distinct.
- **FR8 — Credential refresh:** LocalClaudeRunner obtains renewable subscription credentials at the latest safe boundary without logging or persisting the token; explicit environment credentials remain supported for gateways/CI.
- **FR9 — MCP parity by demand:** `driver_address` gains an MCP surface after the CLI path proves itself. Historical parity verbs remain separately demand-gated.
- **FR10 — Contract convergence:** one Go `Verdict` type and one embedded JSON Schema are structurally authoritative; Gate's reducer owns behavior over that shared type.
- **FR11 — Enforced merge path:** an automated merge is possible only when GitHub requires the Gate result and the governed agents do not possess a bypass credential.
- **FR12 — Risk-scaled review:** review posture depends on risk/invariant class; reviewer count alone is never the safety argument.
- **FR13 — Evidence-shaped local work:** local-model output may influence a gate only when semantic evidence is deterministically checked or error is escalate-safe. Schema validity and self-confidence alone never authorize an effect.
- **FR14 — Receipts:** every closed-loop run records harness, model, effort, provider, review cycles, human interventions, failure/recovery events, and final Git/GitHub refs.

### Non-functional requirements

| Property | Target |
|---|---|
| Safety | Unknown skill collision, verdict field/class, reviewer absence, remote liveness, or Gate state fails closed; absence never reads as green. |
| Portability | Claude and Codex can both perform the Phase 1 flow from a fresh session using documented/installable surfaces. |
| Recovery | A process restart preserves the latest meaningful remote progress and does not reset an inactivity budget or lose the PR branch. |
| Determinism | Skill sync `--check`, schema conformance, reducer laws, and fault-injection tests produce machine-readable verdicts. |
| Auditability | A run is explainable from artifacts/receipts plus GitHub state; no required decision exists only in chat prose. |
| Operability | No new daemon for skill sync or the review seam. CLI/script first; MCP only after live pull. |
| Cost | Static four-reviewer panels are not required for low-risk work. Premium judgment is reserved for ambiguity, disagreement, or high-risk gates. |
| Compatibility | Importing Claude skills never damages Codex-native variants; incompatible skills are visible as such rather than half-working silently. |
| Scope | Each implementation PR targets <700 weighted LOC; >700 requires a no-split argument or another phase. |

## 3. Architecture overview

```text
                         operator
                            │
             ┌──────────────┴──────────────┐
             ▼                             ▼
       Claude harness                 Codex harness
             │                             │
             └──────────┬──────────────────┘
                        ▼
          target adapter + installed skill projection
                        │
                        ▼
        portable workflow contract (artifacts + CLI semantics)
                 │          │          │
                 ▼          ▼          ▼
              Dossier      Ship       GitHub
             (work index) (execution) (change transaction)
                              │             │
                              ▼             ▼
                    ReviewFindingsV1 → Gate verdict/grant
                              │             │
                              └──────┬──────┘
                                     ▼
                              land + receipts
                                     │
                                     ▼
                              Flare / status views
```

The diagram is deliberately not a new runtime platform. Existing tools remain independent binaries/repos until migration is earned. They compose through commands, exit codes, and artifacts. The Workbench Go module shares vocabulary and pure mechanisms; it does not become the owner of cross-tool decision paths.

### New surfaces

1. **Skill catalog/compatibility manifest** — source and install truth, not a new skill runtime.
2. **Collision-aware sync/check script** — mechanical projection from repository to harness homes.
3. **`ReviewFindingsV1`** — the normalized artifact at the review → address seam.
4. **Measurement receipt fields** — extensions to existing receipts/artifacts where possible, not a new analytics store.

### Reused surfaces

- Ship driver state machine and `driver address` / `driver run` / `driver land`.
- Gate's verifier ladder, grants, exit codes, and artifact log.
- Workbench `contracts` and `local` packages.
- Flare's best-effort notification over authoritative producer artifacts.
- GitHub PRs, checks, branch rules, and exact-head merge protection.
- Dossier as the project/phase/task index when its connector is available.

## 4. Key decisions & trade-offs

### D1 — Repository-first skill ownership; installed homes are projections

**Choice:** `pers/cc-skills` is the canonical operator/private catalog. `pers/skills` is the reviewed public projection for entries marked public. `~/.claude/skills` and `~/.codex/skills` are installation targets, never durable authoring locations.

**Alternative:** continue editing live Claude skills, then manually copy outward. Rejected: it already produced divergent `work-driver` variants and makes directionality easy to reverse.

The public projection may carry public-only metadata or redactions; sync must render/patch it intentionally rather than wholesale-copying the private directory.

### D2 — Native implementations, compatible artifacts

**Choice:** Claude and Codex may have independent skill bodies. Portability means they produce and consume the same versioned artifacts, invoke the same engine/CLI contracts, and preserve the same failure semantics. Shared prose/references are an opportunistic deduplication only where the lifecycle primitive is genuinely identical; they are not a design target. Claude-only operations (`Workflow`, chip/session conventions) and Codex-only operations (task/thread tools, plugins) stay native.

**Alternative A:** one shared skill body with target prefix/suffix adapters. Rejected as the default: the adapter can become the entire implementation and shared prose can degrade into harness conditionals.

**Alternative B:** force textual identity across harnesses. Rejected: identical prose can be semantically wrong when the products expose different lifecycle primitives.

### D3 — No silent collision winner

**Choice:** sync refuses a same-name, non-identical target unless the manifest declares `portable-copy`, `target-adapted`, or an explicit replacement. The initial 2026-07-10 local import copied 23 missing skills and preserved all 13 collisions; that is the migration baseline, not the final sync mechanism.

### D4 — Version the review → address artifact

**Choice:** coordinators emit `ReviewFindingsV1` JSON. `driver address --findings <path>` continues treating the file as prompt input, so Ship need not import coordinator logic. The JSON is both human-readable and machine-checkable.

**Alternative:** keep free-form markdown per harness. Rejected: it makes the most frequent loop seam untestable and encourages another set of convention keys.

### D5 — Graduate Gate into Workbench; do not split `contracts` yet

**Choice:** after the Phase 1 closure gate, migrate Gate into the Workbench Go module as another independent `cmd/gate` tenant. Its reducer consumes `contracts.Verdict`; the schema lives once beside that type. Gate keeps its private decision packages and artifact-mediated integration.

**Fallback trigger:** at Phase 3 start, use a temporary standalone `contracts` module for that cycle only when Gate has an in-flight feature PR whose rebase/review cost across the repo move is estimated larger than the migration itself. Otherwise migrate Gate directly. After that feature lands, re-evaluate and remove the temporary module by graduating Gate; the fallback is not a permanent third-home decision.

**Alternative A:** leave Gate separate with mirrored types. Rejected: two sources stay green independently while drifting.

**Alternative B:** publish `contracts` as a standalone module now. Rejected while Gate is the only outside Go producer and is already on the Workbench migration queue; a third module is distribution overhead without an external consumer that cannot migrate.

### D6 — Generalize subject identity before broad schema adoption

**Choice:** the next major verdict schema uses a discriminated subject (`pr`, `run`, `artifact`) instead of reusing PR-shaped `{repo, number, head_sha}` for CI/run verdicts. Existing v0.x readers remain tolerant; this is a versioned v1 boundary, not an in-place semantic mutation.

### D7 — Capability enforcement lives at GitHub's boundary

**Choice:** grants remain useful scope/TTL/tier records, but a Gate-driven live merge is called enforcing only when a required GitHub check and credential custody prevent governed agents from bypassing it. Local HMACs are not the primary security claim.

### D8 — PRs become machine-verifiable change packets

**Choice:** retain PRs for exact-head identity, CI/status checks, review artifacts, branch rules, audit, and rollback. Remove mandatory human diff-reading as the default completeness mechanism; do not remove the transaction boundary.

### D9 — Review posture scales by risk and failure class

**Choice:** deterministic checks always; one general reviewer by default; targeted adversarial invariant review for gates/auth/migrations/concurrency/destructive changes; extra providers on disagreement or high tier; a judge only when findings need reconciliation.

**Alternative:** four vendors on every cycle. Rejected as the permanent policy: observed silent no-shows and duplicated findings make reviewer count a poor proxy for assurance.

### D10 — “Verifiable” means semantic evidence, not valid shape

**Choice:** enum/schema membership validates transport only. Gate-relevant local output must quote source evidence that a deterministic reader confirms, recompute an invariant, or be wrong only in a way that adds work/parks safely. Confidence remains provenance/diagnostic data and does not independently authorize action.

### D11 — Observability may keep derived operational state

**Choice:** Flare and similar sinks may keep cursors, dedupe sets, delivery journals, and caches. They may not own authoritative workflow decisions or write back into producer state. This replaces the redesign's overly literal “storeless” wording with the boundary its implementation actually needs.

### D12 — Join identities before considering store unification

**Choice:** stable provenance refs connect Dossier tasks, Ship runs/streams, review artifacts, Gate runs, PRs, commits, and notifications. Physical store convergence remains parked until duplicated facts cause measured correctness or operator cost.

## 5. Data model

### 5.1 Skill catalog entry

The exact serialization may be YAML or JSON; these fields are required:

```yaml
version: 1
name: work-driver
source: pers/cc-skills/skills/work-driver
visibility: private        # private | public
targets:
  claude:
    status: adapted        # portable-copy | adapted | unsupported
    install: ~/.claude/skills/work-driver
  codex:
    status: adapted
    install: ~/.codex/skills/work-driver
public_projection: pers/skills/skills/work-driver
collision_policy: manual   # refuse | replace | manual
```

The sync report adds observed hashes and one of: `missing`, `identical`, `source-newer`, `target-newer`, `diverged`, `unsupported`.

### 5.2 `ReviewFindingsV1`

```json
{
  "schema": "review-findings/v1",
  "id": "rvf_<stable-content-id>",
  "subject": {
    "kind": "pr",
    "repo": "itsHabib/ship",
    "number": 184,
    "revision": "<reviewed-head-sha>"
  },
  "producer": {
    "class": "review-coordinator",
    "harness": "claude|codex|other",
    "impl": "<skill-or-model>"
  },
  "decision": "address",
  "findings": [
    {
      "id": "<stable-within-artifact>",
      "title": "Failure path leaves run active",
      "severity": "critical|advisory",
      "locus": { "path": "packages/driver/src/engine.ts", "line": 123 },
      "evidence": "<verbatim reviewer evidence>",
      "sources": [{ "reviewer": "codex", "url": "https://..." }]
    }
  ],
  "generated_at": "2026-07-10T00:00:00Z"
}
```

Required invariants:

- `revision` equals the PR head that reviewers inspected; a mismatch refuses or parks before dispatch.
- `id` is stable for identical normalized content; Ship records the consumed id/digest so a repeated address call cannot dispatch twice.
- Every finding has source evidence or is explicitly marked `evidence_unavailable` and cannot independently produce a critical gate.
- The artifact does not declare Ship's review cycle. Ship owns `reviewCycles`, checks remaining capacity at consumption, and records the cycle it assigns to the address attempt. Coordinators never read driver state merely to author valid findings.
- Unknown schema major refuses. Unknown optional fields are ignored by tolerant readers.

### 5.3 Verdict subject v1

```go
type Subject struct {
    Kind     string `json:"kind"`      // pr | run | artifact
    Repo     string `json:"repo,omitempty"`
    Ref      string `json:"ref"`       // PR number string, run id, artifact id
    Revision string `json:"revision,omitempty"`
}
```

This change is versioned separately from `ReviewFindingsV1`. Gate migration must first prove the existing v0 contract is consumed from one package; the subject migration follows as its own reviewed PR.

### 5.4 Closure receipt

Extend existing receipts/artifacts where their owner permits; do not create a new store solely for this shape:

```json
{
  "workflow_ref": "drv_.../ds_...",
  "task_ref": "tsk_...",
  "pr_ref": "owner/repo#184@sha",
  "gate_run_ref": "run_...",
  "seat": "codex",
  "model": "gpt-5.6-sol",
  "effort": "high",
  "provider": "openai",
  "review_cycles": 2,
  "human_interventions": 1,
  "recoveries": ["auth-refresh"],
  "outcome": "merged"
}
```

## 6. API and command contracts

### 6.1 Skill catalog check/sync

No MCP is added. A repository script exposes two modes:

```text
sync-skills --target claude|codex|all --check [--json]
sync-skills --target claude|codex|all --apply
```

- `--check` is read-only. Exit 0 = catalog and installed projections match policy; 1 = drift/collision; 2 = invalid catalog/source.
- `--apply` copies missing/declared portable skills and renders declared adapters. It never deletes an untracked target and never resolves `manual` automatically.
- Every apply writes a machine-readable report outside the installed skill dirs or prints it to stdout; the report never includes secret file contents.
- A post-copy check verifies required `SKILL.md`, referenced relative files, frontmatter name/description, and source/target hashes.

### 6.2 Review/address

```text
review-coordinator <pr> --format review-findings-v1 --out <path>
ship driver address <driver> --stream <stream> --findings <path>
ship driver run <driver>
```

The coordinator name is illustrative; any producer may emit the schema. Phase 1 adds the minimum checks at Ship's command boundary before the portability gate can pass: supported schema, exact reviewed head, remaining engine-owned cycle capacity, and unused artifact id/digest. An external validator may be used during development, but cannot satisfy Gate B or substitute for the engine boundary in the live dogfood.

**Claude non-regression sequencing:** the existing Claude coordinator output and operator path remain unchanged while it emits `ReviewFindingsV1` as a parallel/shadow artifact. Ship exercises the new artifact path on the Phase 1 dogfood, but the old output is not removed or made dependent on the new schema until both harness closures pass Gate B. Codex chooses its smallest honest native producer in a Phase 0 spike (adapted skill versus direct GitHub-plugin ingestion); shared prose is not required.

Structured refusals retain C10's engine codes and add only contract-shaped errors when earned: `findings-schema-unsupported`, `findings-head-mismatch`, `findings-duplicate`. Cycle exhaustion keeps C10's existing `cycle-exhausted` code.

### 6.3 Liveness policy

```ts
interface RunLivenessPolicy {
  readonly startupTimeoutMs: number;
  readonly inactivityTimeoutMs: number;
  readonly expectedRunWindowMs: number;
  readonly absoluteBackstopMs: number;
}
```

`expectedRunWindowMs` informs defaults/telemetry; it is not itself a kill timer. Only provider/remote progress updates `lastMeaningfulProgressAt`. A locally generated persistence/event-pump heartbeat cannot update it.

### 6.4 Gate contract ownership

After migration:

```go
func Reduce(subject contracts.Subject, verdicts []contracts.Verdict) (contracts.Verdict, error)
```

`contracts` owns only type/schema/constants/tolerant decoding. Gate owns reducer behavior and tests. No other tool imports Gate's decision package; it reads verdict artifacts or calls the binary.

## 7. Key flows

### 7.1 Collision-aware skill import

1. Read catalog and validate every canonical source.
2. Inspect target without modifying it.
3. Missing + supported → stage copy/render in a temp directory.
4. Same-name identical → no-op.
5. Same-name divergent + `manual`/undeclared → emit collision, make no change.
6. Same-name divergent + explicit `replace`/declared adapter → stage the replacement and bind the apply to the target hash observed during check; if the target changes before rename, refuse and re-check.
7. Validate staged skill and all relative references.
8. Rename staged directory into place atomically for missing/replacement-authorized targets; retain enough report metadata to identify the replaced hash.
9. Emit report; a fresh harness session is the activation boundary.

The 2026-07-10 raw import performed steps 2–5 manually: 23 missing copies installed, 13 collisions preserved. Phase 0 turns that one-off into an auditable mechanism and classifies semantic compatibility.

### 7.2 Cross-harness C10 review-fix closure

1. Ship cloud stream succeeds and opens/updates a PR.
2. Reviewers inspect exact `head_sha`.
3. Claude or Codex coordinator emits `ReviewFindingsV1` with verbatim sources and no driver-owned cycle field.
4. Ship validates schema, head, unused artifact id/digest, and remaining cycle capacity; refusal parks with a coded reason.
5. `driver address` re-dispatches onto the existing PR branch.
6. `driver run` harvests terminal state; fresh head is pushed by the agent run.
7. Re-request risk-appropriate review on the new head.
8. Gate evaluates exact-head evidence and grant ceiling.
9. `driver land` merges, records merge facts, and finalizes the run.
10. Dossier/receipt refs and Flare notifications close or surface remaining work.

No step checks out or manually pushes the cloud PR branch in the operator seat.

### 7.3 Long local run

1. Runner resolves current credentials immediately before the provider query boundary.
2. Startup timer begins and ends on the first meaningful provider event.
3. Each meaningful remote/provider event persists `lastMeaningfulProgressAt`.
4. Local storage heartbeats may prove the owning process is alive but never prove the remote agent is progressing.
5. Inactivity expiry re-reads persisted/remote liveness before cancellation.
6. Absolute backstop parks/cancels even an endlessly noisy run.
7. Result-vs-cancel races resolve terminal result first when it was observed before the cancellation decision; exactly one terminal receipt wins.
8. Restart/attach resumes from persisted progress and remaining absolute budget.

### 7.4 Gate enforcement

1. Gate evaluates the exact PR head and publishes a GitHub check/status tied to that SHA.
2. Branch rules require that status plus existing CI.
3. Governed agent credentials cannot admin-merge or bypass the rule.
4. A separate controlled identity performs the final merge only after Gate pass and grant check.
5. Emergency/operator bypass remains possible but produces an explicit GitHub audit event and is never described as autonomous Gate success.

### 7.5 Risk-scaled review

1. Deterministic tests/static checks always run.
2. Classify risk from code-grounded evidence; an unknown classification escalates.
3. Low-risk: one general independent reviewer.
4. Gate/auth/migration/concurrency/destructive: add a targeted adversarial reviewer instructed to construct a proceeding-without-proof scenario.
5. Disagreement/high tier: add provider diversity and, if needed, premium judgment.
6. Merge decision uses evidence and invariants, not reviewer-vote count.

## 8. Concurrency, consistency, and failure model

### Skill sync

- One target skill directory is the atomic unit; an apply never partially overlays files into a live target.
- Concurrent sync attempts take a per-target lock or fail with `sync-busy`; last-writer-wins is not acceptable for skill instructions.
- Installed target edits are drift, not merge input. The report names them; the operator promotes intentional changes back to the canonical repo explicitly.
- Unsupported target entries remain installed only if already present; sync neither deletes nor advertises them as compatible.

### Review artifacts

- Findings are immutable for one `(artifact id, repo, pr, revision)` tuple.
- A new head always requires a new artifact/review cycle. Reusing findings across revisions refuses.
- Duplicate delivery is safe: stable artifact id/digest plus the engine's consumed-artifact record prevents double-address from incrementing or dispatching silently.
- Reviewer absence is recorded as absence/pending. It never becomes clean evidence.

### Provider liveness and auth

- Credential material is read at the latest provider boundary and never enters logs, fixtures, artifacts, or error strings.
- Malformed/missing refresh state returns a typed auth failure and parks/retries according to existing driver policy; it does not downgrade to an anonymous or stale request.
- Time decisions use injected monotonic time where local duration matters and persisted remote/provider timestamps where host suspension could invalidate local observation.
- Cancellation is best-effort at the provider but terminal state is truthful: “cancel requested” is not “remote stopped” until confirmed or explicitly marked uncertain.

### Contract evolution

- Schema majors are explicit. Unknown major refuses; unknown optional fields within a supported major are tolerated.
- Conformance tests live with the type/schema and are exercised by every in-module consumer.
- Migration of Gate is a code move/import change, not a behavioral rewrite. Reducer tests must be identical before and after.

## 9. Rollout / implementation plan

The first validation gate sits after Phase 1. Phases 0–1 test the central portability/closure claim. Phase 2 is the already-earned reliability work. Phases 3+ remain planned but uncommitted until the loop proves itself.

| Phase | Goal | High-level tasks | Depends on | Gate | Scope / recommended model |
|---|---|---|---|---|---|
| **0 — Skill catalog baseline** | Make imports explicit and safe | Inventory all personal skills; choose canonical/private/public/install ownership; catalog 36 Claude + Codex-native entries; classify 13 collisions; validate the 23 copied baselines; implement `sync-skills --check`; spike the smallest honest Codex review producer; port `/tdd` references away from Claude-only assumptions where needed | — | pre-gate, no-regret | 2–4 PRs, each <500 wLOC; Terra/medium for inventory/script, Sol/high for compatibility review |
| **1 — Cross-harness C10 closure** | Prove Claude and Codex share one real loop | Define `ReviewFindingsV1`; add Ship boundary validation + consumed-artifact dedupe; update canonical `work-driver` and coordinator producers; install/render both harness targets; dogfood one Codex-produced and one Claude-produced cloud PR through address → re-review → Gate → land; record interventions | 0, Ship C10 | **VALIDATION GATE** | 3–5 PRs, each <700; Sol/high implementation + Sol/max adversarial verification |
| **2 — Long-run reliability** | Stop killing productive or renewable runs | Merge duplicate liveness tasks; reviewed phase doc; refresh-aware Claude auth; four-budget liveness; meaningful-progress persistence; fault matrix; `driver_address` MCP parity as a separate small PR | 1 | post-gate, already earned by live P1s | 3–5 PRs; Sol/high for state/races, Terra/medium for MCP registration |
| **3 — Contract convergence** | One verdict source | Graduate Gate into Workbench without behavior change; make reducer consume `contracts.Verdict`; add cross-consumer conformance; separately design/migrate subject v1 | 1 | post-gate | 2–4 PRs; Sol/high |
| **4 — Enforcing Gate** | Make grants/checks more than advisory discipline | Publish exact-head GitHub check; require it in branch rules; separate merge-capable identity; prove bypass denied; document emergency operator path | 3 | security gate | 2–4 PRs; Sol/max + targeted adversarial review |
| **5 — Risk-scaled verification** | Spend review effort where it adds assurance | Instrument reviewer yield; implement tier/invariant-based panel policy; semantic evidence rule for local outputs; keep confidence diagnostic; compare against current panel | 1, enough receipts | experiment gate | 2–3 PRs + corpus; Sol/high judgment, Terra/medium mechanics |
| **6 — Unattended closure trial** | Test the north star | Run a small varied corpus across Claude and Codex/off-box providers; inject red CI, missing reviewer, auth failure, restart, high-risk grant exceed; measure §11 | 2, 4, 5 | go/no-go for further autonomy | test program, not one PR; Sol/ultra for adversarial orchestration only where justified |
| **7 — Identity joins, not store unification** | Improve explainability without a platform | Stable refs across Dossier/Ship/review/Gate/GitHub/Flare; extend existing receipts/views; revisit physical convergence only on measured double-book cost | 6 | demand-gated | unestimated; breadth only |

### Validation-gate decision after Phase 1

Proceed to the program only if a fresh Codex seat and a fresh Claude seat each complete one real actionable-review loop without manual branch checkout/push, both through the same Ship artifact validator and address boundary, and every catalog collision is visible rather than overwritten. The two seats produce separate artifacts for separate exact PR heads; schema identity alone is not the proof. If the catalog/sync layer starts encoding workflow behavior or the findings artifact needs coordinator access to deep Ship engine state, stop at independent harness-native skills over the shared artifact/CLI contract instead of building a compatibility framework.

## 10. Open questions

1. **Canonical catalog repo:** this TDD chooses `cc-skills` as private source and `skills` as public projection. Confirm before Phase 0 mutates either history; their current extra metadata may argue for a neutral source directory shared by both.
2. **Codex coordinator implementation:** use a native Codex skill or direct GitHub-plugin ingestion? Phase 0 spikes the smallest honest implementation. D2 is already settled: artifact/CLI compatibility, not textual porting, is the portable boundary.
3. **Skill activation boundary:** does Codex reliably reload copied personal skills on the next task, or require app restart for some entries? Pin the real behavior in Phase 0 documentation.
4. **Dossier project:** this TDD assumes the existing `workbench` project. The current Codex session has no dossier connector, so phases/tasks/doc artifact are not seeded yet. The first Dossier-capable seat should reconcile rather than create a duplicate project.
5. **Gate migration timing:** if active Gate feature work makes a repo move disruptive after Phase 1, should it first import a short-lived standalone `contracts` module? Default remains “migrate Gate,” but operational sequencing may override.
6. **GitHub enforcement:** required check from a GitHub Action, GitHub App, or a controlled local identity? Choose the smallest option that genuinely removes governed-agent bypass.
7. **Review experiment baseline:** the existing four-reviewer panel is flaky. Define denominators from requested, actually-started, and completed reviewers separately so silent no-shows do not make the alternative look artificially cheap or weak.
8. **Provider-neutral liveness:** which event projection is semantically meaningful across Cursor, Claude, and Codex? Each provider may need an adapter; the policy must not collapse to the weakest/noisiest shared field.

## 11. Validation plan

### Gate A — skill catalog integrity (Phase 0)

Binary criteria:

- all personal skills appear exactly once in the catalog;
- every installed Claude/Codex directory is owned, external/system, or explicitly unmanaged;
- `sync-skills --check --json` reports zero unknown collisions;
- missing relative references and invalid frontmatter fail the check;
- applying to a temp Codex home never overwrites a divergent same-name skill;
- after one documented `sync-skills --apply` to a temp Claude home, a fresh Claude session discovers and invokes `/tdd`, `/review-coordinator`, and the adapted `/work-driver` with the same operator-facing commands and no new per-invocation ceremony; the existing live Claude home is not replaced before this passes;
- a fresh Codex task discovers `/tdd`, the chosen native review producer, `/wip`, `/shipped`, and the target-adapted `/work-driver` as intended.

### Gate B — cross-harness loop closure (Phase 1, program gate)

On two real Ship cloud PRs with at least one actionable finding each:

1. a fresh Codex seat produces valid `ReviewFindingsV1` for PR A's exact reviewed head and drives it through the shared Ship boundary;
2. a fresh Claude seat independently produces valid `ReviewFindingsV1` for PR B's exact reviewed head and drives it through the same boundary;
3. each `driver address` continues its existing PR branch;
4. each `driver run` reaches terminal success and changes that PR's head;
5. a fresh review runs on each new head;
6. Gate and `driver land` complete both PRs without manual branch checkout/push;
7. each closure receipt contains linked run/PR/Gate refs plus seat/model/effort/review cycles;
8. Ship itself rejects stale-head, cycle-exhausted, missing-reviewer, and duplicate-address probes before dispatch; an external pre-validator is not counted.

**Intervention taxonomy:** `genuine-judgment` is only an action requested because the system explicitly recognized ambiguity (unknown risk class, reviewer disagreement, grant ceiling exceeded, or another recorded escalation question). `mechanism-repair` is any operator action needed because a required automated behavior was absent or wrong, including manual head/cycle correction, branch checkout/push, credential refresh, collision resolution during apply, or recovery from an untruthful state. If an action cannot be classified unambiguously from the receipt, count it as mechanism repair.

**GO:** all eight hold for both harnesses, with at most one `genuine-judgment` intervention per PR and zero `mechanism-repair` interventions.

**NO-GO / reshape:** if target-specific skill prose dominates the core, keep independent adapters and standardize only the artifact/CLI; if Ship needs coordinator internals, keep validation outside the engine until another producer proves the need.

### Gate C — long-run reliability (Phase 2)

Scripted fake-clock/provider tests plus one live long run prove:

- a 45–60m event-producing high-reasoning run is not killed by the former 30m wall;
- local event-pump noise cannot keep a dead remote alive;
- sleep/resume and process restart preserve correct remaining budgets;
- expired credentials refresh without leaking token material;
- result-vs-cancel produces one truthful terminal receipt;
- absolute backstop still stops an endlessly noisy runaway.

### Gate D — measured autonomy corpus (Phases 5–6)

Across at least ten varied PRs, record:

- human interventions per merged PR;
- time parked before notification;
- requested vs started vs completed reviewers;
- unique actionable findings per reviewer call;
- false passes and unnecessary parks;
- recovery success after interruption;
- elapsed time and provider/model usage;
- duplicated facts requiring manual reconciliation.

No fixed score is declared before the baseline exists. The decision is comparative: adopt the new review/closure policy only if it reduces interventions or latency without increasing false passes, and every high-risk injected failure still parks or blocks.

## 12. Dossier seeding handoff

The `/tdd` workflow normally mirrors §9 into Dossier and links this doc/PR. That connector is unavailable in the current Codex session, so no corpus write is attempted. A Dossier-capable follow-up should:

1. resolve the existing `workbench` project rather than create one blindly;
2. add all seven phases as short summary + pointer stubs;
3. materialize tasks only for Phase 0 and Phase 1;
4. tag Phase 0 mechanical tasks `GPT-5.6 Terra / medium`, compatibility decisions `GPT-5.6 Sol / high`, and the Phase 1 adversarial gate `GPT-5.6 Sol / max`;
5. link this file as `kind: doc` and its design PR as `kind: pr`;
6. leave Phases 2–7 task-less until Gate B passes, except already-existing Ship P1 tasks, which should be linked/reconciled rather than duplicated.
