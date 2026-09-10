# Review disposition for the one-Fleet rewrite

This inventory reconciles the discussion through `18c48fc` and the subsequent
operator-approved one-Fleet direction. It covers 11 issue comments, 11 inline
comments and three review summaries retrieved before this rewrite. Related notes
are grouped; “addressed” describes this design, not an implemented runtime claim.
Fresh review of the finished rewrite was explicitly authorized on 2026-09-10.

## Prior implementation and design findings

| Finding | Disposition in the rewrite | Source |
|---|---|---|
| Contact restrictions block same-tenant communication; missing lead-to-seat edge, malformed tree or missing dispatch blocks a send | Removed. Mail is standalone, topology optional, no universal one-hop etiquette | [Codex](https://github.com/itsHabib/workbench/pull/297#discussion_r3975774625), [Astra 1](https://github.com/itsHabib/workbench/pull/297#issuecomment-5613656842) |
| Intent/resolution around every send; undefined sender chain for seats; recipient identity disappears with erased intent body | Removed. Retained mail is the send record; no mandatory duplicate escalation or intent | [Astra 2](https://github.com/itsHabib/workbench/pull/297#issuecomment-5613656842), [Claude U2](https://github.com/itsHabib/workbench/pull/297#issuecomment-5613493480) |
| Two seats attach to the same exclusive Org role | Removed. No new Org attach lifecycle; individual seat addresses are explicit next work | [Codex](https://github.com/itsHabib/workbench/pull/297#discussion_r3975774630) |
| Release from Active fails; missing SessionEnd strands holder; conditional yield and incarnation/session join absent | Removed from new workflow, not patched in legacy Org. Existing held-work callers must migrate with their obligations intact | [Active](https://github.com/itsHabib/workbench/pull/297#discussion_r3975774634), [missed release](https://github.com/itsHabib/workbench/pull/297#discussion_r3975774636), [join](https://github.com/itsHabib/workbench/pull/297#discussion_r3975813102), [Claude C1](https://github.com/itsHabib/workbench/pull/297#issuecomment-5613564553) |
| Directory admission asserted; watcher reservation handoff and dead directory lease recovery undefined | Deferred. No automatic launcher or directory reservation is adopted; future launcher requires independent proof | [admission](https://github.com/itsHabib/workbench/pull/297#discussion_r3975774640), [handoff](https://github.com/itsHabib/workbench/pull/297#discussion_r3975813108), [recovery](https://github.com/itsHabib/workbench/pull/297#discussion_r3975813113) |
| Launcher and seat occupancy behavior attributed to README without supporting implementation | Removed. Observed occupancy is not exclusive admission; no launcher claimed | [Claude U1/U3](https://github.com/itsHabib/workbench/pull/297#issuecomment-5613564553) |
| Dependency graph makes launch independent of mail | Removed. Mail lands independently; launcher is deferred without a mandatory migration step | [Claude G1](https://github.com/itsHabib/workbench/pull/297#issuecomment-5613493480) |
| Seat-address hook injection has no implementation step | Addressed in seat-address slice and sandbox two-seat/replacement tests | [Claude G2](https://github.com/itsHabib/workbench/pull/297#issuecomment-5613493480) |
| Retiring Org message procedure omits guide updates | Addressed by caller inventory/cutover including instructions, hooks and MCP; command retirement waits for active consumers | [Claude G3](https://github.com/itsHabib/workbench/pull/297#issuecomment-5613493480) |
| Root binding leaks into nested worktrees; cleanup ordering unsafe | Explicit configuration slice, preserving user settings; no automatic attach rollout or silent live-root mutation | [Codex](https://github.com/itsHabib/workbench/pull/297#discussion_r3975774646) |
| Cross-charter checks attributed to kernel | Removed. No new tree or kernel validation is a prerequisite; optional discovery must describe its own validation | [Claude U1](https://github.com/itsHabib/workbench/pull/297#issuecomment-5613493480) |
| Multiple effects contradict one-open-intent law | No new intent wrapper and no claim that an ID alone proves recovery. Existing recovery is retained until interruption/retry tests establish replacement | [Claude E1](https://github.com/itsHabib/workbench/pull/297#issuecomment-5613493480) |
| Rehearsal evidence says every tick had an intent/effect; chain-kind criterion excludes necessary hook writes | Removed. Acceptance measures actual continuity, interventions and duplicate effects rather than prescribed chain counts | [Codex](https://github.com/itsHabib/workbench/pull/297#discussion_r3975774653), [Claude G1](https://github.com/itsHabib/workbench/pull/297#issuecomment-5613564553) |
| Long-lived holder wording contradictory; older contacts fallback rationale stale | Removed. Mail queues without a session; #295's final simplified contract is the baseline | [Claude C2/E2](https://github.com/itsHabib/workbench/pull/297#issuecomment-5613493480) |
| Ambiguous cross-repository PR numbers | Explicit repository URLs retained | [Copilot](https://github.com/itsHabib/workbench/pull/297#discussion_r3975758596) |

## Final architectural decisions

| Discussion | Decision in this rewrite |
|---|---|
| [Fable: one binary/store, continuity toward Fleet](https://github.com/itsHabib/workbench/pull/297#issuecomment-5618834137) | Fleet is the destination. Existing Org internals may remain during migration; Gate stays separate. No new hash-chain or fencing claim is inferred from that destination |
| [Astra: one workflow and one authoritative assignment](https://github.com/itsHabib/workbench/pull/297#issuecomment-5618834596) | Accepted, including removal of universal per-tick checkpoint and communication ceremony. Fleet's own overlapping dispatch/placement fields are acknowledged and assigned a cutover |
| Keep useful history and actual callers | Explicit inventory, scratch migration, one writer per migrated cohort, repeatable cutover, preservation of unhandled obligations and readable old history |
| Verify replacement-session usefulness | Named worker/lead exchange, stable retry identity, individual seats, current assignment visibility, context provenance and intervention counts |

The intermediate [6708b7a response](https://github.com/itsHabib/workbench/pull/297#issuecomment-5613666041)
proposed directory leases and session-start stamping. The later
[18c48fc response](https://github.com/itsHabib/workbench/pull/297#issuecomment-5613699883)
deferred launching. Neither intermediate launch scheme is adopted here.

The earlier [advance-to-Gate opinion](https://github.com/itsHabib/workbench/pull/297#issuecomment-5618787391)
was superseded by the one-Fleet direction. Prior reviews do not approve this
rewrite. Future implementation still needs its own checks and review; history is
not marked resolved merely because a migration is planned.
