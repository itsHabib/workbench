# swarm

A fleet substrate with no management sessions: a board derived from git, a hash-chained
decision ledger, claims with fencing epochs, resource leases, admission, and a watcher that
renders, nudges and wakes. One binary, state in files under `<git common dir>/swarm`, shared by
every worktree of the repository. `README.md` is the operator's guide; `docs/DESIGN.md` is the
contract; `poc/KILL.md` and `poc/RESULTS.md` record the adversarial run; an independent review
found it does not justify the tool (`poc/review-2026-09-17-codex/REVIEW.md`,
`KNOWN-DEFECTS.md`).

## Layout

- `main.go` — verbs and exit codes. Positional arguments may precede flags.
- `internal/swarm` — the substrate. `board.go` derives rows, pin checks and contention from
  git; `decide.go` is requests, claims, rulings, tiers, routing and the ledger; `resource.go`
  leases; `admit.go` admission (`disk_unix.go`, `disk_windows.go`); `watch.go` alerts,
  digest and renders; `inbox.go` notes and the hook; `wake.go` session records and wakes;
  `receipt.go` verification receipts at landed heads; `order.go` the landing order the
  ledger implies; `load.go` queue wait per address; `stats.go` the scorecard.
- `internal/poc` — the sandbox generator, the runner (real `claude -p` builders, watcher,
  deterministic operator, tree-mode lead), the scorer and the comparison.

## Invariants

- **Nothing is reported.** A branch's state comes from its tip and its RESULT.json pin; a
  seat's liveness from hook events; who has context from what their branch changed. No verb
  accepts a status claim.
- **A landing pins its own head.** `landed` means the tip differs from `head_sha` by
  RESULT.json only; anything else is `pin_violation` or `pin_invalid`, never `landed`.
- **One effective ruling per scope, by total order.** Same tier must `--supersedes`; higher
  tier overrides and the ledger records what it replaced; lower tier is `outranked`.
- **Claims fence.** A ruling under a stale epoch is `claim_fenced`; a live claim by another
  holder is `claimed_by_other`. Expired claims change hands with a new epoch.
- **Addresses are seats.** A seat is a branch, or `SWARM_SEAT`. Session ids and titles never
  route anything.
- **A wake never collides with a turn.** A seat is wakeable only after its hook recorded Stop
  or SessionEnd; otherwise notes wait for the hook to inject them.
- **The ledger is a chain.** `Decisions()` fails on any rewritten line.

## Exit codes

0 ok · 1 refused (`refused <code>: <why>` on stdout, and the refusal is an event) · 2 `wait`
timed out · 3 usage or error. Refusal codes are listed in `README.md`.

## Checks

```
gofmt -l ./cmd/swarm && go vet ./cmd/swarm/...
go test ./cmd/swarm/...
GOOS=windows go build -o /dev/null ./cmd/swarm
```

`internal/swarm` tests build hermetic repositories (unsigned, hook-free commits) and exercise
pin states, contention, fencing, tiebreak, escalation, ledger tampering, leases, admission,
watch alerts, hook delivery, affinity routing and turn-boundary recording. The poc runner is
exercised by `swarm poc run --only t1-config-timeout` against a fresh `swarm poc init`.
