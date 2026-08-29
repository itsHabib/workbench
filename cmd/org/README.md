# org

The Baton home: role continuity chains for agent sessions. A **role** is a
durable office (`lead:agentic-development`) with an append-only hash chain; a
**session** is a disposable incarnation that attaches to it, acts, and leaves
a record. The next session starts where the last one stopped, and two
sessions cannot silently reach different conclusions about the same thing —
the chain's compare-and-swap refuses the second writer.

The kernel (record spine, state machine, admission laws, the fold) is
[`contracts/org`](../../contracts/org); this binary is its runtime.

## Quickstart

```sh
go install ./cmd/org

# the operator charters a role once
org charter -role lead:agentic-development \
  -scope dossier:org -scope github:itsHabib/workbench \
  -tier T2 -supervisor human:mh -cycle-ceiling 3 \
  -retire-when "org loop merged into steward"

# new work arrives: ask where it belongs before anything is written
org intake  -work github:itsHabib/workbench#88

# a session becomes the incarnation, works, and leaves a record
org attach  -role lead:agentic-development -next-due 4h
org assign  -role lead:agentic-development -work dossier:org/p1/t3 -pin "task body"
org claim   -role lead:agentic-development -work dossier:org/p1/t3
org yield   -role lead:agentic-development -work dossier:org/p1/t3 -body "where I stopped"
org checkpoint -role lead:agentic-development -body "SESSION END: …"

# the next session reads the index the last one left
org boot    -role lead:agentic-development
org status
```

`org boot` is the re-entry surface: a byte-capped index (default 2048) of the
role's charter, held work, obligations, liveness, and the last incarnation's
final word — pointers with hooks, not a context dump. Depth is read lazily
(`org blob <digest>`, `org log`).

Refusals are the substrate working: claim work you don't hold → exit 1,
`work_not_held`. A supervisor `takeover` mid-claim leaves a **dangling
obligation** the successor must discharge before claiming anything — silent
disappearance of work is not representable.

## Harness wiring

Two hooks close the loop for Claude Code sessions (both fail-open):

- `hooks/sessionstart-boot.sh` — injects `org boot` into a fresh session when
  its cwd maps to a role in `$ORG_STATE/roles.map`
  (`<path-prefix> <tenant> <role>`, longest prefix wins).
- `hooks/stop-mark.sh` — appends a mechanical `mark` when a session stops;
  the next boot renders `degraded` until someone distills a checkpoint.

Install snippets are in each script's header. State lives at `$ORG_STATE`
(default `~/dev/org/state`).

## Exit codes

`0` ok · `1` the kernel refused the record (stderr names the reason) ·
`2` usage · `4` error.
