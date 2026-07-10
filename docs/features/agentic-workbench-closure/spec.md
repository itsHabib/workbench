# Agentic Workbench Closure — Technical Design Document

**Status:** draft / proposal — **NOT a build commitment.** This is the artifact we decide from.
**Owner:** @itsHabib
**Date:** 2026-07-10
**Revision:** v1 — adds the cross-cutting property/model/fuzz testing strategy and names the authorization invariants each contract must preserve.
**Related:** [`docs/DESIGN.md`](../../DESIGN.md), [`FOLLOWUPS.md`](../../../FOLLOWUPS.md), `pers/docs/kickoff-post-c10-gpt56-2026-07-10.md`, `pers/workbench-redesign/DESIGN.md`, `pers/workbench-redesign/VERDICT.md`, `pers/workbench-redesign/RED-TEAM.md`, `pers/writer/drafts/embrace-the-slop.md`, `pers/writer/drafts/rebuilding-the-workbench.md`, `pers/ship/docs/features/qe-sdet/phases/03-property-based-state-machine.md`, `pers/dossier/docs/features/advanced-testing/spec.md`.

> **Reviewers — amendment focus:** (1) whether D13's properties describe authorization semantics rather than accidental output formatting, (2) whether the Gate reducer's multiple-judgment ambiguity must be resolved before permutation invariance is enforced, and (3) whether the phase-local property gates in §9/§11 are strong enough without creating a testing big-bang.

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
- **FR15 — Executable contract laws:** every safety-critical deterministic contract names its invariants and carries example tests plus property/model/fuzz tests appropriate to the seam. A schema or migration cannot be called converged when its examples pass but its laws are untested.

### Non-functional requirements

| Property | Target |
|---|---|
| Safety | Unknown skill collision, verdict field/class, reviewer absence, remote liveness, or Gate state fails closed; absence never reads as green. |
| Portability | Claude and Codex can both perform the Phase 1 flow from a fresh session using documented/installable surfaces. |
| Recovery | A process restart preserves the latest meaningful remote progress and does not reset an inactivity budget or lose the PR branch. |
| Determinism | Skill sync `--check`, schema conformance, reducer laws, and fault-injection tests produce machine-readable verdicts. Property runs are reproducible from a printed seed/counterexample. |
| Test runtime | PR property suites use a bounded deterministic run count and add <2 minutes to each affected repository's CI; longer fuzzing runs on a separate scheduled/manual lane. |
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
2. **Collision-aware `skill-sync` policy** — extend the existing provider-neutral synchronizer; do not create a second sync script in Workbench.
3. **`ReviewFindingsV1`** — the normalized artifact at the review → address seam.
4. **Measurement receipt fields** — extensions to existing receipts/artifacts where possible, not a new analytics store.

### Reused surfaces

- Ship driver state machine and `driver address` / `driver run` / `driver land`.
- Gate's verifier ladder, grants, exit codes, and artifact log.
- `skill-sync`'s provider registry, directory projections, status/diff engine, and additive writes.
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

### D5 — Graduate verdict consumers into Workbench; do not split `contracts` yet

**Choice:** after the Phase 1 closure gate, graduate the remaining Go verdict consumers into the Workbench module without combining them into one application. Migrate Tracelens first as the low-risk rehearsal, then migrate Gate and Triage as one coordinated wave because Gate invokes the live `triage-floor` external binary. Gate's reducer consumes `contracts.Verdict`; Tracelens and Triage emit that type. Each keeps independent commands, private decision packages, tests, and artifact-mediated integration.

The migration order is operational, not architectural: Tracelens is already a compact deterministic artifact consumer and can prove module tooling, import mechanics, and Workbench CI for a tenant move cheaply. It cannot prove Gate reducer identity. Before the Gate/Triage wave begins, Gate's reducer suite must pass on its original repository head and the identical tests must accompany the move. Gate and Triage move together for operational safety—not because of an in-process import—so installation/path changes cannot strand the configured `triage-floor` binary between repositories. Triage's label and held-out evaluation corpus migrates with its policy code; it is part of the correctness oracle, not disposable repository history.

