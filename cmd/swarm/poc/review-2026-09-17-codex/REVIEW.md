# Swarm: adversarial experiment review

**Verdict: the headline claim is unsupported.** The evidence supports a narrower proposition: on a pre-decomposed local workload, a derived board, scoped rulings and wakes can handle some coordination without a dedicated lead. It does not show that management sessions are unnecessary, that Swarm beats the successful prompts-and-loop hierarchy, or that its code earns its maintenance cost. Both experimental arms receive Swarm; the comparator strips management of most of its useful work; the generator supplies decomposition and handoffs; and one scorer condition cannot fail. The current implementation also fails independently reproduced fencing, authority, recovery and ownership cases. It is not ready for unattended Rooms.

Reviewed Workbench **`cd3295b83b982741cd5af6b7cea81eb463964466`**, the merged #360 head, plus #359/#360 original histories. This is an experiment review, not approval. No implementation files changed. Local evidence and probes sit beside this document. No cloud resources or model sessions were launched for the review.

## First: what the real hierarchy accomplished

The operator reports a productive day with 57 sessions, 16–20 concurrent, and 46 feature branches folded into eight themes. The [Relay brief](https://github.com/itsHabib/relay/blob/c37658cf8e10c19742d25dfc8b62882f85537f0e/briefs/2026-09-16-fleet-primitives/README.md) documents those counts and the failures. This is meaningful operational evidence, but not a controlled causal attribution to each role. I read the brief and runbook, not all 57 session transcripts; I cannot honestly assign individual decisions to a particular director or sub-lead.

The runbook nevertheless establishes that the hierarchy was doing substantially more than forwarding status:

| Contribution | Mind or loop? | What Swarm actually supplies |
|---|---|---|
| Turn an idea into a bounded task: goal, context, relevant files, branch, acceptance conditions | **Mind.** Requires understanding the repository and intended outcome | No authoring mechanism. `SWARM.md` assigns this to the owner; the experiment generates it for free |
| Divide responsibility, decide which tasks belong together, delegate and keep context | **Mind**, with mechanical session creation | `split` records supplied children; it does not discover the decomposition. The runner creates sessions |
| Answer blocked builders, distinguish product ambiguity from engineering uncertainty | **Mind**, with routing and persistence | `ask/claim/rule/escalate` route and record an answer. Peers or the operator still produce it |
| Reconcile competing priorities, abandon an unpromising branch, absorb changing operator intent | **Mind** | No demonstrated substitute. Owner rewrites the brief; simulated operator is a scope-to-answer lookup table |
| Notice missing work or an unreported branch | Both | Board/watch can notice known mechanical conditions and fetched branches; they cannot infer an omitted requirement from a product goal |
| Group finished work into independently useful, demoable themes | **Mind** | `order` produces a dependency order. The runner supplies one theme containing everything. Ordering is not grouping |
| Author a consolidator handoff: pinned members, conflicts, flags to leave off, demo story | **Mind**, followed by mechanical merging/testing | A fixed consolidator prompt, `consolidate`, and one consolidator model session; no theme-design experiment |
| Preserve decisions, collect branch/pin status, remind quiet builders | Mostly loop | Useful coverage through ledger, board, watcher and wakes |
| Protect a live resource; diagnose unsafe copies, install failures or broken dependencies | Judgment plus enforceable mechanics | Advisory leases and disk checks; neither protects arbitrary effects |
| Maintain operator attention: concise rollups and appropriate escalation | Both | Digest renders records. The experiment does not measure operator time, useful interruption filtering or missed strategic issues |

Evidence: [desktop runbook:20–36](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/fleet/docs/desktop-fleet.md#L20-L36), [42–67](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/fleet/docs/desktop-fleet.md#L42-L67), [93–106](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/fleet/docs/desktop-fleet.md#L93-L106), [123–159](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/fleet/docs/desktop-fleet.md#L123-L159); [Swarm owner responsibilities](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/docs/SWARM.md#L7-L18), [66–69](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/docs/SWARM.md#L66-L69).

The plausible reason the baseline worked is **delegated contextual judgment, persistent ownership, inspectable branches and a simple shared completion rule**, with loops keeping those minds supplied with evidence. That is an interpretation supported by the workflow, not proof that this exact number of management levels is optimal. It directly contradicts assuming, before testing, that the levels were merely residue of missing infrastructure.

## Findings, ranked by damage to the claim

### 1. The experiment removes the jobs whose replacement it claims to demonstrate

The treatment is “Swarm plus peers” versus “Swarm plus a constrained lead.” There is **no prompts/native messaging/loop arm, no traversal-plus-decisions-file arm, and no existing-Fleet arm**. It cannot estimate Swarm's incremental value over the actual baseline at any code size.

The lead is a fresh `claude -p` invocation with a 25-turn allowance. It reads the board, handles requests, nudges and rewrites `BOARD.md`, then exits. The next invocation starts **90 seconds after the previous invocation finishes**, not every 90 seconds. There is no persistent director, no sub-leads, no ongoing operator conversation, no responsibility for authoring tasks or themes. Flat builders are explicitly encouraged to rule; tree builders are told to defer. This tests adding a polling intermediary to an already managed workload, not replacing the successful hierarchy. [run.go:338–369](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/poc/run.go#L338-L369), [451–468](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/poc/run.go#L451-L468), [503–515](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/poc/run.go#L503-L515).

The scale workload consists of map-key additions across six packages. It hands each agent the answer, paths and a matching test. Its “discovered” split is literally specified in the card, including child names and values. Every fifth task gets a fixture lease; task 30 consequently hits that switch case before the product-ambiguity case. No product question is planted at scale. [sandbox.go:24–83](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/poc/sandbox.go#L24-L83).

The six-task workload is somewhat less trivial—configuration, reporting and a schema rename—but remains a tiny synthetic app. Neither workload exercises multi-minute installs, substantial architectural disagreement, hidden requirements, thematic selection or eleven consolidators. The runner supplies task creation, scheduling, retry and completion supervision, including an in-memory started set and concurrency counter. Removing lead sessions did not remove management functions; several moved into the harness and others into builders. [run.go:202–239](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/poc/run.go#L202-L239), [410–448](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/poc/run.go#L410-L448), [473–499](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/poc/run.go#L473-L499).

**What survives:** local feasibility of branch observation, scoped questions, peer answers, reminders and straightforward integration. **What does not:** a verdict on the real hierarchy, overall software quality, operator burden, real-day throughput or unattended reliability.

### 2. “Passed every kill condition” is not a valid safety result

**Kill 4 cannot fail.** It initializes `Passed: true` and never sets it false. Missing task, working, silent, blocked, invalid pin, landed and even an invented state all pass. A landed branch with a version bump is described as “flagged, then re-pinned” without inspecting any flag event. I exercised these cases through a Go test overlay; all passed. [score.go:224–243](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/poc/score.go#L224-L243), [scorer-probes.txt](scorer-probes.txt).

Other scoring defects matter too:

- **Kill 2 is retrospective and weaker than its specification.** It examines final branch/file ownership and only the second final tip for each contended file. Any historical covering ruling before that tip counts; it need not have been effective at the edit, complied with, or even remain effective. Later re-pinning can move the comparison time. It also treats blocked and invalid-pin rows as “done.” [score.go:165–198](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/poc/score.go#L165-L198).
- **Kill 3 accepts missing exposure and does not check chronology or implementation.** An operator ruling on `cmd/app` or export anywhere in the ledger suffices. Both scale runs omit the product-choice fault. [score.go:201–221](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/poc/score.go#L201-L221).
- **Kill 1 has little opportunity to bite.** Its threshold is 20 minutes; the reported scale workloads finish in under 17 minutes and the small workloads in minutes. Outstanding unclaimed requests at termination have not been observed long enough to establish the threshold guarantee. [KILL.md](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/poc/KILL.md#L35-L58).
- **Kill 5 is always true in individual scores**, although `Compare` does perform the cross-mode check. Its tree token check drops the preregistered “with no metric better” qualifier. That omission would unfairly fail a sufficiently expensive but otherwise better tree; it does not change these particular runs because neither reaches 1.5×. [score.go:98–99](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/poc/score.go#L98-L99), [375–390](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/poc/score.go#L375-L390).
- Scoring calls `WatchOnce`, which can verify, write state and send nudges. Observation is not frozen before measurement. Several input-read failures are ignored. [score.go:55–81](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/poc/score.go#L55-L81).

Credit where earned: raw events **do** show actual pin alerts in all four runs—[flat:57](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/poc/runs/flat/state/events.jsonl#L57), [tree:18](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/poc/runs/tree/state/events.jsonl#L18), [scale-flat:25](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/poc/runs/scale-flat/state/events.jsonl#L25), [scale-tree:22](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/poc/runs/scale-tree/state/events.jsonl#L22). The scale alerts concern `INTENT.json`, rather than the version file. Those are observations of particular detections, not validation of a scorer that cannot reject an undetected fault.

### 3. The safety substrate already fails deterministic local counterexamples

The normal `go test -count=1 ./cmd/swarm/...` passes. Independent overlays, outside the checkout, reproduced the following. “PASS” in [probes.txt](probes.txt) means the **bad behavior was reproduced**, not that the safety property holds.

| Failure reproduced | Consequence | Implementation evidence |
|---|---|---|
| Same-holder expired nonzero claim epoch can rule; an older holder's stale epoch can implicitly acquire after another holder expires | Fencing does not enforce the documented live-claim contract | [decide.go:398–412](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/decide.go#L398-L412) |
| Peer explicitly names an operator decision in `supersedes` and overrides it | Even the local tier policy can be bypassed without pretending to be the operator | [decide.go:418–435](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/decide.go#L418-L435) |
| A live lock holder pauses past one minute; B takes over; A releases and deletes B's lock; C enters | Multiple concurrent mutators; age is not proof of death | [state.go:87–112](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/state.go#L87-L112) |
| Restore the request's pre-save state after its decision append; retry with explicit supersession | Two ledger rulings for one request. Crash recovery is not an idempotent completion | [decide.go:384–391](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/decide.go#L384-L391) |
| Two admissions against an empty one-seat system both succeed before either starts | Admission reserves nothing; separate launchers can over-admit | [admit.go:32–75](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/admit.go#L32-L75) |
| Take, drop, take reuses epoch 1; delayed drop from the same seat deletes the replacement lease | Tokens do not remain monotonic; clones sharing a seat can release one another's resources | [resource.go:43–85](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/resource.go#L43-L85) |
| Two identically named children in one split are accepted; a subsequent conflicting split returns failure after queuing another child | Duplicate/partially committed task creation | [split.go:60–88](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/split.go#L60-L88) |
| Merge exists but verification has not run; retry consolidation with `false` as verifier | Branch is skipped as already merged; verification is never attempted | [consolidate.go:42–54](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/consolidate.go#L42-L54) |
| Provider unavailable during wake | Note is moved to delivered, session never starts, and wake exit is recorded as zero alongside an error string | [wake.go:168–217](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/wake.go#L168-L217) |
| Old session's delayed Stop arrives after replacement SessionStart | Old session becomes the resumable session while the replacement is live | [wake.go:42–73](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/wake.go#L42-L73) |

The rule and consolidation crash probes reconstruct the exact durable state at a crash boundary; they are **not claims of live Firecracker crash testing**. The age-lock probe uses the existing injectable clock. No live user work was touched.

### 4. Derived ownership is not trustworthy enough to replace inspection

I tested rebases, merges and moving peer tips against `ownFiles`:

- **Positive control:** a simple rebase onto a peer followed by that peer advancing correctly excludes the inherited file, thanks to the start marker.
- **Counterexample:** A merges B; B later advances. With A unchanged, B's old file becomes attributed to A. The algorithm subtracts current peer tips that are ancestors, not immutable provenance of what was inherited.
- **Counterexample:** a new file introduced only in a merge commit disappears from `ownFiles`, because the default `git log --name-only` invocation omits that merge's changes.

This corrupts contention detection and affinity routing, precisely where consolidation differs from independent map edits. [board.go:227–263](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/board.go#L227-L263), [320–328](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/board.go#L320-L328), [probes.txt](probes.txt).

The hash chain is also a narrower guarantee than “immutable ledger”: ordinary alteration without rehashing is detected (positive control), but removing the last valid decision is accepted. A torn JSON append makes the entire ledger unreadable. There is no external committed tail, transactional request/ledger update, or `fsync` in the append/rename helpers. A hash chain detects some corruption; it does not establish durable, exactly-once decisions. [state.go:75–84](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/state.go#L75-L84), [186–197](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/state.go#L186-L197), [decide.go:493–530](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/decide.go#L493-L530).

### 5. Independent counts partly match the tables, but the experiment does not close its own accounting

I recomputed these from `sessions.jsonl`, `state/wakes.jsonl` and `state/events.jsonl`, without using `score.json` as input. Script: [recompute.py](recompute.py); results: [recomputed.json](recomputed.json). Latencies below are from each ask to its first recorded claim or ruling, with unresolved requests explicitly retained.

| Recomputed metric | Six flat | Six tree | Scale flat | Scale tree |
|---|---:|---:|---:|---:|
| Session rows, excluding wakes | 7 | 9 | 34 | 42 |
| Completed wake rows / wake-start events | 4 / 4 | **5 / 7** | 4 / 4 | 13 / 13 |
| Ask events | 6 | **9** | 7 | 13 |
| Ruling events | 6 | 6 | 7 | 13 |
| Ask → first claim median / max, seconds | 7.51 / 23.03 | 13.16 / 135.07 | 7.54 / 85.53 | 33.20 / 54.72 |
| Ask → ruling median / max, seconds, resolved only | 7.51 / 23.03 | 12.35 / 50.11 | 7.54 / 85.53 | 33.20 / 54.72 |
| Requests without a recorded ruling | 0 | **3** | 0 | 0 |
| Recorded output tokens, including completed wakes | 30,334 | 36,287 | 131,796 | 156,122 |
| Recorded cost | $3.0655 | $4.3650 | $17.8917 | $19.3880 |
| Session wall, excluding wakes, seconds | 209.15 | 241.93 | 940.05 | 1,006.61 |
| Span including completed wakes, seconds | 209.15 | **365.78** | 940.05 | 1,006.61 |

At scale, tree has **18.46% more recorded output tokens, 8.36% more recorded cost and 7.08% longer session wall** than flat. Equivalently, flat has 15.58% fewer tokens than tree. Tree's worst resolved request wait is **36.0% shorter**, contradicting “no metric is better” in the scale verdict. The same document acknowledges that improvement later. [RESULTS-SCALE.md:14–19](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/poc/RESULTS-SCALE.md#L14-L19), [65–68](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/poc/RESULTS-SCALE.md#L65-L68).

The small tree's archived events contain a ninth request and two wake starts without matching completed wake records. Its final event occurs after the last completed wake. The score's eight requests and four-minute wall are not the full archived activity. The runner waits for wakes, **then calls a final wake-enabled watch**, then collects; that sequence permits new work after the wait. Scoring separately snapshots statistics before a state-mutating watch. These are credible accounting mechanisms, not evidence that I know the fate of the missing wakes. [run.go:174–183](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/poc/run.go#L174-L183); [tree events:98–110](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/poc/runs/tree/state/events.jsonl#L98-L110).

All four killed builder attempts record zero tokens and zero cost; scale-tree also has a canceled lead with zero accounting. Zero recorded is not proof of zero provider consumption. Wake input/cache tokens are not counted. The advertised costs are incomplete recorded totals, not reconciled bills. [score.go:103–149](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/poc/score.go#L103-L149).

**Statistics verdict:** no between-run variance estimate, no randomization, no repeat pairs, no reliable confidence interval. Tasks in a run share the scheduler, store and rulings; they are not independent experimental replicates. The 18.46% token difference is descriptive. A large share is mechanically the extra lead's output, not proof of equivalent management outcomes. None of the mode differences can be declared distinguishable from run-to-run noise from these data. I also did not independently certify 33 accepted feature outcomes from a final board or model's result text: the archived scale evidence lacks the full sandbox Git object history needed to reconstruct every landing and integration result.

### 6. Iteration repaired real defects, but also tailored the challenge—and the measured version is not today's version

The relevant history is inspectable in [history.txt](history.txt), the original PR heads and saved diffs:

| Commit / change | Effect on interpretation |
|---|---|
| `469ecbd`: initial substrate, harness and KILL | Establishes initial stated conditions, not an independent scorer |
| `ad78712`: ownership repair, routing includes landed seats; fault timing changed in the experiment | Repairs real defects. Routing to landed peers especially enables flat's peer expertise after builders finish; tree also gets it |
| `6e49557`: results and KILL amendment explaining 120s → 25s kill | Sensible repair of a fault that never fired. The amendment is committed at 03:19 UTC, after scored flat starts 03:08 and tree 03:11; history alone does not establish its claimed before-run-2 fixation |
| `8eacf20`: review fixes after small runs | Atomic hook settings, lock timestamp handling, bounded wake wait and overlap display changes; small-run evidence does not validate these later changes |
| `904d2e1`: package checks, order, receipts, load, generated scale workload and consolidation | Broadens ruling coverage and turns integration into a harness-defined outcome. A different workload/protocol from the small run |
| `740cb02`: split plus runner admission of children | “Discovery” remains scripted; runtime queues supplied work |
| `9e206ed`: intent declarations, start-marker ownership, mechanical consolidation, failed-start retries | Fixes selected after a preliminary flat failure. Gives complete path knowledge and cheap clean merges to both arms, removing potential management work from both |
| `04e1f7f`: **`check` starts reading intents**, plus order-cycle refusal | **After the reported scale runs.** At their stated `9e206ed`, the board reads intent but CLI `check` scans only `row.Files`. The scale runs cannot validate intend-then-check safety |
| `e0a2c00`: load accounting/docs | Further post-run changes, not a rerun |

The last point is especially important: [the actual post-run diff](https://github.com/itsHabib/workbench/commit/04e1f7f601cf923ab97dbec56675b0a0da574bd8) replaces the old `touching` helper with intent-aware `Board.Touching`. The current fix may be sensible; the published trial did not run it.

There is no evidence here that only flat got a different binary in the final pair; the report names the same commit and both options files describe the same scale settings. There is also no recorded executable hash binding those runs to a build. Shared fixes do not remove workload bias: the harness supplies exactly the planning and integration help whose absence could make a lead useful. Treat these as development runs. Freeze the next version, evaluator and workload before a fresh comparison.

### 7. Swarm reduces some pains; it does not justify its whole surface

| Real-day pain | Swarm's likely effect | Cheapest credible alternative |
|---|---|---|
| 90-minute rollup | Derived branch/pin board can reduce it substantially | The operator already wrote the traversal script. Add a compact digest and decisions file |
| Messages queued behind a 45-minute turn | Post-tool hooks can deliver sooner; stopped-session wakes can help. A long tool call still delays delivery, and wake reliability is currently broken | Native messaging plus instruction to inspect between tools; event-trigger existing Fleet mail/watcher when headless |
| Rename loses title-addressed mail | Branch address avoids title dependence locally | Store immutable native session ID / stable role mailbox. Fleet already has mail identity |
| Director level added manually | Avoids explicitly creating that role only by leaving its judgment elsewhere | Change a role prompt/add a session. No experimental evidence that absorbing operator intent disappeared |
| Disk-full from seven installs | Rejects starts once disk is already below a floor; **does not reserve space or prevent seven concurrent checks passing** | One install owner/queue, disk preflight with known install allowance; headless runtime needs an actual capacity reservation |

The disk fault is an environment variable reporting one byte free, not seven real installs consuming storage. The runner's own concurrency counter makes its admission look stronger than the CLI's. A short scripted admission refusal proves neither prevention of the reported outage nor safe scheduling across launchers. [run.go:372–405](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/poc/run.go#L372-L405), [admit.go:32–75](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/admit.go#L32-L75).

### 8. Much of this is a second Fleet, with fewer recovery protections

Current production Go counts, including the POC harness but excluding tests: **Swarm 5,709 lines; Fleet 16,470**. Size alone is not a verdict. Duplication without a measured benefit is.

| Swarm surface | Existing Fleet overlap | Increment worth evaluating |
|---|---|---|
| `board`, `digest`, `load`, `stats`, `who` | [BoardRows](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/fleet/internal/verbs/views.go#L102), slots and occupancy | Git-derived RESULT/pin and changed-path observations |
| `watch`, `nudge`, `inbox`, `hook`, wakes | Fleet watch/mail/provider delivery | Affinity selection and new alert types, not another process supervisor |
| `take`, `drop`, parts of `admit` | Fleet leases, slots, dispatch and assignment checks | Real resource/capacity admission if the runtime enforces it |
| `verify`, receipts | [Fleet exact-subject receipts](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/fleet/internal/verbs/receipts.go#L572-L616) | RESULT observation as another evidence producer |
| `ask`, `claim`, `rule`, `decide`, `escalate`, `wait`, scoped lookup | Mail/work/checkpoints cover parts | A structured scoped decision record and query are the clearest distinct idea; judgment remains in agents |
| `intend`, `order`, `split`, `consolidate` | Task briefs, dispatch and integration workflows | Intent observations and deterministic ordering may help; these trials do not establish a separate runtime |

Fleet already has [stable mailbox identities](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/fleet/internal/fleet/mail_identity.go#L21-L80). Its delivery path takes locks, checks launch/session presence, reserves messages and releases reservations when launch fails—specific behaviors Swarm independently reimplements more weakly. [Fleet deliver.go:185–256](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/fleet/internal/watch/deliver.go#L185-L256), [305–365](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/fleet/internal/watch/deliver.go#L305-L365). This is not a blanket certification of Fleet for distributed operation. It is evidence that a second watcher/lease/mail stack needs an incremental-value argument that the experiment never makes.

## Headless Rooms: what breaks and the minimum contract

**Separate clone filesystems break the present design immediately.** Each clone has its own requests, decisions, inbox, resource leases and session records under its Git common directory. Git pushing source does not replicate this state, and `split` writes a tasks file in a designated shared checkout. “One watcher per machine” gives separate authorities with divergent views. Merely swapping file calls for a remote key/value client does not repair multi-object transactions.

Rooms' work is relevant but narrower than a full fleet proof. [PR128 results](https://github.com/itsHabib/rooms/blob/f79269edae802ab8574ad979fce6bb6a7464a389/docs/experiments/lease-plane-results/README.md) report the virtiofs and Redis throughput contrast; 146ms and 38.3ms are **acquire p95**, while claim-scan p95 at 64 peers is 344ms and 61.8ms. Those are container measurements. [PR130](https://github.com/itsHabib/rooms/blob/b653eafcfe526f1accd6ed8d75bee49b2c256482/docs/experiments/lease-plane-in-rooms-results/README.md) actually runs two/four/six Rooms clones, 240 simulated tasks, kill plus Redis restart, fresh post-restore UUIDs and AOF with `appendfsync always`. It reports one accepted completion per task. It does not prove exactly-once arbitrary Git writes, installs, model calls or deployments, and it explicitly excludes cross-host partitions and several other faults.

### Read-then-act verdicts

| Sequence | Actual guarantee |
|---|---|
| `check` → edit | Observation only. No lease/version attached to the write, no enforcement that an ordering ruling was followed |
| `intend` → `check` | Narrowly sound against **both-clear** with fresh shared reads, persistent declarations and compliant exact-path use. Not an exclusive write grant |
| claim → rule | Serialized locally in the ordinary case, but explicit epoch/expiry checks are defective; decision append and request completion are separate |
| take → write | No fencing at the resource. An expired/paused worker can still write after reassignment |
| admit → start | No reservation or idempotency key. Two launchers can both pass and start |

My [small exhaustive model](model.py) enumerates all six interleavings of two intent/check pairs: zero both-clear executions with a shared fresh view, six with independent unreplicated views. It deliberately does not model Git, ruling changes, undeclared edits or fencing. Thus I **accept the narrow shared-view reasoning and reject the unqualified safety claim**. Even within one repository, `pickTips` can choose an older remote tip after local rebase divergence, so freshness needs an explicit argument. [board.go:179–196](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/board.go#L179-L196).

I also reran the [Rooms two-peer lease model](rooms-model.py): fencing with a separately read pending flag has a double-acceptance trace. An atomic completion done-check repairs that model; atomic pending-check-and-grant also avoids granting completed work. The lesson is to specify where acceptance happens, not merely attach a counter. [rooms-model-results.txt](rooms-model-results.txt).

### Crash, identity and wake requirements

- **Mid-rule:** decision may exist with request still open; request may be complete without its notification. Use one transaction for completion and an outbox event, then idempotent delivery.
- **Mid-split:** cards, task rows and ruling are separate writes; some errors are ignored. Child IDs and the parent transition must commit together, with retry returning the original result.
- **Mid-consolidation:** durable merge is not durable verified acceptance. Persist the exact merged SHA and pending validation; retry validates it. Merge pinned SHAs, hold exclusive integration ownership, and never infer success merely from ancestry.
- **Mid-wake / dead watcher:** consumed notes can have no recipient. A restarted watcher forgets its in-memory active map; old provider children may survive, or messages may remain stranded. Two watchers have independent maps and race on consuming the same files. Need durable delivery reservation, incarnation-bound execution ownership, acknowledgement, and restart reconciliation.
- **Stale-lock recovery:** never infer owner death solely from elapsed time. A fencing epoch is useful only if the mutation/acceptance path rejects the old owner. The reproduced old-release-deletes-new-lock case is unsafe even locally.
- **Identity:** branch is a useful logical work address, not a runtime identity. Snapshot clones can share branch and frozen session records. Mint a post-restore worker incarnation; bind `(task, attempt, incarnation, epoch)` to claims and accepted outputs. Scope logical identity by repository/tenant. Also replace `/`→`_` filename encoding: `a/b` and `a_b` collide in inbox, session and resource paths.
- **Unattended wakes:** current limits are two concurrent wakes, six minutes and 30 turns **per wake**; there is no cumulative budget, retry ceiling or failure backoff. Resuming a live/stale session, missing provider/authentication, invalid local cwd, provider outages and endless standing alerts need explicit terminal states. Cost accounting must survive killed calls. [wake.go](https://github.com/itsHabib/workbench/blob/cd3295b83b982741cd5af6b7cea81eb463964466/cmd/swarm/internal/swarm/wake.go#L76-L217).

**Smallest store contract:** observations plus conditional claims and commits—not one remote RPC per existing file operation.

1. `read/snapshot(cursor)`: versioned task, decision, worker and capacity observations. Cached views are advisory only.
2. `claim(kind, id, incarnation, ttl, idempotencyKey)`: atomically check pending/eligibility, ownership and applicable capacity; allocate a monotonically increasing epoch and reserve the resource. Use store time.
3. `commit(kind, id, epoch, expectedVersion, payload, idempotencyKey)`: atomically validate current ownership/authority and not-already-complete, commit the decision/result/children, and append an outbox event. Return the same receipt on retry.
4. Conditional renew/release and delivery acknowledgement, bound to epoch and incarnation; retain token history after release.

The actual effects must also be protected: publish an immutable result and conditionally accept it, or fence writes at the resource service. A state-store transaction cannot make an external shell command exactly once. Use at-least-once attempts with deduplicated, fenced acceptance; name any non-idempotent external effects separately.

## The smallest version worth keeping

**Keep the traversal, a decisions record/query, and alerts delivered through the existing wake mechanism. Keep the minds until evidence says their jobs are unnecessary.**

Concretely:

- Keep a read-only Git/RESULT observer, with the ownership limitations fixed or explicitly excluded. Emit observations usable by the existing Fleet board and receipts.
- Keep a small append-only decision schema: scope, subject revision, author, reason, supersession and optional order. A simple decisions file is adequate for the current cooperative desktop baseline; use a transactional store only for the headless acceptance contract.
- Reuse Fleet/native mail, sessions and watcher. Affinity routing is an optional selector over those observations, not a new runtime.
- Preserve the POC, raw failed runs and independent probes as experiments. Relabel the current results as feasibility/development evidence.

**Delete from the proposed production surface:** the separate Swarm watcher/wake/session registry, duplicate inbox/hook transport, duplicate resource leases and advisory admission masquerading as reservation. Defer `split`, generic `consolidate`, tier machinery and task-launch lifecycle as a new platform until a real workload demonstrates a missing Fleet capability. Do not delete historical evidence. The useful experiment should become a small Fleet change plus a Rooms store-contract experiment, not a competing 22-verb coordination product.

## Three cheapest experiments that could change this verdict

These are proposals, not experiments run during this review. Freeze workload, commits, evaluator, thresholds and fault schedule before any scored run. Have someone other than the author own the acceptance checklist. Development attempts stay visible but are not scored trials.

| Experiment | Minimum credible setup | Kill condition fixed in advance |
|---|---|---|
| **1. Give management real work; compare three arms** | Use one real repo backlog with 12–18 heterogeneous tasks, long builds/installs, conflicting API changes, one genuinely ambiguous requirement and several possible demo themes. All peers run headless in Rooms; no shared FS. Compare persistent prompt/loop hierarchy with native messaging + traversal; same hierarchy plus decisions/alerts; flat using the same substrate. Each gets the same goal, not prewritten task cards. Three paired batches, rotate order, same model/concurrency/spend allowance. Leads own decomposition and integration as on the real day | Kill “flat replaces management” if it misses any frozen required behavior/theme, needs more operator interventions, or fails to reduce either operator minutes or total model cost by **at least 20% in two of three pairs**, while keeping accepted output equal and completion time within 10%. Treat this as a screening threshold, not statistical proof |
| **2. Real integration, interrupted at the unsafe boundaries** | Eight Rooms peers plus two independent watchers integrate six real branches with one merge conflict, a failing integration test and a task split. Kill/restore workers immediately after claim, decision append, task creation, merge and wake reservation; restart the store; pause one holder past TTL; disconnect one clone. Repeat the fixed schedule ten times. Independent checker reads store history and pinned Git outputs | Kill unattended readiness on **one** duplicate accepted task/ruling/child, stale accepted write, lost required message, accepted unverified merge, or task not resumed within two lease periods plus two poll periods after recovery. A failed injection is “not tested,” never pass |
| **3. Reproduce the actual queue/install pain cheaply** | On one bounded local Rooms host, run eight heterogeneous workers including seven real dependency installs and long tool calls. Compare prompt install-owner + traversal against the proposed admission/wake additions, using the same disk quota and workload. Rename logical display titles; restore a clone; make the provider unavailable once. Measure disk reserve, useful work, ask-to-action latency and provider starts | Kill the additions' incremental-value claim if they breach the disk reserve, lose a message, duplicate an attempt, exceed the fixed per-task wake/spend allowance, or fail to cut p95 ask-to-action time by **30%** without reducing accepted throughput versus the cheap baseline. Any missing accounting makes the result inconclusive |

These thresholds are review proposals chosen now for a future frozen test. They are not post-hoc tests of the existing numbers. Start with experiment 2's deterministic failure cases: it is cheaper than paying models to rediscover broken recovery.

## What must be true before unattended Rooms

An isolated Rooms run must demonstrate a single authoritative, durable acceptance store; post-restore identities; conditional pending/claim/complete transactions; resource-side fencing or immutable-output acceptance; retry-safe split/integration/message delivery; watcher takeover without duplicate live writers; and bounded, reconciled provider spend. The fault harness must catch intentionally broken variants, report missing fault exposure as missing, and validate actual outputs against an independent contract. After those hold, repeat a real-workload comparison with the useful hierarchy intact.

None of this requires an elaborate agent bureaucracy. It requires making the few claimed runtime guarantees true. **Swarm currently automates some of the easy work, assumes away much of the hard work, and overstates what its experiment establishes.**

## Reproduction packet

Everything linked here is committed in this directory. Run the commands from the
repository root.

- [Independent reproduction output](probes.txt) and [Go overlay probes](review_probe_test.go.txt)
- [Scorer probe](scorer_probe_test.go.txt), [output](scorer-probes.txt)
- [Raw-log recount](recompute.py), [results](recomputed.json)
- [Intent/check model](model.py), [results](model-results.json)
- [Rooms lease model](rooms-model.py), [results](rooms-model-results.txt)
- [Review history](history.txt)

The two Go probes carry a `.txt` suffix so they do not join the build; they are
injected through the overlay, and the worktree is unchanged:

```
go test -overlay=cmd/swarm/poc/review-2026-09-17-codex/overlay.json ./cmd/swarm/internal/swarm/... ./cmd/swarm/internal/poc/...
```

`recompute.py` reads `cmd/swarm/poc/runs` relative to itself; pass another
archive as its first argument.

Not preserved in this packet: the normal `go test` output, the original and
amended kill conditions as separate files, and the post-run intent-fix diff. The
kill conditions before and after the run are in [`poc/KILL.md`](../KILL.md) and
its git history; the intent fix is in the history of `cmd/swarm/internal/poc`.
Review checkpoint and all source links are pinned to the reviewed revision. No
claim here relies on an unrecorded successful cloud run.
