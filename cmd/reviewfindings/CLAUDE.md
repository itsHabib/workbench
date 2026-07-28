# reviewfindings

`reviewfindings github` is a Codex-native producer for Ship's
`ReviewFindingsV1` address artifact. It reads exact-head inline review comments
through `gh`, emits the shared contract, and never dispatches or merges.
Native producers pass `-catalog-revision` with the full canonical catalog
commit SHA or `sha256:<digest>`. Omitting it remains valid for legacy callers,
but the resulting artifact cannot complete a closure receipt.

The shared vocabulary and validation law live in
`contracts/reviewfindings`. Ship owns consumption, durable duplicate protection,
cycle capacity, and address dispatch. Do not copy those decisions here.

## Develop

```
go build ./cmd/reviewfindings
go vet ./cmd/reviewfindings/... ./contracts/reviewfindings/...
go test ./cmd/reviewfindings/... ./contracts/reviewfindings/...
```

Exit codes are 0 success, 3 refused input/state, and 4 operational error.
Stale requested heads, closed PRs, and empty exact-head findings refuse before
the output file is replaced.
