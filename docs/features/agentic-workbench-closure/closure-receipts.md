# Closure receipt contract

Workbench reconstructs one closure receipt per driver-state stream. It does not
add a receipt database or a terminal receipt event. Existing lifecycle events
remain authoritative for the run, PR, review-cycle count, merge commit, and
outcome. Two additive stream-scoped events supply only facts that those events
cannot express:

- `closure_facts` may be appended more than once while the stream is `pr_open`.
  Each event is a partial set of typed facts. Repeated values must agree.
- `intervention` records `time`, `kind`, `reason_code`, `actor`, and the
  question reference required by `genuine-judgment`.

`driverstate state --json` exposes the reduced receipt under
`streams.<stream>.closure`, and `driverstate render` shows complete, missing,
and contradictory closure state. Legacy ledgers with neither new event retain
their prior reduced shape.

## Completion law

A receipt is complete only when its existing lifecycle reaches exactly one
merge and the join contains:

- task, Ship run, PR/head, and Gate run references;
- seat, harness, model, provider, and effort;
- native review producer and catalog revision;
- review artifact id, canonical 64-hex digest, and exact reviewed head;
- at least one recorded review cycle and the merge commit.

The reviewed head must equal the head recorded by `stream_pr_opened`, and the
PR number on `stream_merged` must name that same opened PR. Conflicting repeated
facts, a head or PR mismatch, or duplicate terminal merge facts remain visibly
contradictory and cannot complete. An absent catalog revision is the only
legacy-compatible provenance case: `ReviewFindingsV1` still validates, but its
closure receipt remains incomplete. A present malformed revision refuses
contract validation.

Catalog revisions are either a full lowercase source commit SHA (40 or 64
hexadecimal characters) or `sha256:` followed by 64 lowercase hexadecimal
characters. `reviewfindings github` accepts the value through
`-catalog-revision`; native producers should always supply it.

## Intervention law

The only intervention kinds are `genuine-judgment` and `mechanism-repair`.
Genuine judgment must name the recorded escalation/question that requested it.
Unknown kinds, malformed reason codes, invalid timestamps, and a judgment
without its question reference refuse before append. A complete receipt may
still contain mechanism repair: completeness means reconstructable, while the
program-level closure gate separately requires zero mechanism repairs.
