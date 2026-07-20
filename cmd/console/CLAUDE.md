# console

A local, read-only web view of gate's inbox — the runs parked for judgment and
the grant ledger — with a click-through to any run's decision trace. It is a
pure renderer over gate's own JSON: it shells the `gate` binary (`gate next
-json`, `gate explain -json`, `gate audit`) and never reads gate's state files
or imports its decision code.

A workbench tenant: the binary lives at `cmd/console`, its guts under
`cmd/console/internal/`. Read `docs/DESIGN.md` first — it defines the boundary
(gate owns the projection; the console renders it), the security posture, and
what is deliberately out of this version.

## Develop (from the module root)

```
go build -o console.exe ./cmd/console
go vet ./cmd/console/...
golangci-lint run ./cmd/console/...
go test ./cmd/console/...
```

Run it against a gate state dir:

```
export GATE_STATE=~/pers/gate/state GATE_KEY=~/.config/gate   # gate reads these
console serve                                                 # http://127.0.0.1:7788
```

`serve` shells the `gate` binary on PATH (override with `-gate`), passing
through `-state` (default `$GATE_STATE`). The console inherits the environment,
so `$GATE_KEY` reaches gate for the `audit` chain check.

## Constraints that are design decisions, not omissions

- **The console shells gate; it never imports or reads gate.** Its only data
  source is `internal/gatecli`, which runs the gate binary and returns gate's
  JSON verbatim. No `cmd/gate` import, no `log.jsonl` parsing — gate owns the
  projection (`gate next -json`), so the console cannot drift from a schema it
  does not parse. This is the workbench boundary law, enforced by CI's
  `hygiene` job.
- **Loopback only, no auth.** `serve` refuses any non-loopback bind, pins the
  `Host` header to its own address (DNS-rebinding guard), and ships a strict CSP
  on the one embedded, self-contained page. It is an on-machine instrument, not
  a service.
- **Read-only: it renders, it never decides.** There are NO mutating routes in
  this version — judging and minting stay in the CLI. The docket shows each
  parked run's paste-ready `gate judge` command (copy button); the operator runs
  it. Adding action endpoints (a judgment form, a mint desk) is a later,
  deliberate phase, not a gap to fill casually.
- **Stdlib only in production.** No third-party production dependencies; the UI
  is one inlined, dependency-free HTML page served from `//go:embed`.