**Fallback trigger:** at Phase 3 start, use a temporary standalone `contracts` module for that cycle only when Gate or Triage has in-flight feature work whose rebase/review cost across the paired repo move is estimated larger than the migration itself. Otherwise migrate them directly. After that work lands, re-evaluate and remove the temporary module by completing the coordinated graduation; the fallback is not a permanent third-home decision.

**Alternative A:** leave Gate separate with mirrored types. Rejected: two sources stay green independently while drifting.

**Alternative B:** publish `contracts` as a standalone module now. Rejected while the outside Go producers are already on the Workbench migration queue; a third module is distribution overhead without an external consumer that cannot migrate.

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

### D13 — Test laws at deterministic seams; evaluate stochastic judgment with corpora

**Choice:** property testing supplements, and never replaces, named example tests. Use it where the contract makes a universal claim over a pure reducer, codec, state machine, capability, or filesystem projection. Keep hand-written examples for intent and regressions; keep live fault injection for provider/GitHub behavior; keep labeled evaluation corpora for model quality. Do not property-test prose quality or pretend randomized inputs can prove an LLM judgment correct.

Each property names four things in the test: the valid input domain, the law, the authorization projection being compared, and the reproduction mechanism. Compare decisions/tier/confidence/state—not incidental reason ordering, timestamps, generated IDs, or display text—unless those fields are themselves public contract. A failing randomized case prints a seed and minimized counterexample, then graduates into a named example regression test.

| Seam | Required laws | Technique |
|---|---|---|
| Skill catalog / `skill-sync` | check/dry-run never mutates; declared portable sync is idempotent; undeclared same-name divergence is never overwritten; resolved paths remain inside the declared source/target roots | deterministic generated directory trees plus filesystem snapshots |
| `ReviewFindingsV1` / Ship address boundary | exact-head mismatch always refuses; empty/unsourced `address` never dispatches; consuming the same artifact/digest twice dispatches at most once; unknown major refuses while unknown optional fields preserve the known projection | generated artifacts plus model-based command sequences |
| Ship workflow/liveness | every successful transition is legal; terminal state never regresses; one truthful terminal result wins cancel/result races; advancing fake time cannot restore an exhausted budget without a defined recovery event | existing `fast-check` state-machine design, fake clocks, bounded event sequences |
| Verdict codec | marshal/decode preserves known fields; unknown optional fields are tolerated; malformed/truncated input never panics or yields an authorizing verdict; unknown major/class/decision fails closed | Go fuzz targets with checked-in seed corpus plus generated valid values |
| Gate reducer authorization projection | code block remains block under any additional valid evidence; missing code floor never passes; adding a verdict cannot lower tier or increase minimum confidence; duplicate non-judgment evidence cannot improve authorization; permutation invariance applies after the multiple-judgment rule below is resolved | Go `testing/quick`/deterministic generators over valid verdict sets |
| Grants and append-only state | changing any signed field invalidates the grant; scope mismatch never validates; advancing time cannot revive expiry; widening the requested tier/cycle cannot turn refusal into authorization; mutating a recorded artifact breaks chain verification | generated grants/artifacts and bounded tamper mutations |
| Triage | final tier is never below the deterministic floor; advisory failure/uncertainty can only preserve or escalate; identical diff/config produces identical floor output | generated diff feature sets plus the held-out labeled corpus |
| Tracelens | identical trace/config produces the same diagnostic projection; malformed/partial traces never panic or manufacture a pass; adding an observed failure cannot improve the authorization projection | Go fuzzing for parsers plus generated trace/event sequences |

Use each ecosystem's native, already-earned tool: Go standard-library `testing/quick` and fuzzing in Workbench/Gate/Triage/Tracelens; Ship's existing `@fast-check/vitest` design; Dossier's existing Rust `proptest` design. Workbench does not gain a shared cross-language property framework.

