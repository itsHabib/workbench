# console

A local, read-only web view of gate's inbox: one embedded page that shows the
runs parked for judgment and the grant ledger, plus a click-through to any
run's decision trace. It is a pure renderer over gate's own JSON — the single
`serve` verb shells the `gate` binary (`gate next -json -live`, `gate explain
-run <id> -json`, `gate audit`) and hands the bytes to the browser. The docket
prints each parked run's paste-ready `gate judge` / `gate explain` command with
a copy button; the operator runs it. The console itself has no mutating routes.

A workbench tenant: the binary lives at `cmd/console`, its guts under
`cmd/console/internal/` (`gatecli` — the exec seam; `web` — the loopback
HTTP surface and the `//go:embed` page). The load-bearing seam is that
shell-out: console never imports `cmd/gate` and never reads `log.jsonl`. That
is the workbench boundary law — tools compose through artifacts, not call
stacks — and CI's `hygiene` job enforces it.

## Run it

```
go build -o console.exe ./cmd/console
export GATE_STATE=~/dev/gate/state GATE_KEY=~/.config/gate  # gate reads these; console inherits the env
./console.exe serve                                         # http://127.0.0.1:7788
./console.exe serve -addr 127.0.0.1:9000                    # any loopback addr; non-loopback is refused
./console.exe serve -state ~/dev/gate/state                 # gate state dir, passed through; defaults to $GATE_STATE
./console.exe serve -gate ./gate.exe                        # path to the gate binary; defaults to "gate" on PATH
```

`-addr` accepts `127.0.0.1:0`; the bound port is printed once the listener is
up. `$GATE_KEY` is never read by the console — it reaches gate through the
inherited environment, which is what lets `gate audit` check the chain.

## Exit codes

| code | meaning |
|---|---|
| 0 | served until interrupted (Ctrl-C), then shut down cleanly |
| 1 | serve failed (non-loopback bind refused, listen error) |
| 2 | usage — no verb, or a verb other than `serve` |

There is no per-request exit code: the console is a long-running server, not a
gating step. A failed gate invocation surfaces in-band as HTTP 502 with a JSON
`{"error": ...}` body — a gate that errored is an upstream failure, not the
console's.

## How it works

Routes (all GET, all registered in `internal/web/server.go`): `/` and
`/run/{id}` serve the same embedded page — its script picks the view from the
path — while `/api/next`, `/api/run/{id}`, `/api/audit`, and `/api/config` feed
it. `/api/next` and `/api/run/{id}` forward gate's JSON verbatim; `/api/audit`
maps gate's run to `{ok, reason}` (a non-zero exit whose stdout carries a
`TAMPERED` line is a *finding*, rendered as a banner, not an error); the
page turns it into the docket and the trace view. `/api/config` reports only how
this console was launched — the state dir — so the printed commands can carry
`-state`. Before dispatch every request gets `nosniff` and has its `Host`
header checked against the serve address; the app page additionally gets a
strict CSP and `Cache-Control: no-store`. A run id must match
`^run_[0-9a-f]+$` before it is ever forwarded to a subprocess.

## Constraints that are design decisions, not omissions

- **It shells gate; it never imports or reads gate.** `internal/gatecli` is the
  only data source. Because the console does not parse gate's projection, it
  cannot drift from it.
- **Read-only. There are no mutating routes.** Judging and minting stay in the
  CLI; the console shows the command. A judgment form and a mint desk are a
  later, deliberate phase — they carry effectful state changes and land with
  their own CSRF/confirm plane, per `docs/DESIGN.md`.
- **Loopback only, no auth.** `serve` refuses any non-loopback bind outright,
  pins the `Host` header to its own address (DNS-rebinding guard), and serves
  one self-contained page under `default-src 'self'`. Single operator, one
  machine — there is no authentication and none is planned for this version.
- **No state of its own.** Nothing is persisted or cached; every byte on screen
  is a projection gate produced on that request.
- **Stdlib only in production.** No third-party Go dependencies; the UI is one
  inlined, dependency-free HTML page served from `//go:embed`.

## Status

The read-only version is built and tested: `internal/gatecli` and the server
are covered by `go test` (`httptest`), and `web/ui_contract_test.go` pins the
markup↔script seam (every `/api/...` path the page fetches resolves to a real
route; the elements the script binds to exist). A Playwright suite under
`e2e/` drives the real binary against committed gate-state fixtures — docket,
audit intact vs. tampered, empty inbox, security posture — but it is **not yet
wired into CI**; it runs locally (`cd cmd/console/e2e && npm install && npx
playwright test`). Phases 2–4 of `docs/DESIGN.md` (judge, mint, broader ledger
browsing) are planned, not built.

`docs/DESIGN.md` is the charter — the boundary, the security posture, and what
is deliberately out of this version. `CLAUDE.md` carries the scoped agent
guidance and the UI test tiers; `e2e/README.md` documents the fixtures.
