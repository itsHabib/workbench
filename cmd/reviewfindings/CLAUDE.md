# reviewfindings

`reviewfindings github` is a Codex-native producer for Ship's
`ReviewFindingsV1` address artifact. It reads exact-head inline review comments
through `gh`, emits the shared contract, and never dispatches or merges.
Native producers pass `-catalog-revision` with the full canonical catalog
commit SHA or `sha256:<digest>`. Omitting it remains valid for legacy callers,
but the resulting artifact cannot complete a closure receipt.

The shared vocabulary and validation law live in
`contracts/reviewfindings`. Ship owns consumption for Ship-driven runs. For a
session-engine run, `reviewfindings address` consumes the same artifact on the
original implementation child ledger without invoking Ship:

```
reviewfindings address accept    -run <child-run> -stream <stream> -artifact findings.json -decision decision.json -max-cycles 3
reviewfindings address claim     -run <child-run> -stream <stream> -work <raw_id>
reviewfindings address started   -run <child-run> -stream <stream> -work <raw_id> -task <codex-task-id>
reviewfindings address completed -run <child-run> -stream <stream> -work <raw_id> -head <new-live-head>
reviewfindings address resume    -run <child-run> -stream <stream> -work <raw_id>
```

`accept` requires an exact-head `ReviewDecisionV1` action of `address`. Its
accepted finding IDs and cycle must match the findings artifact and
authoritative ledger. `accept` and `completed` read the live PR head through
`gh`. The boundary persists `AddressWorkV1` before consuming it in driver-state
v0.2. A duplicate accept refuses with the existing work ref. A claimed item
without a recorded task id parks on resume because a crash may have happened
after task creation. The CLI creates no task and invokes no model/provider API.

`ReviewDecisionV1` is a closed authorization schema. Adding any field requires
a schema-version bump; older consumers must fail loud instead of ignoring
authority-bearing data they do not understand.

## Develop

```
go build ./cmd/reviewfindings
go vet ./cmd/reviewfindings/... ./contracts/reviewfindings/...
go test ./cmd/reviewfindings/... ./contracts/reviewfindings/...
```

Exit codes are 0 success, 2 parked ambiguity, 3 refused input/state, and 4
operational error.
Stale requested heads, closed PRs, and empty exact-head findings refuse before
the output file is replaced.
