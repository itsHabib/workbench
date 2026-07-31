# review — design

`review` owns verification policy: exact-head tier routing, deterministic
continuation, targeted rereview, and recorded deferment. It does not execute
accepted changes and does not authorize merges.

The boundary is artifact-first:

```text
triage-floor JSON -> ReviewPlanV1 -> ReviewRequestV1
                  -> ReviewPanelV1 + ReviewFindingsV1
                  -> ReviewCycleInputV1 -> ReviewDecisionV1
```

`contracts/reviewroute` carries only vocabulary and validity laws. Decision
logic stays under `cmd/review/internal/policy`. Review calls triage and GitHub
through process/API seams, preserving the rule that Workbench tools share
contracts rather than decision call stacks.

Both Ship and session-ledger execution consume the same `ReviewDecisionV1`.
Their only difference is the post-decision adapter used to apply accepted
findings.

The first checked-in policy is an explicit personal-repository canary. Plans
record its validated content digest automatically; callers do not manage a
separate policy revision. Continuation weights are shadow telemetry, while
exact-head joins, hard caps, and deterministic stop conditions are enforced.