**Multiple-judgment hold:** Gate's reducer says verdict order does not matter, but today more than one judgment verdict is resolved by the last slice element. Do not encode that accident as a property or hide it by generating at most one judgment. Before contract convergence, choose and test one structural rule: reject multiple judgments, or reduce them by an explicit deterministic order/join carried in the artifact. Only then does permutation invariance become a required Gate law.

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
  "panel": {
    "expected": ["codex", "claude", "cursor"],
    "started": ["codex", "cursor"],
    "completed": ["codex", "cursor"],
    "missing": ["claude"]
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
- `decision: address` requires at least one finding. An empty findings array refuses; it cannot manufacture a review-fix cycle.
- Every finding has source evidence or is explicitly marked `evidence_unavailable` and cannot independently produce a critical gate.
- Every named finding source appears in `panel.completed`. Expected-but-absent reviewers remain explicit in `panel.missing`; their absence is not clean evidence.
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
  "review_producer": "codex:github-plugin",
  "skill_catalog_revision": "<git-sha-or-manifest-hash>",
  "review_cycles": 2,
  "interventions": [
    {
      "time": "2026-07-10T00:00:00Z",
      "kind": "genuine-judgment",
      "reason_code": "reviewer-disagreement",
      "question_ref": "esc_...",
      "actor": "human:michael"
    }
  ],
  "recoveries": ["auth-refresh"],
  "outcome": "merged"
}
```

## 6. API and command contracts

### 6.1 Skill catalog check/sync

No MCP and no second synchronizer are added. Extend the existing `skill-sync`
CLI with a Codex provider plus an optional catalog policy manifest:

```text
skill-sync status --source-dir <catalog> --targets claude,codex --manifest <path> [--json]
skill-sync sync   --source-dir <catalog> --targets claude,codex --manifest <path> [--dry-run]
```

- `status` is read-only. Exit 0 = catalog and installed projections match policy; 1 = drift/collision; 2 = invalid catalog/source or policy.
- `sync` copies missing/declared portable skills and target-declared implementations. It never deletes an untracked target, overwrites an undeclared same-name difference, or resolves `manual` automatically.
- Every sync writes a machine-readable report outside the installed skill dirs or prints it to stdout; the report never includes secret file contents.
- A post-copy check verifies required `SKILL.md`, referenced relative files, frontmatter name/description, and source/target hashes.

### 6.2 Review/address

```text
review-coordinator <pr> --format review-findings-v1 --out <path>
ship driver address <driver> --stream <stream> --findings <path>
ship driver run <driver>
```

The coordinator name is illustrative; any producer may emit the schema. Phase 1 adds the minimum checks at Ship's command boundary before the portability gate can pass: supported schema, exact reviewed head, non-empty sourced findings for `decision: address`, source/panel consistency, remaining engine-owned cycle capacity, and unused artifact id/digest. An external validator may be used during development, but cannot satisfy Gate B or substitute for the engine boundary in the live dogfood.

**Claude non-regression sequencing:** the existing Claude coordinator output and operator path remain unchanged while it emits `ReviewFindingsV1` as a parallel/shadow artifact. Ship exercises the new artifact path on the Phase 1 dogfood, but the old output is not removed or made dependent on the new schema until both harness closures pass Gate B. Codex chooses its smallest honest native producer in a Phase 0 spike (adapted skill versus direct GitHub-plugin ingestion); shared prose is not required.

Structured refusals retain C10's engine codes and add only contract-shaped errors when earned: `findings-schema-unsupported`, `findings-head-mismatch`, `findings-empty`, `findings-source-incomplete`, `findings-duplicate`. Cycle exhaustion keeps C10's existing `cycle-exhausted` code. `panel.missing` does **not** prevent addressing valid findings already received; it prevents the later review/Gate path from treating the panel as complete or clean without an explicit recorded escalation/waiver.

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
3. The seat invokes its catalog-declared, installed native review producer; that producer emits `ReviewFindingsV1` with verbatim sources and no driver-owned cycle field. A hand-authored JSON file is valid for schema smoke tests but cannot satisfy Gate B.
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

- Findings and panel state are immutable for one `(artifact id, repo, pr, revision)` tuple.
- A new head always requires a new artifact/review cycle. Reusing findings across revisions refuses.
- Duplicate delivery is safe: stable artifact id/digest plus the engine's consumed-artifact record prevents double-address from incrementing or dispatching silently.
- Reviewer absence is recorded as absence/pending. It never becomes clean evidence. Addressing known findings may proceed, but a merge-ready verdict may not infer panel completeness from that address artifact.

### Provider liveness and auth

- Credential material is read at the latest provider boundary and never enters logs, fixtures, artifacts, or error strings.
- Malformed/missing refresh state returns a typed auth failure and parks/retries according to existing driver policy; it does not downgrade to an anonymous or stale request.
- Time decisions use injected monotonic time where local duration matters and persisted remote/provider timestamps where host suspension could invalidate local observation.
- Cancellation is best-effort at the provider but terminal state is truthful: “cancel requested” is not “remote stopped” until confirmed or explicitly marked uncertain.

### Contract evolution

- Schema majors are explicit. Unknown major refuses; unknown optional fields within a supported major are tolerated.
- Conformance tests live with the type/schema and are exercised by every in-module consumer.
- Migration of Gate is a code move/import change, not a behavioral rewrite. Reducer tests must be identical before and after.
- Every new property failure is reproducible from its seed/minimized counterexample and is promoted into the checked-in regression corpus before the fix merges.
- Property generators are versioned with the contract they exercise. A generator that excludes an inconvenient valid state is a test bug, not a waiver.

## 9. Rollout / implementation plan

The first validation gate sits after Phase 1. Phases 0–1 test the central portability/closure claim. Phase 2 is the already-earned reliability work. Phases 3+ remain planned but uncommitted until the loop proves itself.

| Phase | Goal | High-level tasks | Depends on | Gate | Scope / recommended model |
|---|---|---|---|---|---|
| **0 — Skill catalog baseline** | Make imports explicit and safe | Inventory all personal skills; use `cc-skills` as canonical private catalog and `skills` as reviewed public projection; add Codex + manifest policy to `skill-sync`; catalog 36 Claude + Codex-native entries; classify 13 collisions; validate the 23 copied baselines; add non-mutation/idempotence/path-confinement properties; spike the smallest honest Codex review producer; port `/tdd` references away from Claude-only assumptions where needed | — | pre-gate, no-regret | 2–4 PRs, each <500 wLOC; Terra/medium for inventory/script, Sol/high for compatibility review |
| **1 — Cross-harness C10 closure** | Prove Claude and Codex share one real loop | Define `ReviewFindingsV1`; add Ship boundary validation + consumed-artifact dedupe; property-test exact-head/refusal/dedupe laws; update canonical `work-driver` and coordinator producers; install/render both harness targets; dogfood one Codex-produced and one Claude-produced cloud PR through address → re-review → Gate → land; record interventions | 0, Ship C10 | **VALIDATION GATE** | 3–5 PRs, each <700; Sol/high implementation + Sol/max adversarial verification |
| **2 — Long-run reliability** | Stop killing productive or renewable runs | Merge duplicate liveness tasks; reviewed phase doc; refresh-aware Claude auth; four-budget liveness; meaningful-progress persistence; bounded fake-clock/event-sequence properties; fault matrix; `driver_address` MCP parity as a separate small PR | 1 | post-gate, already earned by live P1s | 3–5 PRs; Sol/high for state/races, Terra/medium for MCP registration |
| **3 — Workbench contract convergence** | One verdict source across independent tenants | Graduate Tracelens and land it green with parser/determinism properties; only then capture a green original-repo Gate reducer law suite, resolve multiple judgments, and open the coordinated Gate + Triage migration; make all consume/emit `contracts.Verdict`; preserve Gate properties and Triage's floor/advisory corpus; add cross-consumer conformance; separately design/migrate subject v1 | 1 | Gate/Triage PRs remain unopened until Tracelens is merged green and Gate's original reducer example + property suites pass | 3–5 PRs; Sol/high |
| **4 — Enforcing Gate** | Make grants/checks more than advisory discipline | Property-test signed-field tamper, scope, TTL, tier, and cycle laws; publish exact-head GitHub check; require it in branch rules; separate merge-capable identity; prove bypass denied; document emergency operator path | 3 | security gate | 2–4 PRs; Sol/max + targeted adversarial review |
| **5 — Risk-scaled verification** | Spend review effort where it adds assurance | Instrument reviewer yield; enforce the Triage final-tier ≥ deterministic-floor property; implement tier/invariant-based panel policy; semantic evidence rule for local outputs; keep confidence diagnostic; compare against current panel | 1, enough receipts | experiment gate | 2–3 PRs + corpus; Sol/high judgment, Terra/medium mechanics |
| **6 — Unattended closure trial** | Test the north star | Run a small varied corpus across Claude and Codex/off-box providers; inject red CI, missing reviewer, auth failure, restart, high-risk grant exceed; measure §11 | 2, 4, 5 | go/no-go for further autonomy | test program, not one PR; Sol/ultra for adversarial orchestration only where justified |
| **7 — Identity joins, not store unification** | Improve explainability without a platform | Stable refs across Dossier/Ship/review/Gate/GitHub/Flare; extend existing receipts/views; revisit physical convergence only on measured double-book cost | 6 | demand-gated | unestimated; breadth only |

### Validation-gate decision after Phase 1

Proceed to the program only if a fresh Codex seat and a fresh Claude seat each complete one real actionable-review loop without manual branch checkout/push, both through the same Ship artifact validator and address boundary, and every catalog collision is visible rather than overwritten. The two seats produce separate artifacts for separate exact PR heads; schema identity alone is not the proof. If the catalog/sync layer starts encoding workflow behavior or the findings artifact needs coordinator access to deep Ship engine state, stop at independent harness-native skills over the shared artifact/CLI contract instead of building a compatibility framework.

## 10. Open questions

1. **Codex coordinator implementation:** use a native Codex skill or direct GitHub-plugin ingestion? Phase 0 spikes the smallest honest implementation. D2 is already settled: artifact/CLI compatibility, not textual porting, is the portable boundary.
2. **Skill activation boundary:** does Codex reliably reload copied personal skills on the next task, or require app restart for some entries? Pin the real behavior in Phase 0 documentation.
3. **Dossier project:** this TDD assumes the existing `workbench` project. The current Codex session has no dossier connector, so phases/tasks/doc artifact are not seeded yet. The first Dossier-capable seat should reconcile rather than create a duplicate project.
4. **Gate migration timing:** if active Gate feature work makes the paired Gate/Triage move disruptive after Phase 1, should they first import a short-lived standalone `contracts` module? Default remains coordinated graduation; operational sequencing may override.
5. **GitHub enforcement:** required check from a GitHub Action, GitHub App, or a controlled local identity? Choose the smallest option that genuinely removes governed-agent bypass.
6. **Review experiment baseline:** the existing four-reviewer panel is flaky. Define denominators from requested, actually-started, and completed reviewers separately so silent no-shows do not make the alternative look artificially cheap or weak.
7. **Provider-neutral liveness:** which event projection is semantically meaningful across Cursor, Claude, and Codex? Each provider may need an adapter; the policy must not collapse to the weakest/noisiest shared field.
8. **Multiple Gate judgments:** reject more than one judgment verdict, or add explicit ordering/identity and a deterministic join? Current last-slice-wins behavior contradicts the reducer's order-independence claim and must not survive contract convergence accidentally.

## 11. Validation plan

### Cross-cutting invariant-suite acceptance

Property testing is phase-local: a contract's laws land with the PR that creates
or migrates that contract, not in a later testing sweep. Every new suite must:

1. name its valid domain, law, and compared authorization/state projection;
2. run at least 100 deterministic generated cases per property in normal PR CI, within the repository's <2-minute added-runtime budget;
3. print a reproducible seed and minimized counterexample on failure;
4. include checked-in fuzz seeds for parser boundaries; time-boxed discovery fuzzing may run scheduled/manual;
5. demonstrate once, before merge, that a deliberate opposite mutation is caught, then revert the mutation;
6. preserve named example tests and promote every real minimized failure into one.

An unseeded random loop, a property that reimplements production as its oracle,
or a generator that excludes a valid troublesome state does not satisfy this
gate.

### Gate A — skill catalog integrity (Phase 0)

Machine prerequisite: the extended `skill-sync` CLI—including `codex`, `--manifest`,
`--targets`, and `--json`—must pass its own deterministic fixture test and a
machine-run read-only check over the catalog directory. If this prerequisite
fails or is replaced by a hand-authored report, none of the criteria below are
evaluated and Gate A cannot pass.

Binary criteria:

- all personal skills appear exactly once in the catalog;
- every installed Claude/Codex directory is owned, external/system, or explicitly unmanaged;
- `skill-sync status --manifest <catalog-policy> --json` reports zero unknown collisions;
- missing relative references and invalid frontmatter fail the check;
- applying to a temp Codex home never overwrites a divergent same-name skill;
- generated catalog trees prove check/dry-run non-mutation, declared portable-sync idempotence, and path confinement;
- after one documented `skill-sync sync` to a temp Claude home, a fresh Claude session discovers and invokes `/tdd`, `/review-coordinator`, and the adapted `/work-driver` with the same operator-facing commands and no new per-invocation ceremony; the existing live Claude home is not replaced before this passes;
- a fresh Codex task discovers `/tdd`, the chosen native review producer, `/wip`, `/shipped`, and the target-adapted `/work-driver` as intended.

### Gate B — cross-harness loop closure (Phase 1, program gate)

On two real Ship cloud PRs with at least one actionable finding each:

1. a fresh Codex seat invokes the catalog-declared installed Codex review producer, which produces valid `ReviewFindingsV1` for PR A's exact reviewed head, then drives it through the shared Ship boundary; ad hoc JSON or a direct CLI-only sequence does not count;
2. a fresh Claude seat independently invokes the catalog-declared installed Claude review producer, which produces valid `ReviewFindingsV1` for PR B's exact reviewed head, then drives it through the same boundary; ad hoc JSON or a direct CLI-only sequence does not count;
3. each `driver address` continues its existing PR branch;
4. each `driver run` reaches terminal success and changes that PR's head;
5. a fresh review runs on each new head;
6. Gate and `driver land` complete both PRs without manual branch checkout/push;
7. each closure receipt contains linked run/PR/Gate refs, seat/model/effort/review cycles, the native review producer id, the skill-catalog revision/hash that installed it, and typed intervention events sufficient to compute the GO condition;
8. Ship itself rejects stale-head, cycle-exhausted, empty/unsourced address artifacts, source/panel inconsistency, and duplicate-address probes before dispatch; the later review/Gate path parks rather than treating `panel.missing` as clean. An external pre-validator is not counted.
9. the `ReviewFindingsV1`/address property suite proves those refusal and at-most-once laws over generated valid/invalid artifacts and bounded repeated-consumption sequences.

**Intervention taxonomy:** every intervention is a typed receipt event with `time`, `kind`, `reason_code`, `actor`, and (for judgment) the recorded escalation/question ref. `genuine-judgment` is only an action requested because the system explicitly recognized ambiguity (unknown risk class, reviewer disagreement, grant ceiling exceeded, or another recorded escalation question). `mechanism-repair` is any operator action needed because a required automated behavior was absent or wrong, including manual head/cycle correction, branch checkout/push, credential refresh, collision resolution during apply, or recovery from an untruthful state. If an event is missing, lacks a question ref for claimed judgment, or cannot be classified unambiguously, count it as mechanism repair.

**GO:** all nine hold for both harnesses, with at most one `genuine-judgment` intervention per PR and zero `mechanism-repair` interventions.

**NO-GO / reshape:** if target-specific skill prose dominates the core, keep independent adapters and standardize only the artifact/CLI; if Ship needs coordinator internals, keep validation outside the engine until another producer proves the need.

### Gate C — long-run reliability (Phase 2)

Scripted fake-clock/provider tests plus one live long run prove:

- a 45–60m event-producing high-reasoning run is not killed by the former 30m wall;
- local event-pump noise cannot keep a dead remote alive;
- sleep/resume and process restart preserve correct remaining budgets;
- expired credentials refresh without leaking token material;
- result-vs-cancel produces one truthful terminal receipt;
- absolute backstop still stops an endlessly noisy runaway;
- bounded generated fake-clock/event sequences preserve terminal-state and budget monotonicity laws.

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
