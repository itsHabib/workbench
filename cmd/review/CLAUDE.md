# review

`review` is Workbench's engine-neutral review-policy tenant. It decides which
reviewers an exact PR head needs and whether another cycle is warranted. Ship
and session engines consume its artifacts; they do not reinterpret its policy.

## Commands

```text
review plan    -repo owner/repo -pr N -head SHA -policy policy.json -out plan.json
review request -plan plan.json [-reviewers codex,cursor] -out request.json
review observe -plan plan.json -out panel.json
review decide  -plan plan.json -input cycle-input.json -out decision.json
review advise  -input finding-evidence.json -out advisory.json
```

`plan` shells `gh` and `triage-floor`; it never imports triage decision code.
`request` is the sole GitHub reviewer-triggering write boundary. `observe`
emits `ReviewPanelV1`; use the existing `reviewfindings` command for sourced
nonempty `ReviewFindingsV1` evidence. `decide` is deterministic. `advise` is an
optional local-model opinion and has no independent suppression authority.

Exit codes are 0 success, 3 refused stale/invalid state, and 4 operational
error. A classifier or policy failure produces a successful exact-head
full-panel fallback plan when the live PR head can still be proven.

## Invariants

- Every artifact joins on repository, PR, exact head, and content-derived plan
  ID.
- A head change invalidates planning, requests, observation, and decisions.
- Missing/invalid policy or classification selects the complete safe panel.
- Reaching a cap never turns incomplete evidence into success.
- Critical findings cannot be deferred or proof-substituted.
- Later cycles target only finding authors still in play.
- T0 uncertainty escalates; cycles 4–8 require T3 and a rationale.
- Local advice is advisory and escalates on low confidence/verifier failure.

## Develop

```text
go build ./cmd/review
go vet ./cmd/review/... ./contracts/reviewroute/...
go test ./cmd/review/... ./contracts/reviewroute/...
```
