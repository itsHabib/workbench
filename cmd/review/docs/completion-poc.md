# Review completion POC

At Workbench `10b066cac0bb959ca3dfa7dc2d77886eed277e78`, Gate recognizes
two completed reviews on PR #334 that `review observe` reports as missing:

- [Claude workflow attestation](https://github.com/itsHabib/workbench/pull/334#issuecomment-5653962971),
  posted by `github-actions[bot]`, naming full head
  `d9ca988176bd47beb6afc33a70b31092b4302e0e`.
- [Codex connector comment](https://github.com/itsHabib/workbench/pull/334#issuecomment-5653964389),
  carrying the connector's ten-character `d9ca988176` footer.

The fixture in `contracts/reviewpanel/testdata/workbench-334-comments.json`
retains the original bodies, IDs, author metadata, URLs, and timestamps. The
source comments were read back from GitHub on 2026-09-13.

From the repository root, with Go installed and the baseline Git object present:

```sh
bash cmd/review/testdata/compare-completion.sh
go test ./contracts/reviewpanel ./cmd/review/... ./cmd/gate/internal/evidence
```

The replay archives the baseline into a temporary directory and copies the same
regression tests and fixture into it. Before: review reports both reviewers
missing while Gate completes both. After: both consumers complete Claude as
`COMMENTED` and Codex as `CLEAN`, retaining the full head and source comment IDs.
The expected baseline failure is checked explicitly; unrelated test/build
failures do not count as reproducing the mismatch.

The shared contract decoders parse body formats into untrusted protocol fields.
Each collector authenticates the issuer and source metadata, binds the parsed
commit to its subject, and selects the completion state. Codex accepts its
ten-character footer and the full-SHA form already supported by review, bound
to a known full PR head. Other abbreviations, conflicting footers, missing bot
metadata, and inline comments are rejected. Workflow attestations must occupy
the entire body and name a full SHA; the consumers match the decoded reviewer
and head. Gate's diff-equivalence candidate reader reuses Gate's authenticated
attestation boundary.

Formal reviews still take precedence. Findings remain separate from completion,
and required panels, continuation policy, equivalence decisions, and merge
authorization remain with their existing owners. This POC uses recorded GitHub
evidence and local execution; it does not establish that every provider/workflow
emits an attestation on every request. In particular, the separate Claude
workflow restriction to a bare review trigger remains outside this change.
